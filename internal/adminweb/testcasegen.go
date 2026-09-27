package adminweb

// Testcases one by one (SPEC_MIN §13, §14): an input typed in the page or
// uploaded, an output typed, uploaded, empty or written by a reference
// solution (a task tester run); and a generator program run once per line
// of parameters. Runs go through the judging workers as user tests owned
// by the administrator; the dispatcher stores each testcase when its runs
// end (internal/dispatcher/testcasejobs.go).

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/jackc/pgx/v5"
)

// maxGeneratedTestcases bounds one generation request.
const maxGeneratedTestcases = 500

// testcaseTools is the data of the testcase forms of the dataset page.
type testcaseTools struct {
	Jobs    []sqlc.TestcaseJob
	Running int
	// Solutions are the task tester runs a reference output can come from;
	// Solve says whether the task type lets a solution write the output.
	Solutions []testerRun
	Solve     bool
	Languages []*langs.Language
	// Next is the proposed codename of the next testcase.
	Next string
}

func (s *Server) testcaseTools(ctx context.Context, d sqlc.Dataset, t sqlc.Task, tcs []sqlc.Testcase) (*testcaseTools, error) {
	jobs, err := s.q.ListTestcaseJobs(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	tt := &testcaseTools{Jobs: jobs, Solve: d.TaskType == "Batch", Languages: s.langs.All()}
	names := make([]string, 0, len(tcs)+len(jobs))
	for _, tc := range tcs {
		names = append(names, tc.Codename)
	}
	for _, j := range jobs {
		names = append(names, j.Codename)
		if j.State != "failed" {
			tt.Running++
		}
	}
	tt.Next = nextCodename(names, "")
	if tt.Solve {
		f, err := s.testerForm(ctx, t)
		if err != nil {
			return nil, err
		}
		tt.Solutions = f.Runs
	}
	return tt, nil
}

// nextCodename proposes the codename after the numbered ones with prefix
// ("7" after "1".."6"; "07" when the existing ones have two digits).
func nextCodename(names []string, prefix string) string {
	best, width := 0, 1
	re := regexp.MustCompile("^" + regexp.QuoteMeta(prefix) + "([0-9]+)$")
	for _, n := range names {
		m := re.FindStringSubmatch(n)
		if m == nil {
			continue
		}
		if v, err := strconv.Atoi(m[1]); err == nil && v >= best {
			best = v
			width = max(width, len(m[1]))
		}
	}
	return prefix + padNumber(best+1, width)
}

func padNumber(n, width int) string {
	s := strconv.Itoa(n)
	for len(s) < width {
		s = "0" + s
	}
	return s
}

// typedText is a text typed in the page as a file: Unix line ends and a
// final newline.
func typedText(v string) []byte {
	v = strings.ReplaceAll(v, "\r\n", "\n")
	if v != "" && !strings.HasSuffix(v, "\n") {
		v += "\n"
	}
	return []byte(v)
}

// fileOrText stores the uploaded file field, or else the text field; ok
// is false when both are empty.
func (s *Server) fileOrText(r *http.Request, fileField, textField string) (digest string, ok bool, err error) {
	if f, fh, ferr := r.FormFile(fileField); ferr == nil {
		defer f.Close()
		if fh.Size > 0 {
			info, err := s.blobs.Put(r.Context(), f)
			return info.Digest, err == nil, err
		}
	}
	text := r.FormValue(textField)
	if strings.TrimSpace(text) == "" {
		return "", false, nil
	}
	info, err := s.blobs.PutBytes(r.Context(), typedText(text))
	return info.Digest, err == nil, err
}

// solutionRun checks that v names a task tester run of task t.
func (s *Server) solutionRun(ctx context.Context, t sqlc.Task, v string) (sqlc.Submission, bool) {
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return sqlc.Submission{}, false
	}
	sub, err := s.q.GetSubmission(ctx, id)
	if err != nil || !sub.Tester || sub.TaskID != t.ID {
		return sqlc.Submission{}, false
	}
	return sub, true
}

