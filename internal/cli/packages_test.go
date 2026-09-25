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
