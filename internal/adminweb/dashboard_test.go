package adminweb

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/webtest"
)

// pngBytes is a small valid PNG image.
func pngBytes(t *testing.T) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	img.Set(1, 1, color.RGBA{200, 20, 20, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestContestDashboard (SPEC_IOI H2): the contest page is a live
// dashboard (scoreboard, problem status, events, analytics, quick stats,
// system health, notifications) with the contest's banner; the admin home
// shows the running contest's; the settings and the problems moved to their
// own pages.
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
	for _, w := range []string{`<h1>OMI 2026</h1>`, "Mérida, México", "“Mejores problemas.”", "Live scoreboard", "Problem status",
		"Recent events", "Contest analytics", "Quick stats", "System health", "Notifications", "Contest in progress",
		`href="/participations/` + fmt.Sprint(f.part.ID) + `"`, "New submission from ana", `data-countdown="`,
		`hx-get="/contests/` + id + `/live"`, `class="nav active" href="/contests/` + id + `"`} {
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
	if _, home := b.Get("/"); !strings.Contains(home, "Live scoreboard") || !strings.Contains(home, "Current and upcoming contests") {
		t.Fatalf("home:\n%s", home)
	}
	// Settings and problems.
	if code, body := b.Get("/contests/" + id + "/settings"); code != 200 || !strings.Contains(body, `name="tagline" value="Mejores problemas."`) ||
		!strings.Contains(body, `action="/contests/`+id+`/banner"`) {
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

// TestContestBanner: the banner image is a PNG, JPEG, GIF or WebP (checked
// on the content, never SVG) of at most 4 MiB; it is served with its type,
// shown on the dashboard, and removable; read-only admins cannot set it.
func TestContestBanner(t *testing.T) {
	f := newFixture(t)
	b := f.login("all")
	path := fmt.Sprintf("/contests/%d/banner", f.contest.ID)
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	if code, _ := b.PostMultipart(path, nil, webtest.File{Field: "banner", Name: "x.png", Data: svg}); code != http.StatusUnprocessableEntity {
		t.Fatalf("svg banner = %d", code)
	}
	big := append(pngBytes(t), make([]byte, maxBannerBytes)...)
	if code, _ := b.PostMultipart(path, nil, webtest.File{Field: "banner", Name: "big.png", Data: big}); code != http.StatusUnprocessableEntity {
		t.Fatalf("big banner = %d", code)
	}
	if code, _ := f.login("read_only").PostMultipart(path, nil, webtest.File{Field: "banner", Name: "b.png", Data: pngBytes(t)}); code != http.StatusForbidden {
		t.Fatalf("read-only banner = %d", code)
	}
	code, body := b.PostMultipart(path, nil, webtest.File{Field: "banner", Name: "b.png", Data: pngBytes(t)})
	if code != 200 || !strings.Contains(body, "Banner saved.") {
		t.Fatalf("upload = %d\n%s", code, body)
	}
	c, _ := f.q.GetContest(bg, f.contest.ID)
	if c.BannerDigest == nil || c.BannerType != "image/png" {
		t.Fatalf("banner not stored: %+v", c)
	}
	resp, err := b.C.Get(f.url + path)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("serve: %d %v", resp.StatusCode, resp.Header)
	}
	if _, dash := b.Get(fmt.Sprintf("/contests/%d", f.contest.ID)); !strings.Contains(dash, `<img class="bg" src="`+path+`?v=`+(*c.BannerDigest)[:12]) {
		t.Fatalf("dashboard without the banner:\n%s", dash)
	}
	if code, body := b.Post(path, url.Values{"remove": {"1"}}); code != 200 || !strings.Contains(body, "Banner removed.") {
		t.Fatalf("remove = %d", code)
	}
	if c, _ = f.q.GetContest(bg, f.contest.ID); c.BannerDigest != nil {
		t.Fatal("banner still set")
	}
	if code, _ := b.Get(path); code != http.StatusNotFound {
		t.Fatalf("removed banner = %d", code)
	}
}
