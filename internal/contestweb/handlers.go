package contestweb

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/contest"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/events"
	"github.com/D4ND3R/Contest-Management-System/internal/i18n"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/D4ND3R/Contest-Management-System/internal/suspicious"
	"github.com/D4ND3R/Contest-Management-System/internal/tasktypes"
	"github.com/D4ND3R/Contest-Management-System/internal/webkit"
	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------- overview

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	if !rc.status.CanStart {
		webkit.Redirect(w, r, "/"+rc.contest.Name+"/")
		return
	}
	now := rc.now
	if _, err := s.q.StartParticipation(r.Context(), sqlc.StartParticipationParams{ID: rc.part.ID, StartingTime: &now}); err != nil {
		s.fail(w, err)
		return
	}
	s.cache.invalidateParticipation(rc.part.ID)
	_ = events.Publish(r.Context(), s.rdb, s.ns, events.Event{Type: events.TypeContest, ParticipationID: rc.part.ID})
	webkit.Redirect(w, r, "/"+rc.contest.Name+"/")
}

// visibleTask returns the task if the contestant may see it now.
func (s *Server) visibleTask(w http.ResponseWriter, r *http.Request, rc *reqCtx) *taskView {
	t := rc.contest.TaskByName[r.PathValue("task")]
	st := rc.status.Phase
	if t == nil || ((st == contest.NotStarted || st == contest.WaitingStart) && !rc.part.Unrestricted) {
		s.errorPage(w, r, rc.contest, http.StatusNotFound, "Not found", "This task does not exist or is not available yet.")
		return nil
	}
	return t
}

// ---------------------------------------------------------------- task

// langChoice is a language of the submission form; Exts are its source
// extensions (the page checks a chosen file against them before sending).
type langChoice struct {
	ID, Name string
	Exts     string
}

type taskData struct {
	Task         *taskView
	Tokens       *tokenView
	TestsEnabled bool
	Tests        []testView
	TestLimits   [][2]string
	Subs         []subView
	CanSubmit    bool
	CannotSubmit string
	Languages    []langChoice
	LastLanguage string
	// MaxFileBytes is the size limit of each file (checked in the page too).
	MaxFileBytes int64
	Limits       [][2]string
	// Statement is the statement shown on the page.
	Statement *statementPage
	// Tab is the part of the problem page shown: "statement" or
	// "submissions".
	Tab string
	// ReadyAt is when the next submission is allowed (Unix milliseconds;
	// 0: now): the minimum interval between submissions.
	ReadyAt int64
	// Submitted is the submission just made (the page without JavaScript
	// comes back here after sending).
	Submitted int64
}

// Why submissions are refused (untranslated).
const (
	msgClosed     = "Submissions are closed."
	msgPaused     = "Submissions are paused by the organizers."
	msgTaskClosed = "Submissions to this task are closed."
)

// submitBlocked says why submissions and user tests to t are refused now,
// "" when they are accepted: the contest window, the organizers' pause or
// the task closed (emergency controls, SPEC_IOI §9.3).
func submitBlocked(rc *reqCtx, t *taskView) string {
	switch {
	case !rc.status.CanSubmit:
		return msgClosed
	case rc.contest.SubmissionsPaused:
		return msgPaused
	case t != nil && t.SubmissionsClosed:
		return msgTaskClosed
	}
	return ""
}

