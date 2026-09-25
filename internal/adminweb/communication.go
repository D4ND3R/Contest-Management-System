package adminweb

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/D4ND3R/Contest-Management-System/internal/contest"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/events"
	"github.com/D4ND3R/Contest-Management-System/internal/webkit"
	"github.com/jackc/pgx/v5"
)

// questionsPage is the staff inbox (A1): pending questions oldest first,
// optionally one contest or task, with quick answers.
type questionsPage struct {
	Rows      []sqlc.AdminListQuestionsRow
	Contests  []sqlc.Contest
	Tasks     []sqlc.Task
	Contest   int64
	Task      int64
	All       bool
	Query     string
	Quick     []string
	CanAnswer bool
}

func (s *Server) publish(ctx context.Context, e events.Event) {
	if err := events.Publish(ctx, s.rdb, s.ns, e); err != nil {
		s.log.Warn("publish event", "type", e.Type, "error", err)
	}
}

func (s *Server) questionsData(r *http.Request, rc *reqCtx) (*questionsPage, error) {
	q := r.URL.Query()
	d := &questionsPage{All: q.Get("all") != "", Quick: contest.QuickAnswers, CanAnswer: roleAllows(rc.admin.Role, permMessaging)}
	d.Contest, _ = strconv.ParseInt(q.Get("contest"), 10, 64)
	d.Task, _ = strconv.ParseInt(q.Get("task"), 10, 64)
	v := url.Values{}
	p := sqlc.AdminListQuestionsParams{WithAnswered: d.All}
	if d.Contest != 0 {
		p.ContestID = &d.Contest
		v.Set("contest", strconv.FormatInt(d.Contest, 10))
	}
	if d.Task != 0 {
		p.TaskID = &d.Task
		v.Set("task", strconv.FormatInt(d.Task, 10))
	}
	if d.All {
		v.Set("all", "1")
	}
	d.Query = v.Encode()
	var err error
	if d.Rows, err = s.q.AdminListQuestions(r.Context(), p); err != nil {
		return nil, err
	}
	if d.Contests, err = s.q.ListContests(r.Context()); err != nil {
		return nil, err
	}
	if d.Contest != 0 {
		d.Tasks, err = s.q.ListTasksByContest(r.Context(), &d.Contest)
	} else {
		d.Tasks, err = s.q.ListTasks(r.Context())
	}
	return d, err
}

func (s *Server) handleQuestions(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	d, err := s.questionsData(r, rc)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	p := s.newPage(w, r, rc, "Questions", "questions", d)
	if r.URL.Query().Get("fragment") != "" {
		s.renderPartial(w, "questions-list", wrap{P: p, V: d})
		return
	}
	s.render(w, "questions", http.StatusOK, p)
}

// handleQuestionCount is the menu counter (refreshed on new questions).
func (s *Server) handleQuestionCount(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	p := s.newPage(w, r, rc, "", "", nil)
	s.renderPartial(w, "question-count", p)
}

func (s *Server) loadQuestion(w http.ResponseWriter, r *http.Request, rc *reqCtx) (sqlc.Question, bool) {
	id, _ := pathID(r, "id")
	q, err := s.q.GetQuestion(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r, rc)
		} else {
			s.internalError(w, r, rc, err)
		}
		return q, false
	}
	return q, true
}

// questionDone answers an inbox action: the updated card for htmx, the
// inbox otherwise.
func (s *Server) questionDone(w http.ResponseWriter, r *http.Request, rc *reqCtx, id int64, msg string) {
	if !webkit.IsHTMX(r) {
		back := r.FormValue("back")
		if back == "" || !strings.HasPrefix(back, "/questions") {
			back = "/questions"
		}
		s.done(w, r, back, msg)
		return
	}
	rows, err := s.q.AdminListQuestions(r.Context(), sqlc.AdminListQuestionsParams{QuestionID: &id, WithAnswered: true})
	if err != nil || len(rows) == 0 {
		s.internalError(w, r, rc, errors.Join(err, pgx.ErrNoRows))
		return
	}
	p := s.newPage(w, r, rc, "", "", nil)
	s.renderPartial(w, "question", wrap{P: p, V: questionCard{Q: rows[0], Quick: contest.QuickAnswers, CanAnswer: true}})
}

// questionCard is one question of the inbox.
type questionCard struct {
	Q         sqlc.AdminListQuestionsRow
	Quick     []string
	CanAnswer bool
	Back      string
}

