package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/metrics"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// Kinds of backups.
const (
	KindScheduled = "scheduled"
	KindManual    = "manual"
	KindCLI       = "cli"
	KindUpgrade   = "upgrade" // taken by cmsctl upgrade before migrating
)

// Entry describes one backup; it is stored next to the archive as
// <name without .tar.zst>.json, so the list survives database restores.
type Entry struct {
	Name     string    `json:"name"`
	Kind     string    `json:"kind"`
	By       string    `json:"by,omitempty"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
	// Status is done or failed.
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256,omitempty"`
	Tables    int    `json:"tables"`
	Rows      int64  `json:"rows"`
	Blobs     int    `json:"blobs"`
	BlobBytes int64  `json:"blob_bytes"`
	Missing   int    `json:"missing_blobs,omitempty"`
	// Remote is set once the off-site copy is stored; RemoteError when it
	// failed (the local copy is still good).
	Remote      bool   `json:"remote,omitempty"`
	RemoteError string `json:"remote_error,omitempty"`
}

// Duration of the backup.
func (e Entry) Duration() time.Duration { return e.Finished.Sub(e.Started).Round(time.Second) }

// Status describes the backup in progress.
type Status struct {
	Kind    string
	By      string
	Started time.Time
	bytes   atomic.Int64
}

// Bytes processed so far.
func (s *Status) Bytes() int64 { return s.bytes.Load() }

// Remote is an off-site destination (S3).
type Remote interface {
	Upload(ctx context.Context, name, path string) error
	Delete(ctx context.Context, name string) error
}

// ErrBusy means another backup is running.
var ErrBusy = errors.New("another backup is running")

var (
	backupRuns = metrics.NewCounterVec(prometheus.CounterOpts{Name: "cms_backups_total",
		Help: "Backups taken, by kind and outcome."}, []string{"kind", "outcome"})
	backupLast = metrics.NewGaugeVec(prometheus.GaugeOpts{Name: "cms_backup_last_success_timestamp_seconds",
		Help: "Time of the last successful backup."}, nil)
)

// Runner takes backups into a directory (plus an optional remote copy),
// on a schedule and on demand, one at a time across processes.
type Runner struct {
	pool   *pgxpool.Pool
	store  blob.Store
	cfg    config.Backup
	log    *slog.Logger
	lease  *queue.Lease
	remote Remote
	// Alert, when set, tells the administrators about a failed backup.
	Alert func(ctx context.Context, msg string)
	// CheckInterval is how often the schedule is looked at.
	CheckInterval time.Duration

	mu      sync.Mutex
	running *Status
	wg      sync.WaitGroup
}

// NewRunner creates a runner; q (for the cross-process lease) and remote
// may be nil.
func NewRunner(pool *pgxpool.Pool, store blob.Store, cfg config.Backup, q *queue.Queue, remote Remote, log *slog.Logger) *Runner {
	r := &Runner{pool: pool, store: store, cfg: cfg, log: log, remote: remote, CheckInterval: 30 * time.Second}
	if q != nil {
		r.lease = q.NewLease("backup", 30*time.Second)
	}
	return r
}

// Config returns the backup configuration.
func (r *Runner) Config() config.Backup { return r.cfg }

// HasRemote reports whether an off-site copy is configured.
func (r *Runner) HasRemote() bool { return r.remote != nil }

// Running returns the backup in progress in this process, or nil.
func (r *Runner) Running() *Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// Start begins a backup in the background.
func (r *Runner) Start(ctx context.Context, kind, by string) error {
	st, err := r.begin(kind, by)
	if err != nil {
		return err
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		_, _ = r.run(context.WithoutCancel(ctx), st)
	}()
	return nil
}

// Wait waits for backups started with Start.
func (r *Runner) Wait() { r.wg.Wait() }

// Backup takes a backup and waits for it.
func (r *Runner) Backup(ctx context.Context, kind, by string) (*Entry, error) {
	st, err := r.begin(kind, by)
	if err != nil {
		return nil, err
	}
	return r.run(ctx, st)
}

func (r *Runner) begin(kind, by string) (*Status, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running != nil {
		return nil, ErrBusy
	}
	r.running = &Status{Kind: kind, By: by, Started: time.Now().UTC()}
	return r.running, nil
}

