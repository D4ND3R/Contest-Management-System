package adminweb

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/webtest"
)

// TestAccessibility checks the administration pages with the shared
// accessibility rules.
func TestAccessibility(t *testing.T) {
	f := newFixture(t)
	anon := webtest.New(t, f.url)
	_, login := anon.Get("/login")
	webtest.A11y(t, "login", login, true)
	b := f.login("all")
	c, tk, u := f.contest.ID, f.task.ID, f.user.ID
	for _, p := range []string{"/", "/contests", fmt.Sprintf("/contests/%d", c), fmt.Sprintf("/contests/%d/settings", c),
		fmt.Sprintf("/contests/%d/tasks", c), "/contests/new", "/tasks", fmt.Sprintf("/tasks/%d", tk),
		fmt.Sprintf("/datasets/%d", f.ds.ID), "/users", fmt.Sprintf("/users/%d", u), "/users/new", "/tasks/import", fmt.Sprintf("/participations/%d", f.part.ID),
		fmt.Sprintf("/contests/%d/participations", c), fmt.Sprintf("/contests/%d/submissions", c),
		fmt.Sprintf("/submissions/%d", f.subs[0]), "/questions", fmt.Sprintf("/contests/%d/communication", c),
		fmt.Sprintf("/contests/%d/ranking", c), fmt.Sprintf("/contests/%d/stats", c), "/system", "/audit", "/backups",
		"/languages", "/admins", "/account", "/teams", fmt.Sprintf("/contests/%d/sites", c),
		fmt.Sprintf("/contests/%d/printing", c), fmt.Sprintf("/contests/%d/appeals", c), fmt.Sprintf("/contests/%d/balloons", c),
		fmt.Sprintf("/contests/%d/certificates", c), fmt.Sprintf("/contests/%d/plagiarism", c), fmt.Sprintf("/tasks/%d/validation", tk)} {
		code, body := b.Get(p)
		if code != http.StatusOK {
			t.Errorf("%s: %d", p, code)
			continue
		}
		webtest.A11y(t, p, body, true)
	}
}
