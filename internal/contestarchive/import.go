package contestarchive

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// batchSize is the number of rows written per INSERT.
const batchSize = 1000

// headerLimit bounds the header read from an archive.
const headerLimit = 4 << 20

var namePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// Statuses an imported contest may take.
var Statuses = []string{"archived", "draft", "published"}

// ImportOptions tune an import.
type ImportOptions struct {
	// Name of the new contest ("": the archived name).
	Name string
	// TaskSuffix is appended to every task name (task names are unique in
	// an installation).
	TaskSuffix string
	// Status of the new contest ("": archived when the archive holds
	// submissions, draft otherwise).
	Status string
}

// Result summarises an import.
type Result struct {
	ContestID   int64
	Header      *Header
	Rows        int64
	Blobs       int
	ReusedUsers int
	ReusedTeams int
}

// ConflictError reports names already taken; nothing was written.
type ConflictError struct {
	Contest string
	Tasks   []string
}

func (e *ConflictError) Error() string {
	var parts []string
	if e.Contest != "" {
		parts = append(parts, "a contest named "+e.Contest+" exists")
	}
	if len(e.Tasks) > 0 {
		parts = append(parts, "tasks named "+strings.Join(e.Tasks, ", ")+" exist")
	}
	return strings.Join(parts, "; ")
}

// ErrNewer is returned for archives written by a newer schema.
var ErrNewer = errors.New("the archive was written by a newer version of CMS: upgrade this installation first")

// ReadHeader reads and checks the header of an archive.
func ReadHeader(zr *zip.Reader) (*Header, error) {
	f := find(zr, headerName)
	if f == nil {
		return nil, fmt.Errorf("not a contest archive (no %s)", headerName)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, headerLimit))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", headerName, err)
	}
	var h Header
	if err := json.Unmarshal(b, &h); err != nil {
		return nil, fmt.Errorf("%s: %w", headerName, err)
	}
	if h.Format != Format {
		return nil, fmt.Errorf("unsupported archive format %d (this version reads %d)", h.Format, Format)
	}
	known := map[string]bool{}
	for _, t := range tables {
		known[t.name] = true
	}
	for _, t := range h.Tables {
		if !known[t.Name] {
			return nil, fmt.Errorf("%w (table %s)", ErrNewer, t.Name)
		}
	}
	return &h, nil
}

