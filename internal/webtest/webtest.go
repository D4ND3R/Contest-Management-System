// Package webtest is a small browser-like HTTP client for tests of the web
// servers: a cookie jar, the CSRF token of the last page, form and
// multipart posts.
package webtest

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"testing"
	"time"
)

// Browser keeps cookies and the latest CSRF token.
type Browser struct {
	T    testing.TB
	Base string
	C    *http.Client
	CSRF string
	// Last is the URL of the last response (after redirects).
	Last string
}

var csrfRe = regexp.MustCompile(`name="csrf-token" content="([^"]+)"`)

// New returns a browser for the server at base (e.g. http://127.0.0.1:1234).
func New(t testing.TB, base string) *Browser {
	jar, _ := cookiejar.New(nil)
	return &Browser{T: t, Base: base, C: &http.Client{Jar: jar, Timeout: 60 * time.Second}}
}

func (b *Browser) do(req *http.Request) (int, string) {
	b.T.Helper()
	resp, err := b.C.Do(req)
	if err != nil {
		b.T.Fatalf("%s %s: %v", req.Method, req.URL, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if m := csrfRe.FindSubmatch(body); m != nil {
		b.CSRF = string(m[1])
	}
	b.Last = resp.Request.URL.String()
	return resp.StatusCode, string(body)
}

// Get fetches path.
func (b *Browser) Get(path string) (int, string) {
	b.T.Helper()
	req, _ := http.NewRequest("GET", b.Base+path, nil)
	return b.do(req)
}

// Post submits a urlencoded form (the CSRF token is added).
func (b *Browser) Post(path string, form url.Values) (int, string) {
	b.T.Helper()
	if form == nil {
		form = url.Values{}
	}
	if form.Get("csrf") == "" {
		form.Set("csrf", b.CSRF)
	}
	req, _ := http.NewRequest("POST", b.Base+path, bytes.NewBufferString(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return b.do(req)
}

// File is a file field of a multipart post.
type File struct {
	Field, Name string
	Data        []byte
}

// PostMultipart submits a multipart form with files.
func (b *Browser) PostMultipart(path string, fields map[string]string, files ...File) (int, string) {
	b.T.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if _, ok := fields["csrf"]; !ok {
		mw.WriteField("csrf", b.CSRF)
	}
	for k, v := range fields {
		mw.WriteField(k, v)
	}
	for _, f := range files {
		w, _ := mw.CreateFormFile(f.Field, f.Name)
		w.Write(f.Data)
	}
	mw.Close()
	req, _ := http.NewRequest("POST", b.Base+path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return b.do(req)
}

// MustOK fails the test unless code is 200.
func MustOK(t testing.TB, what string, code int, body string) {
	t.Helper()
	if code != http.StatusOK {
		if len(body) > 3000 {
			body = body[:3000]
		}
		t.Fatalf("%s: HTTP %d\n%s", what, code, body)
	}
}
