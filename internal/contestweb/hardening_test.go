package contestweb

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// routeSource is the package's non-test source, where routes are registered
// (server.go and extra.go).
func routeSource(t *testing.T) string {
	t.Helper()
	files, _ := filepath.Glob("*.go")
	var src strings.Builder
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src.Write(b)
	}
	return src.String()
}

// postRoutes lists the POST routes registered in the package.
func postRoutes(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, m := range regexp.MustCompile(`"POST (/[^"]+)"`).FindAllStringSubmatch(routeSource(t), -1) {
		out = append(out, m[1])
	}
	if !slices.Contains(out, "/{contest}/questions") {
		t.Fatal("the routes of extra.go were not found")
	}
	if len(out) < 8 {
		t.Fatalf("only %d POST routes found", len(out))
	}
	return out
}

func (f *fixture) post(c *http.Client, path string, form url.Values, hdr ...string) (int, string) {
	f.t.Helper()
	req, _ := http.NewRequest("POST", f.url+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := c.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// TestEveryPostNeedsCSRF (SPEC_CLOSE F2): every state-changing request of
// the contest web server is refused without the session's token, with
// another session's token, and from another origin.
func TestEveryPostNeedsCSRF(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.setContest(t, "registration = 'approval'") // so /register is reachable
	c, other := f.client(), f.client()
	_, page := f.login(c, "ana", "secret")
	mine := csrfOf(t, page)
	_, otherPage := f.get(other, "/ioi/login")
	foreign := csrfOf(t, otherPage)
	repl := strings.NewReplacer("{contest}", "ioi", "{task}", "sum", "{id}", "1", "{which}", "input")
	for _, route := range postRoutes(t) {
		if strings.HasSuffix(route, "/lang") {
			continue // only sets the interface language cookie
		}
		path := repl.Replace(route)
		for name, token := range map[string]string{"none": "", "another session's": foreign} {
			if code, _ := f.post(c, path, url.Values{"csrf": {token}, "username": {"ana"}, "password": {"secret"}}); code != http.StatusForbidden {
				t.Errorf("POST %s with %s token = %d, want 403", path, name, code)
			}
		}
		if code, _ := f.post(c, path, url.Values{"csrf": {mine}}, "Origin", "https://evil.example"); code != http.StatusForbidden {
			t.Errorf("POST %s from another origin = %d, want 403", path, code)
		}
	}
}

// TestSessionCookiesAreHardened: HttpOnly, SameSite=Lax and, when
// configured (HTTPS), Secure.
func TestSessionCookiesAreHardened(t *testing.T) {
	f := newFixture(t, fixtureOpts{cookieSecure: true})
	// The test server speaks plain HTTP: read the Set-Cookie headers.
	resp, err := http.Get(f.url + "/ioi/login")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var cookies []*http.Cookie
	cookies = append(cookies, resp.Cookies()...)
	jar := resp.Cookies()
	req, _ := http.NewRequest("POST", f.url+"/ioi/login", strings.NewReader(url.Values{"csrf": {csrfOf(t, string(b))}, "username": {"ana"}, "password": {"secret"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, ck := range jar {
		req.AddCookie(ck)
	}
	resp, err = http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login = %d", resp.StatusCode)
	}
	cookies = append(cookies, resp.Cookies()...)
	if len(cookies) < 2 {
		t.Fatalf("cookies %v", cookies)
	}
	for _, ck := range cookies {
		if !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteLaxMode {
			t.Errorf("cookie %s: HttpOnly %v Secure %v SameSite %v", ck.Name, ck.HttpOnly, ck.Secure, ck.SameSite)
		}
	}
}

// TestLoginLimitsCountFailures: a whole lab behind one address logs in at
// once (successes are free), while wrong passwords are limited per
// address and per username.
func TestLoginLimitsCountFailures(t *testing.T) {
	f := newFixture(t, fixtureOpts{loginLimit: 5, trusted: []string{"127.0.0.1/32"}})
	// One minute for the whole test: failures split by a minute boundary
	// would not add up.
	frozen := time.Now()
	f.srv.limiter.Now = func() time.Time { return frozen }
	for i := 0; i < 12; i++ {
		if code, _ := f.login(f.client(), "ana", "secret"); code != 200 {
			t.Fatalf("login %d from the shared address = %d", i, code)
		}
	}
	lab := func(ip string) *http.Client {
		c := f.client()
		c.Transport = headerTransport{"X-Forwarded-For", ip}
		return c
	}
	// Five wrong passwords from one address block it, whatever the user.
	c := lab("10.0.0.1")
	for i := 0; i < 5; i++ {
		if code, _ := f.login(c, "nobody", "x"); code != http.StatusUnauthorized {
			t.Fatalf("failure %d = %d", i, code)
		}
	}
	if code, _ := f.login(c, "ana", "secret"); code != http.StatusTooManyRequests {
		t.Fatalf("after 5 failures = %d, want 429", code)
	}
	if code, _ := f.login(lab("10.0.0.2"), "ana", "secret"); code != 200 {
		t.Fatalf("another address = %d", code)
	}
	// Ten wrong passwords for one username, from ten addresses, lock it.
	for i := 0; i < 10; i++ {
		f.login(lab("10.1.0."+itoa(int64(i+1))), "ana", "wrong")
	}
	if code, _ := f.login(lab("10.2.0.1"), "ana", "secret"); code != http.StatusTooManyRequests {
		t.Fatalf("username after 10 failures = %d, want 429", code)
	}
}

type headerTransport struct{ k, v string }

func (h headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set(h.k, h.v)
	return http.DefaultTransport.RoundTrip(r)
}

// TestRequestBodiesAreBounded: an upload over the limit is refused before
// the form is parsed (413), and so is a large form without files; the
// login form is small.
func TestRequestBodiesAreBounded(t *testing.T) {
	f := newFixture(t, fixtureOpts{maxSubmission: 32 << 10})
	c := f.client()
	_, page := f.login(c, "ana", "secret")
	csrf := csrfOf(t, page)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("csrf", csrf)
	mw.WriteField("language", "c11")
	fw, _ := mw.CreateFormFile("sum.%l", "sum.c")
	fw.Write(bytes.Repeat([]byte("/* padding */\n"), 16<<10)) // 224 KiB
	mw.Close()
	req, _ := http.NewRequest("POST", f.url+"/ioi/tasks/sum/submit", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized submission = %d, want 413", resp.StatusCode)
	}
	var n int
	f.pool.QueryRow(bg, "SELECT count(*) FROM submissions WHERE participation_id = $1", f.part.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("%d submissions stored", n)
	}
	big := strings.Repeat("x", 100<<10)
	if code, _ := f.post(c, "/ioi/questions", url.Values{"csrf": {csrf}, "subject": {"s"}, "text": {big}}); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("100 KiB form = %d, want 413", code)
	}
	if code, _ := f.post(f.client(), "/ioi/login", url.Values{"csrf": {"x"}, "username": {big}}); code < 400 {
		t.Fatalf("100 KiB login = %d", code)
	}
}
