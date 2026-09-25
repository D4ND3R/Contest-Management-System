package adminweb

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/auth"
	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/webtest"
)

// adminPostRoutes lists the POST routes registered in server.go.
func adminPostRoutes(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	re := regexp.MustCompile(`(?:post\("|route\("POST |HandleFunc\("POST )(/[^"]*)"`)
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		out = append(out, m[1])
	}
	if len(out) < 60 {
		t.Fatalf("only %d POST routes found", len(out))
	}
	return out
}

func rawPost(t *testing.T, b *webtest.Browser, path, body, contentType string, hdr ...string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", b.Base+path, strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := b.C.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// TestEveryAdminPostNeedsCSRF (SPEC_CLOSE F2): every state-changing admin
// request is refused without the session's token and from another origin.
func TestEveryAdminPostNeedsCSRF(t *testing.T) {
	f := newFixture(t)
	a := f.login("all")
	repl := strings.NewReplacer("{id}", "1", "{name}", "x", "{action}", "deliver", "{digest}", strings.Repeat("0", 64))
	form := "csrf=bogus&name=x&confirm=x"
	for _, route := range adminPostRoutes(t) {
		if route == "/lang" {
			continue // only sets the interface language cookie
		}
		path := repl.Replace(route)
		if code := rawPost(t, a, path, form, "application/x-www-form-urlencoded"); code != http.StatusForbidden {
			t.Errorf("POST %s with a wrong token = %d, want 403", path, code)
		}
		good := "csrf=" + url.QueryEscape(a.CSRF)
		if code := rawPost(t, a, path, good, "application/x-www-form-urlencoded", "Origin", "https://evil.example"); code != http.StatusForbidden {
			t.Errorf("POST %s from another origin = %d, want 403", path, code)
		}
	}
}

// TestAdminCookiesAreHardened: HttpOnly, SameSite=Lax and Secure (HTTPS).
func TestAdminCookiesAreHardened(t *testing.T) {
	f := newFixture(t, func(c *config.AdminWeb) { c.CookieSecure = true })
	resp, err := http.Get(f.url + "/login")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(resp.Cookies()) == 0 {
		t.Fatal("no session cookie on the login page")
	}
	for _, ck := range resp.Cookies() {
		if !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteLaxMode {
			t.Errorf("cookie %s: HttpOnly %v Secure %v SameSite %v", ck.Name, ck.HttpOnly, ck.Secure, ck.SameSite)
		}
	}
}

// TestAdminLoginLimits: successful logins are free; wrong passwords are
// limited per username and per address, wrong second-factor codes per
// administrator.
func TestAdminLoginLimits(t *testing.T) {
	f := newFixture(t, func(c *config.AdminWeb) { c.LoginRateLimit = 15 })
	for i := 0; i < 20; i++ {
		f.login("read_only")
	}
	try := func(user, pass string) int {
		b := webtest.New(t, f.url)
		b.Get("/login")
		code, _ := b.Post("/login", url.Values{"username": {user}, "password": {pass}})
		return code
	}
	for i := 0; i < 10; i++ {
		if code := try("admin_all", "wrong"); code != http.StatusUnauthorized {
			t.Fatalf("failure %d = %d", i, code)
		}
	}
	if code := try("admin_all", "password1"); code != http.StatusTooManyRequests {
		t.Fatalf("username after 10 failures = %d, want 429", code)
	}
	if code := try("admin_messaging", "password1"); code != 200 {
		t.Fatalf("another administrator = %d", code)
	}
	for i := 0; i < 5; i++ {
		try("nobody", "x")
	}
	if code := try("admin_messaging", "password1"); code != http.StatusTooManyRequests {
		t.Fatalf("address after 15 failures = %d, want 429", code)
	}

	// Second factor: five wrong codes lock it, whatever the address.
	g := newFixture(t)
	secret := "JBSWY3DPEHPK3PXP"
	g.pool.Exec(bg, "UPDATE admins SET totp_secret = $1 WHERE id = $2", secret, g.admins["messaging"].ID)
	c := webtest.New(t, g.url)
	c.Get("/login")
	_, body := c.Post("/login", url.Values{"username": {"admin_messaging"}, "password": {"password1"}})
	tok := tokenRe.FindStringSubmatch(body)
	if tok == nil {
		t.Fatalf("no second step:\n%s", body)
	}
	for i := 0; i < 5; i++ {
		if code, _ := c.Post("/login/2fa", url.Values{"token": {tok[1]}, "totp_code": {"000000"}}); code != http.StatusUnauthorized {
			t.Fatalf("wrong code %d = %d", i, code)
		}
	}
	good, _ := auth.TOTPCode(secret, time.Now())
	if code, _ := c.Post("/login/2fa", url.Values{"token": {tok[1]}, "totp_code": {good}}); code != http.StatusTooManyRequests {
		t.Fatalf("right code after 5 wrong ones = %d, want 429", code)
	}
}

// TestAdminBodiesAreBounded: forms over 1 MiB and uploads over the limit
// are refused (413) before anything parses them.
func TestAdminBodiesAreBounded(t *testing.T) {
	f := newFixture(t, func(c *config.AdminWeb) { c.MaxUploadBytes = 64 << 10 })
	a := f.login("all")
	big := "csrf=" + url.QueryEscape(a.CSRF) + "&description=" + strings.Repeat("x", 2<<20)
	if code := rawPost(t, a, "/contests/1", big, "application/x-www-form-urlencoded"); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("2 MiB form = %d, want 413", code)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("csrf", a.CSRF)
	fw, _ := mw.CreateFormFile("package", "big.zip")
	fw.Write(bytes.Repeat([]byte{0}, 2<<20))
	mw.Close()
	if code := rawPost(t, a, "/tasks/import", body.String(), mw.FormDataContentType()); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("upload over the limit = %d, want 413", code)
	}
	a.Get("/login")
	if code := rawPost(t, webtest.New(t, f.url), "/login", "username="+strings.Repeat("x", 64<<10), "application/x-www-form-urlencoded"); code < 400 {
		t.Fatalf("64 KiB login = %d", code)
	}
}
