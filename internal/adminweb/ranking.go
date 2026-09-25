package adminweb

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/i18n"
	"github.com/D4ND3R/Contest-Management-System/internal/ranking"
	"github.com/D4ND3R/Contest-Management-System/internal/rankingpush"
)

func (s *Server) handleRanking(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	site, _ := strconv.ParseInt(r.URL.Query().Get("site"), 10, 64)
	rk, err := ranking.Compute(r.Context(), s.q, c.ID, ranking.Options{IncludeHidden: r.URL.Query().Get("hidden") == "1", SiteID: site})
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	sites, err := s.q.ListSites(r.Context(), c.ID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	d := rankingPage{Ranking: rk, Sites: sites, Site: site, C: c, FreezeAt: ranking.FreezeAt(c), Frozen: ranking.Frozen(c, s.now())}
	if s.rankingURL != "" && (c.RankingVisibility == "public" || c.RankingVisibility == "admins") {
		d.PublicURL = strings.TrimRight(s.rankingURL, "/") + "/" + c.Name + "/"
		if c.RankingVisibility == "admins" {
			d.PublicURL += "?key=" + rankingpush.BoardKey(s.secret, c.Name)
		}
	}
	s.render(w, "ranking", http.StatusOK, s.newPage(w, r, rc, "Ranking", "contests", d).
		crumb("Contests", "/contests").crumb(c.Name, "/contests/"+strconv.FormatInt(c.ID, 10)))
}

// rankingPage is the admin ranking (always complete and unfrozen) with the
// state of the public one.
type rankingPage struct {
	*ranking.Ranking
	Sites     []sqlc.Site
	Site      int64
	C         sqlc.Contest
	FreezeAt  *time.Time
	Frozen    bool
	PublicURL string
}

// handleRankingFreeze unfreezes the public ranking (or freezes it again).
func (s *Server) handleRankingFreeze(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	unfrozen := r.FormValue("unfrozen") == "1"
	if err := s.q.SetContestRankingUnfrozen(r.Context(), sqlc.SetContestRankingUnfrozenParams{ID: c.ID, RankingUnfrozen: unfrozen}); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("contest", c.ID)
	rc.note("unfrozen", unfrozen)
	s.contestChanged(r.Context(), c.ID, 0)
	msg := "The public ranking is frozen again."
	if unfrozen {
		msg = "The public ranking is unfrozen: every result is shown."
	}
	s.done(w, r, "/contests/"+strconv.FormatInt(c.ID, 10)+"/ranking", msg)
}

func (s *Server) exportRanking(w http.ResponseWriter, r *http.Request, rc *reqCtx, csv bool) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	site, _ := strconv.ParseInt(r.URL.Query().Get("site"), 10, 64)
	rk, err := ranking.Compute(r.Context(), s.q, c.ID, ranking.Options{IncludeHidden: r.URL.Query().Get("hidden") == "1", SiteID: site})
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	ext, ct := ".json", "application/json"
	if csv {
		ext, ct = ".csv", "text/csv; charset=utf-8"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", `attachment; filename="ranking-`+c.Name+ext+`"`)
	w.Header().Set("Cache-Control", "no-store")
	if csv {
		err = rk.WriteCSV(w)
	} else {
		err = rk.WriteJSON(w)
	}
	if err != nil {
		s.log.Warn("ranking export", "error", err)
	}
}

// handleRankingPDF is the printable final results (landscape table).
func (s *Server) handleRankingPDF(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	site, _ := strconv.ParseInt(r.URL.Query().Get("site"), 10, 64)
	rk, err := ranking.Compute(r.Context(), s.q, c.ID, ranking.Options{IncludeHidden: r.URL.Query().Get("hidden") == "1", SiteID: site})
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	lang := adminLang(r)
	t := func(msg string, args ...any) string { return i18n.T(lang, msg, args...) }
	title := c.Description
	if title == "" {
		title = c.Name
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="ranking-`+c.Name+`.pdf"`)
	w.Header().Set("Cache-Control", "no-store")
	if err := rk.WritePDF(w, ranking.PDFLabels{Title: t("Results: %s", title), Rank: "#", Contestant: t("Contestant"),
		Team: t("Team / institution"), Total: t("Total"), Solved: t("Solved"), Penalty: t("Penalty"), Page: t("page")}); err != nil {
		s.log.Warn("ranking pdf", "error", err)
	}
}

func (s *Server) handleRankingCSV(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.exportRanking(w, r, rc, true)
}

func (s *Server) handleRankingJSON(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.exportRanking(w, r, rc, false)
}

// taskStats summarises one task.
type taskStats struct {
	Task                                         ranking.Task
	Submissions, Participants, CompileFailed     int64
	Pending, Errors                              int64
	Attempted, Full, Partial, Zero, NotAttempted int
	Average                                      float64
	Histogram                                    []histBucket
	Verdicts                                     []verdictRow
	VerdictTotal                                 int64
	// Submission verdicts and the first accepted submission.
	SubVerdicts []subVerdict
	SubTotal    int64
	FirstAC     *firstAC
}

type subVerdict struct {
	Verdict string
	N       int64
}

type firstAC struct {
	SubmissionID    int64
	Username        string
	Time            time.Time
	ContestMinute   int
	ParticipationID int64
}

type histBucket struct {
	Label string
	Count int
	Pct   int
}

type verdictRow struct {
	Verdict          string
	N                int64
	AvgTime, MaxTime float64
	MaxMemory        int64
}

type statsPage struct {
	Contest      string
	ContestID    int64
	Participants int
	Tasks        []*taskStats
}

// handleStats shows per-task statistics: submission counters, the score
// distribution of the (non-hidden) contestants and testcase verdicts.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	rk, err := ranking.Compute(r.Context(), s.q, c.ID, ranking.Options{})
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	counters, err := s.q.AdminTaskSubmissionStats(r.Context(), &c.ID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	verdicts, err := s.q.AdminTaskVerdictStats(r.Context(), &c.ID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	d := &statsPage{Contest: c.Name, ContestID: c.ID, Participants: len(rk.Rows)}
	byID := map[int64]*taskStats{}
	for i, t := range rk.Tasks {
		ts := &taskStats{Task: t, Histogram: make([]histBucket, 10)}
		for b := range ts.Histogram {
			ts.Histogram[b].Label = strconv.Itoa(b*10) + "–" + strconv.Itoa((b+1)*10) + "%"
		}
		var sum float64
		for _, row := range rk.Rows {
			cell := row.Cells[i]
			if !cell.Submitted {
				ts.NotAttempted++
				continue
			}
			ts.Attempted++
			sum += cell.Score
			switch {
			case t.MaxScore > 0 && cell.Score >= t.MaxScore-1e-9:
				ts.Full++
			case cell.Score > 0:
				ts.Partial++
			default:
				ts.Zero++
			}
			if t.MaxScore > 0 {
				b := int(10 * cell.Score / t.MaxScore)
				ts.Histogram[min(max(b, 0), 9)].Count++
			}
		}
		if ts.Attempted > 0 {
			ts.Average = sum / float64(ts.Attempted)
			for b := range ts.Histogram {
				ts.Histogram[b].Pct = 100 * ts.Histogram[b].Count / ts.Attempted
			}
		}
		byID[t.ID] = ts
		d.Tasks = append(d.Tasks, ts)
	}
	for _, cn := range counters {
		if ts := byID[cn.TaskID]; ts != nil {
			ts.Submissions, ts.Participants, ts.CompileFailed = cn.Submissions, cn.Participants, cn.CompileFailed
			ts.Pending, ts.Errors = cn.Pending, cn.Errors
		}
	}
	for _, v := range verdicts {
		if ts := byID[v.TaskID]; ts != nil {
			ts.Verdicts = append(ts.Verdicts, verdictRow{v.Verdict, v.N, v.AvgTime, v.MaxTime, v.MaxMemory})
			ts.VerdictTotal += v.N
		}
	}
	subVerdicts, err := s.q.AdminTaskSubmissionVerdicts(r.Context(), &c.ID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	for _, v := range subVerdicts {
		if ts := byID[v.TaskID]; ts != nil {
			ts.SubVerdicts = append(ts.SubVerdicts, subVerdict{v.Verdict, v.N})
			ts.SubTotal += v.N
		}
	}
	firsts, err := s.q.AdminTaskFirstAccepted(r.Context(), &c.ID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	for _, f := range firsts {
		if ts := byID[f.TaskID]; ts != nil {
			ts.FirstAC = &firstAC{SubmissionID: f.SubmissionID, Username: f.Username, Time: f.SubmittedAt,
				ContestMinute: int(f.SubmittedAt.Sub(c.StartTime) / time.Minute), ParticipationID: f.ParticipationID}
		}
	}
	s.render(w, "stats", http.StatusOK, s.newPage(w, r, rc, "Statistics", "contests", d).
		crumb("Contests", "/contests").crumb(c.Name, "/contests/"+strconv.FormatInt(c.ID, 10)))
}
