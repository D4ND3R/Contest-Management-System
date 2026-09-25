package adminweb

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

type taskListItem struct {
	sqlc.Task
	Contest string
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	tasks, err := s.q.ListTasks(r.Context())
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	contests, err := s.q.ListContests(r.Context())
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	names := map[int64]string{}
	for _, c := range contests {
		names[c.ID] = c.Name
	}
	items := make([]taskListItem, len(tasks))
	for i, t := range tasks {
		items[i] = taskListItem{Task: t}
		if t.ContestID != nil {
			items[i].Contest = names[*t.ContestID]
		}
	}
	data := struct {
		Tasks    []taskListItem
		Contests []sqlc.Contest
	}{items, contests}
	s.render(w, "tasks", http.StatusOK, s.newPage(w, r, rc, "Tasks", "tasks", data))
}

// handleTaskCreate creates a task (optionally in a contest) with a default
// live dataset, like CMS.
func (s *Server) handleTaskCreate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	f := newForm(r)
	name := f.identifier("name", "Name")
	title := f.str("title")
	if title == "" {
		title = name
	}
	var contestID *int64
	if v := f.str("contest_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			f.fail("invalid contest")
		}
		contestID = &id
	}
	if f.err == nil {
		if _, err := s.q.GetTaskByName(r.Context(), name); err == nil {
			f.fail("a task named %q already exists", name)
		}
	}
	if f.err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, f.err.Error())
		return
	}
	var taskID int64
	err := db.InTx(r.Context(), s.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		tp := db.NewTaskParams(name, title)
		if contestID != nil {
			next, err := q.AdminNextTaskNum(r.Context(), contestID)
			if err != nil {
				return err
			}
			tp.ContestID, tp.Num = contestID, &next
		}
		t, err := q.CreateTask(r.Context(), tp)
		if err != nil {
			return err
		}
		taskID = t.ID
		ds, err := q.CreateDataset(r.Context(), db.NewDatasetParams(t.ID, "Default"))
		if err != nil {
			return err
		}
		return q.SetActiveDataset(r.Context(), sqlc.SetActiveDatasetParams{ID: t.ID, ActiveDatasetID: &ds.ID})
	})
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("task", taskID)
	if contestID != nil {
		s.contestChanged(r.Context(), *contestID, 0)
	}
	s.done(w, r, "/tasks/"+strconv.FormatInt(taskID, 10), "Task created with a default dataset.")
}

// taskPage is the data of the task page.
type taskPage struct {
	T           sqlc.UpdateTaskParams
	Task        sqlc.Task
	Contest     *sqlc.Contest
	Contests    []sqlc.Contest
	Statements  []sqlc.Statement
	Attachments []sqlc.Attachment
	Datasets    []sqlc.Dataset
	Tester      *testerForm
}

func (s *Server) taskPage(ctx context.Context, t sqlc.Task, u sqlc.UpdateTaskParams) (*taskPage, error) {
	d := &taskPage{T: u, Task: t}
	var err error
	if d.Contests, err = s.q.ListContests(ctx); err != nil {
		return nil, err
	}
	for i := range d.Contests {
		if t.ContestID != nil && d.Contests[i].ID == *t.ContestID {
			d.Contest = &d.Contests[i]
		}
	}
	if d.Statements, err = s.q.ListStatements(ctx, t.ID); err != nil {
		return nil, err
	}
	if d.Attachments, err = s.q.ListAttachments(ctx, t.ID); err != nil {
		return nil, err
	}
	if d.Datasets, err = s.q.ListDatasetsByTask(ctx, t.ID); err != nil {
		return nil, err
	}
	if d.Tester, err = s.testerForm(ctx, t); err != nil {
		return nil, err
	}
	return d, nil
}

func (s *Server) loadTask(w http.ResponseWriter, r *http.Request, rc *reqCtx) (sqlc.Task, bool) {
	id, _ := pathID(r, "id")
	t, err := s.q.GetTask(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r, rc)
		} else {
			s.internalError(w, r, rc, err)
		}
		return t, false
	}
	return t, true
}

func (s *Server) taskCrumbs(p *page, d *taskPage) *page {
	if d.Contest != nil {
		p.crumb("Contests", "/contests").crumb(d.Contest.Name, "/contests/"+strconv.FormatInt(d.Contest.ID, 10))
	} else {
		p.crumb("Tasks", "/tasks")
	}
	return p
}

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, ok := s.loadTask(w, r, rc)
	if !ok {
		return
	}
	d, err := s.taskPage(r.Context(), t, db.TaskToUpdate(t))
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	s.render(w, "task", http.StatusOK, s.taskCrumbs(s.newPage(w, r, rc, t.Name, "tasks", d), d))
}

