package adminweb

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/problempkg"
	"github.com/D4ND3R/Contest-Management-System/internal/scoring"
)

// packagePage is the problem package import page: the upload form, then
// the preview of what was detected and every problem found (K19).
type packagePage struct {
	Package  *problempkg.Package
	Digest   string
	FileName string
	// Where the package goes: a new task (optionally in a contest) or a new
	// dataset of an existing task.
	Mode      string
	ContestID int64
	TaskID    int64
	Run       bool // run solutions/ after importing (K20)
	Contests  []sqlc.Contest
	Tasks     []sqlc.Task
	// Conflict is the existing task with the package's name (new task mode).
	Conflict  *sqlc.Task
	Target    *sqlc.Task
	CanImport bool
	Public    int
	Verdicts  map[string]string
}

func (s *Server) packagePage(r *http.Request) (*packagePage, error) {
	d := &packagePage{Mode: "task", Run: true, Verdicts: problempkg.Verdicts}
	var err error
	if d.Contests, err = s.q.ListContests(r.Context()); err != nil {
		return nil, err
	}
	if d.Tasks, err = s.q.ListTasks(r.Context()); err != nil {
		return nil, err
	}
	return d, nil
}

// pkgOptions are the problem package options for the configured languages.
func (s *Server) pkgOptions() problempkg.Options {
	o := problempkg.OptionsFor(s.langs)
	o.MaxBytes = 16 * s.uploadLimit()
	return o
}

func (s *Server) uploadLimit() int64 {
	if limit := int64(s.cfg.MaxUploadBytes); limit > 0 {
		return limit
	}
	return s.maxUpload
}

func (s *Server) handlePackageForm(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	d, err := s.packagePage(r)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	if id, err := strconv.ParseInt(r.URL.Query().Get("task"), 10, 64); err == nil {
		d.Mode, d.TaskID = "dataset", id
	}
	s.render(w, "task_import", http.StatusOK, s.newPage(w, r, rc, "Import a problem package", "tasks", d).crumb("Tasks", "/tasks"))
}

