package adminweb

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/i18n"
	"github.com/D4ND3R/Contest-Management-System/internal/statement"
	"github.com/D4ND3R/Contest-Management-System/internal/tasktypes"
)

// Statements are written here in Markdown or LaTeX (or pasted HTML) with
// a live preview and a PDF preview; uploaded PDFs are kept as they are.
// Examples belong to the task and are shown in every statement.

// maxStatementSource bounds a statement written in the editor.
const maxStatementSource = 1 << 20

// statementFormats are the formats the editor writes.
var statementFormats = []struct{ ID, Name, Type string }{
	{"md", "Markdown", statement.TypeMarkdown},
	{"tex", "LaTeX", statement.TypeLaTeX},
	{"html", "HTML", statement.TypeHTML},
}

func formatType(id string) string {
	for _, f := range statementFormats {
		if f.ID == id {
			return f.Type
		}
	}
	return ""
}

func formatOf(contentType string) string {
	for _, f := range statementFormats {
		if strings.HasPrefix(contentType, strings.SplitN(f.Type, ";", 2)[0]) {
			return f.ID
		}
	}
	return "md"
}

// statementEdit is the editor page.
type statementEdit struct {
	Task      sqlc.Task
	Lang      string
	New       bool
	Format    string
	Formats   []struct{ ID, Name, Type string }
	Source    string
	Primary   bool
	IsPDF     bool // the saved statement is an uploaded PDF
	Preview   template.HTML
	Warnings  []string
	Examples  int
	Languages []string // statements the task already has
}

// renderContext gathers what a statement is rendered with: limits of the
// live dataset, the examples, attachments (images) and the contest.
type renderContext struct {
	info     statement.TaskInfo
	examples []statement.Example
	images   map[string]string
	contest  string
	title    string
	name     string
}

