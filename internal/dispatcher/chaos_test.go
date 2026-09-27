package dispatcher_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/dispatcher"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/logging"
	"github.com/D4ND3R/Contest-Management-System/internal/monitor"
	"github.com/D4ND3R/Contest-Management-System/internal/worker"
)

type proc struct {
	stop context.CancelFunc
	done chan struct{}
}

func (p proc) kill() { p.stop(); <-p.done }

// TestChaos (SPEC_IOI §12): while a batch of submissions is judged, the
// dispatcher and the workers are stopped and replaced at random moments
// (a stopped dispatcher dies mid-transaction; a stopped worker finishes
// its job, as on a shutdown — the SIGKILL case is
// TestKillWorkerMidEvaluation). Every submission still ends with the
// score it deserves, with each testcase evaluated once and no system
// error.
func TestChaos(t *testing.T) {
	if testing.Short() {
		t.Skip("chaos run")
	}
	e := newEnv(t, true)
	e.cancel() // the env's own dispatcher, monitor and worker
	e.wg.Wait()
	seed := uint64(time.Now().UnixNano())
	t.Logf("chaos seed %d", seed)
	rnd := rand.New(rand.NewPCG(seed, seed))
	reg, err := langs.Load(filepath.Join("..", "..", "config", "languages"))
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	mon := monitor.New(e.q, logging.Discard(), monitor.Options{CheckInterval: 200 * time.Millisecond, DeadGrace: 500 * time.Millisecond})
	e.goRun(func() { mon.Run(runCtx) })

	n := 0
	startDispatcher := func() proc {
		n++
		c, stop := context.WithCancel(runCtx)
		d := dispatcher.New(e.pool, e.rdb, reg, logging.Discard(), dispatcher.Options{Namespace: e.ns,
			Consumer: fmt.Sprintf("chaos-d%d", n), SweepInterval: 500 * time.Millisecond})
		done := make(chan struct{})
		go func() { d.Run(c); close(done) }()
		return proc{stop, done}
	}
	startWorker := func(slot int) proc {
		n++
		c, stop := context.WithCancel(runCtx)
		svc, err := worker.NewService(e.workerConfig(fmt.Sprintf("chaos-w%d", n), e.boxBase+20*slot), e.store, e.q, logging.Discard())
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() { svc.Run(c); close(done) }()
		return proc{stop, done}
	}
	disp := startDispatcher()
	workers := []proc{startWorker(0), startWorker(1)}

	var ids []int64
	want := map[int64]float64{}
	for i := range 10 {
		src, score := srcAC, 100.0
		if i%2 == 1 {
			src, score = srcWA, 0
		}
		id := e.submit(src, true)
		ids = append(ids, id)
		want[id] = score
		time.Sleep(time.Duration(rnd.IntN(150)) * time.Millisecond)
	}
	for end := time.Now().Add(6 * time.Second); time.Now().Before(end); {
		time.Sleep(time.Duration(300+rnd.IntN(500)) * time.Millisecond)
		if rnd.IntN(2) == 0 {
			disp.kill()
			disp = startDispatcher()
		} else {
			w := rnd.IntN(len(workers))
			workers[w].kill()
			workers[w] = startWorker(w)
		}
	}
	q := sqlc.New(e.pool)
	for _, id := range ids {
		r := e.waitScored(id, e.dataset.ID, 90*time.Second)
		if r.SystemError != nil || r.Score == nil || *r.Score != want[id] || r.TestcasesDone != 4 {
			e.logResult(r)
			t.Fatalf("submission %d: score %v (want %v), done %d, error %v (seed %d)", id, derefF(r.Score), want[id], r.TestcasesDone, deref(r.SystemError), seed)
		}
		evs, _ := q.ListEvaluations(ctx, sqlc.ListEvaluationsParams{SubmissionID: id, DatasetID: e.dataset.ID})
		if len(evs) != 4 {
			t.Fatalf("submission %d has %d evaluations", id, len(evs))
		}
	}
	if s := e.taskScore(); s.Score != 100 || s.Pending != 0 {
		t.Fatalf("task score %+v", s)
	}
	disp.kill()
	for _, w := range workers {
		w.kill()
	}
}
