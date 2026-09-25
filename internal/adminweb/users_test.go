package adminweb

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/auth"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/webtest"
)

var secretRe = regexp.MustCompile(`Key: <code>([A-Z2-7 ]+)</code>`)
var pendingRe = regexp.MustCompile(`name="pending" value="([^"]+)"`)
var tokenRe = regexp.MustCompile(`name="token" value="([^"]+)"`)

func TestAdminTOTP(t *testing.T) {
	f := newFixture(t)
	b := f.login("all")
	code, body := b.Post("/account/2fa/start", nil)
	webtest.MustOK(t, "2fa start", code, body)
	m, p := secretRe.FindStringSubmatch(body), pendingRe.FindStringSubmatch(body)
	if m == nil || p == nil || !strings.Contains(body, "<svg") {
		t.Fatalf("enrolment page:\n%s", body)
	}
	secret := strings.ReplaceAll(m[1], " ", "")
	if code, _ := b.Post("/account/2fa/enable", url.Values{"pending": {p[1]}, "totp_code": {"000000"}}); code != http.StatusUnprocessableEntity {
		t.Fatalf("wrong code accepted: %d", code)
	}
	good, _ := auth.TOTPCode(secret, time.Now())
	code, body = b.Post("/account/2fa/enable", url.Values{"pending": {p[1]}, "totp_code": {good}})
	webtest.MustOK(t, "2fa enable", code, body)
	a, _ := f.q.GetAdmin(bg, f.admins["all"].ID)
	if a.TotpSecret == nil || *a.TotpSecret != secret {
		t.Fatal("secret not stored")
	}
	// Login now needs the code.
	c := webtest.New(t, f.url)
	c.Get("/login")
	code, body = c.Post("/login", url.Values{"username": {"admin_all"}, "password": {"password1"}})
	tok := tokenRe.FindStringSubmatch(body)
	if code != 200 || tok == nil || strings.Contains(body, "Log out") {
		t.Fatalf("password alone logged in: %d", code)
	}
	if code, _ := c.Post("/login/2fa", url.Values{"token": {tok[1]}, "totp_code": {"123456"}}); code != http.StatusUnauthorized {
		t.Fatalf("wrong 2fa code = %d", code)
	}
	good, _ = auth.TOTPCode(secret, time.Now())
	code, body = c.Post("/login/2fa", url.Values{"token": {tok[1]}, "totp_code": {good}})
	if code != 200 || !strings.Contains(body, "Log out") {
		t.Fatalf("2fa login failed: %d", code)
	}
	// A forged token is rejected.
	if code, _ := c.Post("/login/2fa", url.Values{"token": {"x.y"}, "totp_code": {good}}); code != http.StatusUnauthorized {
		t.Fatalf("forged token = %d", code)
	}
	// Another full administrator can reset a lost device.
	f.q.CreateAdmin(bg, sqlc.CreateAdminParams{Name: "B", Username: "second", PasswordHash: a.PasswordHash, Enabled: true, Role: "all"})
	sb := webtest.New(t, f.url)
	sb.Get("/login")
	sb.Post("/login", url.Values{"username": {"second"}, "password": {"password1"}})
	code, _ = sb.Post(fmt.Sprintf("/admins/%d", a.ID), url.Values{"username": {"admin_all"}, "role": {"all"}, "enabled": {"on"}, "reset_2fa": {"on"}})
	if a, _ = f.q.GetAdmin(bg, a.ID); code != 200 || a.TotpSecret != nil {
		t.Fatalf("2fa reset: %d", code)
	}
}

func TestTeamsSitesAndMembers(t *testing.T) {
	f := newFixture(t)
	b := f.login("all")
	code, body := b.PostMultipart("/teams", map[string]string{"code": "JAL", "name": "Jalisco", "institution": "Delegación Jalisco"})
	webtest.MustOK(t, "team", code, body)
	team, _ := f.q.GetTeamByCode(bg, "JAL")
	if team.Institution != "Delegación Jalisco" {
		t.Fatalf("team %+v", team)
	}
	u2, _ := f.q.CreateUser(bg, sqlc.CreateUserParams{Username: "memb", PasswordHash: "x", PreferredLanguages: []string{}})
	code, body = b.Post(fmt.Sprintf("/teams/%d/members", team.ID), url.Values{"contest_id": {fmt.Sprint(f.contest.ID)}, "usernames": {"ana memb"}})
	webtest.MustOK(t, "members", code, body)
	if !strings.Contains(body, "memb") || !strings.Contains(body, "ana") {
		t.Fatalf("team page lacks members:\n%s", body)
	}
	p2, _ := f.q.GetParticipationByContestUser(bg, sqlc.GetParticipationByContestUserParams{ContestID: f.contest.ID, UserID: u2.ID})
	if p2.TeamID == nil || *p2.TeamID != team.ID {
		t.Fatal("membership not stored")
	}
	// Maximum team size.
	f.pool.Exec(bg, "UPDATE contests SET max_team_size = 2 WHERE id = $1", f.contest.ID)
	f.q.CreateUser(bg, sqlc.CreateUserParams{Username: "extra", PasswordHash: "x", PreferredLanguages: []string{}})
	code, body = b.Post(fmt.Sprintf("/teams/%d/members", team.ID), url.Values{"contest_id": {fmt.Sprint(f.contest.ID)}, "usernames": {"extra"}})
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "allows 2") {
		t.Fatalf("team size not enforced: %d", code)
	}

	// Sites: create, assign, filter the ranking.
	code, body = b.Post(fmt.Sprintf("/contests/%d/sites", f.contest.ID), url.Values{"name": {"Sur"}, "start_time": {"2030-01-01T10:00"}})
	webtest.MustOK(t, "site", code, body)
	sites, _ := f.q.ListSites(bg, f.contest.ID)
	if len(sites) != 1 || sites[0].StartTime == nil {
		t.Fatalf("sites %+v", sites)
	}
	code, body = b.Post(fmt.Sprintf("/participations/%d", p2.ID), url.Values{"team_id": {fmt.Sprint(team.ID)}, "site_id": {fmt.Sprint(sites[0].ID)},
		"delay_time_s": {"0"}, "extra_time_s": {"0"}})
	webtest.MustOK(t, "assign site", code, body)
	code, body = b.Get(fmt.Sprintf("/contests/%d/ranking?site=%d", f.contest.ID, sites[0].ID))
	if code != 200 || !strings.Contains(body, "memb") || strings.Contains(body, ">ana<") {
		t.Fatalf("site ranking:\n%s", body)
	}
	code, body = b.Get(fmt.Sprintf("/contests/%d/sites", f.contest.ID))
	if code != 200 || !strings.Contains(body, "1 participants") {
		t.Fatalf("sites page:\n%s", body)
	}
	code, _ = b.Post(fmt.Sprintf("/sites/%d/delete", sites[0].ID), nil)
	if p, _ := f.q.GetParticipation(bg, p2.ID); code != 200 || p.SiteID != nil {
		t.Fatalf("site delete: %d", code)
	}
}

