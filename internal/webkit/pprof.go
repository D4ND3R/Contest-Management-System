package webkit

import (
	"net"
	"net/http"
	"net/http/pprof"
	"net/netip"
)

// Profiling mounts the Go profiler under /debug/pprof/ on mux (the web
// servers' `pprof` option, for profiling under load). It answers only
// requests made on the machine itself and never through a reverse proxy:
// a request with forwarding headers gets 404 like any unknown path.
func Profiling(mux *http.ServeMux) {
	local := func(h http.HandlerFunc) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host, _, _ := net.SplitHostPort(r.RemoteAddr)
			ip, err := netip.ParseAddr(host)
			if err != nil || !ip.Unmap().IsLoopback() ||
				r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("Forwarded") != "" || r.Header.Get("X-Real-IP") != "" {
				http.NotFound(w, r)
				return
			}
			h(w, r)
		})
	}
	mux.Handle("GET /debug/pprof/", local(pprof.Index))
	mux.Handle("GET /debug/pprof/cmdline", local(pprof.Cmdline))
	mux.Handle("GET /debug/pprof/profile", local(pprof.Profile))
	mux.Handle("GET /debug/pprof/symbol", local(pprof.Symbol))
	mux.Handle("GET /debug/pprof/trace", local(pprof.Trace))
}
