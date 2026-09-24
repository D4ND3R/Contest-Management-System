package worker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "tasks", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// hostBinary compiles a C file on the host (for manager binaries).
func hostBinary(t *testing.T, name string) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "bin")
	if b, err := exec.Command("gcc", "-O2", "-static", "-o", out, filepath.Join("testdata", "tasks", name)).CombinedOutput(); err != nil {
		t.Fatalf("gcc %s: %v %s", name, err, b)
	}
	b, _ := os.ReadFile(out)
	return b
}

type tcase struct {
	name    string
	b       batchJob
	tcs     []jobs.Testcase
	want    []float64 // outcome per testcase (-1: compile error)
	status  string    // expected exit status of the first testcase ("" = ok)
	textHas string    // substring of the first evaluation text
}

func runCases(t *testing.T, h *harness, taskType string, cases []tcase) {
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			comp, evs, err := h.compileAndRun(taskType, c.b, c.tcs)
			if err != nil {
				t.Fatalf("infrastructure error: %v", err)
			}
			if len(c.want) == 1 && c.want[0] == -1 {
				if comp.Success {
					t.Fatalf("expected a compilation error")
				}
				return
			}
			if !comp.Success {
				t.Fatalf("compilation failed: %s %s %s", comp.Text, comp.Stderr, comp.Stdout)
			}
			if len(evs) != len(c.want) {
				t.Fatalf("got %d evaluations", len(evs))
			}
			for i, w := range c.want {
				if evs[i].Outcome != w {
					t.Errorf("testcase %d: outcome %v (%s, %s), want %v", i, evs[i].Outcome, evs[i].ExitStatus, evs[i].Text, w)
				}
			}
			st := c.status
			if st == "" {
				st = "ok"
			}
			if evs[0].ExitStatus != st {
				t.Errorf("status %s (%s), want %s", evs[0].ExitStatus, evs[0].Text, st)
			}
			if c.textHas != "" && !strings.Contains(evs[0].Text, c.textHas) {
				t.Errorf("text %q does not contain %q", evs[0].Text, c.textHas)
			}
		})
	}
}

