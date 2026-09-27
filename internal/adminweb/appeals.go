package adminweb

import (
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
)

// Appeals (SPEC_IOI §14): the staff answer each appeal, accepting or
// rejecting it. Accepting records the decision; the correction itself
// (a rejudge on a fixed dataset, a score adjustment) is done with the
// usual tools, which are audited on their own.

type appealsPage struct {
	C      sqlc.Contest
	Rows   []sqlc.AdminListAppealsRow
	Status string
}

func (s *Server) handleAppealsList(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	d := &appealsPage{C: c, Status: r.URL.Query().Get("status")}
	p := sqlc.AdminListAppealsParams{ContestID: c.ID}
	switch d.Status {
	case "open", "accepted", "rejected":
		p.Status = &d.Status
	default:
		d.Status = ""
	}
	var err error
	if d.Rows, err = s.q.AdminListAppeals(r.Context(), p); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	s.render(w, "appeals", http.StatusOK, s.contestCrumbs(s.newPage(w, r, rc, "Appeals", "appeals", d), c))
}

func (s *Server) handleAppealAnswer(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, _ := pathID(r, "id")
	contestID, err := s.q.GetAppealContest(r.Context(), id)
	if isNotFound(err) {
		s.notFound(w, r, rc)
		return
	}
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	status := r.FormValue("status")
	response := strings.TrimSpace(r.FormValue("response"))
	if status != "accepted" && status != "rejected" && status != "open" {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Choose accept or reject.")
		return
	}
	if status != "open" && response == "" || utf8.RuneCountInString(response) > 4000 {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Write an answer for the contestant (at most 4000 characters).")
		return
	}
	if err := s.q.AnswerAppeal(r.Context(), sqlc.AnswerAppealParams{ID: id, Status: status, Response: response, HandledBy: &rc.admin.ID}); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("appeal", id)
	rc.note("status", status)
	s.done(w, r, "/contests/"+strconv.FormatInt(contestID, 10)+"/appeals", "Appeal answered.")
}
