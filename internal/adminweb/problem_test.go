package adminweb

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/webtest"
)

// optionsForm is the options form of the Configuration window with extra
// values.
func optionsForm(f *fixture, extra url.Values) url.Values {
	v := url.Values{"dataset_id": {fmt.Sprint(f.ds.ID)}, "name": {"sum"}, "title": {"Suma"}, "task_type": {"Batch"},
		"time_limit": {"2"}, "memory_limit_mib": {"128"}, "process_limit": {"1"}, "tt_checker": {"exact"},
		"score_type": {"Sum"}, "score_type_params": {"25"}, "submission_format": {"sum.%l"}, "feedback_level": {"full"},
		"score_mode": {"max"}, "score_precision": {"0"}, "token_mode": {"disabled"}, "token_gen_interval_s": {"1800"},
		"languages": {"c11"}}
	for k, x := range extra {
		v[k] = x
	}
	return v
}

// TestConfigurationWindow (D105): the options form saves the task and its
// dataset at once, problem.yaml shows and edits the same options, and
// files go up one by one, each where its name says.
func TestConfigurationWindow(t *testing.T) {
	f := newFixture(t)
	b := f.login("task_setter")
	path := fmt.Sprintf("/tasks/%d", f.task.ID)
	code, page := b.Get(path)
	webtest.MustOK(t, "configuration", code, page)
	for _, w := range []string{`aria-current="page">Configuration</a>`, `/tests">Testcases (2)</a>`, `action="/tasks/` + fmt.Sprint(f.task.ID) + `/options"`,
		`name="mode" value="fill"`, "name: sum", `>checker.cpp</a></td><td>Checker</td>`, `>sample.zip</a></td><td>Attachment for contestants</td>`} {
		if !strings.Contains(page, w) {
			t.Errorf("configuration lacks %s", w)
		}
	}

	// The options form: task and dataset in one save; the dataset keeps its
	// description.
	code, body := b.Post(path+"/options", optionsForm(f, nil))
	webtest.MustOK(t, "save options", code, body)
	task, _ := f.q.GetTask(bg, f.task.ID)
	ds, _ := f.q.GetDataset(bg, f.ds.ID)
	if task.Title != "Suma" || task.ScoreMode != "max" || strings.Join(task.Languages, ",") != "c11" ||
		*ds.TimeLimitMs != 2000 || *ds.MemoryLimitBytes != 128<<20 || string(ds.ScoreTypeParams) != "25" || ds.Description != "v1" ||
		!strings.Contains(string(ds.TaskTypeParams), `"checker": "exact"`) {
		t.Fatalf("task %+v dataset %+v", task, ds)
	}
	code, body = b.Post(path+"/options", optionsForm(f, url.Values{"time_limit": {"soon"}, "title": {"Kept"}}))
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, `value="Kept"`) {
		t.Fatalf("invalid option: %d", code)
	}

	// problem.yaml: downloaded, then edited as text.
	code, yaml := b.Get(path + "/problem.yaml")
	if code != 200 || !strings.Contains(yaml, "time_limit: 2") || !strings.Contains(yaml, "checker: exact") || !strings.Contains(yaml, "points_per_test: 25") {
		t.Fatalf("problem.yaml %d:\n%s", code, yaml)
	}
	edited := strings.Replace(yaml, "time_limit: 2", "time_limit: 3", 1) + "public_tests: [\"1\"]\n"
	edited = strings.Replace(edited, "public_tests:\n    - \"0\"\n", "", 1)
	code, body = b.Post(path+"/problem.yaml", url.Values{"dataset_id": {fmt.Sprint(f.ds.ID)}, "yaml": {edited}})
	webtest.MustOK(t, "save problem.yaml", code, body)
	ds, _ = f.q.GetDataset(bg, f.ds.ID)
	tcs, _ := f.q.ListTestcases(bg, f.ds.ID)
	if *ds.TimeLimitMs != 3000 || tcs[0].Public || !tcs[1].Public {
		t.Fatalf("after problem.yaml: %d ms, %+v", *ds.TimeLimitMs, tcs)
	}
	code, body = b.Post(path+"/problem.yaml", url.Values{"yaml": {"name: sum\ntype: batch\ntime_limit: -1\nmemory_limit: 64\n"}})
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "time_limit: -1") || !strings.Contains(body, "time limits must be between") {
		t.Fatalf("invalid problem.yaml: %d", code)
	}
	code, _ = b.Post(path+"/problem.yaml", url.Values{"yaml": {"name: sum\ntype: batch\nweird: 1\n"}})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown key: %d", code)
	}

	// Files one by one: known by their names, or as the form says.
	code, body = b.PostMultipart(path+"/files", map[string]string{"dataset_id": fmt.Sprint(f.ds.ID), "kind": "auto"},
		webtest.File{Field: "files", Name: "es.md", Data: []byte("# Suma\n")},
		webtest.File{Field: "files", Name: "grader.cpp", Data: []byte("int main(){}")},
		webtest.File{Field: "files", Name: "data.txt", Data: []byte("1 2 3")})
	webtest.MustOK(t, "upload files", code, body)
	stmts, _ := f.q.ListStatements(bg, f.task.ID)
	managers, _ := f.q.ListManagers(bg, f.ds.ID)
	atts, _ := f.q.ListAttachments(bg, f.task.ID)
	if len(stmts) != 2 || stmts[1].Language != "es" || !strings.HasPrefix(stmts[1].ContentType, "text/markdown") || len(managers) != 2 || managers[1].Filename != "grader.cpp" ||
		len(atts) != 2 || atts[0].Filename != "data.txt" {
		t.Fatalf("statements %+v managers %+v attachments %+v", stmts, managers, atts)
	}
	code, _ = b.PostMultipart(path+"/files", map[string]string{"kind": "statement"}, webtest.File{Field: "files", Name: "statement.md", Data: []byte("x")})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("statement without a language: %d", code)
	}
	code, _ = b.PostMultipart(path+"/files", map[string]string{"kind": "attachment"}, webtest.File{Field: "files", Name: "checker.c", Data: []byte("x")})
	if atts, _ := f.q.ListAttachments(bg, f.task.ID); code != 200 || len(atts) != 3 {
		t.Fatalf("explicit attachment: %d %v", code, atts)
	}

	// Another dataset is shown with ?dataset=; /datasets/N leads there.
	code, body = b.Post(path+"/datasets", url.Values{"description": {"v2"}, "clone_from": {fmt.Sprint(f.ds.ID)}})
	webtest.MustOK(t, "new dataset", code, body)
	all, _ := f.q.ListDatasetsByTask(bg, f.task.ID)
	v2 := all[len(all)-1]
	if !strings.Contains(b.Last, fmt.Sprintf("%s?dataset=%d", path, v2.ID)) || !strings.Contains(body, "This is the dataset v2, not the live one") ||
		!strings.Contains(body, `name="dataset" data-autosubmit`) {
		t.Fatalf("second dataset at %s", b.Last)
	}
	b.Get(fmt.Sprintf("/datasets/%d", v2.ID))
	if !strings.Contains(b.Last, fmt.Sprintf("%s?dataset=%d", path, v2.ID)) {
		t.Fatalf("/datasets/N went to %s", b.Last)
	}
	code, _ = b.Post(fmt.Sprintf("/datasets/%d/rename", v2.ID), url.Values{"description": {"harder"}})
	if d, _ := f.q.GetDataset(bg, v2.ID); code != 200 || d.Description != "harder" {
		t.Fatalf("rename: %d %q", code, d.Description)
	}
	if code, _ := b.Get(fmt.Sprintf("%s?dataset=%d", path, v2.ID+100)); code != 200 {
		t.Fatalf("unknown dataset on a GET: %d", code)
	}
	if code, _ := b.Post(path+"/options", optionsForm(f, url.Values{"dataset_id": {fmt.Sprint(v2.ID + 100)}})); code != http.StatusUnprocessableEntity {
		t.Fatalf("options of an unknown dataset: %d", code)
	}
}

