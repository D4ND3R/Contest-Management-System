package adminweb

import (
	"archive/zip"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/contestarchive"
	"github.com/D4ND3R/Contest-Management-System/internal/webtest"
)

// TestContestArchiveFromAdmin (SPEC_CLOSE D7): full administrators
// download the archive of a contest and import it back as a new contest;
// both are audited, a taken name imports nothing.
func TestContestArchiveFromAdmin(t *testing.T) {
	f := newFixture(t)
	path := fmt.Sprintf("/contests/%d/archive.zip", f.contest.ID)
	if code, _ := f.login("read_only").Get(path + "?submissions=1"); code != http.StatusForbidden {
		t.Fatalf("read-only archive = %d", code)
	}
	a := f.login("all")
	_, page := a.Get(fmt.Sprintf("/contests/%d/settings", f.contest.ID))
	if !strings.Contains(page, fmt.Sprintf(`action="/contests/%d/archive.zip"`, f.contest.ID)) {
		t.Fatal("no archive form on the contest page")
	}
	_, list := a.Get("/contests")
	if !strings.Contains(list, `action="/contests/import"`) {
		t.Fatal("no import form on the contests page")
	}
	code, body := a.Get(path + "?submissions=1")
	if code != 200 {
		t.Fatalf("archive = %d", code)
	}
	zr, err := zip.NewReader(strings.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	h, err := contestarchive.ReadHeader(zr)
	if err != nil || h.Contest != f.contest.Name || !h.Submissions || h.Rows("tasks") != 1 || h.Rows("participations") != 1 {
		t.Fatalf("header %+v %v", h, err)
	}
	file := webtest.File{Field: "archive", Name: "a.zip", Data: []byte(body)}
	code, body = a.PostMultipart("/contests/import", nil, file)
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "already exists") || !strings.Contains(body, "These task names are taken: sum.") {
		t.Fatalf("import over the original = %d\n%s", code, body)
	}
	code, body = a.PostMultipart("/contests/import", map[string]string{"name": "archivado", "task_suffix": "-arch"}, file)
	if code != 200 || !strings.Contains(body, "Contest imported") || !strings.Contains(body, "1 existing users") {
		t.Fatalf("import = %d\n%s", code, body)
	}
	c, err := f.q.GetContestByName(bg, "archivado")
	if err != nil || c.Status != "archived" {
		t.Fatalf("imported contest %+v %v", c, err)
	}
	if tk, err := f.q.GetTaskByName(bg, "sum-arch"); err != nil || tk.ContestID == nil || *tk.ContestID != c.ID {
		t.Fatalf("imported task %+v %v", tk, err)
	}
	code, body = a.PostMultipart("/contests/import", map[string]string{"name": "otro"}, webtest.File{Field: "archive", Name: "x.zip", Data: []byte("hello")})
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "Not a contest archive") {
		t.Fatalf("not a zip = %d\n%s", code, body)
	}
	var n int
	f.pool.QueryRow(bg, "SELECT count(*) FROM audit_log WHERE action IN ('contest.export', 'contest.import')").Scan(&n)
	if n != 2 {
		t.Fatalf("%d audit entries, want 2 (export, import)", n)
	}
}
