package webkit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestSignerAndSessions(t *testing.T) {
	s := NewSigner([]byte("0123456789abcdef0123456789abcdef"), "session")
	tok := s.Sign([]byte("hello"))
	if p, ok := s.Verify(tok); !ok || string(p) != "hello" {
		t.Fatal("roundtrip failed")
	}
	if _, ok := s.Verify(tok + "x"); ok {
		t.Fatal("tampered mac accepted")
	}
	if _, ok := NewSigner([]byte("0123456789abcdef0123456789abcdef"), "other").Verify(tok); ok {
		t.Fatal("token accepted for another purpose")
	}
	codec := &CookieCodec{Name: "sid", Signer: s, TTL: time.Hour, Secure: true}
	rec := httptest.NewRecorder()
	codec.Write(rec, &Session{ParticipationID: 7, Nonce: 3})
	ck := rec.Result().Cookies()[0]
	if !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie flags %+v", ck)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(ck)
	got := codec.Read(req)
	if got == nil || got.ParticipationID != 7 || got.Nonce != 3 || got.ID == "" {
		t.Fatalf("session %+v", got)
	}
	// Expired sessions are rejected.
	codec.TTL = -time.Second
	rec = httptest.NewRecorder()
	codec.Write(rec, &Session{ParticipationID: 7})
	req = httptest.NewRequest("GET", "/", nil)
	req.AddCookie(rec.Result().Cookies()[0])
	if codec.Read(req) != nil {
		t.Fatal("expired session accepted")
	}
}

func TestCSRF(t *testing.T) {
	c := NewCSRF([]byte("0123456789abcdef0123456789abcdef"))
	tok := c.Token("sid1")
	form := url.Values{"csrf": {tok}}
	req := httptest.NewRequest("POST", "http://cms.local/x", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := c.Check(req, "sid1"); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	req = httptest.NewRequest("POST", "http://cms.local/x", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := c.Check(req, "sid2"); err == nil {
		t.Fatal("token of another session accepted")
	}
	req = httptest.NewRequest("POST", "http://cms.local/x", nil)
	req.Header.Set("X-CSRF-Token", tok)
	req.Header.Set("Origin", "http://evil.example")
	if err := c.Check(req, "sid1"); err == nil {
		t.Fatal("cross-origin request accepted")
	}
	req = httptest.NewRequest("POST", "http://cms.local/x", nil)
	req.Header.Set("X-CSRF-Token", tok)
	req.Header.Set("Origin", "http://cms.local")
	if err := c.Check(req, "sid1"); err != nil {
		t.Fatalf("same-origin htmx request rejected: %v", err)
	}
}

func TestStatic(t *testing.T) {
	fsys := fstest.MapFS{"app.js": {Data: []byte(strings.Repeat("console.log(1);", 200))}}
	s, err := NewStatic(fsys, "/static")
	if err != nil {
		t.Fatal(err)
	}
	u := s.URL("app.js")
	if !strings.HasPrefix(u, "/static/app.js?v=") {
		t.Fatalf("url %s", u)
	}
	req := httptest.NewRequest("GET", u, nil)
	req.Header.Set("Accept-Encoding", "gzip, br")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("headers %v", rec.Header())
	}
	if rec.Body.Len() >= 3000 || s.GzipSize("app.js") != rec.Body.Len() {
		t.Fatalf("not compressed: %d", rec.Body.Len())
	}
	req = httptest.NewRequest("GET", "/static/app.js", nil)
	req.Header.Set("If-None-Match", rec.Header().Get("ETag"))
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("conditional GET = %d", rec.Code)
	}
}

func TestClientIP(t *testing.T) {
	r, _ := NewIPResolver([]string{"10.0.0.0/8"})
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	if ip := r.ClientIP(req); ip.String() != "203.0.113.5" {
		t.Fatalf("untrusted peer's XFF honoured: %s", ip)
	}
	req.RemoteAddr = "10.1.1.1:1234"
	req.Header.Set("X-Forwarded-For", "198.51.100.7, 10.2.2.2")
	if ip := r.ClientIP(req); ip.String() != "198.51.100.7" {
		t.Fatalf("client behind proxies = %s", ip)
	}
}

func TestLimiterLocalFallback(t *testing.T) {
	l := NewLimiter(nil, "t:")
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if !l.Allow(ctx, "k", 3, time.Minute) {
			t.Fatalf("event %d denied", i)
		}
	}
	if l.Allow(ctx, "k", 3, time.Minute) {
		t.Fatal("limit not enforced")
	}
}

func TestSecurityHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	SecurityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-inline") || rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("headers %v", rec.Header())
	}
}
