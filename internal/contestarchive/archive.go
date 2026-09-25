// Package contestarchive exports one contest to a self-contained zip file
// and imports such a file into any installation of the same (or a newer)
// version, as a new contest.
//
// An archive holds, in this order:
//
//	tables/<table>.jsonl  one JSON object per row (row_to_json)
//	blobs/<digest>        every file the rows reference (statements,
//	                      attachments, testcases, managers, photos,
//	                      submitted sources); the name is its SHA-256
//	results.csv           the final results, for people (not imported)
//	cms-contest.json      header: format, version, migrations, row counts
//
// The rows are the contest's own tables (settings, sites, tasks with every
// dataset, participants, communication) and, optionally, its submissions
// with their results, tokens, per-task scores and manual adjustments.
// Rows are exported with every column, so columns added by later
// migrations travel without touching this code; on import every id is
// replaced by a fresh one and the references are rewritten.
package contestarchive

import (
	"archive/zip"
	"compress/flate"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/ranking"
	"github.com/D4ND3R/Contest-Management-System/internal/version"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Format is written in every header; readers reject other values.
const Format = 1

const (
	headerName  = "cms-contest.json"
	resultsName = "results.csv"
	tablesDir   = "tables/"
	blobsDir    = "blobs/"
)

// table is one exported table: where selects the contest's rows ($1 is
// the contest id).
type table struct {
	name        string
	where       string
	order       string
	submissions bool // only with Options.Submissions
}

const (
	inTasks          = "task_id IN (SELECT id FROM tasks WHERE contest_id = $1)"
	inDatasets       = "dataset_id IN (SELECT d.id FROM datasets d JOIN tasks t ON t.id = d.task_id WHERE t.contest_id = $1)"
	inParticipations = "participation_id IN (SELECT id FROM participations WHERE contest_id = $1)"
	contestSubs      = "SELECT s.id FROM submissions s JOIN participations p ON p.id = s.participation_id JOIN tasks t ON t.id = s.task_id WHERE p.contest_id = $1 AND t.contest_id = $1"
	inSubmissions    = "submission_id IN (" + contestSubs + ")"
)

// tables lists the exported tables in insertion order (referenced tables
// first). TestEveryTableIsDecided keeps it in step with the schema.
var tables = []table{
	{"contests", "id = $1", "id", false},
	{"sites", "contest_id = $1", "id", false},
	{"users", "id IN (SELECT user_id FROM participations WHERE contest_id = $1)", "id", false},
	{"teams", "id IN (SELECT team_id FROM participations WHERE contest_id = $1)", "id", false},
	{"tasks", "contest_id = $1", "id", false},
	{"statements", inTasks, "id", false},
	{"attachments", inTasks, "id", false},
	{"datasets", inTasks, "id", false},
	{"managers", inDatasets, "id", false},
	{"testcases", inDatasets, "id", false},
	{"participations", "contest_id = $1", "id", false},
	{"announcements", "contest_id = $1", "id", false},
	{"questions", "contest_id = $1", "id", false},
	{"messages", inParticipations, "id", false},
	{"submissions", "id IN (" + contestSubs + ")", "id", true},
	{"submission_files", inSubmissions, "id", true},
	{"tokens", inSubmissions, "id", true},
	{"submission_results", inSubmissions, "submission_id, dataset_id", true},
	{"evaluations", inSubmissions, "submission_id, dataset_id, testcase_id", true},
	{"participation_task_scores", inParticipations + " AND " + inTasks, "participation_id, task_id", true},
	{"score_adjustments", inParticipations + " AND " + inTasks, "id", true},
}

// skipped are the tables an archive leaves out, and why.
var skipped = map[string]string{
	"schema_migrations":     "installation",
	"blobs":                 "installation (the files themselves travel)",
	"languages":             "installation (config/languages)",
	"admins":                "installation: references to admins become empty",
	"audit_log":             "installation",
	"executables":           "compiled again when needed",
	"user_tests":            "contestants' own test runs, not results",
	"user_test_files":       "contestants' own test runs, not results",
	"user_test_results":     "contestants' own test runs, not results",
	"user_test_executables": "compiled again when needed",
	"print_jobs":            "contest-day logistics",
	"balloons":              "contest-day logistics",
}

// refs maps every column holding an id to the table the id belongs to.
var refs = map[string]string{
	"contest_id":        "contests",
	"site_id":           "sites",
	"user_id":           "users",
	"team_id":           "teams",
	"task_id":           "tasks",
	"dataset_id":        "datasets",
	"active_dataset_id": "datasets",
	"testcase_id":       "testcases",
	"participation_id":  "participations",
	"submission_id":     "submissions",
}

// adminRefs are the columns naming an administrator: administrators belong
// to an installation, so they are emptied.
var adminRefs = map[string]bool{
	"admin_id": true, "reply_admin_id": true, "tester_admin_id": true, "invalidated_by": true, "delivered_by": true,
}

// TableCount is the number of rows of one table in an archive.
type TableCount struct {
	Name string `json:"name"`
	Rows int64  `json:"rows"`
}

// Header describes an archive.
type Header struct {
	Format      int          `json:"format"`
	CreatedAt   time.Time    `json:"created_at"`
	Version     string       `json:"cms_version"`
	Migrations  []string     `json:"migrations"`
	Contest     string       `json:"contest"`
	Title       string       `json:"title"`
	Submissions bool         `json:"submissions"`
	Tables      []TableCount `json:"tables"`
	Blobs       int          `json:"blobs"`
	BlobBytes   int64        `json:"blob_bytes"`
	// Missing lists files the rows reference that were absent from the
	// blob store when the archive was written.
	Missing []string `json:"missing_blobs,omitempty"`
}

// Rows returns the row count of a table (0 when absent).
func (h *Header) Rows(name string) int64 {
	for _, t := range h.Tables {
		if t.Name == name {
			return t.Rows
		}
	}
	return 0
}

// Options tune an export.
type Options struct {
	// Submissions adds the submissions with their files, results,
	// evaluations, tokens, per-task scores and manual adjustments.
	Submissions bool
}

// Export writes the archive of a contest to w. The rows come from one
// REPEATABLE READ snapshot, so they are consistent; files are immutable
// and are streamed after the snapshot is released.
func Export(ctx context.Context, pool *pgxpool.Pool, store blob.Store, contestID int64, w io.Writer, o Options) (*Header, error) {
	zw := zip.NewWriter(w)
	zw.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) { return flate.NewWriter(w, flate.BestSpeed) })
	h, digests, err := exportRows(ctx, pool, zw, contestID, o)
	if err != nil {
		return nil, err
	}
	sizes, err := blobSizes(ctx, pool, digests)
	if err != nil {
		return nil, err
	}
	for _, d := range digests {
		n, err := copyBlob(ctx, zw, store, d, sizes[d])
		if errors.Is(err, blob.ErrNotFound) {
			h.Missing = append(h.Missing, d)
			continue
		}
		if err != nil {
			return nil, err
		}
		h.Blobs++
		h.BlobBytes += n
	}
	hb, _ := json.MarshalIndent(h, "", "  ")
	fw, err := zw.Create(headerName)
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(hb); err != nil {
		return nil, err
	}
	return h, zw.Close()
}

