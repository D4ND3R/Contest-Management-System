package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/testutil"
)

// TestUpgradeCommand runs "cmsctl upgrade" against a real database and blob
// store, with a stand-in release (its cms binary only answers "migrate")
// and a stand-in systemctl: it refuses during a contest, takes a real
// backup, and on a failed migration switches back and restores it.
func TestUpgradeCommand(t *testing.T) {
	ctx := context.Background()
	pool, dbURL := testutil.DBWithURL(t)
	q := sqlc.New(pool)
	dir := t.TempDir()
	t.Setenv("CMS_CONFIG", "")
	t.Setenv("CMS_DATABASE_URL", dbURL)
	t.Setenv("CMS_BLOB_BACKEND", "local")
	t.Setenv("CMS_BLOB_DIR", filepath.Join(dir, "blobs"))
	t.Setenv("CMS_BACKUP_DIR", filepath.Join(dir, "backups"))
	// The contest web server "runs" here; nothing else is enabled.
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	defer healthy.Close()
	t.Setenv("CMS_CONTEST_WEB_LISTEN", strings.TrimPrefix(healthy.URL, "http://"))
	syslog := filepath.Join(dir, "systemctl.log")
	systemctl := filepath.Join(dir, "systemctl")
	os.WriteFile(systemctl, []byte(`#!/bin/sh
echo "$*" >> `+syslog+`
case "$*" in
  "is-enabled --quiet cms-contest-web.service") exit 0 ;;
  is-enabled*) exit 1 ;;
esac
exit 0
`), 0o755)
	marker := filepath.Join(dir, "migrated")
	cmsBin := `#!/bin/sh
[ "$1" = migrate ] || exit 2
echo "$CMS_DATABASE_URL" > ` + marker + `
exit ${FAKE_MIGRATE_FAIL:-0}
`
	// Release 2.0.0 for this machine.
	name := fmt.Sprintf("cms_2.0.0_linux_%s.tar.gz", runtime.GOARCH)
	var tgz bytes.Buffer
	zw := gzip.NewWriter(&tgz)
	tw := tar.NewWriter(zw)
	for f, content := range map[string]string{"cms": cmsBin, "cmsctl": cmsBin} {
		tw.WriteHeader(&tar.Header{Name: "cms_2.0.0/" + f, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg})
		tw.Write([]byte(content))
	}
	tw.Close()
	zw.Close()
	sum := sha256.Sum256(tgz.Bytes())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /latest", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/tag/v2.0.0", http.StatusFound) })
	mux.HandleFunc("GET /tag/v2.0.0", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("GET /download/v2.0.0/"+name, func(w http.ResponseWriter, r *http.Request) { w.Write(tgz.Bytes()) })
	mux.HandleFunc("GET /download/v2.0.0/checksums.txt", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintf(w, "%x  %s\n", sum, name) })
	rel := httptest.NewServer(mux)
	defer rel.Close()

	newRoot := func() string {
		root := t.TempDir()
		os.MkdirAll(filepath.Join(root, "releases", "1.0.0"), 0o755)
		os.Symlink("releases/1.0.0", filepath.Join(root, "current"))
		return root
	}
	run := func(root string, extra ...string) (int, string, string) {
		var out, errb bytes.Buffer
		args := append([]string{"ctl", "upgrade", "-root", root, "-release-url", rel.URL, "-systemctl", systemctl, "-health-timeout", "5s"}, extra...)
		code := Main(args, &out, &errb)
		return code, out.String(), errb.String()
	}
	current := func(root string) string { l, _ := os.Readlink(filepath.Join(root, "current")); return l }

	// A contest in progress for one participant (extra time) blocks it.
	u, err := q.CreateUser(ctx, sqlc.CreateUserParams{Username: "ana", PasswordHash: "h", PreferredLanguages: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	var cid int64
	if err := pool.QueryRow(ctx, `INSERT INTO contests (name, start_time, stop_time) VALUES ('omi', now() - interval '3 hours', now() - interval '10 minutes') RETURNING id`).Scan(&cid); err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateParticipation(ctx, sqlc.CreateParticipationParams{ContestID: cid, UserID: u.ID, ExtraTimeS: 3600, Ip: []netip.Prefix{}}); err != nil {
		t.Fatal(err)
	}
	root := newRoot()
	if code, _, errs := run(root); code == 0 || !strings.Contains(errs, "omi until") || current(root) != "releases/1.0.0" {
		t.Fatalf("upgrade during a contest = %d: %s", code, errs)
	}
	if _, err := os.Stat(syslog); err == nil {
		t.Fatal("services were touched during a contest")
	}
	pool.Exec(ctx, `UPDATE participations SET extra_time_s = 0`) // it is over now

	// Success: backup, stop, migrate with the new binary, start, healthy.
	code, out, errs := run(root)
	if code != 0 || current(root) != "releases/2.0.0" {
		t.Fatalf("upgrade = %d\n%s%s", code, out, errs)
	}
	calls, _ := os.ReadFile(syslog)
	if !strings.Contains(string(calls), "stop cms.target\nstart cms.target\nis-enabled --quiet cms-") {
		t.Fatalf("systemctl calls:\n%s", calls)
	}
	if b, _ := os.ReadFile(marker); !strings.Contains(string(b), dbURL) {
		t.Fatal("the new binary did not migrate the configured database")
	}
	backups, _ := filepath.Glob(filepath.Join(dir, "backups", "*-upgrade.tar.zst"))
	if len(backups) != 1 {
		t.Fatalf("backups: %v", backups)
	}

	// A failed migration: back to 1.0.0 with the database restored.
	os.Remove(syslog)
	t.Setenv("FAKE_MIGRATE_FAIL", "1")
	root = newRoot()
	code, out, errs = run(root)
	if code == 0 || !strings.Contains(errs, "rolled back to releases/1.0.0") || current(root) != "releases/1.0.0" {
		t.Fatalf("failed migration = %d\n%s%s", code, out, errs)
	}
	calls, _ = os.ReadFile(syslog)
	if !strings.Contains(string(calls), "stop cms.target\nstop cms.target\nstart cms.target") {
		t.Fatalf("rollback systemctl calls:\n%s", calls)
	}
	if n, err := q.AdminCountUsers(ctx); err != nil || n != 1 {
		t.Fatalf("users after the restore: %d %v", n, err)
	}
	if _, err := q.GetContestByName(ctx, "omi"); err != nil {
		t.Fatalf("contest after the restore: %v", err)
	}

	// A remote worker (blobs over HTTP) has no database: the release is
	// switched and the services restarted, without contest check, backup
	// or migrations, even while a contest runs on the main server.
	pool.Exec(ctx, `UPDATE participations SET extra_time_s = 3600`)
	os.Remove(syslog)
	os.Remove(marker)
	t.Setenv("CMS_DATABASE_URL", "postgres://nobody@127.0.0.1:1/none?connect_timeout=1")
	t.Setenv("CMS_BLOB_BACKEND", "http")
	t.Setenv("CMS_BLOB_HTTP_URL", "http://10.8.0.1:8891")
	t.Setenv("CMS_BLOB_HTTP_TOKEN", "token")
	root = newRoot()
	code, out, errs = run(root)
	if code != 0 || current(root) != "releases/2.0.0" || !strings.Contains(out, "a worker") {
		t.Fatalf("worker upgrade = %d\n%s%s", code, out, errs)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a worker ran the migrations")
	}
	if calls, _ = os.ReadFile(syslog); !strings.Contains(string(calls), "stop cms.target\nstart cms.target") {
		t.Fatalf("worker systemctl calls:\n%s", calls)
	}
	if code, _, errs = run(newRoot(), "-role", "judge"); code == 0 || !strings.Contains(errs, "-role") {
		t.Fatalf("bad role = %d %s", code, errs)
	}
}
