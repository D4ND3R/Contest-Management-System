package contestconfig

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/testutil"
)

var bg = context.Background()

func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const contestYAML = `format: 1
name: ioi-cfg
settings:
  description: Configured from Git
  start_time: 2030-07-01T09:00:00Z
  stop_time: "2030-07-01T14:00:00+00:00"
  allow_printing: true
  max_submission_number: 50
  languages: [C++17 / g++]
tasks: [sum, tree]
`

func pkg(name, answer string) map[string]string {
	p := "tasks/" + name + "/"
	return map[string]string{
		p + "problem.yaml":              "name: " + name + "\ntitle: Task " + name + "\ntype: batch\ntime_limit: 1\nmemory_limit: 64\n",
		p + "tests/1.in":                "1 2\n",
		p + "tests/1.out":               answer,
		p + "statement/en.md":           "# " + name + "\n",
		p + "statement/examples/01.in":  "1 2\n",
		p + "statement/examples/01.out": answer,
	}
}

func taskNames(t *testing.T, q *sqlc.Queries, contestID int64) string {
	ts, err := q.ListTasksByContest(bg, &contestID)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tk := range ts {
		names = append(names, tk.Name)
	}
	return strings.Join(names, ",")
}

func TestApplyAndExport(t *testing.T) {
	pool := testutil.DB(t)
	q := sqlc.New(pool)
	store := blob.NewMem()
	dir := t.TempDir()
	write(t, dir, map[string]string{FileName: contestYAML})
	write(t, dir, pkg("sum", "3\n"))
	write(t, dir, pkg("tree", "7\n"))
	before := time.Date(2030, 6, 1, 0, 0, 0, 0, time.UTC)
	apply := func(d string, o Options) *Report {
		t.Helper()
		if o.Now.IsZero() {
			o.Now = before
		}
		rep, err := Apply(bg, pool, store, d, o)
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}

	// A dry run writes nothing.
	rep := apply(dir, Options{DryRun: true})
	if !rep.Created || len(rep.Tasks) != 2 || rep.Tasks[0].Action != "created" {
		t.Fatalf("dry run %+v", rep)
	}
	if _, err := q.GetContestByName(bg, "ioi-cfg"); err == nil {
		t.Fatal("the dry run created the contest")
	}

	rep = apply(dir, Options{})
	if !rep.Created || !rep.Changed() {
		t.Fatalf("first apply %+v", rep)
	}
	c, err := q.GetContestByName(bg, "ioi-cfg")
	if err != nil {
		t.Fatal(err)
	}
	if c.Description != "Configured from Git" || !c.AllowPrinting || c.MaxSubmissionNumber == nil || *c.MaxSubmissionNumber != 50 ||
		!c.StopTime.Equal(time.Date(2030, 7, 1, 14, 0, 0, 0, time.UTC)) || len(c.Languages) != 1 {
		t.Fatalf("contest %+v", c)
	}
	if got := taskNames(t, q, c.ID); got != "sum,tree" {
		t.Fatalf("tasks %s", got)
	}

	// Idempotent.
	rep = apply(dir, Options{})
	if rep.Changed() || len(rep.Settings) != 0 {
		t.Fatalf("second apply changed %+v", rep)
	}

	// Export is deterministic and applying it changes nothing.
	out1, out2 := t.TempDir(), t.TempDir()
	for _, d := range []string{out1, out2} {
		if err := Export(bg, q, store, "ioi-cfg", d); err != nil {
			t.Fatal(err)
		}
	}
	if a, b := tree(t, out1), tree(t, out2); a != b {
		t.Fatalf("exports differ:\n%s\n%s", a, b)
	}
	if rep := apply(out1, Options{}); rep.Changed() {
		t.Fatalf("applying the export changed %+v", rep)
	}
	y, _ := os.ReadFile(filepath.Join(out1, FileName))
	if !strings.Contains(string(y), "start_time: \"2030-07-01T09:00:00Z\"") || strings.Contains(string(y), "invitation_code") {
		t.Fatalf("contest.yaml:\n%s", y)
	}

	// After the start, a changed task gets a new dataset that is not live,
	// its statements follow the package, and settings changes are listed.
	write(t, dir, map[string]string{"tasks/tree/tests/1.out": "8\n", "tasks/tree/statement/es.md": "# tree\n",
		FileName: strings.Replace(contestYAML, "max_submission_number: 50", "max_submission_number: 40", 1)})
	os.Remove(filepath.Join(dir, "tasks/tree/statement/en.md"))
	during := time.Date(2030, 7, 1, 10, 0, 0, 0, time.UTC)
	rep = apply(dir, Options{Now: during})
	if len(rep.Settings) != 1 || rep.Settings[0] != "max_submission_number" {
		t.Fatalf("settings %v", rep.Settings)
	}
	if rep.Tasks[0].Action != "unchanged" || rep.Tasks[1].Action != "updated" || rep.Tasks[1].Live || len(rep.LiveChanged) != 0 {
		t.Fatalf("tasks %+v", rep.Tasks)
	}
	tree, _ := q.GetTaskByName(bg, "tree")
	ds, _ := q.ListDatasetsByTask(bg, tree.ID)
	if len(ds) != 2 || *tree.ActiveDatasetID != ds[0].ID {
		t.Fatalf("datasets %+v live %d", ds, *tree.ActiveDatasetID)
	}
	stmts, _ := q.ListStatements(bg, tree.ID)
	if len(stmts) != 1 || stmts[0].Language != "es" {
		t.Fatalf("statements %+v", stmts)
	}
	// Applying again imports nothing: the content waits in that dataset,
	// which -activate makes live.
	rep = apply(dir, Options{Now: during})
	if rep.Changed() || rep.Tasks[1].Action != "pending" || rep.Tasks[1].Dataset != ds[1].Description {
		t.Fatalf("pending %+v", rep.Tasks)
	}
	rep = apply(dir, Options{Now: during, Activate: true})
	if rep.Tasks[1].Action != "activated" || !rep.Tasks[1].Live || len(rep.LiveChanged) != 1 {
		t.Fatalf("activate %+v", rep.Tasks)
	}
	if ds, _ = q.ListDatasetsByTask(bg, tree.ID); len(ds) != 2 {
		t.Fatalf("activation imported again: %d datasets", len(ds))
	}
	if rep := apply(dir, Options{Now: during}); rep.Changed() {
		t.Fatalf("not idempotent after activation %+v", rep)
	}

	// Reordering, and a task of the contest that the file does not name.
	write(t, dir, map[string]string{FileName: strings.Replace(contestYAML, "tasks: [sum, tree]", "tasks: [tree]", 1)})
	rep = apply(dir, Options{Now: during})
	if !rep.Reordered || len(rep.Unlisted) != 1 || rep.Unlisted[0] != "sum" {
		t.Fatalf("reorder %+v", rep)
	}
	if got := taskNames(t, q, c.ID); got != "tree,sum" {
		t.Fatalf("tasks %s", got)
	}
	if rep := apply(dir, Options{Now: during}); rep.Reordered {
		t.Fatal("reordered twice")
	}
}

