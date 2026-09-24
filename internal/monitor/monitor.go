// Package monitor watches workers and jobs: it detects dead workers (their
// heartbeat expired) and stuck jobs (pending longer than the job timeout),
// hands their jobs back to the queue, reports jobs that exhausted their
// attempts, and publishes queue/worker statistics.
package monitor

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
	"github.com/D4ND3R/Contest-Management-System/internal/metrics"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	queueGauge = metrics.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cms_queue_jobs", Help: "Jobs per queue and state (waiting, pending).",
	}, []string{"queue", "state"})
	workersGauge = metrics.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cms_workers", Help: "Known workers by liveness.",
	}, []string{"state"})
	requeued = metrics.NewCounterVec(prometheus.CounterOpts{
		Name: "cms_monitor_requeued_jobs_total", Help: "Jobs taken back from workers, by reason.",
	}, []string{"reason"})
)

// Options tune the monitor.
type Options struct {
	Namespace     string
	CheckInterval time.Duration
	// JobTimeout: a job pending longer than this is re-run elsewhere even if
	// its worker still heartbeats.
	JobTimeout time.Duration
	// DeadGrace: how long a job of a dead worker may stay pending before it
	// is reclaimed (covers the heartbeat of a worker that just started).
	DeadGrace   time.Duration
	MaxAttempts int
}

// Stats is the snapshot stored in Redis for the admin UI.
type Stats struct {
	Time    time.Time            `json:"time"`
	Queues  *queue.Stats         `json:"queues"`
	Workers []queue.WorkerStatus `json:"workers"`
}

// Monitor is the checker service.
type Monitor struct {
	q    *queue.Queue
	log  *slog.Logger
	opts Options
}

// New creates a monitor.
func New(q *queue.Queue, log *slog.Logger, opts Options) *Monitor {
	if opts.CheckInterval <= 0 {
		opts.CheckInterval = 2 * time.Second
	}
	if opts.JobTimeout <= 0 {
		opts.JobTimeout = 10 * time.Minute
	}
	if opts.DeadGrace <= 0 {
		opts.DeadGrace = time.Second
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 3
	}
	return &Monitor{q: q, log: log, opts: opts}
}

// StatsKey is where the latest statistics are published.
func StatsKey(q *queue.Queue) string { return q.Key("stats") }

// Run checks periodically until ctx ends; one monitor is active at a time.
func (m *Monitor) Run(ctx context.Context) error {
	if err := m.q.Setup(ctx); err != nil {
		return err
	}
	lease := m.q.NewLease("monitor", 10*time.Second)
	return lease.Run(ctx, func(ctx context.Context) error {
		m.log.Info("monitor active")
		t := time.NewTicker(m.opts.CheckInterval)
		defer t.Stop()
		for {
			if _, err := m.Check(ctx); err != nil && ctx.Err() == nil {
				m.log.Error("check", "error", err)
			}
			select {
			case <-ctx.Done():
				return nil
			case <-t.C:
			}
		}
	})
}

// Check performs one pass and returns how many jobs were taken back.
func (m *Monitor) Check(ctx context.Context) (int, error) {
	pending, err := m.q.PendingJobs(ctx)
	if err != nil {
		return 0, err
	}
	alive := map[string]bool{}
	n := 0
	for _, pe := range pending {
		worker := queue.WorkerOfConsumer(pe.Consumer)
		a, ok := alive[worker]
		if !ok {
			a, err = m.q.WorkerAlive(ctx, worker)
			if err != nil {
				return n, err
			}
			alive[worker] = a
		}
		var reason string
		switch {
		case pe.Consumer == "monitor" && pe.Idle > m.opts.DeadGrace:
			reason = "orphaned" // a previous monitor died mid-requeue
		case !a && pe.Idle > m.opts.DeadGrace:
			reason = "dead_worker"
		case pe.Idle > m.opts.JobTimeout:
			reason = "timeout"
		default:
			continue
		}
		re, ex, err := m.q.Requeue(ctx, pe, m.opts.MaxAttempts)
		if err != nil {
			return n, err
		}
		if re == nil && ex == nil {
			continue
		}
		n++
		requeued.WithLabelValues(reason).Inc()
		if re != nil {
			m.log.Warn("job requeued", "reason", reason, "worker", worker, "job", re.ID, "attempt", re.Attempt)
		}
		if ex != nil {
			m.log.Error("job exhausted its attempts", "reason", reason, "worker", worker, "job", ex.ID)
			res := jobs.ForJob(ex, "monitor")
			res.Error = "the job was lost " + itoa(ex.Attempt) + " times (" + reason + ")"
			if err := m.q.PublishResult(ctx, res); err != nil {
				return n, err
			}
		}
	}
	return n, m.publishStats(ctx)
}

func itoa(v int) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func (m *Monitor) publishStats(ctx context.Context) error {
	st, err := m.q.Stats(ctx)
	if err != nil {
		return err
	}
	ws, err := m.q.Workers(ctx, time.Hour)
	if err != nil {
		return err
	}
	var up, down float64
	for _, w := range ws {
		if w.Alive {
			up++
		} else {
			down++
		}
	}
	workersGauge.WithLabelValues("alive").Set(up)
	workersGauge.WithLabelValues("dead").Set(down)
	for q, v := range st.Waiting {
		queueGauge.WithLabelValues(q, "waiting").Set(float64(v))
	}
	for q, v := range st.Pending {
		queueGauge.WithLabelValues(q, "pending").Set(float64(v))
	}
	data, err := json.Marshal(Stats{Time: time.Now().UTC(), Queues: st, Workers: ws})
	if err != nil {
		return err
	}
	return m.q.Redis().Set(ctx, StatsKey(m.q), data, time.Minute).Err()
}
