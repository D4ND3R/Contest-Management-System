package worker

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/app"
	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/hoststat"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/D4ND3R/Contest-Management-System/internal/version"
)

// Service consumes jobs from the queues, one loop per slot, and publishes
// heartbeats. Each result is published and its job acknowledged atomically;
// if the process dies mid-job the job stays pending and the monitor hands
// it to another worker.
type Service struct {
	exec     *Executor
	q        *queue.Queue
	log      *slog.Logger
	prios    []queue.Priority
	interval time.Duration
	started  time.Time

	mu       sync.Mutex
	slots    []queue.SlotStatus
	jobsDone atomic.Int64
	errors   atomic.Int64

	// The machine's load, reported with every heartbeat.
	host hoststat.Sampler
	dirs [][2]string
}

// NewService builds the worker service.
func NewService(cfg config.Worker, store blob.Store, q *queue.Queue, log *slog.Logger) (*Service, error) {
	exec, err := NewExecutor(cfg, store, log)
	if err != nil {
		return nil, err
	}
	s := &Service{exec: exec, q: q, log: log, interval: cfg.HeartbeatInterval.D(), started: time.Now().UTC(),
		dirs: [][2]string{{"work", cfg.WorkDir}, {"cache", cfg.CacheDir}}}
	if s.interval <= 0 {
		s.interval = 2 * time.Second
	}
	for _, name := range cfg.Queues {
		if p, ok := queue.ParsePriority(name); ok {
			s.prios = append(s.prios, p)
		}
	}
	for i, sl := range exec.Slots {
		s.slots = append(s.slots, queue.SlotStatus{Slot: i, Core: sl.Core})
	}
	return s, nil
}

// Name returns the worker name used for heartbeats and consumers.
func (s *Service) Name() string { return s.exec.Name }

// Run serves until ctx ends. On cancellation every slot finishes its
// current job (so a normal shutdown loses nothing and duplicates nothing),
// then the worker deregisters.
func (s *Service) Run(ctx context.Context) error {
	if err := s.q.Setup(ctx); err != nil {
		return err
	}
	defer s.exec.Close()
	s.log.Info("worker ready", "name", s.exec.Name, "slots", len(s.exec.Slots), "cores", coresOf(s.exec))
	hbCtx, stopHB := context.WithCancel(context.WithoutCancel(ctx))
	defer stopHB()
	go s.heartbeat(hbCtx)

	g, gctx := app.NewGroup(ctx)
	for i := range s.exec.Slots {
		g.Go(func(context.Context) error { return s.loop(gctx, i) })
	}
	err := g.Wait()
	stopHB()
	_ = s.q.Deregister(context.WithoutCancel(ctx), s.exec.Name)
	return err
}

func coresOf(e *Executor) []int {
	var out []int
	for _, s := range e.Slots {
		out = append(out, s.Core)
	}
	return out
}

func (s *Service) consumer(slot int) string { return s.exec.Name + "/" + strconv.Itoa(slot) }

func (s *Service) loop(ctx context.Context, slot int) error {
	for ctx.Err() == nil {
		d, err := s.q.Next(ctx, s.consumer(slot), 2*time.Second, s.prios)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			s.log.Error("read job", "slot", slot, "error", err)
			time.Sleep(time.Second)
			continue
		}
		if d == nil {
			continue
		}
		if d.Job == nil {
			s.log.Error("undecodable job dropped", "id", d.ID)
			_ = s.q.Ack(context.WithoutCancel(ctx), d)
			continue
		}
		s.setSlot(slot, d)
		// Jobs run to completion even during shutdown.
		res := s.exec.Execute(context.WithoutCancel(ctx), slot, d.Job)
		if res.Error != "" {
			s.errors.Add(1)
		}
		if err := s.q.Complete(context.WithoutCancel(ctx), d, res); err != nil {
			// The job stays pending; the monitor will re-run it.
			s.log.Error("publish result", "job", d.Job.ID, "error", err)
		}
		s.jobsDone.Add(1)
		s.setSlot(slot, nil)
	}
	return nil
}

func (s *Service) setSlot(i int, d *queue.Delivery) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := queue.SlotStatus{Slot: i, Core: s.slots[i].Core}
	if d != nil && d.Job != nil {
		st.JobID, st.Kind, st.SubmissionID, st.UserTestID, st.Since = d.Job.ID, string(d.Job.Kind), d.Job.SubmissionID, d.Job.UserTestID, time.Now().UTC()
	}
	s.slots[i] = st
}

func (s *Service) heartbeat(ctx context.Context) {
	host, _ := os.Hostname()
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		hs := s.host.Sample(s.dirs...)
		s.mu.Lock()
		st := &queue.WorkerStatus{Name: s.exec.Name, Hostname: host, Version: version.String(), StartedAt: s.started,
			Slots: append([]queue.SlotStatus(nil), s.slots...), JobsDone: s.jobsDone.Load(), Errors: s.errors.Load(), Host: &hs,
			Seccomp: s.exec.Seccomp()}
		s.mu.Unlock()
		if err := s.q.Heartbeat(ctx, st, 4*s.interval); err != nil && ctx.Err() == nil {
			s.log.Warn("heartbeat", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