func parseTask(f *form, u sqlc.UpdateTaskParams) sqlc.UpdateTaskParams {
	u.Name = f.identifier("name", "Name")
	u.Title = f.required("title", "Title")
	u.PrimaryStatements = f.list("primary_statements")
	u.SubmissionFormat = f.list("submission_format")
	for _, sf := range u.SubmissionFormat {
		if strings.ContainsAny(sf, "/\\") || strings.HasPrefix(sf, ".") {
			f.fail("invalid submission file name %q", sf)
		}
	}
	t := parseTokens(f)
	u.TokenMode, u.TokenMaxNumber, u.TokenMinIntervalS = t.Mode, t.MaxNumber, t.MinIntervalS
	u.TokenGenInitial, u.TokenGenNumber, u.TokenGenIntervalS, u.TokenGenMax = t.GenInitial, t.GenNumber, t.GenIntervalS, t.GenMax
	u.MaxSubmissionNumber = f.optInt32("max_submission_number", "Maximum submissions")
	u.MaxUserTestNumber = f.optInt32("max_user_test_number", "Maximum user tests")
	u.MinSubmissionIntervalS = f.optInt64("min_submission_interval_s", "Minimum interval between submissions")
	u.MinUserTestIntervalS = f.optInt64("min_user_test_interval_s", "Minimum interval between user tests")
	u.FeedbackLevel = f.oneOf("feedback_level", "Feedback level", "full", "restricted")
	u.ScorePrecision = f.int32("score_precision", "Score precision", 0)
	if u.ScorePrecision < 0 || u.ScorePrecision > 6 {
		f.fail("score precision must be between 0 and 6")
	}
	u.ScoreMode = f.oneOf("score_mode", "Score mode", "max_subtask", "max", "max_tokened_last")
	return u
}

func (s *Server) handleTaskUpdate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, ok := s.loadTask(w, r, rc)
	if !ok {
		return
	}
	f := newForm(r)
	u := parseTask(f, db.TaskToUpdate(t))
	if f.err == nil && u.Name != t.Name {
		if _, err := s.q.GetTaskByName(r.Context(), u.Name); err == nil {
			f.fail("a task named %q already exists", u.Name)
		}
	}
	if f.err != nil {
		d, err := s.taskPage(r.Context(), t, u)
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		s.formError(w, r, rc, "task", s.taskCrumbs(s.newPage(w, r, rc, t.Name, "tasks", d), d), f.err.Error())
		return
	}
	if _, err := s.q.UpdateTask(r.Context(), u); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("task", t.ID)
	if t.ContestID != nil {
		s.contestChanged(r.Context(), *t.ContestID, 0)
	}
	// Score mode and precision change aggregated scores.
	if u.ScoreMode != t.ScoreMode || u.ScorePrecision != t.ScorePrecision {
		if t.ActiveDatasetID != nil {
			s.datasetChangedWithAggregate(r.Context(), t.ID, *t.ActiveDatasetID)
		}
	}
	s.done(w, r, "/tasks/"+strconv.FormatInt(t.ID, 10), "Task saved.")
}

func (s *Server) handleTaskDelete(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, ok := s.loadTask(w, r, rc)
	if !ok {
		return
	}
	if r.FormValue("confirm") != t.Name {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Type the task name to confirm the deletion.")
		return
	}
	if err := s.q.DeleteTask(r.Context(), t.ID); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("task", t.ID)
	rc.note("name", t.Name)
	if t.ContestID != nil {
		s.contestChanged(r.Context(), *t.ContestID, 0)
	}
	s.done(w, r, "/tasks", "Task "+t.Name+" deleted.")
}

// ---------------------------------------------------------------- uploads

// parseUpload parses a multipart request bounded by the upload limit.
func (s *Server) parseUpload(w http.ResponseWriter, r *http.Request) error {
	limit := int64(s.cfg.MaxUploadBytes)
	if limit <= 0 {
		limit = s.maxUpload
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	return r.ParseMultipartForm(32 << 20)
}

// storeUpload stores the uploaded file field in the blob store.
func (s *Server) storeUpload(r *http.Request, field string) (digest, filename string, size int64, err error) {
	f, fh, err := r.FormFile(field)
	if err != nil {
		return "", "", 0, err
	}
	defer f.Close()
	info, err := s.blobs.Put(r.Context(), f)
	if err != nil {
		return "", "", 0, err
	}
	return info.Digest, path.Base(strings.ReplaceAll(fh.Filename, "\\", "/")), info.Size, nil
}

var statementLangRe = regexp.MustCompile(`^[a-z]{2,3}([_-][A-Za-z0-9]{2,8})?$`)

func (s *Server) handleStatementUpload(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, ok := s.loadTask(w, r, rc)
	if !ok {
		return
	}
	if err := s.parseUpload(w, r); err != nil {
		s.errorPage(w, r, rc, http.StatusBadRequest, "Upload failed: "+err.Error())
		return
	}
	lang := strings.TrimSpace(r.FormValue("language"))
	if !statementLangRe.MatchString(lang) {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Invalid language code (e.g. en, es, es_MX).")
		return
	}
	digest, name, _, err := s.storeUpload(r, "file")
	if err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Choose a file: "+err.Error())
		return
	}
	ct := "application/pdf"
	switch strings.ToLower(path.Ext(name)) {
	case ".html", ".htm":
		ct = "text/html; charset=utf-8"
	case ".txt":
		ct = "text/plain; charset=utf-8"
	case ".md":
		ct = "text/markdown; charset=utf-8"
	}
	if _, err := s.q.UpsertStatement(r.Context(), sqlc.UpsertStatementParams{TaskID: t.ID, Language: lang, Digest: digest, ContentType: ct}); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("task", t.ID)
	rc.note("digest", digest)
	if r.FormValue("primary") != "" && !contains(t.PrimaryStatements, lang) {
		u := db.TaskToUpdate(t)
		u.PrimaryStatements = append(u.PrimaryStatements, lang)
		if _, err := s.q.UpdateTask(r.Context(), u); err != nil {
			s.internalError(w, r, rc, err)
			return
		}
	}
	if t.ContestID != nil {
		s.contestChanged(r.Context(), *t.ContestID, 0)
	}
	s.done(w, r, "/tasks/"+strconv.FormatInt(t.ID, 10)+"#statements", "Statement ("+lang+") uploaded.")
}

