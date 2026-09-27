package contestweb

import (
	"net/http"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/contest"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/ranking"
)

// ---------------------------------------------------------------- overview

// overviewRow is a task on the contestant's dashboard.
type overviewRow struct {
	Index       int
	Name, Title string
	HasScore    bool
	Score, Max  float64
	Precision   int
	Pending     bool
	// ICPC contests: solved, and the rejected attempts (before solving).
	Solved   bool
	Attempts int32
	// Submissions made to the task (by the contestant or the team).
	Submissions int64
	// Adjustment by the organizers (included in Score), with the reasons.
	Adjustment float64
	Reasons    string
}

// State is solved, partial, tried or none (the status icon).
func (r overviewRow) State() string {
	switch {
	case r.Solved || r.HasScore && r.Max > 0 && r.Score >= r.Max-1e-9:
		return "solved"
	case r.HasScore && r.Score > 0:
		return "partial"
	case r.Submissions > 0:
		return "tried"
	}
	return "none"
}

// recentSub is one of the contestant's latest submissions.
type recentSub struct {
	subView
	Task  *taskView
	Index int
}

type overviewData struct {
	Rows            []overviewRow
	PerUserTime     time.Duration
	ShowTotal       bool
	Total, MaxTotal float64
	// Hidden: the contest does not show scores now.
	Hidden bool
	// Certificate: the contestant may download a certificate.
	Certificate bool
	// Solved, Partial, Unsolved count the tasks (the donut).
	Solved, Partial, Unsolved int
	Recent                    []recentSub
	Announcements             []sqlc.Announcement
	// Board is the top of the ranking (when contestants may see it);
	// Rank is the contestant's place in it, of Ranked.
	Board          []ranking.BoardRow
	BoardICPC      bool
	BoardPrecision int
	Rank, Ranked   int
	Mine           string
	// Duration of the contestant's window.
	Duration time.Duration
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	ctx := r.Context()
	p := s.newPage(rc, rc.contest.Name, "overview")
	p.Title = p.T("Overview")
	d := &overviewData{PerUserTime: rc.contest.Rules.PerUserTime}
	if !rc.status.Begin.IsZero() && !rc.status.End.IsZero() {
		d.Duration = rc.status.End.Sub(rc.status.Begin)
	}
	if d.PerUserTime > 0 {
		d.Duration = d.PerUserTime
	}
	scores, err := s.q.ListScoresByParticipations(ctx, rc.group)
	if err != nil {
		s.fail(w, err)
		return
	}
	counts, err := s.q.CountSubmissionsByTask(ctx, rc.group)
	if err != nil {
		s.fail(w, err)
		return
	}
	subsByTask := make(map[int64]int64, len(counts))
	for _, c := range counts {
		subsByTask[c.TaskID] = c.N
	}
	byTask := mergeScores(scores, rc.contest.TaskByID)
	if !scoresVisible(rc) {
		byTask = nil
		d.Hidden = true
	}
	for i, t := range p.Tasks {
		row := overviewRow{Index: i, Name: t.Name, Title: t.Title, Max: t.MaxScore, Precision: t.Precision, Submissions: subsByTask[t.ID]}
		if sc, ok := byTask[t.ID]; ok {
			row.HasScore, row.Score, row.Pending = true, sc.score, sc.pending > 0
			row.Solved, row.Attempts, row.Adjustment = sc.solved, sc.attempts, sc.adjustment
		}
		switch row.State() {
		case "solved":
			d.Solved++
		case "partial":
			d.Partial++
		default:
			d.Unsolved++
		}
		d.Total += row.Score
		d.MaxTotal += row.Max
		d.Rows = append(d.Rows, row)
	}
	d.ShowTotal = len(d.Rows) > 1 && !d.Hidden && !rc.contest.ICPC()
	// Only once the contestant's time is over (no query before).
	cert, err := s.certificateTemplate(ctx, rc)
	if err != nil {
		s.fail(w, err)
		return
	}
	d.Certificate = cert != nil
	if err := s.adjustmentReasons(r, rc, d.Rows); err != nil {
		s.fail(w, err)
		return
	}
	if len(p.Tasks) > 0 {
		if d.Recent, err = s.recentSubs(r, rc, p); err != nil {
			s.fail(w, err)
			return
		}
	}
	if anns, err := s.q.ListAnnouncements(ctx, rc.contest.ID); err == nil {
		d.Announcements = anns[:min(len(anns), 3)]
	}
	if p.Ranking {
		// The cached board (one computation per contest every few seconds).
		b, err := s.contestBoard(ctx, rc.contest, rc.now)
		if err != nil {
			s.fail(w, err)
			return
		}
		own := rc.contest.RankingContestantView == "own"
		d.BoardICPC, d.BoardPrecision, d.Ranked = b.ICPC, b.Precision, len(b.Rows)
		for _, row := range b.Rows {
			mine := false
			for _, m := range row.Members {
				mine = mine || m == rc.part.ID
			}
			if mine {
				d.Rank, d.Mine = row.Rank, row.Key
			}
			if (len(d.Board) < 8 && !own) || mine {
				d.Board = append(d.Board, row)
			}
		}
	}
	p.Data = d
	s.render(w, "overview", http.StatusOK, p)
}

// recentSubs loads the contestant's latest submissions to any task.
func (s *Server) recentSubs(r *http.Request, rc *reqCtx, p *page) ([]recentSub, error) {
	rows, err := s.q.ListRecentSubmissions(r.Context(), sqlc.ListRecentSubmissionsParams{ParticipationIds: rc.group, Lim: 6})
	if err != nil {
		return nil, err
	}
	index := make(map[int64]int, len(p.Tasks))
	for i, t := range p.Tasks {
		index[t.ID] = i
	}
	hidden := !scoresVisible(rc)
	out := make([]recentSub, 0, len(rows))
	for _, row := range rows {
		t := rc.contest.TaskByID[row.TaskID]
		if t == nil {
			continue
		}
		sv := subView{ID: row.ID, Time: row.SubmittedAt, Official: row.Official, Tokened: row.Tokened, Invalidated: row.InvalidatedAt}
		var done, total int32
		if row.TestcasesDone != nil {
			done = *row.TestcasesDone
		}
		if row.TestcasesTotal != nil {
			total = *row.TestcasesTotal
		}
		s.fillStatus(p, t, subState{compilation: row.CompilationOutcome, evaluation: row.EvaluationOutcome, done: done, total: total,
			score: row.Score, pub: row.PublicScore, scored: row.ScoredAt != nil, systemError: row.SystemError, tokened: row.Tokened,
			hidden: hidden, icpc: rc.contest.ICPC(), verdict: row.Verdict}, &sv)
		out = append(out, recentSub{subView: sv, Task: t, Index: index[t.ID]})
	}
	return out, nil
}

// waiting reports whether the contestant cannot see the tasks yet.
func (p *page) Waiting() bool {
	return p.Status.Phase == contest.NotStarted || p.Status.Phase == contest.WaitingStart
}

// handleBanner serves the contest's banner image (also on the login page,
// so without a session). The type was checked at upload; the browser is
// told not to guess another.
func (s *Server) handleBanner(w http.ResponseWriter, r *http.Request) {
	cv := contestOf(r)
	if cv.BannerDigest == nil || !bannerTypes[cv.BannerType] {
		http.NotFound(w, r)
		return
	}
	s.serveBlob(w, r, *cv.BannerDigest, "banner", cv.BannerType, false)
}

// bannerTypes are the image types a banner may have (never SVG).
var bannerTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}
