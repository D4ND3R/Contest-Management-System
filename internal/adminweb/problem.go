package adminweb

// The two windows of a task (docs/en/creating-a-problem.md, D105):
// Configuration — a package that fills everything, the options form
// (problem.yaml in the browser) and the files one by one — and Testcases —
// adding or importing testcases, their list and test submissions against
// them. Both show one dataset of the task: the live one, or ?dataset=N.

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/problempkg"
	"github.com/D4ND3R/Contest-Management-System/internal/scoring"
	"github.com/D4ND3R/Contest-Management-System/internal/statement"
	"github.com/D4ND3R/Contest-Management-System/internal/tasktypes"
	"github.com/jackc/pgx/v5"
)

// problemHead is what both windows show on top: the task, the window tabs,
// the dataset shown and what the task still misses.
type problemHead struct {
	Task     sqlc.Task
	Contest  *sqlc.Contest
	Dataset  sqlc.Dataset
	Live     bool
	Datasets []sqlc.Dataset
	Window   string // "config" or "tests"
	// Query selects the dataset in the window links ("" for the live one).
	Query string
	Tests int64
	// Todo are the setup steps still missing (all done: ready).
	Todo []setupStep
}

// problemURL is the address of a window of task t ("config" or "tests")
// showing the dataset datasetID (0 or the live one need no parameter).
func problemURL(t sqlc.Task, datasetID int64, window string) string {
	u := "/tasks/" + strconv.FormatInt(t.ID, 10)
	if window == "tests" {
		u += "/tests"
	}
	if datasetID != 0 && (t.ActiveDatasetID == nil || *t.ActiveDatasetID != datasetID) {
		u += "?dataset=" + strconv.FormatInt(datasetID, 10)
	}
	return u
}

// datasetURL is problemURL for a dataset whose task is not at hand.
func (s *Server) datasetURL(ctx context.Context, d sqlc.Dataset, window string) string {
	t, err := s.q.GetTask(ctx, d.TaskID)
	if err != nil {
		return "/tasks/" + strconv.FormatInt(d.TaskID, 10)
	}
	return problemURL(t, d.ID, window)
}

// wantedDataset is the dataset a request asks for (?dataset= or the
// dataset_id field of a form), 0 for the live one.
func wantedDataset(r *http.Request) int64 {
	v := r.URL.Query().Get("dataset")
	if v == "" {
		v = r.FormValue("dataset_id")
	}
	id, _ := strconv.ParseInt(v, 10, 64)
	return id
}

// problemHead loads the top of a window. A task always has a dataset; one
// that lost all of them gets an empty live dataset back.
func (s *Server) problemHead(ctx context.Context, t sqlc.Task, want int64, window string) (*problemHead, error) {
	h := &problemHead{Task: t, Window: window}
	var err error
	if h.Datasets, err = s.q.ListDatasetsByTask(ctx, t.ID); err != nil {
		return nil, err
	}
	if len(h.Datasets) == 0 {
		err := db.InTx(ctx, s.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
			ds, err := q.CreateDataset(ctx, db.NewDatasetParams(t.ID, "Default"))
			if err != nil {
				return err
			}
			h.Datasets = []sqlc.Dataset{ds}
			h.Task.ActiveDatasetID = &ds.ID
			return q.SetActiveDataset(ctx, sqlc.SetActiveDatasetParams{ID: t.ID, ActiveDatasetID: &ds.ID})
		})
		if err != nil {
			return nil, err
		}
	}
	t = h.Task
	pick := func(id int64) bool {
		for _, d := range h.Datasets {
			if d.ID == id {
				h.Dataset = d
				return true
			}
		}
		return false
	}
	if !(want != 0 && pick(want)) && !(t.ActiveDatasetID != nil && pick(*t.ActiveDatasetID)) {
		h.Dataset = h.Datasets[0]
	}
	h.Live = t.ActiveDatasetID != nil && *t.ActiveDatasetID == h.Dataset.ID
	if !h.Live {
		h.Query = "?dataset=" + strconv.FormatInt(h.Dataset.ID, 10)
	}
	if t.ContestID != nil {
		if c, err := s.q.GetContest(ctx, *t.ContestID); err == nil {
			h.Contest = &c
		}
	}
	if h.Tests, err = s.q.CountTestcases(ctx, h.Dataset.ID); err != nil {
		return nil, err
	}
	steps, err := s.setupSteps(ctx, t, h.Contest)
	if err != nil {
		return nil, err
	}
	for _, st := range steps {
		if !st.Done {
			h.Todo = append(h.Todo, st)
		}
	}
	return h, nil
}

