package db_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/testutil"
)

func TestMigrationsAreOrderedAndUnique(t *testing.T) {
	migs, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(migs); i++ {
		if migs[i].Version <= migs[i-1].Version {
			t.Fatalf("migrations out of order: %s after %s", migs[i].Name, migs[i-1].Name)
		}
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	pool := testutil.DB(t) // already migrated from the template
	applied, err := db.Migrate(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 0 {
		t.Fatalf("re-running migrations applied %v", applied)
	}
	migs, _ := db.Migrations()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM schema_migrations").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != len(migs) {
		t.Fatalf("schema_migrations has %d rows, want %d", n, len(migs))
	}
}

// TestUpgradeFromPopulatedV021 (SPEC_MIN §12): every migration after
// v0.2.1 applies to a database holding a real contest, and keeps its data
// (the audit log gets its hash chain).
func TestUpgradeFromPopulatedV021(t *testing.T) {
	ctx := context.Background()
	pool, _ := testutil.EmptyDB(t)
	if _, err := db.MigrateTo(ctx, pool, 16); err != nil {
		t.Fatal(err)
	}
	seed, err := os.ReadFile(filepath.Join("testdata", "v0.2.1-seed.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(seed)); err != nil {
		t.Fatalf("seeding the v0.2.1 schema: %v", err)
	}
	applied, err := db.Migrate(ctx, pool)
	if err != nil {
		t.Fatalf("upgrading a populated v0.2.1 database: %v", err)
	}
	if len(applied) == 0 {
		t.Fatal("nothing to apply after 0016")
	}
	var subs, chained int
	pool.QueryRow(ctx, "SELECT count(*) FROM submissions").Scan(&subs)
	pool.QueryRow(ctx, "SELECT count(*) FROM audit_log WHERE hash IS NOT NULL AND seq IS NOT NULL").Scan(&chained)
	if subs != 180 || chained != 101 {
		t.Fatalf("after the upgrade: %d submissions, %d chained audit entries", subs, chained)
	}
}
