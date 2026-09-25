// Package backup writes and restores complete CMS backups: the database and
// every blob in a single file whose integrity can be verified.
//
// A backup is a zstd-compressed tar archive with, in this order:
//
//	cms-backup.json    header: format, time, CMS version, migrations, tables
//	db/<table>/<n>     COPY (text format) output of each table, in chunks
//	db/sequences.json  sequence values
//	blobs/<digest>     every registered blob (the name is its SHA-256)
//	manifest.json      size and SHA-256 of every other file, row and blob counts
//
// The database part comes from one REPEATABLE READ snapshot, so it is
// consistent; blobs are immutable, so they are streamed after the snapshot
// is released. Restoring checks every hash before committing.
package backup

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"strings"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/version"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/klauspost/compress/zstd"
)

// FormatVersion is written in every header; readers reject other values.
const FormatVersion = 1

const (
	headerName    = "cms-backup.json"
	manifestName  = "manifest.json"
	sequencesName = "db/sequences.json"
	// chunkSize bounds the memory used per table while dumping (tar
	// entries need their size up front).
	chunkSize = 8 << 20
	// metaLimit bounds the JSON files read while restoring.
	metaLimit = 64 << 20
)

// Table is one dumped table and the columns in its COPY data.
type Table struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
}

// Header opens every backup.
type Header struct {
	Format     int       `json:"format"`
	CreatedAt  time.Time `json:"created_at"`
	Version    string    `json:"cms_version"`
	Migrations []string  `json:"migrations"`
	Tables     []Table   `json:"tables"`
}

// FileSum is the size and SHA-256 of one archive member.
type FileSum struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Manifest closes every backup.
type Manifest struct {
	Files     map[string]FileSum `json:"files"`
	Rows      map[string]int64   `json:"rows"`
	Blobs     int                `json:"blobs"`
	BlobBytes int64              `json:"blob_bytes"`
	// Missing lists blobs registered in the database but absent from the
	// store when the backup was taken.
	Missing []string `json:"missing_blobs,omitempty"`
}

// Sequence is the value of one sequence.
type Sequence struct {
	Name  string `json:"name"`
	Value *int64 `json:"value"`
}

// Stats summarises a dump, a verification or a restore.
type Stats struct {
	Header    Header
	Tables    int
	Rows      int64
	DBBytes   int64
	Blobs     int
	BlobBytes int64
	Missing   []string
}

// Options tune a dump.
type Options struct {
	// MaxRate bounds the bytes read per second from the database and the
	// blob store (0: unlimited), so a backup never starves the web servers.
	MaxRate int64
	// Progress, when set, receives the bytes processed so far.
	Progress func(int64)
}

// Dump writes a complete backup to w.
func Dump(ctx context.Context, pool *pgxpool.Pool, store blob.Store, w io.Writer, opt Options) (*Stats, error) {
	zw, err := zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return nil, err
	}
	aw := &archiveWriter{tw: tar.NewWriter(zw), sums: map[string]FileSum{}, lim: &limiter{rate: opt.MaxRate, progress: opt.Progress, start: time.Now()}}
	st, err := aw.dump(ctx, pool, store)
	if err != nil {
		zw.Close()
		return nil, err
	}
	if err := aw.tw.Close(); err != nil {
		zw.Close()
		return nil, err
	}
	return st, zw.Close()
}

type blobRow struct {
	digest string
	size   int64
}

