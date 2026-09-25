package problempkg

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/langs"
)

// zipDir zips a directory (under prefix inside the zip).
func zipDir(t *testing.T, dir, prefix string) *zip.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		w, err := zw.Create(prefix + filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	zw.Close()
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return zr
}

func zipOf(t *testing.T, files map[string]string) *zip.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, _ := zw.Create(name)
		w.Write([]byte(data))
	}
	zw.Close()
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return zr
}

// options uses the configured languages (config/languages).
func options(t *testing.T) Options {
	t.Helper()
	reg, err := langs.Load(filepath.Join("..", "..", "config", "languages"))
	if err != nil {
		t.Fatal(err)
	}
	return OptionsFor(reg)
}

const examples = "../../docs/examples/packages"

// TestExamplePackages keeps the documented examples valid: one per task
// type, zipped as a folder (as people usually do).
func TestExamplePackages(t *testing.T) {
	o := options(t)
	want := map[string]struct {
		taskType, scoreType string
		tests, managers     int
		max                 float64
		solutions           string
	}{
		"batch-suma":            {"Batch", "GroupMin", 4, 0, 100, "ac_suma.cpp=ac/cpp17 ac_suma.py=ac/python3 pa_int.c=pa/c11 tle_lento.cpp=tle/cpp17 wa_resta.c=wa/c11"},
		"interactive-adivina":   {"Interactive", "Sum", 4, 1, 100, "ac_binaria.c=ac/c11 ac_binaria.py=ac/python3 tle_ciclo.c=tle/c11 wa_uno.c=wa/c11"},
		"output-only-cuadrados": {"OutputOnly", "Sum", 4, 0, 100, "ac_todas=ac/ pa_mitad=pa/"},
		"communication-suma":    {"Communication", "Sum", 2, 3, 100, "ac_add.c=ac/c11 wa_resta.c=wa/c11"},
		"two-steps-binario":     {"TwoSteps", "Sum", 5, 1, 100, "ac_binario.cpp=ac/cpp17 wa_trampa.cpp=wa/cpp17"},
	}
	all, err := os.ReadDir(examples)
	if err != nil {
		t.Fatal(err)
	}
	var entries []os.DirEntry
	for _, e := range all {
		if e.IsDir() {
			entries = append(entries, e)
		}
	}
	if len(entries) != len(want) {
		t.Fatalf("%d examples, want %d", len(entries), len(want))
	}
	for _, e := range entries {
		t.Run(e.Name(), func(t *testing.T) {
			w, ok := want[e.Name()]
			if !ok {
				t.Fatalf("unexpected example %s", e.Name())
			}
			p := Read(zipDir(t, filepath.Join(examples, e.Name()), e.Name()+"/"), o)
			if !p.OK() || len(p.Warnings) > 0 {
				t.Fatalf("errors %v warnings %v", p.Errors, p.Warnings)
			}
			c := p.Config
			if c.TaskType() != w.taskType || c.ScoreType() != w.scoreType || len(p.Tests) != w.tests ||
				len(p.Managers) != w.managers || p.MaxScore != w.max || len(p.Statements) == 0 {
				t.Fatalf("%s/%s tests=%d managers=%d max=%v statements=%d", c.TaskType(), c.ScoreType(), len(p.Tests),
					len(p.Managers), p.MaxScore, len(p.Statements))
			}
			var sols []string
			for _, s := range p.Solutions {
				sols = append(sols, s.Name+"="+s.Expected+"/"+s.Language)
			}
			if got := strings.Join(sols, " "); got != w.solutions {
				t.Fatalf("solutions %s", got)
			}
		})
	}
}

