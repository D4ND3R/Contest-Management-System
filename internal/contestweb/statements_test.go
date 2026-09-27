package contestweb

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/events"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/statement"
)

// statementChanged stores a statement and tells the server, as the admin
// does.
func (f *fixture) statementChanged(t *testing.T, lang, ct, src string) {
	t.Helper()
	info, _ := f.store.PutBytes(bg, []byte(src))
	if _, err := f.q.UpsertStatement(bg, sqlc.UpsertStatementParams{TaskID: f.task.ID, Language: lang, Digest: info.Digest, ContentType: ct}); err != nil {
		t.Fatal(err)
	}
	events.Publish(bg, f.rdb, f.ns, events.Event{Type: events.TypeContest, ContestID: f.contest.ID})
	f.srv.cache.invalidateContest(f.contest.ID)
}

// headers fetches a page and returns its status, headers and body.
func (f *fixture) headers(c *http.Client, path string, hdr ...string) (int, http.Header, string) {
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
	var sb strings.Builder
	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp.StatusCode, resp.Header, sb.String()
}

var pdfLinkRe = regexp.MustCompile(`href="(/ioi/tasks/sum/statement/es\.pdf\?v=[0-9a-f]+)"`)

// TestStatementsOnTheTaskPage (SPEC_IOI H1): a Markdown statement with
// formulas is shown on the task page (MathML) and as a PDF with the limits
// and the examples typeset in; a new version shows at once (the links
// carry the version, the old address revalidates); examples are not
// downloads.
func TestStatementsOnTheTaskPage(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	c := f.client()
	f.login(c, "ana", "secret")
	f.pool.Exec(bg, "UPDATE datasets SET time_limit_ms = 1500, memory_limit_bytes = 268435456 WHERE id = $1", f.ds.ID)
	in, _ := f.store.PutBytes(bg, []byte("1 2\n"))
	out, _ := f.store.PutBytes(bg, []byte("3\n"))
	if _, err := f.q.InsertTaskExample(bg, sqlc.InsertTaskExampleParams{TaskID: f.task.ID, InputDigest: in.Digest, OutputDigest: out.Digest,
		Note: "Porque $1+2=3$."}); err != nil {
		t.Fatal(err)
	}
	f.statementChanged(t, "es", statement.TypeMarkdown, "# Suma\n\nCalcula $a+b$ con $1 \\le a, b \\le 10^9$.\n\n## Entrada\n\nDos enteros.\n")

	code, body := f.get(c, "/ioi/tasks/sum")
	if code != 200 {
		t.Fatalf("task page: %d", code)
	}
	for _, w := range []string{`<math><mrow><mi>a</mi><mo>+</mo><mi>b</mi></mrow></math>`, `<h3>Entrada</h3>`, `<h2 class="st-examples">Ejemplos</h2>`,
		`<pre>1 2`, `<div class="io-label">Salida</div>`, `<math><mrow><mn>1</mn><mo>+</mo><mn>2</mn>`} {
		if !strings.Contains(body, w) {
			t.Errorf("task page lacks %s", w)
		}
	}
	m := pdfLinkRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no PDF link:\n%s", body)
	}
	pdfURL := m[1]
	code, h, pdf := f.headers(c, pdfURL)
	if code != 200 || !strings.HasPrefix(pdf, "%PDF-1.4") || h.Get("Content-Type") != "application/pdf" ||
		!strings.Contains(h.Get("Cache-Control"), "immutable") || h.Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Fatalf("pdf: %d %v %q", code, h, pdf[:min(len(pdf), 20)])
	}
	// Without the version (an old bookmark) the browser must revalidate.
	code, h, _ = f.headers(c, "/ioi/tasks/sum/statement/es")
	if code != 200 || h.Get("Cache-Control") != "private, no-cache" || h.Get("ETag") == "" {
		t.Fatalf("unversioned: %d %v", code, h)
	}
	if code, _, _ := f.headers(c, "/ioi/tasks/sum/statement/es", "If-None-Match", h.Get("ETag")); code != http.StatusNotModified {
		t.Fatalf("revalidation: %d", code)
	}

	// A new statement: new content, new version, and the old ETag no
	// longer matches.
	f.statementChanged(t, "es", statement.TypeLaTeX, `\section*{Entrada}Dos enteros $a$ y $b$ \textbf{nuevos}.`)
	_, body = f.get(c, "/ioi/tasks/sum")
	if !strings.Contains(body, "<strong>nuevos</strong>") || strings.Contains(body, pdfURL) {
		t.Fatalf("the new statement does not show:\n%s", body)
	}
	if code, _, _ := f.headers(c, "/ioi/tasks/sum/statement/es", "If-None-Match", h.Get("ETag")); code != 200 {
		t.Fatalf("stale version still valid: %d", code)
	}

	// An uploaded PDF is embedded, with the examples next to it.
	f.statementChanged(t, "es", statement.TypePDF, "%PDF-1.4 uploaded")
	_, body = f.get(c, "/ioi/tasks/sum")
	if !strings.Contains(body, `<iframe class="pdf-frame" src="/ioi/tasks/sum/statement/es.pdf?v=`) || !strings.Contains(body, `class="example"`) {
		t.Fatalf("pdf statement:\n%s", body)
	}
	// Attachments link a version too.
	att, _ := f.store.PutBytes(bg, []byte("grader"))
	f.q.UpsertAttachment(bg, sqlc.UpsertAttachmentParams{TaskID: f.task.ID, Filename: "grader.cpp", Digest: att.Digest})
	f.srv.cache.invalidateContest(f.contest.ID)
	_, body = f.get(c, "/ioi/tasks/sum")
	link := "/ioi/tasks/sum/attachments/grader.cpp?v=" + att.Digest[:20]
	if !strings.Contains(body, link) {
		t.Fatalf("attachment link missing:\n%s", body)
	}
	if _, h, _ := f.headers(c, link); !strings.Contains(h.Get("Cache-Control"), "immutable") {
		t.Fatalf("versioned attachment: %v", h)
	}
	if _, h, _ := f.headers(c, "/ioi/tasks/sum/attachments/grader.cpp"); h.Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("unversioned attachment: %v", h)
	}
}

