package worker

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
)

// maliciousCase is one program of the security battery with the verdict
// it must receive and extra assertions on the host state.
type maliciousCase struct {
	name   string
	file   string
	input  string
	limits jobs.Limits
	// want is the expected exit status ("compile_error" for CE); several
	// values are allowed when the kernel may legitimately pick either.
	want []string
	// outcome is the expected score fraction (0 unless noted).
	outcome float64
	// check runs extra assertions; output is the contestant stdout.
	check func(t *testing.T, output string)
}

func defaultLimits() jobs.Limits {
	return jobs.Limits{TimeMs: 1000, MemoryBytes: 256 << 20, OutputBytes: 16 << 20, Processes: 1}
}

func maliciousCases(t *testing.T, listenerPort int, connections *atomic.Int64) []maliciousCase {
	lim := defaultLimits()
	wide := lim
	wide.Processes = 64
	short := lim
	short.TimeMs = 500
	return []maliciousCase{
		{name: "control_ac", file: "sum_ok.c", input: "2 3\n", limits: lim, want: []string{"ok"}, outcome: 1},
		{name: "control_wa", file: "sum_wrong.c", input: "2 3\n", limits: lim, want: []string{"ok"}},
		{name: "control_ce", file: "syntax_error.c", want: []string{"compile_error"}},
		{name: "fork_bomb", file: "fork_bomb.c", input: "", limits: lim, want: tle,
			check: func(t *testing.T, _ string) { assertNoSandboxProcesses(t) }},
		{name: "fork_bomb_64_procs", file: "fork_bomb_wide.c", input: "", limits: wide, want: tle,
			check: func(t *testing.T, _ string) { assertNoSandboxProcesses(t) }},
		{name: "read_passwd", file: "read_passwd.c", input: "", limits: lim, want: []string{"ok"},
			check: func(t *testing.T, out string) {
				// /etc/alternatives is mounted on purpose (compiler symlinks),
				// so /etc lists exactly that entry.
				out = strings.ReplaceAll(out, "LEAK /etc/alternatives\n", "")
				if strings.Contains(out, "LEAK") {
					t.Errorf("host data readable from the sandbox:\n%s", out)
				}
				if !strings.Contains(out, "done") {
					t.Errorf("program did not complete: %q", out)
				}
			}},
		{name: "network", file: "network.c", input: strconv.Itoa(listenerPort), limits: lim, want: []string{"ok"},
			check: func(t *testing.T, out string) {
				if strings.Contains(out, "CONNECTED") || strings.Contains(out, "UDP-SENT") {
					t.Errorf("network reachable from the sandbox:\n%s", out)
				}
				if n := connections.Load(); n != 0 {
					t.Errorf("host listener received %d connections", n)
				}
			}},
		{name: "write_outside_box", file: "write_outside.c", input: "", limits: lim, want: []string{"ok"},
			check: func(t *testing.T, out string) {
				if strings.Contains(out, "ESCAPED") {
					t.Errorf("wrote outside the box:\n%s", out)
				}
				for _, p := range []string{"/cms_pwned", "/usr/cms_pwned", "/usr/bin/cms_pwned", "/etc/cms_pwned",
					"/dev/shm/cms_pwned", "/tmp/cms_pwned_tmp", "/var/local/lib/isolate/cms_pwned"} {
					if _, err := os.Stat(p); err == nil {
						os.Remove(p)
						t.Errorf("host file %s was created", p)
					}
				}
			}},
		{name: "sleep_forever", file: "sleep_forever.c", input: "", limits: short, want: []string{"timeout_wall"}},
		{name: "memory_hog", file: "memory_hog.c", input: "", limits: lim, want: []string{"memory"}},
		{name: "huge_output", file: "huge_output.c", input: "", limits: lim, want: []string{"output_limit"}},
		// stderr is discarded (/dev/null): unlimited but harmless; the
		// program then prints the right answer.
		{name: "huge_stderr", file: "huge_stderr.c", input: "2 3\n", limits: lim, want: []string{"ok"}, outcome: 1},
		{name: "include_dev_random", file: "include_dev_random.c", want: []string{"compile_error"}},
		{name: "include_dev_zero", file: "include_dev_zero.c", want: []string{"compile_error"}},
		{name: "threads_no_allowance", file: "threads.cpp", input: "", limits: lim, want: []string{"signal"}},
		{name: "threads_with_allowance", file: "threads.cpp", input: "", limits: wide, want: tle,
			check: func(t *testing.T, _ string) { assertNoSandboxProcesses(t) }},
		// kill(1) is ignored (namespace init) and kill(-1) cannot reach
		// anything outside the sandbox's pid namespace.
		{name: "kill_all", file: "kill_all.c", input: "", limits: lim, want: []string{"ok"},
			check: func(t *testing.T, out string) {
				if !strings.Contains(out, "survived") {
					t.Errorf("unexpected output %q", out)
				}
				assertNoSandboxProcesses(t)
			}},
		{name: "stack_overflow", file: "stack_overflow.c", input: "", limits: lim, want: []string{"signal", "memory"}},
		{name: "privilege_escalation", file: "privilege.c", input: "", limits: lim, want: []string{"ok"},
			check: func(t *testing.T, out string) {
				if strings.Contains(out, "ROOT") {
					t.Errorf("privilege escalation succeeded:\n%s", out)
				}
			}},
	}
}

