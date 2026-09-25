package adminweb

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/dispatcher"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/D4ND3R/Contest-Management-System/internal/scoring"
	"github.com/D4ND3R/Contest-Management-System/internal/tasktypes"
	"github.com/jackc/pgx/v5"
)

// datasetChangedWithAggregate asks the dispatcher to reload the dataset
// and recompute the task aggregates (score mode or live dataset changes).
func (s *Server) datasetChangedWithAggregate(ctx context.Context, taskID, datasetID int64) {
	if err := s.queue.Notify(ctx, queue.Event{Kind: queue.EventDatasetChanged, TaskID: taskID, DatasetID: datasetID}); err != nil {
		s.log.Warn("notify dispatcher", "error", err)
	}
}

func (s *Server) handleDatasetCreate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, ok := s.loadTask(w, r, rc)
	if !ok {
		return
	}
	desc := strings.TrimSpace(r.FormValue("description"))
	if desc == "" {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "A description is required.")
		return
	}
	var src *sqlc.Dataset
	if v := r.FormValue("clone_from"); v != "" {
		id, _ := strconv.ParseInt(v, 10, 64)
		d, err := s.q.GetDataset(r.Context(), id)
		if err != nil || d.TaskID != t.ID {
			s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Unknown dataset to clone.")
			return
		}
		src = &d
	}
	var newID int64
	err := db.InTx(r.Context(), s.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		p := db.NewDatasetParams(t.ID, desc)
		if src != nil {
			p = sqlc.CreateDatasetParams{TaskID: t.ID, Description: desc, TimeLimitMs: src.TimeLimitMs,
				WallTimeLimitMs: src.WallTimeLimitMs, MemoryLimitBytes: src.MemoryLimitBytes, OutputLimitBytes: src.OutputLimitBytes,
				ProcessLimit: src.ProcessLimit, SourceSizeLimitBytes: src.SourceSizeLimitBytes, TaskType: src.TaskType,
				TaskTypeParams: src.TaskTypeParams, ScoreType: src.ScoreType, ScoreTypeParams: src.ScoreTypeParams}
		}
		d, err := q.CreateDataset(r.Context(), p)
		if err != nil {
			return err
		}
		newID = d.ID
		if src != nil {
			return q.CloneDatasetContents(r.Context(), sqlc.CloneDatasetContentsParams{Dst: d.ID, Src: src.ID})
		}
		return nil
	})
	if err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Could not create the dataset: "+err.Error())
		return
	}
	rc.target("dataset", newID)
	s.done(w, r, "/datasets/"+strconv.FormatInt(newID, 10), "Dataset created.")
}

// datasetPage is the data of the dataset page.
type datasetPage struct {
	D          sqlc.UpdateDatasetParams
	Dataset    sqlc.Dataset
	Task       sqlc.Task
	Live       bool
	Managers   []sqlc.Manager
	Testcases  []sqlc.Testcase
	TaskTypes  []string
	ScoreTypes []string
	Defaults   string
	ParamsJSON string
	ScoreJSON  string
	ScoreError string
	MaxScore   float64
	Subtasks   []float64
	Required   []string
	Missing    []string
	OtherSets  []sqlc.Dataset
	TF         typeFields
	Editor     *scoreEditor
}

var scoreTypes = []string{"Sum", "GroupMin", "GroupMul", "GroupThreshold"}

