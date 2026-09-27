package contestweb

import (
	"net/http"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
)

// TestProblemsPage (SPEC_MIN §1, §15): the contest's first page is a
// plain list of problems with the contestant's score, the latest
// submissions and announcements; no pictures (the banner image is not
// shown, though its address stays safe).
func TestProblemsPage(t *testing.T) {
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
	if _, body := f.get(anon, "/ioi/login"); strings.Contains(body, "<img") || !strings.Contains(body, ">OMI 2026</a>") {
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
	for _, w := range []string{`<h1>OMI 2026</h1>`, "Selección nacional", `<a href="/ioi/tasks/sum">sum</a>`, "Your latest submissions",
		"Compiling…", "Recordatorio", "Contest in progress", `<a href="/ioi/" aria-current="page">Problems</a>`,
		`<a href="/ioi/tasks/sum" title="Suma" >A. sum</a>`} {
		if !strings.Contains(body, w) {
			t.Errorf("problems page lacks %s", w)
		}
	}
	if strings.Contains(body, "<img") || strings.Contains(body, "<svg") || strings.Contains(body, `class="card`) {
		t.Error("pictures or cards on the problems page")
	}
	// The task counts one submission.
	if !strings.Contains(body, `<td class="num">1</td>`) {
		t.Errorf("task row:\n%s", body)
	}

	// Anything but an image type is never served.
	f.q.SetContestBanner(bg, sqlc.SetContestBannerParams{ID: f.contest.ID, Digest: &img.Digest, MediaType: "image/svg+xml"})
	f.srv.cache.invalidateContest(f.contest.ID)
	if code, _, _ := f.headers(anon, "/ioi/banner"); code != http.StatusNotFound {
		t.Fatalf("svg banner served: %d", code)
	}
}
