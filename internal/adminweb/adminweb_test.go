package adminweb

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/auth"
	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/logging"
	"github.com/D4ND3R/Contest-Management-System/internal/testutil"
	"github.com/D4ND3R/Contest-Management-System/internal/webtest"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

var bg = context.Background()

type fixture struct {
	t       *testing.T
	url     string
	pool    *pgxpool.Pool
	q       *sqlc.Queries
	store   blob.Store
	admins  map[string]sqlc.Admin
	contest sqlc.Contest
	task    sqlc.Task
	ds      sqlc.Dataset
	user    sqlc.User
	part    sqlc.Participation
	subs    []int64
	team    sqlc.Team
	rdb     *redis.Client
	ns      string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := testutil.DB(t)
	rdb, ns := testutil.Redis(t)
	store, err := blob.NewLocal(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := langs.Load(filepath.Join("..", "..", "config", "languages"))
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, pool: pool, q: sqlc.New(pool), store: store, admins: map[string]sqlc.Admin{}, rdb: rdb, ns: ns}
	hash, _ := auth.HashPassword("password1")
	for _, role := range []string{"all", "messaging", "read_only"} {
		a, err := f.q.CreateAdmin(bg, sqlc.CreateAdminParams{Name: role, Username: "admin_" + role, PasswordHash: hash, Enabled: true, Role: role})
		if err != nil {
			t.Fatal(err)
		}
		f.admins[role] = a
	}
	f.seed()
	cfg := config.Default().AdminWeb
	cfg.LoginRateLimit = 1000
	srv, err := New(cfg, Deps{Pool: pool, Redis: rdb, Blobs: blob.NewTracked(store, f.q), Langs: reg,
		Secret: bytes.Repeat([]byte("a"), 32), NS: ns, RankingURL: "https://ranking.example.org"}, logging.Discard())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(bg)
	ready := make(chan net.Addr, 1)
	done := make(chan struct{})
	go func() { srv.Run(ctx, "127.0.0.1:0", ready); close(done) }()
	f.url = "http://" + (<-ready).String()
	t.Cleanup(func() { cancel(); <-done })
	return f
}

func (f *fixture) put(s string) string {
	info, err := f.store.PutBytes(bg, []byte(s))
	if err != nil {
		f.t.Fatal(err)
	}
	return info.Digest
}

