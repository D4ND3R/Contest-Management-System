package webkit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// ContentSecurityPolicy forbids inline scripts and styles and any foreign
// origin: every script is a same-origin file.
const ContentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"connect-src 'self'; font-src 'self'; object-src 'none'; frame-src 'self'; frame-ancestors 'none'; " +
	"form-action 'self'; base-uri 'none'"

// SecurityHeaders adds the standard hardening headers.
func SecurityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Content-Security-Policy", ContentSecurityPolicy)
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Referrer-Policy", "same-origin")
		hd.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		hd.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.ServeHTTP(w, r)
	})
}

// IPResolver extracts the client address, honouring X-Forwarded-For only
// from trusted proxies.
type IPResolver struct{ trusted []netip.Prefix }

// NewIPResolver parses trusted proxy CIDRs.
func NewIPResolver(cidrs []string) (*IPResolver, error) {
	r := &IPResolver{}
	for _, c := range cidrs {
		if !strings.Contains(c, "/") {
			if strings.Contains(c, ":") {
				c += "/128"
			} else {
				c += "/32"
			}
		}
		p, err := netip.ParsePrefix(c)
		if err != nil {
			return nil, fmt.Errorf("trusted proxy %q: %w", c, err)
		}
		r.trusted = append(r.trusted, p)
	}
	return r, nil
}

func (r *IPResolver) isTrusted(a netip.Addr) bool {
	for _, p := range r.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// ClientIP returns the client's address.
func (r *IPResolver) ClientIP(req *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	addr = addr.Unmap()
	if !r.isTrusted(addr) {
		return addr
	}
	// Walk X-Forwarded-For from the right, skipping trusted proxies.
	xff := req.Header.Values("X-Forwarded-For")
	parts := strings.Split(strings.Join(xff, ","), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
		if err != nil {
			break
		}
		a = a.Unmap()
		if !r.isTrusted(a) {
			return a
		}
		addr = a
	}
	return addr
}

// Limiter is a fixed-window rate limiter shared by every web node through
// Redis, with an in-process fallback when Redis is unavailable.
type Limiter struct {
	rdb *redis.Client
	ns  string

	mu    sync.Mutex
	local map[string]*window
}

type window struct {
	start time.Time
	n     int
}

// NewLimiter creates a limiter storing counters under ns.
func NewLimiter(rdb *redis.Client, ns string) *Limiter {
	return &Limiter{rdb: rdb, ns: ns, local: map[string]*window{}}
}

// Allow counts one event for key and reports whether it stays within limit
// events per period.
func (l *Limiter) Allow(ctx context.Context, key string, limit int, period time.Duration) bool {
	if limit <= 0 {
		return true
	}
	slot := time.Now().UnixNano() / int64(period)
	k := fmt.Sprintf("%srl:%s:%d", l.ns, key, slot)
	if l.rdb != nil {
		pipe := l.rdb.Pipeline()
		incr := pipe.Incr(ctx, k)
		pipe.Expire(ctx, k, period+time.Second)
		if _, err := pipe.Exec(ctx); err == nil {
			return incr.Val() <= int64(limit)
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	w, ok := l.local[key]
	now := time.Now()
	if !ok || now.Sub(w.start) >= period {
		if len(l.local) > 100000 {
			l.local = map[string]*window{}
		}
		w = &window{start: now}
		l.local[key] = w
	}
	w.n++
	return w.n <= limit
}

// Over reports whether key already reached limit events in the current
// period, without counting one (see Hit): failed logins are counted, and a
// successful one never uses up the budget of a shared address.
func (l *Limiter) Over(ctx context.Context, key string, limit int, period time.Duration) bool {
	if limit <= 0 {
		return false
	}
	slot := time.Now().UnixNano() / int64(period)
	if l.rdb != nil {
		n, err := l.rdb.Get(ctx, fmt.Sprintf("%srl:%s:%d", l.ns, key, slot)).Int64()
		if err == nil || errors.Is(err, redis.Nil) {
			return n >= int64(limit)
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	w, ok := l.local[key]
	return ok && time.Since(w.start) < period && w.n >= limit
}

// Hit counts one event for key (see Over).
func (l *Limiter) Hit(ctx context.Context, key string, period time.Duration) {
	l.Allow(ctx, key, math.MaxInt, period)
}

var bufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

// Render executes a template into a pooled buffer and writes it with the
// given status, so template errors never produce half-written pages.
func Render(w http.ResponseWriter, log *slog.Logger, t *template.Template, name string, status int, data any) {
	buf := bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bufPool.Put(buf)
	if err := t.ExecuteTemplate(buf, name, data); err != nil {
		log.Error("render template", "template", name, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	if h.Get("Content-Type") == "" {
		h.Set("Content-Type", "text/html; charset=utf-8")
	}
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	io.Copy(w, buf)
}

// IsHTMX reports whether the request comes from htmx.
func IsHTMX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

// Redirect sends a browser (or htmx) to url after a POST.
func Redirect(w http.ResponseWriter, r *http.Request, url string) {
	if IsHTMX(r) {
		w.Header().Set("HX-Redirect", url)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, url, http.StatusSeeOther)
}