func (r *Runner) run(ctx context.Context, st *Status) (*Entry, error) {
	defer func() {
		r.mu.Lock()
		r.running = nil
		r.mu.Unlock()
	}()
	if r.lease != nil {
		ok, err := r.lease.TryAcquire(ctx)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrBusy
		}
		lctx, cancel := context.WithCancel(ctx)
		defer cancel()
		defer r.lease.Release(context.WithoutCancel(ctx))
		go func() {
			t := time.NewTicker(10 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-lctx.Done():
					return
				case <-t.C:
					if ok, err := r.lease.Renew(lctx); err == nil && !ok {
						cancel()
					}
				}
			}
		}()
		ctx = lctx
	}
	e, err := r.write(ctx, st)
	outcome := "done"
	if err != nil {
		outcome = "failed"
		r.log.Error("backup failed", "kind", st.Kind, "error", err)
		if r.Alert != nil {
			r.Alert(ctx, "Backup failed: "+err.Error())
		}
	} else {
		backupLast.WithLabelValues().Set(float64(e.Finished.Unix()))
		r.log.Info("backup done", "name", e.Name, "size", e.Size, "blobs", e.Blobs, "took", e.Duration())
	}
	backupRuns.WithLabelValues(st.Kind, outcome).Inc()
	if st.Kind == KindScheduled {
		if err := r.rotate(ctx); err != nil {
			r.log.Warn("backup rotation", "error", err)
		}
	}
	return e, err
}

var namePattern = regexp.MustCompile(`^cms-backup-[0-9]{8}T[0-9]{6}\.[0-9]{3}Z-[a-z]+\.tar\.zst$`)

// ValidName reports whether name can be a backup file name (it is used to
// build paths, so this also rules out traversal).
func ValidName(name string) bool { return namePattern.MatchString(name) }

func metaPath(dir, name string) string {
	return filepath.Join(dir, strings.TrimSuffix(name, ".tar.zst")+".json")
}

func (r *Runner) write(ctx context.Context, st *Status) (*Entry, error) {
	e := &Entry{Kind: st.Kind, By: st.By, Started: st.Started,
		Name: "cms-backup-" + st.Started.Format("20060102T150405.000Z") + "-" + st.Kind + ".tar.zst"}
	if err := os.MkdirAll(r.cfg.Dir, 0o700); err != nil {
		return nil, err
	}
	final := filepath.Join(r.cfg.Dir, e.Name)
	tmp := final + ".partial"
	fail := func(err error) (*Entry, error) {
		os.Remove(tmp)
		e.Status, e.Error, e.Finished = "failed", err.Error(), time.Now().UTC()
		if werr := r.writeMeta(e); werr != nil {
			r.log.Warn("write backup metadata", "error", werr)
		}
		return e, err
	}
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fail(err)
	}
	hw := NewFileHasher()
	stats, err := Dump(ctx, r.pool, r.store, io.MultiWriter(f, hw), Options{MaxRate: int64(r.cfg.MaxRate), Progress: st.bytes.Store})
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, final)
	}
	if err != nil {
		return fail(err)
	}
	e.Status, e.Finished, e.Size, e.SHA256 = "done", time.Now().UTC(), hw.Size(), hw.Sum()
	e.Tables, e.Rows, e.Blobs, e.BlobBytes, e.Missing = stats.Tables, stats.Rows, stats.Blobs, stats.BlobBytes, len(stats.Missing)
	if err := r.writeMeta(e); err != nil {
		return e, err
	}
	if r.remote != nil {
		if err := r.remote.Upload(ctx, e.Name, final); err != nil {
			e.RemoteError = err.Error()
			r.log.Error("backup off-site copy failed", "name", e.Name, "error", err)
			if r.Alert != nil {
				r.Alert(ctx, "Backup "+e.Name+" was not copied to S3: "+err.Error())
			}
		} else {
			e.Remote = true
		}
		if err := r.writeMeta(e); err != nil {
			return e, err
		}
		if e.Remote {
			if err := r.remote.Upload(ctx, filepath.Base(metaPath(r.cfg.Dir, e.Name)), metaPath(r.cfg.Dir, e.Name)); err != nil {
				r.log.Warn("backup metadata off-site copy", "error", err)
			}
		}
	}
	return e, nil
}