// setupStep is one thing a task needs before a contest: what, whether it
// is done, a short state and where to do it.
type setupStep struct {
	Title, State, URL string
	Done              bool
}

// setupSteps checks, in order, what a task needs before a contest: a
// title, a statement, testcases, a complete judging configuration, a
// scoring, a reference solution with the full score, and a contest. The
// words are translation keys; everything is checked on the live dataset.
func (s *Server) setupSteps(ctx context.Context, t sqlc.Task, contest *sqlc.Contest) ([]setupStep, error) {
	base := "/tasks/" + strconv.FormatInt(t.ID, 10)
	stmts, err := s.q.ListStatements(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	steps := []setupStep{
		{Title: "Name and title", Done: t.Title != "", URL: base + "#options"},
		{Title: "Statement", Done: len(stmts) > 0, URL: base + "#files"},
	}
	if len(stmts) > 0 {
		steps[1].State = strconv.Itoa(len(stmts))
	}
	if t.ActiveDatasetID == nil {
		return append(steps, setupStep{Title: "Testcases", URL: base + "/tests"}), nil
	}
	ds, err := s.q.GetDataset(ctx, *t.ActiveDatasetID)
	if err != nil {
		return nil, err
	}
	tcs, err := s.q.ListTestcases(ctx, ds.ID)
	if err != nil {
		return nil, err
	}
	steps = append(steps, setupStep{Title: "Testcases", Done: len(tcs) > 0, State: strconv.Itoa(len(tcs)), URL: base + "/tests#add"})
	managers, err := s.q.ListManagers(ctx, ds.ID)
	if err != nil {
		return nil, err
	}
	_, missing := requiredManagers(ds, managers)
	steps = append(steps, setupStep{Title: "Type, limits and checker", Done: len(missing) == 0, State: ds.TaskType, URL: base + "#options"})
	codes, pub := make([]string, len(tcs)), make([]bool, len(tcs))
	for i, tc := range tcs {
		codes[i], pub[i] = tc.Codename, tc.Public
	}
	maxScore := 0.0
	score := setupStep{Title: "Scoring and subtasks", URL: base + "#scoring"}
	if st, err := scoring.New(ds.ScoreType, ds.ScoreTypeParams, codes, pub, int(t.ScorePrecision)); err == nil {
		maxScore = st.MaxScore()
		score.Done, score.State = maxScore > 0, strconv.FormatFloat(maxScore, 'f', -1, 64)
	}
	steps = append(steps, score)
	solved, err := s.q.TesterFullScore(ctx, sqlc.TesterFullScoreParams{TaskID: t.ID, DatasetID: ds.ID, MaxScore: maxScore - 1e-9})
	if err != nil {
		return nil, err
	}
	steps = append(steps, setupStep{Title: "Reference solution with the full score", Done: solved && maxScore > 0, URL: base + "/tests#runs"})
	in := setupStep{Title: "In a contest", Done: contest != nil, URL: "/contests"}
	if contest != nil {
		in.State, in.URL = contest.Name, "/contests/"+strconv.FormatInt(contest.ID, 10)+"/tasks"
	}
	return append(steps, in), nil
}

// requiredManagers are the managers the dataset's configuration needs and
// those of them not uploaded ("checker" is met by checker.cpp, too).
func requiredManagers(d sqlc.Dataset, managers []sqlc.Manager) (required, missing []string) {
	required = tasktypes.RequiredManagers(d.TaskType, d.TaskTypeParams)
	have := map[string]bool{}
	for _, m := range managers {
		have[strings.TrimSuffix(m.Filename, path.Ext(m.Filename))], have[m.Filename] = true, true
	}
	for _, req := range required {
		if base := strings.TrimSuffix(req, ".<ext>"); !have[base] && !have[req] {
			missing = append(missing, req)
		}
	}
	return required, missing
}

func (s *Server) taskCrumbs(p *page, h *problemHead) *page {
	if h.Contest != nil {
		p.crumb("Contests", "/contests").crumb(h.Contest.Name, "/contests/"+strconv.FormatInt(h.Contest.ID, 10))
	} else {
		p.crumb("Tasks", "/tasks")
	}
	return p
}

// ---------------------------------------------------------------- configuration

// configPage is the Configuration window.
type configPage struct {
	Head *problemHead
	// T and D are the options form (the saved values, or the posted ones
	// after an error).
	T          sqlc.UpdateTaskParams
	D          sqlc.UpdateDatasetParams
	TF         typeFields
	Editor     *scoreEditor
	TaskTypes  []string
	ScoreTypes []string
	ParamsJSON string
	ScoreJSON  string
	ScoreError string
	MaxScore   float64
	Subtasks   []float64
	Languages  []*langs.Language
	// The files of the task and of the dataset shown.
	Statements  []sqlc.Statement
	Managers    []sqlc.Manager
	Attachments []sqlc.Attachment
	Required    []string
	Missing     []string
	// YAML is problem.yaml of the saved task and dataset (or the text
	// posted with an error, YAMLError).
	YAML      string
	YAMLError string
}

func (s *Server) configPage(ctx context.Context, h *problemHead, u sqlc.UpdateTaskParams, du sqlc.UpdateDatasetParams, paramsText, scoreText string) (*configPage, error) {
	t, d := h.Task, h.Dataset
	p := &configPage{Head: h, T: u, D: du, TaskTypes: tasktypes.SortedNames(), ScoreTypes: scoreTypes,
		ParamsJSON: paramsText, ScoreJSON: scoreText, Languages: s.langs.All()}
	if p.ParamsJSON == "" {
		p.ParamsJSON = prettyJSON(du.TaskTypeParams)
	}
	if p.ScoreJSON == "" {
		p.ScoreJSON = prettyJSON(du.ScoreTypeParams)
	}
	p.TF = typeFieldsOf(du.TaskTypeParams)
	var err error
	if p.Statements, err = s.q.ListStatements(ctx, t.ID); err != nil {
		return nil, err
	}
	if p.Attachments, err = s.q.ListAttachments(ctx, t.ID); err != nil {
		return nil, err
	}
	if p.Managers, err = s.q.ListManagers(ctx, d.ID); err != nil {
		return nil, err
	}
	p.Required, p.Missing = requiredManagers(d, p.Managers)
	tcs, err := s.q.ListTestcases(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	codes, pub := make([]string, len(tcs)), make([]bool, len(tcs))
	for i, tc := range tcs {
		codes[i], pub[i] = tc.Codename, tc.Public
	}
	p.Editor = editorFromParams(d.ID, du.ScoreType, du.ScoreTypeParams, codes)
	if st, err := scoring.New(d.ScoreType, d.ScoreTypeParams, codes, pub, int(t.ScorePrecision)); err != nil {
		p.ScoreError = err.Error()
	} else {
		p.MaxScore, p.Subtasks = st.MaxScore(), st.SubtaskMaxScores()
	}
	p.YAML = problemYAML(t, d, codes, pub)
	return p, nil
}

// problemYAML is problem.yaml of a task and one of its datasets.
func problemYAML(t sqlc.Task, d sqlc.Dataset, codes []string, pub []bool) string {
	c, err := problempkg.ConfigFromCMS(t, d, codes, pub)
	if err != nil {
		return "# " + err.Error() + "\n"
	}
	b, err := c.Marshal()
	if err != nil {
		return "# " + err.Error() + "\n"
	}
	return string(b)
}

// renderConfig shows the Configuration window, with msg as the error of a
// rejected form (then with status 422).
func (s *Server) renderConfig(w http.ResponseWriter, r *http.Request, rc *reqCtx, p *configPage, msg string) {
	pg := s.taskCrumbs(s.newPage(w, r, rc, p.Head.Task.Name, "tasks", p), p.Head)
	if msg != "" {
		s.formError(w, r, rc, "task", pg, msg)
		return
	}
	s.render(w, "task", http.StatusOK, pg)
}

// handleTask shows the Configuration window.
func (s *Server) handleTask(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, ok := s.loadTask(w, r, rc)
	if !ok {
		return
	}
	h, err := s.problemHead(r.Context(), t, wantedDataset(r), "config")
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	p, err := s.configPage(r.Context(), h, db.TaskToUpdate(h.Task), db.DatasetToUpdate(h.Dataset), "", "")
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	s.renderConfig(w, r, rc, p, "")
}

// loadProblem loads the task of the path and the dataset the request asks
// for, as a window head.
func (s *Server) loadProblem(w http.ResponseWriter, r *http.Request, rc *reqCtx, window string) (*problemHead, bool) {
	t, ok := s.loadTask(w, r, rc)
	if !ok {
		return nil, false
	}
	want := wantedDataset(r)
	h, err := s.problemHead(r.Context(), t, want, window)
	if err != nil {
		s.internalError(w, r, rc, err)
		return nil, false
	}
	if want != 0 && want != h.Dataset.ID {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Unknown dataset.")
		return nil, false
	}
	return h, true
}

// handleTaskOptions saves the options form: the task's settings and the
// dataset's in one transaction.
func (s *Server) handleTaskOptions(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	h, ok := s.loadProblem(w, r, rc, "config")
	if !ok {
		return
	}
	t, d := h.Task, h.Dataset
	tcs, err := s.q.ListTestcases(r.Context(), d.ID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	codes, pub := make([]string, len(tcs)), make([]bool, len(tcs))
	for i, tc := range tcs {
		codes[i], pub[i] = tc.Codename, tc.Public
	}
	f := newForm(r)
	u := s.parseTaskForm(r.Context(), f, t)
	du, paramsText, scoreText, editor := parseDataset(f, db.DatasetToUpdate(d), codes)
	if f.err == nil && len(tcs) > 0 {
		if _, err := scoring.New(du.ScoreType, du.ScoreTypeParams, codes, pub, 0); err != nil {
			f.fail("score type parameters: %v", err)
		}
	}
	if f.err != nil {
		p, err := s.configPage(r.Context(), h, u, du, paramsText, scoreText)
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		if editor != nil {
			p.Editor = editor
		}
		s.renderConfig(w, r, rc, p, f.err.Error())
		return
	}
	err = db.InTx(r.Context(), s.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		if _, err := q.UpdateTask(r.Context(), u); err != nil {
			return err
		}
		_, err := q.UpdateDataset(r.Context(), du)
		return err
	})
	if err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Could not save: "+err.Error())
		return
	}
	rc.target("task", t.ID)
	rc.note("dataset", d.ID)
	s.taskSaved(r.Context(), t, u, d.ID)
	s.done(w, r, problemURL(t, d.ID, "config")+"#options", "Options saved. Existing submissions keep their results until you reevaluate them.")
}

// parseTaskForm reads the task's settings from a form and checks what
// needs the database (languages, a free name).
func (s *Server) parseTaskForm(ctx context.Context, f *form, t sqlc.Task) sqlc.UpdateTaskParams {
	u := parseTask(f, db.TaskToUpdate(t))
	for _, l := range u.Languages {
		if _, ok := s.langs.Get(l); !ok {
			f.fail("unknown language %q", l)
		}
	}
	if f.err == nil && u.Name != t.Name {
		if _, err := s.q.GetTaskByName(ctx, u.Name); err == nil {
			f.fail("a task named %q already exists", u.Name)
		}
	}
	return u
}

// taskSaved tells the contest and the dispatcher that a task (and one of
// its datasets) changed; score mode and precision change aggregates.
func (s *Server) taskSaved(ctx context.Context, before sqlc.Task, u sqlc.UpdateTaskParams, datasetID int64) {
	if before.ContestID != nil {
		s.contestChanged(ctx, *before.ContestID, 0)
	}
	if (u.ScoreMode != before.ScoreMode || u.ScorePrecision != before.ScorePrecision) && before.ActiveDatasetID != nil {
		s.datasetChangedWithAggregate(ctx, before.ID, *before.ActiveDatasetID)
	}
	if datasetID != 0 {
		s.datasetChanged(ctx, before.ID, datasetID)
	}
}

// handleProblemYAML downloads problem.yaml of the task and dataset shown.
func (s *Server) handleProblemYAML(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	h, ok := s.loadProblem(w, r, rc, "config")
	if !ok {
		return
	}
	tcs, err := s.q.ListTestcases(r.Context(), h.Dataset.ID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	codes, pub := make([]string, len(tcs)), make([]bool, len(tcs))
	for i, tc := range tcs {
		codes[i], pub[i] = tc.Codename, tc.Public
	}
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "problem.yaml"}))
	w.Write([]byte(problemYAML(h.Task, h.Dataset, codes, pub)))
}

