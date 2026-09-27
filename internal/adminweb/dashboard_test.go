package adminweb

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/suspicious"
	"github.com/D4ND3R/Contest-Management-System/internal/webtest"
)

// TestContestDashboard (SPEC_IOI H2, SPEC_MIN §1): the contest page is a
// live dashboard of plain tables (problems, verdicts, system health,
// scoreboard, events); the admin home shows the running contest's; the
// settings and the problems have their own pages.
func TestContestDashboard(t *testing.T) {
	f := newFixture(t)
	b := f.login("all")
	id := fmt.Sprint(f.contest.ID)
	c := db.ContestToUpdate(f.contest)
	c.Title, c.Location, c.Tagline = "OMI 2026", "Mérida, México", "Mejores problemas."
	if _, err := f.q.UpdateContest(bg, c); err != nil {
		t.Fatal(err)
	}
	code, body := b.Get("/contests/" + id)
	webtest.MustOK(t, "dashboard", code, body)
	for _, w := range []string{`<h1>OMI 2026</h1>`, "Mérida, México", ">Scoreboard <small>", ">Problems <small>",
		"Recent events", "Verdicts", "System health", "Contest in progress",
		`href="/participations/` + fmt.Sprint(f.part.ID) + `"`, "New submission from ana", `data-countdown="`,
		`hx-get="/contests/` + id + `/live"`, `<a href="/contests/` + id + `" title="OMI 2026" aria-current="page">`} {
		if !strings.Contains(body, w) {
			t.Errorf("dashboard lacks %s", w)
		}
	}
	// The live part refreshes alone.
	code, live := b.Get("/contests/" + id + "/live")
	if code != 200 || !strings.HasPrefix(strings.TrimSpace(live), `<div id="dash-live"`) || strings.Contains(live, "<html") {
		t.Fatalf("live: %d\n%s", code, live)
	}
	// The home page follows the running contest.
	if _, home := b.Get("/"); !strings.Contains(home, "Recent events") || !strings.Contains(home, "Current and upcoming contests") {
		t.Fatalf("home:\n%s", home)
	}
	// Settings and problems.
	if code, body := b.Get("/contests/" + id + "/settings"); code != 200 || !strings.Contains(body, `name="tagline" value="Mejores problemas."`) ||
		strings.Contains(body, "/banner") {
		t.Fatalf("settings: %d", code)
	}
	if code, body := b.Get("/contests/" + id + "/tasks"); code != 200 || !strings.Contains(body, `href="/tasks/`+fmt.Sprint(f.task.ID)+`"`) {
		t.Fatalf("problems: %d", code)
	}
	// Presentation fields are saved with the form, with length limits.
	form := url.Values{"name": {"seeded"}, "start_time": {"2030-05-01T09:00"}, "stop_time": {"2030-05-01T14:00"}, "token_mode": {"disabled"},
		"scoring_mode": {"ioi"}, "title": {"Final"}, "location": {"Online"}, "tagline": {strings.Repeat("x", 201)}}
	if code, _ := b.Post("/contests/"+id, form); code != http.StatusUnprocessableEntity {
		t.Fatalf("long motto = %d", code)
	}
	form.Set("tagline", "Go!")
	if code, body := b.Post("/contests/"+id, form); code != 200 || !strings.Contains(body, "Contest saved.") {
		t.Fatalf("save = %d\n%s", code, body)
	}
	got, _ := f.q.GetContest(bg, f.contest.ID)
	if got.Title != "Final" || got.Location != "Online" || got.Tagline != "Go!" {
		t.Fatalf("presentation not saved: %+v", got)
	}
}

// TestSuspiciousSubmissions (SPEC_IOI H3): flagged submissions show why on
// their page, have a tag and a filter in the list, and a notification on
// the dashboard; the workers' seccomp state is on the system page.
func TestSuspiciousSubmissions(t *testing.T) {
	f := newFixture(t)
	b := f.login("all")
	sub := f.subs[0]
	if err := f.q.InsertSubmissionFlag(bg, sqlc.InsertSubmissionFlagParams{SubmissionID: sub, Kind: "source", Reason: suspicious.Process,
		Detail: `sum.%l:2: system("id");`}); err != nil {
		t.Fatal(err)
	}
	f.q.InsertSubmissionFlag(bg, sqlc.InsertSubmissionFlagParams{SubmissionID: sub, Kind: "runtime", Reason: suspicious.Forbidden, Detail: "testcase 0"})
	code, body := b.Get(fmt.Sprintf("/submissions/%d", sub))
	if code != 200 || !strings.Contains(body, "Suspicious submission") || !strings.Contains(body, "starts other programs") ||
		!strings.Contains(body, "seen by the sandbox") || !strings.Contains(body, "system(&#34;id&#34;);") {
		t.Fatalf("submission page: %d\n%s", code, body)
	}
	list := fmt.Sprintf("/contests/%d/submissions", f.contest.ID)
	if _, body := b.Get(list); !strings.Contains(body, "suspicious</span>") {
		t.Fatal("no suspicious tag in the list")
	}
	_, body = b.Get(list + "?status=flagged")
	if !strings.Contains(body, fmt.Sprintf(`href="/submissions/%d"`, sub)) || strings.Contains(body, fmt.Sprintf(`href="/submissions/%d"`, f.subs[1])) {
		t.Fatalf("flagged filter:\n%s", body)
	}
	if _, body := b.Get(fmt.Sprintf("/contests/%d", f.contest.ID)); !strings.Contains(body, "1 suspicious submissions") {
		t.Fatal("no dashboard notification")
	}
}
