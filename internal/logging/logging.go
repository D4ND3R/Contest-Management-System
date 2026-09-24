// Package logging configures structured logging (log/slog) for every service.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// New returns a logger that writes to w (stderr when nil) in the given
// format ("json" or "text") and tags every record with the service name.
func New(w io.Writer, service, level, format string) *slog.Logger {
	if w == nil {
		w = os.Stderr
	}
	opts := &slog.HandlerOptions{Level: ParseLevel(level)}
	var h slog.Handler
	if strings.EqualFold(format, "text") {
		h = slog.NewTextHandler(w, opts)
	} else {
		h = slog.NewJSONHandler(w, opts)
	}
	l := slog.New(h)
	if service != "" {
		l = l.With("service", service)
	}
	return l
}

// ParseLevel maps a level name to a slog.Level (info by default).
func ParseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Discard returns a logger that drops everything (useful in tests and benchmarks).
func Discard() *slog.Logger { return slog.New(slog.DiscardHandler) }
