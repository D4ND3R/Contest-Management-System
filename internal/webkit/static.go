package webkit

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

// Static serves embedded assets under a prefix with content-hash cache
// busting (URLs carry ?v=<hash>), year-long immutable caching and
// precompressed gzip bodies computed once at startup.
type Static struct {
	prefix string
	files  map[string]*asset
}

type asset struct {
	body, gz []byte
	ctype    string
	hash     string
}

// NewStatic indexes every file of fsys (served as prefix + name).
func NewStatic(fsys fs.FS, prefix string) (*Static, error) {
	s := &Static{prefix: strings.TrimSuffix(prefix, "/") + "/", files: map[string]*asset{}}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		a := &asset{body: body, hash: hex.EncodeToString(sum[:6])}
		a.ctype = mime.TypeByExtension(path.Ext(p))
		if a.ctype == "" {
			a.ctype = "application/octet-stream"
		}
		if compressible(a.ctype) {
			var buf bytes.Buffer
			zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
			zw.Write(body)
			zw.Close()
			if buf.Len() < len(body) {
				a.gz = buf.Bytes()
			}
		}
		s.files[p] = a
		return nil
	})
	return s, err
}

func compressible(ct string) bool {
	return strings.HasPrefix(ct, "text/") || strings.Contains(ct, "javascript") || strings.Contains(ct, "json") || strings.Contains(ct, "svg")
}

// URL returns the versioned URL of an asset (for templates).
func (s *Static) URL(name string) string {
	a, ok := s.files[name]
	if !ok {
		return s.prefix + name
	}
	return s.prefix + name + "?v=" + a.hash
}

// GzipSize returns the transfer size of an asset (for the JS budget test).
func (s *Static) GzipSize(name string) int {
	a, ok := s.files[name]
	if !ok {
		return -1
	}
	if a.gz != nil {
		return len(a.gz)
	}
	return len(a.body)
}

// ServeHTTP serves an asset.
func (s *Static) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, s.prefix)
	a, ok := s.files[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", a.ctype)
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("ETag", `"`+a.hash+`"`)
	h.Set("Vary", "Accept-Encoding")
	if r.Header.Get("If-None-Match") == `"`+a.hash+`"` {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	body := a.body
	if a.gz != nil && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		h.Set("Content-Encoding", "gzip")
		body = a.gz
	}
	w.Write(body)
}