func (s *Server) datasetPage(ctx context.Context, d sqlc.Dataset, u sqlc.UpdateDatasetParams, paramsText, scoreText string) (*datasetPage, error) {
	t, err := s.q.GetTask(ctx, d.TaskID)
	if err != nil {
		return nil, err
	}
	p := &datasetPage{D: u, Dataset: d, Task: t, Live: t.ActiveDatasetID != nil && *t.ActiveDatasetID == d.ID,
		TaskTypes: tasktypes.SortedNames(), ScoreTypes: scoreTypes, ParamsJSON: paramsText, ScoreJSON: scoreText}
	defaults, _ := json.Marshal(tasktypes.DefaultParams)
	p.Defaults = string(defaults)
	if p.ParamsJSON == "" {
		p.ParamsJSON = prettyJSON(u.TaskTypeParams)
	}
	if p.ScoreJSON == "" {
		p.ScoreJSON = prettyJSON(u.ScoreTypeParams)
	}
	p.TF = typeFieldsOf(u.TaskTypeParams)
	if p.Managers, err = s.q.ListManagers(ctx, d.ID); err != nil {
		return nil, err
	}
	if p.Testcases, err = s.q.ListTestcases(ctx, d.ID); err != nil {
		return nil, err
	}
	all, err := s.q.ListDatasetsByTask(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	for _, o := range all {
		if o.ID != d.ID {
			p.OtherSets = append(p.OtherSets, o)
		}
	}
	codes, pub := make([]string, len(p.Testcases)), make([]bool, len(p.Testcases))
	for i, tc := range p.Testcases {
		codes[i], pub[i] = tc.Codename, tc.Public
	}
	p.Editor = editorFromParams(d.ID, u.ScoreType, u.ScoreTypeParams, codes)
	if st, err := scoring.New(d.ScoreType, d.ScoreTypeParams, codes, pub, int(t.ScorePrecision)); err != nil {
		p.ScoreError = err.Error()
	} else {
		p.MaxScore, p.Subtasks = st.MaxScore(), st.SubtaskMaxScores()
	}
	p.Required = tasktypes.RequiredManagers(d.TaskType, d.TaskTypeParams)
	have := map[string]bool{}
	for _, m := range p.Managers {
		have[strings.TrimSuffix(m.Filename, path.Ext(m.Filename))] = true
		have[m.Filename] = true
	}
	for _, req := range p.Required {
		base := strings.TrimSuffix(req, ".<ext>")
		if !have[base] && !have[req] {
			p.Missing = append(p.Missing, req)
		}
	}
	return p, nil
}

func prettyJSON(raw json.RawMessage) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func (s *Server) loadDataset(w http.ResponseWriter, r *http.Request, rc *reqCtx) (sqlc.Dataset, bool) {
	id, _ := pathID(r, "id")
	d, err := s.q.GetDataset(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r, rc)
		} else {
			s.internalError(w, r, rc, err)
		}
		return d, false
	}
	return d, true
}

func (s *Server) datasetCrumbs(p *page, d *datasetPage) *page {
	return p.crumb("Tasks", "/tasks").crumb(d.Task.Name, "/tasks/"+strconv.FormatInt(d.Task.ID, 10))
}

func (s *Server) handleDataset(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	d, ok := s.loadDataset(w, r, rc)
	if !ok {
		return
	}
	p, err := s.datasetPage(r.Context(), d, db.DatasetToUpdate(d), "", "")
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	s.render(w, "dataset", http.StatusOK, s.datasetCrumbs(s.newPage(w, r, rc, p.Task.Name+" · "+d.Description, "tasks", p), p))
}

