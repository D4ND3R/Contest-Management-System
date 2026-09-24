package db_test

import (
	"context"
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