// openZipBlob opens a stored zip; cleanup releases it.
func (s *Server) openZipBlob(ctx context.Context, digest string) (*zip.Reader, func(), error) {
	rd, err := s.blobs.Open(ctx, digest)
	if err != nil {
		return nil, nil, err
	}
	f, ok := rd.(*os.File)
	if !ok {
		// Not a local file (S3): spool it to a temporary file.
		tmp, err := os.CreateTemp("", "cms-package-*.zip")
		if err != nil {
			rd.Close()
			return nil, nil, err
		}
		os.Remove(tmp.Name())
		_, err = io.Copy(tmp, rd)
		rd.Close()
		if err != nil {
			tmp.Close()
			return nil, nil, err
		}
		f = tmp
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	zr, err := zip.NewReader(f, st.Size())
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return zr, func() { f.Close() }, nil
}

// handlePackageImport previews an uploaded package (step "preview", nothing
// is written) or imports a previewed one (step "confirm").
func (s *Server) handlePackageImport(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	if err := s.parseAnyForm(w, r); err != nil {
		s.errorPage(w, r, rc, http.StatusBadRequest, "Upload failed: "+err.Error())
		return
	}
	d, err := s.packagePage(r)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	f := newForm(r)
	d.Mode = f.str("mode")
	if d.Mode != "dataset" {
		d.Mode = "task"
	}
	d.ContestID, _ = strconv.ParseInt(f.str("contest_id"), 10, 64)
	d.TaskID, _ = strconv.ParseInt(f.str("task_id"), 10, 64)
	d.Run = f.check("run_solutions")
	confirm := f.str("step") == "confirm"
	tr := adminTr(r)
	render := func(status int) {
		rc.audit.skip = true
		s.render(w, "task_import", status, s.newPage(w, r, rc, "Import a problem package", "tasks", d).crumb("Tasks", "/tasks"))
	}
	if confirm {
		d.Digest, d.FileName = f.str("digest"), f.str("file_name")
		if !blob.ValidDigest(d.Digest) {
			s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "The previewed file is gone; upload it again.")
			return
		}
	} else {
		if d.Digest, d.FileName, _, err = s.storeUpload(r, "package"); err != nil {
			s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Choose a file: "+err.Error())
			return
		}
	}
	zr, cleanup, err := s.openZipBlob(r.Context(), d.Digest)
	switch {
	case errors.Is(err, blob.ErrNotFound):
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "The previewed file is gone; upload it again.")
		return
	case err != nil:
		d.Package = &problempkg.Package{Errors: []problempkg.Issue{{Path: d.FileName, Message: tr("not a zip archive: %v", err)}}}
		render(http.StatusUnprocessableEntity)
		return
	}
	defer cleanup()
	p := problempkg.Read(zr, s.pkgOptions())
	d.Package = p
	for _, t := range p.Tests {
		if t.Public {
			d.Public++
		}
	}
	if p.Config != nil {
		if d.Mode == "task" {
			if t, err := s.q.GetTaskByName(r.Context(), p.Config.Name); err == nil {
				d.Conflict = &t
			}
		} else if t, err := s.q.GetTask(r.Context(), d.TaskID); err == nil {
			d.Target = &t
		}
	}
	d.CanImport = p.OK() && ((d.Mode == "task" && d.Conflict == nil) || (d.Mode == "dataset" && d.Target != nil))
	if !confirm || !d.CanImport {
		status := http.StatusOK
		if confirm {
			status = http.StatusUnprocessableEntity
		}
		render(status)
		return
	}
	opts := problempkg.ImportOptions{TaskID: d.TaskID}
	if d.Mode == "task" {
		opts.TaskID = 0
		if d.ContestID != 0 {
			opts.ContestID = &d.ContestID
		}
	}
	res, err := problempkg.Import(r.Context(), s.pool, s.blobs, p, opts)
	if errors.Is(err, problempkg.ErrNameTaken) {
		t, _ := s.q.GetTaskByName(r.Context(), p.Config.Name)
		d.Conflict, d.CanImport = &t, false
		render(http.StatusUnprocessableEntity)
		return
	}
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("task", res.TaskID)
	rc.note("dataset", res.DatasetID)
	rc.note("package", d.Digest)
	rc.note("tests", len(p.Tests))
	if opts.ContestID != nil {
		s.contestChanged(r.Context(), *opts.ContestID, 0)
	}
	s.datasetChanged(r.Context(), res.TaskID, res.DatasetID)
	to := "/tasks/" + strconv.FormatInt(res.TaskID, 10)
	msg := tr("Package imported: %d testcases into the dataset %q.", len(p.Tests), res.Dataset)
	if d.Run && len(p.Solutions) > 0 {
		ran, skipped, err := s.runPackageSolutions(r.Context(), rc.admin.ID, res.TaskID, p)
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		rc.note("solutions", ran)
		to += "/validation"
		msg += " " + tr("%d reference solutions are being judged.", ran)
		if len(skipped) > 0 {
			msg += " " + tr("Not run (they do not fit the submission format): %s.", strings.Join(skipped, "; "))
		}
	}
	s.done(w, r, to, "%s", msg)
}

// runPackageSolutions submits the package's solutions to the task tester;
// solutions that do not fit the task's submission format are skipped.
func (s *Server) runPackageSolutions(ctx context.Context, adminID, taskID int64, p *problempkg.Package) (int, []string, error) {
	t, err := s.q.GetTask(ctx, taskID)
	if err != nil {
		return 0, nil, err
	}
	formats, outputOnly, err := s.submissionFormats(ctx, t)
	if err != nil {
		return 0, nil, err
	}
	allowed := map[string]bool{}
	for _, f := range formats {
		allowed[f] = true
	}
	ran := 0
	var skipped []string
	for _, sol := range p.Solutions {
		var files []sqlc.CreateSubmissionFilesParams
		put := func(name string, rd io.Reader) error {
			info, err := s.blobs.Put(ctx, io.LimitReader(rd, 64<<20))
			if err == nil {
				files = append(files, sqlc.CreateSubmissionFilesParams{Filename: name, Digest: info.Digest})
			}
			return err
		}
		var lang *string
		switch {
		case outputOnly:
			for _, f := range sol.Files {
				if err := s.addOutputs(f, allowed, put); err != nil {
					return ran, skipped, err
				}
			}
		case len(formats) == 1 && strings.HasSuffix(formats[0], ".%l") && sol.Language != "":
			rd, err := sol.Files[0].Open()
			if err != nil {
				return ran, skipped, err
			}
			err = put(formats[0], rd)
			rd.Close()
			if err != nil {
				return ran, skipped, err
			}
			l := sol.Language
			lang = &l
		}
		if len(files) == 0 {
			skipped = append(skipped, sol.Name)
			continue
		}
		if _, err := s.createTesterRun(ctx, taskID, adminID, lang, files, "solutions/"+sol.Name); err != nil {
			return ran, skipped, err
		}
		ran++
	}
	return ran, skipped, nil
}