// seed creates a contest with a scored submission and every kind of object.
func (f *fixture) seed() {
	t := f.t
	now := time.Now()
	var err error
	f.contest, err = f.q.CreateContest(bg, db.NewContestParams("seeded", now.Add(-time.Hour), now.Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	tp := db.NewTaskParams("sum", "Sum")
	tp.ContestID, tp.Num = &f.contest.ID, ptr(int32(0))
	f.task, _ = f.q.CreateTask(bg, tp)
	dp := db.NewDatasetParams(f.task.ID, "v1")
	dp.ScoreType, dp.ScoreTypeParams = "Sum", json.RawMessage(`50`)
	f.ds, _ = f.q.CreateDataset(bg, dp)
	f.q.SetActiveDataset(bg, sqlc.SetActiveDatasetParams{ID: f.task.ID, ActiveDatasetID: &f.ds.ID})
	var tcs []sqlc.Testcase
	for i := 0; i < 2; i++ {
		tc, err := f.q.UpsertTestcase(bg, sqlc.UpsertTestcaseParams{DatasetID: f.ds.ID, Codename: fmt.Sprint(i), Public: i == 0,
			InputDigest: f.put("1 2\n"), OutputDigest: f.put("3\n")})
		if err != nil {
			t.Fatal(err)
		}
		tcs = append(tcs, tc)
	}
	f.q.UpsertStatement(bg, sqlc.UpsertStatementParams{TaskID: f.task.ID, Language: "en", Digest: f.put("%PDF-1.4"), ContentType: "application/pdf"})
	f.q.UpsertAttachment(bg, sqlc.UpsertAttachmentParams{TaskID: f.task.ID, Filename: "sample.zip", Digest: f.put("zip")})
	f.q.UpsertManager(bg, sqlc.UpsertManagerParams{DatasetID: f.ds.ID, Filename: "checker.cpp", Digest: f.put("int main(){}")})
	f.team, _ = f.q.CreateTeam(bg, sqlc.CreateTeamParams{Code: "MEX", Name: "Mexico"})
	hash, _ := auth.HashPassword("pw")
	f.user, _ = f.q.CreateUser(bg, sqlc.CreateUserParams{Username: "ana", FirstName: "Ana", PasswordHash: hash, PreferredLanguages: []string{}})
	f.part, _ = f.q.CreateParticipation(bg, sqlc.CreateParticipationParams{ContestID: f.contest.ID, UserID: f.user.ID,
		TeamID: &f.team.ID, Ip: []netip.Prefix{}})
	lang := "c11"
	for i, src := range []string{"int main(){\n  return 0;\n}\n", "int main(){\n  int a;\n  return 0;\n}\n"} {
		sub, err := f.q.CreateSubmission(bg, sqlc.CreateSubmissionParams{ParticipationID: &f.part.ID, TaskID: f.task.ID,
			SubmittedAt: now.Add(time.Duration(i) * time.Minute), Language: &lang, Official: true})
		if err != nil {
			t.Fatal(err)
		}
		f.subs = append(f.subs, sub.ID)
		f.q.CreateSubmissionFiles(bg, []sqlc.CreateSubmissionFilesParams{{SubmissionID: sub.ID, Filename: "sum.%l", Digest: f.put(src)}})
		f.q.EnsureSubmissionResult(bg, sqlc.EnsureSubmissionResultParams{SubmissionID: sub.ID, DatasetID: f.ds.ID})
	}
	// The first submission is fully judged.
	sid := f.subs[0]
	ok := "ok"
	if _, err := f.q.SetCompilationResult(bg, sqlc.SetCompilationResultParams{SubmissionID: sid, DatasetID: f.ds.ID, Generation: 0,
		CompilationOutcome: &ok, CompilationText: "Compilation succeeded", TestcasesTotal: 2}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range tcs {
		f.q.UpsertEvaluation(bg, sqlc.UpsertEvaluationParams{SubmissionID: sid, DatasetID: f.ds.ID, TestcaseID: tc.ID, Outcome: 1,
			Text: "Output is correct", ExitStatus: "ok"})
	}
	score := 100.0
	details := json.RawMessage(`{"type":"sum","max_score":100,"testcases":[{"codename":"0","outcome":1,"text":"Output is correct"}]}`)
	f.q.SetScore(bg, sqlc.SetScoreParams{SubmissionID: sid, DatasetID: f.ds.ID, Score: &score, ScoreDetails: details,
		PublicScore: &score, PublicScoreDetails: details, RankingScoreDetails: json.RawMessage(`[100]`)})
	last := now
	f.q.UpsertParticipationTaskScore(bg, sqlc.UpsertParticipationTaskScoreParams{ParticipationID: f.part.ID, TaskID: f.task.ID,
		Score: 100, SubtaskScores: json.RawMessage(`[100]`), LastSubmissionAt: &last})
	f.q.InsertAuditLog(bg, sqlc.InsertAuditLogParams{Action: "seed", Details: json.RawMessage(`{}`)})
}

func ptr[T any](v T) *T { return &v }

func (f *fixture) login(role string) *webtest.Browser {
	f.t.Helper()
	b := webtest.New(f.t, f.url)
	code, body := b.Get("/login")
	webtest.MustOK(f.t, "login form", code, body)
	code, body = b.Post("/login", url.Values{"username": {"admin_" + role}, "password": {"password1"}})
	webtest.MustOK(f.t, "login "+role, code, body)
	if !strings.Contains(body, "admin_"+role) {
		f.t.Fatalf("not logged in as %s:\n%s", role, body)
	}
	return b
}

func TestLoginRolesAndAudit(t *testing.T) {
	f := newFixture(t)
	anon := webtest.New(t, f.url)
	if code, body := anon.Get("/contests"); code != 200 || !strings.Contains(body, `name="password"`) {
		t.Fatalf("anonymous access should redirect to login: %d", code)
	}
	anon.Get("/login")
	if code, _ := anon.Post("/login", url.Values{"username": {"admin_all"}, "password": {"wrong"}}); code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d", code)
	}

	ro := f.login("read_only")
	if code, _ := ro.Get("/contests/" + fmt.Sprint(f.contest.ID)); code != 200 {
		t.Fatalf("read-only GET = %d", code)
	}
	code, body := ro.Post("/contests", url.Values{"name": {"x"}, "start_time": {"2030-01-01T10:00"}, "stop_time": {"2030-01-01T15:00"}})
	if code != http.StatusForbidden || !strings.Contains(body, "role") {
		t.Fatalf("read-only POST = %d", code)
	}
	msg := f.login("messaging")
	if code, _ := msg.Post(fmt.Sprintf("/tasks/%d/delete", f.task.ID), url.Values{"confirm": {"sum"}}); code != http.StatusForbidden {
		t.Fatalf("messaging admin deleted a task: %d", code)
	}

	all := f.login("all")
	// CSRF is required.
	if code, _ := all.Post("/contests", url.Values{"csrf": {"forged"}, "name": {"x"}}); code != http.StatusForbidden {
		t.Fatalf("forged CSRF = %d", code)
	}
	code, body = all.Post("/contests", url.Values{"name": {"final"}, "description": {"Final round"}, "timezone": {"America/Mexico_City"},
		"start_time": {"2030-05-01T09:00"}, "stop_time": {"2030-05-01T14:00"}, "languages": {"c11", "cpp17"},
		"token_mode": {"disabled"}, "scoring_mode": {"ioi"}, "allow_password_authentication": {"on"}})
	webtest.MustOK(t, "create contest", code, body)
	c, err := f.q.GetContestByName(bg, "final")
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2030, 5, 1, 15, 0, 0, 0, time.UTC); !c.StartTime.Equal(want) {
		t.Fatalf("start %v, want %v (Mexico City is UTC-6)", c.StartTime, want)
	}
	if strings.Join(c.Languages, ",") != "c11,cpp17" || c.Description != "Final round" {
		t.Fatalf("contest %+v", c)
	}
	// Validation errors re-render the form (not audited).
	code, body = all.Post(fmt.Sprintf("/contests/%d", c.ID), url.Values{"name": {"final"}, "start_time": {"2030-05-01T09:00"},
		"stop_time": {"2030-05-01T08:00"}, "token_mode": {"disabled"}, "scoring_mode": {"ioi"}})
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "ends before it starts") {
		t.Fatalf("invalid update = %d", code)
	}

	rows, err := f.q.ListAuditLog(bg, sqlc.ListAuditLogParams{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	actions := map[string]int{}
	for _, r := range rows {
		actions[r.Action]++
		if r.Action == "contest.create" {
			if r.TargetID == nil || *r.TargetID != c.ID || r.AdminUsername == nil || *r.AdminUsername != "admin_all" {
				t.Fatalf("audit row %+v", r)
			}
			if strings.Contains(string(r.Details), "csrf") {
				t.Fatalf("csrf token in the audit log: %s", r.Details)
			}
		}
	}
	if actions["contest.create"] != 1 || actions["contest.update"] != 0 || actions["login"] != 3 || actions["login_failed"] != 1 {
		t.Fatalf("audit actions %v", actions)
	}
	code, body = all.Get("/audit")
	webtest.MustOK(t, "audit page", code, body)
	if !strings.Contains(body, "contest.create") {
		t.Fatal("audit page misses the action")
	}
}

var inlineRe = regexp.MustCompile(`(?i)<script(?:\s[^>]*)?>[^<]|\son[a-z]+\s*=|\sstyle\s*=|javascript:`)

func TestEveryPageRenders(t *testing.T) {
	f := newFixture(t)
	b := f.login("all")
	id := func(v int64) string { return fmt.Sprint(v) }
	c, tk, ds, u := id(f.contest.ID), id(f.task.ID), id(f.ds.ID), id(f.user.ID)
	tcs, _ := f.q.ListTestcases(bg, f.ds.ID)
	pages := []string{
		"/", "/contests", "/contests/new", "/contests/" + c, "/contests/" + c + "/participations",
		"/contests/" + c + "/submissions", "/contests/" + c + "/submissions?status=scored&task=" + tk + "&user=an&min=0&max=100",
		"/contests/" + c + "/ranking", "/contests/" + c + "/ranking?hidden=1", "/contests/" + c + "/stats",
		"/participations/" + id(f.part.ID), "/tasks", "/tasks/" + tk, "/datasets/" + ds,
		"/submissions/" + id(f.subs[0]), "/submissions/" + id(f.subs[1]),
		"/submissions/diff?a=" + id(f.subs[0]) + "&b=" + id(f.subs[1]),
		"/users", "/users?q=an", "/users/new", "/users/" + u, "/teams", "/teams/" + id(f.team.ID),
		"/admins", "/admins/" + id(f.admins["read_only"].ID), "/system", "/system/status", "/languages", "/audit",
	}
	for _, p := range pages {
		code, body := b.Get(p)
		webtest.MustOK(t, p, code, body)
		if m := inlineRe.FindString(body); m != "" {
			t.Errorf("%s contains inline code: %q", p, m)
		}
	}
	downloads := map[string]string{
		"/tasks/" + tk + "/statements/en":                "%PDF-1.4",
		"/tasks/" + tk + "/attachments/sample.zip":       "zip",
		"/datasets/" + ds + "/managers/checker.cpp":      "int main(){}",
		"/testcases/" + id(tcs[0].ID) + "/input":         "1 2\n",
		"/testcases/" + id(tcs[0].ID) + "/output":        "3\n",
		"/submissions/" + id(f.subs[0]) + "/files/sum.c": "return 0;",
		"/contests/" + c + "/ranking.csv":                "1,ana,Ana,,MEX,100,100",
	}
	for p, want := range downloads {
		code, body := b.Get(p)
		if code != 200 || !strings.Contains(body, want) {
			t.Errorf("%s = %d %q", p, code, body)
		}
	}
	code, body := b.Get("/contests/" + c + "/ranking.json")
	var rk struct {
		Rows []struct {
			Username string  `json:"username"`
			Total    float64 `json:"total"`
		} `json:"rows"`
	}
	if code != 200 || json.Unmarshal([]byte(body), &rk) != nil || len(rk.Rows) != 1 || rk.Rows[0].Total != 100 {
		t.Fatalf("ranking.json = %d %s", code, body)
	}
	_, body = b.Get("/submissions/diff?a=" + id(f.subs[0]) + "&b=" + id(f.subs[1]))
	if !strings.Contains(body, `class="ins"`) || !strings.Contains(body, "int a;") {
		t.Fatalf("diff does not show the inserted line:\n%s", body)
	}
	resp, err := http.Get(f.url + "/login")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-inline") {
		t.Fatalf("CSP %q", csp)
	}
}

func zipOf(t *testing.T, files map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(data))
	}
	zw.Close()
	return buf.Bytes()
}

