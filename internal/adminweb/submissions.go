package adminweb

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/dispatcher"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/scoring"
)

const submissionsPerPage = 100

// submissionFilter is the query of the submissions page.
type submissionFilter struct {
	Task, Participation, Before int64
	User, Status, Language      string
	MinScore, MaxScore          string
}

func (f submissionFilter) query(before int64) string {
	v := url.Values{}
	if f.Task != 0 {
		v.Set("task", strconv.FormatInt(f.Task, 10))
	}
	if f.Participation != 0 {
		v.Set("participation", strconv.FormatInt(f.Participation, 10))
	}
	for k, x := range map[string]string{"user": f.User, "status": f.Status, "language": f.Language, "min": f.MinScore, "max": f.MaxScore} {
		if x != "" {
			v.Set(k, x)
		}
	}
	if before != 0 {
		v.Set("before", strconv.FormatInt(before, 10))
	}
	return v.Encode()
}

type submissionsPage struct {
	Contest   sqlc.Contest
	Tasks     []sqlc.Task
	Languages []*langs.Language
	F         submissionFilter
	Rows      []sqlc.AdminListSubmissionsRow
	Next      string
	Statuses  []string
}

func (s *Server) handleSubmissions(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	qv := r.URL.Query()
	f := submissionFilter{User: strings.TrimSpace(qv.Get("user")), Status: qv.Get("status"), Language: qv.Get("language"),
		MinScore: strings.TrimSpace(qv.Get("min")), MaxScore: strings.TrimSpace(qv.Get("max"))}
	f.Task, _ = strconv.ParseInt(qv.Get("task"), 10, 64)
	f.Participation, _ = strconv.ParseInt(qv.Get("participation"), 10, 64)
	f.Before, _ = strconv.ParseInt(qv.Get("before"), 10, 64)
	p := sqlc.AdminListSubmissionsParams{ContestID: c.ID, Lim: submissionsPerPage + 1}
	if f.Task != 0 {
		p.TaskID = &f.Task
	}
	if f.Participation != 0 {
		p.ParticipationID = &f.Participation
	}
	if f.User != "" {
		p.Username = &f.User
	}
	if f.Language != "" {
		p.Language = &f.Language
	}
	switch f.Status {
	case "pending", "compile_failed", "scored", "error":
		p.Status = &f.Status
	default:
		f.Status = ""
	}
	if x, err := strconv.ParseFloat(f.MinScore, 64); err == nil {
		p.MinScore = &x
	}
	if x, err := strconv.ParseFloat(f.MaxScore, 64); err == nil {
		p.MaxScore = &x
	}
	if f.Before != 0 {
		p.BeforeID = &f.Before
	}
	rows, err := s.q.AdminListSubmissions(r.Context(), p)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	d := &submissionsPage{Contest: c, Languages: s.langs.All(), F: f, Statuses: []string{"pending", "compile_failed", "scored", "error"}}
	if len(rows) > submissionsPerPage {
		rows = rows[:submissionsPerPage]
		d.Next = f.query(rows[len(rows)-1].ID)
	}
	d.Rows = rows
	if d.Tasks, err = s.q.ListTasksByContest(r.Context(), &c.ID); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	s.render(w, "submissions", http.StatusOK, s.newPage(w, r, rc, "Submissions", "contests", d).
		crumb("Contests", "/contests").crumb(c.Name, "/contests/"+strconv.FormatInt(c.ID, 10)))
}

// resultView is a submission result on one dataset.
type resultView struct {
	sqlc.AdminListSubmissionResultsRow
	Evaluations []sqlc.ListEvaluationsWithTestcaseRow
	Details     *scoring.Details
}

type submissionPage struct {
	S       sqlc.AdminGetSubmissionRow
	Files   []fileView
	Results []resultView
	Contest string
	Lang    string
}

type fileView struct {
	Name, Digest string
	Text         string
	Binary       bool
	Size         int
}

func (s *Server) loadSubmission(w http.ResponseWriter, r *http.Request, rc *reqCtx, id int64) (sqlc.AdminGetSubmissionRow, bool) {
	sub, err := s.q.AdminGetSubmission(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r, rc)
		} else {
			s.internalError(w, r, rc, err)
		}
		return sub, false
	}
	return sub, true
}

// files loads the source files of a submission (text up to 1 MiB each).
func (s *Server) files(r *http.Request, sub sqlc.AdminGetSubmissionRow) ([]fileView, error) {
	fs, err := s.q.ListSubmissionFiles(r.Context(), sub.ID)
	if err != nil {
		return nil, err
	}
	ext := ""
	if sub.Language != nil {
		if l, ok := s.langs.Get(*sub.Language); ok {
			ext = l.SourceExtension()
		}
	}
	out := make([]fileView, 0, len(fs))
	for _, f := range fs {
		fv := fileView{Name: strings.ReplaceAll(f.Filename, ".%l", ext), Digest: f.Digest}
		data, err := readBlobLimited(r.Context(), s, f.Digest, 1<<20)
		switch {
		case err != nil:
			fv.Binary = true
		case !utf8.Valid(data) || strings.ContainsRune(string(data), 0):
			fv.Binary, fv.Size = true, len(data)
		default:
			fv.Text, fv.Size = string(data), len(data)
		}
		out = append(out, fv)
	}
	return out, nil
}

