package httpx

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/metrics"
	"github.com/D4ND3R/Contest-Management-System/internal/version"
)

// Check is a named health probe (database ping, redis ping, ...).
type Check struct {
	Name string
	Fn   func(ctx context.Context) error
}

// HealthHandler returns 200 when every check passes and 503 otherwise, with
// a small JSON body describing each check. Checks run concurrently with a
// 2 second budget.
func HealthHandler(service string, checks ...Check) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		type result struct {
			Status string `json:"status"`
			Error  string `json:"error,omitempty"`
		}
		res := make(map[string]result, len(checks))
		var mu sync.Mutex
		var wg sync.WaitGroup
		ok := true
		for _, c := range checks {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := c.Fn(ctx)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					ok = false
					res[c.Name] = result{Status: "fail", Error: err.Error()}
				} else {
					res[c.Name] = result{Status: "ok"}
				}
			}()
		}
		wg.Wait()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		status := "ok"
		if !ok {
			status = "fail"
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"service": service, "status": status, "version": version.String(), "checks": res,
		})
	})
}

// OpsMux returns a mux exposing /healthz and /metrics, used as the whole
// HTTP surface of non-web services (dispatcher, worker, monitor, printing).
func OpsMux(service string, checks ...Check) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", HealthHandler(service, checks...))
	mux.Handle("GET /metrics", metrics.Handler())
	return mux
}