func find(zr *zip.Reader, name string) *zip.File {
	for _, f := range zr.File {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// Import creates a new contest from an archive. Files go to the blob store
// first (content-addressed, so an aborted import only leaves unreferenced
// files for the garbage collector); every row is then written in one
// transaction with fresh ids. Users and teams that already exist (same
// username or code) are reused as they are.
func Import(ctx context.Context, pool *pgxpool.Pool, store blob.Store, zr *zip.Reader, o ImportOptions) (*Result, error) {
	h, err := ReadHeader(zr)
	if err != nil {
		return nil, err
	}
	applied, err := appliedMigrations(ctx, pool)
	if err != nil {
		return nil, err
	}
	if len(h.Migrations) > len(applied) {
		return nil, ErrNewer
	}
	for i, m := range h.Migrations {
		if applied[i] != m {
			return nil, fmt.Errorf("%w (migration %s)", ErrNewer, m)
		}
	}
	if o.Name == "" {
		o.Name = h.Contest
	}
	if !namePattern.MatchString(o.Name) {
		return nil, fmt.Errorf("invalid contest name %q: use letters, digits, '_', '.' and '-'", o.Name)
	}
	if o.TaskSuffix != "" && !namePattern.MatchString(o.TaskSuffix) {
		return nil, fmt.Errorf("invalid task name suffix %q: use letters, digits, '_', '.' and '-'", o.TaskSuffix)
	}
	if o.Status == "" {
		o.Status = "draft"
		if h.Submissions {
			o.Status = "archived"
		}
	}
	if !contains(Statuses, o.Status) {
		return nil, fmt.Errorf("invalid status %q", o.Status)
	}
	files := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		files[f.Name] = f
	}
	for _, t := range h.Tables {
		if files[tablesDir+t.Name+".jsonl"] == nil {
			return nil, fmt.Errorf("the archive is incomplete: %s%s.jsonl is missing", tablesDir, t.Name)
		}
	}
	if err := checkNames(ctx, pool, files, o); err != nil {
		return nil, err
	}
	res := &Result{Header: h}
	var digests []string
	var sizes []int64
	for _, f := range zr.File {
		d, ok := strings.CutPrefix(f.Name, blobsDir)
		if !ok {
			continue
		}
		if !blob.ValidDigest(d) {
			return nil, fmt.Errorf("the archive is damaged: %s is not a SHA-256 name", f.Name)
		}
		if err := putBlob(ctx, store, f, d); err != nil {
			return nil, err
		}
		digests = append(digests, d)
		sizes = append(sizes, int64(f.UncompressedSize64))
		res.Blobs++
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck
	im := &importer{tx: tx, o: o, maps: map[string]map[int64]int64{}, res: res, later: map[string]bool{}}
	for _, t := range h.Tables {
		im.later[t.Name] = true
	}
	for _, t := range tables {
		if !im.later[t.name] {
			continue
		}
		if err := im.table(ctx, t.name, files[tablesDir+t.name+".jsonl"]); err != nil {
			return nil, fmt.Errorf("import %s: %w", t.name, err)
		}
		delete(im.later, t.name)
	}
	if err := im.fixDeferred(ctx); err != nil {
		return nil, err
	}
	// Registered for the garbage collector even when the store is not
	// tracked.
	if _, err := tx.Exec(ctx, `INSERT INTO blobs (digest, size, description)
SELECT d, s, 'contest archive' FROM unnest($1::text[], $2::bigint[]) AS v(d, s) ON CONFLICT (digest) DO NOTHING`, digests, sizes); err != nil {
		return nil, err
	}
	for _, id := range im.maps["contests"] {
		res.ContestID = id
	}
	if res.ContestID == 0 {
		return nil, errors.New("the archive has no contest")
	}
	return res, tx.Commit(ctx)
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// checkNames fails with a ConflictError when the contest or a task name is
// taken.
func checkNames(ctx context.Context, pool *pgxpool.Pool, files map[string]*zip.File, o ImportOptions) error {
	var names []string
	if f := files[tablesDir+"tasks.jsonl"]; f != nil {
		err := eachRow(f, func(row map[string]json.RawMessage) error {
			var n string
			if err := json.Unmarshal(row["name"], &n); err != nil {
				return fmt.Errorf("tasks: %w", err)
			}
			names = append(names, n+o.TaskSuffix)
			return nil
		})
		if err != nil {
			return err
		}
	}
	ce := &ConflictError{}
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM contests WHERE name = $1", o.Name).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		ce.Contest = o.Name
	}
	rows, err := pool.Query(ctx, "SELECT name FROM tasks WHERE name = ANY($1) ORDER BY name", names)
	if err != nil {
		return err
	}
	if ce.Tasks, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		return err
	}
	if ce.Contest != "" || len(ce.Tasks) > 0 {
		return ce
	}
	return nil
}

// putBlob stores one file of the archive unless the store has it already.
func putBlob(ctx context.Context, store blob.Store, f *zip.File, digest string) error {
	if n, err := store.Stat(ctx, digest); err == nil && n == int64(f.UncompressedSize64) {
		return nil
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	info, err := store.Put(ctx, rc)
	if err != nil {
		return fmt.Errorf("%s: %w", f.Name, err)
	}
	if info.Digest != digest {
		return fmt.Errorf("the archive is damaged: the content of %s does not match its name", f.Name)
	}
	return nil
}

// eachRow calls fn with every row of a table file.
func eachRow(f *zip.File, fn func(map[string]json.RawMessage) error) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	r := bufio.NewReaderSize(rc, 1<<16)
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var row map[string]json.RawMessage
			if jerr := json.Unmarshal(line, &row); jerr != nil {
				return fmt.Errorf("%s: %w", f.Name, jerr)
			}
			if ferr := fn(row); ferr != nil {
				return ferr
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%s: %w", f.Name, err)
		}
	}
}

