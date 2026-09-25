// Package dispatcher orchestrates judging (the equivalent of CMS's
// EvaluationService + ScoringService): it turns new submissions and user
// tests into compilation and evaluation jobs, processes the results the
// workers publish, scores submissions, maintains the per-task aggregates,
// feeds the ranking and handles reevaluations.
//
// Every state transition is a PostgreSQL transaction on the result row
// (SELECT ... FOR UPDATE) that checks the result's generation, so
// duplicated, reordered or stale job results are harmless. Side effects
// that follow a transition (enqueueing jobs, notifying browsers, ranking
// updates) happen after commit; if the dispatcher dies in between, the
// sweeper re-derives the missing work from the database.
package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/app"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/metrics"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

var (
	resultsProcessed = metrics.NewCounterVec(prometheus.CounterOpts{
		Name: "cms_dispatcher_results_total",
		Help: "Job results processed by the dispatcher, by kind and outcome.",
	}, []string{"kind", "outcome"})
	resultLatency = metrics.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cms_dispatcher_result_seconds",
		Help:    "Time to process one job result (transaction + side effects).",
		Buckets: metrics.LatencyBuckets,
	}, []string{"kind"})
)

// Options tune the dispatcher.
type Options struct {
	// Namespace of the Redis keys (default "cms:").
	Namespace string
	// SweepInterval: how often the database is scanned for work to re-derive.
	SweepInterval time.Duration
	// StaleAfter: results whose jobs were enqueued longer ago than this are
	// considered lost and re-enqueued (duplicates are harmless).
	StaleAfter time.Duration
	// MaxAttempts per job before the submission is marked as a system error.
	MaxAttempts int
	// TestcasesPerJob groups testcases in evaluation jobs (1 = most parallel).
	TestcasesPerJob int
	// Parallelism: concurrent result processors.
	Parallelism int
	// Consumer name in the Redis consumer groups (default: hostname).
	Consumer string
}

func (o *Options) defaults() {
	if o.Namespace == "" {
		o.Namespace = "cms:"
	}
	if o.SweepInterval <= 0 {
		o.SweepInterval = 30 * time.Second
	}
	if o.StaleAfter <= 0 {
		o.StaleAfter = 15 * time.Minute
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 3
	}
	if o.TestcasesPerJob <= 0 {
		o.TestcasesPerJob = 1
	}
	if o.Parallelism <= 0 {
		o.Parallelism = 8
	}
	if o.Consumer == "" {
		o.Consumer, _ = os.Hostname()
	}
}

// Dispatcher is the orchestration service.
type Dispatcher struct {
	pool     *pgxpool.Pool
	rdb      *redis.Client
	q        *queue.Queue
	langs    *langs.Registry
	log      *slog.Logger
	opts     Options
	datasets *datasetCache
	sweepNow chan struct{}
}

// New creates a dispatcher.
func New(pool *pgxpool.Pool, rdb *redis.Client, reg *langs.Registry, log *slog.Logger, opts Options) *Dispatcher {
	opts.defaults()
	return &Dispatcher{
		pool: pool, rdb: rdb, q: queue.New(rdb, opts.Namespace), langs: reg, log: log, opts: opts,
		datasets: newDatasetCache(30 * time.Second), sweepNow: make(chan struct{}, 1),
	}
}

// Queue returns the dispatcher's queue handle.
func (d *Dispatcher) Queue() *queue.Queue { return d.q }

// Run serves until ctx ends. Several dispatchers may run; a Redis lease
// makes exactly one of them active.
func (d *Dispatcher) Run(ctx context.Context) error {
	if err := d.q.Setup(ctx); err != nil {
		return err
	}
	lease := d.q.NewLease("dispatcher", 10*time.Second)
	return lease.Run(ctx, func(ctx context.Context) error {
		d.log.Info("dispatcher active", "consumer", d.opts.Consumer)
		d.datasets.invalidate()
		if n, err := d.q.AdoptPending(ctx, d.opts.Consumer); err != nil {
			return err
		} else if n > 0 {
			d.log.Info("adopted pending results/events of a previous dispatcher", "count", n)
		}
		g, ctx := app.NewGroup(ctx)
		g.Go(d.eventLoop)
		g.Go(d.resultLoop)
		g.Go(d.sweepLoop)
		return g.Wait()
	})
}

// RequestSweep asks the sweeper to run as soon as possible.
func (d *Dispatcher) RequestSweep() {
	select {
	case d.sweepNow <- struct{}{}:
	default:
	}
}

