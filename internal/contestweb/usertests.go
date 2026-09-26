package contestweb

import (
	"errors"
	"github.com/D4ND3R/Contest-Management-System/internal/i18n"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/contest"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/jackc/pgx/v5"
)

// User tests: a contestant runs a solution on an input of their own and
// sees its output, with the task's limits and the contest's (or task's)
// user test limits. The dispatcher and the workers do the rest.

// outputPreview is how much of an output is shown inline.
const outputPreview = 4 << 10

// testView is one user test as shown to its author.
type testView struct {
	ID         int64
	Time       time.Time
	Language   string
	Pending    bool
	StatusText string
	Class      string
	Resources  string
	Compiler   string
	HasOutput  bool
	Output     string
	Truncated  bool
}

// testRow is the part of a user test and its result the views need.
type testRow struct {
	id                                  int64
	at                                  time.Time
	language                            *string
	compilation, compText, compOut, err *string
	evalText, exitStatus, output        *string
	execTime                            *float64
	execMemory                          *int64
	systemError                         *string
	completed                           *time.Time
}

// testsEnabled reports whether user tests are offered on the task.
func testsEnabled(rc *reqCtx, t *taskView) bool {
	return rc.contest.AllowUserTests && t.Dataset != nil && t.TaskType != "OutputOnly"
}

func (s *Server) testViewOf(r *http.Request, p *page, row testRow, showCompiler bool) testView {
	v := testView{ID: row.id, Time: row.at}
	if row.language != nil {
		v.Language = s.langName(*row.language)
	}
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	switch {
	case row.systemError != nil:
		v.StatusText, v.Class = p.T("Evaluation failed (the organizers were notified)"), "warn"
	case row.compilation == nil:
		v.StatusText, v.Pending = p.T("Compiling…"), true
	case *row.compilation == "fail":
		v.StatusText, v.Class = p.T("Compilation failed"), "bad"
		if showCompiler {
			v.Compiler = strings.TrimSpace(str(row.err) + "\n" + str(row.compOut))
		}
	case row.completed == nil:
		v.StatusText, v.Pending = p.T("Running…"), true
	default:
		v.StatusText = translateOutcome(p.Lang, str(row.evalText))
		if v.StatusText == "" {
			v.StatusText = p.T("Done")
		}
		if st := str(row.exitStatus); st != "ok" && st != "" {
			v.Class = "warn"
		}
		if row.execTime != nil && row.execMemory != nil {
			v.Resources = p.Seconds(*row.execTime) + " · " + p.Bytes(*row.execMemory)
		}
		if row.output != nil {
			v.HasOutput = true
			data, more, err := blob.ReadLimited(r.Context(), s.blobs, *row.output, outputPreview)
			if err == nil {
				for !utf8.Valid(data) && len(data) > 0 {
					data = data[:len(data)-1]
				}
				v.Output, v.Truncated = string(data), more
			}
		}
	}
	return v
}

func (s *Server) listTests(r *http.Request, rc *reqCtx, p *page, t *taskView) ([]testView, error) {
	if !testsEnabled(rc, t) {
		return nil, nil
	}
	rows, err := s.q.ListUserTestsWithResults(r.Context(), sqlc.ListUserTestsWithResultsParams{
		DatasetID: t.Dataset.ID, ParticipationID: rc.part.ID, TaskID: t.ID})
	if err != nil {
		return nil, err
	}
	out := make([]testView, len(rows))
	for i, x := range rows {
		out[i] = s.testViewOf(r, p, testRow{id: x.ID, at: x.SubmittedAt, language: x.Language, compilation: x.CompilationOutcome,
			compText: x.CompilationText, compOut: x.CompilationStdout, err: x.CompilationStderr, evalText: x.EvaluationText,
			exitStatus: x.ExitStatus, output: x.OutputDigest, execTime: x.ExecutionTime, execMemory: x.ExecutionMemory,
			systemError: x.SystemError, completed: x.CompletedAt}, rc.contest.ShowCompilationOutput)
	}
	return out, nil
}