func exportRows(ctx context.Context, pool *pgxpool.Pool, zw *zip.Writer, contestID int64, o Options) (*Header, []string, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck
	q := sqlc.New(tx)
	c, err := q.GetContest(ctx, contestID)
	if err != nil {
		return nil, nil, fmt.Errorf("contest %d: %w", contestID, err)
	}
	h := &Header{Format: Format, CreatedAt: time.Now().UTC().Truncate(time.Second), Version: version.String(),
		Contest: c.Name, Title: c.Description, Submissions: o.Submissions}
	if h.Migrations, err = appliedMigrations(ctx, tx); err != nil {
		return nil, nil, err
	}
	digestCols, err := digestColumns(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]bool{}
	for _, t := range tables {
		if t.submissions && !o.Submissions {
			continue
		}
		n, err := exportTable(ctx, tx, zw, t, contestID, digestCols[t.name], seen)
		if err != nil {
			return nil, nil, fmt.Errorf("export %s: %w", t.name, err)
		}
		h.Tables = append(h.Tables, TableCount{Name: t.name, Rows: n})
	}
	if o.Submissions {
		rk, err := ranking.Compute(ctx, q, contestID, ranking.Options{})
		if err != nil {
			return nil, nil, err
		}
		fw, err := zw.Create(resultsName)
		if err != nil {
			return nil, nil, err
		}
		if err := rk.WriteCSV(fw); err != nil {
			return nil, nil, err
		}
	}
	digests := make([]string, 0, len(seen))
	for d := range seen {
		digests = append(digests, d)
	}
	sort.Strings(digests)
	return h, digests, nil
}

