package contestweb

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
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
	"github.com/D4ND3R/Contest-Management-System/internal/events"
	"github.com/D4ND3R/Contest-Management-System/internal/i18n"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/logging"
	"github.com/D4ND3R/Contest-Management-System/internal/testutil"
	"github.com/D4ND3R/Contest-Management-System/web"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

var bg = context.Background()

type fixture struct {
	t       *testing.T
	srv     *Server
	url     string
	pool    *pgxpool.Pool
	q       *sqlc.Queries
	rdb     *redis.Client
	ns      string
	store   blob.Store
	contest sqlc.Contest
	task    sqlc.Task
	ds      sqlc.Dataset
	user    sqlc.User
	part    sqlc.Participation
}

type fixtureOpts struct {
	perUserTime int64
	singleLogin bool
	ipRestrict  bool
	ipAutologin bool
	ips         []netip.Prefix
	maxSubs     *int32
	trusted     []string
}

func newFixture(t *testing.T, o fixtureOpts) *fixture {
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
	f := &fixture{t: t, pool: pool, q: sqlc.New(pool), rdb: rdb, ns: ns, store: store}
	now := time.Now()
	cp := db.NewContestParams("ioi", now.Add(-time.Hour), now.Add(2*time.Hour))
	cp.Languages = []string{"c11", "cpp17", "python3"}
	cp.SingleLogin, cp.IpRestriction, cp.IpAutologin = o.singleLogin, o.ipRestrict, o.ipAutologin
	cp.MaxSubmissionNumber = o.maxSubs
	if o.perUserTime > 0 {
		cp.PerUserTimeS = &o.perUserTime
	}
	f.contest, err = f.q.CreateContest(bg, cp)
	if err != nil {
		t.Fatal(err)
	}
	tp := db.NewTaskParams("sum", "Suma")
	tp.ContestID, tp.PrimaryStatements = &f.contest.ID, []string{"es"}
	f.task, err = f.q.CreateTask(bg, tp)
	if err != nil {
		t.Fatal(err)
	}
	dp := db.NewDatasetParams(f.task.ID, "v1")
	dp.ScoreType, dp.ScoreTypeParams = "Sum", json.RawMessage(`50`)
	f.ds, _ = f.q.CreateDataset(bg, dp)
	f.q.SetActiveDataset(bg, sqlc.SetActiveDatasetParams{ID: f.task.ID, ActiveDatasetID: &f.ds.ID})
	for i := 0; i < 2; i++ {
		in, _ := store.PutBytes(bg, []byte("1 2\n"))
		out, _ := store.PutBytes(bg, []byte("3\n"))
		f.q.UpsertTestcase(bg, sqlc.UpsertTestcaseParams{DatasetID: f.ds.ID, Codename: fmt.Sprint(i), InputDigest: in.Digest, OutputDigest: out.Digest})
	}
	pdf, _ := store.PutBytes(bg, []byte("%PDF-1.4 statement"))
	f.q.UpsertStatement(bg, sqlc.UpsertStatementParams{TaskID: f.task.ID, Language: "es", Digest: pdf.Digest, ContentType: "application/pdf"})
	hash, _ := auth.HashPassword("secret")
	f.user, _ = f.q.CreateUser(bg, sqlc.CreateUserParams{Username: "ana", PasswordHash: hash, PreferredLanguages: []string{}})
	ips := o.ips
	if ips == nil {
		ips = []netip.Prefix{}
	}
	f.part, _ = f.q.CreateParticipation(bg, sqlc.CreateParticipationParams{ContestID: f.contest.ID, UserID: f.user.ID, Ip: ips})

	cfg := config.Default().ContestWeb
	cfg.RateLimitPerMinute, cfg.LoginRateLimit, cfg.TrustedProxies = 1000, 1000, o.trusted
	f.srv, err = New(cfg, Deps{Pool: pool, Redis: rdb, Blobs: store, Langs: reg, Secret: bytes.Repeat([]byte("k"), 32), NS: ns}, logging.Discard())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(bg)
	ready := make(chan net.Addr, 1)
	done := make(chan struct{})
	go func() { f.srv.Run(ctx, "127.0.0.1:0", ready); close(done) }()
	f.url = "http://" + (<-ready).String()
	t.Cleanup(func() { cancel(); <-done })
	time.Sleep(50 * time.Millisecond) // event subscription
	return f
}

