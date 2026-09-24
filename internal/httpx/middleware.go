package httpx

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	reqDuration = metrics.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cms_http_request_duration_seconds",
		Help:    "HTTP request latency by service, route pattern and status class.",
		Buckets: metrics.LatencyBuckets,
	}, []string{"service", "route", "code"})
)

// statusWriter records the status code and size of a response. It forwards
// Flush so that SSE handlers keep working behind the middleware.
type statusWriter struct {
	http.ResponseWriter
	status int
	size   int64
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.size += int64(n)
	return n, err
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Observe wraps h with panic recovery, latency metrics and access logging.
// Successful requests are logged at debug level to keep the hot path cheap;
// 5xx responses and slow requests (>250ms) are logged at warn.
func Observe(service string, log *slog.Logger, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		defer func() {
			if p := recover(); p != nil {
				if p == http.ErrAbortHandler {
					panic(p)
				}
				log.Error("panic serving request", "path", r.URL.Path, "panic", p, "stack", string(debug.Stack()))
				if sw.status == 0 {
					http.Error(sw, "internal server error", http.StatusInternalServerError)
				}
			}
			d := time.Since(start)
			if sw.status == 0 {
				sw.status = http.StatusOK
			}
			route := r.Pattern
			if route == "" {
				route = "unmatched"
			}
			reqDuration.WithLabelValues(service, route, strconv.Itoa(sw.status/100)+"xx").Observe(d.Seconds())
			lvl := slog.LevelDebug
			if sw.status >= 500 || d > 250*time.Millisecond {
				lvl = slog.LevelWarn
			}
			if log.Enabled(r.Context(), lvl) {
				log.LogAttrs(r.Context(), lvl, "http request",
					slog.String("method", r.Method), slog.String("path", r.URL.Path),
					slog.Int("status", sw.status), slog.Int64("bytes", sw.size),
					slog.Duration("duration", d), slog.String("remote", r.RemoteAddr))
			}
		}()
		h.ServeHTTP(sw, r)
	})
}
