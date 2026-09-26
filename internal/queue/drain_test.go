package queue

import (
	"context"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
)

func TestDrain(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	for _, p := range []Priority{PriorityEvaluate, PriorityCompile} {
		if _, err := q.Enqueue(ctx, p, &jobs.Job{ID: p.String()}); err != nil {
			t.Fatal(err)
		}
	}
	d, err := q.Next(ctx, "w/0", 100*time.Millisecond, nil) // one delivered, not acknowledged
	if err != nil || d == nil {
		t.Fatal(d, err)
	}
	if err := q.PublishResult(ctx, &jobs.Result{JobID: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := q.Notify(ctx, Event{Kind: EventSubmission, SubmissionID: 7}); err != nil {
		t.Fatal(err)
	}
	if err := q.AddSkips(ctx, Skip{SubmissionID: 7, DatasetID: 1, Generation: 1, Testcases: []int64{3}}); err != nil {
		t.Fatal(err)
	}
	if err := q.Supersede(ctx, 7); err != nil {
		t.Fatal(err)
	}
	other := New(q.Redis(), q.Key("other")+":") // another installation on the same server
	if err := other.Supersede(ctx, 7); err != nil {
		t.Fatal(err)
	}

	if _, err := q.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	st, err := q.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for name, n := range st.Waiting {
		if n != 0 || st.Pending[name] != 0 {
			t.Fatalf("%s: %d waiting, %d pending", name, n, st.Pending[name])
		}
	}
	if st.Results != 0 || st.Events != 0 {
		t.Fatalf("stats %+v", st)
	}
	if q.Skipped(ctx, 7, 1, 1, 3) || q.Superseded(ctx, 7) {
		t.Fatal("hints survived")
	}
	if !other.Superseded(ctx, 7) {
		t.Fatal("drained another namespace")
	}
	// The streams work again.
	if _, err := q.Enqueue(ctx, PriorityEvaluate, &jobs.Job{ID: "after"}); err != nil {
		t.Fatal(err)
	}
	if d, err := q.Next(ctx, "w/0", 100*time.Millisecond, nil); err != nil || d == nil || d.Job.ID != "after" {
		t.Fatalf("after drain: %v %v", d, err)
	}
}
