package contestweb

import (
	"net/http"
	"strings"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
)

// resultCard is the latest result shown next to the submit button: the
// status, the score, a chip per subtask and the first lines of a
// compilation error, updated live.
type resultCard struct {
	Sub          subView
	Subtasks     []detailGroup
	CompileError string
}

// cardCtx gives the card template the page, the card (nil: nothing
// submitted yet) and the task.
type cardCtx struct {
	P    *page
	C    *resultCard
	Task *taskView
}

// submittedCtx is the answer to a submission from the page: the new card
// and the list (swapped out of band).
type submittedCtx struct {
	Card cardCtx
	Page *page
}

// testsCtx gives the test form its page, task data and task; From is
// where to come back after running a test ("testing": the Testing page).
type testsCtx struct {
	P    *page
	D    *taskData
	Task *taskView
	From string
}

// maxCompileLines bounds the compiler output shown on the card.
const maxCompileLines = 12

func (s *Server) resultCardByID(r *http.Request, p *page, rc *reqCtx, t *taskView, id int64) (*resultCard, error) {
	row, err := s.q.GetSubmissionWithResult(r.Context(), sqlc.GetSubmissionWithResultParams{DatasetID: t.Dataset.ID, ID: id})
	if err != nil {
		return nil, err
	}
	return s.resultCard(r, p, rc, t, row)
}

func (s *Server) resultCard(r *http.Request, p *page, rc *reqCtx, t *taskView, row sqlc.GetSubmissionWithResultRow) (*resultCard, error) {
	d, err := s.detailData(r, p, rc, t, row)
	if err != nil {
		return nil, err
	}
	c := &resultCard{Sub: d.Sub}
	if d.Details != nil {
		for _, g := range d.Details.Groups {
			if g.Title != "" {
				c.Subtasks = append(c.Subtasks, detailGroup{Title: g.Title, Index: g.Index, Score: g.Score, Max: g.Max, Class: g.Class})
			}
		}
	}
	if d.Compilation != nil && !d.Compilation.OK {
		out := strings.TrimSpace(d.Compilation.Stderr)
		if out == "" {
			out = strings.TrimSpace(d.Compilation.Stdout)
		}
		lines := strings.Split(out, "\n")
		if len(lines) > maxCompileLines {
			lines = append(lines[:maxCompileLines], "…")
		}
		c.CompileError = strings.Join(lines, "\n")
	}
	return c, nil
}

// handleSubmissionCard renders the result card of a submission (live
// refresh).
func (s *Server) handleSubmissionCard(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	row, t, ok := s.ownSubmission(w, r, rc)
	if !ok {
		return
	}
	p := s.newPage(rc, t.Title, t.Name)
	c, err := s.resultCard(r, p, rc, t, row)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.renderPartial(w, "resultcard", cardCtx{P: p, C: c, Task: t})
}
