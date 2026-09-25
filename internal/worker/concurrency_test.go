package worker

import (
	"sync"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
)

// TestExecutablesUnderConcurrentSlots is the regression test of a race:
// while one slot wrote an executable into its box, another slot forked
// (to start isolate) and the child briefly held the file open for
// writing, so running it failed with ETXTBSY (exit code 127) and a
// correct program got a runtime error. TwoSteps copies the executable
// twice per testcase, which makes the window easy to hit.
func TestExecutablesUnderConcurrentSlots(t *testing.T) {
	h := newHarnessSlots(t, 3)
	if cap(h.free) < 2 {
		t.Skip("needs at least two judging cores")
	}
	b := batchJob{lang: "cpp17", files: map[string][]byte{"sol.cpp": fixture(t, "twosteps_ok.cpp")},
		managers: map[string][]byte{"manager.cpp": fixture(t, "twosteps_manager.cpp")}, limits: defaultLimits()}
	res := h.execute(h.job(jobs.KindCompile, "TwoSteps", b))
	if res.Error != "" || !res.Compilation.Success {
		t.Fatalf("compile: %v", res.Error)
	}
	var tcs []jobs.Testcase
	for i := 1; i <= 5; i++ {
		tcs = append(tcs, h.testcase(int64(i), "42\n", "42\n"))
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	bad := 0
	for g := 0; g < 2*cap(h.free); g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 15; n++ {
				ej := h.job(jobs.KindEvaluate, "TwoSteps", b)
				ej.Executables, ej.Testcases = res.Compilation.Executables, tcs
				er := h.execute(ej)
				mu.Lock()
				if er.Error != "" {
					t.Errorf("infrastructure error: %s", er.Error)
				}
				for _, ev := range er.Evaluations {
					if ev.Outcome != 1 {
						bad++
						if bad <= 3 {
							t.Errorf("testcase %s: %s (exit code %d) %s", ev.Codename, ev.ExitStatus, ev.ExitCode, ev.Text)
						}
					}
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if bad > 0 {
		t.Fatalf("%d wrong verdicts", bad)
	}
}