// exportTable writes the rows of t as JSON lines and collects the digests
// they reference.
func exportTable(ctx context.Context, tx pgx.Tx, zw *zip.Writer, t table, contestID int64, digestCols []string, seen map[string]bool) (int64, error) {
	arr := "NULL::text[]"
	if len(digestCols) > 0 {
		arr = "ARRAY["
		for i, c := range digestCols {
			if i > 0 {
				arr += ", "
			}
			arr += "r." + pgx.Identifier{c}.Sanitize() + "::text"
		}
		arr += "]"
	}
	rows, err := tx.Query(ctx, "SELECT row_to_json(r)::text, "+arr+" FROM "+pgx.Identifier{t.name}.Sanitize()+
		" r WHERE "+t.where+" ORDER BY "+t.order, contestID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	fw, err := zw.Create(tablesDir + t.name + ".jsonl")
	if err != nil {
		return 0, err
	}
	var n int64
	var line string
	var ds []*string
	for rows.Next() {
		if err := rows.Scan(&line, &ds); err != nil {
			return n, err
		}
		for _, d := range ds {
			if d != nil {
				seen[*d] = true
			}
		}
		if _, err := io.WriteString(fw, line+"\n"); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

// digestColumns lists, by table, the columns holding blob digests.
func digestColumns(ctx context.Context, tx pgx.Tx) (map[string][]string, error) {
	rows, err := tx.Query(ctx, `SELECT c.relname::text, a.attname::text
FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid
WHERE c.relnamespace = current_schema()::regnamespace AND c.relkind = 'r'
  AND a.attnum > 0 AND NOT a.attisdropped AND a.atttypid = 'sha256_digest'::regtype
ORDER BY c.relname, a.attnum`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string][]string{}
	for rows.Next() {
		var t, c string
		if err := rows.Scan(&t, &c); err != nil {
			return nil, err
		}
		m[t] = append(m[t], c)
	}
	return m, rows.Err()
}

func appliedMigrations(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}) ([]string, error) {
	rows, err := q.Query(ctx, "SELECT name FROM schema_migrations ORDER BY version")
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// blobSizes returns the registered sizes of the digests (-1 when a digest
// is not registered).
func blobSizes(ctx context.Context, pool *pgxpool.Pool, digests []string) (map[string]int64, error) {
	rows, err := pool.Query(ctx, "SELECT digest, size FROM blobs WHERE digest = ANY($1)", digests)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[string]int64, len(digests))
	for _, d := range digests {
		m[d] = -1
	}
	for rows.Next() {
		var d string
		var n int64
		if err := rows.Scan(&d, &n); err != nil {
			return nil, err
		}
		m[d] = n
	}
	return m, rows.Err()
}

// copyBlob streams one file into the archive, checking its content.
func copyBlob(ctx context.Context, zw *zip.Writer, store blob.Store, digest string, size int64) (int64, error) {
	rc, err := store.Open(ctx, digest)
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	fw, err := zw.CreateHeader(&zip.FileHeader{Name: blobsDir + digest, Method: zip.Deflate, Modified: time.Now().UTC()})
	if err != nil {
		return 0, err
	}
	hw := newDigestWriter(fw)
	n, err := io.Copy(hw, rc)
	if err != nil {
		return n, fmt.Errorf("file %s: %w", digest, err)
	}
	if hw.digest() != digest || (size >= 0 && n != size) {
		return n, fmt.Errorf("file %s is damaged in the blob store (%d bytes read)", digest, n)
	}
	return n, nil
}
