package worker

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
)

// Sample solutions for every problem type (SPEC_AUDIT §2): each type is
// judged with an accepted, a wrong, a too slow, a too greedy and a crashing
// solution. Sources are generated from small per-language templates.

// verdicts in the order the tables use.
var fiveVerdicts = []string{"AC", "WA", "TLE", "MLE", "RE"}

// cMain returns a C program reading "a b" (stdin, or input.txt when files
// is set) and printing a+b with the requested behaviour.
func cMain(v string, files bool) string {
	in, out, open := "stdin", "stdout", ""
	if files {
		in, out = "fi", "fo"
		open = `FILE *fi = fopen("input.txt", "r"), *fo = fopen("output.txt", "w"); if (!fi || !fo) return 1;`
	}
	body := map[string]string{
		"AC":  "fprintf(" + out + ", \"%lld\\n\", a + b);",
		"WA":  "fprintf(" + out + ", \"%lld\\n\", a + b + 1);",
		"TLE": "volatile unsigned long x = 0; for (;;) x++;",
		"MLE": "size_t n = 1UL << 27; long long *p = malloc(n * sizeof *p); if (!p) return 1; for (size_t i = 0; i < n; i++) p[i] = (long long)i + a; volatile long long s = 0; for (size_t i = 0; i < n; i += 4096) s += p[i]; fprintf(" + out + ", \"%lld\\n\", s);",
		"RE":  "volatile int *p = 0; *p = (int)a; fprintf(" + out + ", \"%lld\\n\", a + b);",
	}[v]
	return "#include <stdio.h>\n#include <stdlib.h>\nint main(void) { " + open + " long long a, b; if (fscanf(" + in + ", \"%lld %lld\", &a, &b) != 2) return 1; " + body + " return 0; }\n"
}

// solveFunc returns the contestant's solve(a, b) for a grader.
func solveFunc(lang, v string) (name string, src string) {
	switch lang {
	case "c11", "cpp17":
		body := map[string]string{
			"AC":  "return a + b;",
			"WA":  "return a - b;",
			"TLE": "volatile unsigned long x = 0; for (;;) x++; return 0;",
			"MLE": "size_t n = 1UL << 27; long long *p = (long long *)malloc(n * sizeof *p); if (!p) return 0; for (size_t i = 0; i < n; i++) p[i] = (long long)i + a; volatile long long s = 0; for (size_t i = 0; i < n; i += 4096) s += p[i]; return s;",
			"RE":  "volatile int *p = 0; *p = (int)a; return a + b;",
		}[v]
		ext := ".c"
		if lang == "cpp17" {
			ext = ".cpp"
		}
		return "sol" + ext, "#include <stdlib.h>\n#include \"task.h\"\nlong long solve(long long a, long long b) { " + body + " }\n"
	case "java":
		body := map[string]string{
			"AC":  "return a + b;",
			"WA":  "return a - b;",
			"TLE": "while (System.nanoTime() > 0) { } return 0;",
			"MLE": "java.util.ArrayList<long[]> l = new java.util.ArrayList<>(); while (true) l.add(new long[1 << 20]);",
			"RE":  "int[] x = new int[1]; return x[(int) (a + 5)];",
		}[v]
		return "sol.java", "public class sol { public static long solve(long a, long b) { " + body + " } }\n"
	case "python3":
		body := map[string]string{
			"AC":  "return a + b",
			"WA":  "return a - b",
			"TLE": "while True:\n        pass",
			"MLE": "x = bytearray(900 * 1024 * 1024)\n    return len(x)",
			"RE":  "raise ValueError('boom')",
		}[v]
		return "sol.py", "def solve(a, b):\n    " + body + "\n"
	}
	panic(lang)
}

// judgeVerdict runs one job and returns the contest verdict of its first
// testcase (or CE).
func judgeVerdict(t *testing.T, h *harness, taskType string, b batchJob, tcs []jobs.Testcase) (string, []jobs.Evaluation) {
	t.Helper()
	comp, evs, err := h.compileAndRun(taskType, b, tcs)
	if err != nil {
		t.Fatalf("infrastructure error: %v", err)
	}
	if comp != nil && !comp.Success {
		t.Fatalf("compilation failed: %s %s %s", comp.Text, comp.Stderr, comp.Stdout)
	}
	return verdict(comp, evs), evs
}

