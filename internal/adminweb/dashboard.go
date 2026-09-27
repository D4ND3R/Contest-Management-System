package adminweb

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/ranking"
	"github.com/D4ND3R/Contest-Management-System/internal/scoring"
	"github.com/D4ND3R/Contest-Management-System/internal/webkit"
)

// contestDash is the dashboard of a contest (also the admin home page,
// for the contest the pages default to).
type contestDash struct {
	C        sqlc.Contest
	Counts   sqlc.AdminContestCountsRow
	Teams    int64
	Duration time.Duration
	Live     *dashLive
	// Home: shown as the admin home page, with the other contests and the
	// system errors under it.
	Home     bool
	Contests []contestListItem
	Errors   []sqlc.AdminListSystemErrorsRow
	Status   *systemStatus
}

// dashLive is the part of the dashboard that refreshes by itself.
type dashLive struct {
	C         sqlc.Contest
	ICPC      bool
	Precision int
	Board     []dashRow
	Ranked    int
	Tasks     []dashTask
	// Solved, Partial and Unsolved count the tasks (by anybody).
	Solved, Partial, Unsolved int
	Events                    []dashEvent
	Stats                     quickStats
	Health                    []healthItem
	Notes                     []dashEvent
	Updated                   time.Time
	loc                       *time.Location
}

type dashRow struct {
	Rank                int
	ParticipationID     int64
	Name, Username, Sub string
	Total               float64
	Solved, Penalty     int
	Hidden              bool
}

type dashTask struct {
	Index                int
	ID                   int64
	Name, Title          string
	Solvers, Partial     int
	Submissions, Pending int64
	FirstSolver          string
	FirstSolveSubmission int64
	// Closed: submissions to the task are closed (emergency control).
	Closed bool
}

// State is solved (by someone), partial or unsolved.
func (t dashTask) State() string {
	switch {
	case t.Solvers > 0:
		return "solved"
	case t.Partial > 0:
		return "partial"
	}
	return "unsolved"
}

// dashEvent is a line of the events feed or of the notifications.
type dashEvent struct {
	At             time.Time
	Class          string
	Text, Sub, URL string
}

type quickStats struct {
	Total, Accepted, Wrong, Time, Memory, Runtime, Compile, Other, Pending int64
}

type healthItem struct {
	Name, Value, Class string
}

// Time formats an instant in the contest's time zone.
func (l *dashLive) Time(t time.Time) string { return t.In(l.loc).Format("15:04") }

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	if rc.admin.Role == "leader" {
		webkit.Redirect(w, r, "/delegation")
		return
	}
	list, err := s.q.ListContests(r.Context())
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	d := &contestDash{Home: true, Status: s.systemStatus(r, rc)}
	now := s.now()
	counts, err := s.q.AdminContestCounts(r.Context())
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	byID := map[int64]sqlc.AdminContestCountsRow{}
	for _, c := range counts {
		byID[c.ID] = c
	}
	for _, c := range list {
		// Current and upcoming contests; old ones are on /contests.
		if c.StopTime.Before(now.Add(-7 * 24 * time.Hour)) {
			continue
		}
		d.Contests = append(d.Contests, contestListItem{Contest: c, Participations: byID[c.ID].Participations, Tasks: byID[c.ID].Tasks,
			Submissions: byID[c.ID].Submissions, Phase: contestPhase(c, now)})
	}
	if d.Errors, err = s.q.AdminListSystemErrors(r.Context()); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	if c := focusContest(list, now); c != nil {
		rc.contest = c
		if err := s.fillDash(r, rc, d, *c); err != nil {
			s.internalError(w, r, rc, err)
			return
		}
	}
	s.render(w, "dashboard", http.StatusOK, s.newPage(w, r, rc, "Dashboard", "home", d))
}

func (s *Server) handleContestDashboard(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	d := &contestDash{}
	if err := s.fillDash(r, rc, d, c); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	s.render(w, "dashboard", http.StatusOK, s.newPage(w, r, rc, c.Name, "contests", d).crumb("Contests", "/contests"))
}

// handleContestLive refreshes the live part of a dashboard (htmx).
func (s *Server) handleContestLive(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	live, err := s.dashLive(r, rc, c)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	p := s.newPage(w, r, rc, "", "", nil)
	s.renderPartial(w, "dash-live", wrap{P: p, V: live})
}

func (s *Server) fillDash(r *http.Request, rc *reqCtx, d *contestDash, c sqlc.Contest) error {
	ctx := r.Context()
	d.C = c
	counts, err := s.q.AdminContestCounts(ctx)
	if err != nil {
		return err
	}
	for _, row := range counts {
		if row.ID == c.ID {
			d.Counts = row
		}
	}
	if d.Teams, err = s.q.AdminContestTeams(ctx, c.ID); err != nil {
		return err
	}
	d.Duration = c.StopTime.Sub(c.StartTime)
	if c.PerUserTimeS != nil {
		d.Duration = time.Duration(*c.PerUserTimeS) * time.Second
	}
	d.Live, err = s.dashLive(r, rc, c)
	return err
}

