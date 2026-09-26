package adminweb

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestAuditFilters (SPEC_CLOSE D6): the audit log filters by
// administrator, action prefix and date range.
func TestAuditFilters(t *testing.T) {
	f := newFixture(t)
	all := f.admins["all"]
	ro := f.admins["read_only"]
	// The log is append-only: entries are written with their time.
	add := func(admin int64, action, at string) {
		if _, err := f.pool.Exec(bg, "INSERT INTO audit_log (admin_id, action, details, created_at) VALUES ($1, $2, $3, $4)",
			admin, action, json.RawMessage(`{"marker": "`+action+`"}`), at); err != nil {
			t.Fatal(err)
		}
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

// TestAuditChainPage (SPEC_IOI §9.4): the audit page shows the chain's
// head and verifies it on request; submission receipts are listed only
// when asked for.
func TestAuditChainPage(t *testing.T) {
	f := newFixture(t)
	b := f.login("read_only")
	f.pool.Exec(bg, "INSERT INTO audit_log (actor, action, target_type, target_id, details) VALUES ('contestant:ana', 'submission.received', 'submission', 1, '{\"files\": {}}')")
	_, body := b.Get("/audit")
	if !strings.Contains(body, "Tamper evidence") || !strings.Contains(body, `href="/audit/verify"`) || strings.Contains(body, "contestant:ana") {
		t.Fatalf("audit page:\n%s", body)
	}
	if _, body := b.Get("/audit?action=submission.received"); !strings.Contains(body, "contestant:ana") {
		t.Fatal("receipts not listed with the filter")
	}
	if code, body := b.Get("/audit/verify"); code != 200 || !strings.Contains(body, "Verified: the chain of") {
		t.Fatalf("verify = %d\n%s", code, body)
	}
}
