package contestweb

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/checkers"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/scoring"
)

// subView is one submission as shown to its author.
type subView struct {
	ID         int64
	Time       time.Time
	Language   string
	langID     string
	Official   bool
	Tokened    bool
	Pending    bool
	Evaluating bool
	Done       int32
	Total      int32
	StatusText string
	Class      string
	// Verdict is the short code shown in a coloured box (AC, PA, WA, TLE,
	// CE...; "" while pending or when results are hidden) and VClass its
	// colour.
	Verdict, VClass string
	HasScore        bool
	Score, Max      float64
	Precision       int
	// Public: Score is the public score (tokens: the full one is revealed
	// by playing a token).
	Public bool
	// Queued: waiting for a worker, with Ahead submissions before it;
	// results take about Typical seconds now (0: unknown).
	Queued         bool
	Ahead, Typical int64
	// Author is the team member who submitted (team contests only).
	Author string
	// CanToken: a token can be played on it now.
	CanToken bool
	// Invalidated submissions stay visible but do not count.
	Invalidated       *time.Time
	InvalidatedReason string
}

// rowCtx gives the row template both the page and the submission.
type rowCtx struct {
	P *page
	S subView
}

// submission states shared by the list and the detail views.
type subState struct {
	compilation *string
	evaluation  *string
	done, total int32
	score, pub  *float64
	scored      bool
	systemError *string
	tokened     bool
	// hidden: the contest does not show scores now.
	hidden bool
	// icpc: show the verdict instead of the score.
	icpc    bool
	verdict *string
	// full: the contestant sees full scores (no tokens on the task).
	full bool
}

// verdictNames are the verdicts as contestants read them.
var verdictNames = map[string]string{
	scoring.VerdictAccepted:     "Accepted",
	scoring.VerdictPartial:      "Partially correct",
	scoring.VerdictWrong:        "Wrong answer",
	scoring.VerdictTime:         "Time limit exceeded",
	scoring.VerdictMemory:       "Memory limit exceeded",
	scoring.VerdictRuntime:      "Runtime error",
	scoring.VerdictOutputLimit:  "Output limit exceeded",
	scoring.VerdictSecurity:     "Security violation",
	scoring.VerdictCompileError: "Compilation failed",
	scoring.VerdictSkipped:      "Skipped",
}

// verdictName is the translatable name of a verdict code.
func verdictName(v string) string {
	if n, ok := verdictNames[v]; ok {
		return n
	}
	return "Rejected"
}

// fullScores reports whether contestants see full scores on task t: always,
// except with tokens, where a token reveals the full result of one
// submission (the public score shows until then).
func fullScores(rc *reqCtx, t *taskView) bool {
	return rc.contest.TokenMode == "disabled" || t.TokenMode == "disabled"
}

func (s *Server) fillStatus(p *page, t *taskView, st subState, sv *subView) {
	sv.Max, sv.Precision = t.MaxScore, t.Precision
	stored := ""
	if st.verdict != nil {
		stored = *st.verdict
	}
	switch {
	case st.systemError != nil:
		sv.StatusText, sv.Class = p.T("Evaluation failed (the organizers were notified)"), "warn"
	case st.compilation == nil:
		sv.StatusText, sv.Pending, sv.Class = p.T("Compiling…"), true, "pending"
	case *st.compilation == "fail":
		sv.Verdict = scoring.VerdictCompileError
		sv.StatusText, sv.Class = p.T("Compilation failed"), "bad"
	case !st.scored:
		sv.Pending, sv.Evaluating, sv.Done, sv.Total = true, true, st.done, st.total
		sv.StatusText, sv.Class = p.T("Evaluating"), "pending"
	case st.hidden:
		sv.StatusText = p.T("Evaluated")
	case st.icpc:
		v := stored
		if v == "" && st.score != nil && t.MaxScore > 0 && *st.score >= t.MaxScore-1e-9 {
			v = scoring.VerdictAccepted
		}
		if v == "" {
			v = scoring.VerdictWrong
		}
		sv.Verdict = v
		sv.StatusText, sv.Class = p.T(verdictName(v)), scoring.VerdictClass(v)
	default:
		sv.HasScore = true
		if st.full || st.tokened {
			if st.score != nil {
				sv.Score = *st.score
			}
			sv.Verdict = scoring.Verdict(stored, sv.Score, sv.Max)
			sv.StatusText, sv.Class = p.T(verdictName(sv.Verdict)), scoring.VerdictClass(sv.Verdict)
		} else {
			// Tokens: the public score until a token reveals the result.
			if st.pub != nil {
				sv.Score = *st.pub
			}
			sv.Public = true
			sv.StatusText = p.T("Evaluated")
		}
	}
	sv.VClass = scoring.VerdictClass(sv.Verdict)
}