func (s *Server) taskData(r *http.Request, rc *reqCtx, p *page, t *taskView) (*taskData, error) {
	why := submitBlocked(rc, t)
	d := &taskData{Task: t, CanSubmit: why == ""}
	if !d.CanSubmit {
		d.CannotSubmit = p.T(why)
		if why == msgPaused && rc.contest.PauseMessage != "" {
			d.CannotSubmit += " " + rc.contest.PauseMessage
		}
	}
	for _, l := range t.Languages {
		d.Languages = append(d.Languages, langChoice{ID: l.ID, Name: l.Name, Exts: strings.Join(l.SourceExtensions, " ")})
	}
	d.MaxFileBytes = s.fileLimit(rc, t)
	subs, err := s.listSubs(r, rc, t)
	if err != nil {
		return nil, err
	}
	for _, sv := range subs {
		if sv.Language != "" {
			d.LastLanguage = sv.langID
			break
		}
	}
	d.Subs = subs
	d.Limits = s.limitsText(p, rc, t)
	if d.CanSubmit {
		if d.ReadyAt, err = s.readyAt(r, rc, t); err != nil {
			return nil, err
		}
	}
	if d.Tokens, err = s.tokenView(r, rc, t); err != nil {
		return nil, err
	}
	if d.TestsEnabled = testsEnabled(rc, t) && d.CanSubmit; testsEnabled(rc, t) {
		if d.Tests, err = s.listTests(r, rc, p, t); err != nil {
			return nil, err
		}
		if m := rc.contest.MaxUserTestNumber; m != nil {
			d.TestLimits = append(d.TestLimits, [2]string{p.T("Tests (contest)"), p.T("at most %d", *m)})
		}
		if m := t.MaxUserTestNumber; m != nil {
			d.TestLimits = append(d.TestLimits, [2]string{p.T("Tests (task)"), p.T("at most %d", *m)})
		}
		for _, m := range []*int64{rc.contest.MinUserTestIntervalS, t.MinUserTestIntervalS} {
			if m != nil && *m > 0 {
				d.TestLimits = append(d.TestLimits, [2]string{p.T("Minimum interval between tests"), p.Dur(*m)})
			}
		}
	}
	return d, nil
}

func (s *Server) limitsText(p *page, rc *reqCtx, t *taskView) [][2]string {
	var out [][2]string
	if m := rc.contest.MaxSubmissionNumber; m != nil {
		out = append(out, [2]string{p.T("Submissions (contest)"), p.T("at most %d", *m)})
	}
	if m := t.MaxSubmissionNumber; m != nil {
		out = append(out, [2]string{p.T("Submissions (task)"), p.T("at most %d", *m)})
	}
	if m := rc.contest.MinSubmissionIntervalS; m != nil && *m > 0 {
		out = append(out, [2]string{p.T("Minimum interval"), p.Dur(*m)})
	}
	if m := t.MinSubmissionIntervalS; m != nil && *m > 0 {
		out = append(out, [2]string{p.T("Minimum interval (task)"), p.Dur(*m)})
	}
	if m := rc.contest.MaxSubmissionBytes; m != nil && (t.SourceLimit <= 0 || *m < t.SourceLimit) {
		out = append(out, [2]string{p.T("Maximum file size"), p.Bytes(*m)})
	} else if t.SourceLimit > 0 {
		out = append(out, [2]string{p.T("Maximum file size"), p.Bytes(t.SourceLimit)})
	}
	return out
}

// readyAt is when the contestant may submit to t again (Unix ms, 0: now),
// after the minimum intervals of the contest and of the task.
func (s *Server) readyAt(r *http.Request, rc *reqCtx, t *taskView) (int64, error) {
	cmin, tmin := rc.contest.MinSubmissionIntervalS, t.MinSubmissionIntervalS
	if !rc.status.Official || rc.part.Unrestricted || (cmin == nil || *cmin <= 0) && (tmin == nil || *tmin <= 0) {
		return 0, nil
	}
	st, err := s.q.SubmissionStats(r.Context(), sqlc.SubmissionStatsParams{ParticipationIds: rc.group, TaskID: t.ID})
	if err != nil {
		return 0, err
	}
	var at time.Time
	if cmin != nil && *cmin > 0 && st.ContestCount > 0 {
		at = st.ContestLast.Add(time.Duration(*cmin) * time.Second)
	}
	if tmin != nil && *tmin > 0 && st.TaskCount > 0 {
		if a := st.TaskLast.Add(time.Duration(*tmin) * time.Second); a.After(at) {
			at = a
		}
	}
	if !at.After(rc.now) {
		return 0, nil
	}
	return at.UnixMilli(), nil
}

// handleTask shows a problem: its statement (the first tab).
func (s *Server) handleTask(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.taskPage(w, r, rc, "statement")
}