// TestFillFromPackage (D105): a zip checked in the Configuration window
// replaces the task's options, files and testcases in place.
func TestFillFromPackage(t *testing.T) {
	f := newFixture(t)
	b := f.login("task_setter")
	pkg := zipOf(t, map[string]string{
		"problem.yaml":    "name: suma\ntitle: Suma de dos\ntype: batch\ntime_limit: 1.5\nmemory_limit: 64\npublic_tests: [\"a\"]\n",
		"statement/es.md": "# Suma\n",
		"tests/a.in":      "1 2\n", "tests/a.out": "3\n",
		"tests/b.in": "5 5\n", "tests/b.out": "10\n",
		"tests/c.in": "0 0\n", "tests/c.out": "0\n",
	})
	fields := map[string]string{"step": "preview", "mode": "fill", "task_id": fmt.Sprint(f.task.ID), "dataset_id": fmt.Sprint(f.ds.ID)}
	code, body := b.PostMultipart("/tasks/import", fields, webtest.File{Field: "package", Name: "suma.zip", Data: pkg})
	webtest.MustOK(t, "preview", code, body)
	if !strings.Contains(body, "Into the task sum") || !strings.Contains(body, "Fill the task") {
		t.Fatalf("preview:\n%s", body)
	}
	if n, _ := f.q.CountTestcases(bg, f.ds.ID); n != 2 {
		t.Fatal("the preview changed the task")
	}
	dg := regexp.MustCompile(`name="digest" value="([0-9a-f]{64})"`).FindStringSubmatch(body)
	code, body = b.Post("/tasks/import", url.Values{"step": {"confirm"}, "digest": {dg[1]}, "file_name": {"suma.zip"}, "mode": {"fill"},
		"task_id": {fmt.Sprint(f.task.ID)}, "dataset_id": {fmt.Sprint(f.ds.ID)}})
	webtest.MustOK(t, "fill", code, body)
	if !strings.Contains(body, "The task was filled from the package: 3 testcases") || !strings.HasSuffix(b.Last, fmt.Sprintf("/tasks/%d", f.task.ID)) {
		t.Fatalf("after filling (%s):\n%s", b.Last, body)
	}
	task, _ := f.q.GetTask(bg, f.task.ID)
	ds, _ := f.q.GetDataset(bg, f.ds.ID)
	tcs, _ := f.q.ListTestcases(bg, f.ds.ID)
	stmts, _ := f.q.ListStatements(bg, f.task.ID)
	managers, _ := f.q.ListManagers(bg, f.ds.ID)
	atts, _ := f.q.ListAttachments(bg, f.task.ID)
	if task.Name != "suma" || task.Title != "Suma de dos" || *task.ActiveDatasetID != f.ds.ID || *ds.TimeLimitMs != 1500 || ds.Description != "v1" ||
		len(tcs) != 3 || !tcs[0].Public || tcs[1].Public || len(stmts) != 1 || stmts[0].Language != "es" || len(managers) != 0 || len(atts) != 0 {
		t.Fatalf("task %+v dataset %+v testcases %d statements %v managers %v attachments %v", task, ds, len(tcs), stmts, managers, atts)
	}
}

