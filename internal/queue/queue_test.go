package queue

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
	"github.com/D4ND3R/Contest-Management-System/internal/testutil"
)

func newQueue(t *testing.T) *Queue {
	rdb, ns := testutil.Redis(t)
	q := New(rdb, ns)
	if err := q.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := q.Setup(context.Background()); err != nil { // idempotent
		t.Fatal(err)
	}
	return q
}

func TestPriorityOrder(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	// Enqueue lowest priority first.
	for _, p := range []Priority{PriorityBackground, PriorityUserTest, PriorityCompile, PriorityEvaluate} {
		if _, err := q.Enqueue(ctx, p, &jobs.Job{ID: p.String()}); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for i := 0; i < 4; i++ {
		d, err := q.Next(ctx, "w/0", 100*time.Millisecond, nil)
		if err != nil || d == nil {
			t.Fatalf("next: %v %v", d, err)
		}
		got = append(got, d.Job.ID)
		if err := q.Ack(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"evaluate", "compile", "usertest", "background"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
	if d, err := q.Next(ctx, "w/0", 50*time.Millisecond, nil); err != nil || d != nil {
		t.Fatalf("empty queue returned %v %v", d, err)
	}
}

func TestAllowedPriorities(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	q.Enqueue(ctx, PriorityCompile, &jobs.Job{ID: "c"})
	q.Enqueue(ctx, PriorityBackground, &jobs.Job{ID: "b"})
	d, _ := q.Next(ctx, "bg/0", 50*time.Millisecond, []Priority{PriorityBackground})
	if d == nil || d.Job.ID != "b" {
		t.Fatalf("background-only consumer got %+v", d)
	}
}

func TestBlockingNextWakesUp(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	go func() {
		time.Sleep(100 * time.Millisecond)
		q.Enqueue(ctx, PriorityUserTest, &jobs.Job{ID: "late"})
	}()
	start := time.Now()
	d, err := q.Next(ctx, "w/0", 5*time.Second, nil)
	if err != nil || d == nil || d.Job.ID != "late" {
		t.Fatalf("got %+v %v", d, err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("blocking read did not wake up promptly")
	}
}

func TestCompletePublishesAndAcks(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	q.Enqueue(ctx, PriorityEvaluate, &jobs.Job{ID: "j1", SubmissionID: 7, Generation: 2})
	d, _ := q.Next(ctx, "w/0", time.Second, nil)
	if err := q.Complete(ctx, d, &jobs.Result{JobID: "j1", SubmissionID: 7, Generation: 2}); err != nil {
		t.Fatal(err)
	}
	st, err := q.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Pending["evaluate"] != 0 || st.Waiting["evaluate"] != 0 || st.Results != 1 {
		t.Fatalf("stats = %+v", st)
	}
	rs, err := q.ReadResults(ctx, "d", 10, time.Second)
	if err != nil || len(rs) != 1 || rs[0].Result.SubmissionID != 7 || rs[0].Result.Generation != 2 {
		t.Fatalf("results = %+v %v", rs, err)
	}
	// Not acknowledged: a restarted dispatcher sees it again.
	rs2, _ := q.ReadResults(ctx, "d", 10, 10*time.Millisecond)
	if len(rs2) != 1 || rs2[0].ID != rs[0].ID {
		t.Fatalf("pending result not redelivered: %+v", rs2)
	}
	q.AckResults(ctx, rs[0].ID)
	rs3, _ := q.ReadResults(ctx, "d", 10, 10*time.Millisecond)
	if len(rs3) != 0 {
		t.Fatalf("acknowledged result redelivered: %+v", rs3)
	}
}

func TestRequeueFromDeadConsumer(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	q.Enqueue(ctx, PriorityCompile, &jobs.Job{ID: "j", Attempt: 0})
	d, _ := q.Next(ctx, "dead-worker/0", time.Second, nil)
	if d == nil {
		t.Fatal("no delivery")
	}
	ps, err := q.PendingJobs(ctx)
	if err != nil || len(ps) != 1 || ps[0].Consumer != "dead-worker/0" || WorkerOfConsumer(ps[0].Consumer) != "dead-worker" {
		t.Fatalf("pending = %+v %v", ps, err)
	}
	re, ex, err := q.Requeue(ctx, ps[0], 3)
	if err != nil || re == nil || ex != nil || re.Attempt != 1 {
		t.Fatalf("requeue = %+v %+v %v", re, ex, err)
	}
	if ps, _ := q.PendingJobs(ctx); len(ps) != 0 {
		t.Fatalf("still pending: %+v", ps)
	}
	d2, _ := q.Next(ctx, "other/0", time.Second, nil)
	if d2 == nil || d2.Job.ID != "j" || d2.Job.Attempt != 1 {
		t.Fatalf("requeued job = %+v", d2)
	}
	// Exhausted attempts: not re-added.
	ps, _ = q.PendingJobs(ctx)
	re, ex, err = q.Requeue(ctx, ps[0], 2)
	if err != nil || re != nil || ex == nil || ex.Attempt != 2 {
		t.Fatalf("exhausted requeue = %+v %+v %v", re, ex, err)
	}
	if d3, _ := q.Next(ctx, "other/0", 50*time.Millisecond, nil); d3 != nil {
		t.Fatalf("exhausted job was re-added: %+v", d3)
	}
	// Requeueing an acknowledged job is a no-op.
	re, ex, err = q.Requeue(ctx, Pending{Priority: PriorityCompile, ID: d2.ID}, 3)
	if err != nil || re != nil || ex != nil {
		t.Fatalf("requeue after ack = %+v %+v %v", re, ex, err)
	}
}

func TestEvents(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	q.Notify(ctx, Event{Kind: EventSubmission, SubmissionID: 42})
	evs, err := q.ReadEvents(ctx, "d", 10, time.Second)
	if err != nil || len(evs) != 1 || evs[0].Event.SubmissionID != 42 {
		t.Fatalf("events = %+v %v", evs, err)
	}
	q.AckEvents(ctx, evs[0].ID)
	if evs, _ := q.ReadEvents(ctx, "d", 10, 10*time.Millisecond); len(evs) != 0 {
		t.Fatal("event redelivered after ack")
	}
}

func TestHeartbeats(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	if err := q.Heartbeat(ctx, &WorkerStatus{Name: "w1", Slots: []SlotStatus{{Slot: 0, Core: 1}}}, time.Second); err != nil {
		t.Fatal(err)
	}
	q.Heartbeat(ctx, &WorkerStatus{Name: "w2"}, 100*time.Millisecond)
	time.Sleep(250 * time.Millisecond)
	ws, err := q.Workers(ctx, time.Hour)
	if err != nil || len(ws) != 2 {
		t.Fatalf("workers = %+v %v", ws, err)
	}
	if !ws[0].Alive || ws[1].Alive {
		t.Fatalf("liveness = %v %v", ws[0].Alive, ws[1].Alive)
	}
	if alive, _ := q.WorkerAlive(ctx, "w2"); alive {
		t.Fatal("expired worker reported alive")
	}
	q.Deregister(ctx, "w1")
	if ws, _ := q.Workers(ctx, time.Hour); len(ws) != 1 {
		t.Fatalf("after deregister: %+v", ws)
	}
}

func TestLeaseExclusive(t *testing.T) {
	q := newQueue(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var active, maxActive atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l := q.NewLease("dispatcher", 300*time.Millisecond)
			l.Run(ctx, func(ctx context.Context) error {
				n := active.Add(1)
				for {
					if m := maxActive.Load(); n > m {
						maxActive.CompareAndSwap(m, n)
					}
					select {
					case <-ctx.Done():
						active.Add(-1)
						return nil
					case <-time.After(20 * time.Millisecond):
					}
				}
			})
		}()
	}
	time.Sleep(time.Second)
	cancel()
	wg.Wait()
	if maxActive.Load() != 1 {
		t.Fatalf("%d lease holders were active at once", maxActive.Load())
	}
}

func TestLeaseFailover(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	a := q.NewLease("x", 200*time.Millisecond)
	if ok, _ := a.TryAcquire(ctx); !ok {
		t.Fatal("first acquire failed")
	}
	b := q.NewLease("x", 200*time.Millisecond)
	if ok, _ := b.TryAcquire(ctx); ok {
		t.Fatal("lease acquired twice")
	}
	// a crashes (stops renewing): b gets it after the TTL.
	time.Sleep(300 * time.Millisecond)
	if ok, _ := b.TryAcquire(ctx); !ok {
		t.Fatal("lease not taken over after expiry")
	}
	if ok, _ := a.Renew(ctx); ok {
		t.Fatal("stale holder renewed a lease it lost")
	}
}

func BenchmarkEnqueueNextComplete(b *testing.B) {
	rdb, ns := testutil.Redis(b)
	q := New(rdb, ns)
	ctx := context.Background()
	q.Setup(ctx)
	j := &jobs.Job{ID: "bench", Kind: jobs.KindEvaluate, Testcases: []jobs.Testcase{{ID: 1, Input: "x", Output: "y"}}}
	for b.Loop() {
		q.Enqueue(ctx, PriorityEvaluate, j)
		d, _ := q.Next(ctx, "w/0", time.Second, nil)
		q.Complete(ctx, d, &jobs.Result{JobID: d.Job.ID})
	}
}

// TestInFlightAndManualRequeue (SPEC_CLOSE D3): the system panel lists the
// jobs being run with what they are about and can queue one again.
func TestInFlightAndManualRequeue(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	q.Enqueue(ctx, PriorityEvaluate, &jobs.Job{ID: "e1", Kind: jobs.KindEvaluate, SubmissionID: 42, DatasetID: 7, Attempt: 2})
	d, err := q.Next(ctx, "w1/0", 100*time.Millisecond, nil)
	if err != nil || d == nil {
		t.Fatalf("next: %v", err)
	}
	fl, err := q.InFlightJobs(ctx)
	if err != nil || len(fl) != 1 {
		t.Fatalf("in flight %+v %v", fl, err)
	}
	if j := fl[0]; j.Kind != string(jobs.KindEvaluate) || j.SubmissionID != 42 || j.DatasetID != 7 || j.Attempt != 2 || j.Consumer != "w1/0" {
		t.Fatalf("in flight %+v", j)
	}
	ok, err := q.RequeueByID(ctx, fl[0].Priority, fl[0].ID)
	if err != nil || !ok {
		t.Fatalf("requeue %v %v", ok, err)
	}
	st, _ := q.Stats(ctx)
	if st.Waiting[PriorityEvaluate.String()] != 1 || st.Pending[PriorityEvaluate.String()] != 0 {
		t.Fatalf("stats %+v", st)
	}
	// Not in flight any more.
	if ok, err := q.RequeueByID(ctx, fl[0].Priority, fl[0].ID); ok || err != nil {
		t.Fatalf("second requeue %v %v", ok, err)
	}
	d, _ = q.Next(ctx, "w2/0", 100*time.Millisecond, nil)
	if d == nil || d.Job.ID != "e1" || d.Job.Attempt != 3 {
		t.Fatalf("requeued job %+v", d)
	}
}