func TestBatchVariants(t *testing.T) {
	h := newHarness(t)
	lim := defaultLimits()
	sum := []jobs.Testcase{h.testcase(1, "2 3\n", "5\n"), h.testcase(2, "10 -4\n", "6\n")}
	cppGrader := map[string][]byte{"grader.cpp": fixture(t, "grader.cpp"), "task.h": fixture(t, "task.h")}
	pyGrader := map[string][]byte{"grader.py": fixture(t, "grader.py")}
	javaGrader := map[string][]byte{"grader.java": fixture(t, "grader.java")}
	cmsChecker := map[string][]byte{"checker.cpp": fixture(t, "checker.cpp")}
	testlib := map[string][]byte{"checker": hostBinary(t, "testlib_checker.c")}
	offByOne := []byte("#include <stdio.h>\nint main(void){long a,b;scanf(\"%ld %ld\",&a,&b);printf(\"%ld\\n\",a+b+1);return 0;}")
	right := []byte("#include <stdio.h>\nint main(void){long a,b;scanf(\"%ld %ld\",&a,&b);printf(\"%ld\\n\",a+b);return 0;}")
	piTC := []jobs.Testcase{h.testcase(1, "", "3.14159265\n")}

	runCases(t, h, "Batch", []tcase{
		{name: "grader_cpp", b: batchJob{lang: "cpp17", files: map[string][]byte{"sol.cpp": fixture(t, "grader_sol.cpp")},
			managers: cppGrader, params: map[string]any{"compilation": "grader"}, limits: lim}, tcs: sum, want: []float64{1, 1}},
		{name: "grader_python", b: batchJob{lang: "python3", files: map[string][]byte{"sol.py": fixture(t, "grader_sol.py")},
			managers: pyGrader, params: map[string]any{"compilation": "grader"}, limits: lim}, tcs: sum, want: []float64{1, 1}},
		{name: "grader_java", b: batchJob{lang: "java", files: map[string][]byte{"sol.java": fixture(t, "grader_sol.java")},
			managers: javaGrader, params: map[string]any{"compilation": "grader"}, limits: jobs.Limits{TimeMs: 2000, MemoryBytes: 256 << 20, OutputBytes: 1 << 20}},
			tcs: sum, want: []float64{1, 1}},
		{name: "grader_reserved_name", b: batchJob{lang: "cpp17", files: map[string][]byte{"grader.cpp": fixture(t, "grader_sol.cpp")},
			managers: cppGrader, params: map[string]any{"compilation": "grader"}, limits: lim}, tcs: sum, want: []float64{-1}},
		{name: "file_io", b: batchJob{lang: "c11", files: map[string][]byte{"sol.c": fixture(t, "fileio_ok.c")},
			params: map[string]any{"input_file": "input.txt", "output_file": "output.txt"}, limits: lim}, tcs: sum, want: []float64{1, 1}},
		{name: "file_io_missing_output", b: batchJob{lang: "c11", files: map[string][]byte{"sol.c": fixture(t, "fileio_stdout.c")},
			params: map[string]any{"input_file": "input.txt", "output_file": "output.txt"}, limits: lim}, tcs: sum[:1], want: []float64{0},
			textHas: "didn't produce file output.txt"},
		{name: "cms_checker_from_source_full", b: batchJob{lang: "c11", files: map[string][]byte{"sol.c": right},
			managers: cmsChecker, params: map[string]any{"checker": "custom"}, limits: lim}, tcs: sum, want: []float64{1, 1}, textHas: "Output is correct"},
		{name: "cms_checker_partial", b: batchJob{lang: "c11", files: map[string][]byte{"sol.c": offByOne},
			managers: cmsChecker, params: map[string]any{"checker": "custom"}, limits: lim}, tcs: sum, want: []float64{0.5, 0.5}, textHas: "Off by one"},
		{name: "testlib_checker_ok", b: batchJob{lang: "c11", files: map[string][]byte{"sol.c": right},
			managers: testlib, params: map[string]any{"checker": "testlib"}, limits: lim}, tcs: sum, want: []float64{1, 1}, textHas: "ok answer"},
		{name: "testlib_checker_points", b: batchJob{lang: "c11", files: map[string][]byte{"sol.c": offByOne},
			managers: testlib, params: map[string]any{"checker": "testlib"}, limits: lim}, tcs: sum, want: []float64{0.25, 0.25}},
		{name: "float_within_tolerance", b: batchJob{lang: "c11", files: map[string][]byte{"sol.c": fixture(t, "float_ok.c")},
			params: map[string]any{"checker": "float", "float_abs_tol": 1e-6}, limits: lim}, tcs: piTC, want: []float64{1}},
		{name: "float_outside_tolerance", b: batchJob{lang: "c11", files: map[string][]byte{"sol.c": fixture(t, "float_wa.c")},
			params: map[string]any{"checker": "float", "float_abs_tol": 1e-6}, limits: lim}, tcs: piTC, want: []float64{0}},
		{name: "exact_rejects_missing_newline", b: batchJob{lang: "c11",
			files:  map[string][]byte{"sol.c": []byte("#include <stdio.h>\nint main(void){printf(\"5\");return 0;}")},
			params: map[string]any{"checker": "exact"}, limits: lim}, tcs: sum[:1], want: []float64{0}},
	})
}

func TestBatchUserTest(t *testing.T) {
	h := newHarness(t)
	j := h.job(jobs.KindUserTest, "Batch", batchJob{lang: "python3", files: map[string][]byte{"sol.py": []byte("print(sum(map(int, input().split())) * 2)\n")}, limits: defaultLimits()})
	j.Input = h.put([]byte("20 1\n"))
	r := h.execute(j)
	if r.Error != "" || r.Compilation == nil || !r.Compilation.Success || r.UserTest == nil {
		t.Fatalf("user test: %+v", r)
	}
	out, _ := blob.ReadAll(context.Background(), h.store, r.UserTest.Output)
	if string(out) != "42\n" || r.UserTest.ExitStatus != "ok" {
		t.Fatalf("output %q status %s", out, r.UserTest.ExitStatus)
	}
	// A compilation error is reported without running.
	j = h.job(jobs.KindUserTest, "Batch", batchJob{lang: "c11", files: map[string][]byte{"s.c": []byte("nope")}, limits: defaultLimits()})
	j.Input = h.put(nil)
	r = h.execute(j)
	if r.Error != "" || r.Compilation.Success || r.UserTest != nil {
		t.Fatalf("user test with CE: %+v", r)
	}
}

