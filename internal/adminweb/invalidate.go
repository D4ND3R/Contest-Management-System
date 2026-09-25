package adminweb

import (
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/events"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
)

// handleSubmissionInvalidate excludes a submission from every score (A3):
// a reason is mandatory, contestants see it, and scores and rankings are
// recomputed at once by the dispatcher.
func (s *Server) handleSubmissionInvalidate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, _ := pathID(r, "id")
	reason := strings.TrimSpace(r.FormValue("reason"))
	if reason == "" || utf8.RuneCountInString(reason) > 1000 {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Write the reason (at most 1000 characters): the contestant sees it.")
		return
	}
	sub, err := s.q.SetSubmissionInvalidated(r.Context(), sqlc.SetSubmissionInvalidatedParams{ID: id, Reason: reason, AdminID: &rc.admin.ID})
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r, rc)
		} else {
			s.internalError(w, r, rc, err)
		}
		return
	}
	rc.target("submission", sub.ID)
	rc.note("reason", reason)
	s.submissionCounted(r, sub)
	s.done(w, r, "/submissions/"+strconv.FormatInt(sub.ID, 10), "Submission invalidated: it no longer counts.")
}

// handleSubmissionRestore makes an invalidated submission count again.
func (s *Server) handleSubmissionRestore(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, _ := pathID(r, "id")
	sub, err := s.q.ClearSubmissionInvalidated(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r, rc)
		} else {
			s.internalError(w, r, rc, err)
		}
		return
	}
	rc.target("submission", sub.ID)
	s.submissionCounted(r, sub)
	s.done(w, r, "/submissions/"+strconv.FormatInt(sub.ID, 10), "Submission restored: it counts again.")
}

// submissionCounted asks the dispatcher to recompute the task score and
// refreshes the contestant's pages.
func (s *Server) submissionCounted(r *http.Request, sub sqlc.Submission) {
	if err := s.queue.Notify(r.Context(), queue.Event{Kind: queue.EventReaggregate, SubmissionID: sub.ID}); err != nil {
		s.log.Warn("notify dispatcher", "error", err)
	}
	if sub.ParticipationID != nil {
		s.publish(r.Context(), events.Event{Type: events.TypeSubmission, ParticipationID: *sub.ParticipationID,
			TaskID: sub.TaskID, SubmissionID: sub.ID, Status: "scored"})
	}
}