func TestTaskAndDatasetManagement(t *testing.T) {
	f := newFixture(t)
	b := f.login("all")
	code, body := b.Post("/tasks", url.Values{"name": {"paths"}, "title": {"Paths"}, "contest_id": {fmt.Sprint(f.contest.ID)}})
	webtest.MustOK(t, "create task", code, body)
	task, err := f.q.GetTaskByName(bg, "paths")
	if err != nil || task.ContestID == nil || task.ActiveDatasetID == nil || task.Num == nil || *task.Num != 1 {
		t.Fatalf("task %+v %v", task, err)
	}
	dsID := *task.ActiveDatasetID
	dsPath := fmt.Sprintf("/datasets/%d", dsID)

	// Invalid task type parameters are rejected with a message.
	code, body = b.Post(dsPath, url.Values{"description": {"Default"}, "time_limit": {"1.5"}, "memory_limit_mib": {"256"},
		"process_limit": {"1"}, "task_type": {"Batch"}, "tt_checker": {"magic"}, "score_type": {"Sum"}, "score_type_params": {"10"}})
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "unknown checker") {
		t.Fatalf("invalid params = %d", code)
	}
	code, body = b.Post(dsPath, url.Values{"description": {"Default"}, "time_limit": {"1.5"}, "memory_limit_mib": {"128"},
		"process_limit": {"1"}, "source_size_limit_kib": {"64"}, "task_type": {"Batch"},
		"tt_input_file": {"paths.in"}, "tt_output_file": {"paths.out"}, "tt_checker": {"white_diff"}, "score_type": {"GroupMin"},
		"score_type_params": {`[[40, "a.*"], [60, "b.*"]]`}})
	webtest.MustOK(t, "save dataset", code, body)
	// Raw JSON parameters (advanced) are validated as well.
	code, body = b.Post(dsPath, url.Values{"description": {"Default"}, "time_limit": {"1"}, "memory_limit_mib": {"64"},
		"process_limit": {"1"}, "task_type": {"Communication"}, "raw_params": {"on"}, "task_type_params": {`{"num_processes": 9}`},
		"score_type": {"Sum"}, "score_type_params": {"10"}})
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "num_processes") {
		t.Fatalf("invalid raw params = %d", code)
	}
	ds, _ := f.q.GetDataset(bg, dsID)
	if *ds.TimeLimitMs != 1500 || *ds.MemoryLimitBytes != 128<<20 || *ds.SourceSizeLimitBytes != 64<<10 || ds.ScoreType != "GroupMin" ||
		!strings.Contains(string(ds.TaskTypeParams), `"input_file": "paths.in"`) {
		t.Fatalf("dataset %+v", ds)
	}

	archive := zipOf(t, map[string]string{"tests/a1.in": "1\n", "tests/a1.out": "1\n", "tests/a2.in": "2\n", "tests/a2.out": "2\n",
		"tests/b1.in": "3\n", "tests/b1.out": "3\n", "tests/README": "x", "tests/c1.in": "orphan"})
	code, body = b.PostMultipart(dsPath+"/testcases/archive", map[string]string{"input_template": "*.in", "output_template": "*.out", "public": "on"},
		webtest.File{Field: "archive", Name: "tests.zip", Data: archive})
	webtest.MustOK(t, "import archive", code, body)
	if !strings.Contains(body, "Imported 3 testcases") || !strings.Contains(body, "Maximum score <b>100</b>") {
		t.Fatalf("after import:\n%s", body)
	}
	// Re-import without overwrite keeps the existing ones.
	code, body = b.PostMultipart(dsPath+"/testcases/archive", map[string]string{"input_template": "*.in", "output_template": "*.out"},
		webtest.File{Field: "archive", Name: "tests.zip", Data: archive})
	if code != 200 || !strings.Contains(body, "3 existing testcases were kept") {
		t.Fatalf("re-import: %d", code)
	}
	tcs, _ := f.q.ListTestcases(bg, dsID)
	if len(tcs) != 3 || !tcs[0].Public {
		t.Fatalf("testcases %+v", tcs)
	}
	code, _ = b.Post(fmt.Sprintf("/testcases/%d/public", tcs[0].ID), url.Values{"public": {"0"}})
	if tc, _ := f.q.GetTestcase(bg, tcs[0].ID); code != 200 || tc.Public {
		t.Fatalf("public toggle: %d %v", code, tc.Public)
	}
	code, body = b.PostMultipart(dsPath+"/testcases", map[string]string{"codename": "b2"},
		webtest.File{Field: "input", Name: "in", Data: []byte("4\n")}, webtest.File{Field: "output", Name: "out", Data: []byte("4\n")})
	webtest.MustOK(t, "single testcase", code, body)
	code, body = b.PostMultipart(dsPath+"/managers", nil,
		webtest.File{Field: "files", Name: "checker.cpp", Data: []byte("// checker")}, webtest.File{Field: "files", Name: "grader.h", Data: []byte("//")})
	webtest.MustOK(t, "managers", code, body)
	if ms, _ := f.q.ListManagers(bg, dsID); len(ms) != 2 {
		t.Fatalf("managers %+v", ms)
	}
	code, body = b.PostMultipart(fmt.Sprintf("/tasks/%d/statements", task.ID), map[string]string{"language": "es", "primary": "on"},
		webtest.File{Field: "file", Name: "enunciado.pdf", Data: []byte("%PDF-1.7 es")})
	webtest.MustOK(t, "statement", code, body)
	if tk, _ := f.q.GetTask(bg, task.ID); strings.Join(tk.PrimaryStatements, ",") != "es" {
		t.Fatalf("primary statements %v", tk.PrimaryStatements)
	}
	// Clone the dataset, make the clone live, delete the old one.
	code, body = b.Post(fmt.Sprintf("/tasks/%d/datasets", task.ID), url.Values{"description": {"v2"}, "clone_from": {fmt.Sprint(dsID)}})
	webtest.MustOK(t, "clone dataset", code, body)
	all, _ := f.q.ListDatasetsByTask(bg, task.ID)
	clone := all[len(all)-1]
	if n, _ := f.q.CountTestcases(bg, clone.ID); n != 4 || clone.ScoreType != "GroupMin" {
		t.Fatalf("clone %+v with %d testcases", clone, n)
	}
	code, _ = b.Post(fmt.Sprintf("/datasets/%d/activate", clone.ID), nil)
	if tk, _ := f.q.GetTask(bg, task.ID); code != 200 || *tk.ActiveDatasetID != clone.ID {
		t.Fatalf("activate: %d", code)
	}
	code, _ = b.Post(fmt.Sprintf("/datasets/%d/delete", clone.ID), nil)
	if code != http.StatusConflict {
		t.Fatalf("deleting the live dataset = %d", code)
	}
	code, body = b.Post(fmt.Sprintf("/datasets/%d/delete", dsID), nil)
	webtest.MustOK(t, "delete dataset", code, body)
	// Reorder tasks: paths moves above sum.
	code, _ = b.Post(fmt.Sprintf("/contests/%d/tasks/%d/move", f.contest.ID, task.ID), url.Values{"dir": {"up"}})
	tasks, _ := f.q.ListTasksByContest(bg, &f.contest.ID)
	if code != 200 || tasks[0].Name != "paths" || tasks[1].Name != "sum" {
		t.Fatalf("order after move: %d %s,%s", code, tasks[0].Name, tasks[1].Name)
	}
	// Deleting needs the name typed.
	if code, _ := b.Post(fmt.Sprintf("/tasks/%d/delete", task.ID), url.Values{"confirm": {"nope"}}); code != http.StatusUnprocessableEntity {
		t.Fatalf("unconfirmed delete = %d", code)
	}
	code, body = b.Post(fmt.Sprintf("/tasks/%d/delete", task.ID), url.Values{"confirm": {"paths"}})
	webtest.MustOK(t, "delete task", code, body)
}