// handleProblemYAMLSave applies problem.yaml edited as text: the same
// options as the form, checked like a package's.
func (s *Server) handleProblemYAMLSave(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	h, ok := s.loadProblem(w, r, rc, "config")
	if !ok {
		return
	}
	t, d := h.Task, h.Dataset
	tcs, err := s.q.ListTestcases(r.Context(), d.ID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	codes := make([]string, len(tcs))
	for i, tc := range tcs {
		codes[i] = tc.Codename
	}
	text := strings.ReplaceAll(r.FormValue("yaml"), "\r\n", "\n")
	tr := adminTr(r)
	var problems []string
	c, err := problempkg.ParseConfig([]byte(text))
	if err != nil {
		problems = []string{err.Error()}
	} else {
		problems = c.Validate(codes)
	}
	if len(problems) == 0 {
		err = db.InTx(r.Context(), s.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
			return problempkg.ApplyConfig(r.Context(), q, t, d, c, codes)
		})
		switch {
		case errors.Is(err, problempkg.ErrNameTaken):
			problems = []string{tr("a task named %q already exists", c.Name)}
		case err != nil:
			s.internalError(w, r, rc, err)
			return
		}
	}
	if len(problems) > 0 {
		p, err := s.configPage(r.Context(), h, db.TaskToUpdate(t), db.DatasetToUpdate(d), "", "")
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		p.YAML, p.YAMLError = text, strings.Join(problems, "; ")
		s.renderConfig(w, r, rc, p, "problem.yaml: "+p.YAMLError)
		return
	}
	rc.target("task", t.ID)
	rc.note("dataset", d.ID)
	s.taskSaved(r.Context(), t, c.TaskUpdate(t, false), d.ID)
	s.done(w, r, problemURL(t, d.ID, "config")+"#options", "problem.yaml saved. Existing submissions keep their results until you reevaluate them.")
}