func TestOutputOnly(t *testing.T) {
	h := newHarness(t)
	tcs := []jobs.Testcase{h.testcase(0, "in0", "1 2 3\n"), h.testcase(1, "in1", "hello\n"), h.testcase(2, "in2", "7\n")}
	files := map[string][]byte{
		"output_" + tcs[0].Codename + ".txt": []byte("1   2 3"), // white-diff equal
		"output_" + tcs[1].Codename + ".txt": []byte("world\n"), // wrong
	}
	_, evs, err := h.compileAndRun("OutputOnly", batchJob{files: files}, tcs)
	if err != nil {
		t.Fatal(err)
	}
	if evs[0].Outcome != 1 || evs[1].Outcome != 0 || evs[2].Outcome != 0 || !strings.Contains(evs[2].Text, "not submitted") {
		t.Fatalf("evaluations %+v", evs)
	}
	// With a custom checker (partial scores) the file goes through the sandbox.
	files = map[string][]byte{"output_" + tcs[2].Codename + ".txt": []byte("8\n")}
	_, evs, err = h.compileAndRun("OutputOnly", batchJob{files: files, managers: map[string][]byte{"checker.cpp": fixture(t, "checker.cpp")},
		params: map[string]any{"checker": "custom"}}, tcs[2:])
	if err != nil || evs[0].Outcome != 0.5 {
		t.Fatalf("custom checker on output-only: %+v %v", evs, err)
	}
	j := h.job(jobs.KindUserTest, "OutputOnly", batchJob{})
	if r := h.execute(j); r.Error == "" {
		t.Fatal("user tests must be rejected for OutputOnly")
	}
}

func TestTwoSteps(t *testing.T) {
	h := newHarness(t)
	lim := defaultLimits()
	mgr := map[string][]byte{"manager.cpp": fixture(t, "twosteps_manager.cpp")}
	tcs := []jobs.Testcase{h.testcase(1, "42\n", "42\n"), h.testcase(2, "123456789\n", "123456789\n")}
	runCases(t, h, "TwoSteps", []tcase{
		{name: "encode_decode", b: batchJob{lang: "cpp17", files: map[string][]byte{"sol.cpp": fixture(t, "twosteps_ok.cpp")}, managers: mgr, limits: lim},
			tcs: tcs, want: []float64{1, 1}},
		// The second step cannot read the original input: it only sees the message.
		{name: "second_step_isolated", b: batchJob{lang: "cpp17", files: map[string][]byte{"sol.cpp": fixture(t, "twosteps_cheat.cpp")}, managers: mgr, limits: lim},
			tcs: tcs, want: []float64{0, 0}},
	})
}

func TestCommunication(t *testing.T) {
	h := newHarness(t)
	lim := defaultLimits()
	mgr := map[string][]byte{"manager.cpp": fixture(t, "comm_manager.c"), "stub.c": fixture(t, "stub.c")}
	in := "4\n1 2\n3 4\n100 -1\n7 7\n"
	tcs := []jobs.Testcase{h.testcase(1, in, "")}
	two := map[string]any{"num_processes": 2}
	runCases(t, h, "Communication", []tcase{
		{name: "ok", b: batchJob{lang: "c11", files: map[string][]byte{"sol.c": fixture(t, "comm_ok.c")}, managers: mgr, limits: lim},
			tcs: tcs, want: []float64{1}, textHas: "Output is correct"},
		{name: "wrong", b: batchJob{lang: "c11", files: map[string][]byte{"sol.c": fixture(t, "comm_wa.c")}, managers: mgr, limits: lim},
			tcs: tcs, want: []float64{0}, textHas: "isn't correct"},
		{name: "crash", b: batchJob{lang: "c11", files: map[string][]byte{"sol.c": fixture(t, "comm_crash.c")}, managers: mgr, limits: lim},
			tcs: tcs, want: []float64{0}, status: "signal"},
		{name: "timeout", b: batchJob{lang: "c11", files: map[string][]byte{"sol.c": fixture(t, "comm_tle.c")}, managers: mgr, limits: lim},
			tcs: tcs, want: []float64{0}, status: "timeout"},
		{name: "two_processes", b: batchJob{lang: "c11", files: map[string][]byte{"sol.c": fixture(t, "comm_ok.c")}, managers: mgr, params: two, limits: lim},
			tcs: tcs, want: []float64{1}},
		{name: "std_io", b: batchJob{lang: "c11", files: map[string][]byte{"sol.c": fixture(t, "comm_stdio.c")}, managers: mgr,
			params: map[string]any{"compilation": "alone", "user_io": "std_io"}, limits: lim}, tcs: tcs, want: []float64{1}},
	})
}
