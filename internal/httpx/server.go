// Package httpx contains the HTTP plumbing shared by all web services:
// graceful server lifecycle, health checks, metrics and middleware.
package httpx

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Serve runs srv on addr until ctx is cancelled, then shuts it down
// gracefully (in-flight requests get up to 10 seconds). If ready is non-nil
// it receives the bound address once the listener is open.
func Serve(ctx context.Context, log *slog.Logger, addr string, h http.Handler, ready chan<- net.Addr) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return ServeListener(ctx, log, ln, h, ready)
}

// ServeListener is Serve with a pre-opened listener.
func ServeListener(ctx context.Context, log *slog.Logger, ln net.Listener, h http.Handler, ready chan<- net.Addr) error {
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelDebug),
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	log.Info("http server listening", "addr", ln.Addr().String())
	if ready != nil {
		ready <- ln.Addr()
	}
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := srv.Shutdown(shutdownCtx)
	if errors.Is(err, context.DeadlineExceeded) {
		err = srv.Close()
	}
	if e := <-errc; e != nil && !errors.Is(e, http.ErrServerClosed) {
		return e
	}
	return err
}