// ---------------------------------------------------------------- files

// managerKinds are the managers known by their name without extension,
// with what the Files table calls them.
var managerKinds = map[string]string{"checker": "Checker", "interactor": "Interactor", "manager": "Manager",
	"grader": "Grader", "stub": "Stub"}

// managerKind is what a manager file is, as a translation key.
func managerKind(name string) string {
	ext := path.Ext(name)
	if k := managerKinds[strings.TrimSuffix(name, ext)]; k != "" {
		return k
	}
	if ext == ".h" || ext == ".hpp" {
		return "Header"
	}
	return "Other judging file"
}

// fileKind guesses what an uploaded file is when the form says
// "automatic": a checker, grader, stub, interactor, manager or header is a
// manager; a Markdown, LaTeX, PDF or HTML file named after a language (or
// uploaded with one) is a statement; anything else is an attachment.
func fileKind(name, lang string) string {
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	switch {
	case managerKinds[stem] != "" || ext == ".h" || ext == ".hpp":
		return "manager"
	case statement.TypeForName(name) != "" && (lang != "" || statementLangRe.MatchString(stem) || stem == "statement"):
		return "statement"
	}
	return "attachment"
}

// handleTaskFiles uploads files one by one (or several at once): each is a
// statement, a manager of the dataset shown or an attachment.
func (s *Server) handleTaskFiles(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	if err := s.parseUpload(w, r); err != nil {
		s.errorPage(w, r, rc, http.StatusBadRequest, "Upload failed: "+err.Error())
		return
	}
	h, ok := s.loadProblem(w, r, rc, "config")
	if !ok {
		return
	}
	t, d := h.Task, h.Dataset
	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Choose at least one file.")
		return
	}
	kind := r.FormValue("kind")
	lang := strings.TrimSpace(r.FormValue("language"))
	if lang != "" && !statementLangRe.MatchString(lang) {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Invalid language code (e.g. en, es, es_MX).")
		return
	}
	type upload struct{ name, kind, lang, ct, digest string }
	var ups []upload
	for _, fh := range files {
		name := path.Base(strings.ReplaceAll(fh.Filename, "\\", "/"))
		if !safeFileRe.MatchString(name) || name == "." || name == ".." {
			s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Invalid file name: "+fh.Filename)
			return
		}
		up := upload{name: name, kind: kind}
		switch up.kind {
		case "statement", "manager", "attachment":
		default:
			up.kind = fileKind(name, lang)
		}
		if up.kind == "statement" {
			stem := strings.TrimSuffix(name, path.Ext(name))
			up.lang = lang
			if up.lang == "" && statementLangRe.MatchString(stem) {
				up.lang = stem
			}
			if up.lang == "" {
				s.errorPage(w, r, rc, http.StatusUnprocessableEntity, adminTr(r)("Say the language of the statement %s (e.g. es).", name))
				return
			}
			if up.ct = statement.TypeForName(name); up.ct == "" {
				s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "A statement is a PDF, Markdown (.md), LaTeX (.tex), HTML or text file.")
				return
			}
		}
		f, err := fh.Open()
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		info, err := s.blobs.Put(r.Context(), f)
		f.Close()
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		up.digest = info.Digest
		ups = append(ups, up)
	}
	var names []string
	content, managers := false, false
	err := db.InTx(r.Context(), s.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		for _, up := range ups {
			var err error
			switch up.kind {
			case "statement":
				content = true
				_, err = q.UpsertStatement(r.Context(), sqlc.UpsertStatementParams{TaskID: t.ID, Language: up.lang, Digest: up.digest, ContentType: up.ct})
				names = append(names, up.name+" ("+up.lang+")")
			case "manager":
				managers = true
				_, err = q.UpsertManager(r.Context(), sqlc.UpsertManagerParams{DatasetID: d.ID, Filename: up.name, Digest: up.digest})
				names = append(names, up.name)
			default:
				content = true
				_, err = q.UpsertAttachment(r.Context(), sqlc.UpsertAttachmentParams{TaskID: t.ID, Filename: up.name, Digest: up.digest})
				names = append(names, up.name)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("task", t.ID)
	rc.note("files", strings.Join(names, ", "))
	if content && t.ContestID != nil {
		s.statementChanged(r.Context(), t)
	}
	if managers {
		s.datasetChanged(r.Context(), t.ID, d.ID)
	}
	s.done(w, r, problemURL(t, d.ID, "config")+"#files", "Uploaded %s.", strings.Join(names, ", "))
}
