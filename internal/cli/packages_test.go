package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/testutil"
)

// TestTaskImportExport runs "cmsctl task-import" (dry run, import, as a
// dataset) and "cmsctl task-export" on an example package.
func TestTaskImportExport(t *testing.T) {
	pool, dbURL := testutil.DBWithURL(t)
	dir := t.TempDir()
	t.Setenv("CMS_CONFIG", "")
	t.Setenv("CMS_DATABASE_URL", dbURL)
	t.Setenv("CMS_BLOB_BACKEND", "local")
	t.Setenv("CMS_BLOB_DIR", filepath.Join(dir, "blobs"))
	t.Setenv("CMS_LANGUAGES_DIR", filepath.Join("..", "..", "config", "languages"))

	// Zip the batch example.
	src := filepath.Join("..", "..", "docs", "examples", "packages", "batch-suma")
	pkg := filepath.Join(dir, "suma.zip")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		b, _ := os.ReadFile(p)
		w, _ := zw.Create(filepath.ToSlash(rel))
		w.Write(b)
		return nil
	})
	zw.Close()
	os.WriteFile(pkg, buf.Bytes(), 0o644)

	run := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := Main(append([]string{"ctl"}, args...), &out, &errb)
		return code, out.String(), errb.String()
	}
	q := sqlc.New(pool)
	ctx := context.Background()
	if code, out, errs := run("task-import", "-dry-run", pkg); code != 0 || !strings.Contains(out, "suma (Batch, GroupMin): 4 testcases") {
		t.Fatalf("dry run: %d %s %s", code, out, errs)
	}
	if _, err := q.GetTaskByName(ctx, "suma"); err == nil {
		t.Fatal("the dry run imported")
	}
	if code, out, errs := run("task-import", pkg); code != 0 || !strings.Contains(out, "imported: task") {
		t.Fatalf("import: %d %s %s", code, out, errs)
	}
	if code, _, errs := run("task-import", pkg); code != 1 || !strings.Contains(errs, "-task suma") {
		t.Fatalf("second import: %d %s", code, errs)
	}
	if code, out, errs := run("task-import", "-task", "suma", pkg); code != 0 || !strings.Contains(out, "Default (2)") {
		t.Fatalf("as a dataset: %d %s %s", code, out, errs)
	}
	outZip := filepath.Join(dir, "out.zip")
	if code, out, errs := run("task-export", "suma", outZip); code != 0 || !strings.Contains(out, "exported suma") {
		t.Fatalf("export: %d %s %s", code, out, errs)
	}
	if code, out, errs := run("task-import", "-dry-run", outZip); code != 0 || !strings.Contains(out, "4 testcases, maximum score 100") {
		t.Fatalf("exported package: %d %s %s", code, out, errs)
	}
	// A broken package lists its problems and imports nothing.
	bad := filepath.Join(dir, "bad.zip")
	buf.Reset()
	zw = zip.NewWriter(&buf)
	w, _ := zw.Create("problem.yaml")
	w.Write([]byte("name: bad\ntime_limit: 1\nmemory_limit: 1\n"))
	zw.Close()
	os.WriteFile(bad, buf.Bytes(), 0o644)
	if code, _, errs := run("task-import", bad); code != 1 || !strings.Contains(errs, "error: tests/: no testcases") {
		t.Fatalf("bad package: %d %s", code, errs)
	}
}

// zipDir zips a directory (members relative to it) into a file.
func zipDir(t *testing.T, src, dst string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		b, _ := os.ReadFile(p)
		w, _ := zw.Create(filepath.ToSlash(rel))
		w.Write(b)
		return nil
	})
	zw.Close()
	if err := os.WriteFile(dst, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestContestArchiveCommands converts other formats with "task-import",
// then runs "cmsctl contest-export" and "contest-import" (D7).
func TestContestArchiveCommands(t *testing.T) {
	pool, dbURL := testutil.DBWithURL(t)
	dir := t.TempDir()
	t.Setenv("CMS_CONFIG", "")
	t.Setenv("CMS_DATABASE_URL", dbURL)
	t.Setenv("CMS_BLOB_BACKEND", "local")
	t.Setenv("CMS_BLOB_DIR", filepath.Join(dir, "blobs"))
	t.Setenv("CMS_LANGUAGES_DIR", filepath.Join("..", "..", "config", "languages"))
	run := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := Main(append([]string{"ctl"}, args...), &out, &errb)
		return code, out.String(), errb.String()
	}
	ctx := context.Background()
	q := sqlc.New(pool)
	now := time.Now()
	if _, err := q.CreateContest(ctx, db.NewContestParams("final", now.Add(-time.Hour), now.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	others := filepath.Join("..", "..", "docs", "examples", "other-formats")
	for name, want := range map[string]string{"italy-suma": "isuma (Batch, ", "polygon-suma": "psuma (Batch, "} {
		pkg := filepath.Join(dir, name+".zip")
		zipDir(t, filepath.Join(others, name), pkg)
		if code, out, errs := run("task-import", "-contest", "final", pkg); code != 0 || !strings.Contains(out, want) {
			t.Fatalf("%s: %d %s %s", name, code, out, errs)
		}
	}
	arch := filepath.Join(dir, "final.zip")
	if code, out, errs := run("contest-export", "final", arch); code != 0 || !strings.Contains(out, "exported final") {
		t.Fatalf("export: %d %s %s", code, out, errs)
	}
	if code, _, errs := run("contest-import", arch); code != 1 || !strings.Contains(errs, "a contest named final exists") {
		t.Fatalf("second import: %d %s", code, errs)
	}
	if code, out, errs := run("contest-import", "-name", "final-2027", "-task-suffix", "-2027", "-status", "draft", arch); code != 0 || !strings.Contains(out, "imported final as contest") {
		t.Fatalf("import: %d %s %s", code, out, errs)
	}
	c, err := q.GetContestByName(ctx, "final-2027")
	if err != nil || c.Status != "draft" {
		t.Fatalf("imported contest: %+v %v", c, err)
	}
	for _, name := range []string{"isuma-2027", "psuma-2027"} {
		task, err := q.GetTaskByName(ctx, name)
		if err != nil || task.ContestID == nil || *task.ContestID != c.ID || task.ActiveDatasetID == nil {
			t.Fatalf("task %s: %+v %v", name, task, err)
		}
	}
}
