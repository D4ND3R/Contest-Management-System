package adminweb

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/auth"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/webtest"
)

// TestDefaultPasswordMustBeChanged: an administrator who logs in with a
// well-known default password (admin/admin, as `make dev` creates) reaches
// nothing but the account page until choosing another one, which cannot be
// a default either; the admin forms refuse default passwords too.
func TestDefaultPasswordMustBeChanged(t *testing.T) {
	f := newFixture(t)
	hash, _ := auth.HashPassword("admin")
	a, err := f.q.CreateAdmin(bg, sqlc.CreateAdminParams{Name: "Admin", Username: "admin", PasswordHash: hash, Enabled: true, Role: "all"})
	if err != nil {
		t.Fatal(err)
	}
	b := webtest.New(t, f.url)
	b.Get("/login")
	const banner = "You logged in with a well-known default password"
	code, body := b.Post("/login", url.Values{"username": {"admin"}, "password": {"admin"}, "next": {"/contests"}})
	if code != 200 || !strings.Contains(body, banner) || !strings.HasSuffix(b.Last, "/account") {
		t.Fatalf("login with the default password = %d at %s\n%s", code, b.Last, body)
	}
	// Everything else leads back to the account page, reads and changes.
	if _, body := b.Get("/contests"); !strings.Contains(body, banner) {
		t.Fatal("the contests page opened before the password was changed")
	}
	if _, body := b.Post("/admins", url.Values{"username": {"intruder"}, "password": {"long-enough-1"}, "role": {"all"}}); !strings.Contains(body, banner) {
		t.Fatal("a change went through before the password was changed")
	}
	if _, err := f.q.GetAdminByUsername(bg, "intruder"); err == nil {
		t.Fatal("the admin was created")
	}
	// Not another default, nor the same one.
	for _, pw := range []string{"password", "admin", "ADMIN"} {
		if code, _ := b.Post("/account/password", url.Values{"current_password": {"admin"}, "password": {pw}}); code != http.StatusUnprocessableEntity {
			t.Fatalf("new password %q = %d", pw, code)
		}
	}
	if code, body := b.Post("/account/password", url.Values{"current_password": {"admin"}, "password": {"k3x9-Tq7vB"}}); code != 200 || strings.Contains(body, banner) {
		t.Fatalf("change = %d\n%s", code, body)
	}
	if code, body := b.Get("/contests"); code != 200 || strings.Contains(body, banner) {
		t.Fatalf("contests after the change = %d", code)
	}
	if got, _ := f.q.GetAdmin(bg, a.ID); got.PasswordChangeRequired {
		t.Fatal("the flag stayed")
	}
	// A good password never raises the flag.
	nb := webtest.New(t, f.url)
	nb.Get("/login")
	if _, body := nb.Post("/login", url.Values{"username": {"admin"}, "password": {"k3x9-Tq7vB"}}); strings.Contains(body, banner) {
		t.Fatal("asked to change a good password")
	}

	// The admin forms refuse defaults.
	c := f.login("all")
	for _, form := range []url.Values{
		{"username": {"staffer1"}, "password": {"staffer1"}, "role": {"all"}},
		{"username": {"staffer2"}, "password": {"password"}, "role": {"all"}},
	} {
		if code, _ := c.Post("/admins", form); code != http.StatusUnprocessableEntity {
			t.Fatalf("admin with %v = %d", form, code)
		}
	}
	if code, _ := c.Post(fmt.Sprintf("/admins/%d", a.ID), url.Values{"username": {"admin"}, "enabled": {"on"}, "role": {"all"}, "password": {"12345678"}}); code != http.StatusUnprocessableEntity {
		t.Fatalf("reset to a default = %d", code)
	}
}
