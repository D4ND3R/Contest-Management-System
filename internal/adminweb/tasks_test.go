package adminweb

import (
	"archive/zip"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/webtest"
)

// taskForm is a valid task update form for the fixture's task.
func taskForm(f *fixture, extra url.Values) url.Values {
	v := url.Values{"name": {f.task.Name}, "title": {f.task.Title}, "submission_format": {"sum.%l"}, "token_mode": {"disabled"},
		"token_gen_interval_s": {"1800"}, "feedback_level": {"full"}, "score_mode": {"max_subtask"}, "score_precision": {"0"}}
	for k, x := range extra {
		v[k] = x
	}
	return v
}

// TestTaskLanguages covers the per-task language list (K10).
func TestTaskLanguages(t *testing.T) {
	f := newFixture(t)
	b := f.login("all")
	path := fmt.Sprintf("/tasks/%d", f.task.ID)
	code, body := b.Post(path, taskForm(f, url.Values{"languages": {"cpp17", "python3"}}))
	webtest.MustOK(t, "save languages", code, body)
	tk, _ := f.q.GetTask(bg, f.task.ID)
	if strings.Join(tk.Languages, ",") != "cpp17,python3" {
		t.Fatalf("languages %v", tk.Languages)
	}
	if !strings.Contains(body, `value="cpp17" checked`) || strings.Contains(body, `value="c11" checked`) {
		t.Fatal("the form does not show the stored languages")
	}
	// The tester offers the task's languages only.
	if i := strings.Index(body, `id="tester"`); i < 0 || strings.Contains(body[i:], `<option value="c11">`) {
		t.Fatal("tester language choices")
	}
	code, _ = b.Post(path, taskForm(f, url.Values{"languages": {"brainfuck"}}))
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown language = %d", code)
	}
	// No language checked: back to every language of the contest.
	code, body = b.Post(path, taskForm(f, nil))
	webtest.MustOK(t, "clear languages", code, body)
	if tk, _ := f.q.GetTask(bg, f.task.ID); len(tk.Languages) != 0 {
		t.Fatalf("languages %v", tk.Languages)
	}
}

// datasetForm is a valid Batch dataset form with the score editor fields.
func datasetForm(scoreType string, extra url.Values) url.Values {
	v := url.Values{"description": {"v1"}, "time_limit": {"1"}, "memory_limit_mib": {"256"}, "process_limit": {"1"},
		"task_type": {"Batch"}, "tt_checker": {"white_diff"}, "score_type": {scoreType}, "score_editor": {"1"}}
	for k, x := range extra {
		v[k] = x
	}
	return v
}

