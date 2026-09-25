package adminweb

import (
	"context"
	"html/template"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/highlight"
	"github.com/D4ND3R/Contest-Management-System/internal/plagiarism"
)

// sourceLimit bounds each file read for the plagiarism report; larger
// files (generated tables) are left out.
const sourceLimit = 512 << 10

// plagiarismReaders is the number of files read at once (remote stores).
const plagiarismReaders = 8

type plagiarismPage struct {
	ContestID int64
	Contest   string
	Tasks     []sqlc.Task
	TaskID    int64
	Which     string
	Threshold int
	Ran       bool
	Compared  int
	Skipped   int
	Templates []string
	Pairs     []plagiarismPair
}

type plagiarismPair struct {
	A, B       sqlc.PlagiarismCandidatesRow
	Similarity int
	Shared     int
}

// sourceName is a submission file's name with its language's extension
// (it selects the syntax).
func (s *Server) sourceName(filename string, lang *string) string {
	ext := ""
	if lang != nil {
		if l, ok := s.langs.Get(*lang); ok {
			ext = l.SourceExtension()
		}
	}
	return strings.ReplaceAll(filename, ".%l", ext)
}

// readSource returns a file as text, or false when it is binary, too
// large or unreadable.
func (s *Server) readSource(ctx context.Context, digest string) (string, bool) {
	b, truncated, err := blob.ReadLimited(ctx, s.blobs, digest, sourceLimit)
	if err != nil || truncated || !utf8.Valid(b) || strings.IndexByte(string(b), 0) >= 0 {
		return "", false
	}
	return string(b), true
}

// taskBase is the code handed to the contestants of a task: its
// attachments in a known language and the live dataset's graders and
// stubs (not the checker, manager or interactor, which they never see).
func (s *Server) taskBase(ctx context.Context, t sqlc.Task) (plagiarism.Base, []string, error) {
	var files []plagiarism.Source
	var names []string
	add := func(name, digest string) {
		if highlight.ForFile(name) == nil {
			return
		}
		if text, ok := s.readSource(ctx, digest); ok {
			files = append(files, plagiarism.Source{Name: name, Data: text})
			names = append(names, name)
		}
	}
	atts, err := s.q.ListAttachments(ctx, t.ID)
	if err != nil {
		return nil, nil, err
	}
	for _, a := range atts {
		add(a.Filename, a.Digest)
	}
	if t.ActiveDatasetID != nil {
		ms, err := s.q.ListManagers(ctx, *t.ActiveDatasetID)
		if err != nil {
			return nil, nil, err
		}
		for _, m := range ms {
			switch strings.TrimSuffix(m.Filename, path.Ext(m.Filename)) {
			case "checker", "manager", "interactor":
				continue
			}
			add(m.Filename, m.Digest)
		}
	}
	return plagiarism.NewBase(files), names, nil
}

// handlePlagiarism is the similarity report of one task: one submission
// per contestant (the latest or the best), every pair above the
// threshold, most similar first.
func (s *Server) handlePlagiarism(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	ctx := r.Context()
	d := &plagiarismPage{ContestID: c.ID, Contest: c.Name, Which: "last", Threshold: 60}
	var err error
	if d.Tasks, err = s.q.ListTasksByContest(ctx, &c.ID); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	qv := r.URL.Query()
	if qv.Get("which") == "best" {
		d.Which = "best"
	}
	if v, err := strconv.Atoi(qv.Get("threshold")); err == nil && v >= 1 && v <= 100 {
		d.Threshold = v
	}
	d.TaskID, _ = strconv.ParseInt(qv.Get("task"), 10, 64)
	var task *sqlc.Task
	for i := range d.Tasks {
		if d.Tasks[i].ID == d.TaskID {
			task = &d.Tasks[i]
		}
	}
	render := func() {
		s.render(w, "plagiarism", http.StatusOK, s.newPage(w, r, rc, "Plagiarism", "contests", d).
			crumb("Contests", "/contests").crumb(c.Name, "/contests/"+strconv.FormatInt(c.ID, 10)))
	}
	if task == nil {
		render()
		return
	}
	d.Ran = true
	base, names, err := s.taskBase(ctx, *task)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	d.Templates = names
	cands, err := s.q.PlagiarismCandidates(ctx, sqlc.PlagiarismCandidatesParams{TaskID: task.ID, ContestID: c.ID, Best: d.Which == "best"})
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	docs, err := s.fingerprints(ctx, cands, base, c.TeamMode)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	byID := map[int64]sqlc.PlagiarismCandidatesRow{}
	var usable []*plagiarism.Doc
	for i, doc := range docs {
		byID[doc.ID] = cands[i]
		if doc.Tokens < plagiarism.K {
			d.Skipped++
			continue
		}
		usable = append(usable, doc)
	}
	d.Compared = len(usable)
	for _, p := range plagiarism.Compare(usable, plagiarism.Options{Threshold: float64(d.Threshold) / 100}) {
		d.Pairs = append(d.Pairs, plagiarismPair{A: byID[p.A.ID], B: byID[p.B.ID], Similarity: int(p.Similarity*100 + 0.5), Shared: p.Shared})
	}
	render()
}