// handleSubmissionList shows the Submissions tab of a problem, or only the
// list (live refresh: an htmx request or ?fragment=1).
func (s *Server) handleSubmissionList(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	if webkit.IsHTMX(r) || r.URL.Query().Get("fragment") == "1" {
		t := s.visibleTask(w, r, rc)
		if t == nil {
			return
		}
		p := s.newPage(rc, t.Title, t.Name)
		d, err := s.taskData(r, rc, p, t)
		if err != nil {
			s.fail(w, err)
			return
		}
		p.Data = d
		s.renderPartial(w, "submissions", p)
		return
	}
	s.taskPage(w, r, rc, "submissions")
}

func (s *Server) taskPage(w http.ResponseWriter, r *http.Request, rc *reqCtx, tab string) {
	t := s.visibleTask(w, r, rc)
	if t == nil {
		return
	}
	p := s.newPage(rc, t.Title, t.Name)
	d, err := s.taskData(r, rc, p, t)
	if err != nil {
		s.fail(w, err)
		return
	}
	d.Tab = tab
	if tab == "statement" {
		if d.Statement, err = s.statementPage(r, rc, p, t); err != nil {
			s.fail(w, err)
			return
		}
	}
	if id, err := strconv.ParseInt(r.URL.Query().Get("submitted"), 10, 64); err == nil && id > 0 {
		d.Submitted = id
		p.Flash = p.T("Submission #%d received. Watch its status below: it updates by itself.", id)
	}
	p.Data = d
	s.render(w, "task", http.StatusOK, p)
}

