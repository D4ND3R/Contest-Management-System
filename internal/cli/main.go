// Package cli implements the "cms" multi-service binary and the "cmsctl"
// administrative commands.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"

	"github.com/D4ND3R/Contest-Management-System/internal/app"
	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/logging"
	"github.com/D4ND3R/Contest-Management-System/internal/version"
)

// ServiceFunc runs a long-lived service until ctx is cancelled.
type ServiceFunc func(ctx context.Context, cfg *config.Config, log *slog.Logger) error

// services maps subcommand names to their entry points (see services.go).
var services = map[string]ServiceFunc{}

func usage(w io.Writer) {
	names := make([]string, 0, len(services))
	for n := range services {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Fprintf(w, "usage: cms <command> [-config file] [args]\n\nservices:\n")
	for _, n := range names {
		fmt.Fprintf(w, "  %s\n", n)
	}
	fmt.Fprintf(w, "\nother commands:\n  ctl        administrative commands (cms ctl help)\n  migrate    apply database migrations\n  version    print the version\n")
}

// Main is the entry point shared by cmd/cms and cmd/cmsctl. It returns the
// process exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "version", "-version", "--version":
		fmt.Fprintln(stdout, "cms", version.String())
		return 0
	case "help", "-h", "-help", "--help":
		usage(stdout)
		return 0
	case "ctl":
		return ctlMain(rest, stdout, stderr)
	case "migrate":
		return ctlMain(append([]string{"migrate"}, rest...), stdout, stderr)
	}
	run, ok := services[cmd]
	if !ok {
		fmt.Fprintf(stderr, "unknown command %q\n\n", cmd)
		usage(stderr)
		return 2
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "configuration file (default $CMS_CONFIG)")
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "config:", err)
		return 1
	}
	log := logging.New(stderr, cmd, cfg.Log.Level, cfg.Log.Format)
	slog.SetDefault(log)
	ctx, stop := app.SignalContext()
	defer stop()
	log.Info("starting", "version", version.String(), "config", cfg.Path)
	if err := run(ctx, cfg, log); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("service failed", "error", err)
		return 1
	}
	log.Info("stopped")
	return 0
}

// splitCSV splits a comma-separated flag value, dropping blanks.
func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
