package worker

import (
	"context"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
	"github.com/D4ND3R/Contest-Management-System/internal/scoring"
)

// TestPinnedToSlotCore checks that sandboxed programs run on the slot's core.
func TestPinnedToSlotCore(t *testing.T) {
	h := newHarness(t)
	src := []byte(`#define _GNU_SOURCE
#include <sched.h>
#include <stdio.h>
int main(void){ cpu_set_t s; sched_getaffinity(0,sizeof s,&s);
  for(int i=0;i<CPU_SETSIZE;i++) if(CPU_ISSET(i,&s)) printf("%d ",i); puts(""); return 0; }`)
	j := h.job(jobs.KindUserTest, "Batch", batchJob{lang: "c11", files: map[string][]byte{"p.c": src}, limits: defaultLimits()})
	j.Input = h.put(nil)
	r := h.exec.Execute(context.Background(), 0, j)
	if r.Error != "" || r.UserTest == nil {
		t.Fatalf("user test failed: %+v", r)
	}
	out, _ := blob.ReadAll(context.Background(), h.store, r.UserTest.Output)
	want := strconv.Itoa(h.exec.Slots[0].Core)
	if strings.TrimSpace(string(out)) != want {
		t.Fatalf("program affinity %q, want exactly core %s", out, want)
	}
}

// countingStore counts backend reads.
type countingStore struct {
	blob.Store
	opens atomic.Int64
}

func (c *countingStore) Open(ctx context.Context, d string) (io.ReadCloser, error) {
	c.opens.Add(1)
	return c.Store.Open(ctx, d)
}

// TestTestcaseCacheReuse checks that testcases and executables are fetched
// from the blob store once per worker, then served by the local cache.
func TestTestcaseCacheReuse(t *testing.T) {
	h := newHarness(t)
	counting := &countingStore{Store: h.store}
	h.exec.cache = mustCache(t, counting, t.TempDir())
	h.exec.store = h.exec.cache
	src := []byte("#include <stdio.h>\nint main(void){long a,b;scanf(\"%ld %ld\",&a,&b);printf(\"%ld\\n\",a+b);return 0;}")
	tcs := []jobs.Testcase{h.testcase(1, "1 2\n", "3\n"), h.testcase(2, "5 5\n", "10\n")}
	for round := 0; round < 3; round++ {
		_, evs, err := h.compileAndRun("Batch", batchJob{lang: "c11", files: map[string][]byte{"s.c": src}, limits: defaultLimits()}, tcs)
		if err != nil || len(evs) != 2 || evs[0].Outcome != 1 || evs[1].Outcome != 1 {
			t.Fatalf("round %d: %+v %v", round, evs, err)
		}
	}
	// Source (1) + 2 inputs + 2 outputs; executables are cached on upload.
	if n := counting.opens.Load(); n > 5 {
		t.Fatalf("backend read %d times over 3 rounds; the cache is not reused", n)
	}
}

func mustCache(t *testing.T, s blob.Store, dir string) *blob.Cache {
	c, err := blob.NewCache(s, dir, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestSkippedTestcases (SPEC_IOI H4): a testcase the dispatcher marked as
// skipped (short-circuit) is not run; the others are.
func TestSkippedTestcases(t *testing.T) {
	h := newHarness(t)
	h.exec.Skip = func(_ context.Context, _ *jobs.Job, tc jobs.Testcase) bool { return tc.ID == 2 }
	src := []byte("#include <stdio.h>\nint main(void){long a,b;scanf(\"%ld %ld\",&a,&b);printf(\"%ld\\n\",a+b);return 0;}")
	tcs := []jobs.Testcase{h.testcase(1, "1 2\n", "3\n"), h.testcase(2, "5 5\n", "10\n"), h.testcase(3, "2 2\n", "4\n")}
	_, evs, err := h.compileAndRun("Batch", batchJob{lang: "c11", files: map[string][]byte{"s.c": src}, limits: defaultLimits()}, tcs)
	if err != nil || len(evs) != 3 {
		t.Fatalf("%+v %v", evs, err)
	}
	if evs[0].Outcome != 1 || evs[2].Outcome != 1 || evs[1].ExitStatus != scoring.StatusSkipped || evs[1].Outcome != 0 ||
		evs[1].Text != scoring.MsgSkipped || evs[1].TestcaseID != 2 || evs[1].Time != 0 {
		t.Fatalf("evaluations %+v", evs)
	}
}