// dashLive gathers the live panels: a handful of aggregate queries and one
// ranking computation, for the few administrators looking.
func (s *Server) dashLive(r *http.Request, rc *reqCtx, c sqlc.Contest) (*dashLive, error) {
	ctx := r.Context()
	now := s.now()
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		loc = time.UTC
	}
	l := &dashLive{C: c, ICPC: c.ScoringMode == "icpc", Precision: int(c.ScorePrecision), Updated: now, loc: loc}
	tr := adminTr(r)

	rk, err := ranking.Compute(ctx, s.q, c.ID, ranking.Options{})
	if err != nil {
		return nil, err
	}
	l.Ranked = len(rk.Rows)
	for i, row := range rk.Rows {
		if i >= 10 {
			break
		}
		name := strings.TrimSpace(row.FirstName + " " + row.LastName)
		if name == "" {
			name = row.Username
		}
		sub := row.Institution
		if row.TeamName != "" {
			sub = row.TeamName
		}
		if sub == "" {
			sub = row.Username
		}
		l.Board = append(l.Board, dashRow{Rank: row.Place, ParticipationID: row.ParticipationID, Name: name, Username: row.Username,
			Sub: sub, Total: row.Total, Solved: row.Solved, Penalty: row.Penalty, Hidden: row.Hidden})
	}

	stats, err := s.q.AdminTaskSubmissionStats(ctx, &c.ID)
	if err != nil {
		return nil, err
	}
	statByTask := map[int64]sqlc.AdminTaskSubmissionStatsRow{}
	for _, st := range stats {
		statByTask[st.TaskID] = st
		l.Stats.Pending += st.Pending
	}
	firsts, err := s.q.AdminTaskFirstAccepted(ctx, &c.ID)
	if err != nil {
		return nil, err
	}
	firstByTask := map[int64]sqlc.AdminTaskFirstAcceptedRow{}
	for _, f := range firsts {
		firstByTask[f.TaskID] = f
	}
	closed := map[int64]bool{}
	if ts, err := s.q.ListTasksByContest(ctx, &c.ID); err == nil {
		for _, t := range ts {
			closed[t.ID] = t.SubmissionsClosed
		}
	}
	for i, t := range rk.Tasks {
		dt := dashTask{Index: i, ID: t.ID, Name: t.Name, Title: t.Title, Closed: closed[t.ID]}
		for _, row := range rk.Rows {
			if i >= len(row.Cells) {
				continue
			}
			cell := row.Cells[i]
			switch {
			case cell.Solved || !l.ICPC && t.MaxScore > 0 && cell.Score >= t.MaxScore-1e-9:
				dt.Solvers++
			case cell.Score > 0:
				dt.Partial++
			}
		}
		st := statByTask[t.ID]
		dt.Submissions, dt.Pending = st.Submissions, st.Pending
		if f, ok := firstByTask[t.ID]; ok {
			dt.FirstSolver, dt.FirstSolveSubmission = f.Username, f.SubmissionID
		}
		switch dt.State() {
		case "solved":
			l.Solved++
		case "partial":
			l.Partial++
		default:
			l.Unsolved++
		}
		l.Tasks = append(l.Tasks, dt)
	}

	verdicts, err := s.q.AdminTaskSubmissionVerdicts(ctx, &c.ID)
	if err != nil {
		return nil, err
	}
	for _, v := range verdicts {
		switch v.Verdict {
		case scoring.VerdictAccepted:
			l.Stats.Accepted += v.N
		case scoring.VerdictWrong:
			l.Stats.Wrong += v.N
		case scoring.VerdictTime:
			l.Stats.Time += v.N
		case scoring.VerdictMemory:
			l.Stats.Memory += v.N
		case scoring.VerdictRuntime:
			l.Stats.Runtime += v.N
		case "CE":
			l.Stats.Compile += v.N
		default:
			l.Stats.Other += v.N
		}
		l.Stats.Total += v.N
	}
	l.Stats.Total += l.Stats.Pending

	if err := s.dashEvents(r, l, c, firsts, tr); err != nil {
		return nil, err
	}
	if c.AppealsUntil != nil {
		if n, err := s.q.CountOpenAppeals(r.Context(), c.ID); err == nil && n > 0 {
			l.Notes = append(l.Notes, dashEvent{At: now, Class: "warn", URL: "/contests/" + strconv.FormatInt(c.ID, 10) + "/appeals?status=open",
				Text: tr("%d appeals waiting for an answer", n)})
		}
	}
	if c.SubmissionsPaused {
		l.Notes = append([]dashEvent{{At: now, Class: "bad", URL: "/contests/" + strconv.FormatInt(c.ID, 10) + "/settings#emergency",
			Text: tr("Submissions are paused"), Sub: c.PauseMessage}}, l.Notes...)
	}
	s.dashHealth(r, rc, l, c, now)
	return l, nil
}

