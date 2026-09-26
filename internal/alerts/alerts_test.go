package alerts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
	"github.com/D4ND3R/Contest-Management-System/internal/logging"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/D4ND3R/Contest-Management-System/internal/testutil"
)

var ctx = context.Background()

// TestManager: a rule is announced once when it has failed for its For,
// again when it recovers, through the notification and the webhook; a
// rule that cannot be evaluated keeps its state.
func TestManager(t *testing.T) {
	rdb, ns := testutil.Redis(t)
	var mu sync.Mutex
	var hooks, notes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]string
		json.Unmarshal(b, &m)
		mu.Lock()
		hooks = append(hooks, m["text"]+"|"+m["content"])
		mu.Unlock()
	}))
	defer srv.Close()
	firing, broken := false, false
	rule := Rule{Name: "r", For: time.Minute, Check: func(context.Context) (bool, string, error) {
		if broken {
			return false, "", io.ErrUnexpectedEOF
		}
		return firing, "the thing broke", nil
	}}
	m := New(rdb, ns+"alerts", []Rule{rule}, func(_ context.Context, s string) { notes = append(notes, s) }, srv.URL, logging.Discard())
	now := time.Now()
	m.now = func() time.Time { return now }
	step := func(d time.Duration) {
		t.Helper()
		now = now.Add(d)
		if err := m.Evaluate(ctx); err != nil && !broken {
			t.Fatal(err)
		}
	}
	step(0)
	firing = true
	step(time.Second) // failing, not yet for a minute
	if a, _ := Active(ctx, rdb, ns+"alerts"); len(a) != 1 || a[0].Fired || len(notes) != 0 {
		t.Fatalf("pending: %+v %v", a, notes)
	}
	broken = true
	step(2 * time.Minute) // cannot evaluate: nothing changes
	broken = false
	step(0)
	step(10 * time.Second) // still failing: announced once
	if len(notes) != 1 || notes[0] != "Alert: the thing broke" {
		t.Fatalf("notes %v", notes)
	}
	firing = false
	step(time.Second)
	if a, _ := Active(ctx, rdb, ns+"alerts"); len(a) != 0 || len(notes) != 2 || notes[1] != "Resolved: the thing broke" {
		t.Fatalf("resolved: %+v %v", a, notes)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(hooks) != 2 || hooks[0] != "Alert: the thing broke|Alert: the thing broke" {
		t.Fatalf("webhook %v", hooks)
	}
	// A rule that recovers before its For is never announced.
	firing = true
	step(0)
	firing = false
	step(time.Second)
	if len(notes) != 2 {
		t.Fatalf("flapping rule announced: %v", notes)
	}
}

// TestStandardRules evaluates the built-in rules against real state.
func TestStandardRules(t *testing.T) {
	rdb, ns := testutil.Redis(t)
	pool := testutil.DB(t)
	q := queue.New(rdb, ns)
	q.Setup(ctx)
	rules := map[string]Rule{}
	for _, r := range Standard(Sources{Queue: q, Pool: pool}, config.Alerts{QueueDepth: 2, JudgingLatency: config.Duration(time.Minute), DiskFreePercent: 1}) {
		rules[r.Name] = r
	}
	check := func(name string, want bool) string {
		t.Helper()
		got, detail, err := rules[name].Check(ctx)
		if err != nil || got != want {
			t.Fatalf("%s = %v (%s, %v), want %v", name, got, detail, err, want)
		}
		return detail
	}
	for _, name := range []string{"queue_backlog", "no_workers", "judging_latency", "wal_archiving", "timing_drift", "disk_space"} {
		check(name, false)
	}
	for i := range 3 {
		q.Enqueue(ctx, queue.PriorityEvaluate, &jobs.Job{ID: string(rune('a' + i))})
	}
	if d := check("queue_backlog", true); !strings.Contains(d, "3 jobs are waiting") {
		t.Fatal(d)
	}
	now := time.Now()
	sqlc.New(pool).CreateContest(ctx, db.NewContestParams("live", now.Add(-time.Hour), now.Add(time.Hour)))
	check("no_workers", true)
	q.Heartbeat(ctx, &queue.WorkerStatus{Name: "w"}, time.Minute)
	check("no_workers", false)
	q.SaveCalibration(ctx, queue.Calibration{Worker: "w", Median: 1, Off: 2})
	if d := check("timing_drift", true); !strings.Contains(d, "w (2 cores)") {
		t.Fatal(d)
	}
}
