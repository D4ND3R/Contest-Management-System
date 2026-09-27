package problempkg

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/testutil"
)

var bg = context.Background()

// fileDigests lists "path=sha256" of every file of a package.
func fileDigests(t *testing.T, p *Package) []string {
	t.Helper()
	var out []string
	add := func(prefix string, f File) {
		rd, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		buf.ReadFrom(rd)
		rd.Close()
		out = append(out, prefix+f.Name+"="+blob.Sum(buf.Bytes()))
	}
	for _, s := range p.Statements {
		add("statement/", s.File)
	}
	for _, tc := range p.Tests {
		add("in/", tc.Input)
		add("out/", tc.Output)
	}
	for _, m := range p.Managers {
		add("manager/", m)
	}
	for _, a := range p.Attachments {
		add("attachment/", a)
	}
	for _, e := range p.Examples {
		add("example/in/", e.Input)
		add("example/out/", e.Output)
		if e.Note != nil {
			add("example/note/", *e.Note)
		}
	}
	sort.Strings(out)
	return out
}

// TestImportExportRoundTrip imports every example package, exports it and
// reads it back: configuration and files must be identical (K18, K21).
func TestImportExportRoundTrip(t *testing.T) {
	pool := testutil.DB(t)
	q := sqlc.New(pool)
	store := blob.NewMem()
	o := options(t)
	now := time.Now()
	ct, err := q.CreateContest(bg, db.NewContestParams("omi", now, now.Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	// What the packages leave unset comes from the contest (SPEC_CLOSE B3).
	pool.Exec(bg, "UPDATE contests SET default_score_mode = 'max', score_precision = 2 WHERE id = $1", ct.ID)
	for _, name := range []string{"batch-suma", "interactive-adivina", "output-only-cuadrados", "communication-suma", "two-steps-binario"} {
		t.Run(name, func(t *testing.T) {
			p := Read(zipDir(t, filepath.Join(examples, name), ""), o)
			if !p.OK() {
				t.Fatal(p.Errors)
			}
			res, err := Import(bg, pool, store, p, ImportOptions{ContestID: &ct.ID})
			if err != nil {
				t.Fatal(err)
			}
			task, _ := q.GetTask(bg, res.TaskID)
			ds, _ := q.GetDataset(bg, res.DatasetID)
			if !res.NewTask || task.ContestID == nil || *task.ContestID != ct.ID || *task.ActiveDatasetID != ds.ID ||
				ds.TaskType != p.Config.TaskType() || ds.Description != "Default" {
				t.Fatalf("task %+v dataset %+v", task, ds)
			}
			if p.Config.ScoreMode == "" && (task.ScoreMode != "max" || task.ScorePrecision != 2) {
				t.Fatalf("contest defaults not applied: %s %d", task.ScoreMode, task.ScorePrecision)
			}
			var buf bytes.Buffer
			if err := Export(bg, q, store, task.ID, 0, &buf); err != nil {
				t.Fatal(err)
			}
			zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
			if err != nil {
				t.Fatal(err)
			}
			back := Read(zr, o)
			if !back.OK() || len(back.Warnings) > 0 {
				t.Fatalf("exported package: %v %v", back.Errors, back.Warnings)
			}
			if got, want := string(back.Config.TaskTypeParams()), string(p.Config.TaskTypeParams()); got != want {
				t.Fatalf("task type params %s, want %s", got, want)
			}
			if got, want := string(back.Config.ScoreTypeParams(len(back.Tests))), string(p.Config.ScoreTypeParams(len(p.Tests))); got != want {
				t.Fatalf("score params %s, want %s", got, want)
			}
			if back.MaxScore != p.MaxScore || back.Config.TimeLimit != p.Config.TimeLimit || back.Config.MemoryLimit != p.Config.MemoryLimit ||
				!reflect.DeepEqual(back.Config.Languages, p.Config.Languages) {
				t.Fatalf("config %+v, want %+v", back.Config, p.Config)
			}
			for i := range p.Tests {
				if back.Tests[i].Codename != p.Tests[i].Codename || back.Tests[i].Public != p.Tests[i].Public {
					t.Fatalf("test %d: %+v vs %+v", i, back.Tests[i], p.Tests[i])
				}
			}
			if got, want := fileDigests(t, back), fileDigests(t, p); !reflect.DeepEqual(got, want) {
				t.Fatalf("files differ:\n%v\n%v", got, want)
			}
		})
	}

	// The same package again: a new task is refused (name taken); as a new
	// dataset of the existing task it gets a fresh, not live, dataset and
	// leaves the statements alone.
	p := Read(zipDir(t, filepath.Join(examples, "batch-suma"), ""), o)
	if _, err := Import(bg, pool, store, p, ImportOptions{}); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("duplicate name: %v", err)
	}
	task, _ := q.GetTaskByName(bg, "suma")
	q.DeleteStatement(bg, sqlc.DeleteStatementParams{TaskID: task.ID, Language: "en"})
	res, err := Import(bg, pool, store, p, ImportOptions{TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	task2, _ := q.GetTask(bg, task.ID)
	stmts, _ := q.ListStatements(bg, task.ID)
	if res.NewTask || res.Dataset != "Default (2)" || *task2.ActiveDatasetID == res.DatasetID || len(stmts) != 1 {
		t.Fatalf("new dataset %+v, live %d, %d statements", res, *task2.ActiveDatasetID, len(stmts))
	}
	if n, _ := q.CountTestcases(bg, res.DatasetID); n != 4 {
		t.Fatalf("%d testcases", n)
	}
}

// TestFillAndApplyConfig fills a blank task from a package in place and
// then edits its problem.yaml: the task keeps its dataset, and everything
// the package describes replaces what the task had.
func TestFillAndApplyConfig(t *testing.T) {
	pool := testutil.DB(t)
	q := sqlc.New(pool)
	store := blob.NewMem()
	o := options(t)
	blank := func(name string) (sqlc.Task, sqlc.Dataset) {
		t.Helper()
		task, err := q.CreateTask(bg, db.NewTaskParams(name, name))
		if err != nil {
			t.Fatal(err)
		}
		ds, err := q.CreateDataset(bg, db.NewDatasetParams(task.ID, "Default"))
		if err != nil {
			t.Fatal(err)
		}
		if err := q.SetActiveDataset(bg, sqlc.SetActiveDatasetParams{ID: task.ID, ActiveDatasetID: &ds.ID}); err != nil {
			t.Fatal(err)
		}
		info, _ := store.PutBytes(bg, []byte("old\n"))
		q.UpsertTestcase(bg, sqlc.UpsertTestcaseParams{DatasetID: ds.ID, Codename: "old", InputDigest: info.Digest, OutputDigest: info.Digest})
		q.UpsertManager(bg, sqlc.UpsertManagerParams{DatasetID: ds.ID, Filename: "junk.txt", Digest: info.Digest})
		q.UpsertStatement(bg, sqlc.UpsertStatementParams{TaskID: task.ID, Language: "fr", Digest: info.Digest, ContentType: "text/markdown"})
		task, _ = q.GetTask(bg, task.ID)
		return task, ds
	}
	p := Read(zipDir(t, filepath.Join(examples, "batch-suma"), ""), o)
	if !p.OK() {
		t.Fatal(p.Errors)
	}
	task, ds := blank("nuevo")
	res, err := Fill(bg, pool, store, p, task.ID, ds.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, _ = q.GetTask(bg, task.ID)
	ds, _ = q.GetDataset(bg, ds.ID)
	if res.KeptName || task.Name != "suma" || task.Title != p.Config.Title || !reflect.DeepEqual(task.SubmissionFormat, []string{"suma.%l"}) ||
		!reflect.DeepEqual(task.Languages, p.Config.Languages) || *task.ActiveDatasetID != ds.ID {
		t.Fatalf("task %+v (%+v)", task, res)
	}
	if ds.ScoreType != "GroupMin" || *ds.TimeLimitMs != 1000 || ds.Description != "Default" {
		t.Fatalf("dataset %+v", ds)
	}
	tcs, _ := q.ListTestcases(bg, ds.ID)
	stmts, _ := q.ListStatements(bg, task.ID)
	managers, _ := q.ListManagers(bg, ds.ID)
	exs, _ := q.ListTaskExamples(bg, task.ID)
	if len(tcs) != len(p.Tests) || tcs[0].Codename == "old" || len(stmts) != len(p.Statements) || stmts[0].Language == "fr" ||
		len(managers) != len(p.Managers) || len(exs) != len(p.Examples) {
		t.Fatalf("%d testcases, %v statements, %d managers, %d examples", len(tcs), stmts, len(managers), len(exs))
	}
	for _, tc := range tcs {
		if tc.Public != (tc.Codename == "1_01") {
			t.Fatalf("testcase %s public %v", tc.Codename, tc.Public)
		}
	}

	// Another task: the name is taken, so it keeps its own.
	other, ods := blank("otro")
	res, err = Fill(bg, pool, store, p, other.ID, ods.ID)
	if err != nil {
		t.Fatal(err)
	}
	other, _ = q.GetTask(bg, other.ID)
	if !res.KeptName || other.Name != "otro" || !reflect.DeepEqual(other.SubmissionFormat, []string{"otro.%l"}) {
		t.Fatalf("kept name: %+v %+v", res, other)
	}
	if _, err := Fill(bg, pool, store, p, other.ID, ds.ID); err == nil {
		t.Fatal("a dataset of another task was filled")
	}

	// problem.yaml edited in the administration.
	codes, pub := make([]string, len(tcs)), make([]bool, len(tcs))
	for i, tc := range tcs {
		codes[i], pub[i] = tc.Codename, tc.Public
	}
	c, err := ConfigFromCMS(task, ds, codes, pub)
	if err != nil {
		t.Fatal(err)
	}
	c.TimeLimit, c.PublicTests, c.ShortCircuit = 2.5, []string{"2_.*"}, true
	if errs := c.Validate(codes); len(errs) > 0 {
		t.Fatal(errs)
	}
	if err := ApplyConfig(bg, q, task, ds, c, codes); err != nil {
		t.Fatal(err)
	}
	ds, _ = q.GetDataset(bg, ds.ID)
	tcs, _ = q.ListTestcases(bg, ds.ID)
	if *ds.TimeLimitMs != 2500 || !ds.ShortCircuit {
		t.Fatalf("dataset %+v", ds)
	}
	for _, tc := range tcs {
		if tc.Public != (tc.Codename[0] == '2') {
			t.Fatalf("testcase %s public %v", tc.Codename, tc.Public)
		}
	}
	c.Name = "otro"
	if err := ApplyConfig(bg, q, task, ds, c, codes); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("taken name: %v", err)
	}
	c.Name, c.Subtasks = "suma", []Subtask{{Points: 100, Tests: Tests{Regex: "("}}}
	if errs := c.Validate(codes); len(errs) == 0 {
		t.Fatal("an invalid subtask regex passed")
	}
}