func (aw *archiveWriter) dump(ctx context.Context, pool *pgxpool.Pool, store blob.Store) (*Stats, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck
	h := Header{Format: FormatVersion, CreatedAt: time.Now().UTC().Truncate(time.Second), Version: version.String()}
	if h.Migrations, err = appliedMigrations(ctx, tx); err != nil {
		return nil, err
	}
	if len(h.Migrations) == 0 {
		return nil, errors.New("the database has no schema (run the migrations first)")
	}
	if h.Tables, err = listTables(ctx, tx); err != nil {
		return nil, err
	}
	seqs, err := listSequences(ctx, tx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, "SELECT digest, size FROM blobs ORDER BY digest")
	if err != nil {
		return nil, err
	}
	blobs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (blobRow, error) {
		var b blobRow
		return b, r.Scan(&b.digest, &b.size)
	})
	if err != nil {
		return nil, err
	}
	hb, _ := json.MarshalIndent(h, "", "  ")
	if err := aw.writeFile(headerName, hb); err != nil {
		return nil, err
	}
	st := &Stats{Header: h, Tables: len(h.Tables)}
	man := Manifest{Files: aw.sums, Rows: map[string]int64{}}
	for _, t := range h.Tables {
		cw := &chunkWriter{aw: aw, ctx: ctx, prefix: "db/" + t.Name + "/"}
		tag, err := tx.Conn().PgConn().CopyTo(ctx, cw, copySQL(t, "TO STDOUT"))
		if err == nil {
			err = cw.flush()
		}
		if err != nil {
			return nil, fmt.Errorf("dump table %s: %w", t.Name, err)
		}
		man.Rows[t.Name] = tag.RowsAffected()
		st.Rows += tag.RowsAffected()
		st.DBBytes += cw.total
	}
	sb, _ := json.MarshalIndent(seqs, "", "  ")
	if err := aw.writeFile(sequencesName, sb); err != nil {
		return nil, err
	}
	// The snapshot is not needed for the (immutable) blob contents.
	if err := tx.Rollback(ctx); err != nil {
		return nil, err
	}
	conn.Release()
	for _, b := range blobs {
		n, err := aw.writeBlob(ctx, store, b)
		if errors.Is(err, blob.ErrNotFound) {
			man.Missing = append(man.Missing, b.digest)
			continue
		}
		if err != nil {
			return nil, err
		}
		man.Blobs++
		man.BlobBytes += n
	}
	st.Blobs, st.BlobBytes, st.Missing = man.Blobs, man.BlobBytes, man.Missing
	mb, _ := json.MarshalIndent(man, "", "  ")
	return st, aw.writeRaw(manifestName, mb)
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

// listTables returns every table of the schema but the migration log (the
// schema itself is recreated by the migrations). Generated columns are left
// out of the COPY data.
func listTables(ctx context.Context, tx pgx.Tx) ([]Table, error) {
	rows, err := tx.Query(ctx, `
SELECT c.relname, array_agg(a.attname::text ORDER BY a.attnum)
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped AND a.attgenerated = ''
WHERE n.nspname = current_schema() AND c.relkind = 'r' AND c.relname <> 'schema_migrations'
GROUP BY c.relname ORDER BY c.relname`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Table, error) {
		var t Table
		return t, r.Scan(&t.Name, &t.Columns)
	})
}

func listSequences(ctx context.Context, tx pgx.Tx) ([]Sequence, error) {
	rows, err := tx.Query(ctx, `SELECT sequencename::text, last_value FROM pg_sequences
WHERE schemaname = current_schema() ORDER BY sequencename`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Sequence, error) {
		var s Sequence
		return s, r.Scan(&s.Name, &s.Value)
	})
}

func copySQL(t Table, dir string) string {
	cols := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		cols[i] = pgx.Identifier{c}.Sanitize()
	}
	return "COPY " + pgx.Identifier{t.Name}.Sanitize() + " (" + strings.Join(cols, ", ") + ") " + dir
}

// ---------------------------------------------------------------- writing

type archiveWriter struct {
	tw   *tar.Writer
	sums map[string]FileSum
	lim  *limiter
}

func (aw *archiveWriter) header(name string, size int64) error {
	return aw.tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: size, ModTime: time.Now().UTC().Truncate(time.Second), Format: tar.FormatPAX})
}

