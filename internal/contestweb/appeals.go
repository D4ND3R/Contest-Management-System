package contestweb

import (
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/D4ND3R/Contest-Management-System/internal/contest"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
)

// Appeals (SPEC_IOI §14): once a contestant's contest is over, and until
// the contest's appeals deadline, they may contest the evaluation of a
// task; the staff answer each appeal on the admin site.

const (
	maxAppealLength = 4000
	maxAppeals      = 20
)

// appealsShown: the contest takes appeals (the page exists once the
// contestant's contest is over).
func appealsShown(rc *reqCtx) bool {
	return rc.contest.AppealsUntil != nil && (rc.status.Phase == contest.Finished || rc.status.Phase == contest.Analysis)
}

// appealsOpen: new appeals are accepted now.
func appealsOpen(rc *reqCtx) bool {
	return appealsShown(rc) && rc.now.Before(*rc.contest.AppealsUntil)
}

type appealsData struct {
	Open    bool
	Until   time.Time
	Appeals []appealView
	Error   string
}

type appealView struct {
	Task                   string
	Submission             int64
	At                     time.Time
	Status, Text, Response string
}

func (s *Server) handleAppeals(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	if !appealsShown(rc) {
		s.errorPage(w, r, rc.contest, http.StatusNotFound, "Appeals", "Appeals are not open.")
		return
	}
	s.renderAppeals(w, r, rc, http.StatusOK, "")
}

func (s *Server) renderAppeals(w http.ResponseWriter, r *http.Request, rc *reqCtx, status int, errMsg string) {
	p := s.newPage(rc, "", "appeals")
	p.Title = p.T("Appeals")
	d := &appealsData{Open: appealsOpen(rc), Until: *rc.contest.AppealsUntil, Error: errMsg}
	rows, err := s.q.ListAppealsByParticipation(r.Context(), rc.part.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	for _, a := range rows {
		v := appealView{At: a.CreatedAt, Status: a.Status, Text: a.Text, Response: a.Response}
		if a.TaskName != nil {
			v.Task = *a.TaskName
		}
		if a.SubmissionID != nil {
			v.Submission = *a.SubmissionID
		}
		d.Appeals = append(d.Appeals, v)
	}
	if r.URL.Query().Get("sent") != "" {
		p.Flash = p.T("Your appeal was sent.")
	}
	p.Data = d
	s.render(w, "appeals", status, p)
}

func (s *Server) handleAppeal(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	if !appealsOpen(rc) {
		s.errorPage(w, r, rc.contest, http.StatusForbidden, "Appeals", "Appeals are closed.")
		return
	}
	text := strings.TrimSpace(r.FormValue("text"))
	if text == "" || utf8.RuneCountInString(text) > maxAppealLength {
		s.renderAppeals(w, r, rc, http.StatusBadRequest, "Explain your appeal (at most 4000 characters).")
		return
	}
	t := rc.contest.TaskByName[r.FormValue("task")]
	if t == nil {
		s.renderAppeals(w, r, rc, http.StatusBadRequest, "Unknown task.")
		return
	}
	var subID *int64
	if v := strings.TrimSpace(r.FormValue("submission")); v != "" {
		id, err := strconv.ParseInt(strings.TrimPrefix(v, "#"), 10, 64)
		if err == nil {
			if task, err := s.q.GetOwnSubmissionTask(r.Context(), sqlc.GetOwnSubmissionTaskParams{ID: id, ParticipationIds: rc.group}); err != nil || task != t.ID {
				id = 0
			}
		}
		if err != nil || id == 0 {
			s.renderAppeals(w, r, rc, http.StatusBadRequest, "That submission is not yours or not of this task.")
			return
		}
		subID = &id
	}
	if !s.limiter.Allow(r.Context(), "appeal:"+itoa(rc.part.ID), 3, time.Minute) {
		s.renderAppeals(w, r, rc, http.StatusTooManyRequests, "Too many requests, please slow down.")
		return
	}
	if n, err := s.q.CountAppealsByParticipation(r.Context(), rc.part.ID); err != nil {
		s.fail(w, err)
		return
	} else if n >= maxAppeals {
		s.renderAppeals(w, r, rc, http.StatusTooManyRequests, "You cannot send more appeals.")
		return
	}
	a, err := s.q.CreateAppeal(r.Context(), sqlc.CreateAppealParams{ParticipationID: rc.part.ID, TaskID: &t.ID, SubmissionID: subID, Text: text})
	if err != nil {
		s.fail(w, err)
		return
	}
	s.log.Info("appeal sent", "participation", rc.part.ID, "appeal", a.ID)
	http.Redirect(w, r, "/"+rc.contest.Name+"/appeals?sent=1", http.StatusSeeOther)
}