var digestRe = regexp.MustCompile(`name="digest" value="([0-9a-f]{64})"`)

// importCSV runs the preview and, when it has no errors, the confirmation.
func importCSV(t *testing.T, b *webtest.Browser, fields map[string]string, data string) (int, string, string) {
	t.Helper()
	code, preview := b.PostMultipart("/users/import", fields, webtest.File{Field: "file", Name: "users.csv", Data: []byte(data)})
	if code != 200 {
		return code, preview, preview
	}
	m := digestRe.FindStringSubmatch(preview)
	if m == nil {
		return code, preview, preview
	}
	form := url.Values{"step": {"confirm"}, "digest": {m[1]}}
	for k, v := range fields {
		form.Set(k, v)
	}
	code, body := b.Post("/users/import", form)
	return code, preview, body
}

func TestUserImportAndParticipations(t *testing.T) {
	f := newFixture(t)
	b := f.login("all")
	f.q.CreateSite(bg, sqlc.CreateSiteParams{ContestID: f.contest.ID, Name: "Norte"})
	bad := "username,password,team\nbeto,,XXX\ncarla,pw\ncarla,pw2\n"
	code, preview, _ := importCSV(t, b, map[string]string{"contest_id": fmt.Sprint(f.contest.ID)}, bad)
	if code != 200 || !strings.Contains(preview, "unknown team") || !strings.Contains(preview, "repeated") || strings.Contains(preview, `name="step"`) {
		t.Fatalf("bad import preview = %d\n%s", code, preview)
	}
	if _, err := f.q.GetUserByUsername(bg, "carla"); err == nil {
		t.Fatal("a failed import created users")
	}
	good := "\ufeffusername,first_name,last_name,password,team,hidden,ip,extra_time,institution,country,site\nbeto,Beto,Ruiz,,MEX,,10.0.0.5,600,Prepa 1,MX,Norte\ncarla,Carla,Paz,secret123,,yes,,,,,\n"
	code, preview, body := importCSV(t, b, map[string]string{"contest_id": fmt.Sprint(f.contest.ID), "generate": "on"}, good)
	webtest.MustOK(t, "import", code, body)
	if !strings.Contains(preview, "2 valid rows") || !strings.Contains(preview, "Import 2 users") {
		t.Fatalf("preview:\n%s", preview)
	}
	if !strings.Contains(body, "2 users created") || !strings.Contains(body, "2 participations added") {
		t.Fatalf("import result:\n%s", body)
	}
	m := regexp.MustCompile(`<td>beto</td><td><code>([a-z0-9]{10})</code>`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no generated password shown:\n%s", body)
	}
	beto, _ := f.q.GetUserByUsername(bg, "beto")
	if auth.VerifyPassword(beto.PasswordHash, m[1]) != nil || beto.LastName != "Ruiz" || beto.Institution != "Prepa 1" || beto.Country != "MX" {
		t.Fatalf("imported user %+v", beto)
	}
	carla, _ := f.q.GetUserByUsername(bg, "carla")
	if auth.VerifyPassword(carla.PasswordHash, "secret123") != nil {
		t.Fatal("carla's password")
	}
	pb, _ := f.q.GetParticipationByContestUser(bg, sqlc.GetParticipationByContestUserParams{ContestID: f.contest.ID, UserID: beto.ID})
	pc, _ := f.q.GetParticipationByContestUser(bg, sqlc.GetParticipationByContestUserParams{ContestID: f.contest.ID, UserID: carla.ID})
	if pb.TeamID == nil || *pb.TeamID != f.team.ID || pb.ExtraTimeS != 600 || len(pb.Ip) != 1 || pb.Ip[0].String() != "10.0.0.5/32" || !pc.Hidden || pb.SiteID == nil {
		t.Fatalf("participations %+v %+v", pb, pc)
	}
	// The credentials sheet is a PDF built from the posted credentials.
	resp := postRaw(t, b, "/credentials.pdf", url.Values{"credentials": {"username,password,name\nbeto,abc,Beto Ruiz\n"}, "per_page": {"4"}, "title": {"OMI"}})
	if !strings.HasPrefix(resp, "%PDF-") {
		t.Fatalf("credentials PDF: %.40q", resp)
	}
	// Export in the import format.
	code, body = b.Get(fmt.Sprintf("/users/export.csv?contest=%d", f.contest.ID))
	if code != 200 || !strings.Contains(body, "beto,Beto,Ruiz,,Prepa 1,MX,,,MEX,Norte,,,10.0.0.5,0,600") {
		t.Fatalf("export:\n%s", body)
	}
	// Existing users need "update".
	code, preview, _ = importCSV(t, b, nil, "username,password\nbeto,x\n")
	if code != 200 || !strings.Contains(preview, "the user exists") {
		t.Fatalf("re-import without update = %d", code)
	}
	code, _, body = importCSV(t, b, map[string]string{"update": "on"}, "username,password,first_name\nbeto,newpass1,Roberto\n")
	webtest.MustOK(t, "update import", code, body)
	beto, _ = f.q.GetUserByUsername(bg, "beto")
	if auth.VerifyPassword(beto.PasswordHash, "newpass1") != nil || beto.FirstName != "Roberto" {
		t.Fatal("update import did not apply")
	}

	// Participation edit: password, IPs, extra time, logout.
	code, body = b.Post(fmt.Sprintf("/participations/%d", pb.ID), url.Values{"team_id": {""}, "ip": {"192.168.1.0/24, 10.0.0.9"},
		"delay_time_s": {"0"}, "extra_time_s": {"300"}, "participation_password": {"contestpw"}, "logout": {"on"}})
	webtest.MustOK(t, "participation update", code, body)
	pb2, _ := f.q.GetParticipation(bg, pb.ID)
	if pb2.TeamID != nil || len(pb2.Ip) != 2 || pb2.ExtraTimeS != 300 || pb2.PasswordHash == nil || pb2.LoginNonce != pb.LoginNonce+1 {
		t.Fatalf("participation %+v", pb2)
	}
	// Add an existing user by name; unknown names fail atomically.
	u, _ := f.q.CreateUser(bg, sqlc.CreateUserParams{Username: "dora", PasswordHash: "x", PreferredLanguages: []string{}})
	if code, _ := b.Post(fmt.Sprintf("/contests/%d/participations", f.contest.ID), url.Values{"usernames": {"dora nobody"}}); code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown username = %d", code)
	}
	code, body = b.Post(fmt.Sprintf("/contests/%d/participations", f.contest.ID), url.Values{"usernames": {"dora\nana"}, "unrestricted": {"on"}})
	webtest.MustOK(t, "add participations", code, body)
	pd, err := f.q.GetParticipationByContestUser(bg, sqlc.GetParticipationByContestUserParams{ContestID: f.contest.ID, UserID: u.ID})
	if err != nil || !pd.Unrestricted || !strings.Contains(body, "1 participations added") {
		t.Fatalf("dora %+v %v", pd, err)
	}
}