// writeRaw adds a member that the manifest does not list (the manifest).
func (aw *archiveWriter) writeRaw(name string, b []byte) error {
	if err := aw.header(name, int64(len(b))); err != nil {
		return err
	}
	_, err := aw.tw.Write(b)
	return err
}

func (aw *archiveWriter) writeFile(name string, b []byte) error {
	sum := sha256.Sum256(b)
	aw.sums[name] = FileSum{Size: int64(len(b)), SHA256: hex.EncodeToString(sum[:])}
	return aw.writeRaw(name, b)
}

func (aw *archiveWriter) writeBlob(ctx context.Context, store blob.Store, b blobRow) (int64, error) {
	rc, err := store.Open(ctx, b.digest)
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	if err := aw.header("blobs/"+b.digest, b.size); err != nil {
		return 0, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(aw.tw, h), &limitedReader{r: io.LimitReader(rc, b.size), lim: aw.lim, ctx: ctx})
	if err != nil {
		return n, fmt.Errorf("blob %s: %w", b.digest, err)
	}
	if n != b.size || hex.EncodeToString(h.Sum(nil)) != b.digest {
		return n, fmt.Errorf("blob %s is corrupted in the store (%d bytes read, %d registered)", b.digest, n, b.size)
	}
	return n, nil
}

// chunkWriter splits one table's COPY output into tar members.
type chunkWriter struct {
	aw     *archiveWriter
	ctx    context.Context
	prefix string
	buf    []byte
	n      int
	total  int64
}

