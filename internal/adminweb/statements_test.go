package adminweb

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/statement"
	"github.com/D4ND3R/Contest-Management-System/internal/webtest"
)

// TestStatementEditorAndExamples (SPEC_IOI H1): statements are written in
// Markdown or LaTeX with a live preview (formulas, unsupported commands
// listed) and a PDF preview, saved, typeset to PDF with the examples;
// examples are added from text, files or a testcase, explained, moved and
// deleted; a read-only administrator can preview but not change anything.
func TestStatementEditorAndExamples(t *testing.T) {
	f := newFixture(t)
	b := f.login("all")
	taskPath := fmt.Sprintf("/tasks/%d", f.task.ID)

	code, body := b.Get(taskPath + "/statements/new/edit")
	webtest.MustOK(t, "editor", code, body)
	if !strings.Contains(body, `hx-post="`+taskPath+`/statement-preview"`) {
		t.Fatalf("no live preview in the editor:\n%s", body)
	}
	src := "# Suma\n\nCalcula $\\sum_{i=1}^n a_i$ y \\foo.\n\n## Entrada\n\nUn entero $n$.\n\n{{examples}}\n"
	code, body = b.PostHTMX(taskPath+"/statement-preview", url.Values{"language": {"es"}, "format": {"md"}, "source": {src}})
	webtest.MustOK(t, "preview", code, body)
	if !strings.Contains(body, `<munderover><mo movablelimits="true">∑</mo>`) && !strings.Contains(body, `<msubsup><mo movablelimits="true">∑</mo>`) {
		t.Fatalf("preview lacks the formula:\n%s", body)
	}
	code, body = b.PostHTMX(taskPath+"/statement-preview", url.Values{"language": {"es"}, "format": {"tex"}, "source": {`\weird{x}`}})
	if code != 200 || !strings.Contains(body, `unsupported command \weird`) {
		t.Fatalf("preview warnings: %d\n%s", code, body)
	}
	code, body = b.Post(taskPath+"/statement-preview.pdf", url.Values{"language": {"es"}, "format": {"md"}, "source": {src}})
	if code != 200 || !strings.HasPrefix(body, "%PDF-1.4") {
		t.Fatalf("pdf preview: %d %q", code, body[:min(len(body), 30)])
	}

	// Save as the Spanish statement (primary).
	code, body = b.Post(taskPath+"/statements/new/source", url.Values{"language": {"es"}, "format": {"md"}, "source": {src}, "primary": {"on"}})
	webtest.MustOK(t, "save", code, body)
	stmts, _ := f.q.ListStatements(bg, f.task.ID)
	var saved bool
	for _, s := range stmts {
		if s.Language == "es" && s.ContentType == statement.TypeMarkdown {
			data, _, _ := blob.ReadLimited(bg, f.store, s.Digest, 1<<20)
			saved = string(data) == src
		}
	}
	task, _ := f.q.GetTask(bg, f.task.ID)
	if !saved || !contains(task.PrimaryStatements, "es") {
		t.Fatalf("statement not saved: %+v primary %v", stmts, task.PrimaryStatements)
	}
	code, body = b.Get(taskPath + "/statements/es/edit")
	if code != 200 || !strings.Contains(body, "Calcula $") || !strings.Contains(body, `<h3>Entrada</h3>`) {
		t.Fatalf("editor reopened: %d\n%s", code, body)
	}
	if code, body = b.Get(taskPath); code != 200 || !strings.Contains(body, taskPath+`/statements/es/pdf`) || !strings.Contains(body, "Markdown") {
		t.Fatalf("task page statements: %d", code)
	}

	// Examples: typed, from files, from a testcase.
	code, body = b.Post(taskPath+"/examples", url.Values{"input": {"1 2"}, "output": {"3"}, "note": {"Porque $1+2=3$."}})
	webtest.MustOK(t, "add example", code, body)
	code, body = b.PostMultipart(taskPath+"/examples", map[string]string{}, webtest.File{Field: "input_file", Name: "in.txt", Data: []byte("5 5\n")},
		webtest.File{Field: "output_file", Name: "out.txt", Data: []byte("10\n")})
	webtest.MustOK(t, "add example from files", code, body)
	if code, _ := b.Post(taskPath+"/examples", url.Values{"input": {"only input"}}); code != http.StatusUnprocessableEntity {
		t.Fatalf("example without output = %d", code)
	}
	tcs, _ := f.q.ListTestcases(bg, f.ds.ID)
	code, body = b.Post(fmt.Sprintf("/testcases/%d/example", tcs[1].ID), url.Values{})
	webtest.MustOK(t, "example from testcase", code, body)
	exs, _ := f.q.ListTaskExamples(bg, f.task.ID)
	if len(exs) != 3 || exs[2].InputDigest != tcs[1].InputDigest || exs[0].Note != "Porque $1+2=3$." {
		t.Fatalf("examples %+v", exs)
	}
	// Move the testcase example first, then delete the second one.
	for i := 0; i < 2; i++ {
		if code, body := b.Post(fmt.Sprintf("%s/examples/%d/up", taskPath, exs[2].ID), url.Values{}); code != 200 {
			t.Fatalf("up: %d %s", code, body)
		}
	}
	exs, _ = f.q.ListTaskExamples(bg, f.task.ID)
	if exs[0].InputDigest != tcs[1].InputDigest {
		t.Fatalf("order %+v", exs)
	}
	b.Post(fmt.Sprintf("%s/examples/%d/delete", taskPath, exs[1].ID), url.Values{})
	if exs, _ = f.q.ListTaskExamples(bg, f.task.ID); len(exs) != 2 {
		t.Fatalf("after delete %+v", exs)
	}

	// The saved statement as PDF has the examples; the editor preview counts them.
	code, body = b.Get(taskPath + "/statements/es/pdf")
	if code != 200 || !strings.HasPrefix(body, "%PDF-1.4") {
		t.Fatalf("statement pdf: %d", code)
	}
	if _, body = b.Get(taskPath + "/statements/es/edit"); !strings.Contains(body, "with 2 examples") {
		t.Fatalf("editor preview without examples:\n%s", body)
	}
	// The uploaded PDF statement opens in the editor as a frame.
	if _, body = b.Get(taskPath + "/statements/en/edit"); !strings.Contains(body, `<iframe class="pdf-frame" src="`+taskPath+`/statements/en/pdf"`) {
		t.Fatalf("pdf statement in the editor:\n%s", body)
	}
	if code, _ := b.Get(taskPath + "/statements/fr/edit"); code != http.StatusNotFound {
		t.Fatalf("missing statement = %d", code)
	}

	// Read-only administrators preview but cannot save or change examples.
	ro := f.login("read_only")
	if code, _ := ro.PostHTMX(taskPath+"/statement-preview", url.Values{"language": {"es"}, "format": {"md"}, "source": {"x"}}); code != 200 {
		t.Fatalf("read-only preview = %d", code)
	}
	if code, _ := ro.Post(taskPath+"/statements/es/source", url.Values{"format": {"md"}, "source": {"x"}}); code != http.StatusForbidden {
		t.Fatalf("read-only save = %d", code)
	}
	if code, _ := ro.Post(taskPath+"/examples", url.Values{"input": {"1"}, "output": {"1"}}); code != http.StatusForbidden {
		t.Fatalf("read-only example = %d", code)
	}
	// Bad input is refused politely.
	if code, _ := b.Post(taskPath+"/statements/new/source", url.Values{"language": {"../x"}, "format": {"md"}, "source": {"x"}}); code != http.StatusUnprocessableEntity {
		t.Fatalf("bad language = %d", code)
	}
	if code, _ := b.Post(taskPath+"/statements/new/source", url.Values{"language": {"es"}, "format": {"docx"}, "source": {"x"}}); code != http.StatusUnprocessableEntity {
		t.Fatalf("bad format = %d", code)
	}
}
