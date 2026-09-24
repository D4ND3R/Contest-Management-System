// Package webkit contains the HTTP building blocks shared by the contest,
// admin and ranking web servers: signed session cookies, CSRF tokens,
// hashed and precompressed static assets, templates, security headers,
// client-IP resolution and rate limiting.
package webkit

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

var b64 = base64.RawURLEncoding

// Signer authenticates small payloads with HMAC-SHA256. Each purpose
// (sessions, CSRF, ...) derives its own key from the installation secret.
type Signer struct{ key []byte }

// NewSigner derives a signer for purpose from secret.
func NewSigner(secret []byte, purpose string) *Signer {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte("cms/" + purpose))
	return &Signer{key: m.Sum(nil)}
}

func (s *Signer) mac(data []byte) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write(data)
	return m.Sum(nil)
}

// Sign returns base64(payload).base64(mac).
func (s *Signer) Sign(payload []byte) string {
	return b64.EncodeToString(payload) + "." + b64.EncodeToString(s.mac(payload))
}

// Verify returns the payload of a valid token.
func (s *Signer) Verify(token string) ([]byte, bool) {
	p, m, ok := strings.Cut(token, ".")
	if !ok {
		return nil, false
	}
	payload, err := b64.DecodeString(p)
	if err != nil {
		return nil, false
	}
	mac, err := b64.DecodeString(m)
	if err != nil || !hmac.Equal(mac, s.mac(payload)) {
		return nil, false
	}
	return payload, true
}

// RandomID returns a random URL-safe identifier of n bytes of entropy.
func RandomID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b64.EncodeToString(b)
}

// Session is the content of a session cookie. Sessions are stateless: the
// server keeps nothing but can invalidate them through the nonce.
type Session struct {
	ID              string `json:"s"`           // random id (CSRF binding)
	ParticipationID int64  `json:"p,omitempty"` // contestants
	UserID          int64  `json:"u,omitempty"`
	ContestID       int64  `json:"c,omitempty"`
	AdminID         int64  `json:"a,omitempty"` // administrators
	Nonce           int64  `json:"n,omitempty"` // single-login counter
	Lang            string `json:"l,omitempty"`
	Issued          int64  `json:"i"`
	Expires         int64  `json:"e"`
}

// Valid reports whether the session has not expired.
func (s *Session) Valid(now time.Time) bool { return now.Unix() < s.Expires }

// CookieCodec reads and writes session cookies.
type CookieCodec struct {
	Name   string
	Signer *Signer
	Secure bool
	Path   string
	TTL    time.Duration
}

// Read returns the session in r, or nil.
func (c *CookieCodec) Read(r *http.Request) *Session {
	ck, err := r.Cookie(c.Name)
	if err != nil {
		return nil
	}
	payload, ok := c.Signer.Verify(ck.Value)
	if !ok {
		return nil
	}
	var s Session
	if json.Unmarshal(payload, &s) != nil || !s.Valid(time.Now()) {
		return nil
	}
	return &s
}

// Write sets a session cookie (issuing a new id when empty).
func (c *CookieCodec) Write(w http.ResponseWriter, s *Session) {
	now := time.Now()
	if s.ID == "" {
		s.ID = RandomID(12)
	}
	if s.Issued == 0 {
		s.Issued = now.Unix()
	}
	s.Expires = now.Add(c.TTL).Unix()
	payload, _ := json.Marshal(s)
	http.SetCookie(w, &http.Cookie{
		Name: c.Name, Value: c.Signer.Sign(payload), Path: c.path(), HttpOnly: true, Secure: c.Secure,
		SameSite: http.SameSiteLaxMode, Expires: now.Add(c.TTL),
	})
}

// Clear deletes the session cookie.
func (c *CookieCodec) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: c.Name, Value: "", Path: c.path(), HttpOnly: true, Secure: c.Secure,
		SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

func (c *CookieCodec) path() string {
	if c.Path == "" {
		return "/"
	}
	return c.Path
}

// CSRF issues and checks per-session anti-forgery tokens.
type CSRF struct{ signer *Signer }

// NewCSRF derives the CSRF key from secret.
func NewCSRF(secret []byte) *CSRF { return &CSRF{signer: NewSigner(secret, "csrf")} }

// Token returns the token bound to a session id.
func (c *CSRF) Token(sessionID string) string {
	return b64.EncodeToString(c.signer.mac([]byte(sessionID)))
}

// ErrCSRF is returned for missing or invalid tokens.
var ErrCSRF = errors.New("invalid or missing CSRF token")

// Check validates the token of a state-changing request: the form field
// "csrf" or the X-CSRF-Token header (htmx), plus a same-origin check on the
// Origin header when present.
func (c *CSRF) Check(r *http.Request, sessionID string) error {
	if o := r.Header.Get("Origin"); o != "" && o != "null" {
		host := strings.TrimPrefix(strings.TrimPrefix(o, "https://"), "http://")
		if host != r.Host {
			return ErrCSRF
		}
	}
	tok := r.Header.Get("X-CSRF-Token")
	if tok == "" {
		tok = r.FormValue("csrf")
	}
	want := c.Token(sessionID)
	if tok == "" || !hmac.Equal([]byte(tok), []byte(want)) {
		return ErrCSRF
	}
	return nil
}