// handleTestcaseUpload adds (or replaces) one testcase.
func (s *Server) handleTestcaseUpload(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	d, ok := s.loadDataset(w, r, rc)
	if !ok {
		return
	}
	if err := s.parseUpload(w, r); err != nil {
		s.errorPage(w, r, rc, http.StatusBadRequest, "Upload failed: "+err.Error())
		return
	}
	back := "/datasets/" + strconv.FormatInt(d.ID, 10) + "#testcases"
	code := strings.TrimSpace(r.FormValue("codename"))
	if code == "" {
		tcs, err := s.q.ListTestcases(r.Context(), d.ID)
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		names := make([]string, len(tcs))
		for i, tc := range tcs {
			names[i] = tc.Codename
		}
		code = nextCodename(names, "")
	}
	if !codenameRe.MatchString(code) {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Invalid codename (letters, digits, '_', '.', '-').")
		return
	}
	in, ok, err := s.fileOrText(r, "input", "input_text")
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	if !ok {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Type the input or choose an input file.")
		return
	}
	public := r.FormValue("public") != ""
	var out string
	switch r.FormValue("output_mode") {
	case "solution":
		t, err := s.q.GetTask(r.Context(), d.TaskID)
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		sol, ok := s.solutionRun(r.Context(), t, r.FormValue("solution_id"))
		if !ok || d.TaskType != "Batch" {
			s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Choose a reference solution (a task tester run).")
			return
		}
		runs, err := s.startTestcaseJobs(r.Context(), d, rc.admin.ID, []newJob{{code: code, input: in, public: public}}, "solution", &sol, nil)
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		s.notifyRuns(r.Context(), runs)
		rc.note("codename", code)
		s.done(w, r, back, "Testcase %s: the reference solution is writing its output.", code)
		return
	case "empty":
		info, err := s.blobs.PutBytes(r.Context(), nil)
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		out = info.Digest
	default:
		if out, ok, err = s.fileOrText(r, "output", "output_text"); err != nil {
			s.internalError(w, r, rc, err)
			return
		} else if !ok {
			s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Type the output, choose an output file, or let the reference solution write it.")
			return
		}
	}
	tc, err := s.q.UpsertTestcase(r.Context(), sqlc.UpsertTestcaseParams{DatasetID: d.ID, Codename: code,
		Public: public, InputDigest: in, OutputDigest: out})
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("testcase", tc.ID)
	s.datasetChanged(r.Context(), d.TaskID, d.ID)
	s.done(w, r, back, "Testcase %s saved.", code)
}

// newJob is a testcase to make: its codename, and its input when known
// (else the generator's parameters).
type newJob struct {
	code, input string
	params      []byte
	public      bool
}

// generator is the program that writes inputs.
type generator struct {
	lang   string
	digest string
}

// startTestcaseJobs stores the jobs with their first run: the generator
// on the parameters (gen set) or the reference solution on the input.
// It returns the runs to start.
func (s *Server) startTestcaseJobs(ctx context.Context, d sqlc.Dataset, adminID int64, list []newJob, output string,
	sol *sqlc.Submission, gen *generator) ([]int64, error) {
	var runs []int64
	err := db.InTx(ctx, s.pool, func(_ pgx.Tx, q *sqlc.Queries) error {
		var solFiles []sqlc.SubmissionFile
		if sol != nil {
			var err error
			if solFiles, err = q.ListSubmissionFiles(ctx, sol.ID); err != nil {
				return err
			}
		}
		for _, j := range list {
			p := sqlc.CreateAdminUserTestParams{TaskID: d.TaskID, AdminID: &adminID, DatasetID: &d.ID}
			var files []sqlc.CreateUserTestFilesParams
			state := "output"
			var input *string
			if gen != nil {
				info, err := s.blobs.PutBytes(ctx, j.params)
				if err != nil {
					return err
				}
				p.Plain, p.Language, p.InputDigest = true, &gen.lang, info.Digest
				files = append(files, sqlc.CreateUserTestFilesParams{Filename: "gen.%l", Digest: gen.digest})
				state = "input"
			} else {
				in := j.input
				input = &in
				p.Language, p.InputDigest = sol.Language, j.input
				for _, f := range solFiles {
					files = append(files, sqlc.CreateUserTestFilesParams{Filename: f.Filename, Digest: f.Digest})
				}
			}
			ut, err := q.CreateAdminUserTest(ctx, p)
			if err != nil {
				return err
			}
			for i := range files {
				files[i].UserTestID = ut.ID
			}
			if _, err := q.CreateUserTestFiles(ctx, files); err != nil {
				return err
			}
			var solID *int64
			if sol != nil {
				solID = &sol.ID
			}
			if _, err := q.CreateTestcaseJob(ctx, sqlc.CreateTestcaseJobParams{DatasetID: d.ID, Codename: j.code, Public: j.public,
				AdminID: &adminID, InputDigest: input, Output: output, SolutionID: solID, UserTestID: &ut.ID, State: state}); err != nil {
				return err
			}
			runs = append(runs, ut.ID)
		}
		return nil
	})
	return runs, err
}

