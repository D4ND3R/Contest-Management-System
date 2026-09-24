package monitor

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
	"github.com/D4ND3R/Contest-Management-System/internal/logging"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/D4ND3R/Contest-Management-System/internal/testutil"
)

func TestRequeueDeadAndExhausted(t *testing.T) {
	rdb, ns := testutil.Redis(t)
	q := queue.New(rdb, ns)
	ctx := context.Background()
	q.Setup(ctx)
	m := New(q, logging.Discard(), Options{DeadGrace: 50 * time.Millisecond, MaxAttempts: 2, JobTimeout: time.Hour})

	// A live worker's job is left alone.
	q.Heartbeat(ctx, &queue.WorkerStatus{Name: "alive"}, time.Minute)
	q.Enqueue(ctx, queue.PriorityEvaluate, &jobs.Job{ID: "busy", SubmissionID: 1})
	if d, _ := q.Next(ctx, "alive/0", time.Second, nil); d == nil {
		t.Fatal("no job")
	}
	// A dead worker (no heartbeat) holds two jobs: one gets retried, the
	// other has used its last attempt.
	q.Enqueue(ctx, queue.PriorityCompile, &jobs.Job{ID: "retry", SubmissionID: 2, Attempt: 0})
	q.Enqueue(ctx, queue.PriorityCompile, &jobs.Job{ID: "last", SubmissionID: 3, Attempt: 1, Kind: jobs.KindCompile})
	q.Next(ctx, "ghost/0", time.Second, nil)
	q.Next(ctx, "ghost/1", time.Second, nil)
	time.Sleep(100 * time.Millisecond)
	n, err := m.Check(ctx)
	if err != nil || n != 2 {
		t.Fatalf("check reclaimed %d jobs, err %v", n, err)
	}
	d, _ := q.Next(ctx, "rescuer/0", time.Second, []queue.Priority{queue.PriorityCompile})
	if d == nil || d.Job.ID != "retry" || d.Job.Attempt != 1 {
		t.Fatalf("requeued job = %+v", d)
	}
	rs, _ := q.ReadResults(ctx, "disp", 10, time.Second)
	if len(rs) != 1 || rs[0].Result.SubmissionID != 3 || rs[0].Result.Error == "" {
		t.Fatalf("exhausted job result = %+v", rs)
	}
	ps, _ := q.PendingJobs(ctx)
	busy := 0
	for _, p := range ps {
		if p.Consumer == "alive/0" {
			busy++
		}
	}
	if busy != 1 {
		t.Fatalf("live worker's job was touched: %+v", ps)
	}
	raw, err := rdb.Get(ctx, StatsKey(q)).Result()
	if err != nil {
		t.Fatal(err)
	}
	var st Stats
	json.Unmarshal([]byte(raw), &st)
	if st.Queues == nil || len(st.Workers) != 1 || !st.Workers[0].Alive {
		t.Fatalf("stats = %+v", st)
	}
}

func TestStuckJobTimeout(t *testing.T) {
	rdb, ns := testutil.Redis(t)
	q := queue.New(rdb, ns)
	ctx := context.Background()
	q.Setup(ctx)
	q.Heartbeat(ctx, &queue.WorkerStatus{Name: "stuck"}, time.Minute)
	q.Enqueue(ctx, queue.PriorityEvaluate, &jobs.Job{ID: "j"})
	q.Next(ctx, "stuck/0", time.Second, nil)
	time.Sleep(150 * time.Millisecond)
	m := New(q, logging.Discard(), Options{JobTimeout: 100 * time.Millisecond, MaxAttempts: 3})
	if n, err := m.Check(ctx); err != nil || n != 1 {
		t.Fatalf("stuck job not reclaimed: %d %v", n, err)
	}
}