func (s *Server) handleStatementDownload(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, ok := s.loadTask(w, r, rc)
	if !ok {
		return
	}
	stmts, err := s.q.ListStatements(r.Context(), t.ID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	for _, st := range stmts {
		if st.Language == r.PathValue("lang") {
			ext := ".pdf"
			if strings.HasPrefix(st.ContentType, "text/html") {
				ext = ".html"
			}
			s.serveBlob(w, r, rc, st.Digest, st.ContentType, t.Name+"-"+st.Language+ext, false)
			return
		}
	}
	s.notFound(w, r, rc)
}

func (s *Server) handleStatementDelete(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, ok := s.loadTask(w, r, rc)
	if !ok {
		return
	}
	lang := r.PathValue("lang")
	if err := s.q.DeleteStatement(r.Context(), sqlc.DeleteStatementParams{TaskID: t.ID, Language: lang}); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("task", t.ID)
	if t.ContestID != nil {
		s.contestChanged(r.Context(), *t.ContestID, 0)
	}
	s.done(w, r, "/tasks/"+strconv.FormatInt(t.ID, 10)+"#statements", "Statement ("+lang+") deleted.")
}

var safeFileRe = regexp.MustCompile(`^[A-Za-z0-9_.+-][A-Za-z0-9_.+ -]*$`)

func (s *Server) handleAttachmentUpload(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, ok := s.loadTask(w, r, rc)
	if !ok {
		return
	}
	if err := s.parseUpload(w, r); err != nil {
		s.errorPage(w, r, rc, http.StatusBadRequest, "Upload failed: "+err.Error())
		return
	}
	digest, name, _, err := s.storeUpload(r, "file")
	if err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Choose a file: "+err.Error())
		return
	}
	if v := strings.TrimSpace(r.FormValue("filename")); v != "" {
		name = v
	}
	if !safeFileRe.MatchString(name) || name == "." || name == ".." {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Invalid file name.")
		return
	}
	if _, err := s.q.UpsertAttachment(r.Context(), sqlc.UpsertAttachmentParams{TaskID: t.ID, Filename: name, Digest: digest}); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("task", t.ID)
	if t.ContestID != nil {
		s.contestChanged(r.Context(), *t.ContestID, 0)
	}
	s.done(w, r, "/tasks/"+strconv.FormatInt(t.ID, 10)+"#attachments", "Attachment "+name+" uploaded.")
}

func (s *Server) handleAttachmentDownload(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, ok := s.loadTask(w, r, rc)
	if !ok {
		return
	}
	atts, err := s.q.ListAttachments(r.Context(), t.ID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	for _, a := range atts {
		if a.Filename == r.PathValue("file") {
			s.serveBlob(w, r, rc, a.Digest, "application/octet-stream", a.Filename, true)
			return
		}
	}
	s.notFound(w, r, rc)
}

func (s *Server) handleAttachmentDelete(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, ok := s.loadTask(w, r, rc)
	if !ok {
		return
	}
	name := r.PathValue("file")
	if err := s.q.DeleteAttachment(r.Context(), sqlc.DeleteAttachmentParams{TaskID: t.ID, Filename: name}); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("task", t.ID)
	if t.ContestID != nil {
		s.contestChanged(r.Context(), *t.ContestID, 0)
	}
	s.done(w, r, "/tasks/"+strconv.FormatInt(t.ID, 10)+"#attachments", "Attachment "+name+" deleted.")
}

// serveBlob streams a blob. Downloads are forced as attachments except for
// statements, which are shown inline inside a sandboxing CSP.
func (s *Server) serveBlob(w http.ResponseWriter, r *http.Request, rc *reqCtx, digest, contentType, filename string, attachment bool) {
	rd, err := s.blobs.Open(r.Context(), digest)
	if err != nil {
		if errors.Is(err, blob.ErrNotFound) {
			s.notFound(w, r, rc)
		} else {
			s.internalError(w, r, rc, err)
		}
		return
	}
	defer rd.Close()
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src data: 'self'; style-src 'unsafe-inline'")
	disp := "inline"
	if attachment {
		disp = "attachment"
	}
	h.Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": filename}))
	h.Set("Cache-Control", "private, max-age=0")
	io.Copy(w, rd)
}
