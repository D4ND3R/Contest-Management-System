package adminweb

// The Testcases window of a task (D105): adding testcases (typed, from a
// generator or a zip), their list, and test submissions whose result is
// shown testcase by testcase next to the list.

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/scoring"
)

// matrixRuns is how many test submissions get a column in the testcase
// list (the newest ones judged on the dataset shown).
const matrixRuns = 4

// testsPage is the Testcases window.
type testsPage struct {
	Head  *problemHead
	Rows  []testcaseRow
	Tools *testcaseTools
	// Tester is the test submission form; Runs are the test submissions
	// with their result on the dataset shown, and Columns the newest of
	// them, shown testcase by testcase.
	Tester   *testerForm
	Runs     []datasetRun
	Columns  []datasetRun
	Examples []exampleView
	// Pending: testcases are being made or a test submission is being
	// judged, so the list refreshes by itself.
	Pending bool
}

// testcaseRow is a testcase with its results in the matrix columns.
type testcaseRow struct {
	sqlc.ListTestcasesWithSizesRow
	Example bool
	Cells   []runCell
}

// datasetRun is a test submission with its result on one dataset.
type datasetRun struct {
	ID       int64
	Time     time.Time
	Admin    string
	Language string
	Name     string
	Result   *testerResult
	// Class colours the score: full, partial or none.
	Class string
}

// runCell is the result of a test submission on one testcase.
type runCell struct {
	Verdict string
	Class   string
	Time    *float64
	Memory  *int64
	Text    string
}

