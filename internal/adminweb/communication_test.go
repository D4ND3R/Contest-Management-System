package adminweb

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/events"
	"github.com/D4ND3R/Contest-Management-System/internal/webtest"
)

// TestQuestionInbox covers the staff side of A1: ordering and filters,
// quick and free answers (private or public) with their events, ignoring,
// htmx cards, roles and the live-event filter.
func TestQuestionInbox(t *testing.T) {
	f := newFixture(t)
	ask := func(task *int64, text string, at time.Time) sqlc.Question {
		q, err := f.q.CreateQuestion(bg, sqlc.CreateQuestionParams{ParticipationID: f.part.ID, TaskID: task, AskedAt: at, Subject: "s", Text: text})
		if err != nil {
			t.Fatal(err)
		}
		return q
	}
	now := time.Now()
	general := ask(nil, "pregunta general", now.Add(-time.Minute))
	older := ask(&f.task.ID, "pregunta vieja", now.Add(-time.Hour))
	sub := f.rdb.Subscribe(bg, events.Channel(f.ns))
	defer sub.Close()
	sub.Receive(bg)

	b := f.login("messaging")
	code, body := b.Get("/questions")
	webtest.MustOK(t, "inbox", code, body)
	if i, j := strings.Index(body, "pregunta vieja"), strings.Index(body, "pregunta general"); i < 0 || j < 0 || i > j {
		t.Fatal("pending questions are not oldest first")
	}
	if !strings.Contains(body, `id="q-count" class="badge" data-src="/questions/count" title="unanswered questions" >2<`) {
		t.Fatalf("menu counter:\n%s", body)
	}
	if _, body = b.Get(fmt.Sprintf("/questions?task=%d", f.task.ID)); strings.Contains(body, "pregunta general") {
		t.Fatal("task filter")
	}
	// Validation.
	if code, _ := b.Post(fmt.Sprintf("/questions/%d/reply", general.ID), url.Values{}); code != http.StatusUnprocessableEntity {
		t.Fatalf("empty answer = %d", code)
	}
	if code, _ := b.Post(fmt.Sprintf("/questions/%d/reply", general.ID), url.Values{"quick": {"Maybe"}}); code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown quick answer = %d", code)
	}
	// A private free answer through htmx: the card comes back.
	code, body = b.PostHTMX(fmt.Sprintf("/questions/%d/reply", general.ID), url.Values{"text": {"Sí, en el enunciado."}})
	if code != 200 || strings.Contains(body, "<html") || !strings.Contains(body, `id="question-`) || !strings.Contains(body, "Sí, en el enunciado.") {
		t.Fatalf("htmx reply = %d\n%s", code, body)
	}
	m, err := sub.ReceiveMessage(bg)
	if err != nil || !strings.Contains(m.Payload, `"type":"question"`) || !strings.Contains(m.Payload, fmt.Sprintf(`"participation_id":%d`, f.part.ID)) {
		t.Fatalf("private answer event %v %v", m, err)
	}
	// A public quick answer goes to the whole contest.
	b.Post(fmt.Sprintf("/questions/%d/reply", older.ID), url.Values{"quick": {"No comment"}, "public": {"on"}})
	m, _ = sub.ReceiveMessage(bg)
	if !strings.Contains(m.Payload, fmt.Sprintf(`"contest_id":%d`, f.contest.ID)) || strings.Contains(m.Payload, "participation_id") {
		t.Fatalf("public answer event %s", m.Payload)
	}
	q, _ := f.q.GetQuestion(bg, older.ID)
	if !q.Public || *q.ReplySubject != "No comment" || q.ReplyAdminID == nil {
		t.Fatalf("stored answer %+v", q)
	}
	// Ignore and back to pending.
	third := ask(nil, "spam", now)
	b.Post(fmt.Sprintf("/questions/%d/ignore", third.ID), url.Values{"ignore": {"1"}})
	if q, _ := f.q.GetQuestion(bg, third.ID); !q.Ignored {
		t.Fatal("not ignored")
	}
	if n, _ := f.q.CountPendingQuestions(bg); n != 0 {
		t.Fatalf("pending = %d", n)
	}
	b.Post(fmt.Sprintf("/questions/%d/ignore", third.ID), url.Values{"ignore": {"0"}})
	if n, _ := f.q.CountPendingQuestions(bg); n != 1 {
		t.Fatalf("pending after unignore = %d", n)
	}
	// Read-only administrators see the inbox but cannot answer or announce.
	ro := f.login("read_only")
	if code, body := ro.Get("/questions"); code != 200 || strings.Contains(body, `action="/questions/`) {
		t.Fatalf("read-only inbox = %d", code)
	}
	if code, _ := ro.Post(fmt.Sprintf("/questions/%d/reply", third.ID), url.Values{"quick": {"Yes"}}); code != http.StatusForbidden {
		t.Fatalf("read-only answer = %d", code)
	}
	if code, _ := ro.Post(fmt.Sprintf("/contests/%d/announcements", f.contest.ID), url.Values{"subject": {"x"}}); code != http.StatusForbidden {
		t.Fatalf("read-only announcement = %d", code)
	}
	// Announcements: create, list, delete.
	code, body = b.Post(fmt.Sprintf("/contests/%d/announcements", f.contest.ID), url.Values{"subject": {"Aviso"}, "text": {"texto"}})
	if code != 200 || !strings.Contains(body, "Aviso") {
		t.Fatalf("announcement = %d", code)
	}
	as, _ := f.q.ListAnnouncements(bg, f.contest.ID)
	b.Post(fmt.Sprintf("/announcements/%d/delete", as[0].ID), nil)
	if as, _ = f.q.ListAnnouncements(bg, f.contest.ID); len(as) != 0 {
		t.Fatal("announcement not deleted")
	}
	// Private message by username.
	code, body = b.Post(fmt.Sprintf("/contests/%d/messages", f.contest.ID), url.Values{"to": {"ana"}, "subject": {"Hola"}})
	if ms, _ := f.q.ListMessagesByParticipation(bg, f.part.ID); code != 200 || len(ms) != 1 || !strings.Contains(body, "Message sent to 1 participants.") {
		t.Fatalf("message = %d", code)
	}
}

func TestAdminHubForwardsOnlyStaffEvents(t *testing.T) {
	h := &adminHub{clients: map[chan []byte]struct{}{}}
	ch := make(chan []byte, 8)
	h.clients[ch] = struct{}{}
	for _, typ := range []string{events.TypeSubmission, events.TypeAnnouncement, events.TypeContest, events.TypeAlert, events.TypeQuestionNew} {
		h.publish(events.Event{Type: typ})
	}
	if len(ch) != 2 {
		t.Fatalf("%d events forwarded, want alert and question_new", len(ch))
	}
}