func (d *Dispatcher) eventLoop(ctx context.Context) error {
	for ctx.Err() == nil {
		evs, err := d.q.ReadEvents(ctx, d.opts.Consumer, 64, 2*time.Second)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			d.log.Error("read events", "error", err)
			sleepCtx(ctx, time.Second)
			continue
		}
		var ids []string
		for _, e := range evs {
			if e.Event != nil {
				if err := d.handleEvent(ctx, e.Event); err != nil {
					d.log.Error("handle event", "event", e.Event, "error", err)
					// The sweeper will re-derive the work; do not block the stream.
				}
			}
			ids = append(ids, e.ID)
		}
		if err := d.q.AckEvents(ctx, ids...); err != nil && ctx.Err() == nil {
			d.log.Error("ack events", "error", err)
		}
	}
	return nil
}

func (d *Dispatcher) handleEvent(ctx context.Context, e *queue.Event) error {
	switch e.Kind {
	case queue.EventSubmission:
		return d.newSubmission(ctx, e.SubmissionID)
	case queue.EventUserTest:
		return d.newUserTest(ctx, e.UserTestID)
	case queue.EventReevaluate:
		d.RequestSweep()
	case queue.EventReaggregate:
		if e.SubmissionID == 0 {
			return d.reaggregate(ctx, e.ParticipationID, e.TaskID) // a manual adjustment
		}
		return d.reaggregateSubmission(ctx, e.SubmissionID)
	case queue.EventDatasetChanged:
		d.datasets.invalidate()
		if e.TaskID != 0 {
			if err := d.reaggregateTask(ctx, e.TaskID); err != nil {
				return err
			}
		}
		d.RequestSweep()
	default:
		d.log.Warn("unknown dispatcher event", "kind", e.Kind)
	}
	return nil
}

// resultLoop reads result batches and processes them in parallel, keeping
// the results of one submission (or user test) in order on one goroutine.
func (d *Dispatcher) resultLoop(ctx context.Context) error {
	n := d.opts.Parallelism
	for ctx.Err() == nil {
		batch, err := d.q.ReadResults(ctx, d.opts.Consumer, 256, 2*time.Second)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			d.log.Error("read results", "error", err)
			sleepCtx(ctx, time.Second)
			continue
		}
		if len(batch) == 0 {
			continue
		}
		parts := make([][]queue.ResultDelivery, n)
		for _, r := range batch {
			k := 0
			if r.Result != nil {
				k = shard(r.Result.SubmissionID, r.Result.UserTestID, n)
			}
			parts[k] = append(parts[k], r)
		}
		var wg sync.WaitGroup
		var mu sync.Mutex
		var done []string
		for _, part := range parts {
			if len(part) == 0 {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				for _, r := range part {
					if r.Result == nil {
						d.log.Error("undecodable job result dropped", "id", r.ID)
					} else if err := d.processResult(ctx, r.Result); err != nil {
						if ctx.Err() != nil {
							return
						}
						// Leave it pending: it is redelivered to this consumer
						// on the next read (transient database errors).
						d.log.Error("process result", "submission", r.Result.SubmissionID,
							"user_test", r.Result.UserTestID, "error", err)
						continue
					}
					mu.Lock()
					done = append(done, r.ID)
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if err := d.q.AckResults(ctx, done...); err != nil && ctx.Err() == nil {
			d.log.Error("ack results", "error", err)
		}
		if len(done) < len(batch) {
			sleepCtx(ctx, 200*time.Millisecond)
		}
	}
	return nil
}

func shard(sub, ut int64, n int) int {
	h := fnv.New32a()
	var b [9]byte
	v := sub
	if sub == 0 {
		v, b[8] = ut, 1
	}
	for i := 0; i < 8; i++ {
		b[i] = byte(v >> (8 * i))
	}
	h.Write(b[:])
	return int(h.Sum32() % uint32(n))
}

func (d *Dispatcher) processResult(ctx context.Context, r *resultT) error {
	start := time.Now()
	var err error
	outcome := "ok"
	if r.UserTestID != 0 {
		err = d.handleUserTestResult(ctx, r)
	} else {
		err = d.handleSubmissionResult(ctx, r)
	}
	if err != nil {
		outcome = "error"
	} else if r.Error != "" {
		outcome = "job_error"
	}
	resultsProcessed.WithLabelValues(string(r.Kind), outcome).Inc()
	resultLatency.WithLabelValues(string(r.Kind)).Observe(time.Since(start).Seconds())
	return err
}

func (d *Dispatcher) sweepLoop(ctx context.Context) error {
	t := time.NewTicker(d.opts.SweepInterval)
	defer t.Stop()
	d.RequestSweep() // on becoming active: recover whatever was in flight
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		case <-d.sweepNow:
		}
		if err := d.Sweep(ctx); err != nil && ctx.Err() == nil {
			d.log.Error("sweep", "error", err)
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

var errStale = errors.New("stale")

func wrap(what string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", what, err)
}