type typeReport struct {
	rows map[string]string
}

func (r *typeReport) add(name, got string) {
	if r.rows == nil {
		r.rows = map[string]string{}
	}
	r.rows[name] = got
}

func (r *typeReport) log(t *testing.T) {
	var names []string
	for n := range r.rows {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "| %s | %s |\n", n, r.rows[n])
	}
	t.Logf("verdicts:\n%s", b.String())
}

func TestProblemTypeSamples(t *testing.T) {
	h := newHarness(t)
	lim := defaultLimits()
	sum := []jobs.Testcase{h.testcase(1, "2 3\n", "5\n"), h.testcase(2, "10 -4\n", "6\n")}
	rep := &typeReport{}
	defer rep.log(t)
	expect := func(t *testing.T, name, got, want string, evs []jobs.Evaluation) {
		t.Helper()
		rep.add(name, got)
		if got != want {
			var detail string
			if len(evs) > 0 {
				detail = fmt.Sprintf("%s: %s", evs[0].ExitStatus, evs[0].Text)
			}
			t.Errorf("%s: verdict %s (%s), want %s", name, got, detail, want)
		}
	}

	t.Run("batch_files", func(t *testing.T) {
		params := map[string]any{"input_file": "input.txt", "output_file": "output.txt"}
		for _, v := range fiveVerdicts {
			got, evs := judgeVerdict(t, h, "Batch", batchJob{lang: "c11", files: map[string][]byte{"sol.c": []byte(cMain(v, true))},
				params: params, limits: lim}, sum)
			expect(t, "batch_files/"+v, got, v, evs)
		}
	})

	t.Run("batch_grader", func(t *testing.T) {
		graders := map[string]map[string][]byte{
			"c11":     {"grader.c": fixture(t, "grader.c"), "task.h": fixture(t, "task.h")},
			"cpp17":   {"grader.cpp": fixture(t, "grader.cpp"), "task.h": fixture(t, "task.h")},
			"java":    {"grader.java": fixture(t, "grader.java")},
			"python3": {"grader.py": fixture(t, "grader.py")},
		}
		for _, lang := range []string{"c11", "cpp17", "java", "python3"} {
			if !toolchainAvailable(h, lang) || !selectedLanguage(lang) {
				t.Logf("skipping %s (toolchain not installed or not selected)", lang)
				continue
			}
			for _, v := range fiveVerdicts {
				name, src := solveFunc(lang, v)
				got, evs := judgeVerdict(t, h, "Batch", batchJob{lang: lang, files: map[string][]byte{name: []byte(src)},
					managers: graders[lang], params: map[string]any{"compilation": "grader"}, limits: lim}, sum)
				expect(t, "batch_grader/"+lang+"/"+v, got, v, evs)
			}
		}
	})

	t.Run("batch_grader_files", func(t *testing.T) {
		mgr := map[string][]byte{"grader.c": fixture(t, "grader_files.c"), "task.h": fixture(t, "task.h")}
		params := map[string]any{"compilation": "grader", "input_file": "input.txt", "output_file": "output.txt"}
		for _, v := range fiveVerdicts {
			name, src := solveFunc("c11", v)
			got, evs := judgeVerdict(t, h, "Batch", batchJob{lang: "c11", files: map[string][]byte{name: []byte(src)},
				managers: mgr, params: params, limits: lim}, sum)
			expect(t, "batch_grader_files/"+v, got, v, evs)
		}
	})

	t.Run("interactive", func(t *testing.T) {
		mgr := map[string][]byte{"interactor.cpp": fixture(t, "interactor.cpp")}
		game := []jobs.Testcase{h.testcase(1, "123456789 40\n", ""), h.testcase(2, "7 40\n", "")}
		cases := []struct {
			name, lang, file, want, textHas string
		}{
			{"AC", "c11", "inter_ok.c", "AC", "found in"},
			{"AC_python", "python3", "inter_ok.py", "AC", "found in"},
			{"WA", "c11", "inter_wa.c", "WA", "wrong answer"},
			{"TLE", "c11", "inter_tle.c", "TLE", ""},
			{"TLE_no_flush", "c11", "inter_noflush.c", "TLE", "wall"},
			{"MLE", "c11", "inter_mle.c", "MLE", ""},
			{"RE", "c11", "inter_re.c", "RE", ""},
			{"contestant_ends_first", "c11", "inter_early_exit.c", "WA", "unexpected end of file"},
			{"invalid_output", "c11", "inter_invalid.c", "WA", "invalid query"},
			{"interactor_ends_first", "c11", "inter_spam.c", "WA", "too many queries"},
		}
		for _, c := range cases {
			if !toolchainAvailable(h, c.lang) {
				continue
			}
			ext := ".c"
			if c.lang == "python3" {
				ext = ".py"
			}
			got, evs := judgeVerdict(t, h, "Interactive", batchJob{lang: c.lang, files: map[string][]byte{"sol" + ext: fixture(t, c.file)},
				managers: mgr, limits: lim}, game)
			expect(t, "interactive/"+c.name, got, c.want, evs)
			if c.textHas != "" && (len(evs) == 0 || !strings.Contains(strings.ToLower(evs[0].Text), c.textHas)) {
				t.Errorf("interactive/%s: text %q does not mention %q", c.name, evs[0].Text, c.textHas)
			}
		}
		// The interactor burns 1.5 s of CPU before playing: the contestant
		// (1 s limit) is not charged for it.
		slow := lim
		slow.WallTimeMs = 6000
		got, evs := judgeVerdict(t, h, "Interactive", batchJob{lang: "c11", files: map[string][]byte{"sol.c": fixture(t, "inter_ok.c")},
			managers: mgr, limits: slow}, []jobs.Testcase{h.testcase(1, "999 40 1\n", "")})
		expect(t, "interactive/slow_interactor", got, "AC", evs)
		if len(evs) > 0 && evs[0].Time > 0.5 {
			t.Errorf("contestant charged %.2fs for the interactor's work", evs[0].Time)
		}
	})

	t.Run("communication", func(t *testing.T) {
		mgr := map[string][]byte{"manager.cpp": fixture(t, "comm_manager.c"), "stub.c": fixture(t, "stub.c")}
		in := []jobs.Testcase{h.testcase(1, "4\n1 2\n3 4\n100 -1\n7 7\n", "")}
		add := map[string]string{
			"AC":  "long long add(long long a, long long b) { return a + b; }",
			"WA":  "long long add(long long a, long long b) { return a - b; }",
			"TLE": "long long add(long long a, long long b) { volatile unsigned long x = 0; for (;;) x++; return 0; }",
			"MLE": "#include <stdlib.h>\nlong long add(long long a, long long b) { size_t n = 1UL << 27; long long *p = malloc(n * sizeof *p); if (!p) return 0; for (size_t i = 0; i < n; i++) p[i] = (long long)i; volatile long long s = 0; for (size_t i = 0; i < n; i += 4096) s += p[i]; return a + b + (s & 0); }",
			"RE":  "long long add(long long a, long long b) { volatile int *p = 0; *p = 1; return a + b; }",
		}
		for _, v := range fiveVerdicts {
			got, evs := judgeVerdict(t, h, "Communication", batchJob{lang: "c11", files: map[string][]byte{"sol.c": []byte(add[v])},
				managers: mgr, limits: lim}, in)
			expect(t, "communication/"+v, got, v, evs)
		}
		// Stubs in other languages.
		if toolchainAvailable(h, "cpp17") {
			got, evs := judgeVerdict(t, h, "Communication", batchJob{lang: "cpp17",
				files:    map[string][]byte{"sol.cpp": []byte("long long add(long long a, long long b) { return a + b; }\n")},
				managers: map[string][]byte{"manager.cpp": fixture(t, "comm_manager.c"), "stub.cpp": fixture(t, "stub.cpp")}, limits: lim}, in)
			expect(t, "communication/stub_cpp", got, "AC", evs)
		}
		if toolchainAvailable(h, "python3") {
			got, evs := judgeVerdict(t, h, "Communication", batchJob{lang: "python3",
				files:    map[string][]byte{"sol.py": []byte("def add(a, b):\n    return a + b\n")},
				managers: map[string][]byte{"manager.cpp": fixture(t, "comm_manager.c"), "stub.py": fixture(t, "stub.py")}, limits: lim}, in)
			expect(t, "communication/stub_python", got, "AC", evs)
		}
		// Per-process versus summed limits: two processes using 0.6 s of
		// CPU each fit a 1 s limit per process but not in total.
		burn := "#include <time.h>\nstatic int burnt;\nlong long add(long long a, long long b) { if (!burnt) { clock_t s = clock(); while (clock() - s < CLOCKS_PER_SEC * 6 / 10); burnt = 1; } return a + b; }"
		for mode, want := range map[string]string{"per_process": "AC", "total": "TLE"} {
			got, evs := judgeVerdict(t, h, "Communication", batchJob{lang: "c11", files: map[string][]byte{"sol.c": []byte(burn)},
				managers: mgr, params: map[string]any{"num_processes": 2, "limits_mode": mode}, limits: lim}, in)
			expect(t, "communication/limits_"+mode, got, want, evs)
		}
	})

	t.Run("twosteps", func(t *testing.T) {
		mgr := map[string][]byte{"manager.cpp": fixture(t, "twosteps_manager.cpp")}
		tcs := []jobs.Testcase{h.testcase(1, "42\n", "42\n")}
		encode := map[string]string{
			"AC":  `std::string s; do { s += char('0' + n % 2); n /= 2; } while (n); return s;`,
			"WA":  `std::string s; do { s += char('0' + n % 2); n /= 2; } while (n); return s;`,
			"TLE": `volatile unsigned long x = 0; for (;;) x++; return "";`,
			"MLE": `size_t k = 1UL << 27; long long *p = (long long *)malloc(k * sizeof *p); if (!p) return ""; for (size_t i = 0; i < k; i++) p[i] = (long long)i; volatile long long t = 0; for (size_t i = 0; i < k; i += 4096) t += p[i]; return t < 0 ? "x" : "1";`,
			"RE":  `volatile int *p = 0; *p = 1; return "";`,
		}
		for _, v := range fiveVerdicts {
			decodeBody := "long long n = 0; for (int i = (int)m.size() - 1; i >= 0; i--) n = n * 2 + (m[i] - '0'); return n;"
			if v == "WA" {
				decodeBody = "long long n = 0; for (int i = (int)m.size() - 1; i >= 0; i--) n = n * 2 + (m[i] - '0'); return n + 1;"
			}
			src := "#include <string>\n#include <cstdlib>\nstd::string encode(long long n) { " + encode[v] + " }\nlong long decode(const std::string &m) { " + decodeBody + " }\n"
			got, evs := judgeVerdict(t, h, "TwoSteps", batchJob{lang: "cpp17", files: map[string][]byte{"sol.cpp": []byte(src)},
				managers: mgr, limits: lim}, tcs)
			expect(t, "twosteps/"+v, got, v, evs)
		}
	})

	t.Run("output_only", func(t *testing.T) {
		// Nothing is executed: only AC, WA and missing (partial) outputs apply.
		tcs := []jobs.Testcase{h.testcase(0, "", "1\n"), h.testcase(1, "", "2\n")}
		got, evs := judgeVerdict(t, h, "OutputOnly", batchJob{files: map[string][]byte{"output_a.txt": []byte("1\n"), "output_b.txt": []byte("2\n")}}, tcs)
		expect(t, "output_only/AC", got, "AC", evs)
		got, evs = judgeVerdict(t, h, "OutputOnly", batchJob{files: map[string][]byte{"output_a.txt": []byte("3\n")}}, tcs)
		expect(t, "output_only/WA", got, "WA", evs)
		_, evs = judgeVerdict(t, h, "OutputOnly", batchJob{files: map[string][]byte{"output_b.txt": []byte("2\n")}}, tcs)
		if evs[0].Outcome != 0 || evs[1].Outcome != 1 || !strings.Contains(evs[0].Text, "not submitted") {
			t.Errorf("partial output-only submission: %+v", evs)
		}
	})
}