// listSubs loads the contestant's submissions to a task (one query).
func (s *Server) listSubs(r *http.Request, rc *reqCtx, t *taskView) ([]subView, error) {
	if t.Dataset == nil {
		return nil, nil
	}
	rows, err := s.q.ListSubmissionsWithResults(r.Context(), sqlc.ListSubmissionsWithResultsParams{
		DatasetID: t.Dataset.ID, ParticipationIds: rc.group, TaskID: t.ID})
	if err != nil {
		return nil, err
	}
	p := &page{Lang: rc.lang}
	hidden := !scoresVisible(rc)
	tv, err := s.tokenView(r, rc, t)
	if err != nil {
		return nil, err
	}
	out := make([]subView, 0, len(rows))
	for _, row := range rows {
		sv := subView{ID: row.ID, Time: row.SubmittedAt, Official: row.Official, Tokened: row.Tokened,
			Invalidated: row.InvalidatedAt, InvalidatedReason: row.InvalidatedReason}
		if len(rc.group) > 1 {
			sv.Author = row.Author // team contests: who submitted it
		}
		if row.Language != nil {
			sv.langID = *row.Language
			sv.Language = s.langName(*row.Language)
		}
		var total int32
		if row.TestcasesTotal != nil {
			total = *row.TestcasesTotal
		}
		var done int32
		if row.TestcasesDone != nil {
			done = *row.TestcasesDone
		}
		s.fillStatus(p, t, subState{compilation: row.CompilationOutcome, evaluation: row.EvaluationOutcome, done: done, total: total,
			score: row.Score, pub: row.PublicScore, scored: row.ScoredAt != nil, systemError: row.SystemError, tokened: row.Tokened,
			hidden: hidden, icpc: rc.contest.ICPC(), verdict: row.Verdict, full: fullScores(rc, t)}, &sv)
		sv.CanToken = tv != nil && tv.CanPlay && !row.Tokened && row.Official && row.InvalidatedAt == nil && row.Author == rc.part.Username
		out = append(out, sv)
	}
	// Where the newest waiting submissions stand in the queue (a few at
	// most: the others are judged by then).
	for i := 0; i < len(out) && i < 3; i++ {
		if err := s.queueInfo(r, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// queueInfo fills the queue position of a submission waiting for a worker.
func (s *Server) queueInfo(r *http.Request, sv *subView) error {
	if !sv.Pending || sv.Evaluating || sv.Invalidated != nil {
		return nil
	}
	var err error
	sv.Queued = true
	if sv.Ahead, err = s.q.CountSubmissionsAhead(r.Context(), sv.ID); err != nil {
		return err
	}
	sv.Typical = s.typicalLatency(r)
	return nil
}

func (s *Server) langName(id string) string {
	if l, ok := s.langs.Get(id); ok {
		return l.Name
	}
	return id
}

func (s *Server) subViewFromDetail(p *page, rc *reqCtx, t *taskView, row sqlc.GetSubmissionWithResultRow) subView {
	sv := subView{ID: row.ID, Time: row.SubmittedAt, Official: row.Official, Tokened: row.Tokened,
		Invalidated: row.InvalidatedAt, InvalidatedReason: row.InvalidatedReason}
	if len(rc.group) > 1 {
		sv.Author = row.Author
	}
	if row.Language != nil {
		sv.langID, sv.Language = *row.Language, s.langName(*row.Language)
	}
	var done, total int32
	if row.TestcasesDone != nil {
		done = *row.TestcasesDone
	}
	if row.TestcasesTotal != nil {
		total = *row.TestcasesTotal
	}
	s.fillStatus(p, t, subState{compilation: row.CompilationOutcome, evaluation: row.EvaluationOutcome, done: done, total: total,
		score: row.Score, pub: row.PublicScore, scored: row.ScoredAt != nil, systemError: row.SystemError, tokened: row.Tokened,
		hidden: !scoresVisible(rc), icpc: rc.contest.ICPC(), verdict: row.Verdict, full: fullScores(rc, t)}, &sv)
	if !row.Tokened && row.Official && row.InvalidatedAt == nil && row.ParticipationID != nil && *row.ParticipationID == rc.part.ID {
		if tv, err := s.tokenView(nil, rc, t); err == nil && tv != nil && tv.CanPlay {
			sv.CanToken = true
		}
	}
	return sv
}

// tokenView loads the contestant's plays and computes the token view of a
// task.
func (s *Server) tokenView(r *http.Request, rc *reqCtx, t *taskView) (*tokenView, error) {
	if rc.contest.TokenMode == "disabled" || t.TokenMode == "disabled" {
		return nil, nil
	}
	played, err := s.q.ListTokenTimesByParticipation(rc.ctx, rc.part.ID)
	if err != nil {
		return nil, err
	}
	return s.tokens(rc, t, played), nil
}

// ---------------------------------------------------------------- details

type compilationView struct {
	OK             bool
	Text           string
	Stdout, Stderr string
	Time           *float64
	Memory         *int64
}

type detailRow struct {
	Codename string
	// Subtask is the subtask of the testcase (0: none).
	Subtask int
	Verdict string
	Text    string
	Class   string
	Time    float64
	Memory  int64
}

// block is a subtask's result: its points and verdict, coloured.
type block struct {
	Index      int
	Score, Max float64
	Verdict    string
	Class      string
}

type detailData struct {
	Sub         subView
	TaskName    string
	Files       []string
	Download    bool
	Compilation *compilationView
	// Blocks are the subtasks (group score types) and Rows the testcases
	// shown: the public ones, or every one once a token was played (All).
	Blocks        []block
	Rows          []detailRow
	All           bool
	Scored        bool
	ShowResources bool
}

func (s *Server) detailData(r *http.Request, p *page, rc *reqCtx, t *taskView, row sqlc.GetSubmissionWithResultRow) (*detailData, error) {
	d := &detailData{Sub: s.subViewFromDetail(p, rc, t, row), TaskName: t.Name, Download: rc.contest.SubmissionsDownloadAllowed}
	files, err := s.q.ListSubmissionFiles(r.Context(), row.ID)
	if err != nil {
		return nil, err
	}
	ext := ""
	if row.Language != nil {
		if l, ok := s.langs.Get(*row.Language); ok {
			ext = l.SourceExtension()
		}
	}
	for _, f := range files {
		d.Files = append(d.Files, replaceExt(f.Filename, ext))
	}
	if row.CompilationOutcome != nil {
		c := &compilationView{OK: *row.CompilationOutcome == "ok", Time: row.CompilationTime, Memory: row.CompilationMemory}
		if row.CompilationText != nil {
			c.Text = *row.CompilationText
		}
		if row.CompilationStdout != nil {
			c.Stdout = *row.CompilationStdout
		}
		if row.CompilationStderr != nil {
			c.Stderr = *row.CompilationStderr
		}
		if !rc.contest.ShowCompilationOutput {
			c.Stdout, c.Stderr = "", ""
		}
		if c.OK && c.Stdout == "" && c.Stderr == "" && t.TaskType == "OutputOnly" {
			c = nil
		}
		d.Compilation = c
	}
	if d.Sub.Queued = d.Sub.Pending && !d.Sub.Evaluating && d.Sub.Invalidated == nil; d.Sub.Queued {
		if d.Sub.Ahead, err = s.q.CountSubmissionsAhead(r.Context(), row.ID); err != nil {
			return nil, err
		}
		d.Sub.Typical = s.typicalLatency(r)
	}
	if row.ScoredAt == nil || !scoresVisible(rc) || rc.contest.ICPC() {
		// ICPC contests show the verdict only.
		return d, nil
	}
	d.Scored = true
	// Every subtask's score, and the public testcases' runs; a token
	// reveals every testcase (and, with tokens, the full score).
	raw := row.PublicScoreDetails
	d.All = row.Tokened
	if fullScores(rc, t) || row.Tokened {
		raw = row.ScoreDetails
	}
	var det scoring.Details
	if len(raw) == 0 || json.Unmarshal(raw, &det) != nil {
		return d, nil
	}
	d.ShowResources = t.FeedbackLevel != "restricted"
	mkRow := func(tc scoring.TestcaseDetail, subtask int) detailRow {
		text := tc.Text
		if t.HideCheckerMessages && !standardMessage(text) {
			// The checker's own words stay with the staff.
			text = checkers.TranslateMessage("", tc.Outcome)
		}
		v := scoring.TestcaseVerdict(tc)
		return detailRow{Codename: tc.Codename, Subtask: subtask, Verdict: v, Text: translateOutcome(p.Lang, text),
			Class: scoring.VerdictClass(v), Time: tc.Time, Memory: tc.Memory}
	}
	switch det.Type {
	case "group":
		for _, st := range det.Subtasks {
			v := scoring.SubtaskVerdict(st)
			d.Blocks = append(d.Blocks, block{Index: st.Index, Score: st.Score, Max: st.MaxScore, Verdict: v, Class: scoring.VerdictClass(v)})
			for _, tc := range st.Testcases {
				if tc.Public || d.All {
					d.Rows = append(d.Rows, mkRow(tc, st.Index))
				}
			}
		}
	default:
		for _, tc := range det.Testcases {
			if tc.Public || d.All {
				d.Rows = append(d.Rows, mkRow(tc, 0))
			}
		}
	}
	return d, nil
}

func replaceExt(name, ext string) string {
	if len(name) > 3 && name[len(name)-3:] == ".%l" {
		return name[:len(name)-3] + ext
	}
	return name
}

// taskScore is a task score of a contestant or, in team contests, of the
// team: per task the best member score or, with "best per subtask"
// scoring, the sum of the best member score of every subtask (as the
// ranking does).
type taskScore struct {
	score   float64
	pending int32
	// ICPC: solved by anyone of the group; attempts of the first solver,
	// or of everybody while unsolved.
	solved   bool
	attempts int32
	solvedAt time.Time
	// adjustment made by the organizers (included in score).
	adjustment float64
}

func mergeScores(rows []sqlc.ListScoresByParticipationsRow, tasks map[int64]*taskView) map[int64]taskScore {
	out := map[int64]taskScore{}
	subtasks := map[int64][]float64{}
	for _, r := range rows {
		ts := out[r.TaskID]
		ts.pending += r.Pending
		ts.score = max(ts.score, r.Score)
		ts.adjustment += r.Adjustment
		switch {
		case r.IcpcSolved && r.IcpcSolvedAt != nil && (!ts.solved || r.IcpcSolvedAt.Before(ts.solvedAt)):
			ts.solved, ts.attempts, ts.solvedAt = true, r.IcpcAttempts, *r.IcpcSolvedAt
		case !ts.solved && !r.IcpcSolved:
			ts.attempts += r.IcpcAttempts
		}
		var st []float64
		if json.Unmarshal(r.SubtaskScores, &st) == nil {
			best := subtasks[r.TaskID]
			for i, v := range st {
				if i < len(best) {
					best[i] = max(best[i], v)
				} else {
					best = append(best, v)
				}
			}
			subtasks[r.TaskID] = best
		}
		out[r.TaskID] = ts
	}
	for id, best := range subtasks {
		t := tasks[id]
		if t == nil || t.ScoreMode != "max_subtask" || len(best) == 0 {
			continue
		}
		ts := out[id]
		sum := ts.adjustment
		for _, v := range best {
			sum += v
		}
		ts.score = scoring.Round(sum, t.Precision)
		out[id] = ts
	}
	return out
}
