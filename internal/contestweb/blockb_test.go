package contestweb

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
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

// TestPracticeMode (SPEC_CLOSE B2): after the contest, practice accepts
// submissions and marks them unofficial.
func TestPracticeMode(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.setContest(t, "start_time = now() - interval '3 hours', stop_time = now() - interval '1 hour'")
	c := f.client()
	_, body := f.login(c, "ana", "secret")
	if !strings.Contains(body, "The contest is over.") {
		t.Fatalf("finished page:\n%s", body)
	}
	if code, _ := f.submit(c, csrfOf(t, body), "c11", "int main(){}", false); code != 403 {
		t.Fatalf("submission after the end = %d", code)
	}
	f.setContest(t, "practice_enabled = true")
	_, body = f.get(c, "/ioi/")
	if !strings.Contains(body, "Practice mode") {
		t.Fatalf("practice page:\n%s", body)
	}
	if code, body := f.submit(c, csrfOf(t, body), "c11", "int main(){}", false); code != 200 {
		t.Fatalf("practice submission = %d\n%s", code, body)
	}
	subs, _ := f.q.ListSubmissionsByParticipation(bg, f.part.ID)
	if len(subs) != 1 || subs[0].Official {
		t.Fatalf("practice submission %+v", subs)
	}
}

// TestClockFollowsExtension (SPEC_CLOSE B2): changing the times sends a
// "clock" event to open pages, whose clock endpoint returns the new end.
func TestClockFollowsExtension(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	c := f.client()
	f.login(c, "ana", "secret")
	clock := func() (string, int64) {
		t.Helper()
		_, body := f.get(c, "/ioi/clock")
		var v struct {
			Phase string
			End   int64
		}
		if err := json.Unmarshal([]byte(body), &v); err != nil {
			t.Fatalf("clock %q: %v", body, err)
		}
		return v.Phase, v.End
	}
	phase, end := clock()
	if phase != "running" || end == 0 {
		t.Fatalf("clock %s %d", phase, end)
	}
	c.Timeout = 0
	req, _ := http.NewRequest("GET", f.url+"/ioi/events", nil)
	ctx, cancel := context.WithTimeout(bg, 5*time.Second)
	defer cancel()
	resp, err := c.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	br.ReadString('\n')
	time.Sleep(100 * time.Millisecond)
	f.setContest(t, "stop_time = stop_time + interval '10 minutes'")
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("no clock event: %v", err)
		}
		if strings.HasPrefix(line, "event: clock") {
			break
		}
	}
	if _, end2 := clock(); end2-end != 10*60*1000 {
		t.Fatalf("clock moved by %d ms", end2-end)
	}
}
