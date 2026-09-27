package auditlog

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/testutil"
)

var ctx = context.Background()

// TestChain (SPEC_IOI §9.4, §13): entries, including concurrent ones, form
// one gapless chain; the log refuses updates, deletions and truncation
// (but survives deleting an administrator); tampering done behind its back
// is found.
func TestChain(t *testing.T) {
	pool := testutil.DB(t)
	q := sqlc.New(pool)
	rep, err := Verify(ctx, q)
	if err != nil || !rep.OK() || rep.Entries != 0 || rep.HeadSeq != 0 {
		t.Fatalf("empty log: %+v %v", rep, err)
	}
	admin, err := q.CreateAdmin(ctx, sqlc.CreateAdminParams{Name: "A", Username: "alice", PasswordHash: "x", Enabled: true, Role: "all"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var err error
			if i%2 == 0 {
				err = q.InsertAuditLog(ctx, sqlc.InsertAuditLogParams{AdminID: &admin.ID, Action: "contest.update", TargetType: "contest",
					Details: json.RawMessage(`{"i": 1, "ñ": "é"}`), Ip: "10.0.0.1"})
			} else {
				err = q.InsertSubmissionReceipt(ctx, sqlc.InsertSubmissionReceiptParams{Actor: "contestant:ana", SubmissionID: int64(i),
					Details: json.RawMessage(`{"files": {"a.%l": "ab"}}`), Ip: "10.0.0.2"})
			}
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	rep, err = Verify(ctx, q)
	if err != nil || !rep.OK() || rep.Entries != 40 || rep.HeadSeq != 40 || len(rep.HeadHash) != 64 {
		t.Fatalf("after inserts: %+v %v", rep, err)
	}
	rows, _ := q.ListAuditLog(ctx, sqlc.ListAuditLogParams{Limit: 100})
	if len(rows) != 20 || rows[0].Actor != "alice" {
		t.Fatalf("listing without receipts: %d rows, actor %q", len(rows), rows[0].Actor)
	}
	receipts := "submission.received"
	if rows, _ := q.ListAuditLog(ctx, sqlc.ListAuditLogParams{Limit: 100, Action: &receipts}); len(rows) != 20 {
		t.Fatalf("%d receipts listed", len(rows))
	}

	for _, stmt := range []string{"UPDATE audit_log SET action = 'x' WHERE seq = 3", "DELETE FROM audit_log WHERE seq = 3",
		"TRUNCATE audit_log", "UPDATE audit_log SET admin_id = NULL, ip = '' WHERE seq = 2"} {
		if _, err := pool.Exec(ctx, stmt); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%s: %v", stmt, err)
		}
	}
	// Deleting an administrator clears admin_id (the only change allowed).
	if _, err := pool.Exec(ctx, "DELETE FROM admins WHERE id = $1", admin.ID); err != nil {
		t.Fatalf("delete admin: %v", err)
	}
	if rep, _ = Verify(ctx, q); !rep.OK() {
		t.Fatalf("after deleting the admin: %v", rep.Problems)
	}

	// Tampering behind the log's back (as the table owner could) is found.
	head := rep.HeadHash
	for _, stmt := range []string{"ALTER TABLE audit_log DISABLE TRIGGER audit_log_append_only",
		`UPDATE audit_log SET details = '{"i": 2}' WHERE seq = 5`, "DELETE FROM audit_log WHERE seq = 9",
		"ALTER TABLE audit_log ENABLE TRIGGER audit_log_append_only"} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	rep, err = Verify(ctx, q)
	if err != nil || rep.OK() {
		t.Fatalf("tampering not found: %+v %v", rep, err)
	}
	all := strings.Join(rep.Problems, "; ")
	for _, want := range []string{"entry 5 (id", "was modified", "entries 9 to 9 are missing", "entry 10 (id"} {
		if !strings.Contains(all, want) {
			t.Errorf("problems %q lack %q", all, want)
		}
	}
	if rep.HeadHash != head {
		t.Fatal("the head changed")
	}
}

// TestChainBackfill: entries written before the chain existed (migration
// 0021) are chained in their original order, with their administrator's
// name, when the database is upgraded.
func TestChainBackfill(t *testing.T) {
	pool, _ := testutil.EmptyDB(t)
	migs, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	before := 0
	for _, m := range migs {
		if strings.HasPrefix(m.Name, "0021") {
			break
		}
		before = m.Version
	}
	if _, err := db.MigrateTo(ctx, pool, before); err != nil {
		t.Fatal(err)
	}
	var admin int64
	if err := pool.QueryRow(ctx, "INSERT INTO admins (name, username, password_hash) VALUES ('B', 'bob', 'x') RETURNING id").Scan(&admin); err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		if _, err := pool.Exec(ctx, "INSERT INTO audit_log (admin_id, action, details) VALUES ($1, $2, '{}')", admin, "old."+string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	q := sqlc.New(pool)
	q.InsertAuditLog(ctx, sqlc.InsertAuditLogParams{AdminID: &admin, Action: "new", Details: json.RawMessage(`{}`)})
	rep, err := Verify(ctx, q)
	if err != nil || !rep.OK() || rep.Entries != 6 || rep.HeadSeq != 6 {
		t.Fatalf("%+v %v", rep, err)
	}
	var first, actor string
	pool.QueryRow(ctx, "SELECT action, actor FROM audit_log WHERE seq = 1").Scan(&first, &actor)
	if first != "old.a" || actor != "bob" {
		t.Fatalf("first entry %s by %q", first, actor)
	}
}