func TestReevaluateFromUI(t *testing.T) {
	f := newFixture(t)
	b := f.login("all")
	before, _ := f.q.GetSubmissionResult(bg, sqlc.GetSubmissionResultParams{SubmissionID: f.subs[0], DatasetID: f.ds.ID})
	code, body := b.Post("/reevaluate", url.Values{"submission_id": {fmt.Sprint(f.subs[0])}, "level": {"reevaluate"},
		"back": {fmt.Sprintf("/submissions/%d", f.subs[0])}})
	webtest.MustOK(t, "reevaluate", code, body)
	if !strings.Contains(body, "1 results scheduled for reevaluation") {
		t.Fatalf("flash missing:\n%s", body)
	}
	after, _ := f.q.GetSubmissionResult(bg, sqlc.GetSubmissionResultParams{SubmissionID: f.subs[0], DatasetID: f.ds.ID})
	if after.Generation != before.Generation+1 || after.ScoredAt != nil || after.CompilationOutcome == nil {
		t.Fatalf("result after reevaluation %+v", after)
	}
	// An open redirect through "back" is not possible.
	code, _ = b.Post("/reevaluate", url.Values{"task_id": {fmt.Sprint(f.task.ID)}, "level": {"rescore"}, "back": {"//evil.example/"}})
	if code != 200 || !strings.HasPrefix(b.Last, f.url+"/") {
		t.Fatalf("redirected to %s", b.Last)
	}
}

