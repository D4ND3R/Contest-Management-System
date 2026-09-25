package e2e

import (
	"archive/zip"
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/auth"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/webtest"
)

func workerFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "worker", "testdata", "tasks", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func zipFiles(files map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, _ := zw.Create(name)
		w.Write([]byte(data))
	}
	zw.Close()
	return buf.Bytes()
}

// typeCase is one task type configured through the admin forms.
type typeCase struct {
	name     string
	fields   url.Values // structured task type fields
	managers map[string][]byte
	tests    [][2]string // input, output
	lang     string
	file     string // submission file field
	ac, wa   []byte
	zip      bool // output-only: the solution is a zip of outputs
}

// TestEveryTaskTypeFromAdminUI (SPEC_AUDIT §2): every problem type is
// created and configured only through the admin web server, and the task
// tester judges an accepted and a wrong reference solution for each one on
// the real dispatcher and isolate worker.
func TestEveryTaskTypeFromAdminUI(t *testing.T) {
	s := newStack(t, stackOpts{workers: true, admin: true, empty: true})
	hash, _ := auth.HashPassword("adminpass")
	s.q.CreateAdmin(bg, sqlc.CreateAdminParams{Name: "Admin", Username: "admin", PasswordHash: hash, Enabled: true, Role: "all"})
	a := webtest.New(t, s.awsURL)
	a.Get("/login")
	code, body := a.Post("/login", url.Values{"username": {"admin"}, "password": {"adminpass"}})
	webtest.MustOK(t, "admin login", code, body)

	sumTests := [][2]string{{"2 3\n", "5\n"}, {"10 -4\n", "6\n"}}
	sumAC := []byte("#include <stdio.h>\nint main(void){long a,b;scanf(\"%ld %ld\",&a,&b);printf(\"%ld\\n\",a+b);return 0;}\n")
	sumWA := []byte("#include <stdio.h>\nint main(void){long a,b;scanf(\"%ld %ld\",&a,&b);printf(\"%ld\\n\",a-b);return 0;}\n")
	cases := []typeCase{
		{name: "normal_io", fields: url.Values{"task_type": {"Batch"}, "tt_checker": {"white_diff"}},
			tests: sumTests, lang: "c11", file: "normal_io.%l", ac: sumAC, wa: sumWA},
		{name: "normal_io_float", fields: url.Values{"task_type": {"Batch"}, "tt_checker": {"float"}, "tt_float_abs_tol": {"0.01"}},
			tests: [][2]string{{"1 2\n", "3.001\n"}, {"2 2\n", "4\n"}}, lang: "c11", file: "normal_io_float.%l", ac: sumAC,
			wa: []byte("#include <stdio.h>\nint main(void){long a,b;scanf(\"%ld %ld\",&a,&b);printf(\"%ld.5\\n\",a+b);return 0;}\n")},
		{name: "batch_grader_files", fields: url.Values{"task_type": {"Batch"}, "tt_compilation": {"grader"}, "tt_input_file": {"input.txt"},
			"tt_output_file": {"output.txt"}, "tt_checker": {"exact"}},
			managers: map[string][]byte{"grader.c": workerFixture(t, "grader_files.c"), "task.h": workerFixture(t, "task.h")},
			tests:    sumTests, lang: "c11", file: "batch_grader_files.%l",
			ac: []byte("#include \"task.h\"\nlong long solve(long long a, long long b) { return a + b; }\n"),
			wa: []byte("#include \"task.h\"\nlong long solve(long long a, long long b) { return a * b; }\n")},
		{name: "interactive", fields: url.Values{"task_type": {"Interactive"}},
			managers: map[string][]byte{"interactor.cpp": workerFixture(t, "interactor.cpp")},
			tests:    [][2]string{{"123456 40\n", "\n"}, {"7 40\n", "\n"}}, lang: "c11", file: "interactive.%l",
			ac: workerFixture(t, "inter_ok.c"), wa: workerFixture(t, "inter_wa.c")},
		{name: "communication", fields: url.Values{"task_type": {"Communication"}, "tt_num_processes": {"2"}, "tt_comm_compilation": {"stub"},
			"tt_user_io": {"fifos"}, "tt_limits_mode": {"total"}},
			managers: map[string][]byte{"manager.c": workerFixture(t, "comm_manager.c"), "stub.c": workerFixture(t, "stub.c")},
			tests:    [][2]string{{"4\n1 2\n3 4\n100 -1\n7 7\n", "\n"}, {"1\n5 5\n", "\n"}}, lang: "c11", file: "communication.%l",
			ac: workerFixture(t, "comm_ok.c"), wa: workerFixture(t, "comm_wa.c")},
		{name: "twosteps", fields: url.Values{"task_type": {"TwoSteps"}, "tt_checker": {"white_diff"}},
			managers: map[string][]byte{"manager.cpp": workerFixture(t, "twosteps_manager.cpp")},
			tests:    [][2]string{{"42\n", "42\n"}, {"123456789\n", "123456789\n"}}, lang: "cpp17", file: "twosteps.%l",
			ac: workerFixture(t, "twosteps_ok.cpp"), wa: workerFixture(t, "twosteps_cheat.cpp")},
		{name: "output_only", fields: url.Values{"task_type": {"OutputOnly"}, "tt_output_pattern": {"out_%s.txt"}, "tt_checker": {"white_diff"}},
			tests: [][2]string{{"x\n", "1\n"}, {"y\n", "2\n"}}, zip: true,
			ac: zipFiles(map[string]string{"out_01.txt": "1\n", "out_02.txt": "2\n"}), wa: zipFiles(map[string]string{"out_01.txt": "1\n", "out_02.txt": "3\n"})},
	}
	dsRe := regexp.MustCompile(`href="/datasets/(\d+)"`)
	runRe := regexp.MustCompile(`/submissions/(\d+)$`)
	type pending struct {
		name, kind string
		id, ds     int64
	}
	var runs []pending
	for _, c := range cases {
		code, body = a.Post("/tasks", url.Values{"name": {c.name}, "title": {c.name}})
		webtest.MustOK(t, c.name+": create task", code, body)
		taskPath := strings.TrimPrefix(a.Last, s.awsURL)
		m := dsRe.FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("%s: no dataset link", c.name)
		}
		dsID, _ := strconv.ParseInt(m[1], 10, 64)
		if c.zip {
			// Output-only: one file per testcase, named after the pattern.
			code, body = a.Post(taskPath, url.Values{"name": {c.name}, "title": {c.name}, "submission_format": {""},
				"token_mode": {"disabled"}, "token_gen_interval_s": {"1800"}, "feedback_level": {"full"}, "score_mode": {"max_subtask"}})
			webtest.MustOK(t, c.name+": task format", code, body)
		}
		form := url.Values{"description": {"Default"}, "time_limit": {"1"}, "memory_limit_mib": {"256"}, "output_limit_mib": {"64"},
			"process_limit": {"1"}, "score_type": {"Sum"}, "score_type_params": {"50"}}
		for k, v := range c.fields {
			form[k] = v
		}
		code, body = a.Post(fmt.Sprintf("/datasets/%d", dsID), form)
		webtest.MustOK(t, c.name+": dataset", code, body)
		if len(c.managers) > 0 {
			var files []webtest.File
			for n, data := range c.managers {
				files = append(files, webtest.File{Field: "files", Name: n, Data: data})
			}
			code, body = a.PostMultipart(fmt.Sprintf("/datasets/%d/managers", dsID), nil, files...)
			webtest.MustOK(t, c.name+": managers", code, body)
		}
		tz := map[string]string{}
		for i, tc := range c.tests {
			tz[fmt.Sprintf("%02d.in", i+1)] = tc[0]
			tz[fmt.Sprintf("%02d.out", i+1)] = tc[1]
		}
		code, body = a.PostMultipart(fmt.Sprintf("/datasets/%d/testcases/archive", dsID), map[string]string{"input_template": "*.in", "output_template": "*.out"},
			webtest.File{Field: "archive", Name: "t.zip", Data: zipFiles(tz)})
		webtest.MustOK(t, c.name+": testcases", code, body)
		if strings.Contains(body, "Missing managers") || strings.Contains(body, `class="alert"`) {
			t.Fatalf("%s: dataset page reports a problem:\n%s", c.name, body)
		}
		for _, kind := range []string{"ac", "wa"} {
			src := c.ac
			if kind == "wa" {
				src = c.wa
			}
			fields := map[string]string{"language": c.lang}
			file := webtest.File{Field: c.file, Name: "solution", Data: src}
			if c.zip {
				fields, file = nil, webtest.File{Field: "zip", Name: "outputs.zip", Data: src}
			}
			code, body = a.PostMultipart(taskPath+"/tester", fields, file)
			webtest.MustOK(t, c.name+": tester "+kind, code, body)
			m := runRe.FindStringSubmatch(a.Last)
			if m == nil || !strings.Contains(body, "Task tester run") {
				t.Fatalf("%s: tester did not open the run page (%s)", c.name, a.Last)
			}
			id, _ := strconv.ParseInt(m[1], 10, 64)
			runs = append(runs, pending{c.name, kind, id, dsID})
		}
	}
	deadline := time.Now().Add(3 * time.Minute)
	for _, r := range runs {
		for {
			res, err := s.q.GetSubmissionResult(bg, sqlc.GetSubmissionResultParams{SubmissionID: r.id, DatasetID: r.ds})
			if err == nil && (res.ScoredAt != nil || res.SystemError != nil) {
				if res.SystemError != nil {
					t.Errorf("%s/%s: system error %s", r.name, r.kind, *res.SystemError)
				} else if r.kind == "ac" && *res.Score != 100 {
					t.Errorf("%s/ac: score %v, want 100", r.name, *res.Score)
				} else if r.kind == "wa" && *res.Score >= 100 {
					t.Errorf("%s/wa: score %v, want < 100", r.name, *res.Score)
				} else {
					t.Logf("%s/%s: score %v", r.name, r.kind, *res.Score)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s/%s not judged in time", r.name, r.kind)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	// Tester runs never count: no participation, no aggregate.
	var n int
	s.pool.QueryRow(bg, "SELECT count(*) FROM participation_task_scores").Scan(&n)
	if n != 0 {
		t.Fatalf("tester runs produced %d task scores", n)
	}
	code, body = a.Get(fmt.Sprintf("/submissions/%d", runs[0].id))
	if code != 200 || !strings.Contains(body, "Output is correct") {
		t.Fatalf("tester run page: %d", code)
	}
}
