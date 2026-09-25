package contestweb

import (
	"context"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/D4ND3R/Contest-Management-System/internal/contest"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/events"
	"github.com/D4ND3R/Contest-Management-System/internal/webkit"
)

// commData is the communication page: announcements, private messages,
// the participant's questions and the answers given to everyone.
type commData struct {
	CanAsk        bool
	Announcements []sqlc.Announcement
	Messages      []sqlc.Message
	Questions     []questionView
	Public        []questionView
}

type questionView struct {
	About                   string
	Text                    string
	AskedAt, ReplyAt        time.Time
	ReplySubject, ReplyText string
	Answered, Public        bool
}

const maxQuestionLength = 4000

func (s *Server) questionView(p *page, rc *reqCtx, q sqlc.Question) questionView {
	v := questionView{About: p.T("General"), Text: q.Text, AskedAt: q.AskedAt, Public: q.Public}
	if q.TaskID != nil {
		if t := rc.contest.TaskByID[*q.TaskID]; t != nil {
			v.About = t.Name + " — " + t.Title
		}
	}
	if q.ReplyAt != nil {
		v.Answered, v.ReplyAt = true, *q.ReplyAt
		v.ReplySubject, v.ReplyText = derefString(q.ReplySubject), derefString(q.ReplyText)
	}
	return v
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// canAsk reports whether the participant may ask questions now.
func canAsk(rc *reqCtx) bool {
	return rc.contest.AllowQuestions && (rc.status.Phase == contest.Running || rc.part.Unrestricted)
}

func (s *Server) commData(ctx context.Context, p *page, rc *reqCtx) (*commData, error) {
	d := &commData{CanAsk: canAsk(rc) && !rc.sess.ReadOnly}
	var err error
	if d.Announcements, err = s.q.ListAnnouncements(ctx, rc.contest.ID); err != nil {
		return nil, err
	}
	if d.Messages, err = s.q.ListMessagesByParticipation(ctx, rc.part.ID); err != nil {
		return nil, err
	}
	qs, err := s.q.ListQuestionsByParticipation(ctx, rc.part.ID)
	if err != nil {
		return nil, err
	}
	for _, q := range qs {
		d.Questions = append(d.Questions, s.questionView(p, rc, q))
	}
	pub, err := s.q.ListPublicAnswers(ctx, rc.contest.ID)
	if err != nil {
		return nil, err
	}
	for _, q := range pub {
		if q.ParticipationID != rc.part.ID {
			d.Public = append(d.Public, s.questionView(p, rc, q))
		}
	}
	// Opening the page marks everything as read (not in the read-only
	// administrator view).
	if !rc.sess.ReadOnly {
		if err := s.q.SetCommunicationSeen(ctx, rc.part.ID); err != nil {
			return nil, err
		}
	}
	return d, nil
}

func (s *Server) handleCommunication(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	p := s.newPage(rc, "", "communication")
	p.Title = p.T("Communication")
	d, err := s.commData(r.Context(), p, rc)
	if err != nil {
		s.fail(w, err)
		return
	}
	p.Data, p.Unread = d, 0
	if r.URL.Query().Get("sent") != "" {
		p.Flash = p.T("Your question was sent.")
	}
	s.render(w, "communication", http.StatusOK, p)
}

// handleCommunicationList re-renders the lists (live updates).
func (s *Server) handleCommunicationList(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	p := s.newPage(rc, "", "communication")
	d, err := s.commData(r.Context(), p, rc)
	if err != nil {
		s.fail(w, err)
		return
	}
	p.Data = d
	s.renderPartial(w, "communication", p)
}

// handleAsk stores a question and notifies the staff.
func (s *Server) handleAsk(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	fail := func(status int, msg string) {
		p := s.newPage(rc, "", "communication")
		if webkit.IsHTMX(r) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(status)
			w.Write([]byte(p.T(msg)))
			return
		}
		s.errorPage(w, r, rc.contest, status, "Communication", msg)
	}
	if !canAsk(rc) {
		fail(http.StatusForbidden, "Questions are closed.")
		return
	}
	text := strings.TrimSpace(r.FormValue("text"))
	if text == "" || utf8.RuneCountInString(text) > maxQuestionLength {
		fail(http.StatusBadRequest, "Write a question (at most 4000 characters).")
		return
	}
	var taskID *int64
	subject := "General"
	if name := r.FormValue("task"); name != "" {
		t := rc.contest.TaskByName[name]
		if t == nil {
			fail(http.StatusBadRequest, "Unknown task.")
			return
		}
		taskID, subject = &t.ID, t.Name
	}
	if n := int(rc.contest.QuestionsPerMinute); n > 0 && !s.limiter.Allow(r.Context(), "question:"+itoa(rc.part.ID), n, time.Minute) {
		fail(http.StatusTooManyRequests, "You are asking too many questions; wait a minute.")
		return
	}
	q, err := s.q.CreateQuestion(r.Context(), sqlc.CreateQuestionParams{ParticipationID: rc.part.ID, TaskID: taskID,
		AskedAt: rc.now, Subject: subject, Text: text})
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := events.Publish(r.Context(), s.rdb, s.ns, events.Event{Type: events.TypeQuestionNew, ContestID: rc.contest.ID,
		ParticipationID: rc.part.ID, TaskID: derefID(q.TaskID), Text: rc.part.Username + " · " + subject}); err != nil {
		s.log.Warn("publish question", "error", err)
	}
	s.log.Info("question asked", "participation", rc.part.ID, "question", q.ID)
	if webkit.IsHTMX(r) {
		s.handleCommunicationList(w, r, rc)
		return
	}
	webkit.Redirect(w, r, "/"+rc.contest.Name+"/communication?sent=1")
}

func derefID(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}