func TestAdministratorSafety(t *testing.T) {
	f := newFixture(t)
	b := f.login("all")
	me := f.admins["all"]
	if code, _ := b.Post(fmt.Sprintf("/admins/%d/delete", me.ID), nil); code != http.StatusConflict {
		t.Fatalf("self-delete = %d", code)
	}
	code, body := b.Post(fmt.Sprintf("/admins/%d", me.ID), url.Values{"username": {me.Username}, "role": {"read_only"}, "enabled": {"on"}})
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "must remain") {
		t.Fatalf("demoting the last full admin = %d", code)
	}
	code, body = b.Post("/admins", url.Values{"username": {"second"}, "password": {"longpassword"}, "role": {"all"}})
	webtest.MustOK(t, "create admin", code, body)
	// Disabling an admin ends their sessions.
	ro := f.login("read_only")
	roID := f.admins["read_only"].ID
	code, _ = b.Post(fmt.Sprintf("/admins/%d", roID), url.Values{"username": {"admin_read_only"}, "role": {"read_only"}})
	if code != 200 {
		t.Fatalf("disable = %d", code)
	}
	time.Sleep(3100 * time.Millisecond) // admin cache TTL
	if _, body := ro.Get("/contests"); !strings.Contains(body, `name="password"`) {
		t.Fatal("a disabled admin still has access")
	}
}