func (s *Server) handleQuestionReply(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	q, ok := s.loadQuestion(w, r, rc)
	if !ok {
		return
	}
	quick := strings.TrimSpace(r.FormValue("quick"))
	text := strings.TrimSpace(r.FormValue("text"))
	if quick != "" && !contains(contest.QuickAnswers, quick) {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Unknown quick answer.")
		return
	}
	if quick == "" && text == "" {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Write an answer or choose a quick answer.")
		return
	}
	if utf8.RuneCountInString(text) > 10000 {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "The answer is too long.")
		return
	}
	public := r.FormValue("public") != ""
	if _, err := s.q.ReplyQuestion(r.Context(), sqlc.ReplyQuestionParams{ID: q.ID, ReplySubject: &quick, ReplyText: &text,
		ReplyAdminID: &rc.admin.ID, Public: public}); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("question", q.ID)
	rc.note("public", public)
	e := events.Event{Type: events.TypeQuestion, ContestID: q.ContestID, TaskID: derefID(q.TaskID), Text: q.Subject}
	if !public {
		e.ParticipationID = q.ParticipationID
	}
	s.publish(r.Context(), e)
	s.questionDone(w, r, rc, q.ID, "Answer sent.")
}

func (s *Server) handleQuestionIgnore(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	q, ok := s.loadQuestion(w, r, rc)
	if !ok {
		return
	}
	ignore := r.FormValue("ignore") != "0"
	if err := s.q.SetQuestionIgnored(r.Context(), sqlc.SetQuestionIgnoredParams{ID: q.ID, Ignored: ignore}); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("question", q.ID)
	rc.note("ignored", ignore)
	s.questionDone(w, r, rc, q.ID, "")
}

func derefID(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

// commPage is a contest's announcements and private messages.
type commPage struct {
	Contest       sqlc.Contest
	Announcements []sqlc.Announcement
	Messages      []sqlc.ListMessagesByContestRow
	Teams         []sqlc.Team
}

func (s *Server) handleContestCommunication(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	d := &commPage{Contest: c}
	var err error
	if d.Announcements, err = s.q.ListAnnouncements(r.Context(), c.ID); err == nil {
		if d.Messages, err = s.q.ListMessagesByContest(r.Context(), c.ID); err == nil {
			d.Teams, err = s.q.ListTeams(r.Context())
		}
	}
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	p := s.newPage(w, r, rc, "Communication", "contests", d).crumb("Contests", "/contests").crumb(c.Name, "/contests/"+strconv.FormatInt(c.ID, 10))
	s.render(w, "communication", http.StatusOK, p)
}

func (s *Server) commBack(c sqlc.Contest) string {
	return "/contests/" + strconv.FormatInt(c.ID, 10) + "/communication"
}

func (s *Server) handleAnnouncementCreate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	f := newForm(r)
	subject := f.required("subject", "Subject")
	text := f.str("text")
	if f.err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, f.err.Error())
		return
	}
	a, err := s.q.CreateAnnouncement(r.Context(), sqlc.CreateAnnouncementParams{ContestID: c.ID, Subject: subject, Text: text, AdminID: &rc.admin.ID})
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("announcement", a.ID)
	s.publish(r.Context(), events.Event{Type: events.TypeAnnouncement, ContestID: c.ID, Text: subject})
	s.done(w, r, s.commBack(c), "Announcement published.")
}

func (s *Server) handleAnnouncementDelete(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, _ := pathID(r, "id")
	a, err := s.q.GetAnnouncement(r.Context(), id)
	if err != nil {
		s.notFound(w, r, rc)
		return
	}
	if err := s.q.DeleteAnnouncement(r.Context(), a.ID); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("announcement", a.ID)
	s.done(w, r, "/contests/"+strconv.FormatInt(a.ContestID, 10)+"/communication", "Announcement deleted.")
}

// handleMessageCreate sends a private message to a participant (by
// username) or to every member of a team (by team code).
func (s *Server) handleMessageCreate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	f := newForm(r)
	to := f.required("to", "Recipient")
	subject := f.required("subject", "Subject")
	text := f.str("text")
	var parts []int64
	if f.err == nil {
		if u, err := s.q.GetUserByUsername(r.Context(), to); err == nil {
			if p, err := s.q.GetParticipationByContestUser(r.Context(), sqlc.GetParticipationByContestUserParams{ContestID: c.ID, UserID: u.ID}); err == nil {
				parts = []int64{p.ID}
			}
		} else if t, err := s.q.GetTeamByCode(r.Context(), to); err == nil {
			parts, _ = s.q.ListTeamParticipations(r.Context(), sqlc.ListTeamParticipationsParams{ContestID: c.ID, TeamID: &t.ID})
		}
		if len(parts) == 0 {
			f.fail("%q is neither a participant nor a team with members in this contest", to)
		}
	}
	if f.err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, f.err.Error())
		return
	}
	for _, pid := range parts {
		if _, err := s.q.CreateMessage(r.Context(), sqlc.CreateMessageParams{ParticipationID: pid, Subject: subject, Text: text, AdminID: &rc.admin.ID}); err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		s.publish(r.Context(), events.Event{Type: events.TypeMessage, ContestID: c.ID, ParticipationID: pid, Text: subject})
	}
	rc.target("contest", c.ID)
	rc.note("to", to)
	rc.note("recipients", len(parts))
	s.done(w, r, s.commBack(c), "Message sent to %d participants.", len(parts))
}
