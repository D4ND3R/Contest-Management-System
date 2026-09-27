package adminweb

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/webtest"
)

// TestTestcasesOneByOne (SPEC_MIN §13, §14): a testcase is typed in the
// page (the codename proposed is the next number); its output can be typed,
// empty, or written by a task tester run; a generator runs once per line of
// parameters. The runs are the administrator's own (no participation), on
// this dataset; the task page lists the setup steps.
func TestTestcasesOneByOne(t *testing.T) {
	f := newFixture(t)
	b := f.login("task_setter")
	ds := fmt.Sprintf("/datasets/%d", f.ds.ID)
	code, page := b.Get(ds)
	webtest.MustOK(t, "dataset", code, page)
	if !strings.Contains(page, `name="codename" value="2"`) || !strings.Contains(page, "Generate with a program") {
		t.Fatalf("dataset page lacks the testcase tools:\n%s", page)
	}
	// Typed input and output (Windows line ends, no final newline).
	code, body := b.PostMultipart(ds+"/testcases", map[string]string{"codename": "", "input_text": "5 6\r\n7", "output_text": "11", "output_mode": "given"})
	if code != 200 || !strings.Contains(body, "Testcase 2 saved.") {
		t.Fatalf("typed testcase: %d\n%s", code, body)
	}
	tcs, _ := f.q.ListTestcases(bg, f.ds.ID)
	var typed sqlc.Testcase
	for _, tc := range tcs {
		if tc.Codename == "2" {
			typed = tc
		}
	}
	in, _ := blob.ReadAll(bg, f.store, typed.InputDigest)
	out, _ := blob.ReadAll(bg, f.store, typed.OutputDigest)
	if string(in) != "5 6\n7\n" || string(out) != "11\n" {
		t.Fatalf("typed testcase %q %q", in, out)
	}
	// Empty output; nothing typed is an error.
	if code, _ := b.PostMultipart(ds+"/testcases", map[string]string{"codename": "e", "input_text": "1", "output_mode": "empty"}); code != 200 {
		t.Fatalf("empty output: %d", code)
	}
	if code, _ := b.PostMultipart(ds+"/testcases", map[string]string{"codename": "x", "input_text": " ", "output_text": "1"}); code != http.StatusUnprocessableEntity {
		t.Fatalf("no input: %d", code)
	}
	// The reference solution needs a task tester run.
	if code, _ := b.PostMultipart(ds+"/testcases", map[string]string{"codename": "s", "input_text": "1 1", "output_mode": "solution", "solution_id": "999"}); code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown solution: %d", code)
	}
	lang := "c11"
	sol, err := f.q.CreateTesterSubmission(bg, sqlc.CreateTesterSubmissionParams{TaskID: f.task.ID, Language: &lang, AdminID: f.admins["all"].ID})
	if err != nil {
		t.Fatal(err)
	}
	f.q.CreateSubmissionFiles(bg, []sqlc.CreateSubmissionFilesParams{{SubmissionID: sol.ID, Filename: "sum.%l", Digest: f.put("int main(){}")}})
	code, body = b.PostMultipart(ds+"/testcases", map[string]string{"codename": "s", "input_text": "1 1", "output_mode": "solution", "solution_id": fmt.Sprint(sol.ID)})
	if code != 200 || !strings.Contains(body, "the reference solution is writing its output") || !strings.Contains(body, `hx-trigger="every 2s"`) {
		t.Fatalf("solution output: %d\n%s", code, body)
	}
	// A generator on three lines.
	code, body = b.PostMultipart(ds+"/testcases/generate", map[string]string{"language": "c11", "lines": "10\n\n20\r\n30", "prefix": "g", "start": "8",
		"output_mode": "solution", "solution_id": fmt.Sprint(sol.ID)}, webtest.File{Field: "generator", Name: "gen.c", Data: []byte("int main(){}")})
	if code != 200 || !strings.Contains(body, "Generating 3 testcases") {
		t.Fatalf("generate: %d\n%s", code, body)
	}
	jobs, _ := f.q.ListTestcaseJobs(bg, f.ds.ID)
	var names []string
	for _, j := range jobs {
		names = append(names, j.Codename+":"+j.State)
		ut, err := f.q.GetUserTest(bg, *j.UserTestID)
		if err != nil || ut.ParticipationID != nil || ut.DatasetID == nil || *ut.DatasetID != f.ds.ID || ut.Plain != (j.State == "input") {
			t.Fatalf("run of %s: %+v %v", j.Codename, ut, err)
		}
	}
	if strings.Join(names, " ") != "s:output g08:input g09:input g10:input" {
		t.Fatalf("jobs %v", names)
	}
	if code, _ := b.PostMultipart(ds+"/testcases/generate", map[string]string{"language": "c11", "lines": ""},
		webtest.File{Field: "generator", Name: "gen.c", Data: []byte("x")}); code != http.StatusUnprocessableEntity {
		t.Fatalf("no lines: %d", code)
	}
	// Failed jobs can be forgotten.
	f.pool.Exec(bg, "UPDATE testcase_jobs SET state = 'failed', error = 'boom' WHERE codename = 'g10'")
	if _, page := b.Get(ds); !strings.Contains(page, "boom") || !strings.Contains(page, "forget the failed ones") {
		t.Fatal("failed job not shown")
	}
	if code, _ := b.Post(ds+"/testcase-jobs/clear", url.Values{}); code != 200 {
		t.Fatalf("clear: %d", code)
	}
	if jobs, _ := f.q.ListTestcaseJobs(bg, f.ds.ID); len(jobs) != 3 {
		t.Fatalf("after clearing: %d jobs", len(jobs))
	}
	// Read-only administrators cannot add testcases.
	if code, _ := f.login("read_only").PostMultipart(ds+"/testcases", map[string]string{"input_text": "1", "output_text": "1"}); code != http.StatusForbidden {
		t.Fatalf("read-only: %d", code)
	}
	// The task page shows what is done and what is missing.
	_, page = b.Get(fmt.Sprintf("/tasks/%d", f.task.ID))
	for _, w := range []string{`<ol class="steps">`, `<li class="done"><a href="/tasks/` + fmt.Sprint(f.task.ID) + `#statements">Statement</a>`,
		`#add-testcases">Testcases</a> <span class="muted">(4)</span>`, `<li class="todo"><a href="/tasks/` + fmt.Sprint(f.task.ID) + `#tester">Reference solution with the full score</a>`} {
		if !strings.Contains(page, w) {
			t.Errorf("task page lacks %s", w)
		}
	}
}