// parseDataset reads the dataset form; codes are the dataset's testcases
// (for the score editor, which is returned when it was used).
func parseDataset(f *form, u sqlc.UpdateDatasetParams, codes []string) (sqlc.UpdateDatasetParams, string, string, *scoreEditor) {
	u.Description = f.required("description", "Description")
	u.Autojudge = f.check("autojudge")
	u.TimeLimitMs = f.secondsToMs("time_limit", "Time limit")
	u.WallTimeLimitMs = f.secondsToMs("wall_time_limit", "Wall time limit")
	u.MemoryLimitBytes = f.mib("memory_limit_mib", "Memory limit")
	if out := f.mib("output_limit_mib", "Output limit"); out != nil {
		u.OutputLimitBytes = *out
	} else {
		u.OutputLimitBytes = 64 << 20
	}
	u.ProcessLimit = f.int32("process_limit", "Process limit", 1)
	if u.ProcessLimit < 1 || u.ProcessLimit > 256 {
		f.fail("the process limit must be between 1 and 256")
	}
	u.SourceSizeLimitBytes = f.kib("source_size_limit_kib", "Source size limit")
	u.TaskType = f.oneOf("task_type", "Task type", tasktypes.SortedNames()...)
	paramsText := f.str("task_type_params")
	if !f.check("raw_params") {
		// The structured fields of the chosen type (the default).
		paramsText = string(buildTypeParams(f, u.TaskType))
	}
	if paramsText == "" {
		paramsText = "{}"
	}
	if !json.Valid([]byte(paramsText)) {
		f.fail("task type parameters are not valid JSON")
	} else {
		u.TaskTypeParams = compactJSON(paramsText)
		if err := tasktypes.ValidateParams(u.TaskType, u.TaskTypeParams); err != nil {
			f.fail("task type parameters: %v", err)
		}
	}
	u.ScoreType = f.oneOf("score_type", "Score type", scoreTypes...)
	scoreText := f.str("score_type_params")
	if f.str("score_editor") != "" && !f.check("raw_score") {
		// The visual editor (the default in the page); the JSON field is
		// used when "raw_score" is ticked or by API-style posts.
		ed := editorFromForm(f, u.ID, codes)
		u.ScoreTypeParams = ed.params(f, u.ScoreType)
		return u, paramsText, prettyJSON(u.ScoreTypeParams), ed
	}
	if scoreText == "" {
		scoreText = "{}"
	}
	if !json.Valid([]byte(scoreText)) {
		f.fail("score type parameters are not valid JSON")
	} else {
		u.ScoreTypeParams = compactJSON(scoreText)
	}
	return u, paramsText, scoreText, nil
}

func compactJSON(s string) json.RawMessage {
	var buf bytes.Buffer
	if json.Compact(&buf, []byte(s)) != nil {
		return json.RawMessage(s)
	}
	return json.RawMessage(buf.Bytes())
}

func (s *Server) handleDatasetUpdate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	d, ok := s.loadDataset(w, r, rc)
	if !ok {
		return
	}
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
	u, paramsText, scoreText, editor := parseDataset(f, db.DatasetToUpdate(d), codes)
	// Score parameters must fit the current testcases (when there are any).
	if f.err == nil && len(tcs) > 0 {
		if _, err := scoring.New(u.ScoreType, u.ScoreTypeParams, codes, pub, 0); err != nil {
			f.fail("score type parameters: %v", err)
		}
	}
	if f.err != nil {
		p, err := s.datasetPage(r.Context(), d, u, paramsText, scoreText)
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		if editor != nil {
			p.Editor = editor
		}
		s.formError(w, r, rc, "dataset", s.datasetCrumbs(s.newPage(w, r, rc, p.Task.Name+" · "+d.Description, "tasks", p), p), f.err.Error())
		return
	}
	if _, err := s.q.UpdateDataset(r.Context(), u); err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Could not save: "+err.Error())
		return
	}
	rc.target("dataset", d.ID)
	s.datasetChanged(r.Context(), d.TaskID, d.ID)
	s.done(w, r, "/datasets/"+strconv.FormatInt(d.ID, 10),
		"Dataset saved. Existing submissions keep their results until you reevaluate them.")
}

func (s *Server) handleDatasetActivate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	d, ok := s.loadDataset(w, r, rc)
	if !ok {
		return
	}
	if err := dispatcher.ChangeLiveDataset(r.Context(), s.pool, s.queue, d.ID); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("dataset", d.ID)
	s.datasetChanged(r.Context(), d.TaskID, d.ID)
	s.done(w, r, "/datasets/"+strconv.FormatInt(d.ID, 10),
		"Dataset is now live: scores are recomputed and missing results are being judged.")
}

