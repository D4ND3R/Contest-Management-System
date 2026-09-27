package contestweb

import (
	"net/http"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
)

// TestContestantDashboard (SPEC_IOI H2): the overview is a dashboard: the
// contest's banner (title, subtitle, dates, place, motto, image), shortcuts,
// the tasks with their state, the latest submissions, the progress, the
// ranking and the announcements; the banner image is public (the login
// page shows it) and served only with an image type.
func TestContestantDashboard(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	c := db.ContestToUpdate(f.contest)
	c.Title, c.Description, c.Location, c.Tagline = "OMI 2026", "Selección nacional", "Mérida", "Mejores problemas."
	f.q.UpdateContest(bg, c)
	img, _ := f.store.PutBytes(bg, []byte("\x89PNG\r\n\x1a\nfake"))
	f.q.SetContestBanner(bg, sqlc.SetContestBannerParams{ID: f.contest.ID, Digest: &img.Digest, MediaType: "image/png"})
	f.q.CreateAnnouncement(bg, sqlc.CreateAnnouncementParams{ContestID: f.contest.ID, Subject: "Recordatorio", Text: "Sin bibliotecas."})
	f.srv.cache.invalidateContest(f.contest.ID)
	banner := "/ioi/banner?v=" + img.Digest[:20]

	anon := f.client()
	if _, body := f.get(anon, "/ioi/login"); !strings.Contains(body, `<img class="login-banner" src="`+banner+`"`) || !strings.Contains(body, "<b>OMI 2026</b>") {
		t.Fatalf("login page:\n%s", body)
	}
	code, h, _ := f.headers(anon, banner)
	if code != 200 || h.Get("Content-Type") != "image/png" || !strings.Contains(h.Get("Cache-Control"), "immutable") {
		t.Fatalf("banner: %d %v", code, h)
	}

	cl := f.client()
	_, page := f.login(cl, "ana", "secret")
	csrf := csrfOf(t, page)
	f.submit(cl, csrf, "c11", "int main(){}", false)
	code, body := f.get(cl, "/ioi/")
	if code != 200 {
		t.Fatalf("overview: %d", code)
	}
	for _, w := range []string{`<h1>OMI 2026</h1>`, `<p class="sub">Selección nacional</p>`, "Mérida", "“Mejores problemas.”",
		`<img class="bg" src="` + banner + `"`, `class="tile blue" href="/ioi/tasks/sum"`, "Your latest submissions", "Compiling…",
		"Your progress", "Recordatorio", `<span class="letter c1">A</span>`, "Contest in progress", `class="nav active" href="/ioi/"`} {
		if !strings.Contains(body, w) {
			t.Errorf("overview lacks %s", w)
		}
	}
	// The task counts one submission and was tried, not solved.
	if !strings.Contains(body, `<td class="num">1</td>`) || !strings.Contains(body, `class="st-ic bad"`) {
		t.Errorf("task row:\n%s", body)
	}

	// Anything but an image type is never served.
	f.q.SetContestBanner(bg, sqlc.SetContestBannerParams{ID: f.contest.ID, Digest: &img.Digest, MediaType: "image/svg+xml"})
	f.srv.cache.invalidateContest(f.contest.ID)
	if code, _, _ := f.headers(anon, "/ioi/banner"); code != http.StatusNotFound {
		t.Fatalf("svg banner served: %d", code)
	}
}