func (s *Server) handleSubmission(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, _ := pathID(r, "id")
	sub, ok := s.loadSubmission(w, r, rc, id)
	if !ok {
		return
	}
	d := &submissionPage{S: sub}
	var err error
	if d.Files, err = s.files(r, sub); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	if sub.Language != nil {
		d.Lang = *sub.Language
		if l, ok := s.langs.Get(*sub.Language); ok {
			d.Lang = l.Name
		}
	}
	results, err := s.q.AdminListSubmissionResults(r.Context(), sub.ID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	for _, res := range results {
		rv := resultView{AdminListSubmissionResultsRow: res}
		if rv.Evaluations, err = s.q.ListEvaluationsWithTestcase(r.Context(), sqlc.ListEvaluationsWithTestcaseParams{
			SubmissionID: sub.ID, DatasetID: res.DatasetID}); err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		if len(res.ScoreDetails) > 0 {
			var det scoring.Details
			if json.Unmarshal(res.ScoreDetails, &det) == nil && det.Type != "" {
				rv.Details = &det
			}
		}
		d.Results = append(d.Results, rv)
	}
	c, err := s.q.GetContest(r.Context(), sub.ContestID)
	if err == nil {
		d.Contest = c.Name
	}
	s.render(w, "submission", http.StatusOK, s.newPage(w, r, rc, "Submission "+strconv.FormatInt(sub.ID, 10), "contests", d).
		crumb("Contests", "/contests").crumb(d.Contest, "/contests/"+strconv.FormatInt(sub.ContestID, 10)).
		crumb("Submissions", "/contests/"+strconv.FormatInt(sub.ContestID, 10)+"/submissions"))
}

func (s *Server) handleSubmissionFile(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, _ := pathID(r, "id")
	sub, ok := s.loadSubmission(w, r, rc, id)
	if !ok {
		return
	}
	files, err := s.files(r, sub)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	for _, f := range files {
		if f.Name == r.PathValue("name") {
			s.serveBlob(w, r, rc, f.Digest, "text/plain; charset=utf-8", strconv.FormatInt(sub.ID, 10)+"-"+f.Name, true)
			return
		}
	}
	s.notFound(w, r, rc)
}

type diffFile struct {
	Name  string
	Lines []diffLine
	Same  bool
	Note  string
}

type diffPage struct {
	A, B  sqlc.AdminGetSubmissionRow
	Files []diffFile
}

// handleSubmissionDiff compares two submissions file by file
// (?a=1&b=2, or ?id=1&id=2 from the list checkboxes).
func (s *Server) handleSubmissionDiff(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	q := r.URL.Query()
	ids := q["id"]
	if q.Get("a") != "" && q.Get("b") != "" {
		ids = []string{q.Get("a"), q.Get("b")}
	}
	if len(ids) != 2 {
		s.errorPage(w, r, rc, http.StatusBadRequest, "Choose exactly two submissions to compare.")
		return
	}
	var subs [2]sqlc.AdminGetSubmissionRow
	var files [2][]fileView
	for i, v := range ids {
		id, _ := strconv.ParseInt(v, 10, 64)
		sub, ok := s.loadSubmission(w, r, rc, id)
		if !ok {
			return
		}
		fs, err := s.files(r, sub)
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		subs[i], files[i] = sub, fs
	}
	// Older submission on the left.
	if subs[0].ID > subs[1].ID {
		subs[0], subs[1] = subs[1], subs[0]
		files[0], files[1] = files[1], files[0]
	}
	d := &diffPage{A: subs[0], B: subs[1]}
	byName := map[string]fileView{}
	for _, f := range files[1] {
		byName[f.Name] = f
	}
	// Files are matched by name; with a single file each (the usual case)
	// they are compared even when the extensions differ.
	single := len(files[0]) == 1 && len(files[1]) == 1
	used := map[string]bool{}
	for _, fa := range files[0] {
		fb, ok := byName[fa.Name]
		if !ok && single {
			fb, ok = files[1][0], true
		}
		df := diffFile{Name: fa.Name}
		if ok {
			used[fb.Name] = true
			if fb.Name != fa.Name {
				df.Name = fa.Name + " → " + fb.Name
			}
		}
		switch {
		case fa.Binary || fb.Binary:
			df.Note = "binary file"
		case !ok:
			df.Lines = lineDiff(splitLines(fa.Text), nil, 2000)
		default:
			df.Same = fa.Text == fb.Text
			df.Lines = lineDiff(splitLines(fa.Text), splitLines(fb.Text), 2000)
		}
		d.Files = append(d.Files, df)
	}
	for _, fb := range files[1] {
		if !used[fb.Name] {
			d.Files = append(d.Files, diffFile{Name: fb.Name, Lines: lineDiff(nil, splitLines(fb.Text), 2000)})
		}
	}
	s.render(w, "diff", http.StatusOK, s.newPage(w, r, rc, "Diff", "contests", d))
}

// handleReevaluate invalidates results. The scope comes from hidden form
// fields; back is where to return.
func (s *Server) handleReevaluate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	level, err := dispatcher.ParseLevel(r.FormValue("level"))
	if err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, err.Error())
		return
	}
	var sc dispatcher.Scope
	for name, dst := range map[string]*int64{"contest_id": &sc.ContestID, "task_id": &sc.TaskID, "dataset_id": &sc.DatasetID,
		"participation_id": &sc.ParticipationID, "user_id": &sc.UserID, "submission_id": &sc.SubmissionID} {
		if v := r.FormValue(name); v != "" {
			*dst, _ = strconv.ParseInt(v, 10, 64)
			rc.note(name, *dst)
		}
	}
	n, err := dispatcher.Invalidate(r.Context(), s.pool, s.queue, sc, level)
	if err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Reevaluation failed: "+err.Error())
		return
	}
	rc.note("results", n)
	back := safeNext(r.FormValue("back"))
	s.done(w, r, back, strconv.Itoa(n)+" results scheduled for "+strings.ToLower(levelName(level))+".")
}

func levelName(l dispatcher.Level) string {
	switch l {
	case dispatcher.Recompile:
		return "Recompilation"
	case dispatcher.Reevaluate:
		return "Reevaluation"
	}
	return "Rescoring"
}
