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
