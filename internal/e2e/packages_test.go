package e2e

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/auth"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/problempkg"
	"github.com/D4ND3R/Contest-Management-System/internal/webtest"
)

// drop removes a file in zipFolder's patch.
const drop = "\x00drop"

// zipFolder zips dir as "<name>/..." (as a folder is usually zipped),
// replacing, adding or dropping the files in patch.
func zipFolder(t *testing.T, dir string, patch map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	name := filepath.Base(dir)
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if _, ok := patch[rel]; ok {
			return nil
		}
		b, _ := os.ReadFile(p)
		w, _ := zw.Create(name + "/" + rel)
		w.Write(b)
		return nil
	})
	for rel, data := range patch {
		if data == drop {
			continue
		}
		w, _ := zw.Create(name + "/" + rel)
		w.Write([]byte(data))
	}
	zw.Close()
	return buf.Bytes()
}

var digestRe = regexp.MustCompile(`name="digest" value="([0-9a-f]{64})"`)

// TestProblemPackagesFromAdminUI imports the documented example of every
// problem type through the admin UI (preview, confirmation), lets the real
// judge run each package's reference solutions and checks the validation
// report; a broken checker is reported; the export imports back (K18–K21).
func TestProblemPackagesFromAdminUI(t *testing.T) {
	s := newStack(t, stackOpts{workers: true, admin: true, empty: true})
	hash, _ := auth.HashPassword("adminpass")
	s.q.CreateAdmin(bg, sqlc.CreateAdminParams{Name: "Admin", Username: "admin", PasswordHash: hash, Enabled: true, Role: "all"})
	now := time.Now()
	ct, err := s.q.CreateContest(bg, db.NewContestParams("pkgs", now.Add(-time.Hour), now.Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	a := webtest.New(t, s.awsURL)
	a.Get("/login")
	code, body := a.Post("/login", url.Values{"username": {"admin"}, "password": {"adminpass"}})
	webtest.MustOK(t, "admin login", code, body)

	importPkg := func(t *testing.T, data []byte, fields url.Values) (string, string) {
		t.Helper()
		form := map[string]string{"step": "preview", "run_solutions": "on"}
		for k := range fields {
			form[k] = fields.Get(k)
		}
		code, body := a.PostMultipart("/tasks/import", form, webtest.File{Field: "package", Name: "pkg.zip", Data: data})
		webtest.MustOK(t, "preview", code, body)
		m := digestRe.FindStringSubmatch(body)
		if m == nil || strings.Contains(body, "Problems to fix") {
			t.Fatalf("preview:\n%s", body)
		}
		v := url.Values{"step": {"confirm"}, "digest": {m[1]}, "run_solutions": {"on"}}
		for k, x := range fields {
			v[k] = x
		}
		code, body = a.Post("/tasks/import", v)
		webtest.MustOK(t, "confirm", code, body)
		return strings.TrimPrefix(a.Last, s.awsURL), body
	}
	// waitReport polls the validation report until nothing is pending.
	waitReport := func(t *testing.T, path string) string {
		t.Helper()
		deadline := time.Now().Add(4 * time.Minute)
		for {
			_, body := a.Get(path + "?fragment=1")
			if !strings.Contains(body, "still being judged") && strings.Contains(body, `id="validation"`) {
				return body
			}
			if time.Now().After(deadline) {
				t.Fatalf("validation never finished:\n%s", body)
			}
			time.Sleep(300 * time.Millisecond)
		}
	}

	examples := filepath.Join("..", "..", "docs", "examples", "packages")
	for _, name := range []string{"batch-suma", "interactive-adivina", "output-only-cuadrados", "communication-suma", "two-steps-binario"} {
		t.Run(name, func(t *testing.T) {
			path, _ := importPkg(t, zipFolder(t, filepath.Join(examples, name), nil), url.Values{"mode": {"task"}, "contest_id": {itoa(ct.ID)}})
			if !strings.HasSuffix(path, "/validation") {
				t.Fatalf("landed on %s", path)
			}
			body := waitReport(t, path)
			if !strings.Contains(body, "Every solution behaves as expected") {
				logEvaluations(t, s)
				t.Fatalf("report:\n%s", body)
			}
		})
	}

	// A package whose checker does not compile: the report says so.
	yaml := mustRead(t, filepath.Join(examples, "batch-suma", "problem.yaml"))
	yaml = strings.Replace(strings.Replace(yaml, "checker: white_diff", "checker: testlib", 1), "name: suma", "name: roto", 1)
	broken := zipFolder(t, filepath.Join(examples, "batch-suma"), map[string]string{"problem.yaml": yaml, "checker.cpp": "int main( {",
		"solutions/tle_lento.cpp": drop, "solutions/ac_suma.py": drop, "solutions/pa_int.c": drop, "solutions/wa_resta.c": drop})
	path, _ := importPkg(t, broken, url.Values{"mode": {"task"}})
	body = waitReport(t, path)
	if !strings.Contains(body, "does not compile") || !strings.Contains(body, "do not behave as expected") {
		t.Fatalf("broken checker report:\n%s", body)
	}

	// Export the batch task: it reads back cleanly, solutions included, and
	// imports as a new dataset of the same task.
	task, _ := s.q.GetTaskByName(bg, "suma")
	code, body = a.Get("/tasks/" + itoa(task.ID) + "/export.zip")
	if code != 200 {
		t.Fatalf("export = %d", code)
	}
	zr, err := zip.NewReader(strings.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	p := problempkg.Read(zr, problempkg.OptionsFor(s.langs))
	if !p.OK() || len(p.Solutions) != 5 || len(p.Tests) != 4 || p.MaxScore != 100 {
		t.Fatalf("exported package: %v, %d solutions", p.Errors, len(p.Solutions))
	}
	path, _ = importPkg(t, []byte(body), url.Values{"mode": {"dataset"}, "task_id": {itoa(task.ID)}})
	body = waitReport(t, path)
	if !strings.Contains(body, "Default (2)") || !strings.Contains(body, "Every solution behaves as expected") {
		t.Fatalf("second dataset report:\n%s", body)
	}
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

// logEvaluations logs every evaluation of the stack (diagnostics of a
// failed validation).
func logEvaluations(t *testing.T, s *stack) {
	rows, err := s.pool.Query(bg, `SELECT s.comment, e.testcase_id, e.outcome, e.text, e.exit_status, e.exit_code, e.signal,
		e.execution_time, e.execution_wall_time FROM evaluations e JOIN submissions s ON s.id = e.submission_id ORDER BY s.id, e.testcase_id`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var comment, text, status string
		var tc int64
		var outcome float64
		var code, sig *int32
		var cpu, wall *float64
		rows.Scan(&comment, &tc, &outcome, &text, &status, &code, &sig, &cpu, &wall)
		t.Logf("%s tc=%d outcome=%g status=%s code=%v signal=%v cpu=%v wall=%v text=%q", comment, tc, outcome, status,
			deref32(code), deref32(sig), derefF(cpu), derefF(wall), text)
	}
}

func deref32(p *int32) any {
	if p == nil {
		return nil
	}
	return *p
}

func derefF(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}
