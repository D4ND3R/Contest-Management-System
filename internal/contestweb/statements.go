package contestweb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/statement"
)

// Statements written in Markdown, LaTeX or HTML are rendered on the task
// page (formulas in MathML) and typeset to PDF with the limits and the
// examples; uploaded PDFs are shown embedded. Renderings are cached by
// everything they depend on (the source, the examples, the limits, the
// language), so a new upload or a changed limit shows at once and the
// cache key doubles as the URL version.

// maxStatementBytes bounds a statement source read for rendering.
const maxStatementBytes = 4 << 20

// maxExampleBytes bounds one example file shown in a statement.
const maxExampleBytes = 256 << 10

type stmtEntry struct {
	once sync.Once
	html string
	doc  *statement.Doc
	opts statement.PDFOptions
	warn []string
	err  error

	pdfOnce sync.Once
	pdf     []byte
}

// stmtCache keeps the latest renderings (a contest has few statements).
type stmtCache struct {
	mu      sync.Mutex
	entries map[string]*stmtEntry
	order   []string
}

const stmtCacheSize = 256

func (c *stmtCache) get(key string) *stmtEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]*stmtEntry{}
	}
	if e, ok := c.entries[key]; ok {
		return e
	}
	e := &stmtEntry{}
	c.entries[key] = e
	c.order = append(c.order, key)
	if len(c.order) > stmtCacheSize {
		delete(c.entries, c.order[0])
		c.order = c.order[1:]
	}
	return e
}

// statementPage is what the task page shows of a statement.
type statementPage struct {
	Current   statementView
	Languages []statementView
	HTML      template.HTML // rendered source ("" for an uploaded PDF)
	PDFURL    string        // the PDF: uploaded, or typeset from the source
	Embedded  bool          // an uploaded PDF, shown in a frame
	Examples  template.HTML // the task's examples next to an uploaded PDF
}

// pickStatement chooses the statement to show: the one asked for, else the
// interface language's, else an official one, else the first.
func pickStatement(t *taskView, asked, ui string) (statementView, bool) {
	if len(t.Statements) == 0 {
		return statementView{}, false
	}
	for _, st := range t.Statements {
		if asked != "" && st.Lang == asked {
			return st, true
		}
	}
	for _, st := range t.Statements {
		if statement.UILang(st.Lang) == ui && strings.HasPrefix(st.Lang, ui) {
			return st, true
		}
	}
	return t.Statements[0], true // official ones come first
}

// statementKey identifies a rendering.
func (s *Server) statementKey(rc *reqCtx, t *taskView, st statementView) string {
	h := sha256.New()
	fmt.Fprintf(h, "v1|%s|%s|%s|%s|%s|%d|%d|%s|%s|%s|%s|%s\n", st.Digest, st.ContentType, st.Lang, t.Title, rc.contest.Name,
		t.TimeLimit, t.MemoryLimit, t.TaskType, t.InputFile, t.OutputFile, t.Name, rc.contest.Description)
	for _, e := range t.Examples {
		fmt.Fprintf(h, "%s|%s|%s\n", e.InputDigest, e.OutputDigest, e.Note)
	}
	for _, a := range t.Attachments {
		fmt.Fprintf(h, "%s=%s\n", a, t.AttachDigest[a])
	}
	return hex.EncodeToString(h.Sum(nil))[:20]
}

// renderStatement parses and renders a source statement (once per key).
func (s *Server) renderStatement(ctx context.Context, rc *reqCtx, t *taskView, st statementView, base string) (*stmtEntry, string) {
	key := s.statementKey(rc, t, st)
	e := s.stmts.get(key)
	e.once.Do(func() {
		data, _, err := blob.ReadLimited(ctx, s.blobs, st.Digest, maxStatementBytes)
		if err != nil {
			e.err = err
			return
		}
		doc, ok := statement.ParseIn(st.ContentType, data, st.Lang)
		if !ok {
			return
		}
		lang := statement.UILang(st.Lang)
		labels := statement.LabelsFor(lang)
		examples, err := s.loadExamples(ctx, t)
		if err != nil {
			e.err = err
			return
		}
		version := url.Values{"v": {key}}.Encode()
		e.doc = doc
		e.warn = doc.Warnings
		e.html = doc.HTML(statement.HTMLOptions{
			Labels: labels, Examples: examples,
			Image: func(name string) string {
				if _, ok := t.AttachDigest[name]; !ok {
					return ""
				}
				return base + "tasks/" + url.PathEscape(t.Name) + "/attachments/" + url.PathEscape(name) + "?" + version
			},
		})
		e.opts = statement.PDFOptions{
			Title: statementTitle(t), Subtitle: rc.contest.Description, Labels: labels, Examples: examples,
			Info: statement.InfoRows(lang, statement.TaskInfo{TimeLimit: t.TimeLimit, MemoryLimit: t.MemoryLimit, TaskType: t.TaskType, InputFile: t.InputFile, OutputFile: t.OutputFile}), Footer: t.Name,
			Image: func(name string) ([]byte, bool) {
				d, ok := t.AttachDigest[name]
				if !ok {
					return nil, false
				}
				b, _, err := blob.ReadLimited(context.Background(), s.blobs, d, 16<<20)
				return b, err == nil
			},
		}
		if e.opts.Subtitle == "" {
			e.opts.Subtitle = rc.contest.Name
		}
	})
	return e, key
}

