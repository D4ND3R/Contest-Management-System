// Package tasktypes implements how each kind of task is compiled and
// evaluated inside the sandbox: Batch, OutputOnly, Communication and
// TwoSteps. A task type receives self-contained jobs (see package jobs) and
// an Env giving it sandboxes, the verified blob cache and the blob store.
//
// Errors returned by task types are infrastructure failures (the job is
// retried); everything about the contestant's program is reported inside
// the returned results.
package tasktypes

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/sandbox"
)

// Env is what a task type needs to run a job on one worker slot.
type Env struct {
	Slot  *sandbox.Slot
	Stage *sandbox.Stage
	// FifoDir is a per-slot host directory for Communication FIFOs.
	FifoDir string
	Cache   *blob.Cache
	Store   blob.Store
	// CG reports whether isolate uses control groups (exact memory
	// accounting); without them memory is limited by address space.
	CG bool
	// SandboxDirs are extra read-only host directories (worker config).
	SandboxDirs []string
	// Checkers compiles and caches checker sources (shared by the worker).
	Checkers *CheckerCache
	// Seeds holds per-language compile seeds (shared by the worker).
	Seeds *SeedCache
	Log   *slog.Logger
}

// TaskType compiles and evaluates submissions of one kind of task.
type TaskType interface {
	// Compile builds the executables of a submission (job.Kind == compile).
	Compile(ctx context.Context, env *Env, job *jobs.Job) (*jobs.Compilation, error)
	// Evaluate runs the submission on one testcase.
	Evaluate(ctx context.Context, env *Env, job *jobs.Job, tc jobs.Testcase) (*jobs.Evaluation, error)
	// UserTest compiles and runs a submission on a contestant-provided input.
	UserTest(ctx context.Context, env *Env, job *jobs.Job) (*jobs.Compilation, *jobs.UserTestRun, error)
}

var registry = map[string]TaskType{}

func register(name string, t TaskType) { registry[name] = t }

// Get returns the task type called name.
func Get(name string) (TaskType, error) {
	t, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown task type %q", name)
	}
	return t, nil
}

// Names returns the registered task type names.
func Names() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	return out
}

// errInfra marks infrastructure failures.
var errInfra = errors.New("worker infrastructure error")

func infra(format string, a ...any) error {
	return fmt.Errorf("%w: %s", errInfra, fmt.Sprintf(format, a...))
}

// fetch returns the local cache path of a digest.
func (e *Env) fetch(ctx context.Context, digest string) (string, error) {
	p, err := e.Cache.Fetch(ctx, digest)
	if err != nil {
		return "", infra("fetch %s: %v", digest, err)
	}
	return p, nil
}

// put copies a blob into the box.
func (e *Env) put(ctx context.Context, b *sandbox.Box, name, digest string, mode os.FileMode) error {
	p, err := e.fetch(ctx, digest)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(name); dir != "." {
		if err := os.MkdirAll(b.Path(dir), 0o755); err != nil {
			return infra("mkdir %s: %v", dir, err)
		}
	}
	if err := b.CopyIn(p, name, mode); err != nil {
		return infra("copy %s into box: %v", name, err)
	}
	return nil
}

// stage makes a blob readable at /stage/name.
func (e *Env) stage(ctx context.Context, name, digest string) (string, error) {
	p, err := e.fetch(ctx, digest)
	if err != nil {
		return "", err
	}
	if err := e.Stage.Add(p, name); err != nil {
		return "", infra("stage %s: %v", name, err)
	}
	return e.Stage.Path(name), nil
}

// box returns a clean box of the slot.
func (e *Env) box(ctx context.Context, i int) (*sandbox.Box, error) {
	b, err := e.Slot.Box(ctx, i)
	if err != nil {
		return nil, infra("prepare box: %v", err)
	}
	return b, nil
}

// dirs returns the extra read-only mounts for a language.
func (e *Env) dirs(l *langs.Language) []sandbox.Dir {
	var out []sandbox.Dir
	seen := map[string]bool{}
	add := func(p string) {
		paths := []string{p}
		if strings.ContainsAny(p, "*?[") {
			paths, _ = filepath.Glob(p)
		}
		for _, p := range paths {
			if p != "" && !seen[p] {
				seen[p] = true
				out = append(out, sandbox.Dir{Inside: p, Maybe: true})
			}
		}
	}
	if l != nil {
		for _, d := range langs.DefaultDirs {
			add(d)
		}
		for _, d := range l.Dirs {
			add(d)
		}
	}
	for _, d := range e.SandboxDirs {
		add(d)
	}
	return out
}

// upload stores a box file in the blob store.
func (e *Env) upload(ctx context.Context, b *sandbox.Box, name string, limit int64) (string, error) {
	f, err := e.uploadFile(ctx, b, name, limit)
	return f.Digest, err
}

// uploadFile is upload returning the digest and size.
func (e *Env) uploadFile(ctx context.Context, b *sandbox.Box, name string, limit int64) (jobs.File, error) {
	f, size, err := b.OpenOutput(name)
	if err != nil {
		return jobs.File{}, err
	}
	defer f.Close()
	if size > limit {
		return jobs.File{}, fmt.Errorf("%s is too large (%d bytes)", name, size)
	}
	info, err := e.Store.Put(ctx, f)
	if err != nil {
		return jobs.File{}, infra("upload %s: %v", name, err)
	}
	return jobs.File{Name: name, Digest: info.Digest, Size: info.Size}, nil
}

// WallLimit returns the wall-clock limit for a CPU limit: the explicit one
// when set, else max(2×TL, TL+1s).
func WallLimit(timeMs, wallMs int64) time.Duration {
	if wallMs > 0 {
		return time.Duration(wallMs) * time.Millisecond
	}
	if timeMs <= 0 {
		return 0
	}
	tl := time.Duration(timeMs) * time.Millisecond
	return max(2*tl, tl+time.Second)
}

// CheckerCache compiles checker sources once per worker and keeps the
// executables by source digest.
type CheckerCache struct {
	mu    sync.Mutex
	dir   string
	built map[string]string // key -> host path of the executable
}

// NewCheckerCache stores compiled checkers under dir.
func NewCheckerCache(dir string) (*CheckerCache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &CheckerCache{dir: dir, built: map[string]string{}}, nil
}