// importer writes the rows of one archive.
type importer struct {
	tx  pgx.Tx
	o   ImportOptions
	res *Result
	// maps holds, by table, the new id of every archived id.
	maps map[string]map[int64]int64
	// later are the tables still to import: references to them are
	// written once they exist.
	later    map[string]bool
	deferred []deferredRef
}

// deferredRef is a reference to a row imported later (a task's live
// dataset).
type deferredRef struct {
	table, column, target string
	id, old               int64
}

var null = json.RawMessage("null")

func quote(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

func id(raw json.RawMessage) (int64, bool) {
	v, err := strconv.ParseInt(string(raw), 10, 64)
	return v, err == nil
}

// columns returns the columns of a table: every one, and whether each can
// be inserted (generated ones cannot); hasID says the table has an
// identity id.
func (im *importer) columns(ctx context.Context, table string) (map[string]bool, bool, error) {
	rows, err := im.tx.Query(ctx, `SELECT attname::text, attgenerated = '', attidentity <> '' FROM pg_attribute
WHERE attrelid = $1::regclass AND attnum > 0 AND NOT attisdropped`, table)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	cols := map[string]bool{}
	hasID := false
	for rows.Next() {
		var name string
		var insertable, identity bool
		if err := rows.Scan(&name, &insertable, &identity); err != nil {
			return nil, false, err
		}
		cols[name] = insertable
		if name == "id" && identity {
			hasID = true
		}
	}
	return cols, hasID, rows.Err()
}

func (im *importer) table(ctx context.Context, name string, f *zip.File) error {
	cols, hasID, err := im.columns(ctx, name)
	if err != nil {
		return err
	}
	im.maps[name] = map[int64]int64{}
	batch := make([]map[string]json.RawMessage, 0, batchSize)
	flush := func() error {
		err := im.insert(ctx, name, cols, hasID, batch)
		batch = batch[:0]
		return err
	}
	err = eachRow(f, func(row map[string]json.RawMessage) error {
		batch = append(batch, row)
		if len(batch) == batchSize {
			return flush()
		}
		return nil
	})
	if err != nil {
		return err
	}
	return flush()
}

// reuse maps the rows whose natural key (username, team code) exists to
// the existing row and drops them from the batch.
func (im *importer) reuse(ctx context.Context, table, key string, batch []map[string]json.RawMessage) ([]map[string]json.RawMessage, int, error) {
	keys := make([]string, 0, len(batch))
	for _, row := range batch {
		var k string
		if err := json.Unmarshal(row[key], &k); err != nil {
			return nil, 0, fmt.Errorf("%s: %w", key, err)
		}
		keys = append(keys, k)
	}
	rows, err := im.tx.Query(ctx, "SELECT "+key+", id FROM "+table+" WHERE "+key+" = ANY($1)", keys)
	if err != nil {
		return nil, 0, err
	}
	existing := map[string]int64{}
	for rows.Next() {
		var k string
		var v int64
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return nil, 0, err
		}
		existing[k] = v
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	kept := batch[:0]
	n := 0
	for i, row := range batch {
		if v, ok := existing[keys[i]]; ok {
			old, _ := id(row["id"])
			im.maps[table][old] = v
			n++
			continue
		}
		kept = append(kept, row)
	}
	return kept, n, nil
}

func (im *importer) insert(ctx context.Context, name string, cols map[string]bool, hasID bool, batch []map[string]json.RawMessage) error {
	if len(batch) == 0 {
		return nil
	}
	var err error
	var reused int
	switch name {
	case "contests":
		for _, row := range batch {
			row["name"], row["status"] = quote(im.o.Name), quote(im.o.Status)
		}
	case "tasks":
		for _, row := range batch {
			var n string
			if err := json.Unmarshal(row["name"], &n); err != nil {
				return err
			}
			row["name"] = quote(n + im.o.TaskSuffix)
		}
	case "users":
		batch, reused, err = im.reuse(ctx, "users", "username", batch)
		im.res.ReusedUsers += reused
	case "teams":
		batch, reused, err = im.reuse(ctx, "teams", "code", batch)
		im.res.ReusedTeams += reused
	}
	if err != nil || len(batch) == 0 {
		return err
	}
	var ids []int64
	if hasID {
		rows, err := im.tx.Query(ctx, "SELECT nextval(pg_get_serial_sequence($1, 'id')) FROM generate_series(1, $2)", name, len(batch))
		if err != nil {
			return err
		}
		if ids, err = pgx.CollectRows(rows, pgx.RowTo[int64]); err != nil {
			return err
		}
	}
	for i, row := range batch {
		if hasID {
			old, ok := id(row["id"])
			if !ok {
				return fmt.Errorf("row without id: %s", row["id"])
			}
			im.maps[name][old] = ids[i]
			row["id"] = json.RawMessage(strconv.FormatInt(ids[i], 10))
		}
		for col, raw := range row {
			if adminRefs[col] {
				row[col] = null
				continue
			}
			target, ok := refs[col]
			if !ok || bytes.Equal(raw, null) {
				continue
			}
			old, ok := id(raw)
			if !ok {
				return fmt.Errorf("column %s: %s is not an id", col, raw)
			}
			if im.later[target] && target != name {
				if !hasID {
					return fmt.Errorf("column %s refers to %s, imported later", col, target)
				}
				im.deferred = append(im.deferred, deferredRef{table: name, column: col, target: target, id: ids[i], old: old})
				row[col] = null
				continue
			}
			if v, ok := im.maps[target][old]; ok {
				row[col] = json.RawMessage(strconv.FormatInt(v, 10))
			} else {
				// A row outside the archive (e.g. a question about a task
				// moved to another contest): the database decides whether
				// the reference may be empty.
				row[col] = null
			}
		}
	}
	var names []string
	for col := range batch[0] {
		insertable, ok := cols[col]
		if !ok {
			return fmt.Errorf("%w (column %s.%s)", ErrNewer, name, col)
		}
		if insertable {
			names = append(names, pgx.Identifier{col}.Sanitize())
		}
	}
	sort.Strings(names)
	payload, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	list := strings.Join(names, ", ")
	t := pgx.Identifier{name}.Sanitize()
	override := ""
	if hasID {
		override = " OVERRIDING SYSTEM VALUE"
	}
	tag, err := im.tx.Exec(ctx, "INSERT INTO "+t+" ("+list+")"+override+" SELECT "+list+" FROM json_populate_recordset(NULL::"+t+", $1::json)", string(payload))
	if err != nil {
		return err
	}
	im.res.Rows += tag.RowsAffected()
	return nil
}

// fixDeferred writes the references to rows imported after their holders.
func (im *importer) fixDeferred(ctx context.Context) error {
	type key struct{ table, column string }
	groups := map[key][2][]int64{}
	for _, d := range im.deferred {
		v, ok := im.maps[d.target][d.old]
		if !ok {
			continue
		}
		k := key{d.table, d.column}
		g := groups[k]
		g[0], g[1] = append(g[0], d.id), append(g[1], v)
		groups[k] = g
	}
	for k, g := range groups {
		t, c := pgx.Identifier{k.table}.Sanitize(), pgx.Identifier{k.column}.Sanitize()
		if _, err := im.tx.Exec(ctx, "UPDATE "+t+" SET "+c+" = v.ref FROM unnest($1::bigint[], $2::bigint[]) AS v(id, ref) WHERE "+t+".id = v.id", g[0], g[1]); err != nil {
			return fmt.Errorf("%s.%s: %w", k.table, k.column, err)
		}
	}
	return nil
}

// digestWriter hashes what it forwards.
type digestWriter struct {
	w io.Writer
	h hash.Hash
}

func newDigestWriter(w io.Writer) *digestWriter { return &digestWriter{w: w, h: sha256.New()} }

func (d *digestWriter) Write(p []byte) (int, error) {
	n, err := d.w.Write(p)
	d.h.Write(p[:n])
	return n, err
}

func (d *digestWriter) digest() string { return hex.EncodeToString(d.h.Sum(nil)) }
