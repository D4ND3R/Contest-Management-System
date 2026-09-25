package adminweb

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
)

// TestAuditFilters (SPEC_CLOSE D6): the audit log filters by
// administrator, action prefix and date range.
func TestAuditFilters(t *testing.T) {
	f := newFixture(t)
	all := f.admins["all"]
	ro := f.admins["read_only"]
	add := func(admin int64, action, at string) {
		if err := f.q.InsertAuditLog(bg, sqlc.InsertAuditLogParams{AdminID: &admin, Action: action,
			Details: json.RawMessage(`{"marker": "` + action + `"}`)}); err != nil {
			t.Fatal(err)
		}
		f.pool.Exec(bg, "UPDATE audit_log SET created_at = $2 WHERE action = $1", action, at)
	}
	add(all.ID, "contest.update", "2030-03-01 10:00:00+00")
	add(all.ID, "contest.extend", "2030-03-02 10:00:00+00")
	add(ro.ID, "score.adjust", "2030-03-02 12:00:00+00")
	add(all.ID, "contestant.x", "2030-03-03 10:00:00+00")
	b := f.login("read_only")
	list := func(q string) string {
		t.Helper()
		code, body := b.Get("/audit?" + q)
		if code != 200 {
			t.Fatalf("audit %s = %d", q, code)
		}
		return body
	}
	has := func(body, action string) bool {
		return strings.Contains(body, "&#34;marker&#34;: &#34;"+action+"&#34;")
	}
	body := list("action=contest.")
	if !has(body, "contest.update") || !has(body, "contest.extend") || has(body, "score.adjust") || has(body, "contestant.x") {
		t.Errorf("action prefix:\n%s", body)
	}
	if body := list("action=contest%25"); has(body, "contest.update") || has(body, "contestant.x") {
		t.Error("a % in the filter must be literal")
	}
	if body := list("from=2030-03-02T00:00&to=2030-03-02T23:59"); has(body, "contest.update") || !has(body, "contest.extend") || !has(body, "score.adjust") || has(body, "contestant.x") {
		t.Errorf("date range:\n%s", body)
	}
	if body := list(fmt.Sprintf("admin=%d&action=score", ro.ID)); !has(body, "score.adjust") || has(body, "contest.extend") {
		t.Errorf("admin + action:\n%s", body)
	}
	if !strings.Contains(list(""), `<option value="score.adjust">`) {
		t.Error("no action suggestions")
	}
}