func (r *Runner) writeMeta(e *Entry) error {
	b, _ := json.MarshalIndent(e, "", "  ")
	p := metaPath(r.cfg.Dir, e.Name)
	if err := os.WriteFile(p+".tmp", b, 0o600); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}

// List returns the backups in the directory, newest first. Archives copied
// in by hand (without metadata) are listed too.
func (r *Runner) List() ([]Entry, error) {
	des, err := os.ReadDir(r.cfg.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []Entry
	for _, de := range des {
		if !strings.HasSuffix(de.Name(), ".json") || !strings.HasPrefix(de.Name(), "cms-backup-") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(r.cfg.Dir, de.Name()))
		if err != nil {
			continue
		}
		var e Entry
		if json.Unmarshal(b, &e) != nil || !ValidName(e.Name) {
			continue
		}
		seen[e.Name] = true
		out = append(out, e)
	}
	for _, de := range des {
		if !ValidName(de.Name()) || seen[de.Name()] {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		// The name carries the time the backup was taken.
		at, err := time.Parse("20060102T150405.000Z", de.Name()[len("cms-backup-"):len("cms-backup-")+len("20060102T150405.000Z")])
		if err != nil {
			at = info.ModTime().UTC()
		}
		out = append(out, Entry{Name: de.Name(), Kind: "external", Status: "done", Size: info.Size(), Started: at, Finished: at})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out, nil
}

// Path returns the file of a finished backup.
func (r *Runner) Path(name string) (string, error) {
	if !ValidName(name) {
		return "", os.ErrNotExist
	}
	p := filepath.Join(r.cfg.Dir, name)
	if _, err := os.Stat(p); err != nil {
		return "", err
	}
	return p, nil
}

// Delete removes a backup (and its off-site copy).
func (r *Runner) Delete(ctx context.Context, name string) error {
	if !ValidName(name) {
		return os.ErrNotExist
	}
	var errs []error
	for _, p := range []string{filepath.Join(r.cfg.Dir, name), metaPath(r.cfg.Dir, name)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	if r.remote != nil {
		for _, n := range []string{name, filepath.Base(metaPath(r.cfg.Dir, name))} {
			if err := r.remote.Delete(ctx, n); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// rotate keeps the newest cfg.Keep scheduled backups.
func (r *Runner) rotate(ctx context.Context) error {
	list, err := r.List()
	if err != nil {
		return err
	}
	kept := 0
	var errs []error
	for _, e := range list {
		if e.Kind != KindScheduled {
			continue
		}
		if kept < r.cfg.Keep {
			kept++
			continue
		}
		if err := r.Delete(ctx, e.Name); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Run takes the scheduled backups until ctx ends.
func (r *Runner) Run(ctx context.Context) error {
	t := time.NewTicker(r.CheckInterval)
	defer t.Stop()
	for {
		if err := r.Tick(ctx); err != nil && ctx.Err() == nil && !errors.Is(err, ErrBusy) {
			r.log.Warn("backup schedule", "error", err)
		}
		select {
		case <-ctx.Done():
			r.Wait()
			return nil
		case <-t.C:
		}
	}
}

// Tick takes a scheduled backup when one is due.
func (r *Runner) Tick(ctx context.Context) error {
	interval := r.cfg.Interval.D()
	var live bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM contests
WHERE start_time - interval '30 minutes' <= now() AND now() <= stop_time + interval '30 minutes')`).Scan(&live); err != nil {
		return err
	}
	if live {
		interval = r.cfg.ContestInterval.D()
	}
	if interval <= 0 {
		return nil
	}
	list, err := r.List()
	if err != nil {
		return err
	}
	for _, e := range list {
		if e.Kind == KindScheduled {
			if time.Since(e.Started) < interval {
				return nil
			}
			break
		}
	}
	_, err = r.Backup(ctx, KindScheduled, "")
	return err
}

// String is a one-line summary for logs and the CLI.
func (e *Entry) String() string {
	return fmt.Sprintf("%s (%d bytes, %d rows, %d blobs, sha256 %s)", e.Name, e.Size, e.Rows, e.Blobs, e.SHA256)
}