// ownTest loads one of the contestant's user tests.
func (s *Server) ownTest(w http.ResponseWriter, r *http.Request, rc *reqCtx) (sqlc.GetUserTestWithResultRow, *taskView, bool) {
	var zero sqlc.GetUserTestWithResultRow
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return zero, nil, false
	}
	ut, err := s.q.GetUserTest(r.Context(), id)
	if err != nil || ut.ParticipationID != rc.part.ID {
		http.NotFound(w, r)
		return zero, nil, false
	}
	t := rc.contest.TaskByID[ut.TaskID]
	if t == nil || t.Dataset == nil {
		http.NotFound(w, r)
		return zero, nil, false
	}
	row, err := s.q.GetUserTestWithResult(r.Context(), sqlc.GetUserTestWithResultParams{DatasetID: t.Dataset.ID, ID: id})
	if err != nil {
		s.fail(w, err)
		return zero, nil, false
	}
	return row, t, true
}

func (s *Server) handleUserTestRow(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	x, t, ok := s.ownTest(w, r, rc)
	if !ok {
		return
	}
	p := s.newPage(rc, "", t.Name)
	v := s.testViewOf(r, p, testRow{id: x.ID, at: x.SubmittedAt, language: x.Language, compilation: x.CompilationOutcome,
		compText: x.CompilationText, compOut: x.CompilationStdout, err: x.CompilationStderr, evalText: x.EvaluationText,
		exitStatus: x.ExitStatus, output: x.OutputDigest, execTime: x.ExecutionTime, execMemory: x.ExecutionMemory,
		systemError: x.SystemError, completed: x.CompletedAt}, rc.contest.ShowCompilationOutput)
	w.Header().Set("Cache-Control", "no-store")
	s.renderPartial(w, "testrow", testCtx{P: p, T: v})
}

type testCtx struct {
	P *page
	T testView
}

// handleUserTestFile downloads the input or the output of a user test.
func (s *Server) handleUserTestFile(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	x, _, ok := s.ownTest(w, r, rc)
	if !ok {
		return
	}
	var digest string
	switch r.PathValue("which") {
	case "input":
		digest = x.InputDigest
	case "output":
		if x.OutputDigest != nil {
			digest = *x.OutputDigest
		}
	}
	if digest == "" {
		http.NotFound(w, r)
		return
	}
	rd, err := s.blobs.Open(r.Context(), digest)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer rd.Close()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="test-`+strconv.FormatInt(x.ID, 10)+"-"+r.PathValue("which")+`.txt"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	io.Copy(w, rd)
}

// handleUserTest runs a contestant's solution on their own input.
func (s *Server) handleUserTest(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t := s.visibleTask(w, r, rc)
	if t == nil {
		return
	}
	back := "/" + rc.contest.Name + "/tasks/" + t.Name + "#tests"
	if r.URL.Query().Get("from") == "testing" {
		back = "/" + rc.contest.Name + "/testing?task=" + url.QueryEscape(t.Name)
	}
	if !testsEnabled(rc, t) || !rc.status.CanSubmit {
		s.errorPage(w, r, rc.contest, http.StatusForbidden, "Test rejected", "Tests are not available now.")
		return
	}
	if !s.limiter.Allow(r.Context(), "test:"+itoa(rc.part.ID), s.cfg.RateLimitPerMinute, time.Minute) {
		s.errorPage(w, r, rc.contest, http.StatusTooManyRequests, "Test rejected", "Too many requests, please slow down.")
		return
	}
	max := int64(s.cfg.MaxUserTestBytes)
	if max <= 0 {
		max = 8 << 20
	}
	files, lang, msg := s.readSources(w, r, rc, t, max)
	if msg != "" {
		s.errorPage(w, r, rc.contest, http.StatusBadRequest, "Test rejected", msg)
		return
	}
	input, msg := readTestInput(r, max)
	if msg != "" {
		s.errorPage(w, r, rc.contest, http.StatusBadRequest, "Test rejected", msg)
		return
	}
	if !rc.part.Unrestricted {
		st, err := s.q.UserTestStats(r.Context(), sqlc.UserTestStatsParams{ParticipationID: rc.part.ID, TaskID: t.ID})
		if err != nil {
			s.fail(w, err)
			return
		}
		cl := contest.Limit{MaxNumber: rc.contest.MaxUserTestNumber, MinInterval: rc.contest.MinUserTestIntervalS}
		tl := contest.Limit{MaxNumber: t.MaxUserTestNumber, MinInterval: t.MinUserTestIntervalS}
		u := contest.Usage{ContestCount: st.ContestCount, TaskCount: st.TaskCount, ContestLast: st.ContestLast, TaskLast: st.TaskLast}
		if err := contest.Check(cl, tl, u, rc.now); err != nil {
			var le *contest.LimitError
			if errors.As(err, &le) {
				s.errorPage(w, r, rc.contest, http.StatusTooManyRequests, "Test rejected", testLimitMessage(le))
				return
			}
		}
	}
	id, err := s.storeUserTest(r, rc, t, files, lang, input)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.log.Debug("user test received", "user_test", id, "participation", rc.part.ID, "task", t.Name)
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// testLimitMessage words the limit messages for tests.
func testLimitMessage(le *contest.LimitError) string {
	switch le.Key {
	case contest.MsgContestMax, contest.MsgTaskMax:
		return "You have reached the maximum number of tests."
	}
	return "Please wait before testing again."
}

// readTestInput takes the input from the uploaded file or the text box.
func readTestInput(r *http.Request, max int64) ([]byte, string) {
	if f, _, err := r.FormFile("input"); err == nil {
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, max+1))
		if err != nil {
			return nil, "Could not read the file."
		}
		if int64(len(data)) > max {
			return nil, "The input is too large."
		}
		if len(data) > 0 {
			return data, ""
		}
	}
	text := r.FormValue("input_text")
	if int64(len(text)) > max {
		return nil, "The input is too large."
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return []byte(strings.ReplaceAll(text, "\r\n", "\n")), ""
}