func TestApplyRejects(t *testing.T) {
	pool := testutil.DB(t)
	store := blob.NewMem()
	for name, tc := range map[string]struct{ files map[string]string }{
		"secret":        {map[string]string{FileName: strings.Replace(contestYAML, "settings:\n", "settings:\n  invitation_code: abc\n", 1)}},
		"unknown":       {map[string]string{FileName: strings.Replace(contestYAML, "settings:\n", "settings:\n  colour: red\n", 1)}},
		"bad type":      {map[string]string{FileName: strings.Replace(contestYAML, "allow_printing: true", "allow_printing: often", 1)}},
		"no times":      {map[string]string{FileName: "name: x\ntasks: []\n"}},
		"missing task":  {map[string]string{FileName: strings.Replace(contestYAML, "[sum, tree]", "[sum, nope]", 1)}},
		"wrong name":    {map[string]string{"tasks/sum/problem.yaml": "name: other\ntype: batch\ntime_limit: 1\nmemory_limit: 64\n"}},
		"path in names": {map[string]string{FileName: strings.Replace(contestYAML, "[sum, tree]", "[../sum]", 1)}},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, map[string]string{FileName: contestYAML})
			write(t, dir, pkg("sum", "3\n"))
			write(t, dir, pkg("tree", "7\n"))
			write(t, dir, tc.files)
			if _, err := Apply(bg, pool, store, dir, Options{}); err == nil {
				t.Fatal("accepted")
			}
			if _, err := sqlc.New(pool).GetContestByName(bg, "ioi-cfg"); err == nil {
				t.Fatal("something was applied")
			}
		})
	}
}

// tree lists a directory's files and contents.
func tree(t *testing.T, dir string) string {
	var b bytes.Buffer
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, _ := os.ReadFile(p)
		rel, _ := filepath.Rel(dir, p)
		b.WriteString(rel + "\n" + string(data) + "\n")
		return nil
	})
	return b.String()
}