// TestSubtaskEditor covers the visual score editor (K5).
func TestSubtaskEditor(t *testing.T) {
	f := newFixture(t)
	for _, c := range []string{"2", "3"} {
		f.q.UpsertTestcase(bg, sqlc.UpsertTestcaseParams{DatasetID: f.ds.ID, Codename: c, InputDigest: f.put("1"), OutputDigest: f.put("1")})
	}
	b := f.login("all")
	path := fmt.Sprintf("/datasets/%d", f.ds.ID)
	code, body := b.Get(path)
	webtest.MustOK(t, "dataset", code, body)
	if !strings.Contains(body, `id="score-editor"`) || !strings.Contains(body, `name="sum_points" value="50"`) {
		t.Fatalf("editor missing:\n%s", body)
	}

	// Three subtasks: a regex, a hand-picked list and a count; the empty
	// last row is ignored.
	form := datasetForm("GroupMin", url.Values{"st_n": {"4"},
		"st0_score": {"20"}, "st0_mode": {"regex"}, "st0_regex": {"[01]"},
		"st1_score": {"30"}, "st1_mode": {"list"}, "st1_tc": {"1", "2"},
		"st2_score": {"50"}, "st2_mode": {"count"}, "st2_count": {"4"}})
	code, body = b.Post(path, form)
	webtest.MustOK(t, "save subtasks", code, body)
	ds, _ := f.q.GetDataset(bg, f.ds.ID)
	if ds.ScoreType != "GroupMin" || string(ds.ScoreTypeParams) != `[[20, "[01]"], [30, ["1", "2"]], [50, 4]]` {
		t.Fatalf("stored %s %s", ds.ScoreType, ds.ScoreTypeParams)
	}
	if !strings.Contains(body, "Maximum score <b>100</b>") || !strings.Contains(body, `name="st2_count" value="4"`) {
		t.Fatalf("page after saving:\n%s", body)
	}

	// Removing a row and adding a threshold (GroupThreshold).
	form = datasetForm("GroupThreshold", url.Values{"st_n": {"3"},
		"st0_score": {"40"}, "st0_mode": {"regex"}, "st0_regex": {"[0-3]"}, "st0_threshold": {"0.5"},
		"st1_score": {"60"}, "st1_mode": {"regex"}, "st1_regex": {"3"}, "st1_threshold": {"1"}, "st1_remove": {"on"}})
	code, body = b.Post(path, form)
	webtest.MustOK(t, "threshold", code, body)
	if ds, _ = f.q.GetDataset(bg, f.ds.ID); string(ds.ScoreTypeParams) != `[[40, "[0-3]", 0.5]]` {
		t.Fatalf("stored %s", ds.ScoreTypeParams)
	}

	// Invalid rows are refused with the subtask number, nothing is saved.
	for _, bad := range []url.Values{
		{"st_n": {"1"}, "st0_score": {"10"}, "st0_mode": {"list"}},
		{"st_n": {"1"}, "st0_score": {"x"}, "st0_mode": {"regex"}, "st0_regex": {"0"}},
		{"st_n": {"1"}, "st0_score": {"10"}, "st0_mode": {"regex"}, "st0_regex": {"("}},
		{"st_n": {"1"}},
	} {
		code, body = b.Post(path, datasetForm("GroupMin", bad))
		if code != http.StatusUnprocessableEntity || !strings.Contains(body, "subtask") {
			t.Fatalf("invalid rows %v = %d", bad, code)
		}
	}
	if ds, _ = f.q.GetDataset(bg, f.ds.ID); ds.ScoreType != "GroupThreshold" {
		t.Fatal("an invalid form was saved")
	}

	// Sum through the editor.
	code, _ = b.Post(path, datasetForm("Sum", url.Values{"sum_points": {"25"}, "st_n": {"0"}}))
	if ds, _ = f.q.GetDataset(bg, f.ds.ID); code != 200 || string(ds.ScoreTypeParams) != "25" {
		t.Fatalf("sum: %d %s", code, ds.ScoreTypeParams)
	}

	// The live preview renders only the editor and saves nothing.
	before, _ := f.q.ListAuditLog(bg, sqlc.ListAuditLogParams{Limit: 100})
	code, body = b.Post(path+"/score-editor", datasetForm("GroupMin", url.Values{"st_n": {"2"},
		"st0_score": {"70"}, "st0_mode": {"regex"}, "st0_regex": {"[12]"},
		"st1_score": {"30"}, "st1_mode": {"regex"}, "st1_regex": {"2|3"}}))
	if code != 200 || strings.Contains(body, "<html") || !strings.Contains(body, "<b>2</b>") ||
		!strings.Contains(body, "Testcases in no subtask: 0") || !strings.Contains(body, "Testcases in several subtasks: 2") ||
		!strings.Contains(body, "Total: 100 points.") || !strings.Contains(body, `name="st2_score"`) {
		t.Fatalf("preview = %d\n%s", code, body)
	}
	if after, _ := f.q.ListAuditLog(bg, sqlc.ListAuditLogParams{Limit: 100}); len(after) != len(before) {
		t.Fatal("the preview was audited")
	}
	if ds2, _ := f.q.GetDataset(bg, f.ds.ID); string(ds2.ScoreTypeParams) != "25" {
		t.Fatal("the preview saved")
	}
	// Errors show per row.
	_, body = b.Post(path+"/score-editor", datasetForm("GroupMin", url.Values{"st_n": {"1"}, "st0_score": {"5"}, "st0_mode": {"regex"}, "st0_regex": {"zz"}}))
	if !strings.Contains(body, "matches no testcase") {
		t.Fatalf("preview error:\n%s", body)
	}
	// Read-only administrators cannot use it.
	ro := f.login("read_only")
	if code, _ := ro.Post(path+"/score-editor", datasetForm("Sum", nil)); code != http.StatusForbidden {
		t.Fatalf("read-only preview = %d", code)
	}
}

