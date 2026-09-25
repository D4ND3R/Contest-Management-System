package e2e

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
)

// post sends a form from the contestant's browser.
func (b *browser) post(path string, v url.Values) (int, string) {
	v.Set("csrf", b.csrf)
	resp, err := b.c.PostForm(b.s.cwsURL+path, v)
	if err != nil {
		b.s.t.Fatal(err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	bufio.NewReader(resp.Body).WriteTo(&sb)
	return resp.StatusCode, sb.String()
}

// events opens the contestant's SSE stream and returns its event names.
func (b *browser) events(t *testing.T) (<-chan string, context.CancelFunc) {
	ctx, cancel := context.WithCancel(bg)
	req, _ := http.NewRequestWithContext(ctx, "GET", b.s.cwsURL+"/e2e/events", nil)
	c := *b.c
	c.Timeout = 0
	resp, err := c.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("events: %v", err)
	}
	ch := make(chan string, 16)
	go func() {
		defer resp.Body.Close()
		br := bufio.NewReader(resp.Body)
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				close(ch)
				return
			}
			if strings.HasPrefix(line, "event: ") {
				ch <- strings.TrimSpace(strings.TrimPrefix(line, "event: "))
			}
		}
	}()
	time.Sleep(100 * time.Millisecond) // subscribed
	return ch, cancel
}

func expectEvent(t *testing.T, ch <-chan string, want string) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e := <-ch:
			if e == want {
				return
			}
		case <-timeout:
			t.Fatalf("no %s event", want)
		}
	}
}

var badgeRe = regexp.MustCompile(`id="unread" class="badge"[^>]*?(hidden)?>(\d+)<`)

func unread(t *testing.T, b *browser) string {
	t.Helper()
	_, body := b.get("/e2e/")
	m := badgeRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no unread badge:\n%s", body)
	}
	if m[1] != "" {
		return "0"
	}
	return m[2]
}

// TestCommunicationFlow covers A1 across both web servers: a contestant
// asks about a task, the staff sees it in the inbox and the menu counter,
// answers publicly with a quick answer (every contestant is notified live
// and sees it in their language), announces, messages a team; unread
// counters and the anti-spam limit.
func TestCommunicationFlow(t *testing.T) {
	s := newStack(t, stackOpts{admin: true})
	team, _ := s.q.CreateTeam(bg, sqlc.CreateTeamParams{Code: "JAL", Name: "Jalisco"})
	ana, beto := s.addContestant("ana"), s.addContestant("beto")
	for _, p := range []sqlc.Participation{ana, beto} {
		s.pool.Exec(bg, "UPDATE participations SET team_id = $1 WHERE id = $2", team.ID, p.ID)
	}
	s.pool.Exec(bg, "UPDATE contests SET questions_per_minute = 2 WHERE id = $1", s.contest.ID)
	ca, cb := s.login("ana"), s.login("beto")
	evA, stopA := ca.events(t)
	defer stopA()
	evB, stopB := cb.events(t)
	defer stopB()
	a := s.adminBrowser(t)

	code, body := ca.post("/e2e/questions", url.Values{"task": {"sum"}, "text": {"¿Los números pueden ser negativos?"}})
	if code != 200 || !strings.Contains(body, "Your question was sent.") || !strings.Contains(body, "Waiting for an answer.") {
		t.Fatalf("ask = %d\n%s", code, body)
	}
	// The staff inbox and the menu counter.
	_, body = a.Get("/questions")
	if !strings.Contains(body, "¿Los números pueden ser negativos?") || !regexp.MustCompile(`id="q-count" class="badge"[^>]*>1<`).MatchString(body) {
		t.Fatalf("inbox:\n%s", body)
	}
	qs, _ := s.q.ListQuestionsByParticipation(bg, ana.ID)
	if len(qs) != 1 || qs[0].TaskID == nil || *qs[0].TaskID != s.task.ID {
		t.Fatalf("questions %+v", qs)
	}
	// Public quick answer: both contestants are notified.
	code, _ = a.Post(fmt.Sprintf("/questions/%d/reply", qs[0].ID), url.Values{"quick": {"Yes"}, "text": {"Hasta 10^9 en valor absoluto."}, "public": {"on"}})
	if code != 200 {
		t.Fatalf("reply = %d", code)
	}
	expectEvent(t, evA, "question")
	expectEvent(t, evB, "question")
	if n := unread(t, cb); n != "1" {
		t.Fatalf("beto unread = %s", n)
	}
	cb.c.Jar.SetCookies(mustURL(s.cwsURL), []*http.Cookie{{Name: "cms_lang", Value: "es", Path: "/"}})
	_, body = cb.get("/e2e/communication")
	if !strings.Contains(body, "Respuestas para todos") || !strings.Contains(body, "<b>Sí</b>") || !strings.Contains(body, "Hasta 10^9") {
		t.Fatalf("public answer:\n%s", body)
	}
	if n := unread(t, cb); n != "0" {
		t.Fatalf("beto unread after reading = %s", n)
	}
	// Announcement and a message to the team.
	code, _ = a.Post(fmt.Sprintf("/contests/%d/announcements", s.contest.ID), url.Values{"subject": {"Nuevo caso"}, "text": {"Se agregó un caso."}})
	if code != 200 {
		t.Fatalf("announcement = %d", code)
	}
	expectEvent(t, evB, "announcement")
	code, body = a.Post(fmt.Sprintf("/contests/%d/messages", s.contest.ID), url.Values{"to": {"JAL"}, "subject": {"Hola"}, "text": {"Equipo"}})
	if code != 200 || !strings.Contains(body, "Message sent to 2 participants.") {
		t.Fatalf("team message = %d\n%s", code, body)
	}
	expectEvent(t, evA, "message")
	expectEvent(t, evB, "message")
	if n := unread(t, cb); n != "2" {
		t.Fatalf("beto unread = %s", n)
	}
	if code, _ := a.Post(fmt.Sprintf("/contests/%d/messages", s.contest.ID), url.Values{"to": {"nadie"}, "subject": {"x"}}); code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown recipient = %d", code)
	}
	// Anti-spam: at most 2 questions a minute (one already asked).
	ca.post("/e2e/questions", url.Values{"text": {"otra"}})
	if code, _ := ca.post("/e2e/questions", url.Values{"text": {"y otra"}}); code != http.StatusTooManyRequests {
		t.Fatalf("third question = %d", code)
	}
	// Audit log.
	rows, _ := s.q.ListAuditLog(bg, sqlc.ListAuditLogParams{Limit: 50})
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.Action] = true
	}
	for _, act := range []string{"question.reply", "announcement.create", "message.create"} {
		if !seen[act] {
			t.Errorf("audit log misses %s", act)
		}
	}
}

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}
