package contestweb

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/webkit"
)

// register fills the registration form.
func (f *fixture) register(c *http.Client, fields url.Values) (int, string) {
	f.t.Helper()
	code, page := f.get(c, "/ioi/register")
	if code != 200 {
		return code, page
	}
	fields.Set("csrf", csrfOf(f.t, page))
	resp, err := c.PostForm(f.url+"/ioi/register", fields)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func regForm(user, pw string) url.Values {
	return url.Values{"username": {user}, "first_name": {"Eva"}, "last_name": {"Pérez"}, "email": {"eva@example.org"},
		"password": {pw}, "password2": {pw}}
}

// TestRegistrationWithApproval (SPEC_CLOSE B6): self-registered accounts
// wait for an administrator; the password policy applies.
func TestRegistrationWithApproval(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	c := f.client()
	// Admin-only contests have no registration page nor link.
	if code, _ := f.get(c, "/ioi/register"); code != 404 {
		t.Fatalf("registration in an admin-only contest = %d", code)
	}
	if _, page := f.get(c, "/ioi/login"); strings.Contains(page, "/ioi/register") {
		t.Fatal("registration link shown in an admin-only contest")
	}
	f.setContest(t, "registration = 'approval', password_min_length = 10")
	if _, page := f.get(c, "/ioi/login"); !strings.Contains(page, `href="/ioi/register"`) {
		t.Fatalf("no registration link:\n%s", page)
	}
	for _, tc := range []struct {
		form url.Values
		want string
	}{
		{regForm("e", "abcdef12345"), "3 to 32"},
		{regForm("eva", "abc123"), "at least 10 characters"},
		{regForm("eva", "abcdefghijk"), "letters and digits"},
		{regForm("eva12345678", "eva12345678"), "cannot be the username"},
		{func() url.Values { v := regForm("eva", "abcdef12345"); v.Set("password2", "x"); return v }(), "do not match"},
		{regForm("ana", "abcdef12345"), "username is taken"},
	} {
		if code, body := f.register(c, tc.form); code != 422 || !strings.Contains(body, tc.want) {
			t.Errorf("register %v = %d, want %q:\n%s", tc.form, code, tc.want, body)
		}
	}
	code, body := f.register(c, regForm("eva", "abcdef12345"))
	if code != 200 || !strings.Contains(body, "organizers approve") {
		t.Fatalf("register = %d:\n%s", code, body)
	}
	var approved bool
	var pid int64
	if err := f.pool.QueryRow(bg, `SELECT p.id, p.approved FROM participations p JOIN users u ON u.id = p.user_id
WHERE u.username = 'eva' AND p.contest_id = $1`, f.contest.ID).Scan(&pid, &approved); err != nil || approved {
		t.Fatalf("participation approved=%v err=%v", approved, err)
	}
	if code, body := f.login(c, "eva", "abcdef12345"); code != 403 || !strings.Contains(body, "waiting for the organizers") {
		t.Fatalf("login before approval = %d:\n%s", code, body)
	}
	if _, err := f.pool.Exec(bg, "UPDATE participations SET approved = true WHERE id = $1", pid); err != nil {
		t.Fatal(err)
	}
	f.srv.cache.invalidateParticipation(pid)
	if code, body := f.login(c, "eva", "abcdef12345"); code != 200 || !strings.Contains(body, "Eva") {
		t.Fatalf("login after approval = %d:\n%s", code, body)
	}
	// Registration closes with the contest (no practice).
	f.setContest(t, "stop_time = now() - interval '1 minute', start_time = now() - interval '2 hours'")
	if code, _ := f.get(f.client(), "/ioi/register"); code != 404 {
		t.Fatalf("registration after the end = %d", code)
	}
}

// TestRegistrationWithCode (SPEC_CLOSE B6): the invitation code admits the
// contestant at once.
func TestRegistrationWithCode(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.setContest(t, "registration = 'code', invitation_code = 'OMI-2026'")
	c := f.client()
	v := regForm("luis", "clave2026x")
	v.Set("code", "wrong")
	if code, body := f.register(c, v); code != 422 || !strings.Contains(body, "invitation code is not valid") {
		t.Fatalf("wrong code = %d:\n%s", code, body)
	}
	v.Set("code", "OMI-2026")
	if code, body := f.register(c, v); code != 200 || !strings.Contains(body, "luis") {
		t.Fatalf("register with code = %d:\n%s", code, body)
	}
	if code, _ := f.get(c, "/ioi/tasks/sum"); code != 200 {
		t.Fatalf("new contestant cannot see the task: %d", code)
	}
}

// TestSessionDuration (SPEC_CLOSE B6): sessions older than the contest's
// duration are closed.
func TestSessionDuration(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.setContest(t, "session_minutes = 5")
	c := f.client()
	if code, _ := f.login(c, "ana", "secret"); code != 200 {
		t.Fatalf("login = %d", code)
	}
	u, _ := url.Parse(f.url + "/ioi/")
	var ck *http.Cookie
	for _, k := range c.Jar.Cookies(u) {
		if strings.HasPrefix(k.Name, "cms_c") {
			ck = k
		}
	}
	if ck == nil {
		t.Fatal("no session cookie")
	}
	payload, ok := f.srv.signer.Verify(ck.Value)
	var sess webkit.Session
	if !ok || json.Unmarshal(payload, &sess) != nil {
		t.Fatal("cannot read the session")
	}
	if left := time.Until(time.Unix(sess.Expires, 0)); left > 5*time.Minute+time.Second || left < 4*time.Minute {
		t.Fatalf("session expires in %v, want 5 minutes", left)
	}
	if code, _ := f.get(c, "/ioi/tasks/sum"); code != 200 {
		t.Fatalf("fresh session = %d", code)
	}
	// Same session, issued 6 minutes ago.
	sess.Issued = time.Now().Add(-6 * time.Minute).Unix()
	b, _ := json.Marshal(sess)
	c.Jar.SetCookies(u, []*http.Cookie{{Name: ck.Name, Value: f.srv.signer.Sign(b), Path: "/"}})
	resp, err := c.Get(f.url + "/ioi/tasks/sum")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Request.URL.Path != "/ioi/login" {
		t.Fatalf("old session reached %s", resp.Request.URL.Path)
	}
}