// TestSubmitAnswerAndTesting (SPEC_MIN §6, §7): the Submissions tab of a
// task holds the form and the list; submitting through htmx answers with
// the refreshed tab and a "submission sent" notification; the submission's
// page shows the subtask blocks and the compiler's message; the Testing
// page exists.
func TestSubmitAnswerAndTesting(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	c := f.client()
	_, page := f.login(c, "ana", "secret")
	csrf := csrfOf(t, page)
	_, body := f.get(c, "/ioi/tasks/sum/submissions")
	if !strings.Contains(body, `id="subs-tab"`) || !strings.Contains(body, "No submissions yet.") {
		t.Fatalf("empty submissions tab:\n%s", body)
	}
	var body0 bytes.Buffer
	mw := multipart.NewWriter(&body0)
	mw.WriteField("csrf", csrf)
	mw.WriteField("language", "c11")
	fw, _ := mw.CreateFormFile("sum.%l", "sum.c")
	fw.Write([]byte("int main(){}"))
	mw.Close()
	req, _ := http.NewRequest("POST", f.url+"/ioi/tasks/sum/submit", &body0)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("HX-Request", "true")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	body = string(b)
	if resp.StatusCode != 200 || !strings.Contains(body, `id="subs-tab"`) || !strings.Contains(body, `id="submissions"`) ||
		!strings.Contains(body, "Compiling…") || !strings.Contains(resp.Header.Get("HX-Trigger"), `"cms-submitted"`) {
		t.Fatalf("submit answer: %d %v\n%s", resp.StatusCode, resp.Header, body)
	}
	subs, _ := f.q.ListSubmissionsByParticipation(bg, f.part.ID)
	id := subs[0].ID
	// Without htmx the browser lands on the Submissions tab with a notice.
	if code, body := f.submit(c, csrf, "c11", "int main(){return 0;}", false); code != 200 || !strings.Contains(body, "received. Watch its status below") {
		t.Fatalf("plain submit: %d\n%s", code, body)
	}
	// Scored with subtasks: one block per subtask.
	f.scoreSubmission(t, id)
	det := json.RawMessage(`{"type":"group","max_score":100,"subtasks":[{"index":1,"score":30,"max_score":30,"fraction":1,"testcases":[]},{"index":2,"score":0,"max_score":70,"fraction":0,"testcases":[]}]}`)
	full, pub := 30.0, 30.0
	f.q.SetScore(bg, sqlc.SetScoreParams{SubmissionID: id, DatasetID: f.ds.ID, Score: &full, ScoreDetails: det, PublicScore: &pub,
		PublicScoreDetails: det, RankingScoreDetails: json.RawMessage(`[30, 0]`)})
	code, body := f.get(c, "/ioi/submissions/"+itoa(id))
	if code != 200 || !strings.Contains(body, `<li class="ok"><small>Subtask 1</small><b>30 / 30</b>`) || !strings.Contains(body, `<li class="bad"><small>Subtask 2</small><b>0 / 70</b>`) {
		t.Fatalf("submission page: %d\n%s", code, body)
	}
	// A compilation error shows the compiler's message.
	ce := "fail"
	f.q.SetCompilationResult(bg, sqlc.SetCompilationResultParams{SubmissionID: id, DatasetID: f.ds.ID, CompilationOutcome: &ce,
		CompilationText: "Compilation failed", CompilationStderr: "sol.c:1: error: expected ';'", TestcasesTotal: 2})
	if _, body = f.get(c, "/ioi/submissions/"+itoa(id)); !strings.Contains(body, `<pre class="compile-error">sol.c:1: error: expected &#39;;&#39;</pre>`) {
		t.Fatalf("compile error:\n%s", body)
	}
	// Someone else's submission is not readable.
	if code, _ := f.get(c, "/ioi/submissions/999999"); code != 404 {
		t.Fatalf("foreign submission: %d", code)
	}

	// The Testing page.
	code, body = f.get(c, "/ioi/testing")
	if code != 200 || !strings.Contains(body, `action="/ioi/tasks/sum/test?from=testing"`) {
		t.Fatalf("testing page: %d\n%s", code, body)
	}
}

