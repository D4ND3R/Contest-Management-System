package adminweb

import (
	"bytes"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/backup"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
)

// TestBackupsFromAdmin (A4): "Back up now" runs in the background while the
// list follows it; the file downloads, verifies and can be deleted; only
// full admins may do any of it, and it is all audited.
func TestBackupsFromAdmin(t *testing.T) {
	f := newFixture(t)
	ro := f.login("read_only")
	if code, _ := ro.Post("/backups", nil); code != http.StatusForbidden {
		t.Fatalf("read-only backup = %d", code)
	}
	a := f.login("all")
	code, body := a.Post("/backups", nil)
	if code != 200 || !strings.Contains(body, "Backup started.") {
		t.Fatalf("backup = %d\n%s", code, body)
	}
	deadline := time.Now().Add(30 * time.Second)
	for strings.Contains(body, "Backup in progress") || !strings.Contains(body, "download") {
		if time.Now().After(deadline) {
			t.Fatalf("backup never finished:\n%s", body)
		}
		time.Sleep(50 * time.Millisecond)
		_, body = a.Get("/backups?fragment=1")
	}
	if !strings.Contains(body, ">done<") || !strings.Contains(body, "admin_all") {
		t.Fatalf("list:\n%s", body)
	}
	link := regexp.MustCompile(`/backups/(cms-backup-[^/"]+)/download`).FindStringSubmatch(body)
	if link == nil {
		t.Fatalf("no download link:\n%s", body)
	}
	if code, _ := ro.Get(link[0]); code != http.StatusForbidden {
		t.Fatalf("read-only download = %d", code)
	}
	code, file := a.Get(link[0])
	if code != 200 || !strings.HasPrefix(file, "\x28\xb5\x2f\xfd") {
		t.Fatalf("download = %d (%d bytes)", code, len(file))
	}
	st, err := backup.Verify(bg, bytes.NewReader([]byte(file)))
	if err != nil || st.Rows == 0 {
		t.Fatalf("downloaded backup: %d rows, %v", st.Rows, err)
	}
	if code, _ := a.Get("/backups/..%2Fetc%2Fpasswd/download"); code != http.StatusNotFound {
		t.Fatalf("traversal = %d", code)
	}
	if code, _ := a.Post("/backups/"+link[1]+"/delete", nil); code != 200 {
		t.Fatalf("delete = %d", code)
	}
	if code, _ := a.Get(link[0]); code != http.StatusNotFound {
		t.Fatalf("deleted backup still downloads: %d", code)
	}
	rows, _ := f.q.ListAuditLog(bg, sqlc.ListAuditLogParams{Limit: 100})
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.Action] = true
	}
	for _, act := range []string{"backup.create", "backup.download", "backup.delete"} {
		if !seen[act] {
			t.Errorf("audit log misses %s", act)
		}
	}
}