func statementTitle(t *taskView) string {
	if t.Title == "" || t.Title == t.Name {
		return t.Name
	}
	return t.Name + ". " + t.Title
}

// loadExamples reads the task's examples.
func (s *Server) loadExamples(ctx context.Context, t *taskView) ([]statement.Example, error) {
	var out []statement.Example
	for _, e := range t.Examples {
		in, _, err := blob.ReadLimited(ctx, s.blobs, e.InputDigest, maxExampleBytes)
		if err != nil {
			return nil, err
		}
		o, _, err := blob.ReadLimited(ctx, s.blobs, e.OutputDigest, maxExampleBytes)
		if err != nil {
			return nil, err
		}
		ex := statement.Example{Input: string(in), Output: string(o)}
		if strings.TrimSpace(e.Note) != "" {
			ex.Note = statement.ParseMarkdown(e.Note).Blocks
		}
		out = append(out, ex)
	}
	return out, nil
}

// statementPage prepares the statement part of the task page.
func (s *Server) statementPage(r *http.Request, rc *reqCtx, p *page, t *taskView) (*statementPage, error) {
	st, ok := pickStatement(t, r.URL.Query().Get("lang"), p.Lang)
	if !ok {
		if len(t.Examples) == 0 {
			return nil, nil
		}
		// No statement, but examples: show them.
		examples, err := s.loadExamples(r.Context(), t)
		if err != nil {
			return nil, err
		}
		html := (&statement.Doc{}).HTML(statement.HTMLOptions{Labels: statement.LabelsFor(p.Lang), Examples: examples})
		return &statementPage{Examples: template.HTML(html)}, nil
	}
	sp := &statementPage{Current: st, Languages: t.Statements}
	base := p.Base + "tasks/" + url.PathEscape(t.Name) + "/statement/" + url.PathEscape(st.Lang)
	if !statement.IsSource(st.ContentType) {
		sp.Embedded = true
		sp.PDFURL = base + ".pdf?v=" + st.Digest[:20]
		if len(t.Examples) > 0 {
			examples, err := s.loadExamples(r.Context(), t)
			if err != nil {
				return nil, err
			}
			lang := statement.UILang(st.Lang)
			html := (&statement.Doc{}).HTML(statement.HTMLOptions{Labels: statement.LabelsFor(lang), Examples: examples})
			sp.Examples = template.HTML(html)
		}
		return sp, nil
	}
	e, key := s.renderStatement(r.Context(), rc, t, st, p.Base)
	if e.err != nil {
		return nil, e.err
	}
	sp.HTML = template.HTML(e.html)
	sp.PDFURL = base + ".pdf?v=" + key
	return sp, nil
}

// handleStatement serves a statement as PDF: the uploaded one, or the
// source typeset with the limits and examples. "/statement/es" (the old
// address) and "/statement/es.pdf" are the same.
func (s *Server) handleStatement(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t := s.visibleTask(w, r, rc)
	if t == nil {
		return
	}
	lang := strings.TrimSuffix(r.PathValue("lang"), ".pdf")
	var st statementView
	found := false
	for _, x := range t.Statements {
		if x.Lang == lang {
			st, found = x, true
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	name := t.Name + "-" + st.Lang + ".pdf"
	// Embedding in the task page (same origin) is allowed.
	h := w.Header()
	h.Set("X-Frame-Options", "SAMEORIGIN")
	h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'self'")
	if !statement.IsSource(st.ContentType) {
		s.serveVersioned(w, r, st.Digest[:20], name, statement.TypePDF, func() ([]byte, error) {
			b, _, err := blob.ReadLimited(r.Context(), s.blobs, st.Digest, 64<<20)
			return b, err
		})
		return
	}
	e, key := s.renderStatement(r.Context(), rc, t, st, s.newPage(rc, "", "").Base)
	if e.err != nil {
		s.fail(w, e.err)
		return
	}
	s.serveVersioned(w, r, key, name, statement.TypePDF, func() ([]byte, error) {
		e.pdfOnce.Do(func() {
			if e.doc != nil {
				e.pdf = e.doc.PDF(e.opts)
			}
		})
		return e.pdf, nil
	})
}

// serveVersioned serves content that changes: with ?v=<its version> it is
// cached for good (the page links the current version), otherwise the
// browser must revalidate (ETag) so a new version always shows.
func (s *Server) serveVersioned(w http.ResponseWriter, r *http.Request, version, name, ctype string, body func() ([]byte, error)) {
	h := w.Header()
	etag := `"` + version + `"`
	h.Set("ETag", etag)
	if r.URL.Query().Get("v") == version {
		h.Set("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "private, no-cache")
	}
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	b, err := body()
	if err != nil {
		s.fail(w, err)
		return
	}
	h.Set("Content-Type", ctype)
	h.Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", name))
	h.Set("Content-Length", strconv.Itoa(len(b)))
	w.Write(b)
}