func (s *Server) handleDatasetDelete(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	d, ok := s.loadDataset(w, r, rc)
	if !ok {
		return
	}
	t, err := s.q.GetTask(r.Context(), d.TaskID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	if t.ActiveDatasetID != nil && *t.ActiveDatasetID == d.ID {
		s.errorPage(w, r, rc, http.StatusConflict, "The live dataset cannot be deleted; activate another one first.")
		return
	}
	if err := s.q.DeleteDataset(r.Context(), d.ID); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("dataset", d.ID)
	rc.note("description", d.Description)
	s.datasetChanged(r.Context(), d.TaskID, d.ID)
	s.done(w, r, "/tasks/"+strconv.FormatInt(t.ID, 10)+"#datasets", "Dataset deleted.")
}

// ---------------------------------------------------------------- managers

func (s *Server) handleManagerUpload(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	d, ok := s.loadDataset(w, r, rc)
	if !ok {
		return
	}
	if err := s.parseUpload(w, r); err != nil {
		s.errorPage(w, r, rc, http.StatusBadRequest, "Upload failed: "+err.Error())
		return
	}
	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Choose at least one file.")
		return
	}
	var names []string
	for _, fh := range files {
		name := path.Base(strings.ReplaceAll(fh.Filename, "\\", "/"))
		if !safeFileRe.MatchString(name) {
			s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Invalid file name: "+fh.Filename)
			return
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
		if _, err := s.q.UpsertManager(r.Context(), sqlc.UpsertManagerParams{DatasetID: d.ID, Filename: name, Digest: info.Digest}); err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		names = append(names, name)
	}
	rc.target("dataset", d.ID)
	s.datasetChanged(r.Context(), d.TaskID, d.ID)
	s.done(w, r, "/datasets/"+strconv.FormatInt(d.ID, 10)+"#managers", "Uploaded "+strings.Join(names, ", ")+".")
}

func (s *Server) handleManagerDownload(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	d, ok := s.loadDataset(w, r, rc)
	if !ok {
		return
	}
	ms, err := s.q.ListManagers(r.Context(), d.ID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	for _, m := range ms {
		if m.Filename == r.PathValue("file") {
			s.serveBlob(w, r, rc, m.Digest, "application/octet-stream", m.Filename, true)
			return
		}
	}
	s.notFound(w, r, rc)
}

func (s *Server) handleManagerDelete(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	d, ok := s.loadDataset(w, r, rc)
	if !ok {
		return
	}
	name := r.PathValue("file")
	if err := s.q.DeleteManager(r.Context(), sqlc.DeleteManagerParams{DatasetID: d.ID, Filename: name}); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("dataset", d.ID)
	s.datasetChanged(r.Context(), d.TaskID, d.ID)
	s.done(w, r, "/datasets/"+strconv.FormatInt(d.ID, 10)+"#managers", "Deleted "+name+".")
}

// ---------------------------------------------------------------- testcases

var codenameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

func (s *Server) handleTestcaseUpload(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	d, ok := s.loadDataset(w, r, rc)
	if !ok {
		return
	}
	if err := s.parseUpload(w, r); err != nil {
		s.errorPage(w, r, rc, http.StatusBadRequest, "Upload failed: "+err.Error())
		return
	}
	code := strings.TrimSpace(r.FormValue("codename"))
	if !codenameRe.MatchString(code) {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Invalid codename (letters, digits, '_', '.', '-').")
		return
	}
	in, _, _, err := s.storeUpload(r, "input")
	if err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Choose an input file.")
		return
	}
	out, _, _, err := s.storeUpload(r, "output")
	if err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Choose an output file.")
		return
	}
	tc, err := s.q.UpsertTestcase(r.Context(), sqlc.UpsertTestcaseParams{DatasetID: d.ID, Codename: code,
		Public: r.FormValue("public") != "", InputDigest: in, OutputDigest: out})
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("testcase", tc.ID)
	s.datasetChanged(r.Context(), d.TaskID, d.ID)
	s.done(w, r, "/datasets/"+strconv.FormatInt(d.ID, 10)+"#testcases", "Testcase "+code+" saved.")
}

