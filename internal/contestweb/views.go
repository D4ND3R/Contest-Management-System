package contestweb

import (
	"encoding/json"
	"net/http"
	"time"

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
	HasScore   bool
	Score, Max float64
	Precision  int
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
}

func (s *Server) fillStatus(p *page, t *taskView, st subState, sv *subView) {
	sv.Max, sv.Precision = t.MaxScore, t.Precision
	switch {
	case st.systemError != nil:
		sv.StatusText, sv.Class = p.T("Evaluation failed (the organizers were notified)"), "warn"
	case st.compilation == nil:
		sv.StatusText, sv.Pending = p.T("Compiling…"), true
	case *st.compilation == "fail":
		sv.StatusText, sv.Class = p.T("Compilation failed"), "bad"
	case !st.scored:
		sv.Pending, sv.Evaluating, sv.Done, sv.Total = true, true, st.done, st.total
		sv.StatusText = p.T("Evaluating")
	default:
		sv.HasScore = true
		// Contestants see the public score unless they played a token or
		// every testcase is public (then both coincide).
		if st.tokened && st.score != nil {
			sv.Score = *st.score
		} else if st.pub != nil {
			sv.Score = *st.pub
		}
		sv.StatusText, sv.Class = p.T("Evaluated"), "ok"
		if sv.Score < sv.Max {
			sv.Class = ""
		}
	}
}

// listSubs loads the contestant's submissions to a task (one query).
func (s *Server) listSubs(r *http.Request, rc *reqCtx, t *taskView) ([]subView, error) {
	if t.Dataset == nil {
		return nil, nil
	}
	rows, err := s.q.ListSubmissionsWithResults(r.Context(), sqlc.ListSubmissionsWithResultsParams{
		DatasetID: t.Dataset.ID, ParticipationID: rc.part.ID, TaskID: t.ID})
	if err != nil {
		return nil, err
	}
	p := &page{Lang: rc.lang}
	out := make([]subView, 0, len(rows))
	for _, row := range rows {
		sv := subView{ID: row.ID, Time: row.SubmittedAt, Official: row.Official, Tokened: row.Tokened}
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
			score: row.Score, pub: row.PublicScore, scored: row.ScoredAt != nil, systemError: row.SystemError, tokened: row.Tokened}, &sv)
		out = append(out, sv)
	}
	return out, nil
}

func (s *Server) langName(id string) string {
	if l, ok := s.langs.Get(id); ok {
		return l.Name
	}
	return id
}

func (s *Server) subViewFromDetail(p *page, rc *reqCtx, t *taskView, row sqlc.GetSubmissionWithResultRow) subView {
	sv := subView{ID: row.ID, Time: row.SubmittedAt, Official: row.Official, Tokened: row.Tokened}
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
		score: row.Score, pub: row.PublicScore, scored: row.ScoredAt != nil, systemError: row.SystemError, tokened: row.Tokened}, &sv)
	return sv
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
	Text     string
	Class    string
	Time     float64
	Memory   int64
}

type detailGroup struct {
	Title      string
	Score, Max float64
	Class      string
	Rows       []detailRow
}

type detailsView struct{ Groups []detailGroup }

type detailData struct {
	Sub           subView
	TaskName      string
	Files         []string
	Download      bool
	Compilation   *compilationView
	Details       *detailsView
	Full          bool
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
		if c.OK && c.Stdout == "" && c.Stderr == "" && t.TaskType == "OutputOnly" {
			c = nil
		}
		d.Compilation = c
	}
	if row.ScoredAt == nil {
		return d, nil
	}
	// Full details for tokened submissions, public ones otherwise.
	raw := row.PublicScoreDetails
	d.Full = row.Tokened
	if d.Full {
		raw = row.ScoreDetails
	}
	var det scoring.Details
	if len(raw) == 0 || json.Unmarshal(raw, &det) != nil {
		return d, nil
	}
	restricted := t.FeedbackLevel == "restricted"
	d.ShowResources = !restricted
	dv := &detailsView{}
	classOf := func(o float64) string {
		switch {
		case o >= 1:
			return "ok"
		case o <= 0:
			return "bad"
		}
		return "warn"
	}
	mkRow := func(tc scoring.TestcaseDetail) detailRow {
		return detailRow{Codename: tc.Codename, Text: translateOutcome(p.Lang, tc.Text), Class: classOf(tc.Outcome), Time: tc.Time, Memory: tc.Memory}
	}
	switch det.Type {
	case "group":
		for _, st := range det.Subtasks {
			g := detailGroup{Title: p.T("Subtask %d", st.Index), Score: st.Score, Max: st.MaxScore, Class: classOf(st.Fraction)}
			for _, tc := range st.Testcases {
				if restricted {
					// Only the first testcase that did not pass is shown.
					if tc.Outcome < 1 {
						g.Rows = append(g.Rows, mkRow(tc))
						break
					}
					continue
				}
				g.Rows = append(g.Rows, mkRow(tc))
			}
			dv.Groups = append(dv.Groups, g)
		}
	default:
		g := detailGroup{}
		for _, tc := range det.Testcases {
			if restricted && tc.Outcome >= 1 {
				continue
			}
			g.Rows = append(g.Rows, mkRow(tc))
			if restricted {
				break
			}
		}
		dv.Groups = append(dv.Groups, g)
	}
	d.Details = dv
	return d, nil
}

func replaceExt(name, ext string) string {
	if len(name) > 3 && name[len(name)-3:] == ".%l" {
		return name[:len(name)-3] + ext
	}
	return name
}