func (f *fixture) client() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}
}

var csrfRe = regexp.MustCompile(`name="csrf-token" content="([^"]+)"`)

func (f *fixture) get(c *http.Client, path string, hdr ...string) (int, string) {
	f.t.Helper()
	req, _ := http.NewRequest("GET", f.url+path, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := c.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func csrfOf(t *testing.T, html string) string {
	m := csrfRe.FindStringSubmatch(html)
	if m == nil {
		t.Fatalf("no csrf token in page")
	}
	return m[1]
}

func (f *fixture) login(c *http.Client, user, pass string) (int, string) {
	f.t.Helper()
	_, page := f.get(c, "/ioi/login")
	resp, err := c.PostForm(f.url+"/ioi/login", url.Values{"csrf": {csrfOf(f.t, page)}, "username": {user}, "password": {pass}})
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (f *fixture) submit(c *http.Client, csrf, lang, src string, htmx bool) (int, string) {
	f.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("csrf", csrf)
	mw.WriteField("language", lang)
	fw, _ := mw.CreateFormFile("sum.%l", "sum.c")
	fw.Write([]byte(src))
	mw.Close()
	req, _ := http.NewRequest("POST", f.url+"/ioi/tasks/sum/submit", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	resp, err := c.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestLoginAndPages(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	c := f.client()
	if code, body := f.get(c, "/ioi/"); code != 200 || !strings.Contains(body, `name="password"`) {
		t.Fatalf("anonymous overview must show the login form, got %d", code)
	}
	// Missing CSRF token.
	resp, _ := c.PostForm(f.url+"/ioi/login", url.Values{"username": {"ana"}, "password": {"secret"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("login without CSRF = %d", resp.StatusCode)
	}
	if code, body := f.login(c, "ana", "wrong"); code != 401 || !strings.Contains(body, "Invalid username or password") {
		t.Fatalf("bad password: %d", code)
	}
	if code, _ := f.login(c, "nobody", "secret"); code != 401 {
		t.Fatalf("unknown user: %d", code)
	}
	code, body := f.login(c, "ana", "secret")
	if code != 200 || !strings.Contains(body, "The contest is running.") || !strings.Contains(body, "/ioi/tasks/sum") {
		t.Fatalf("overview after login: %d\n%s", code, body)
	}
	code, body = f.get(c, "/ioi/tasks/sum")
	if code != 200 || !strings.Contains(body, "Suma") || !strings.Contains(body, "/ioi/tasks/sum/statement/es") || !strings.Contains(body, "(official)") {
		t.Fatalf("task page: %d\n%s", code, body)
	}
	code, body = f.get(c, "/ioi/tasks/sum/statement/es")
	if code != 200 || !strings.HasPrefix(body, "%PDF") {
		t.Fatalf("statement: %d %q", code, body)
	}
	if code, _ := f.get(c, "/ioi/tasks/nope"); code != 404 {
		t.Fatalf("unknown task = %d", code)
	}
	if code, _ := f.get(c, "/nope/"); code != 404 {
		t.Fatalf("unknown contest = %d", code)
	}
	code, body = f.get(c, "/ioi/documentation")
	if code != 200 || !strings.Contains(body, "gnu11") {
		t.Fatalf("documentation: %d\n%s", code, body)
	}
	// Logout.
	resp, _ = c.PostForm(f.url+"/ioi/logout", url.Values{"csrf": {csrfOf(t, body)}})
	resp.Body.Close()
	if _, body := f.get(c, "/ioi/"); !strings.Contains(body, `name="password"`) {
		t.Fatal("still logged in after logout")
	}
}

func TestSpanishUI(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	c := f.client()
	f.login(c, "ana", "secret")
	_, body := f.get(c, "/ioi/", "Accept-Language", "es-MX,es;q=0.9")
	if !strings.Contains(body, "Resumen") || !strings.Contains(body, "El concurso está en curso.") || !strings.Contains(body, `lang="es"`) {
		t.Fatalf("Spanish overview:\n%s", body)
	}
}

func TestSubmitFlow(t *testing.T) {
	two := int32(2)
	f := newFixture(t, fixtureOpts{maxSubs: &two})
	c := f.client()
	_, page := f.login(c, "ana", "secret")
	csrf := csrfOf(t, page)
	code, body := f.submit(c, csrf, "c11", "int main(){return 0;}", true)
	if code != 200 || !strings.Contains(body, `id="sub-`) || !strings.Contains(body, "Compiling") {
		t.Fatalf("htmx submit: %d\n%s", code, body)
	}
	subs, _ := f.q.ListSubmissionsByParticipationTask(bg, sqlc.ListSubmissionsByParticipationTaskParams{ParticipationID: f.part.ID, TaskID: f.task.ID})
	if len(subs) != 1 || *subs[0].Language != "c11" || !subs[0].Official {
		t.Fatalf("stored submissions %+v", subs)
	}
	files, _ := f.q.ListSubmissionFiles(bg, subs[0].ID)
	data, _ := blob.ReadAll(bg, f.store, files[0].Digest)
	if files[0].Filename != "sum.%l" || string(data) != "int main(){return 0;}" {
		t.Fatalf("stored file %+v %q", files, data)
	}
	// The dispatcher was notified.
	msgs, _ := f.rdb.XRange(bg, f.ns+"dispatch", "-", "+").Result()
	if len(msgs) != 1 || !strings.Contains(msgs[0].Values["event"].(string), fmt.Sprintf(`"submission_id":%d`, subs[0].ID)) {
		t.Fatalf("dispatch stream %+v", msgs)
	}
	// Invalid language, then the per-contest limit (2).
	if code, body := f.submit(c, csrf, "haskell", "x", true); code != 400 || !strings.Contains(body, "allowed language") {
		t.Fatalf("disallowed language: %d %s", code, body)
	}
	if code, _ := f.submit(c, csrf, "c11", "int main(){return 1;}", false); code != 200 {
		t.Fatalf("second submission (redirect followed) = %d", code)
	}
	if code, body := f.submit(c, csrf, "c11", "int main(){return 2;}", true); code != 429 || !strings.Contains(body, "maximum number") {
		t.Fatalf("limit: %d %s", code, body)
	}
	// Row, list and details endpoints.
	id := subs[0].ID
	code, body = f.get(c, fmt.Sprintf("/ioi/submissions/%d/row", id))
	if code != 200 || !strings.Contains(body, fmt.Sprintf(`id="sub-%d"`, id)) {
		t.Fatalf("row: %d", code)
	}
	code, body = f.get(c, fmt.Sprintf("/ioi/submissions/%d", id))
	if code != 200 || !strings.Contains(body, "sum.c") {
		t.Fatalf("details: %d\n%s", code, body)
	}
	code, body = f.get(c, fmt.Sprintf("/ioi/submissions/%d/file/sum.c", id))
	if code != 200 || body != "int main(){return 0;}" {
		t.Fatalf("source download: %d %q", code, body)
	}
	// Another contestant cannot see it.
	hash, _ := auth.HashPassword("pw2")
	u2, _ := f.q.CreateUser(bg, sqlc.CreateUserParams{Username: "bob", PasswordHash: hash, PreferredLanguages: []string{}})
	f.q.CreateParticipation(bg, sqlc.CreateParticipationParams{ContestID: f.contest.ID, UserID: u2.ID, Ip: []netip.Prefix{}})
	c2 := f.client()
	f.login(c2, "bob", "pw2")
	if code, _ := f.get(c2, fmt.Sprintf("/ioi/submissions/%d", id)); code != 404 {
		t.Fatalf("foreign submission visible: %d", code)
	}
}

func TestScoredSubmissionShowsPublicScore(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	c := f.client()
	_, page := f.login(c, "ana", "secret")
	f.submit(c, csrfOf(t, page), "c11", "int main(){}", true)
	subs, _ := f.q.ListSubmissionsByParticipationTask(bg, sqlc.ListSubmissionsByParticipationTaskParams{ParticipationID: f.part.ID, TaskID: f.task.ID})
	id := subs[0].ID
	// Simulate the dispatcher: compiled, evaluated and scored (public 50, total 100).
	f.q.EnsureSubmissionResult(bg, sqlc.EnsureSubmissionResultParams{SubmissionID: id, DatasetID: f.ds.ID})
	ok := "ok"
	f.q.SetCompilationResult(bg, sqlc.SetCompilationResultParams{SubmissionID: id, DatasetID: f.ds.ID, CompilationOutcome: &ok, CompilationText: "Compilation succeeded", TestcasesTotal: 2})
	full, pub := 100.0, 50.0
	det := json.RawMessage(`{"type":"sum","max_score":100,"testcases":[{"codename":"0","public":true,"outcome":1,"text":"Output is correct","time":0.01,"memory":1024,"status":"ok"}]}`)
	f.q.SetScore(bg, sqlc.SetScoreParams{SubmissionID: id, DatasetID: f.ds.ID, Score: &full, ScoreDetails: det, PublicScore: &pub, PublicScoreDetails: det, RankingScoreDetails: json.RawMessage(`[100]`)})
	_, body := f.get(c, fmt.Sprintf("/ioi/submissions/%d/row", id))
	if !strings.Contains(body, "50 / 100") || !strings.Contains(body, "Evaluated") {
		t.Fatalf("row shows %s", body)
	}
	_, body = f.get(c, fmt.Sprintf("/ioi/submissions/%d", id), "Accept-Language", "es")
	if !strings.Contains(body, "La salida es correcta") || !strings.Contains(body, "Compilación exitosa") || !strings.Contains(body, "Sólo se muestran los casos públicos.") {
		t.Fatalf("details in Spanish:\n%s", body)
	}
}

func TestSingleLogin(t *testing.T) {
	f := newFixture(t, fixtureOpts{singleLogin: true})
	a, b := f.client(), f.client()
	f.login(a, "ana", "secret")
	if code, body := f.get(a, "/ioi/"); code != 200 || strings.Contains(body, `name="password"`) {
		t.Fatalf("first session not logged in: %d", code)
	}
	f.login(b, "ana", "secret")
	time.Sleep(100 * time.Millisecond) // cache invalidation event
	if code, body := f.get(a, "/ioi/"); code != 401 || !strings.Contains(body, "another place") {
		t.Fatalf("old session still valid: %d", code)
	}
	if code, _ := f.get(b, "/ioi/"); code != 200 {
		t.Fatalf("new session rejected: %d", code)
	}
}

func TestIPRestrictionAndAutologin(t *testing.T) {
	f := newFixture(t, fixtureOpts{ipRestrict: true, ips: []netip.Prefix{netip.MustParsePrefix("10.9.0.0/16")}, trusted: []string{"127.0.0.1"}})
	c := f.client()
	if code, body := f.login(c, "ana", "secret"); code != 403 || !strings.Contains(body, "this address") {
		t.Fatalf("login from a foreign address: %d", code)
	}
	// Behind the trusted proxy, from the allowed network.
	_, page := f.get(c, "/ioi/login")
	req, _ := http.NewRequest("POST", f.url+"/ioi/login", strings.NewReader(url.Values{"csrf": {csrfOf(t, page)}, "username": {"ana"}, "password": {"secret"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-For", "10.9.1.2")
	resp, err := c.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("login from the allowed address: %v %v", resp.StatusCode, err)
	}
	resp.Body.Close()

	g := newFixture(t, fixtureOpts{ipAutologin: true, ips: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}})
	c2 := g.client()
	if code, body := g.get(c2, "/ioi/"); code != 200 || !strings.Contains(body, "ana") || strings.Contains(body, `name="password"`) {
		t.Fatalf("autologin failed: %d", code)
	}
}

func TestPerUserTimeStart(t *testing.T) {
	f := newFixture(t, fixtureOpts{perUserTime: 3600})
	c := f.client()
	_, body := f.login(c, "ana", "secret")
	if !strings.Contains(body, "Start the contest") || strings.Contains(body, "/ioi/tasks/sum") {
		t.Fatalf("waiting page:\n%s", body)
	}
	if code, _ := f.get(c, "/ioi/tasks/sum"); code != 404 {
		t.Fatalf("task visible before starting: %d", code)
	}
	resp, _ := c.PostForm(f.url+"/ioi/start", url.Values{"csrf": {csrfOf(t, body)}})
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "The contest is running.") || !strings.Contains(string(b), "data-countdown") {
		t.Fatalf("after start:\n%s", b)
	}
	p, _ := f.q.GetParticipation(bg, f.part.ID)
	if p.StartingTime == nil {
		t.Fatal("starting time not stored")
	}
}

func TestServerSentEvents(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	c := f.client()
	f.login(c, "ana", "secret")
	c.Timeout = 0
	req, _ := http.NewRequest("GET", f.url+"/ioi/events", nil)
	ctx, cancel := context.WithTimeout(bg, 5*time.Second)
	defer cancel()
	resp, err := c.Do(req.WithContext(ctx))
	if err != nil || resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("events: %v %+v", err, resp)
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	br.ReadString('\n') // retry line
	time.Sleep(100 * time.Millisecond)
	// An event for another participation must not arrive; ours must.
	events.Publish(bg, f.rdb, f.ns, events.Event{Type: events.TypeSubmission, ParticipationID: f.part.ID + 1000, SubmissionID: 1})
	events.Publish(bg, f.rdb, f.ns, events.Event{Type: events.TypeSubmission, ParticipationID: f.part.ID, SubmissionID: 42, Status: "scored"})
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("stream ended: %v", err)
		}
		if strings.HasPrefix(line, "data: ") {
			if !strings.Contains(line, `"submission_id":42`) {
				t.Fatalf("unexpected event %s", line)
			}
			return
		}
	}
}

var (
	inlineScript = regexp.MustCompile(`<script(?:\s[^>]*)?>\s*[^<\s]`)
	inlineStyle  = regexp.MustCompile(`\sstyle="`)
	inlineHandle = regexp.MustCompile(`\son[a-z]+="`)
)

func TestNoInlineCodeAndSecurityHeaders(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	c := f.client()
	f.login(c, "ana", "secret")
	for _, p := range []string{"/ioi/", "/ioi/tasks/sum", "/ioi/documentation", "/"} {
		req, _ := http.NewRequest("GET", f.url+p, nil)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		csp := resp.Header.Get("Content-Security-Policy")
		if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe") {
			t.Errorf("%s: CSP %q", p, csp)
		}
		for _, re := range []*regexp.Regexp{inlineScript, inlineStyle, inlineHandle} {
			if loc := re.FindIndex(b); loc != nil {
				t.Errorf("%s: inline code at %q", p, b[loc[0]:min(loc[1]+40, len(b))])
			}
		}
	}
}

// TestTemplatesTranslated checks that every string the templates translate
// has a Spanish entry.
func TestTemplatesTranslated(t *testing.T) {
	re := regexp.MustCompile(`\.T "([^"]+)"`)
	fs.WalkDir(web.Templates, ".", func(p string, d fs.DirEntry, err error) error {
		if d.IsDir() {
			return nil
		}
		b, _ := fs.ReadFile(web.Templates, p)
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			if !i18n.Has("es", m[1]) {
				t.Errorf("%s: %q has no Spanish translation", p, m[1])
			}
		}
		return nil
	})
}

// TestJavaScriptBudget enforces SPEC §2: < 30 KB of JavaScript (gzip).
func TestJavaScriptBudget(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	total := 0
	for _, name := range []string{"htmx.min.js", "app.js"} {
		n := f.srv.static.GzipSize(name)
		if n < 0 {
			t.Fatalf("%s missing", name)
		}
		total += n
	}
	t.Logf("JavaScript transferred: %d bytes gzip", total)
	if total >= 30*1024 {
		t.Fatalf("JavaScript budget exceeded: %d bytes", total)
	}
}

// TestTaskLanguages checks that a task's own language list narrows the
// contest's languages (K10).
func TestTaskLanguages(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	if _, err := f.pool.Exec(bg, "UPDATE tasks SET languages = '{cpp17,java}' WHERE id = $1", f.task.ID); err != nil {
		t.Fatal(err)
	}
	c := f.client()
	_, page := f.login(c, "ana", "secret")
	csrf := csrfOf(t, page)
	_, body := f.get(c, "/ioi/tasks/sum")
	// java is allowed by the task but not by the contest.
	if !strings.Contains(body, `value="cpp17"`) || strings.Contains(body, `value="c11"`) || strings.Contains(body, `value="java"`) {
		t.Fatalf("language choices:\n%s", body)
	}
	if code, body := f.submit(c, csrf, "c11", "int main(){}", true); code != 400 || !strings.Contains(body, "allowed language") {
		t.Fatalf("contest language outside the task list: %d %s", code, body)
	}
	// The language is accepted (the helper's file is named sum.c, so only
	// the extension check objects).
	if _, body := f.submit(c, csrf, "cpp17", "int main(){}", true); strings.Contains(body, "allowed language") || !strings.Contains(body, "extension") {
		t.Fatalf("allowed language: %s", body)
	}
}

// TestAskQuestion covers the contestant side of A1: validation, the
// contest switch and the event that reaches the staff.
func TestAskQuestion(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	c := f.client()
	_, page := f.login(c, "ana", "secret")
	csrf := csrfOf(t, page)
	sub := f.rdb.Subscribe(bg, events.Channel(f.ns))
	defer sub.Close()
	sub.Receive(bg)
	ask := func(v url.Values) (int, string) {
		v.Set("csrf", csrf)
		req, _ := http.NewRequest("POST", f.url+"/ioi/questions", strings.NewReader(v.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("HX-Request", "true")
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, _ := ask(url.Values{"text": {"  "}}); code != 400 {
		t.Fatalf("empty = %d", code)
	}
	if code, _ := ask(url.Values{"text": {"x"}, "task": {"nope"}}); code != 400 {
		t.Fatalf("unknown task = %d", code)
	}
	if code, _ := ask(url.Values{"text": {strings.Repeat("a", 4001)}}); code != 400 {
		t.Fatalf("too long = %d", code)
	}
	code, body := ask(url.Values{"text": {"¿Hay límite de memoria?"}, "task": {"sum"}})
	if code != 200 || !strings.Contains(body, `id="communication"`) || !strings.Contains(body, "¿Hay límite de memoria?") {
		t.Fatalf("ask = %d\n%s", code, body)
	}
	m, err := sub.ReceiveMessage(bg)
	if err != nil || !strings.Contains(m.Payload, `"type":"question_new"`) || !strings.Contains(m.Payload, "ana · sum") {
		t.Fatalf("staff event %v %v", m, err)
	}
	// Questions switched off in the contest (the cache reloads on the
	// contest event).
	f.pool.Exec(bg, "UPDATE contests SET allow_questions = false WHERE id = $1", f.contest.ID)
	events.Publish(bg, f.rdb, f.ns, events.Event{Type: events.TypeContest, ContestID: f.contest.ID})
	time.Sleep(100 * time.Millisecond)
	if code, _ := ask(url.Values{"text": {"otra"}}); code != 403 {
		t.Fatalf("questions closed = %d", code)
	}
	if _, body := f.get(c, "/ioi/communication"); strings.Contains(body, `action="/ioi/questions"`) {
		t.Fatal("the form is shown while questions are closed")
	}
}
