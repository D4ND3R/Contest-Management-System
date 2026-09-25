// Package blobserver serves the blob store to workers on other machines
// ("cms blob-server"): GET/HEAD /v1/blobs/{digest} and POST /v1/blobs,
// behind a bearer token. Content is addressed by SHA-256, so a client can
// always check what it reads; nothing can be deleted or overwritten.
package blobserver

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/httpx"
	"github.com/D4ND3R/Contest-Management-System/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

var requests = metrics.NewCounterVec(prometheus.CounterOpts{Name: "cms_blob_server_requests_total",
	Help: "Blob server requests by method and outcome."}, []string{"method", "outcome"})

// Server is the blob server.
type Server struct {
	store     blob.Store
	token     []byte
	maxUpload int64
	log       *slog.Logger
	checks    []httpx.Check
}

// New creates a server; token must not be empty.
func New(store blob.Store, token string, maxUpload int64, log *slog.Logger, checks ...httpx.Check) (*Server, error) {
	if len(token) < 16 {
		return nil, errors.New("blob_server.token must be set (at least 16 characters)")
	}
	if maxUpload <= 0 {
		maxUpload = 1 << 30
	}
	return &Server{store: store, token: []byte(token), maxUpload: maxUpload, log: log, checks: checks}, nil
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", httpx.HealthHandler("blob-server", s.checks...))
	mux.Handle("GET /metrics", metrics.Handler())
	mux.HandleFunc("GET /v1/blobs/{digest}", s.auth(s.get))
	mux.HandleFunc("HEAD /v1/blobs/{digest}", s.auth(s.get))
	mux.HandleFunc("POST /v1/blobs", s.auth(s.put))
	return mux
}

func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		want := append([]byte("Bearer "), s.token...)
		if subtle.ConstantTimeCompare(got, want) != 1 {
			requests.WithLabelValues(r.Method, "unauthorized").Inc()
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	digest := r.PathValue("digest")
	if !blob.ValidDigest(digest) {
		http.Error(w, "invalid digest", http.StatusBadRequest)
		return
	}
	size, err := s.store.Stat(r.Context(), digest)
	if errors.Is(err, blob.ErrNotFound) {
		requests.WithLabelValues(r.Method, "not_found").Inc()
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	// Content-addressed: a digest names the same bytes forever.
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	requests.WithLabelValues(r.Method, "ok").Inc()
	if r.Method == http.MethodHead {
		return
	}
	rc, err := s.store.Open(r.Context(), digest)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer rc.Close()
	if _, err := io.Copy(w, rc); err != nil {
		s.log.Warn("blob download interrupted", "digest", digest, "error", err)
	}
}

func (s *Server) put(w http.ResponseWriter, r *http.Request) {
	body := http.MaxBytesReader(w, r.Body, s.maxUpload)
	info, err := s.store.Put(r.Context(), body)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			requests.WithLabelValues(r.Method, "too_large").Inc()
			http.Error(w, "blob too large", http.StatusRequestEntityTooLarge)
			return
		}
		s.fail(w, r, err)
		return
	}
	requests.WithLabelValues(r.Method, "ok").Inc()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{"digest": info.Digest, "size": info.Size, "created": info.Created})
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	requests.WithLabelValues(r.Method, "error").Inc()
	s.log.Error("blob server", "method", r.Method, "path", r.URL.Path, "error", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// Run serves until ctx ends; ready (optional) receives the bound address.
func (s *Server) Run(ctx context.Context, addr string, ready chan<- net.Addr) error {
	return httpx.Serve(ctx, s.log, addr, s.Handler(), ready)
}
