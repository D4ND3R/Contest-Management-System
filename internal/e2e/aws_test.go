package e2e

import (
	"archive/zip"
	"bytes"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/auth"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/webtest"
)

// TestSetUpContestFromAdminUI is the F6 exit criterion: starting from an
// empty system with one administrator (created by `cmsctl bootstrap`), a
// complete contest is set up only through the admin web server — contest,
// task, statement, dataset limits and scoring, testcases, team, users with
// generated passwords — and a contestant then logs in and is judged.
func TestSetUpContestFromAdminUI(t *testing.T) {
	s := newStack(t, stackOpts{workers: true, admin: true, empty: true})
	hash, _ := auth.HashPassword("adminpass")
	if _, err := s.q.CreateAdmin(bg, sqlc.CreateAdminParams{Name: "Admin", Username: "admin", PasswordHash: hash, Enabled: true, Role: "all"}); err != nil {
		t.Fatal(err)
	}
	a := webtest.New(t, s.awsURL)
	a.Get("/login")
	code, body := a.Post("/login", url.Values{"username": {"admin"}, "password": {"adminpass"}})
	webtest.MustOK(t, "admin login", code, body)

	// 1. Contest (running now, UTC).
	now := time.Now().UTC()
	code, body = a.Post("/contests", url.Values{"name": {"omi"}, "description": {"OMI 2026"}, "timezone": {"UTC"},
		"start_time": {now.Add(-time.Hour).Format("2006-01-02T15:04:05")}, "stop_time": {now.Add(4 * time.Hour).Format("2006-01-02T15:04:05")},
		"languages": {"c11", "cpp17", "python3"}, "allow_password_authentication": {"on"}, "allow_questions": {"on"},
		"submissions_download_allowed": {"on"}, "token_mode": {"disabled"}, "token_gen_interval_s": {"1800"},
		"scoring_mode": {"ioi"}, "score_precision": {"0"}})
	webtest.MustOK(t, "create contest", code, body)
	contestPath := strings.TrimPrefix(a.Last, s.awsURL)
	contestID := strings.TrimPrefix(contestPath, "/contests/")

	// 2. Task in the contest (it gets a default live dataset).
	code, body = a.Post("/tasks", url.Values{"name": {"sum"}, "title": {"Suma"}, "contest_id": {contestID}})
	webtest.MustOK(t, "create task", code, body)
	taskPath := strings.TrimPrefix(a.Last, s.awsURL)
	m := regexp.MustCompile(`href="/datasets/(\d+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no dataset link on the task page:\n%s", body)
	}
	dsPath := "/datasets/" + m[1]

	// 3. Statement.
	code, body = a.PostMultipart(taskPath+"/statements", map[string]string{"language": "es", "primary": "on"},
		webtest.File{Field: "file", Name: "suma.pdf", Data: []byte("%PDF-1.4 Suma de dos enteros")})
	webtest.MustOK(t, "statement", code, body)

	// 4. Dataset: limits and scoring, then the testcases from a zip.
	code, body = a.Post(dsPath, url.Values{"description": {"Default"}, "time_limit": {"1"}, "memory_limit_mib": {"256"},
		"output_limit_mib": {"64"}, "process_limit": {"1"}, "task_type": {"Batch"}, "task_type_params": {`{"checker": "white_diff"}`},
		"score_type": {"Sum"}, "score_type_params": {"25"}})
	webtest.MustOK(t, "dataset", code, body)
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	for i, c := range [][2]string{{"1 2", "3"}, {"5 5", "10"}, {"-1 1", "0"}, {"100 23", "123"}} {
		for ext, data := range map[string]string{".in": c[0] + "\n", ".out": c[1] + "\n"} {
			w, _ := zw.Create(fmt.Sprintf("%02d%s", i+1, ext))
			w.Write([]byte(data))
		}
	}
	zw.Close()
	code, body = a.PostMultipart(dsPath+"/testcases/archive", map[string]string{"input_template": "*.in", "output_template": "*.out", "public": "on"},
		webtest.File{Field: "archive", Name: "tests.zip", Data: zbuf.Bytes()})
	webtest.MustOK(t, "testcases", code, body)
	if !strings.Contains(body, "Imported 4 testcases") || !strings.Contains(body, "Maximum score <b>100</b>") {
		t.Fatalf("dataset page after import:\n%s", body)
	}

	// 5. Team and users (CSV, generated passwords) in the contest.
	code, body = a.PostMultipart("/teams", map[string]string{"code": "JAL", "name": "Jalisco"})
	webtest.MustOK(t, "team", code, body)
	csv := "username,first_name,last_name,team\nlucia,Lucía,Gómez,JAL\nmario,Mario,Pérez,JAL\n"
	code, body = a.PostMultipart("/users/import", map[string]string{"contest_id": contestID, "generate": "on"},
		webtest.File{Field: "file", Name: "users.csv", Data: []byte(csv)})
	webtest.MustOK(t, "import preview", code, body)
	dg := regexp.MustCompile(`name="digest" value="([0-9a-f]{64})"`).FindStringSubmatch(body)
	if dg == nil {
		t.Fatalf("no import confirmation:\n%s", body)
	}
	code, body = a.Post("/users/import", url.Values{"step": {"confirm"}, "digest": {dg[1]}, "contest_id": {contestID}, "generate": {"on"}})
	webtest.MustOK(t, "import users", code, body)
	pw := regexp.MustCompile(`<td>lucia</td><td><code>([a-z0-9]+)</code>`).FindStringSubmatch(body)
	if pw == nil {
		t.Fatalf("no generated password:\n%s", body)
	}

	// The contestant logs in with the generated password and is judged.
	c := webtest.New(t, s.cwsURL)
	c.Get("/omi/login")
	code, body = c.Post("/omi/login", url.Values{"username": {"lucia"}, "password": {pw[1]}})
	webtest.MustOK(t, "contestant login", code, body)
	code, body = c.Get("/omi/tasks/sum")
	webtest.MustOK(t, "task page", code, body)
	if !strings.Contains(body, "Suma") || !strings.Contains(body, "/omi/tasks/sum/statement/es") {
		t.Fatalf("task page lacks the statement:\n%s", body)
	}
	src := "#include <stdio.h>\nint main(void){long a,b;scanf(\"%ld %ld\",&a,&b);printf(\"%ld\\n\",a+b);return 0;}\n"
	code, body = c.PostMultipart("/omi/tasks/sum/submit", map[string]string{"language": "c11"},
		webtest.File{Field: "sum.%l", Name: "sum.c", Data: []byte(src)})
	webtest.MustOK(t, "submit", code, body)
	deadline := time.Now().Add(60 * time.Second)
	for {
		_, body = c.Get("/omi/tasks/sum")
		if strings.Contains(body, "100 / 100") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("submission not scored:\n%s", body)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// The administrator sees it everywhere.
	code, body = a.Get(contestPath + "/submissions")
	webtest.MustOK(t, "admin submissions", code, body)
	if !strings.Contains(body, "lucia") || !strings.Contains(body, `<td class="num">100</td>`) {
		t.Fatalf("admin submission list:\n%s", body)
	}
	code, body = a.Get(contestPath + "/ranking.csv")
	if code != 200 || !strings.Contains(body, "1,lucia,Lucía,Gómez,JAL,100,100") || !strings.Contains(body, "2,mario,Mario,Pérez,JAL,0,0") {
		t.Fatalf("ranking.csv = %d\n%s", code, body)
	}
	code, body = a.Get(contestPath + "/stats")
	webtest.MustOK(t, "stats", code, body)
	if !regexp.MustCompile(`<b>1</b><span>full score</span>`).MatchString(body) {
		t.Fatalf("stats:\n%s", body)
	}
	rows, _ := s.q.ListAuditLog(bg, sqlc.ListAuditLogParams{Limit: 100})
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.Action] = true
	}
	for _, act := range []string{"contest.create", "task.create", "statement.upload", "dataset.update", "testcase.upload_archive", "team.create", "user.import"} {
		if !seen[act] {
			t.Errorf("audit log misses %s", act)
		}
	}
}