func (s *Server) statementContext(ctx context.Context, t sqlc.Task) (*renderContext, error) {
	rc := &renderContext{images: map[string]string{}, name: t.Name, title: t.Name}
	if t.Title != "" && t.Title != t.Name {
		rc.title = t.Name + ". " + t.Title
	}
	if t.ActiveDatasetID != nil {
		ds, err := s.q.GetDataset(ctx, *t.ActiveDatasetID)
		if err == nil {
			rc.info.TaskType = ds.TaskType
			if ds.TimeLimitMs != nil {
				rc.info.TimeLimit = time.Duration(*ds.TimeLimitMs) * time.Millisecond
			}
			if ds.MemoryLimitBytes != nil {
				rc.info.MemoryLimit = *ds.MemoryLimitBytes
			}
			if ds.TaskType == "Batch" || ds.TaskType == "TwoSteps" {
				var bp tasktypes.BatchParams
				if json.Unmarshal(ds.TaskTypeParams, &bp) == nil {
					rc.info.InputFile, rc.info.OutputFile = bp.InputFile, bp.OutputFile
				}
			}
		}
	}
	exs, err := s.q.ListTaskExamples(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	for _, e := range exs {
		in, _, err := blob.ReadLimited(ctx, s.blobs, e.InputDigest, 256<<10)
		if err != nil {
			return nil, err
		}
		out, _, err := blob.ReadLimited(ctx, s.blobs, e.OutputDigest, 256<<10)
		if err != nil {
			return nil, err
		}
		ex := statement.Example{Input: string(in), Output: string(out)}
		if strings.TrimSpace(e.Note) != "" {
			ex.Note = statement.ParseMarkdown(e.Note).Blocks
		}
		rc.examples = append(rc.examples, ex)
	}
	atts, err := s.q.ListAttachments(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	for _, a := range atts {
		rc.images[a.Filename] = a.Digest
	}
	if t.ContestID != nil {
		if c, err := s.q.GetContest(ctx, *t.ContestID); err == nil {
			rc.contest = c.Description
			if rc.contest == "" {
				rc.contest = c.Name
			}
		}
	}
	return rc, nil
}

func (s *Server) statementHTML(t sqlc.Task, doc *statement.Doc, rc *renderContext, lang string) string {
	return doc.HTML(statement.HTMLOptions{
		Labels: statement.LabelsFor(statement.UILang(lang)), Examples: rc.examples,
		Image: func(name string) string {
			if _, ok := rc.images[name]; !ok {
				return ""
			}
			return "/tasks/" + strconv.FormatInt(t.ID, 10) + "/attachments/" + name
		},
	})
}

func (s *Server) statementPDF(ctx context.Context, doc *statement.Doc, rc *renderContext, lang string) []byte {
	ui := statement.UILang(lang)
	return doc.PDF(statement.PDFOptions{
		Title: rc.title, Subtitle: rc.contest, Labels: statement.LabelsFor(ui), Examples: rc.examples,
		Info: statement.InfoRows(ui, rc.info), Footer: rc.name,
		Image: func(name string) ([]byte, bool) {
			d, ok := rc.images[name]
			if !ok {
				return nil, false
			}
			b, _, err := blob.ReadLimited(ctx, s.blobs, d, 16<<20)
			return b, err == nil
		},
	})
}

// handleStatementEdit shows the editor (a new statement with lang "new").
func (s *Server) handleStatementEdit(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, ok := s.loadTask(w, r, rc)
	if !ok {
		return
	}
	lang := r.PathValue("lang")
	d := &statementEdit{Task: t, Lang: lang, Format: "md", Formats: statementFormats, New: lang == "new"}
	if d.New {
		d.Lang = ""
		if f := r.URL.Query().Get("format"); formatType(f) != "" {
			d.Format = f
		}
	}
	stmts, err := s.q.ListStatements(r.Context(), t.ID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	for _, st := range stmts {
		d.Languages = append(d.Languages, st.Language)
		if st.Language != lang {
			continue
		}
		d.Primary = contains(t.PrimaryStatements, lang)
		if !statement.IsSource(st.ContentType) {
			d.IsPDF = true
			continue
		}
		data, _, err := blob.ReadLimited(r.Context(), s.blobs, st.Digest, maxStatementSource)
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		d.Source, d.Format = string(data), formatOf(st.ContentType)
	}
	if !d.New && d.Source == "" && !d.IsPDF && !contains(d.Languages, lang) {
		s.notFound(w, r, rc)
		return
	}
	if d.Source != "" {
		ctx, err := s.statementContext(r.Context(), t)
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		doc, _ := statement.ParseIn(formatType(d.Format), []byte(d.Source), lang)
		d.Preview = template.HTML(s.statementHTML(t, doc, ctx, lang))
		d.Warnings = doc.Warnings
		d.Examples = len(ctx.examples)
	}
	title := t.Name + " · " + lang
	if d.New {
		title = t.Name
	}
	p := s.newPage(w, r, rc, title, "tasks", d)
	p.crumb("Tasks", "/tasks").crumb(t.Name, "/tasks/"+strconv.FormatInt(t.ID, 10))
	s.render(w, "statement_edit", http.StatusOK, p)
}

// readSource reads the editor's form: language, format and text.
func readSource(r *http.Request) (lang, ct, src, msg string) {
	lang = strings.TrimSpace(r.FormValue("language"))
	if lang == "" {
		lang = r.PathValue("lang")
	}
	ct = formatType(r.FormValue("format"))
	src = strings.ReplaceAll(r.FormValue("source"), "\r\n", "\n")
	switch {
	case ct == "":
		msg = "Choose a format."
	case len(src) > maxStatementSource:
		msg = "The statement is too long (at most 1 MiB)."
	case !utf8.ValidString(src):
		msg = "The statement must be UTF-8 text."
	}
	return
}

// handleStatementPreview renders the editor's text (htmx fragment).
func (s *Server) handleStatementPreview(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, ok := s.loadTask(w, r, rc)
	if !ok {
		return
	}
	lang, ct, src, msg := readSource(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if msg != "" {
		fmt.Fprintf(w, `<p class="bad">%s</p>`, template.HTMLEscapeString(i18n.T(adminLang(r), msg)))
		return
	}
	ctx, err := s.statementContext(r.Context(), t)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	doc, _ := statement.ParseIn(ct, []byte(src), lang)
	if len(doc.Warnings) > 0 {
		fmt.Fprintf(w, `<div class="alert"><b>%s</b><ul>`, template.HTMLEscapeString(i18n.T(adminLang(r), "Not understood (shown as written):")))
		for _, x := range doc.Warnings {
			fmt.Fprintf(w, "<li>%s</li>", template.HTMLEscapeString(x))
		}
		w.Write([]byte("</ul></div>"))
	}
	w.Write([]byte(s.statementHTML(t, doc, ctx, lang)))
}

// handleStatementPreviewPDF typesets the editor's text (a new tab).
func (s *Server) handleStatementPreviewPDF(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, ok := s.loadTask(w, r, rc)
	if !ok {
		return
	}
	lang, ct, src, msg := readSource(r)
	if msg != "" {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, msg)
		return
	}
	ctx, err := s.statementContext(r.Context(), t)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	doc, _ := statement.ParseIn(ct, []byte(src), lang)
	servePDF(w, t.Name+"-"+lang+"-preview.pdf", s.statementPDF(r.Context(), doc, ctx, lang))
}

// handleStatementPDF serves a saved statement as PDF (typeset from its
// source, or the uploaded file).
func (s *Server) handleStatementPDF(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, ok := s.loadTask(w, r, rc)
	if !ok {
		return
	}
	lang := r.PathValue("lang")
	stmts, err := s.q.ListStatements(r.Context(), t.ID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	for _, st := range stmts {
		if st.Language != lang {
			continue
		}
		if !statement.IsSource(st.ContentType) {
			b, _, err := blob.ReadLimited(r.Context(), s.blobs, st.Digest, 64<<20)
			if err != nil {
				s.internalError(w, r, rc, err)
				return
			}
			servePDF(w, t.Name+"-"+lang+".pdf", b)
			return
		}
		data, _, err := blob.ReadLimited(r.Context(), s.blobs, st.Digest, maxStatementSource*4)
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		ctx, err := s.statementContext(r.Context(), t)
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		doc, _ := statement.ParseIn(st.ContentType, data, lang)
		servePDF(w, t.Name+"-"+lang+".pdf", s.statementPDF(r.Context(), doc, ctx, lang))
		return
	}
	s.notFound(w, r, rc)
}

// servePDF serves a PDF that the editor may show in a frame.
func servePDF(w http.ResponseWriter, name string, b []byte) {
	h := w.Header()
	h.Set("X-Frame-Options", "SAMEORIGIN")
	h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'self'")
	h.Set("Content-Type", statement.TypePDF)
	h.Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", name))
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Length", strconv.Itoa(len(b)))
	w.Write(b)
}

// handleStatementSave stores the editor's text as the statement.
func (s *Server) handleStatementSave(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, ok := s.loadTask(w, r, rc)
	if !ok {
		return
	}
	lang, ct, src, msg := readSource(r)
	if msg == "" && !statementLangRe.MatchString(lang) {
		msg = "Invalid language code (e.g. en, es, es_MX)."
	}
	if msg != "" {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, msg)
		return
	}
	info, err := s.blobs.PutBytes(r.Context(), []byte(src))
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	if _, err := s.q.UpsertStatement(r.Context(), sqlc.UpsertStatementParams{TaskID: t.ID, Language: lang, Digest: info.Digest, ContentType: ct}); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	if err := s.setPrimary(r.Context(), t, lang, r.FormValue("primary") != ""); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("task", t.ID)
	rc.note("language", lang)
	rc.note("digest", info.Digest)
	if t.ContestID != nil {
		s.contestChanged(r.Context(), *t.ContestID, 0)
	}
	s.done(w, r, "/tasks/"+strconv.FormatInt(t.ID, 10)+"/statements/"+lang+"/edit", "Statement ("+lang+") saved.")
}

// setPrimary marks or unmarks a language as an official statement.
func (s *Server) setPrimary(ctx context.Context, t sqlc.Task, lang string, primary bool) error {
	has := contains(t.PrimaryStatements, lang)
	if has == primary {
		return nil
	}
	u := db.TaskToUpdate(t)
	if primary {
		u.PrimaryStatements = append(u.PrimaryStatements, lang)
	} else {
		var keep []string
		for _, l := range u.PrimaryStatements {
			if l != lang {
				keep = append(keep, l)
			}
		}
		u.PrimaryStatements = keep
	}
	_, err := s.q.UpdateTask(ctx, u)
	return err
}

// ---------------------------------------------------------------- examples

// exampleView is an example on the task page.
type exampleView struct {
	sqlc.TaskExample
	Input, Output string
	Truncated     bool
}

const exampleShown = 4 << 10

func (s *Server) exampleViews(ctx context.Context, taskID int64) ([]exampleView, error) {
	exs, err := s.q.ListTaskExamples(ctx, taskID)
	if err != nil {
		return nil, err
	}
	out := make([]exampleView, 0, len(exs))
	for _, e := range exs {
		v := exampleView{TaskExample: e}
		in, tin, err := blob.ReadLimited(ctx, s.blobs, e.InputDigest, exampleShown)
		if err != nil {
			return nil, err
		}
		o, tout, err := blob.ReadLimited(ctx, s.blobs, e.OutputDigest, exampleShown)
		if err != nil {
			return nil, err
		}
		v.Input, v.Output, v.Truncated = string(in), string(o), tin || tout
		out = append(out, v)
	}
	return out, nil
}

// exampleText reads an example part from a file or a text box.
func (s *Server) exampleText(r *http.Request, field string) (string, bool, error) {
	if f, fh, err := r.FormFile(field + "_file"); err == nil {
		defer f.Close()
		if fh.Size > 0 {
			info, err := s.blobs.Put(r.Context(), f)
			return info.Digest, true, err
		}
	}
	text := strings.ReplaceAll(r.FormValue(field), "\r\n", "\n")
	if strings.TrimSpace(text) == "" {
		return "", false, nil
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	info, err := s.blobs.PutBytes(r.Context(), []byte(text))
	return info.Digest, true, err
}

func (s *Server) handleExampleAdd(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, ok := s.loadTask(w, r, rc)
	if !ok {
		return
	}
	// The form has files; typed examples may come without them.
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := s.parseUpload(w, r); err != nil {
			s.errorPage(w, r, rc, http.StatusBadRequest, "Upload failed: "+err.Error())
			return
		}
	}
	in, okIn, err := s.exampleText(r, "input")
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	out, okOut, err := s.exampleText(r, "output")
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	if !okIn || !okOut {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "An example needs an input and an output.")
		return
	}
	e, err := s.q.InsertTaskExample(r.Context(), sqlc.InsertTaskExampleParams{TaskID: t.ID, InputDigest: in, OutputDigest: out,
		Note: strings.TrimSpace(r.FormValue("note"))})
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("task", t.ID)
	rc.note("example", e.ID)
	s.examplesChanged(r.Context(), t)
	s.done(w, r, "/tasks/"+strconv.FormatInt(t.ID, 10)+"#examples", "Example added.")
}

// handleExampleFromTestcase makes a testcase an example of its task.
func (s *Server) handleExampleFromTestcase(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, _ := pathID(r, "id")
	tc, err := s.q.GetTestcase(r.Context(), id)
	if err != nil {
		s.notFound(w, r, rc)
		return
	}
	ds, err := s.q.GetDataset(r.Context(), tc.DatasetID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	t, err := s.q.GetTask(r.Context(), ds.TaskID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	e, err := s.q.InsertTaskExample(r.Context(), sqlc.InsertTaskExampleParams{TaskID: t.ID, InputDigest: tc.InputDigest, OutputDigest: tc.OutputDigest})
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("task", t.ID)
	rc.note("example", e.ID)
	rc.note("testcase", tc.Codename)
	s.examplesChanged(r.Context(), t)
	s.done(w, r, "/datasets/"+strconv.FormatInt(ds.ID, 10)+"#testcases", "Testcase "+tc.Codename+" added to the statement's examples.")
}

func (s *Server) handleExampleUpdate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, ok := s.loadTask(w, r, rc)
	if !ok {
		return
	}
	eid, _ := pathID(r, "eid")
	exs, err := s.q.ListTaskExamples(r.Context(), t.ID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	idx := -1
	for i, e := range exs {
		if e.ID == eid {
			idx = i
		}
	}
	if idx < 0 {
		s.notFound(w, r, rc)
		return
	}
	switch r.PathValue("action") {
	case "delete":
		err = s.q.DeleteTaskExample(r.Context(), sqlc.DeleteTaskExampleParams{ID: eid, TaskID: t.ID})
	case "up", "down":
		j := idx - 1
		if r.PathValue("action") == "down" {
			j = idx + 1
		}
		if j < 0 || j >= len(exs) {
			break
		}
		// Renumber everything in the new order (positions may have gaps).
		exs[idx], exs[j] = exs[j], exs[idx]
		for i, e := range exs {
			if err = s.q.SetTaskExamplePosition(r.Context(), sqlc.SetTaskExamplePositionParams{ID: e.ID, TaskID: t.ID, Position: int32(i + 1)}); err != nil {
				break
			}
		}
	case "note":
		e := exs[idx]
		err = s.q.UpdateTaskExample(r.Context(), sqlc.UpdateTaskExampleParams{ID: eid, TaskID: t.ID, InputDigest: e.InputDigest,
			OutputDigest: e.OutputDigest, Note: strings.TrimSpace(r.FormValue("note"))})
	default:
		s.notFound(w, r, rc)
		return
	}
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("task", t.ID)
	rc.note("example", eid)
	s.examplesChanged(r.Context(), t)
	s.done(w, r, "/tasks/"+strconv.FormatInt(t.ID, 10)+"#examples", "Examples updated.")
}

// examplesChanged tells the contest pages to reload the task.
func (s *Server) examplesChanged(ctx context.Context, t sqlc.Task) {
	if t.ContestID != nil {
		s.contestChanged(ctx, *t.ContestID, 0)
	}
}
