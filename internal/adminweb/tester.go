package adminweb

import (
	"archive/zip"
	"context"
	"io"
	"net/http"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/D4ND3R/Contest-Management-System/internal/tasktypes"
	"github.com/jackc/pgx/v5"
)

// The task tester judges a reference solution on every dataset of a task
// without a participation: the run never appears to contestants, in
// rankings, statistics or exports (SPEC_AUDIT §2).

// testerForm describes what a tester run needs.
type testerForm struct {
	Formats       []string
	NeedsLanguage bool
	OutputOnly    bool
	Languages     []*langs.Language
	Runs          []testerRun
}

type testerRun struct {
	ID       int64
	Time     time.Time
	Language string
	Admin    string
	Results  []testerResult
}

type testerResult struct {
	Dataset string
	Status  string // an i18n key; "evaluating" uses Done/Total
	Class   string
	Done    int32
	Total   int32
	Score   *float64
}

// submissionFormats returns the files a submission to the task consists
// of: the task's submission format, or one output per testcase of the live
// dataset for output-only tasks without an explicit format.
func (s *Server) submissionFormats(ctx context.Context, t sqlc.Task) ([]string, bool, error) {
	formats := append([]string(nil), t.SubmissionFormat...)
	outputOnly := false
	if t.ActiveDatasetID != nil {
		ds, err := s.q.GetDataset(ctx, *t.ActiveDatasetID)
		if err != nil {
			return nil, false, err
		}
		outputOnly = ds.TaskType == "OutputOnly"
		if outputOnly && len(formats) == 0 {
			pattern, _, _ := tasktypes.OutputOnlyConfig(ds.TaskTypeParams)
			tcs, err := s.q.ListTestcases(ctx, ds.ID)
			if err != nil {
				return nil, false, err
			}
			for _, tc := range tcs {
				formats = append(formats, tasktypes.OutputFileName(pattern, tc.Codename))
			}
			sort.Strings(formats)
		}
	}
	return formats, outputOnly, nil
}

func (s *Server) testerForm(ctx context.Context, t sqlc.Task) (*testerForm, error) {
	formats, outputOnly, err := s.submissionFormats(ctx, t)
	if err != nil {
		return nil, err
	}
	f := &testerForm{Formats: formats, OutputOnly: outputOnly, Languages: taskLanguages(s.langs, t)}
	for _, x := range formats {
		f.NeedsLanguage = f.NeedsLanguage || strings.HasSuffix(x, ".%l")
	}
	rows, err := s.q.AdminListTesterRuns(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if len(f.Runs) == 0 || f.Runs[len(f.Runs)-1].ID != row.ID {
			f.Runs = append(f.Runs, testerRun{ID: row.ID, Time: row.SubmittedAt, Language: derefStr(row.Language), Admin: row.AdminUsername})
		}
		if row.DatasetID == nil {
			continue
		}
		res := testerResult{Dataset: derefStr(row.DatasetDescription), Score: row.Score}
		switch {
		case row.SystemError != nil:
			res.Status, res.Class = "system error", "bad"
		case row.CompilationOutcome == nil:
			res.Status = "compiling"
		case *row.CompilationOutcome == "fail":
			res.Status, res.Class = "compilation failed", "bad"
		case row.ScoredAt == nil:
			res.Status, res.Done, res.Total = "evaluating", deref32(row.TestcasesDone), deref32(row.TestcasesTotal)
		default:
			res.Status, res.Class = "scored", "ok"
		}
		run := &f.Runs[len(f.Runs)-1]
		run.Results = append(run.Results, res)
	}
	return f, nil
}

func deref32(v *int32) int32 {
	if v == nil {
		return 0
	}
	return *v
}

// handleTesterSubmit stores a tester run and asks the dispatcher to judge
// it on every dataset.
func (s *Server) handleTesterSubmit(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, ok := s.loadTask(w, r, rc)
	if !ok {
		return
	}
	if err := s.parseUpload(w, r); err != nil {
		s.errorPage(w, r, rc, http.StatusBadRequest, "Upload failed: "+err.Error())
		return
	}
	formats, outputOnly, err := s.submissionFormats(r.Context(), t)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	var lang *langs.Language
	for _, f := range formats {
		if strings.HasSuffix(f, ".%l") {
			l, ok := s.langs.Get(r.FormValue("language"))
			if !ok {
				s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Choose a language.")
				return
			}
			lang = l
			break
		}
	}
	const perFile = 64 << 20
	var params []sqlc.CreateSubmissionFilesParams
	have := map[string]bool{}
	store := func(name string, rd io.Reader) error {
		info, err := s.blobs.Put(r.Context(), io.LimitReader(rd, perFile))
		if err != nil {
			return err
		}
		params = append(params, sqlc.CreateSubmissionFilesParams{Filename: name, Digest: info.Digest})
		have[name] = true
		return nil
	}
	for _, format := range formats {
		f, _, err := r.FormFile(format)
		if err != nil {
			continue
		}
		err = store(format, f)
		f.Close()
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
	}
	if outputOnly {
		if f, hdr, err := r.FormFile("zip"); err == nil {
			defer f.Close()
			zr, err := zip.NewReader(f, hdr.Size)
			if err != nil {
				s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "The archive is not a valid zip file.")
				return
			}
			allowed := map[string]bool{}
			for _, x := range formats {
				allowed[x] = true
			}
			for _, zf := range zr.File {
				name := path.Base(zf.Name)
				if zf.FileInfo().IsDir() || !allowed[name] || have[name] {
					continue
				}
				rd, err := zf.Open()
				if err != nil {
					s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "The archive is not a valid zip file.")
					return
				}
				err = store(name, rd)
				rd.Close()
				if err != nil {
					s.internalError(w, r, rc, err)
					return
				}
			}
		}
	}
	if len(params) == 0 || (!outputOnly && len(params) != len(formats)) {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Attach every file of the submission.")
		return
	}
	var langID *string
	if lang != nil {
		langID = &lang.ID
	}
	var id int64
	err = db.InTx(r.Context(), s.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		sub, err := q.CreateTesterSubmission(r.Context(), sqlc.CreateTesterSubmissionParams{TaskID: t.ID, Language: langID, AdminID: rc.admin.ID})
		if err != nil {
			return err
		}
		id = sub.ID
		for i := range params {
			params[i].SubmissionID = id
		}
		_, err = q.CreateSubmissionFiles(r.Context(), params)
		return err
	})
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	if err := s.queue.Notify(r.Context(), queue.Event{Kind: queue.EventSubmission, SubmissionID: id}); err != nil {
		s.log.Warn("notify dispatcher", "error", err)
	}
	rc.target("submission", id)
	rc.note("task", t.Name)
	s.done(w, r, "/submissions/"+strconv.FormatInt(id, 10), "Test run submitted: it is judged on every dataset and never counts as a submission.")
}

// taskLanguages returns the languages a task accepts: its own list when it
// has one, otherwise every configured language.
func taskLanguages(reg *langs.Registry, t sqlc.Task) []*langs.Language {
	if len(t.Languages) == 0 {
		return reg.All()
	}
	var out []*langs.Language
	for _, l := range reg.All() {
		if slices.Contains(t.Languages, l.ID) {
			out = append(out, l)
		}
	}
	return out
}
