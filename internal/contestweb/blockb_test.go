package contestweb

import (
	"io"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/events"
)

// setContest changes contest columns and tells the server (as the admin
// web server does).
func (f *fixture) setContest(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(bg, "UPDATE contests SET "+sql+" WHERE id = $1", append([]any{f.contest.ID}, args...)...); err != nil {
		t.Fatal(err)
	}
	events.Publish(bg, f.rdb, f.ns, events.Event{Type: events.TypeContest, ContestID: f.contest.ID})
	time.Sleep(100 * time.Millisecond)
}

// TestContestStatus (SPEC_CLOSE B1): drafts do not exist for contestants,
// archived contests are read-only and unlisted; the interface languages and
// the timezone of the contest apply.
func TestContestStatus(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	c := f.client()
	if _, body := f.get(c, "/"); !strings.Contains(body, "/ioi/") {
		t.Fatalf("published contest not listed:\n%s", body)
	}
	f.setContest(t, "status = 'draft'")
	if code, _ := f.get(c, "/ioi/login"); code != 404 {
		t.Fatalf("draft login page = %d", code)
	}
	if _, body := f.get(c, "/"); strings.Contains(body, "/ioi/") {
		t.Fatal("draft contest listed")
	}
	f.setContest(t, "status = 'archived'")
	if _, body := f.get(c, "/"); strings.Contains(body, "/ioi/") {
		t.Fatal("archived contest listed")
	}
	_, body := f.login(c, "ana", "secret")
	if !strings.Contains(body, "archived") {
		t.Fatalf("no archived notice:\n%s", body)
	}
	if code, body := f.submit(c, csrfOf(t, body), "c11", "int main(){}", false); code != 403 || !strings.Contains(body, "closed") {
		t.Fatalf("submission to an archived contest = %d", code)
	}
	resp, _ := c.PostForm(f.url+"/ioi/questions", url.Values{"csrf": {csrfOf(t, body)}, "text": {"hola"}})
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode == 200 && strings.Contains(string(b), "hola") {
		t.Fatal("question accepted in an archived contest")
	}
	// Only Spanish allowed: the interface is Spanish whatever the browser asks.
	f.setContest(t, "status = 'published', allowed_localizations = '{es}', timezone = 'America/Mexico_City'")
	code, page := f.get(c, "/ioi/", "Accept-Language", "en")
	if code != 200 || !strings.Contains(page, `lang="es"`) {
		t.Fatalf("interface language = %d:\n%s", code, page)
	}
	if !strings.Contains(page, "America/Mexico_City") && !strings.Contains(page, "CST") && !strings.Contains(page, "-06") {
		t.Errorf("contest timezone not shown:\n%s", page)
	}
}