// TestPackageImportPreview covers the admin side of problem packages
// without judging (K19, K21): per-file errors before creating anything,
// name conflicts, the dataset mode, permissions and the export.
func TestPackageImportPreview(t *testing.T) {
	f := newFixture(t)
	b := f.login("all")
	code, body := b.Get("/tasks/import")
	if code != 200 || !strings.Contains(body, "data-dropzone") || !strings.Contains(body, `name="package"`) {
		t.Fatalf("form = %d", code)
	}
	tasksBefore, _ := f.q.ListTasks(bg)
	auditBefore, _ := f.q.ListAuditLog(bg, sqlc.ListAuditLogParams{Limit: 100})

	// A broken package: every problem is listed with its file, nothing is
	// created and there is nothing to confirm.
	bad := zipOf(t, map[string]string{"problem.yaml": "name: nuevo\ntype: batch\ntime_limit: 1\nmemory_limit: 64\nchecker: custom\n",
		"tests/1.in": "1", "statement/xx-long-name.pdf": "%PDF", "solutions/main.c": "int main(){}"})
	code, body = b.PostMultipart("/tasks/import", map[string]string{"step": "preview", "mode": "task"},
		webtest.File{Field: "package", Name: "nuevo.zip", Data: bad})
	for _, want := range []string{"Problems to fix", "tests/1.in", "no expected output", "statement/xx-long-name.pdf",
		"solutions/main.c", "start the name with the expected verdict", "needs checker"} {
		if !strings.Contains(body, want) {
			t.Errorf("preview lacks %q", want)
		}
	}
	if code != 200 || strings.Contains(body, "Create the task") {
		t.Fatalf("broken preview = %d", code)
	}
	// With subtasks but no usable testcase (every expected output missing):
	// the subtasks are listed without coverage, the problems too.
	bad = zipOf(t, map[string]string{"problem.yaml": "name: nuevo\ntype: batch\ntime_limit: 1\nmemory_limit: 64\nscoring: group_min\nsubtasks:\n  - {points: 40, tests: \"1_.*\"}\n  - {points: 60, tests: \"2_.*\"}\n",
		"tests/1_01.in": "1", "tests/2_01.in": "2"})
	code, body = b.PostMultipart("/tasks/import", map[string]string{"step": "preview", "mode": "task"},
		webtest.File{Field: "package", Name: "nuevo.zip", Data: bad})
	if code != 200 || !strings.Contains(body, "tests/2_01.in") || !strings.Contains(body, "no expected output") || !strings.Contains(body, "Subtasks") {
		t.Fatalf("subtasks without testcases = %d\n%s", code, body)
	}
	// Not a zip at all.
	code, body = b.PostMultipart("/tasks/import", map[string]string{"step": "preview"}, webtest.File{Field: "package", Name: "x.zip", Data: []byte("hello")})
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "not a zip archive") {
		t.Fatalf("not a zip = %d", code)
	}

	// A valid package named like the fixture's task: conflict in task mode.
	good := zipOf(t, map[string]string{"sum/problem.yaml": "name: sum\ntitle: Otra suma\ntime_limit: 2\nmemory_limit: 128\n",
		"sum/tests/a.in": "1 2\n", "sum/tests/a.out": "3\n", "sum/statement/es.pdf": "%PDF-1.4"})
	code, body = b.PostMultipart("/tasks/import", map[string]string{"step": "preview", "mode": "task"},
		webtest.File{Field: "package", Name: "sum.zip", Data: good})
	dg := regexp.MustCompile(`name="digest" value="([0-9a-f]{64})"`).FindStringSubmatch(body)
	if code != 200 || dg == nil || !strings.Contains(body, "already exists") || strings.Contains(body, "Create the task") {
		t.Fatalf("conflict preview = %d\n%s", code, body)
	}
	if code, _ := b.Post("/tasks/import", url.Values{"step": {"confirm"}, "digest": {dg[1]}, "mode": {"task"}}); code != http.StatusUnprocessableEntity {
		t.Fatalf("confirming a conflict = %d", code)
	}
	if tasks, _ := f.q.ListTasks(bg); len(tasks) != len(tasksBefore) {
		t.Fatal("a task was created")
	}
	if audit, _ := f.q.ListAuditLog(bg, sqlc.ListAuditLogParams{Limit: 100}); len(audit) != len(auditBefore) {
		t.Fatal("previews were audited")
	}
	// Read-only administrators cannot import.
	if code, _ := f.login("read_only").Post("/tasks/import", url.Values{"step": {"confirm"}, "digest": {dg[1]}}); code != http.StatusForbidden {
		t.Fatalf("read-only import = %d", code)
	}
	// As a new dataset of the existing task: created, not live, audited.
	code, body = b.Post("/tasks/import", url.Values{"step": {"confirm"}, "digest": {dg[1]}, "mode": {"dataset"}, "task_id": {fmt.Sprint(f.task.ID)}})
	webtest.MustOK(t, "import dataset", code, body)
	dss, _ := f.q.ListDatasetsByTask(bg, f.task.ID)
	nd := dss[len(dss)-1]
	if len(dss) != 2 || nd.Description != "Default" || *nd.TimeLimitMs != 2000 || *nd.MemoryLimitBytes != 128<<20 || string(nd.ScoreTypeParams) != "100" {
		t.Fatalf("datasets %+v", dss)
	}
	if tk, _ := f.q.GetTask(bg, f.task.ID); *tk.ActiveDatasetID == nd.ID || tk.Title != "Sum" {
		t.Fatal("the task changed")
	}
	if audit, _ := f.q.ListAuditLog(bg, sqlc.ListAuditLogParams{Limit: 1}); audit[0].Action != "task.import" {
		t.Fatalf("audit %+v", audit[0])
	}

	// Export of the new dataset.
	code, body = b.Get(fmt.Sprintf("/tasks/%d/export.zip?dataset=%d", f.task.ID, nd.ID))
	if code != 200 || !strings.HasPrefix(body, "PK") {
		t.Fatalf("export = %d", code)
	}
	zr, _ := zip.NewReader(strings.NewReader(body), int64(len(body)))
	names := map[string]bool{}
	for _, zf := range zr.File {
		names[zf.Name] = true
	}
	for _, n := range []string{"problem.yaml", "tests/a.in", "tests/a.out", "statement/en.pdf", "attachments/sample.zip"} {
		if !names[n] {
			t.Errorf("export lacks %s (has %v)", n, names)
		}
	}
	if code, _ := b.Get(fmt.Sprintf("/tasks/%d/export.zip?dataset=999999", f.task.ID)); code != 404 {
		t.Fatalf("foreign dataset = %d", code)
	}
	// The validation page renders without solutions.
	code, body = b.Get(fmt.Sprintf("/tasks/%d/validation", f.task.ID))
	if code != 200 || !strings.Contains(body, "No reference solutions") {
		t.Fatalf("validation = %d", code)
	}
}
