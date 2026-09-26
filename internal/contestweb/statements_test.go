package contestweb

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/events"
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

// TestResultCardAndTesting (SPEC_IOI H1): the result of the newest
// submission is next to the submit button (the submission's answer replaces
// it and updates the list out of band); the Testing page exists.
func TestResultCardAndTesting(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	c := f.client()
	_, page := f.login(c, "ana", "secret")
	csrf := csrfOf(t, page)
	_, body := f.get(c, "/ioi/tasks/sum")
	if !strings.Contains(body, `id="latest"`) || !strings.Contains(body, "No submissions yet.") {
		t.Fatalf("empty card missing:\n%s", body)
	}
	code, body := f.submit(c, csrf, "c11", "int main(){}", true)
	if code != 200 || !strings.Contains(body, `<section class="card result`) || !strings.Contains(body, `id="latest"`) ||
		!strings.Contains(body, `id="submissions"`) || !strings.Contains(body, `hx-swap-oob="true"`) {
		t.Fatalf("submit answer: %d\n%s", code, body)
	}
	subs, _ := f.q.ListSubmissionsByParticipation(bg, f.part.ID)
	id := subs[0].ID
	// Scored with subtasks: the card shows the score and a chip per subtask.
	f.scoreSubmission(t, id)
	det := json.RawMessage(`{"type":"group","max_score":100,"subtasks":[{"index":1,"score":30,"max_score":30,"fraction":1,"testcases":[]},{"index":2,"score":0,"max_score":70,"fraction":0,"testcases":[]}]}`)
	full, pub := 30.0, 30.0
	f.q.SetScore(bg, sqlc.SetScoreParams{SubmissionID: id, DatasetID: f.ds.ID, Score: &full, ScoreDetails: det, PublicScore: &pub,
		PublicScoreDetails: det, RankingScoreDetails: json.RawMessage(`[30, 0]`)})
	code, body = f.get(c, "/ioi/submissions/"+itoa(id)+"/card")
	if code != 200 || !strings.Contains(body, `<li class="ok">Subtask 1 <b>30/30</b></li>`) || !strings.Contains(body, `<li class="bad">Subtask 2 <b>0/70</b></li>`) {
		t.Fatalf("card: %d\n%s", code, body)
	}
	// A compilation error shows its first lines on the card.
	ce := "fail"
	f.q.SetCompilationResult(bg, sqlc.SetCompilationResultParams{SubmissionID: id, DatasetID: f.ds.ID, CompilationOutcome: &ce,
		CompilationText: "Compilation failed", CompilationStderr: "sol.c:1: error: expected ';'", TestcasesTotal: 2})
	if _, body = f.get(c, "/ioi/submissions/"+itoa(id)+"/card"); !strings.Contains(body, `<pre class="compile-error">sol.c:1: error: expected &#39;;&#39;</pre>`) {
		t.Fatalf("compile error on the card:\n%s", body)
	}
	// Someone else's submission is not readable.
	if code, _ := f.get(c, "/ioi/submissions/999999/card"); code != 404 {
		t.Fatalf("foreign card: %d", code)
	}

	// The Testing page (the menu linked to a 404).
	code, body = f.get(c, "/ioi/testing")
	if code != 200 || !strings.Contains(body, `action="/ioi/tasks/sum/test?from=testing"`) {
		t.Fatalf("testing page: %d\n%s", code, body)
	}
}
