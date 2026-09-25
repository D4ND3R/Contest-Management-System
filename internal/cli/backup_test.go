package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/testutil"
)

// TestDumpVerifyRestore runs "cmsctl dump", "backup-verify", "backups" and
// "restore" into an empty database with an empty blob store.
func TestDumpVerifyRestore(t *testing.T) {
	ctx := context.Background()
	pool, dbURL := testutil.DBWithURL(t)
	dir := t.TempDir()
	blobs := filepath.Join(dir, "blobs")
	st, _ := blob.NewLocal(blobs, false)
	info, _ := blob.NewTracked(st, sqlc.New(pool)).PutBytes(ctx, []byte("contenido"))
	if _, err := sqlc.New(pool).CreateUser(ctx, sqlc.CreateUserParams{Username: "ana", PasswordHash: "h", PreferredLanguages: []string{}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CMS_CONFIG", "")
	t.Setenv("CMS_DATABASE_URL", dbURL)
	t.Setenv("CMS_BLOB_BACKEND", "local")
	t.Setenv("CMS_BLOB_DIR", blobs)
	t.Setenv("CMS_BACKUP_DIR", filepath.Join(dir, "backups"))
	run := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := Main(append([]string{"ctl"}, args...), &out, &errb)
		return code, out.String(), errb.String()
	}
	file := filepath.Join(dir, "b.tar.zst")
	if code, _, errs := run("dump", "-o", file, "-max-rate", "0"); code != 0 {
		t.Fatalf("dump: %s", errs)
	}
	if code, out, errs := run("backup-verify", file); code != 0 || !strings.HasPrefix(out, "OK:") {
		t.Fatalf("verify: %s %s", out, errs)
	}
	// Into backup.dir, listed, with its whole-file digest checked.
	code, out, errs := run("dump")
	if code != 0 || !strings.Contains(out, "sha256 ") {
		t.Fatalf("dump to dir: %s %s", out, errs)
	}
	if code, out, _ := run("backups"); code != 0 || !strings.Contains(out, "-cli.tar.zst") || !strings.Contains(out, "done") {
		t.Fatalf("backups: %s", out)
	}
	ents, _ := filepath.Glob(filepath.Join(dir, "backups", "*.tar.zst"))
	if len(ents) != 1 {
		t.Fatalf("backup dir: %v", ents)
	}
	if code, out, errs := run("backup-verify", ents[0]); code != 0 {
		t.Fatalf("verify dir backup: %s %s", out, errs)
	}
	b, _ := os.ReadFile(ents[0])
	os.WriteFile(ents[0], append(b, 0), 0o600) // trailing garbage changes the file digest
	if code, _, errs := run("backup-verify", ents[0]); code == 0 || !strings.Contains(errs, "does not match") {
		t.Fatalf("damaged file accepted: %s", errs)
	}

	// Restore into an empty database and store.
	_, emptyURL := testutil.EmptyDB(t)
	t.Setenv("CMS_DATABASE_URL", emptyURL)
	t.Setenv("CMS_BLOB_DIR", filepath.Join(dir, "blobs2"))
	if code, out, errs := run("restore", file); code != 0 || !strings.Contains(out, "restored the backup") {
		t.Fatalf("restore: %s %s", out, errs)
	}
	if code, _, errs := run("restore", file); code == 0 || !strings.Contains(errs, "already holds data") {
		t.Fatalf("second restore: %s", errs)
	}
	if code, _, errs := run("restore", "-force", file); code != 0 {
		t.Fatalf("forced restore: %s", errs)
	}
	st2, _ := blob.NewLocal(filepath.Join(dir, "blobs2"), false)
	if got, err := blob.ReadAll(ctx, st2, info.Digest); err != nil || string(got) != "contenido" {
		t.Fatalf("restored blob %q %v", got, err)
	}
}