func TestUserAccountActions(t *testing.T) {
	f := newFixture(t)
	b := f.login("all")
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89")
	code, body := b.PostMultipart("/users", map[string]string{"username": "nuevo", "first_name": "Nuevo", "institution": "UNAM",
		"country": "MX", "region": "CDMX", "generate_password": "on", "contest_id": fmt.Sprint(f.contest.ID)},
		webtest.File{Field: "photo", Name: "me.png", Data: png})
	webtest.MustOK(t, "create user", code, body)
	m := regexp.MustCompile(`<td>nuevo</td><td>Nuevo</td><td></td><td><code>([a-z0-9]{10})</code>`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("generated password not shown:\n%s", body)
	}
	u, _ := f.q.GetUserByUsername(bg, "nuevo")
	if auth.VerifyPassword(u.PasswordHash, m[1]) != nil || u.Institution != "UNAM" || u.Region != "CDMX" || u.PhotoDigest == nil {
		t.Fatalf("user %+v", u)
	}
	if code, body := b.Get(fmt.Sprintf("/users/%d/photo", u.ID)); code != 200 || !strings.HasPrefix(body, "\x89PNG") {
		t.Fatalf("photo = %d", code)
	}
	// A photo that is not an image is refused.
	code, _ = b.PostMultipart(fmt.Sprintf("/users/%d", u.ID), map[string]string{"username": "nuevo"},
		webtest.File{Field: "photo", Name: "x.png", Data: []byte("<script>")})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("non-image photo = %d", code)
	}
	code, body = b.Post(fmt.Sprintf("/users/%d/reset-password", u.ID), nil)
	m2 := regexp.MustCompile(`<code>([a-z0-9]{10})</code>`).FindStringSubmatch(body)
	u2, _ := f.q.GetUserByUsername(bg, "nuevo")
	if code != 200 || m2 == nil || m2[1] == m[1] || auth.VerifyPassword(u2.PasswordHash, m2[1]) != nil {
		t.Fatal("password reset")
	}
	code, _ = b.Post(fmt.Sprintf("/users/%d/disable", u.ID), nil)
	if u2, _ = f.q.GetUserByUsername(bg, "nuevo"); code != 200 || !u2.Disabled {
		t.Fatal("disable")
	}
	b.Post(fmt.Sprintf("/users/%d/enable", u.ID), nil)
	p, _ := f.q.GetParticipationByContestUser(bg, sqlc.GetParticipationByContestUserParams{ContestID: f.contest.ID, UserID: u.ID})
	code, _ = b.Post(fmt.Sprintf("/users/%d/logout", u.ID), nil)
	if p2, _ := f.q.GetParticipation(bg, p.ID); code != 200 || p2.LoginNonce != p.LoginNonce+1 {
		t.Fatal("force logout")
	}
	// Bulk reset needs the typed confirmation.
	if code, _ := b.Post(fmt.Sprintf("/contests/%d/reset-passwords", f.contest.ID), url.Values{"confirm": {"yes"}}); code != http.StatusUnprocessableEntity {
		t.Fatalf("unconfirmed bulk reset = %d", code)
	}
	code, body = b.Post(fmt.Sprintf("/contests/%d/reset-passwords", f.contest.ID), url.Values{"confirm": {"RESET"}})
	if code != 200 || strings.Count(body, "<code>") != 2 {
		t.Fatalf("bulk reset:\n%s", body)
	}
	for _, page := range []string{"/account", fmt.Sprintf("/users/%d", u.ID), fmt.Sprintf("/contests/%d/sites", f.contest.ID),
		fmt.Sprintf("/teams/%d", f.team.ID), "/users/export.csv"} {
		code, body := b.Get(page)
		webtest.MustOK(t, page, code, body)
		if m := inlineRe.FindString(body); m != "" {
			t.Errorf("%s contains inline code: %q", page, m)
		}
	}
	// Every account action was audited.
	rows, _ := f.q.ListAuditLog(bg, sqlc.ListAuditLogParams{Limit: 100})
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.Action] = true
		if strings.Contains(string(r.Details), m[1]) {
			t.Fatalf("a password reached the audit log: %s", r.Details)
		}
	}
	for _, a := range []string{"user.create", "user.reset_password", "user.disable", "user.enable", "user.logout", "contest.reset_passwords"} {
		if !seen[a] {
			t.Errorf("audit log misses %s", a)
		}
	}
}