func (s *Server) storeUserTest(r *http.Request, rc *reqCtx, t *taskView, files []submittedFile, lang *langs.Language, input []byte) (int64, error) {
	ctx := r.Context()
	in, err := s.blobs.PutBytes(ctx, input)
	if err != nil {
		return 0, err
	}
	params := make([]sqlc.CreateUserTestFilesParams, len(files))
	for i, f := range files {
		info, err := s.blobs.PutBytes(ctx, f.data)
		if err != nil {
			return 0, err
		}
		params[i] = sqlc.CreateUserTestFilesParams{Filename: f.name, Digest: info.Digest}
	}
	var langID *string
	if lang != nil {
		langID = &lang.ID
	}
	var id int64
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		ut, err := q.CreateUserTest(ctx, sqlc.CreateUserTestParams{ParticipationID: rc.part.ID, TaskID: t.ID,
			SubmittedAt: rc.now, Language: langID, InputDigest: in.Digest})
		if err != nil {
			return err
		}
		id = ut.ID
		for i := range params {
			params[i].UserTestID = id
		}
		_, err = q.CreateUserTestFiles(ctx, params)
		return err
	})
	if err != nil {
		return 0, err
	}
	if err := s.queue.Notify(ctx, queue.Event{Kind: queue.EventUserTest, UserTestID: id}); err != nil {
		s.log.Warn("notify dispatcher", "user_test", id, "error", err)
	}
	return id, nil
}

// testingData is the Testing page: a task's test form and the tests of
// every task.
type testingData struct {
	Tasks   []*taskView // tasks that accept tests
	Task    *taskView   // the chosen one
	Form    *taskData
	ByTask  []taskTests
	Enabled bool
}

type taskTests struct {
	Task  *taskView
	Tests []testView
}

// handleTesting shows every task's tests and a form for one of them
// (?task=NAME, the first by default).
func (s *Server) handleTesting(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	p := s.newPage(rc, i18n.T(rc.lang, "Testing"), "testing")
	d := &testingData{}
	for _, t := range p.Tasks { // only once the tasks are visible
		if !testsEnabled(rc, t) {
			continue
		}
		d.Tasks = append(d.Tasks, t)
		if r.URL.Query().Get("task") == t.Name {
			d.Task = t
		}
	}
	if d.Task == nil && len(d.Tasks) > 0 {
		d.Task = d.Tasks[0]
	}
	if d.Task != nil {
		td, err := s.taskData(r, rc, p, d.Task)
		if err != nil {
			s.fail(w, err)
			return
		}
		d.Form = td
		d.Enabled = td.TestsEnabled
	}
	for _, t := range d.Tasks {
		tests, err := s.listTests(r, rc, p, t)
		if err != nil {
			s.fail(w, err)
			return
		}
		if len(tests) > 0 {
			d.ByTask = append(d.ByTask, taskTests{Task: t, Tests: tests})
		}
	}
	p.Data = d
	s.render(w, "testing", http.StatusOK, p)
}