// TestQueuePosition (SPEC_IOI H4): while a submission waits for a worker
// its row says how many submissions are ahead and how long results take
// now, and is marked for refreshing; once judged it is not.
func TestQueuePosition(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	c := f.client()
	_, page := f.login(c, "ana", "secret")
	csrf := csrfOf(t, page)
	f.submit(c, csrf, "c11", "int main(){}", true)
	f.submit(c, csrf, "c11", "int main(){return 0;}", true)
	subs, _ := f.q.ListSubmissionsByParticipation(bg, f.part.ID)
	if len(subs) != 2 {
		t.Fatalf("%d submissions", len(subs))
	}
	older, newer := subs[0].ID, subs[1].ID
	for _, id := range []int64{older, newer} {
		f.q.EnsureSubmissionResult(bg, sqlc.EnsureSubmissionResultParams{SubmissionID: id, DatasetID: f.ds.ID})
	}
	row := func(id int64) string { _, b := f.get(c, "/ioi/submissions/"+itoa(id)+"/row"); return b }
	if b := row(older); !strings.Contains(b, "Next in the queue.") || !strings.Contains(b, `data-pending="1"`) {
		t.Fatalf("older:\n%s", b)
	}
	if b := row(newer); !strings.Contains(b, "Submissions ahead of yours in the queue: 1.") || strings.Contains(b, "Results take about") {
		t.Fatalf("newer:\n%s", b)
	}
	f.scoreSubmission(t, older)
	f.srv.latency = latencyCache{} // the typical time is cached for 10 s
	if b := row(older); strings.Contains(b, "queue") || strings.Contains(b, "data-pending") {
		t.Fatalf("judged row still refreshes:\n%s", b)
	}
	if b := row(newer); !strings.Contains(b, "Next in the queue.") || !strings.Contains(b, "Results take about 1 s right now.") {
		t.Fatalf("newer after the older was judged:\n%s", b)
	}
	// The submission's own page says it too.
	if _, b := f.get(c, "/ioi/submissions/"+itoa(newer)); !strings.Contains(b, "Next in the queue.") || !strings.Contains(b, `data-pending="1"`) {
		t.Fatalf("submission page:\n%s", b)
	}
}

// TestLanguageTimes (SPEC_IOI §3): the task page lists the time limit of
// every language with a time multiplier.
func TestLanguageTimes(t *testing.T) {
	tv := &taskView{TimeLimit: 1500 * time.Millisecond, Languages: []*langs.Language{{Name: "C"}, {Name: "Java", TimeMultiplier: 2}, {Name: "Py", TimeMultiplier: 1}}}
	got := tv.LanguageTimes()
	if len(got) != 1 || got[0].Name != "Java" || got[0].Limit != 3*time.Second {
		t.Fatalf("%+v", got)
	}
	if (&taskView{Languages: tv.Languages}).LanguageTimes() != nil {
		t.Fatal("no time limit, yet language limits")
	}
}