// templateRe turns an archive name template ("input*.txt", "*.in") into a
// regexp capturing the codename.
func templateRe(tpl string) (*regexp.Regexp, error) {
	if strings.Count(tpl, "*") != 1 {
		return nil, fmt.Errorf("template %q must contain exactly one '*'", tpl)
	}
	parts := strings.SplitN(tpl, "*", 2)
	return regexp.Compile("^" + regexp.QuoteMeta(parts[0]) + "(.+)" + regexp.QuoteMeta(parts[1]) + "$")
}

// handleTestcaseArchive imports testcases from a zip archive, pairing
// inputs and outputs by the part of the name matched by '*'.
func (s *Server) handleTestcaseArchive(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	d, ok := s.loadDataset(w, r, rc)
	if !ok {
		return
	}
	if err := s.parseUpload(w, r); err != nil {
		s.errorPage(w, r, rc, http.StatusBadRequest, "Upload failed: "+err.Error())
		return
	}
	inRe, err := templateRe(strings.TrimSpace(r.FormValue("input_template")))
	if err == nil {
		var outRe *regexp.Regexp
		if outRe, err = templateRe(strings.TrimSpace(r.FormValue("output_template"))); err == nil {
			n, msg, ierr := s.importArchive(r, d, inRe, outRe)
			if ierr != nil {
				s.errorPage(w, r, rc, http.StatusUnprocessableEntity, ierr.Error())
				return
			}
			rc.target("dataset", d.ID)
			rc.note("imported", n)
			s.datasetChanged(r.Context(), d.TaskID, d.ID)
			s.done(w, r, "/datasets/"+strconv.FormatInt(d.ID, 10)+"#testcases", msg)
			return
		}
	}
	s.errorPage(w, r, rc, http.StatusUnprocessableEntity, err.Error())
}