// TestTestsWindow (D105): the testcase list shows the newest test
// submissions testcase by testcase and refreshes while one is judged; a
// testcase has its own page; every testcase can go at once.
func TestTestsWindow(t *testing.T) {
	f := newFixture(t)
	b := f.login("task_setter")
	admin := f.admins["task_setter"]
	tcs, _ := f.q.ListTestcases(bg, f.ds.ID)
	lang := "c11"
	run, err := f.q.CreateTesterSubmission(bg, sqlc.CreateTesterSubmissionParams{TaskID: f.task.ID, Language: &lang, AdminID: admin.ID})
	if err != nil {
		t.Fatal(err)
	}
	f.q.EnsureSubmissionResult(bg, sqlc.EnsureSubmissionResultParams{SubmissionID: run.ID, DatasetID: f.ds.ID})
	ok := "ok"
	f.q.SetCompilationResult(bg, sqlc.SetCompilationResultParams{SubmissionID: run.ID, DatasetID: f.ds.ID, CompilationOutcome: &ok, TestcasesTotal: 2})
	secs := 0.25
	f.q.UpsertEvaluation(bg, sqlc.UpsertEvaluationParams{SubmissionID: run.ID, DatasetID: f.ds.ID, TestcaseID: tcs[0].ID, Outcome: 1,
		Text: "Output is correct", ExitStatus: "ok", ExecutionTime: &secs})
	f.q.UpsertEvaluation(bg, sqlc.UpsertEvaluationParams{SubmissionID: run.ID, DatasetID: f.ds.ID, TestcaseID: tcs[1].ID, Outcome: 0,
		Text: "Time limit exceeded", ExitStatus: "timeout"})
	score := 50.0
	f.q.SetScore(bg, sqlc.SetScoreParams{SubmissionID: run.ID, DatasetID: f.ds.ID, Score: &score, PublicScore: &score})

	path := fmt.Sprintf("/tasks/%d/tests", f.task.ID)
	code, page := b.Get(path)
	webtest.MustOK(t, "testcases window", code, page)
	for _, w := range []string{fmt.Sprintf(`<th class="run"><a href="/submissions/%d"`, run.ID), `<span class="v ok" title="Output is correct">AC</span> <small class="muted">0.250 s</small>`,
		`<span class="v bad" title="Time limit exceeded">TLE</span>`, fmt.Sprintf(`<a href="/testcases/%d">0</a>`, tcs[0].ID), "<b>50</b>", `id="examples"`} {
		if !strings.Contains(page, w) {
			t.Errorf("testcases window lacks %s", w)
		}
	}
	if strings.Contains(page, `hx-trigger="every 2s"`) {
		t.Error("nothing is pending, yet the list refreshes")
	}
	// A run being judged makes the list refresh by itself.
	pending, _ := f.q.CreateTesterSubmission(bg, sqlc.CreateTesterSubmissionParams{TaskID: f.task.ID, Language: &lang, AdminID: admin.ID})
	f.q.EnsureSubmissionResult(bg, sqlc.EnsureSubmissionResultParams{SubmissionID: pending.ID, DatasetID: f.ds.ID})
	if _, page := b.Get(path); !strings.Contains(page, `hx-trigger="every 2s"`) || !strings.Contains(page, "compiling") {
		t.Error("a pending run does not refresh the list")
	}
	// A test submission returns here.
	code, body := b.PostMultipart(fmt.Sprintf("/tasks/%d/tester", f.task.ID), map[string]string{"language": "c11"},
		webtest.File{Field: "sum.%l", Name: "sum.c", Data: []byte("int main(){}")})
	if code != 200 || !strings.Contains(b.Last, path) || !strings.Contains(body, "Test submission #") {
		t.Fatalf("test submission: %d %s", code, b.Last)
	}

	// One testcase: input and output, previous and next.
	code, body = b.Get(fmt.Sprintf("/testcases/%d", tcs[0].ID))
	if code != 200 || !strings.Contains(body, "<pre>1 2\n</pre>") || !strings.Contains(body, fmt.Sprintf(`<a href="/testcases/%d">next (1)</a>`, tcs[1].ID)) {
		t.Fatalf("testcase page %d:\n%s", code, body)
	}

	// Everything at once.
	if code, _ := b.Post(fmt.Sprintf("/datasets/%d/testcases/clear", f.ds.ID), nil); code != 200 {
		t.Fatalf("clear: %d", code)
	}
	if n, _ := f.q.CountTestcases(bg, f.ds.ID); n != 0 {
		t.Fatalf("%d testcases left", n)
	}
	if code, _ := f.login("read_only").Post(fmt.Sprintf("/datasets/%d/testcases/clear", f.ds.ID), nil); code != http.StatusForbidden {
		t.Fatalf("read-only clear: %d", code)
	}
}
