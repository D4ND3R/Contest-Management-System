// Package worker executes compilation and evaluation jobs in isolate
// sandboxes. A worker owns one slot per physical core; each slot runs one
// job at a time pinned to its core, with its own staging directory.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
	"github.com/D4ND3R/Contest-Management-System/internal/metrics"
	"github.com/D4ND3R/Contest-Management-System/internal/sandbox"
	"github.com/D4ND3R/Contest-Management-System/internal/tasktypes"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	jobDuration = metrics.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cms_worker_job_duration_seconds",
		Help:    "Time spent executing a job on a worker, by kind and outcome.",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60},
	}, []string{"kind", "outcome"})
)

// Executor runs jobs on sandbox slots.
type Executor struct {
	Name     string
	Slots    []*sandbox.Slot
	stages   []*sandbox.Stage
	workDir  string
	cache    *blob.Cache
	store    blob.Store
	checkers *tasktypes.CheckerCache
	seeds    *tasktypes.SeedCache
	cg       bool
	dirs     []string
	log      *slog.Logger
}

// NewExecutor prepares slots (one per configured or detected physical core),
// the verified testcase cache and the staging directories.
func NewExecutor(cfg config.Worker, store blob.Store, log *slog.Logger) (*Executor, error) {
	name := cfg.Name
	if name == "" {
		h, _ := os.Hostname()
		name = h + "-" + strconv.Itoa(cfg.BoxIDOffset)
	}
	cores := cfg.Cores
	if len(cores) == 0 {
		cores = sandbox.DefaultCores()
	}
	iso := &sandbox.Isolate{Path: cfg.IsolatePath, CG: cfg.IsolateCG, BoxRoot: cfg.IsolateBoxRoot}
	if cfg.Seccomp != "off" {
		l, err := sandbox.BuildLauncher(context.Background(), filepath.Join(cfg.WorkDir, "launcher"), "cc")
		switch {
		case err == nil:
			iso.Launcher = l
		case cfg.Seccomp == "on":
			return nil, fmt.Errorf("seccomp (worker.seccomp: on): %w", err)
		default:
			log.Warn("running without the seccomp filter (install a C compiler, or set worker.seccomp: off)", "error", err)
		}
	}
	cacheMax := int64(cfg.CacheMaxBytes)
	if cacheMax <= 0 {
		cacheMax = 2 << 30
	}
	cache, err := blob.NewCache(store, cfg.CacheDir, cacheMax)
	if err != nil {
		return nil, fmt.Errorf("testcase cache: %w", err)
	}
	checkers, err := tasktypes.NewCheckerCache(filepath.Join(cfg.WorkDir, "checkers"))
	if err != nil {
		return nil, err
	}
	seeds, err := tasktypes.NewSeedCache(filepath.Join(cfg.WorkDir, "seeds"))
	if err != nil {
		return nil, err
	}
	e := &Executor{
		Name: name, Slots: sandbox.NewSlots(iso, cores, cfg.BoxIDOffset, sandbox.Complement(cores)), cache: cache, store: cache,
		checkers: checkers, seeds: seeds, cg: cfg.IsolateCG, workDir: cfg.WorkDir, dirs: cfg.SandboxDirs, log: log,
	}
	for i := range e.Slots {
		st, err := sandbox.NewStage(filepath.Join(cfg.WorkDir, "stage", strconv.Itoa(i)))
		if err != nil {
			return nil, fmt.Errorf("stage: %w", err)
		}
		e.stages = append(e.stages, st)
	}
	return e, nil
}

// Seccomp reports whether programs run behind the seccomp filter.
func (e *Executor) Seccomp() bool {
	return len(e.Slots) > 0 && e.Slots[0].Isolate().Launcher != ""
}

// Close destroys every box.
func (e *Executor) Close() {
	for _, s := range e.Slots {
		s.Close(context.Background())
	}
}

func (e *Executor) env(slot int) *tasktypes.Env {
	return &tasktypes.Env{
		Slot: e.Slots[slot], Stage: e.stages[slot], Cache: e.cache, Store: e.store,
		FifoDir: filepath.Join(e.workDir, "fifo", strconv.Itoa(slot)),
		CG:      e.cg, SandboxDirs: e.dirs, Checkers: e.checkers, Seeds: e.seeds, Log: e.log,
	}
}

// Execute runs job on slot and always returns a result; infrastructure
// failures are reported in Result.Error so the dispatcher can retry.
func (e *Executor) Execute(ctx context.Context, slot int, job *jobs.Job) *jobs.Result {
	start := time.Now()
	res := jobs.ForJob(job, e.Name)
	err := e.execute(ctx, slot, job, res)
	outcome := "ok"
	if err != nil {
		outcome = "error"
		res.Error = err.Error()
		res.Compilation, res.Evaluations, res.UserTest = nil, nil, nil
		if errors.Is(err, sandbox.ErrSandbox) {
			e.Slots[slot].Recycle(ctx)
		}
		e.log.Warn("job failed", "job", job.ID, "kind", job.Kind, "submission", job.SubmissionID,
			"user_test", job.UserTestID, "attempt", job.Attempt, "error", err)
	}
	d := time.Since(start)
	res.DurationMs = d.Milliseconds()
	jobDuration.WithLabelValues(string(job.Kind), outcome).Observe(d.Seconds())
	return res
}

func (e *Executor) execute(ctx context.Context, slot int, job *jobs.Job, res *jobs.Result) error {
	tt, err := tasktypes.Get(job.TaskType)
	if err != nil {
		return err
	}
	env := e.env(slot)
	switch job.Kind {
	case jobs.KindCompile:
		c, err := tt.Compile(ctx, env, job)
		if err != nil {
			return err
		}
		res.Compilation = c
	case jobs.KindEvaluate:
		for _, tc := range job.Testcases {
			ev, err := tt.Evaluate(ctx, env, job, tc)
			if err != nil {
				return fmt.Errorf("testcase %s: %w", tc.Codename, err)
			}
			res.Evaluations = append(res.Evaluations, *ev)
		}
	case jobs.KindUserTest:
		c, r, err := tt.UserTest(ctx, env, job)
		if err != nil {
			return err
		}
		res.Compilation, res.UserTest = c, r
	default:
		return fmt.Errorf("unknown job kind %q", job.Kind)
	}
	return nil
}