func (s *Server) testsPage(ctx context.Context, h *problemHead) (*testsPage, error) {
	t, d := h.Task, h.Dataset
	p := &testsPage{Head: h}
	tcs, err := s.q.ListTestcasesWithSizes(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	if p.Tester, err = s.testerForm(ctx, t); err != nil {
		return nil, err
	}
	codes, pub := make([]string, len(tcs)), make([]bool, len(tcs))
	for i, tc := range tcs {
		codes[i], pub[i] = tc.Codename, tc.Public
	}
	maxScore := 0.0
	if st, err := scoring.New(d.ScoreType, d.ScoreTypeParams, codes, pub, int(t.ScorePrecision)); err == nil {
		maxScore = st.MaxScore()
	}
	// Outputs are best written by a solution with the full score: those
	// come first in the reference solution choices.
	var full, rest []testerRun
	for _, run := range p.Tester.Runs {
		for _, res := range run.Results {
			if res.DatasetID == d.ID && res.Status == "scored" && res.Score != nil && maxScore > 0 && *res.Score >= maxScore-1e-9 {
				run.Full = true
			}
		}
		if run.Full {
			full = append(full, run)
		} else {
			rest = append(rest, run)
		}
	}
	if p.Tools, err = s.testcaseTools(ctx, d, codes, append(full, rest...)); err != nil {
		return nil, err
	}
	p.Pending = p.Tools.Running > 0
	for _, run := range p.Tester.Runs {
		dr := datasetRun{ID: run.ID, Time: run.Time, Admin: run.Admin, Language: run.Language, Name: run.Name}
		for i := range run.Results {
			if run.Results[i].DatasetID == d.ID {
				dr.Result = &run.Results[i]
			}
		}
		if dr.Result == nil {
			// Sent before this dataset existed: tester runs are judged
			// on the datasets the task had then.
			continue
		}
		dr.Class = dr.Result.Class
		switch {
		case dr.Result.Class == "":
			p.Pending = true
		case dr.Result.Status == "scored" && dr.Result.Score != nil:
			dr.Class = scoring.VerdictClass(scoring.Verdict("", *dr.Result.Score, maxScore))
		}
		p.Runs = append(p.Runs, dr)
		if len(p.Columns) < matrixRuns {
			p.Columns = append(p.Columns, dr)
		}
	}
	if p.Examples, err = s.exampleViews(ctx, t.ID); err != nil {
		return nil, err
	}
	examples := map[[2]string]bool{}
	for _, e := range p.Examples {
		examples[[2]string{e.InputDigest, e.OutputDigest}] = true
	}
	ids := make([]int64, len(p.Columns))
	for i, c := range p.Columns {
		ids[i] = c.ID
	}
	cells := map[[2]int64]runCell{}
	if len(ids) > 0 {
		evs, err := s.q.ListEvaluationsBySubmissions(ctx, sqlc.ListEvaluationsBySubmissionsParams{Ids: ids, DatasetID: d.ID})
		if err != nil {
			return nil, err
		}
		for _, e := range evs {
			v := scoring.TestcaseVerdict(scoring.TestcaseDetail{Outcome: e.Outcome, Status: e.ExitStatus})
			cells[[2]int64{e.SubmissionID, e.TestcaseID}] = runCell{Verdict: v, Class: scoring.VerdictClass(v),
				Time: e.ExecutionTime, Memory: e.ExecutionMemory, Text: e.Text}
		}
	}
	p.Rows = make([]testcaseRow, len(tcs))
	for i, tc := range tcs {
		row := testcaseRow{ListTestcasesWithSizesRow: tc, Example: examples[[2]string{tc.InputDigest, tc.OutputDigest}],
			Cells: make([]runCell, len(p.Columns))}
		for j, c := range p.Columns {
			row.Cells[j] = cells[[2]int64{c.ID, tc.ID}]
		}
		p.Rows[i] = row
	}
	return p, nil
}

// handleTaskTests shows the Testcases window.
func (s *Server) handleTaskTests(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	h, ok := s.loadProblem(w, r, rc, "tests")
	if !ok {
		return
	}
	p, err := s.testsPage(r.Context(), h)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	s.render(w, "task_tests", http.StatusOK, s.taskCrumbs(s.newPage(w, r, rc, h.Task.Name, "tasks", p), h))
}

// testcaseView is the page of one testcase: its input and output.
type testcaseView struct {
	Head       *problemHead
	Testcase   sqlc.Testcase
	Input      string
	Output     string
	InputCut   bool
	OutputCut  bool
	Prev, Next *sqlc.Testcase
}

// testcaseShown is how much of an input or output the page shows.
const testcaseShown = 64 << 10

func (s *Server) handleTestcaseView(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	tc, ok := s.loadTestcase(w, r, rc)
	if !ok {
		return
	}
	d, err := s.q.GetDataset(r.Context(), tc.DatasetID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	t, err := s.q.GetTask(r.Context(), d.TaskID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	h, err := s.problemHead(r.Context(), t, d.ID, "tests")
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	v := &testcaseView{Head: h, Testcase: tc}
	in, cutIn, err := blob.ReadLimited(r.Context(), s.blobs, tc.InputDigest, testcaseShown)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	out, cutOut, err := blob.ReadLimited(r.Context(), s.blobs, tc.OutputDigest, testcaseShown)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	v.Input, v.InputCut, v.Output, v.OutputCut = string(in), cutIn, string(out), cutOut
	tcs, err := s.q.ListTestcases(r.Context(), d.ID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	for i := range tcs {
		if tcs[i].ID == tc.ID {
			if i > 0 {
				v.Prev = &tcs[i-1]
			}
			if i+1 < len(tcs) {
				v.Next = &tcs[i+1]
			}
		}
	}
	s.render(w, "testcase", http.StatusOK, s.taskCrumbs(s.newPage(w, r, rc, t.Name+" · "+tc.Codename, "tasks", v), h).
		crumb(t.Name, problemURL(t, d.ID, "tests")))
}

// handleTestcasesClear deletes every testcase of a dataset (to import a
// new set).
func (s *Server) handleTestcasesClear(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	d, ok := s.loadDataset(w, r, rc)
	if !ok {
		return
	}
	n, err := s.q.CountTestcases(r.Context(), d.ID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	if err := s.q.DeleteDatasetTestcases(r.Context(), d.ID); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("dataset", d.ID)
	rc.note("deleted", n)
	s.datasetChanged(r.Context(), d.TaskID, d.ID)
	s.done(w, r, s.datasetURL(r.Context(), d, "tests")+"#add", "%s testcases deleted.", strconv.FormatInt(n, 10))
}