// notifyRuns asks the dispatcher to start the runs (the sweeper starts
// any whose notice is lost).
func (s *Server) notifyRuns(ctx context.Context, ids []int64) {
	for _, id := range ids {
		if err := s.queue.Notify(ctx, queue.Event{Kind: queue.EventUserTest, UserTestID: id}); err != nil {
			s.log.Warn("notify dispatcher", "error", err)
		}
	}
}

// handleTestcaseGenerate runs a generator once per line of parameters.
func (s *Server) handleTestcaseGenerate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	d, ok := s.loadDataset(w, r, rc)
	if !ok {
		return
	}
	if err := s.parseUpload(w, r); err != nil {
		s.errorPage(w, r, rc, http.StatusBadRequest, "Upload failed: "+err.Error())
		return
	}
	t, err := s.q.GetTask(r.Context(), d.TaskID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	lang, ok := s.langs.Get(r.FormValue("language"))
	if !ok {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Choose the generator's language.")
		return
	}
	gen, _, _, err := s.storeUpload(r, "generator")
	if err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Choose the generator's source file.")
		return
	}
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(r.FormValue("lines"), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 || len(lines) > maxGeneratedTestcases {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Write between 1 and 500 lines of parameters, one per testcase.")
		return
	}
	prefix := strings.TrimSpace(r.FormValue("prefix"))
	start, err := strconv.Atoi(strings.TrimSpace(r.FormValue("start")))
	if err != nil || start < 0 {
		start = 1
	}
	output := "empty"
	var sol *sqlc.Submission
	if r.FormValue("output_mode") == "solution" {
		sub, ok := s.solutionRun(r.Context(), t, r.FormValue("solution_id"))
		if !ok || d.TaskType != "Batch" {
			s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Choose a reference solution (a task tester run).")
			return
		}
		output, sol = "solution", &sub
	} else if _, err := s.blobs.PutBytes(r.Context(), nil); err != nil { // the empty output
		s.internalError(w, r, rc, err)
		return
	}
	width := len(strconv.Itoa(start + len(lines) - 1))
	list := make([]newJob, len(lines))
	for i, l := range lines {
		list[i] = newJob{code: prefix + padNumber(start+i, width), params: typedText(l)}
		if !codenameRe.MatchString(list[i].code) {
			s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Invalid codename (letters, digits, '_', '.', '-').")
			return
		}
	}
	runs, err := s.startTestcaseJobs(r.Context(), d, rc.admin.ID, list, output, sol, &generator{lang: lang.ID, digest: gen})
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	s.notifyRuns(r.Context(), runs)
	rc.note("testcases", len(list))
	s.done(w, r, "/datasets/"+strconv.FormatInt(d.ID, 10)+"#testcases", "Generating %d testcases: they appear below as they are ready.", len(list))
}

// handleTestcaseJobsClear forgets the failed jobs of a dataset.
func (s *Server) handleTestcaseJobsClear(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	d, ok := s.loadDataset(w, r, rc)
	if !ok {
		return
	}
	if err := s.q.DeleteFailedTestcaseJobs(r.Context(), d.ID); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	s.done(w, r, "/datasets/"+strconv.FormatInt(d.ID, 10)+"#testcases", "")
}