func TestReadReportsEveryProblem(t *testing.T) {
	good := "name: x\ntype: batch\ntime_limit: 1\nmemory_limit: 64\n"
	cases := []struct {
		name  string
		files map[string]string
		want  []string // substrings of "path: message" errors
	}{
		{"no config", map[string]string{"tests/1.in": "1", "tests/1.out": "1"}, []string{"problem.yaml: missing"}},
		{"unknown key", map[string]string{"problem.yaml": good + "colour: red\n", "tests/1.in": "1", "tests/1.out": "1"},
			[]string{"field colour not found"}},
		{"bad values", map[string]string{"problem.yaml": "name: a b\ntype: batchy\nmemory_limit: -1\nscoring: group_min\n", "tests/1.in": "1", "tests/1.out": "1"},
			[]string{`name "a b"`, `type "batchy"`, "time_limit (seconds) is required", "scoring group_min needs subtasks"}},
		{"files", map[string]string{"problem.yaml": good + "checker: testlib\nlanguages: [cobol]\n",
			"tests/1.in": "1", "tests/2.out": "2", "tests/3.txt": "x", "statement/spanish.pdf": "%PDF", "statement/es.doc": "x",
			"solutions/main.cpp": "int main(){}", "solutions/ac_x.zzz": "?", "../evil": "x"},
			[]string{"tests/1.in: no expected output", "tests/2.out: no input", "tests/3.txt: testcase files end in",
				"statement/spanish.pdf: name the statement after its language", "statement/es.doc: statements must be",
				`languages: "cobol" is not configured`, "needs checker", "solutions/main.cpp: start the name with the expected verdict",
				`solutions/ac_x.zzz: no configured language uses the extension ".zzz"`, "../evil: unsafe path"}},
		{"subtasks", map[string]string{"problem.yaml": good + "scoring: group_min\nsubtasks:\n  - {points: 50, tests: \"a.*\"}\n  - {points: 50, tests: [zz]}\n",
			"tests/a1.in": "1", "tests/a1.out": "1"}, []string{`unknown testcase "zz"`}},
	}
	o := options(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := Read(zipOf(t, c.files), o)
			var all []string
			for _, e := range p.Errors {
				all = append(all, e.String())
			}
			joined := strings.Join(all, "\n")
			for _, w := range c.want {
				if !strings.Contains(joined, w) {
					t.Errorf("missing %q in:\n%s", w, joined)
				}
			}
			if p.OK() {
				t.Error("accepted")
			}
		})
	}
	// Uncovered testcases and stray files are warnings only.
	p := Read(zipOf(t, map[string]string{"problem.yaml": good + "scoring: group_min\nsubtasks:\n  - {points: 100, tests: 1}\n",
		"tests/a.in": "1", "tests/a.out": "1", "tests/b.in": "1", "tests/b.out": "1", "README.md": "hi", "__MACOSX/._x": ""}), o)
	if !p.OK() || len(p.Warnings) != 2 || !strings.Contains(p.Warnings[0].String()+p.Warnings[1].String(), "testcases in no subtask") {
		t.Fatalf("errors %v warnings %v", p.Errors, p.Warnings)
	}
	// Zip bombs are refused before anything is read.
	o.MaxBytes = 10
	if p := Read(zipOf(t, map[string]string{"problem.yaml": good, "tests/1.in": "123456789012", "tests/1.out": "1"}), o); p.OK() {
		t.Fatal("size limit ignored")
	}
}

func TestConfigParams(t *testing.T) {
	c, err := ParseConfig([]byte("name: t\ntype: communication\ntime_limit: 1.5\nmemory_limit: 64\nprocesses: 2\nuser_io: std_io\n" +
		"limits_mode: total\nmanager_time_limit: 2\nscoring: group_threshold\nsubtasks:\n  - {points: 40, tests: 3, threshold: 0.5}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(c.TaskTypeParams()); got != `{"limits_mode":"total","manager_time_limit_ms":2000,"num_processes":2,"user_io":"std_io"}` {
		t.Fatalf("params %s", got)
	}
	if got := string(c.ScoreTypeParams(3)); got != `[[40,3,0.5]]` {
		t.Fatalf("score %s", got)
	}
	s, _ := ParseConfig([]byte("name: t\ntime_limit: 1\nmemory_limit: 1\n"))
	if s.Type != "batch" || s.Scoring != "sum" || string(s.ScoreTypeParams(4)) != "25" || s.Title != "t" || s.Dataset != "Default" {
		t.Fatalf("defaults %+v", s)
	}
}

func TestCheck(t *testing.T) {
	yes, no := true, false
	scored := func(score float64, kinds ...string) RunResult {
		r := RunResult{Judged: true, Compiled: &yes, Scored: true, Score: score, MaxScore: 100}
		for _, k := range kinds {
			switch k {
			case "wa":
				r.WA = true
			case "tle":
				r.TLE = true
			case "mle":
				r.MLE = true
			case "re":
				r.RE = true
			}
		}
		return r
	}
	cases := []struct {
		expected    string
		r           RunResult
		status, got string
	}{
		{"ac", scored(100), "pass", "ac"},
		{"ac", scored(30, "wa"), "fail", "pa 30/100 wa"},
		{"pa", scored(30, "wa"), "pass", "pa 30/100 wa"},
		{"pa", scored(0, "wa"), "fail", "wa"},
		{"wa", scored(0, "wa", "tle"), "pass", "wa tle"},
		{"tle", scored(0, "wa", "tle"), "pass", "wa tle"},
		{"tle", scored(100), "fail", "ac"},
		{"mle", scored(0, "re"), "fail", "re"},
		{"re", scored(12.5, "re"), "pass", "pa 12.5/100 re"},
		{"ce", RunResult{Judged: true, Compiled: &no}, "pass", "ce"},
		{"ac", RunResult{Judged: true, Compiled: &no}, "fail", "ce"},
		{"any", scored(0, "tle"), "pass", "tle"},
		{"ac", RunResult{Judged: true, Compiled: &yes}, "pending", ""},
		{"ac", RunResult{}, "pending", ""},
		{"ac", RunResult{Judged: true, SystemError: "checker.cpp does not compile"}, "fail", "system error: checker.cpp does not compile"},
	}
	for _, c := range cases {
		if st, got := Check(c.expected, c.r); st != c.status || got != c.got {
			t.Errorf("Check(%s, %+v) = %s %q, want %s %q", c.expected, c.r, st, got, c.status, c.got)
		}
	}
}