func TestLineDiff(t *testing.T) {
	a := strings.Split("a b c d e f", " ")
	b := strings.Split("a x c d f g", " ")
	var got []string
	for _, l := range lineDiff(a, b, 100) {
		got = append(got, l.Op+l.Text)
	}
	want := " a,-b,+x, c, d,-e, f,+g"
	if strings.Join(got, ",") != want {
		t.Fatalf("diff = %s, want %s", strings.Join(got, ","), want)
	}
	// Line numbers of the common suffix are kept.
	ls := lineDiff([]string{"x", "1", "2"}, []string{"1", "2"}, 100)
	if ls[0].Op != "-" || ls[1].A != 2 || ls[1].B != 1 || ls[2].A != 3 || ls[2].B != 2 {
		t.Fatalf("%+v", ls)
	}
	// Beyond maxD the rest is a replacement.
	if ls := lineDiff([]string{"a", "b"}, []string{"c", "d"}, 1); len(ls) != 4 {
		t.Fatalf("bounded diff %+v", ls)
	}
}

func TestTemplateRe(t *testing.T) {
	re, err := templateRe("input.*.txt")
	if err != nil {
		t.Fatal(err)
	}
	if m := re.FindStringSubmatch("input.07.txt"); m == nil || m[1] != "07" {
		t.Fatalf("%v", m)
	}
	if _, err := templateRe("noglob"); err == nil {
		t.Fatal("template without * accepted")
	}
}

// postRaw posts a form and returns the raw body (downloads).
func postRaw(t *testing.T, b *webtest.Browser, path string, form url.Values) string {
	t.Helper()
	form.Set("csrf", b.CSRF)
	resp, err := b.C.PostForm(b.Base+path, form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return string(data)
}
