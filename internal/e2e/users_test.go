package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/auth"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/webtest"
)

func (s *stack) adminBrowser(t *testing.T) *webtest.Browser {
	t.Helper()
	hash, _ := auth.HashPassword("adminpass")
	s.q.CreateAdmin(bg, sqlc.CreateAdminParams{Name: "Admin", Username: "admin", PasswordHash: hash, Enabled: true, Role: "all"})
	a := webtest.New(t, s.awsURL)
	a.Get("/login")
	code, body := a.Post("/login", url.Values{"username": {"admin"}, "password": {"adminpass"}})
	webtest.MustOK(t, "admin login", code, body)
	return a
}

// TestAdminControlsContestantSessions (SPEC_CLOSE C5, C6): the admin sees
// a contestant's active sessions with their IP, closes them, disables and
// re-enables the account, and opens a read-only, audited view of the
// contest as that contestant.
func TestAdminControlsContestantSessions(t *testing.T) {
	s := newStack(t, stackOpts{admin: true})
	p := s.addContestant("ana")
	c := s.login("ana")
	c.get("/e2e/")
	a := s.adminBrowser(t)

	// The session shows up (written asynchronously, at most once a minute).
	var body string
	for i := 0; i < 50; i++ {
		_, body = a.Get(fmt.Sprintf("/participations/%d", p.ID))
		if strings.Contains(body, "127.0.0.1") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(body, "127.0.0.1") || !strings.Contains(body, ">active<") {
		t.Fatalf("session not listed:\n%s", body)
	}

	// Force logout: the contestant's next request is rejected.
	code, _ := a.Post(fmt.Sprintf("/users/%d/logout", p.UserID), nil)
	if code != 200 {
		t.Fatalf("logout = %d", code)
	}
	time.Sleep(100 * time.Millisecond)
	if code, body := c.get("/e2e/tasks/sum"); code != http.StatusUnauthorized || !strings.Contains(body, "closed by the organizers") {
		t.Fatalf("closed session still works: %d", code)
	}
	// Disabled accounts cannot log in; enabled again, they can.
	a.Post(fmt.Sprintf("/users/%d/disable", p.UserID), nil)
	time.Sleep(100 * time.Millisecond)
	c2 := webtestBrowser(t, s)
	c2.Get("/e2e/login")
	if code, body := c2.Post("/e2e/login", url.Values{"username": {"ana"}, "password": {"pw"}}); code != http.StatusForbidden || !strings.Contains(body, "disabled") {
		t.Fatalf("disabled login = %d", code)
	}
	a.Post(fmt.Sprintf("/users/%d/enable", p.UserID), nil)
	time.Sleep(100 * time.Millisecond)
	if code, _ := c2.Post("/e2e/login", url.Values{"username": {"ana"}, "password": {"pw"}}); code != 200 {
		t.Fatalf("re-enabled login = %d", code)
	}

	// View as contestant: a signed link opens a read-only session.
	code, body = a.Post(fmt.Sprintf("/participations/%d/view-as", p.ID), nil)
	if code != 200 || !strings.HasPrefix(a.Last, s.cwsURL+"/e2e/") || !strings.Contains(body, "Administrator view as ana") {
		t.Fatalf("view as: %d %s\n%s", code, a.Last, body)
	}
	// The admin browser now holds a read-only CWS session: submitting fails.
	cws := webtest.New(t, s.cwsURL)
	cws.C.Jar = a.C.Jar
	cws.Get("/e2e/tasks/sum")
	code, body = cws.PostMultipart("/e2e/tasks/sum/submit", map[string]string{"language": "c11"},
		webtest.File{Field: "sum.%l", Name: "sum.c", Data: []byte("int main(){}")})
	if code != http.StatusForbidden || !strings.Contains(body, "read-only") {
		t.Fatalf("read-only view could submit: %d", code)
	}
	subs, _ := s.q.ListSubmissionsByParticipationTask(bg, sqlc.ListSubmissionsByParticipationTaskParams{ParticipationID: p.ID, TaskID: s.task.ID})
	if len(subs) != 0 {
		t.Fatal("a submission was stored")
	}
	// Links expire and cannot be forged.
	if code, _ := cws.Get("/e2e/impersonate?t=forged.token"); code != http.StatusForbidden {
		t.Fatalf("forged link = %d", code)
	}
	rows, _ := s.q.ListAuditLog(bg, sqlc.ListAuditLogParams{Limit: 50})
	found := false
	for _, r := range rows {
		found = found || r.Action == "participation.view_as"
	}
	if !found {
		t.Fatal("view-as not audited")
	}
}

func webtestBrowser(t *testing.T, s *stack) *webtest.Browser { return webtest.New(t, s.cwsURL) }
