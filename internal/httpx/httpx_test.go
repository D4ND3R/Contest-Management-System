package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/logging"
)

func TestHealthHandler(t *testing.T) {
	ok := Check{Name: "a", Fn: func(context.Context) error { return nil }}
	bad := Check{Name: "b", Fn: func(context.Context) error { return errors.New("down") }}

	rec := httptest.NewRecorder()
	HealthHandler("svc", ok).ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 {
		t.Fatalf("healthy code = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	HealthHandler("svc", ok, bad).ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 503 {
		t.Fatalf("unhealthy code = %d", rec.Code)
	}
	var body struct {
		Status string                       `json:"status"`
		Checks map[string]map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "fail" || body.Checks["b"]["error"] != "down" || body.Checks["a"]["status"] != "ok" {
		t.Fatalf("body = %+v", body)
	}
}

func TestObserveRecoversPanics(t *testing.T) {
	h := Observe("test", logging.Discard(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 500 {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestObservePreservesFlusher(t *testing.T) {
	var flushed bool
	h := Observe("test", logging.Discard(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("flush: %v", err)
		}
		flushed = true
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if !flushed {
		t.Fatal("handler did not run")
	}
}

func TestServeGracefulShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan net.Addr, 1)
	errc := make(chan error, 1)
	go func() {
		errc <- Serve(ctx, logging.Discard(), "127.0.0.1:0", OpsMux("svc"), ready)
	}()
	addr := <-ready
	resp, err := http.Get("http://" + addr.String() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("healthz = %d", resp.StatusCode)
	}
	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop")
	}
}