// dashEvents merges the latest submissions, first solves, questions and
// announcements, newest first.
func (s *Server) dashEvents(r *http.Request, l *dashLive, c sqlc.Contest, firsts []sqlc.AdminTaskFirstAcceptedRow, tr func(string, ...any) string) error {
	ctx := r.Context()
	subs, err := s.q.AdminContestRecentSubmissions(ctx, sqlc.AdminContestRecentSubmissionsParams{ContestID: c.ID, Lim: 10})
	if err != nil {
		return err
	}
	var ev []dashEvent
	for _, sub := range subs {
		e := dashEvent{At: sub.SubmittedAt, Class: "info", URL: "/submissions/" + strconv.FormatInt(sub.ID, 10),
			Text: tr("New submission from %s", sub.Username)}
		switch {
		case sub.SystemError != nil:
			e.Class, e.Sub = "warn", tr("%s: evaluation failed", sub.TaskName)
		case sub.CompilationOutcome != nil && *sub.CompilationOutcome == "fail":
			e.Class, e.Sub = "bad", tr("%s: compilation failed", sub.TaskName)
		case sub.ScoredAt == nil:
			e.Sub = tr("%s: being evaluated", sub.TaskName)
		case sub.Verdict != nil && *sub.Verdict == scoring.VerdictAccepted:
			e.Class, e.Sub = "ok", tr("%s: accepted", sub.TaskName)
		default:
			v := ""
			if sub.Verdict != nil {
				v = *sub.Verdict
			}
			score := ""
			if sub.Score != nil && c.ScoringMode != "icpc" {
				score = ranking.FormatScore(*sub.Score, int(c.ScorePrecision)) + " "
			}
			e.Class, e.Sub = "bad", strings.TrimSpace(sub.TaskName+": "+score+v)
			if sub.Score != nil && *sub.Score > 0 {
				e.Class = "warn"
			}
		}
		ev = append(ev, e)
	}
	names := map[int64]string{}
	for _, t := range l.Tasks {
		names[t.ID] = t.Name
	}
	for _, f := range firsts {
		ev = append(ev, dashEvent{At: f.SubmittedAt, Class: "purple", URL: "/submissions/" + strconv.FormatInt(f.SubmissionID, 10),
			Text: tr("%s solved %s", f.Username, names[f.TaskID]), Sub: tr("First to solve")})
	}
	qs, err := s.q.AdminContestRecentQuestions(ctx, sqlc.AdminContestRecentQuestionsParams{ContestID: c.ID, Lim: 5})
	if err != nil {
		return err
	}
	for _, q := range qs {
		e := dashEvent{At: q.AskedAt, Class: "info", URL: "/questions?contest=" + strconv.FormatInt(c.ID, 10),
			Text: tr("Question from %s", q.Username), Sub: q.Subject}
		if q.ReplyAt == nil && !q.Ignored {
			e.Class = "warn"
			l.Notes = append(l.Notes, dashEvent{At: q.AskedAt, Class: "warn", URL: e.URL, Text: tr("Unanswered question from %s", q.Username), Sub: q.Subject})
		}
		ev = append(ev, e)
	}
	anns, err := s.q.ListAnnouncements(ctx, c.ID)
	if err != nil {
		return err
	}
	for i, a := range anns {
		if i == 3 {
			break
		}
		ev = append(ev, dashEvent{At: a.CreatedAt, Class: "bad", URL: "/contests/" + strconv.FormatInt(c.ID, 10) + "/communication",
			Text: tr("Announcement"), Sub: a.Subject})
	}
	sort.SliceStable(ev, func(i, j int) bool { return ev[i].At.After(ev[j].At) })
	l.Events = ev[:min(len(ev), 8)]
	return nil
}