func (s *Server) serveBlob(w http.ResponseWriter, r *http.Request, digest, name, ctype string, attachment bool) {
	rc, err := s.blobs.Open(r.Context(), digest)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer rc.Close()
	if ctype == "" {
		ctype = mime.TypeByExtension(path.Ext(name))
	}
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	// Linked with ?v=<digest prefix> the content never changes at that
	// address; without it (an attachment replaced under the same name) the
	// browser revalidates, so the new file shows at once.
	if v := r.URL.Query().Get("v"); len(v) >= 12 && strings.HasPrefix(digest, v) {
		h.Set("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "private, no-cache")
	}
	h.Set("ETag", `"`+digest+`"`)
	disp := "inline"
	if attachment {
		disp = "attachment"
	}
	h.Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": name}))
	if r.Header.Get("If-None-Match") == `"`+digest+`"` {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	io.Copy(w, rc)
}

func (s *Server) handleAttachment(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t := s.visibleTask(w, r, rc)
	if t == nil {
		return
	}
	name := r.PathValue("file")
	d, ok := t.AttachDigest[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.serveBlob(w, r, d, name, "", true)
}

// ---------------------------------------------------------------- submit

func (s *Server) submitError(w http.ResponseWriter, r *http.Request, rc *reqCtx, status int, msg string) {
	p := s.newPage(rc, "", "")
	if webkit.IsHTMX(r) {
		w.Header().Set("HX-Retarget", "#submit-result")
		w.Header().Set("HX-Reswap", "innerHTML")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		fmt.Fprintf(w, `<span class="bad">%s</span>`, templateEscape(i18n.TDetail(p.Lang, msg)))
		return
	}
	s.errorPage(w, r, rc.contest, status, "Submission rejected", msg)
}

func (s *Server) handleSubmit(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t := s.visibleTask(w, r, rc)
	if t == nil {
		return
	}
	if why := submitBlocked(rc, t); why != "" {
		s.submitError(w, r, rc, http.StatusForbidden, why)
		return
	}
	if !s.limiter.Allow(r.Context(), "submit:"+itoa(rc.part.ID), s.cfg.RateLimitPerMinute, time.Minute) {
		s.submitError(w, r, rc, http.StatusTooManyRequests, "Too many requests, please slow down.")
		return
	}
	files, lang, errMsg := s.readSubmission(w, r, rc, t)
	if errMsg != "" {
		s.submitError(w, r, rc, http.StatusBadRequest, errMsg)
		return
	}
	now := rc.now
	stats, err := s.q.SubmissionStats(r.Context(), sqlc.SubmissionStatsParams{ParticipationIds: rc.group, TaskID: t.ID})
	if err != nil {
		s.fail(w, err)
		return
	}
	if rc.status.Official && !rc.part.Unrestricted {
		cl := contest.Limit{MaxNumber: rc.contest.MaxSubmissionNumber, MinInterval: rc.contest.MinSubmissionIntervalS}
		tl := contest.Limit{MaxNumber: t.MaxSubmissionNumber, MinInterval: t.MinSubmissionIntervalS}
		u := contest.Usage{ContestCount: stats.ContestCount, TaskCount: stats.TaskCount, ContestLast: stats.ContestLast, TaskLast: stats.TaskLast}
		if err := contest.Check(cl, tl, u, now); err != nil {
			var le *contest.LimitError
			if errors.As(err, &le) {
				s.submitError(w, r, rc, http.StatusTooManyRequests, le.Key)
				return
			}
		}
	}
	if files, err = s.mergePreviousOutputs(r, rc, t, files); err != nil {
		s.fail(w, err)
		return
	}
	id, err := s.storeSubmission(r, rc, t, files, lang, now)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.log.Debug("submission received", "submission", id, "participation", rc.part.ID, "task", t.Name)
	if webkit.IsHTMX(r) {
		p := s.newPage(rc, t.Title, t.Name)
		d, err := s.taskData(r, rc, p, t)
		if err != nil {
			s.fail(w, err)
			return
		}
		d.Tab, d.Submitted = "submissions", id
		p.Data = d
		// The page shows a notification, and the tab (form and list) is
		// replaced: the new row updates by itself as it is judged.
		trig, _ := json.Marshal(map[string]any{"cms-submitted": map[string]any{"id": id, "title": p.T("Submission sent"),
			"text": p.T("Submission #%d received. Watch its status below: it updates by itself.", id)}})
		w.Header().Set("HX-Trigger", string(trig))
		s.renderPartial(w, "subs-tab", p)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/%s/tasks/%s/submissions?submitted=%d", rc.contest.Name, t.Name, id), http.StatusSeeOther)
}

type submittedFile struct {
	name   string // submission format entry, e.g. "sum.%l"
	data   []byte
	digest string // already stored (output-only merge), data unused
}

// readSubmission parses and validates the multipart form.
func (s *Server) readSubmission(w http.ResponseWriter, r *http.Request, rc *reqCtx, t *taskView) ([]submittedFile, *langs.Language, string) {
	return s.readSources(w, r, rc, t, s.submissionLimit())
}

// submissionLimit bounds a whole submission request.
func (s *Server) submissionLimit() int64 {
	if max := int64(s.cfg.MaxSubmissionBytes); max > 0 {
		return max
	}
	return 1 << 20
}

// fileLimit is the size limit of each file of a submission to t: the
// task's source limit, the contest's and the server's.
func (s *Server) fileLimit(rc *reqCtx, t *taskView) int64 {
	perFile := t.SourceLimit
	max := s.submissionLimit()
	if perFile <= 0 {
		perFile = max
	}
	if m := rc.contest.MaxSubmissionBytes; m != nil && *m < perFile {
		perFile = *m
	}
	return perFile
}

// readSources reads the files (and language) of a submission or a user
// test from a multipart request of at most max bytes.
func (s *Server) readSources(w http.ResponseWriter, r *http.Request, rc *reqCtx, t *taskView, max int64) ([]submittedFile, *langs.Language, string) {
	r.Body = http.MaxBytesReader(w, r.Body, max+64<<10)
	if err := r.ParseMultipartForm(max); err != nil {
		return nil, nil, "The submission is too large."
	}
	var lang *langs.Language
	if t.NeedsLanguage {
		id := r.FormValue("language")
		for _, l := range t.Languages {
			if l.ID == id {
				lang = l
			}
		}
		if lang == nil {
			return nil, nil, "Please choose an allowed language."
		}
	}
	perFile := t.SourceLimit
	if perFile <= 0 {
		perFile = max
	}
	if m := rc.contest.MaxSubmissionBytes; m != nil && *m < perFile {
		perFile = *m
	}
	var files []submittedFile
	for _, format := range t.Formats {
		f, hdr, err := r.FormFile(format)
		if err != nil && t.Editor() && strings.TrimSpace(r.FormValue("source")) != "" {
			// Typed in the page's editor (browsers send its lines with CRLF).
			data := []byte(strings.ReplaceAll(r.FormValue("source"), "\r\n", "\n"))
			if int64(len(data)) > perFile {
				return nil, nil, "A file exceeds the size limit."
			}
			files = append(files, submittedFile{name: format, data: data})
			continue
		}
		if err != nil {
			if t.NeedsLanguage {
				return nil, nil, "Every file of the submission is required."
			}
			continue // output-only tasks accept partial submissions
		}
		data, err := io.ReadAll(io.LimitReader(f, perFile+1))
		f.Close()
		if err != nil {
			return nil, nil, "Could not read the file."
		}
		if int64(len(data)) > perFile {
			return nil, nil, "A file exceeds the size limit."
		}
		if lang != nil && strings.HasSuffix(format, ".%l") && hdr.Filename != "" && !lang.IsSource(hdr.Filename) && path.Ext(hdr.Filename) != "" {
			return nil, nil, "The file extension does not match the chosen language."
		}
		files = append(files, submittedFile{name: format, data: data})
	}
	if t.TaskType == "OutputOnly" {
		var msg string
		if files, msg = readOutputArchive(r, t, files, perFile); msg != "" {
			return nil, nil, msg
		}
	}
	if len(files) == 0 {
		return nil, nil, "Please attach at least one file."
	}
	return files, lang, ""
}

// readOutputArchive adds the outputs of an output-only submission sent as
// a zip archive (field "zip"). Every entry must be one of the task's
// output file names (directories inside the archive are ignored); files
// uploaded one by one take precedence over the archive.
func readOutputArchive(r *http.Request, t *taskView, files []submittedFile, perFile int64) ([]submittedFile, string) {
	f, hdr, err := r.FormFile("zip")
	if err != nil {
		return files, ""
	}
	defer f.Close()
	zr, err := zip.NewReader(f, hdr.Size)
	if err != nil {
		return nil, "The archive is not a valid zip file."
	}
	allowed := map[string]bool{}
	for _, name := range t.Formats {
		allowed[name] = true
	}
	have := map[string]bool{}
	for _, sf := range files {
		have[sf.name] = true
	}
	var unexpected []string
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		name := path.Base(zf.Name)
		if !allowed[name] {
			if len(unexpected) < 5 {
				unexpected = append(unexpected, name)
			}
			continue
		}
		if have[name] {
			continue
		}
		if zf.UncompressedSize64 > uint64(perFile) {
			return nil, "A file exceeds the size limit."
		}
		rd, err := zf.Open()
		if err != nil {
			return nil, "The archive is not a valid zip file."
		}
		data, err := io.ReadAll(io.LimitReader(rd, perFile+1))
		rd.Close()
		if err != nil || int64(len(data)) > perFile {
			return nil, "A file exceeds the size limit."
		}
		have[name] = true
		files = append(files, submittedFile{name: name, data: data})
	}
	if len(unexpected) > 0 {
		return nil, "Unexpected files in the archive: " + strings.Join(unexpected, ", ")
	}
	return files, ""
}

// mergePreviousOutputs completes an output-only submission with, for each
// missing output, the file of the contestant's previous submission that
// scored best on that testcase (when the dataset enables it).
func (s *Server) mergePreviousOutputs(r *http.Request, rc *reqCtx, t *taskView, files []submittedFile) ([]submittedFile, error) {
	if t.TaskType != "OutputOnly" || t.Dataset == nil {
		return files, nil
	}
	pattern, merge, err := tasktypes.OutputOnlyConfig(t.Dataset.TaskTypeParams)
	if err != nil || !merge {
		return files, nil
	}
	prev, err := s.q.BestPreviousOutputs(r.Context(), sqlc.BestPreviousOutputsParams{DatasetID: t.Dataset.ID, Pattern: pattern,
		ParticipationIds: rc.group, TaskID: t.ID})
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, f := range files {
		have[f.name] = true
	}
	for _, p := range prev {
		if !have[p.Filename] {
			files = append(files, submittedFile{name: p.Filename, digest: p.Digest})
		}
	}
	return files, nil
}

// storeSubmission saves blobs, inserts the submission and notifies the
// dispatcher. The blobs are written first (content-addressed, so a crash
// leaves at most an unreferenced blob for the garbage collector).
func (s *Server) storeSubmission(r *http.Request, rc *reqCtx, t *taskView, files []submittedFile, lang *langs.Language, now time.Time) (int64, error) {
	ctx := r.Context()
	params := make([]sqlc.CreateSubmissionFilesParams, len(files))
	for i, f := range files {
		if f.digest != "" {
			params[i] = sqlc.CreateSubmissionFilesParams{Filename: f.name, Digest: f.digest}
			continue
		}
		info, err := s.blobs.PutBytes(ctx, f.data)
		if err != nil {
			return 0, err
		}
		params[i] = sqlc.CreateSubmissionFilesParams{Filename: f.name, Digest: info.Digest}
	}
	var langID *string
	var flags []suspicious.Flag
	if lang != nil {
		langID = &lang.ID
		// Sources only (output-only submissions have no language).
		src := make(map[string][]byte, len(files))
		for _, f := range files {
			src[f.name] = f.data
		}
		flags = suspicious.Scan(lang.ID, src)
	}
	var id int64
	err := db.InTx(ctx, s.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		sub, err := q.CreateSubmission(ctx, sqlc.CreateSubmissionParams{ParticipationID: &rc.part.ID, TaskID: t.ID,
			SubmittedAt: now, Language: langID, Official: rc.status.Official})
		if err != nil {
			return err
		}
		id = sub.ID
		for i := range params {
			params[i].SubmissionID = id
		}
		if _, err = q.CreateSubmissionFiles(ctx, params); err != nil {
			return err
		}
		for _, f := range flags {
			if err := q.InsertSubmissionFlag(ctx, sqlc.InsertSubmissionFlagParams{SubmissionID: id, Kind: "source",
				Reason: f.Reason, Detail: f.Detail}); err != nil {
				return err
			}
		}
		// Last, so the audit chain's lock is held as briefly as possible.
		return q.InsertSubmissionReceipt(ctx, receipt(rc, t, id, params, langID, now, s.ips.ClientIP(r).String()))
	})
	if err != nil {
		return 0, err
	}
	// If the notification is lost the dispatcher's sweeper still finds it.
	if err := s.queue.Notify(ctx, queue.Event{Kind: queue.EventSubmission, SubmissionID: id}); err != nil {
		s.log.Warn("notify dispatcher", "submission", id, "error", err)
	}
	return id, nil
}

// receipt is a submission's entry in the audit chain (SPEC_IOI §13): who,
// what, when and the SHA-256 of every file as received.
func receipt(rc *reqCtx, t *taskView, id int64, files []sqlc.CreateSubmissionFilesParams, lang *string, at time.Time,
	ip string) sqlc.InsertSubmissionReceiptParams {
	digests := make(map[string]string, len(files))
	for _, f := range files {
		digests[f.Filename] = f.Digest
	}
	det, _ := json.Marshal(struct {
		Contest       string            `json:"contest"`
		Participation int64             `json:"participation"`
		Task          string            `json:"task"`
		Language      *string           `json:"language,omitempty"`
		SubmittedAt   time.Time         `json:"submitted_at"`
		Files         map[string]string `json:"files"`
	}{rc.contest.Name, rc.part.ID, t.Name, lang, at.UTC(), digests})
	return sqlc.InsertSubmissionReceiptParams{Actor: "contestant:" + rc.part.Username, SubmissionID: id, Details: det, Ip: ip}
}

// ---------------------------------------------------------------- submissions

func (s *Server) ownSubmission(w http.ResponseWriter, r *http.Request, rc *reqCtx) (sqlc.GetSubmissionWithResultRow, *taskView, bool) {
	var zero sqlc.GetSubmissionWithResultRow
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return zero, nil, false
	}
	// The live dataset is needed before loading: resolve the task first.
	sub, err := s.q.GetSubmission(r.Context(), id)
	if err != nil || sub.ParticipationID == nil || !slices.Contains(rc.group, *sub.ParticipationID) {
		http.NotFound(w, r)
		return zero, nil, false
	}
	t := rc.contest.TaskByID[sub.TaskID]
	if t == nil || t.Dataset == nil {
		http.NotFound(w, r)
		return zero, nil, false
	}
	row, err := s.q.GetSubmissionWithResult(r.Context(), sqlc.GetSubmissionWithResultParams{DatasetID: t.Dataset.ID, ID: id})
	if err != nil {
		s.fail(w, err)
		return zero, nil, false
	}
	return row, t, true
}

func (s *Server) handleSubmissionRow(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	row, t, ok := s.ownSubmission(w, r, rc)
	if !ok {
		return
	}
	p := s.newPage(rc, "", t.Name)
	sv := s.subViewFromDetail(p, rc, t, row)
	if err := s.queueInfo(r, &sv); err != nil {
		s.fail(w, err)
		return
	}
	s.renderPartial(w, "subrow", rowCtx{P: p, S: sv})
}

func (s *Server) handleSubmission(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	row, t, ok := s.ownSubmission(w, r, rc)
	if !ok {
		return
	}
	p := s.newPage(rc, fmt.Sprintf("#%d", row.ID), t.Name)
	d, err := s.detailData(r, p, rc, t, row)
	if err != nil {
		s.fail(w, err)
		return
	}
	p.Data = d
	s.render(w, "submission", http.StatusOK, p)
}

func (s *Server) handleSubmissionFile(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	row, t, ok := s.ownSubmission(w, r, rc)
	if !ok {
		return
	}
	if !rc.contest.SubmissionsDownloadAllowed {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	files, err := s.q.ListSubmissionFiles(r.Context(), row.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	ext := ""
	if row.Language != nil {
		if l, ok := s.langs.Get(*row.Language); ok {
			ext = l.SourceExtension()
		}
	}
	for _, f := range files {
		name := strings.ReplaceAll(f.Filename, ".%l", ext)
		if name == r.PathValue("name") {
			s.serveBlob(w, r, f.Digest, name, "text/plain; charset=utf-8", true)
			return
		}
	}
	_ = t
	http.NotFound(w, r)
}

// ---------------------------------------------------------------- misc

type docLanguage struct {
	ID, Name   string
	Extensions string
	Compile    []string
	Run        string
	// Version is the toolchain the judging machines report (the most
	// common one; "" when none reports it).
	Version string
}

func (s *Server) handleDocumentation(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	p := s.newPage(rc, "", "documentation")
	p.Title = p.T("Documentation")
	var tcs map[string][]queue.Toolchain
	if ws, err := s.queue.Workers(r.Context(), 10*time.Minute); err == nil {
		tcs = queue.Toolchains(ws)
	}
	var ls []docLanguage
	for _, l := range rc.contest.Languages {
		d := docLanguage{ID: l.ID, Name: l.Name, Extensions: strings.Join(l.SourceExtensions, " ")}
		if list := tcs[l.ID]; len(list) > 0 {
			d.Version = list[0].Version
		}
		v := langs.Vars{Sources: []string{"sol" + l.SourceExtension()}, MainSource: "sol" + l.SourceExtension(), Main: "sol",
			Executable: l.ExecutableName("sol"), Memory: 256 << 20}
		for _, c := range l.Compile {
			d.Compile = append(d.Compile, strings.Join(langs.Expand(c, v), " "))
		}
		d.Run = strings.Join(langs.Expand(l.Run, v), " ")
		ls = append(ls, d)
	}
	p.Data = ls
	s.render(w, "documentation", http.StatusOK, p)
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, blob.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	s.log.Error("request failed", "error", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

func templateEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;", "'", "&#39;")
	return r.Replace(s)
}

// jsonBytes marshals v (for SSE payloads).
func jsonBytes(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// adjustmentReasons fills the reasons of the adjusted rows (one query, only
// when some score was adjusted).
func (s *Server) adjustmentReasons(r *http.Request, rc *reqCtx, rows []overviewRow) error {
	adjusted := false
	for _, row := range rows {
		adjusted = adjusted || row.Adjustment != 0
	}
	if !adjusted {
		return nil
	}
	adjs, err := s.q.ListScoreAdjustments(r.Context(), rc.group)
	if err != nil {
		return err
	}
	for i := range rows {
		var reasons []string
		for _, a := range adjs {
			if a.TaskName == rows[i].Name {
				reasons = append(reasons, strconv.FormatFloat(a.Points, 'f', -1, 64)+": "+a.Reason)
			}
		}
		rows[i].Reasons = strings.Join(reasons, "; ")
	}
	return nil
}
