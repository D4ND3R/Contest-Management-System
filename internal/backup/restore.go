package backup

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// target is the database and store a backup is restored into.
type target struct {
	pool  *pgxpool.Pool
	store blob.Store
	force bool
	// schema is where the CMS tables live (the migrations do not qualify
	// names: it is the connection's current schema, public by default).
	schema string

	conn *pgxpool.Conn
	tx   pgx.Tx
	fks  []foreignKey
}

type foreignKey struct{ table, name, def string }

// regeneratedTables are refilled by the services on start; rows there do
// not make a database "in use".
var regeneratedTables = map[string]bool{"languages": true}

// checkMigrations makes sure this binary knows every migration of the
// backup (in the same order) and returns the version of the last one.
func checkMigrations(names []string) (int, error) {
	migs, err := db.Migrations()
	if err != nil {
		return 0, err
	}
	if len(names) == 0 {
		return 0, fmt.Errorf("%w: no migrations in the header", ErrNotBackup)
	}
	for i, n := range names {
		if i >= len(migs) || migs[i].Name != n {
			return 0, fmt.Errorf("the backup was made by a newer or different CMS version (unknown migration %s)", n)
		}
	}
	return migs[len(names)-1].Version, nil
}

// prepare brings the target schema to the backup's migration, opens the
// restore transaction and drops the foreign keys (they are added back,
// and validated, after the data is in).
func (tg *target) prepare(ctx context.Context, h Header, last int) error {
	applied, used, err := tg.inspect(ctx)
	if err != nil {
		return err
	}
	prefix := len(applied) <= len(h.Migrations)
	for i := 0; prefix && i < len(applied); i++ {
		prefix = applied[i] == h.Migrations[i]
	}
	if !prefix || len(used) > 0 {
		if !tg.force {
			why := "it has a newer schema"
			if len(used) > 0 {
				why = "it already holds data (" + strings.Join(used, ", ") + ")"
			}
			return fmt.Errorf("refusing to restore into this database: %s; use an empty database or force the replacement", why)
		}
		if _, err := tg.pool.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{tg.schema}.Sanitize()+" CASCADE"); err != nil {
			return fmt.Errorf("drop the current data: %w", err)
		}
		if _, err := tg.pool.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{tg.schema}.Sanitize()); err != nil {
			return err
		}
	}
	if _, err := db.MigrateTo(ctx, tg.pool, last); err != nil {
		return err
	}
	if tg.conn, err = tg.pool.Acquire(ctx); err != nil {
		return err
	}
	if tg.tx, err = tg.conn.Begin(ctx); err != nil {
		return err
	}
	rows, err := tg.tx.Query(ctx, `SELECT conrelid::regclass::text, conname::text, pg_get_constraintdef(oid)
FROM pg_constraint WHERE contype = 'f' AND connamespace = current_schema()::regnamespace ORDER BY 1, 2`)
	if err != nil {
		return err
	}
	tg.fks, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (foreignKey, error) {
		var fk foreignKey
		return fk, r.Scan(&fk.table, &fk.name, &fk.def)
	})
	if err != nil {
		return err
	}
	for _, fk := range tg.fks {
		if _, err := tg.tx.Exec(ctx, "ALTER TABLE "+fk.table+" DROP CONSTRAINT "+pgx.Identifier{fk.name}.Sanitize()); err != nil {
			return err
		}
	}
	return nil
}

// inspect returns the migrations applied to the target and the tables that
// hold rows.
func (tg *target) inspect(ctx context.Context) (applied, used []string, err error) {
	if err := tg.pool.QueryRow(ctx, "SELECT current_schema()").Scan(&tg.schema); err != nil {
		return nil, nil, err
	}
	var hasLog bool
	if err := tg.pool.QueryRow(ctx, "SELECT to_regclass('schema_migrations') IS NOT NULL").Scan(&hasLog); err != nil {
		return nil, nil, err
	}
	if hasLog {
		if applied, err = appliedMigrations(ctx, tg.pool); err != nil {
			return nil, nil, err
		}
	}
	rows, err := tg.pool.Query(ctx, `SELECT c.relname::text FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = current_schema() AND c.relkind = 'r' AND c.relname <> 'schema_migrations' ORDER BY 1`)
	if err != nil {
		return nil, nil, err
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, nil, err
	}
	for _, t := range tables {
		if regeneratedTables[t] {
			continue
		}
		var any bool
		if err := tg.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM "+pgx.Identifier{t}.Sanitize()+")").Scan(&any); err != nil {
			return nil, nil, err
		}
		if any {
			used = append(used, t)
		}
	}
	return applied, used, nil
}

// tableLoad streams one table's COPY data into the restore transaction.
type tableLoad struct {
	pw   *io.PipeWriter
	done chan copyResult
}

type copyResult struct {
	tag pgconn.CommandTag
	err error
}

func (tg *target) startTable(ctx context.Context, t Table) *tableLoad {
	pr, pw := io.Pipe()
	l := &tableLoad{pw: pw, done: make(chan copyResult, 1)}
	// Tables restored from a backup replace the regenerated rows.
	if regeneratedTables[t.Name] {
		if _, err := tg.tx.Exec(ctx, "DELETE FROM "+pgx.Identifier{t.Name}.Sanitize()); err != nil {
			pr.CloseWithError(err)
			l.done <- copyResult{err: err}
			return l
		}
	}
	go func() {
		tag, err := tg.tx.Conn().PgConn().CopyFrom(ctx, pr, copySQL(t, "FROM STDIN"))
		if err == nil {
			// Drain whatever the writer still sends (nothing, normally).
			_, err = io.Copy(io.Discard, pr)
		}
		pr.CloseWithError(err)
		l.done <- copyResult{tag, err}
	}()
	return l
}

func (l *tableLoad) Write(p []byte) (int, error) { return l.pw.Write(p) }

func (l *tableLoad) finish() (int64, error) {
	l.pw.Close()
	r := <-l.done
	return r.tag.RowsAffected(), r.err
}

// commit restores the sequences and the foreign keys, commits and applies
// the migrations newer than the backup.
func (tg *target) commit(ctx context.Context, seqs []Sequence) error {
	for _, s := range seqs {
		if s.Value == nil {
			continue
		}
		if _, err := tg.tx.Exec(ctx, "SELECT setval($1::regclass, $2, true)", pgx.Identifier{s.Name}.Sanitize(), *s.Value); err != nil {
			return fmt.Errorf("sequence %s: %w", s.Name, err)
		}
	}
	for _, fk := range tg.fks {
		if _, err := tg.tx.Exec(ctx, "ALTER TABLE "+fk.table+" ADD CONSTRAINT "+pgx.Identifier{fk.name}.Sanitize()+" "+fk.def); err != nil {
			return fmt.Errorf("the restored data breaks %s on %s: %w", fk.name, fk.table, err)
		}
	}
	if err := tg.tx.Commit(ctx); err != nil {
		return err
	}
	tg.tx = nil
	tg.conn.Release()
	tg.conn = nil
	if _, err := db.Migrate(ctx, tg.pool); err != nil {
		return fmt.Errorf("restored; applying newer migrations failed: %w", err)
	}
	_, err := tg.pool.Exec(ctx, "ANALYZE")
	return err
}

// abort rolls back an unfinished restore.
func (tg *target) abort() {
	if tg.tx != nil {
		_ = tg.tx.Rollback(context.Background())
		tg.tx = nil
	}
	if tg.conn != nil {
		tg.conn.Release()
		tg.conn = nil
	}
}