func (s *Server) importArchive(r *http.Request, d sqlc.Dataset, inRe, outRe *regexp.Regexp) (int, string, error) {
	f, fh, err := r.FormFile("archive")
	if err != nil {
		return 0, "", fmt.Errorf("choose a zip archive")
	}
	defer f.Close()
	zr, err := zip.NewReader(f, fh.Size)
	if err != nil {
		return 0, "", fmt.Errorf("not a zip archive: %v", err)
	}
	// Bound the uncompressed size (zip bombs): 16x the upload limit.
	limit := int64(s.cfg.MaxUploadBytes)
	if limit <= 0 {
		limit = s.maxUpload
	}
	limit *= 16
	var total uint64
	inputs, outputs := map[string]*zip.File{}, map[string]*zip.File{}
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		total += zf.UncompressedSize64
		base := path.Base(zf.Name)
		if m := inRe.FindStringSubmatch(base); m != nil {
			inputs[m[1]] = zf
		} else if m := outRe.FindStringSubmatch(base); m != nil {
			outputs[m[1]] = zf
		}
	}
	if total > uint64(limit) {
		return 0, "", fmt.Errorf("the archive expands to more than %d bytes", limit)
	}
	var codes []string
	for code := range inputs {
		if outputs[code] != nil {
			if !codenameRe.MatchString(code) {
				return 0, "", fmt.Errorf("invalid codename %q", code)
			}
			codes = append(codes, code)
		}
	}
	sort.Strings(codes)
	if len(codes) == 0 {
		return 0, "", fmt.Errorf("no input/output pairs matched the templates")
	}
	store := func(zf *zip.File) (string, error) {
		rd, err := zf.Open()
		if err != nil {
			return "", err
		}
		defer rd.Close()
		info, err := s.blobs.Put(r.Context(), io.LimitReader(rd, int64(zf.UncompressedSize64)+1))
		if err != nil {
			return "", err
		}
		if uint64(info.Size) != zf.UncompressedSize64 {
			return "", fmt.Errorf("%s: size mismatch", zf.Name)
		}
		return info.Digest, nil
	}
	type pair struct{ code, in, out string }
	pairs := make([]pair, 0, len(codes))
	for _, code := range codes {
		in, err := store(inputs[code])
		if err != nil {
			return 0, "", err
		}
		out, err := store(outputs[code])
		if err != nil {
			return 0, "", err
		}
		pairs = append(pairs, pair{code, in, out})
	}
	overwrite := r.FormValue("overwrite") != ""
	public := r.FormValue("public") != ""
	existing, err := s.q.ListTestcases(r.Context(), d.ID)
	if err != nil {
		return 0, "", err
	}
	have := map[string]bool{}
	for _, tc := range existing {
		have[tc.Codename] = true
	}
	n, skipped := 0, 0
	err = db.InTx(r.Context(), s.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		for _, p := range pairs {
			if have[p.code] && !overwrite {
				skipped++
				continue
			}
			if _, err := q.UpsertTestcase(r.Context(), sqlc.UpsertTestcaseParams{DatasetID: d.ID, Codename: p.code,
				Public: public, InputDigest: p.in, OutputDigest: p.out}); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	if err != nil {
		return 0, "", err
	}
	msg := fmt.Sprintf("Imported %d testcases.", n)
	if skipped > 0 {
		msg += fmt.Sprintf(" %d existing testcases were kept (tick \"overwrite\" to replace them).", skipped)
	}
	return n, msg, nil
}

func (s *Server) loadTestcase(w http.ResponseWriter, r *http.Request, rc *reqCtx) (sqlc.Testcase, bool) {
	id, _ := pathID(r, "id")
	tc, err := s.q.GetTestcase(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r, rc)
		} else {
			s.internalError(w, r, rc, err)
		}
		return tc, false
	}
	return tc, true
}

func (s *Server) handleTestcasePublic(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	tc, ok := s.loadTestcase(w, r, rc)
	if !ok {
		return
	}
	public := r.FormValue("public") == "1"
	if err := s.q.SetTestcasePublic(r.Context(), sqlc.SetTestcasePublicParams{ID: tc.ID, Public: public}); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("testcase", tc.ID)
	if d, err := s.q.GetDataset(r.Context(), tc.DatasetID); err == nil {
		s.datasetChanged(r.Context(), d.TaskID, d.ID)
	}
	s.done(w, r, "/datasets/"+strconv.FormatInt(tc.DatasetID, 10)+"#testcases", "")
}

func (s *Server) handleTestcaseDelete(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	tc, ok := s.loadTestcase(w, r, rc)
	if !ok {
		return
	}
	if err := s.q.DeleteTestcase(r.Context(), tc.ID); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("testcase", tc.ID)
	rc.note("codename", tc.Codename)
	if d, err := s.q.GetDataset(r.Context(), tc.DatasetID); err == nil {
		s.datasetChanged(r.Context(), d.TaskID, d.ID)
	}
	s.done(w, r, "/datasets/"+strconv.FormatInt(tc.DatasetID, 10)+"#testcases", "Testcase "+tc.Codename+" deleted.")
}

func (s *Server) handleTestcaseDownload(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	tc, ok := s.loadTestcase(w, r, rc)
	if !ok {
		return
	}
	switch r.PathValue("which") {
	case "input":
		s.serveBlob(w, r, rc, tc.InputDigest, "text/plain; charset=utf-8", tc.Codename+".in", true)
	case "output":
		s.serveBlob(w, r, rc, tc.OutputDigest, "text/plain; charset=utf-8", tc.Codename+".out", true)
	default:
		s.notFound(w, r, rc)
	}
}