// addOutputs adds an output-only solution file, expanding a zip.
func (s *Server) addOutputs(f problempkg.File, allowed map[string]bool, put func(string, io.Reader) error) error {
	rd, err := f.Open()
	if err != nil {
		return err
	}
	defer rd.Close()
	if !strings.EqualFold(path.Ext(f.Name), ".zip") {
		if allowed[f.Name] {
			return put(f.Name, rd)
		}
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(rd, 256<<20))
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil
	}
	for _, zf := range zr.File {
		if name := path.Base(zf.Name); allowed[name] && !zf.FileInfo().IsDir() {
			zrd, err := zf.Open()
			if err != nil {
				return err
			}
			err = put(name, zrd)
			zrd.Close()
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// handlePackageExport downloads a task (the live dataset, or ?dataset=) as
// a problem package (K21).
func (s *Server) handlePackageExport(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, ok := s.loadTask(w, r, rc)
	if !ok {
		return
	}
	dsID, _ := strconv.ParseInt(r.URL.Query().Get("dataset"), 10, 64)
	if dsID == 0 && t.ActiveDatasetID != nil {
		dsID = *t.ActiveDatasetID
	}
	d, err := s.q.GetDataset(r.Context(), dsID)
	if err != nil || d.TaskID != t.ID {
		s.notFound(w, r, rc)
		return
	}
	// Refuse what cannot be exported before streaming anything.
	if _, err := problempkg.ConfigFromCMS(t, d, nil, nil); err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Cannot export: "+err.Error())
		return
	}
	name := t.Name
	if t.ActiveDatasetID == nil || *t.ActiveDatasetID != d.ID {
		name += "-" + strconv.FormatInt(d.ID, 10)
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, name))
	if err := problempkg.Export(r.Context(), s.q, s.blobs, t.ID, d.ID, w); err != nil {
		s.log.Error("package export", "task", t.ID, "dataset", d.ID, "error", err)
	}
}

// validationPage is the report of the package solutions of a task (K20).
type validationPage struct {
	Task     sqlc.Task
	Datasets []validationDataset
	Rows     []validationRow
	Pass     int
	Fail     int
	Pending  int
	Errors   []string // distinct system errors (e.g. a checker that does not compile)
}

type validationDataset struct {
	ID          int64
	Description string
	Live        bool
	MaxScore    float64
}

type validationRow struct {
	SubmissionID int64
	Name         string
	Expected     string
	Language     string
	Cells        []validationCell
	Status       string // worst over the datasets
}

type validationCell struct {
	Status, Got string
}