func (c *chunkWriter) Write(p []byte) (int, error) {
	if err := c.aw.lim.wait(c.ctx, len(p)); err != nil {
		return 0, err
	}
	written := 0
	for len(p) > 0 {
		room := chunkSize - len(c.buf)
		k := min(room, len(p))
		c.buf = append(c.buf, p[:k]...)
		p = p[k:]
		written += k
		if len(c.buf) == chunkSize {
			if err := c.flush(); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

func (c *chunkWriter) flush() error {
	if len(c.buf) == 0 {
		return nil
	}
	if err := c.aw.writeFile(fmt.Sprintf("%s%06d", c.prefix, c.n), c.buf); err != nil {
		return err
	}
	c.total += int64(len(c.buf))
	c.n++
	c.buf = c.buf[:0]
	return nil
}

// limiter throttles reads to rate bytes per second and reports progress.
type limiter struct {
	rate     int64
	start    time.Time
	n        int64
	progress func(int64)
}

func (l *limiter) wait(ctx context.Context, n int) error {
	l.n += int64(n)
	if l.progress != nil {
		l.progress(l.n)
	}
	if l.rate <= 0 {
		return ctx.Err()
	}
	due := time.Duration(float64(l.n) / float64(l.rate) * float64(time.Second))
	if d := due - time.Since(l.start); d > 5*time.Millisecond {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

type limitedReader struct {
	r   io.Reader
	lim *limiter
	ctx context.Context
}

func (l *limitedReader) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	if n > 0 {
		if werr := l.lim.wait(l.ctx, n); werr != nil {
			return n, werr
		}
	}
	return n, err
}

// ---------------------------------------------------------------- reading

// RestoreOptions tune a restore.
type RestoreOptions struct {
	// Force replaces a database that already holds data: every object of
	// its schema is dropped first.
	Force bool
}

// Verify reads a whole backup and checks every hash without touching any
// database or store.
func Verify(ctx context.Context, r io.Reader) (*Stats, error) {
	return read(ctx, r, nil)
}

// Restore loads a backup into the database behind pool and the blob store.
// The target must be empty (or opt.Force set); the data is committed only
// when every hash matched, and newer migrations are applied afterwards.
func Restore(ctx context.Context, pool *pgxpool.Pool, store blob.Store, r io.Reader, opt RestoreOptions) (*Stats, error) {
	// Registering blobs is the restored blobs table's job.
	if t, ok := store.(interface{ Unwrap() blob.Store }); ok {
		store = t.Unwrap()
	}
	return read(ctx, r, &target{pool: pool, store: store, force: opt.Force})
}

// ErrNotBackup means the input is not a CMS backup.
var ErrNotBackup = errors.New("not a CMS backup")

func read(ctx context.Context, r io.Reader, tg *target) (*Stats, error) {
	zr, err := zstd.NewReader(r, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	first, err := tr.Next()
	if err != nil || first.Name != headerName {
		return nil, ErrNotBackup
	}
	sums := map[string]FileSum{}
	hb, err := readMeta(tr, headerName, sums)
	if err != nil {
		return nil, err
	}
	var h Header
	if err := json.Unmarshal(hb, &h); err != nil {
		return nil, fmt.Errorf("%w: bad header: %v", ErrNotBackup, err)
	}
	if h.Format != FormatVersion {
		return nil, fmt.Errorf("unsupported backup format %d (this CMS reads format %d)", h.Format, FormatVersion)
	}
	last, err := checkMigrations(h.Migrations)
	if err != nil {
		return nil, err
	}
	tables := map[string]Table{}
	for _, t := range h.Tables {
		tables[t.Name] = t
	}
	if tg != nil {
		if err := tg.prepare(ctx, h, last); err != nil {
			return nil, err
		}
		defer tg.abort()
	}
	st := &Stats{Header: h, Tables: len(h.Tables)}
	rows := map[string]int64{}
	var (
		cur  string
		load *tableLoad
		done = map[string]bool{}
		seqs []Sequence
		man  *Manifest
	)
	endTable := func() error {
		if cur == "" {
			return nil
		}
		done[cur] = true
		if load != nil {
			n, err := load.finish()
			if err != nil {
				return fmt.Errorf("restore table %s: %w", cur, err)
			}
			rows[cur] = n
			load = nil
		}
		cur = ""
		return nil
	}
	for {
		e, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("truncated or damaged backup: %w", err)
		}
		if man != nil {
			return nil, fmt.Errorf("unexpected %q after the manifest", e.Name)
		}
		switch name := e.Name; {
		case name == sequencesName:
			if err := endTable(); err != nil {
				return nil, err
			}
			b, err := readMeta(tr, name, sums)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(b, &seqs); err != nil {
				return nil, fmt.Errorf("sequences: %w", err)
			}
		case strings.HasPrefix(name, "db/"):
			table := name[len("db/"):strings.LastIndexByte(name, '/')]
			t, ok := tables[table]
			if !ok || done[table] {
				return nil, fmt.Errorf("unexpected member %q", name)
			}
			if table != cur {
				if err := endTable(); err != nil {
					return nil, err
				}
				cur = table
				if tg != nil {
					load = tg.startTable(ctx, t)
				}
			}
			var w io.Writer = io.Discard
			if load != nil {
				w = load
			}
			hs := sha256.New()
			n, err := io.Copy(io.MultiWriter(w, hs), tr)
			if err != nil {
				if load != nil {
					if _, lerr := load.finish(); lerr != nil {
						err = lerr
					}
					load = nil
				}
				return nil, fmt.Errorf("restore table %s: %w", table, err)
			}
			sums[name] = FileSum{Size: n, SHA256: hex.EncodeToString(hs.Sum(nil))}
			st.DBBytes += n
		case strings.HasPrefix(name, "blobs/"):
			if err := endTable(); err != nil {
				return nil, err
			}
			digest := name[len("blobs/"):]
			if !blob.ValidDigest(digest) {
				return nil, fmt.Errorf("unexpected member %q", name)
			}
			n, err := restoreBlob(ctx, tr, digest, tg)
			if err != nil {
				return nil, err
			}
			st.Blobs++
			st.BlobBytes += n
		case name == manifestName:
			if err := endTable(); err != nil {
				return nil, err
			}
			b, err := readMeta(tr, name, nil)
			if err != nil {
				return nil, err
			}
			man = &Manifest{}
			if err := json.Unmarshal(b, man); err != nil {
				return nil, fmt.Errorf("manifest: %w", err)
			}
		default:
			return nil, fmt.Errorf("unexpected member %q", name)
		}
	}
	if man == nil {
		return nil, errors.New("truncated backup: the manifest is missing")
	}
	if err := checkManifest(man, sums, st, tables); err != nil {
		return nil, err
	}
	for _, n := range man.Rows {
		st.Rows += n
	}
	st.Missing = man.Missing
	if tg != nil {
		for t, want := range man.Rows {
			if rows[t] != want {
				return nil, fmt.Errorf("table %s: %d rows restored, the backup has %d", t, rows[t], want)
			}
		}
		if err := tg.commit(ctx, seqs); err != nil {
			return nil, err
		}
	}
	return st, nil
}

func readMeta(r io.Reader, name string, sums map[string]FileSum) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, metaLimit+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if len(b) > metaLimit {
		return nil, fmt.Errorf("%s is too large", name)
	}
	if sums != nil {
		s := sha256.Sum256(b)
		sums[name] = FileSum{Size: int64(len(b)), SHA256: hex.EncodeToString(s[:])}
	}
	return b, nil
}

func restoreBlob(ctx context.Context, r io.Reader, digest string, tg *target) (int64, error) {
	if tg == nil {
		h := sha256.New()
		n, err := io.Copy(h, r)
		if err != nil {
			return n, err
		}
		if hex.EncodeToString(h.Sum(nil)) != digest {
			return n, fmt.Errorf("blob %s is corrupted in the backup", digest)
		}
		return n, nil
	}
	info, err := tg.store.Put(ctx, r)
	if err != nil {
		return 0, fmt.Errorf("store blob %s: %w", digest, err)
	}
	if info.Digest != digest {
		// Content-addressed: the wrong content went under its own name and
		// is harmless (garbage-collected later), but the backup is bad.
		return 0, fmt.Errorf("blob %s is corrupted in the backup", digest)
	}
	return info.Size, nil
}

func checkManifest(man *Manifest, sums map[string]FileSum, st *Stats, tables map[string]Table) error {
	var bad []string
	for name, want := range man.Files {
		got, ok := sums[name]
		switch {
		case !ok:
			bad = append(bad, name+" is missing")
		case got != want:
			bad = append(bad, name+" is corrupted")
		}
	}
	for name := range sums {
		if _, ok := man.Files[name]; !ok {
			bad = append(bad, name+" is not in the manifest")
		}
	}
	if st.Blobs != man.Blobs || st.BlobBytes != man.BlobBytes {
		bad = append(bad, fmt.Sprintf("%d blobs (%d bytes) read, the manifest lists %d (%d bytes)", st.Blobs, st.BlobBytes, man.Blobs, man.BlobBytes))
	}
	for t := range man.Rows {
		if _, ok := tables[t]; !ok {
			bad = append(bad, "rows of unknown table "+t)
		}
	}
	if len(bad) > 0 {
		if len(bad) > 5 {
			bad = append(bad[:5], fmt.Sprintf("and %d more problems", len(bad)-5))
		}
		return fmt.Errorf("integrity check failed: %s", strings.Join(bad, "; "))
	}
	return nil
}

// FileHasher counts and hashes what is written to it (whole-file digests).
type FileHasher struct {
	h hash.Hash
	n int64
}

// NewFileHasher returns an empty FileHasher.
func NewFileHasher() *FileHasher { return &FileHasher{h: sha256.New()} }

func (w *FileHasher) Write(p []byte) (int, error) {
	w.h.Write(p)
	w.n += int64(len(p))
	return len(p), nil
}

// Sum returns the hex SHA-256 of everything written.
func (w *FileHasher) Sum() string { return hex.EncodeToString(w.h.Sum(nil)) }

// Size returns the number of bytes written.
func (w *FileHasher) Size() int64 { return w.n }
