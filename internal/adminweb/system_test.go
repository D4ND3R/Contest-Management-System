package adminweb

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/hoststat"
	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
)

// TestSystemPanel (SPEC_CLOSE D3): the panel shows this machine's and the
// workers' load, the storage, the jobs in flight (a job on a dead worker
// is flagged and can be queued again) and the system errors.
func TestSystemPanel(t *testing.T) {
	f := newFixture(t)
	q := queue.New(f.rdb, f.ns)
	if err := q.Setup(bg); err != nil {
		t.Fatal(err)
	}
	// A live worker reporting its machine, and a job taken by a worker
	// that died.
	var hs hoststat.Sampler
	host := hs.Sample([2]string{"work", t.TempDir()})
	q.Heartbeat(bg, &queue.WorkerStatus{Name: "w-live", Hostname: "judge1", Host: &host}, time.Minute)
	q.Enqueue(bg, queue.PriorityEvaluate, &jobs.Job{ID: "ev-1", Kind: jobs.KindEvaluate, SubmissionID: f.subs[1]})
	if d, err := q.Next(bg, "w-ghost/0", 100*time.Millisecond, nil); err != nil || d == nil {
		t.Fatalf("next: %v", err)
	}
	msg := "sandbox failure"
	f.pool.Exec(bg, "UPDATE submission_results SET system_error = $2 WHERE submission_id = $1", f.subs[1], msg)

	a := f.login("all")
	code, body := a.Get("/system")
	if code != 200 {
		t.Fatalf("system = %d\n%s", code, body)
	}
	for _, want := range []string{"This server", "CPU (", "blob store:", "database", "judge1", "free for work",
		"jobs that look stuck", "stuck?", "w-ghost", "/system/jobs/requeue", "sandbox failure"} {
		if !strings.Contains(body, want) {
			t.Errorf("system page lacks %q", want)
		}
	}
	if _, body := a.Get("/system/status"); !strings.Contains(body, "stuck?") {
		t.Error("the polled status lacks the stuck job")
	}
	fl, _ := q.InFlightJobs(bg)
	if len(fl) != 1 {
		t.Fatalf("in flight %+v", fl)
	}
	form := url.Values{"priority": {fl[0].Priority.String()}, "id": {fl[0].ID}}
	if code, _ := f.login("read_only").Post("/system/jobs/requeue", form); code != http.StatusForbidden {
		t.Fatalf("read-only requeue = %d", code)
	}
	if code, body := a.Post("/system/jobs/requeue", url.Values{"priority": {fl[0].Priority.String()}, "id": {fl[0].ID}}); code != 200 || !strings.Contains(body, "Job queued again") {
		t.Fatalf("requeue = %d\n%s", code, body)
	}
	if st, _ := q.Stats(bg); st.Waiting[queue.PriorityEvaluate.String()] != 1 || st.Pending[queue.PriorityEvaluate.String()] != 0 {
		t.Fatalf("stats after requeue %+v", st)
	}
	if code, body := a.Post("/system/jobs/requeue", url.Values{"priority": {fl[0].Priority.String()}, "id": {fl[0].ID}}); code != 200 || !strings.Contains(body, "already finished") {
		t.Fatalf("second requeue = %d\n%s", code, body)
	}
}
