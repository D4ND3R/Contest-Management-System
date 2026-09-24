package e2e

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
)

// TestSubmissionFlow is the F5 functional exit criterion: a contestant logs
// in, submits through the web form, receives live updates over SSE and sees
// the final score, while the ranking aggregate is updated.
func TestSubmissionFlow(t *testing.T) {
	s := newStack(t, stackOpts{workers: true})
	p := s.addContestant("ana")
	b := s.login("ana")

	// Listen to the event stream like the browser does.
	sse := &http.Client{Jar: b.c.Jar}
	ctx, cancel := context.WithTimeout(bg, 90*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", s.cwsURL+"/e2e/events", nil)
	resp, err := sse.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("events: %v", err)
	}
	defer resp.Body.Close()
	statuses := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if line := sc.Text(); strings.HasPrefix(line, "data: ") {
				statuses <- line
			}
		}
		close(statuses)
	}()
	time.Sleep(100 * time.Millisecond)

	src := "#include <stdio.h>\nint main(void){long a,b;scanf(\"%ld %ld\",&a,&b);printf(\"%ld\\n\",a+b);return 0;}\n"
	start := time.Now()
	if code := b.submit("c11", "sum.c", src); code != 200 {
		t.Fatalf("submit = %d", code)
	}
	seen := map[string]bool{}
	for !seen["scored"] {
		select {
		case line, ok := <-statuses:
			if !ok {
				t.Fatal("event stream closed")
			}
			for _, st := range []string{"compiling", "evaluating", "scored"} {
				if strings.Contains(line, `"status":"`+st+`"`) {
					seen[st] = true
				}
			}
		case <-ctx.Done():
			t.Fatalf("no scored event (seen %v)", seen)
		}
	}
	t.Logf("submission judged and notified in %v (events: %v)", time.Since(start).Round(time.Millisecond), seen)
	_, body := b.get("/e2e/tasks/sum")
	// Public score: only testcase 0 is public (25 points of 100).
	if !strings.Contains(body, "25 / 100") || !strings.Contains(body, "Evaluated") {
		t.Fatalf("task page after judging:\n%s", body)
	}
	subs, _ := s.q.ListSubmissionsByParticipationTask(bg, sqlc.ListSubmissionsByParticipationTaskParams{ParticipationID: p.ID, TaskID: s.task.ID})
	_, body = b.get(fmt.Sprintf("/e2e/submissions/%d", subs[0].ID))
	if !strings.Contains(body, "Output is correct") || !strings.Contains(body, "Compilation succeeded") {
		t.Fatalf("details:\n%s", body)
	}
	ts, err := s.q.GetParticipationTaskScore(bg, sqlc.GetParticipationTaskScoreParams{ParticipationID: p.ID, TaskID: s.task.ID})
	if err != nil || ts.Score != 100 {
		t.Fatalf("ranking aggregate %+v %v", ts, err)
	}
}

// TestLightLoadLatency is the F5 performance exit criterion: under light
// load (50 concurrent contestants browsing and polling) the server answers
// with p95 < 15 ms.
func TestLightLoadLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("load test")
	}
	s := newStack(t, stackOpts{})
	const users = 50
	var browsers []*browser
	for i := 0; i < users; i++ {
		s.addContestant(fmt.Sprintf("u%02d", i))
	}
	for i := 0; i < users; i++ {
		b := s.login(fmt.Sprintf("u%02d", i))
		b.submit("c11", "sum.c", "int main(void){return 0;}") // something to list
		browsers = append(browsers, b)
	}
	paths := []string{"/e2e/", "/e2e/tasks/sum", "/e2e/tasks/sum/submissions", "/e2e/documentation"}
	var mu sync.Mutex
	var lat []time.Duration
	deadline := time.Now().Add(5 * time.Second)
	var wg sync.WaitGroup
	for i, b := range browsers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var mine []time.Duration
			for k := i; time.Now().Before(deadline); k++ {
				start := time.Now()
				code, _ := b.get(paths[k%len(paths)])
				d := time.Since(start)
				if code != 200 {
					t.Errorf("GET %s = %d", paths[k%len(paths)], code)
					return
				}
				mine = append(mine, d)
				time.Sleep(20 * time.Millisecond) // think time
			}
			mu.Lock()
			lat = append(lat, mine...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	q := func(p float64) time.Duration { return lat[int(p*float64(len(lat)-1))] }
	t.Logf("%d requests: p50=%v p95=%v p99=%v max=%v", len(lat), q(0.5), q(0.95), q(0.99), lat[len(lat)-1])
	// Latency is only meaningful on a quiet machine: `go test ./...` runs
	// packages in parallel (the worker tests compile in every language at
	// the same time). `make test-e2e` runs this package alone and enforces
	// the target; elsewhere the numbers are only reported.
	if os.Getenv("CMS_PERF_ASSERT") == "" {
		return
	}
	limit := 15 * time.Millisecond
	if raceEnabled {
		limit *= 5
	}
	if q(0.95) >= limit {
		t.Fatalf("p95 = %v, want < %v", q(0.95), limit)
	}
}
