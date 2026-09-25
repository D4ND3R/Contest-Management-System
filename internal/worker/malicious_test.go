package worker

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/selftest"
)

// judge runs the self-test cases on the harness executor. The uid range
// covers every box of this package (400-599, isolate uid = 60000 + box).
func (h *harness) judge(slots int) *selftest.Judge {
	return &selftest.Judge{Exec: h.exec, Store: h.store, Langs: h.langs, Slots: slots, UIDs: [2]int{60000 + 400, 60000 + 599}}
}

// report fails the test for every result that is not OK and logs a table.
func report(t *testing.T, results []selftest.Result) {
	t.Helper()
	for _, r := range results {
		if !r.OK() {
			t.Errorf("%s %s: got %s (%s), want one of %v; %s", r.Group, r.Name, r.Got, r.Detail, r.Want, strings.Join(r.Problems, "; "))
		}
		t.Logf("%-24s %-14s %s (%.2fs)", r.Name, r.Got, r.Detail, r.Took.Seconds())
	}
}

// compareRuns fails on verdicts that changed between runs and writes the
// summary table to the file named by env (when set).
func compareRuns(t *testing.T, first, second []selftest.Result, env string) {
	t.Helper()
	for _, d := range selftest.Compare(first, second) {
		t.Errorf("verdict changed between runs: %s", d)
	}
	got := map[string]string{}
	for _, r := range second {
		got[r.Name] = r.Got
	}
	var b strings.Builder
	for _, r := range first {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", r.Name, r.Got, got[r.Name])
	}
	t.Logf("summary (name | run 1 | run 2):\n%s", b.String())
	if out := os.Getenv(env); out != "" {
		os.WriteFile(out, []byte(b.String()), 0o644)
	}
}

// TestMaliciousBattery runs the security battery twice and requires
// identical verdicts on both runs (SPEC §9: F2 exit criterion).
func TestMaliciousBattery(t *testing.T) {
	h := newHarness(t)
	j := h.judge(1)
	first, err := j.RunBattery(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	report(t, first)
	second, err := j.RunBattery(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	report(t, second)
	compareRuns(t, first, second, "CMS_BATTERY_REPORT")
}