func (s *Server) validationPage(ctx context.Context, t sqlc.Task) (*validationPage, error) {
	v := &validationPage{Task: t}
	dss, err := s.q.ListDatasetsByTask(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(dss))
	for i, d := range dss {
		ids[i] = d.ID
	}
	tcs, err := s.q.ListTestcasesByDatasets(ctx, ids)
	if err != nil {
		return nil, err
	}
	byDS := map[int64][]sqlc.Testcase{}
	for _, tc := range tcs {
		byDS[tc.DatasetID] = append(byDS[tc.DatasetID], tc)
	}
	col := map[int64]int{}
	for i, d := range dss {
		vd := validationDataset{ID: d.ID, Description: d.Description, Live: t.ActiveDatasetID != nil && *t.ActiveDatasetID == d.ID}
		codes, pub := make([]string, len(byDS[d.ID])), make([]bool, len(byDS[d.ID]))
		for k, tc := range byDS[d.ID] {
			codes[k], pub[k] = tc.Codename, tc.Public
		}
		if st, err := scoring.New(d.ScoreType, d.ScoreTypeParams, codes, pub, int(t.ScorePrecision)); err == nil {
			vd.MaxScore = st.MaxScore()
		}
		col[d.ID] = i
		v.Datasets = append(v.Datasets, vd)
	}
	runs, err := s.q.AdminPackageSolutionRuns(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	seenErr := map[string]bool{}
	rank := map[string]int{"pass": 0, "pending": 1, "fail": 2}
	for _, run := range runs {
		if len(v.Rows) == 0 || v.Rows[len(v.Rows)-1].SubmissionID != run.ID {
			name := strings.TrimPrefix(run.Comment, "solutions/")
			exp, _, _ := strings.Cut(name, "_")
			v.Rows = append(v.Rows, validationRow{SubmissionID: run.ID, Name: name, Expected: strings.ToLower(exp),
				Language: derefStr(run.Language), Cells: make([]validationCell, len(dss)), Status: "pass"})
		}
		row := &v.Rows[len(v.Rows)-1]
		rr := problempkg.RunResult{Judged: run.Judged, Scored: run.ScoredAt != nil, MaxScore: v.Datasets[col[run.DatasetID]].MaxScore,
			TLE: run.AnyTle, MLE: run.AnyMle, RE: run.AnyRe, WA: run.AnyWa}
		if run.CompilationOutcome != nil {
			ok := *run.CompilationOutcome == "ok"
			rr.Compiled = &ok
		}
		if run.Score != nil {
			rr.Score = *run.Score
		}
		if run.SystemError != nil {
			rr.SystemError = *run.SystemError
			if !seenErr[rr.SystemError] {
				seenErr[rr.SystemError] = true
				v.Errors = append(v.Errors, rr.SystemError)
			}
		}
		st, got := problempkg.Check(row.Expected, rr)
		row.Cells[col[run.DatasetID]] = validationCell{Status: st, Got: got}
		if rank[st] > rank[row.Status] {
			row.Status = st
		}
	}
	for _, row := range v.Rows {
		switch row.Status {
		case "pass":
			v.Pass++
		case "fail":
			v.Fail++
		default:
			v.Pending++
		}
	}
	sort.Strings(v.Errors)
	return v, nil
}

func (s *Server) handleValidation(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, ok := s.loadTask(w, r, rc)
	if !ok {
		return
	}
	v, err := s.validationPage(r.Context(), t)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	p := s.newPage(w, r, rc, t.Name, "tasks", v).crumb("Tasks", "/tasks").crumb(t.Name, "/tasks/"+strconv.FormatInt(t.ID, 10))
	if r.URL.Query().Get("fragment") != "" {
		s.renderPartial(w, "validation-report", wrap{P: p, V: v})
		return
	}
	s.render(w, "task_validation", http.StatusOK, p)
}

// handleValidationRerun judges the newest version of every package
// solution again (e.g. after changing the dataset).
func (s *Server) handleValidationRerun(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, ok := s.loadTask(w, r, rc)
	if !ok {
		return
	}
	sols, err := s.q.ListPackageSolutionFiles(r.Context(), t.ID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	runs, err := s.q.AdminPackageSolutionRuns(r.Context(), t.ID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	lang := map[int64]*string{}
	for _, run := range runs {
		lang[run.ID] = run.Language
	}
	n := 0
	for i := 0; i < len(sols); {
		j := i
		var files []sqlc.CreateSubmissionFilesParams
		for ; j < len(sols) && sols[j].SubmissionID == sols[i].SubmissionID; j++ {
			files = append(files, sqlc.CreateSubmissionFilesParams{Filename: sols[j].Filename, Digest: sols[j].Digest})
		}
		if _, err := s.createTesterRun(r.Context(), t.ID, rc.admin.ID, lang[sols[i].SubmissionID], files, sols[i].Comment); err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		n++
		i = j
	}
	rc.target("task", t.ID)
	rc.note("solutions", n)
	s.done(w, r, "/tasks/"+strconv.FormatInt(t.ID, 10)+"/validation", "%d reference solutions are being judged.", n)
}