// fingerprints reads the files of the candidates (a few at a time) and
// fingerprints them, in the candidates' order.
func (s *Server) fingerprints(ctx context.Context, cands []sqlc.PlagiarismCandidatesRow, base plagiarism.Base, teams bool) ([]*plagiarism.Doc, error) {
	ids := make([]int64, len(cands))
	for i, c := range cands {
		ids[i] = c.ID
	}
	files, err := s.q.ListSubmissionFilesBySubmissions(ctx, ids)
	if err != nil {
		return nil, err
	}
	bySub := map[int64][]sqlc.SubmissionFile{}
	for _, f := range files {
		bySub[f.SubmissionID] = append(bySub[f.SubmissionID], f)
	}
	docs := make([]*plagiarism.Doc, len(cands))
	next := make(chan int)
	var wg sync.WaitGroup
	for range plagiarismReaders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				c := cands[i]
				var srcs []plagiarism.Source
				for _, f := range bySub[c.ID] {
					name := s.sourceName(f.Filename, c.Language)
					if highlight.ForFile(name) == nil {
						continue
					}
					if text, ok := s.readSource(ctx, f.Digest); ok {
						srcs = append(srcs, plagiarism.Source{Name: name, Data: text})
					}
				}
				var group int64
				if teams && c.TeamID != nil {
					group = *c.TeamID
				}
				docs[i] = plagiarism.Fingerprint(c.ID, group, srcs, base)
			}
		}()
	}
	for i := range cands {
		select {
		case next <- i:
		case <-ctx.Done():
		}
	}
	close(next)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return docs, nil
}

type compareSide struct {
	Sub   sqlc.AdminGetSubmissionRow
	Files []compareFile
}

type compareFile struct {
	Name string
	HTML template.HTML
}

type comparePage struct {
	ContestID  int64
	A, B       compareSide
	Similarity int
}

// handlePlagiarismCompare shows two submissions side by side with the
// lines they share marked.
func (s *Server) handlePlagiarismCompare(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	ctx := r.Context()
	var subs [2]sqlc.AdminGetSubmissionRow
	for i, key := range []string{"a", "b"} {
		id, _ := strconv.ParseInt(r.URL.Query().Get(key), 10, 64)
		sub, err := s.q.AdminGetSubmission(ctx, id)
		if err != nil || sub.ContestID != c.ID || sub.ParticipationID == nil {
			if err != nil && !isNotFound(err) {
				s.internalError(w, r, rc, err)
			} else {
				s.notFound(w, r, rc)
			}
			return
		}
		subs[i] = sub
	}
	if subs[0].TaskID != subs[1].TaskID {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "The two submissions are for different tasks.")
		return
	}
	task, err := s.q.GetTask(ctx, subs[0].TaskID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	base, _, err := s.taskBase(ctx, task)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	var sources [2][]plagiarism.Source
	var docs [2]*plagiarism.Doc
	for i, sub := range subs {
		fs, err := s.q.ListSubmissionFiles(ctx, sub.ID)
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		for _, f := range fs {
			text, ok := s.readSource(ctx, f.Digest)
			if !ok {
				text = "(" + adminTr(r)("binary or too large") + ")"
			}
			sources[i] = append(sources[i], plagiarism.Source{Name: s.sourceName(f.Filename, sub.Language), Data: text})
		}
		docs[i] = plagiarism.Fingerprint(sub.ID, 0, sources[i], base)
	}
	d := &comparePage{ContestID: c.ID}
	if pairs := plagiarism.Compare(docs[:], plagiarism.Options{Common: 2}); len(pairs) > 0 {
		d.Similarity = int(pairs[0].Similarity*100 + 0.5)
	}
	la, lb := plagiarism.Matches(docs[0], docs[1])
	for i, lines := range []map[int]bool{la, lb} {
		side := compareSide{Sub: subs[i]}
		offsets := plagiarism.Offsets(sources[i])
		for j, f := range sources[i] {
			end := int(^uint(0) >> 1)
			if j+1 < len(offsets) {
				end = offsets[j+1]
			}
			marked := map[int]bool{}
			for l := range lines {
				if l >= offsets[j] && l < end {
					marked[l-offsets[j]] = true
				}
			}
			side.Files = append(side.Files, compareFile{Name: f.Name, HTML: highlight.HTMLMarked(f.Data, highlight.ForFile(f.Name), marked)})
		}
		if i == 0 {
			d.A = side
		} else {
			d.B = side
		}
	}
	s.render(w, "plagiarism_compare", http.StatusOK, s.newPage(w, r, rc, "Compare submissions", "contests", d).
		crumb("Contests", "/contests").crumb(c.Name, "/contests/"+strconv.FormatInt(c.ID, 10)).
		crumb("Plagiarism", "/contests/"+strconv.FormatInt(c.ID, 10)+"/plagiarism?task="+strconv.FormatInt(task.ID, 10)))
}