// dashHealth summarizes the judges, queues, database, disk and backups,
// and adds the notifications that need someone.
func (s *Server) dashHealth(r *http.Request, rc *reqCtx, l *dashLive, c sqlc.Contest, now time.Time) {
	ctx := r.Context()
	tr := adminTr(r)
	st := s.systemStatus(r, rc)
	judges := healthItem{Name: tr("Judges"), Value: tr("%d online, %d/%d slots busy", st.Alive, st.Busy, st.Slots), Class: "ok"}
	unfiltered := 0
	for _, w := range st.Workers {
		if w.Alive && !w.Seccomp {
			unfiltered++
		}
	}
	switch {
	case st.Alive == 0:
		judges.Value, judges.Class = tr("offline"), "bad"
	case unfiltered > 0:
		judges.Class = "warn"
		l.Notes = append(l.Notes, dashEvent{At: now, Class: "warn", URL: "/system",
			Text: tr("%d judges run without the seccomp filter", unfiltered)})
	}
	var waiting int64
	for _, p := range st.Priorities {
		waiting += st.Queues.Waiting[p]
	}
	queue := healthItem{Name: tr("Queue"), Value: tr("%d waiting", waiting), Class: "ok"}
	if st.QueueError != "" {
		queue.Value, queue.Class = tr("unreachable"), "bad"
	} else if waiting > int64(10*max(st.Slots, 1)) {
		queue.Class = "warn"
	}
	dbItem := healthItem{Name: tr("Database"), Value: tr("online"), Class: "ok"}
	if st.Storage != nil {
		dbItem.Value = tr("online, %s", humanBytes(st.Storage.DbBytes))
	}
	l.Health = append(l.Health, judges, queue, dbItem)
	for _, d := range st.Host.Disks {
		it := healthItem{Name: tr("Disk (%s)", tr(d.Name)), Value: tr("%s free", humanBytes(d.Free)), Class: "ok"}
		if d.UsedPercent() > 90 {
			it.Class = "bad"
		} else if d.UsedPercent() > 80 {
			it.Class = "warn"
		}
		l.Health = append(l.Health, it)
	}
	if s.backups != nil {
		it := healthItem{Name: tr("Backups"), Value: tr("none yet"), Class: "warn"}
		var last time.Time
		if list, err := s.backups.List(); err == nil {
			for _, e := range list {
				if e.Status == "done" && e.Finished.After(last) {
					last = e.Finished
				}
			}
		}
		if !last.IsZero() {
			it.Value, it.Class = tr("last %s ago", humanAgo(now.Sub(last))), "ok"
			if now.Sub(last) > 24*time.Hour {
				it.Class = "warn"
			}
		}
		l.Health = append(l.Health, it)
	}
	if st.Stuck > 0 {
		l.Notes = append(l.Notes, dashEvent{At: now, Class: "bad", URL: "/system", Text: tr("%d jobs look stuck", st.Stuck)})
	}
	if errs, err := s.q.AdminListSystemErrors(ctx); err == nil {
		n := 0
		for _, e := range errs {
			if e.ContestID == c.ID {
				n++
			}
		}
		if n > 0 {
			l.Notes = append(l.Notes, dashEvent{At: now, Class: "bad", URL: "/", Text: tr("%d submissions could not be judged", n)})
		}
	}
	if n, err := s.q.AdminContestFlagged(ctx, c.ID); err == nil && n > 0 {
		l.Notes = append(l.Notes, dashEvent{At: now, Class: "warn",
			URL: "/contests/" + strconv.FormatInt(c.ID, 10) + "/submissions?status=flagged", Text: tr("%d suspicious submissions", n)})
	}
	if c.Registration == "approval" {
		if n, err := s.q.CountPendingRegistrations(ctx, c.ID); err == nil && n > 0 {
			l.Notes = append(l.Notes, dashEvent{At: now, Class: "info", URL: "/contests/" + strconv.FormatInt(c.ID, 10) + "/participations",
				Text: tr("%d registrations waiting for approval", n)})
		}
	}
	if left := c.StopTime.Sub(now); left > 0 && left <= time.Hour && !now.Before(c.StartTime) {
		l.Notes = append(l.Notes, dashEvent{At: now, Class: "warn", Text: tr("The contest ends in %s", humanAgo(left))})
	}
	if ranking.Frozen(c, now) {
		l.Notes = append(l.Notes, dashEvent{At: now, Class: "info", URL: "/contests/" + strconv.FormatInt(c.ID, 10) + "/ranking",
			Text: tr("The public ranking is frozen")})
	}
}

// humanBytes writes a size with a binary unit.
func humanBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return strconv.FormatFloat(float64(b)/(1<<30), 'f', 1, 64) + " GiB"
	case b >= 1<<20:
		return strconv.FormatFloat(float64(b)/(1<<20), 'f', 1, 64) + " MiB"
	case b >= 1<<10:
		return strconv.FormatInt(b>>10, 10) + " KiB"
	}
	return strconv.FormatInt(b, 10) + " B"
}

// humanAgo writes a duration roughly ("3 min", "2 h", "5 d").
func humanAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d/time.Second)) + " s"
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + " min"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + " h"
	}
	return strconv.Itoa(int(d/(24*time.Hour))) + " d"
}