// assertNoSandboxProcesses fails if any process owned by a sandbox uid of
// the boxes used by these tests (isolate uid = 60000 + box id) survived.
func assertNoSandboxProcesses(t *testing.T) {
	t.Helper()
	time.Sleep(50 * time.Millisecond)
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		st, err := os.Stat(filepath.Join("/proc", e.Name()))
		if err != nil {
			continue
		}
		if uid := statUID(st); uid >= 60000+400 && uid < 60000+600 {
			cmd, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
			t.Errorf("sandbox process %s (uid %d) survived: %q", e.Name(), uid, cmd)
		}
	}
}

// runBattery runs every case and returns name -> verdict summary.
func runBattery(t *testing.T, h *harness) map[string]string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var conns atomic.Int64
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conns.Add(1)
			c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port

	summary := map[string]string{}
	for _, c := range maliciousCases(t, port, &conns) {
		t.Run(c.name, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join("testdata", "malicious", c.file))
			if err != nil {
				t.Fatal(err)
			}
			lang := "c11"
			if strings.HasSuffix(c.file, ".cpp") {
				lang = "cpp17"
			}
			name := "sol" + filepath.Ext(c.file)
			tc := h.testcase(1, c.input, "5\n")
			start := time.Now()
			comp, evs, err := h.compileAndRun("Batch", batchJob{
				lang: lang, files: map[string][]byte{name: src}, limits: c.limits,
			}, []jobs.Testcase{tc})
			if err != nil {
				t.Fatalf("infrastructure error: %v", err)
			}
			got := "compile_error"
			var outcome float64
			var output string
			var detail string
			if comp.Success {
				ev := evs[0]
				got, outcome = ev.ExitStatus, ev.Outcome
				detail = fmt.Sprintf("time=%.3fs wall=%.3fs mem=%dKiB", ev.Time, ev.WallTime, ev.Memory>>10)
				output = h.lastOutput(t, lang, name, src, c)
			} else {
				detail = comp.Text + ": " + comp.Stderr + comp.Stdout
			}
			ok := false
			for _, w := range c.want {
				ok = ok || w == got
			}
			if !ok {
				t.Errorf("status = %s (%s), want one of %v", got, detail, c.want)
			}
			if outcome != c.outcome {
				t.Errorf("outcome = %v, want %v", outcome, c.outcome)
			}
			if c.check != nil {
				c.check(t, output)
			}
			summary[c.name] = got
			t.Logf("%-24s %-14s %s (%.2fs)", c.name, got, detail, time.Since(start).Seconds())
		})
	}
	return summary
}

// tle accepts both time limits for programs that burn CPU: on an
// overloaded machine the wall-clock limit (2×TL) can expire before the
// program accumulates TL of CPU time. Both are "time limit exceeded".
var tle = []string{"timeout", "timeout_wall"}

// verdictClass is the verdict a contestant sees (both time limits are TLE).
func verdictClass(status string) string {
	if status == "timeout" || status == "timeout_wall" {
		return "TLE"
	}
	return status
}

// lastOutput re-runs a program as a user test to capture its stdout (the
// evaluation itself only stores the verdict).
func (h *harness) lastOutput(t *testing.T, lang, name string, src []byte, c maliciousCase) string {
	if c.check == nil {
		return ""
	}
	j := h.job(jobs.KindUserTest, "Batch", batchJob{lang: lang, files: map[string][]byte{name: src}, limits: c.limits})
	j.Input = h.put([]byte(c.input))
	r := h.exec.Execute(t.Context(), 0, j)
	if r.Error != "" || r.UserTest == nil || r.UserTest.Output == "" {
		return ""
	}
	data, _, err := blob.ReadLimited(t.Context(), h.store, r.UserTest.Output, 1<<20)
	if err != nil {
		return ""
	}
	return string(data)
}

// TestMaliciousBattery runs the security battery twice and requires
// identical verdicts on both runs (SPEC §9: F2 exit criterion).
func TestMaliciousBattery(t *testing.T) {
	h := newHarness(t)
	first := runBattery(t, h)
	second := runBattery(t, h)
	var names []string
	for n := range first {
		names = append(names, n)
	}
	sort.Strings(names)
	var report strings.Builder
	for _, n := range names {
		fmt.Fprintf(&report, "| %s | %s | %s |\n", n, first[n], second[n])
		if verdictClass(first[n]) != verdictClass(second[n]) {
			t.Errorf("%s: verdict changed between runs: %s vs %s", n, first[n], second[n])
		}
	}
	t.Logf("battery summary (name | run 1 | run 2):\n%s", report.String())
	if out := os.Getenv("CMS_BATTERY_REPORT"); out != "" {
		os.WriteFile(out, []byte(report.String()), 0o644)
	}
}
