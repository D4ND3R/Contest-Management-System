package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/backup"
	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/deps"
	"github.com/D4ND3R/Contest-Management-System/internal/logging"
	"github.com/D4ND3R/Contest-Management-System/internal/upgrade"
	"github.com/D4ND3R/Contest-Management-System/internal/version"
)

func init() {
	ctlCommands["upgrade"] = ctlCommand{"install another release: backup, migrations, restart; back to the previous one if anything fails", cmdUpgrade}
}

func cmdUpgrade(args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("upgrade", stderr)
	ver := fs.String("version", "latest", "release to install: X.Y.Z or latest")
	archive := fs.String("archive", "", "install this release tarball (with checksums.txt next to it) instead of downloading")
	force := fs.Bool("force", false, "upgrade even while a contest is in progress (the services stop for a minute)")
	reinstall := fs.Bool("reinstall", false, "install the release even if it is the one running")
	root := fs.String("root", "/opt/cms", "installation root (releases/ and current)")
	releaseURL := fs.String("release-url", "https://github.com/D4ND3R/Contest-Management-System/releases", "where releases are published")
	systemctl := fs.String("systemctl", "systemctl", "service manager command")
	wait := fs.Duration("health-timeout", 2*time.Minute, "how long the services have to answer after starting")
	role := fs.String("role", "auto", "main (with the database) or worker (a remote worker: no contest check, backup or migrations); auto: worker when blob.backend is http")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	switch *role {
	case "auto":
		// Only remote workers read the blobs over HTTP from the main server.
		if cfg.Blob.Backend == "http" {
			*role = "worker"
		} else {
			*role = "main"
		}
	case "main", "worker":
	default:
		return fmt.Errorf("-role must be auto, main or worker")
	}
	sys := func(ctx context.Context, args ...string) error {
		out, err := exec.CommandContext(ctx, *systemctl, args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s %s: %v %s", *systemctl, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	steps := upgrade.Steps{
		Stop:  func(ctx context.Context) error { return sys(ctx, "stop", "cms.target") },
		Start: func(ctx context.Context) error { return sys(ctx, "start", "cms.target") },
		Health: func(ctx context.Context) error {
			var urls []string
			for svc, listen := range healthAddrs(cfg) {
				// Only the services this machine runs.
				if exec.CommandContext(ctx, *systemctl, "is-enabled", "--quiet", "cms-"+svc+".service").Run() == nil {
					urls = append(urls, healthURL(listen))
				}
			}
			return waitHealthy(ctx, urls, *wait)
		},
	}
	if *role == "main" {
		d, err := deps.Open(ctx, cfg, logging.New(stderr, "cmsctl", "warn", "text"), deps.Need{DB: true, Blobs: true})
		if err != nil {
			return err
		}
		defer d.Close()
		addDatabaseSteps(&steps, cfg, d, *cfgPath, stdout, stderr)
	}
	return upgrade.Run(ctx, upgrade.Options{
		Root: *root, ReleaseURL: strings.TrimSuffix(*releaseURL, "/"), Version: *ver, Archive: *archive,
		Arch: runtime.GOARCH, Current: version.Version, Force: *force, Reinstall: *reinstall, Out: stdout,
	}, steps)
}

// addDatabaseSteps adds what a machine with the database does around an
// upgrade: the contest check, the backup (and its restore on rollback) and
// the migrations.
func addDatabaseSteps(s *upgrade.Steps, cfg *config.Config, d *deps.Deps, cfgPath string, stdout, stderr io.Writer) {
	q := sqlc.New(d.DB)
	s.Running = func(ctx context.Context) ([]string, error) {
		rows, err := q.ListRunningContests(ctx)
		var out []string
		for _, r := range rows {
			out = append(out, fmt.Sprintf("%s until %s", r.Name, r.EndsAt.UTC().Format("2006-01-02 15:04 MST")))
		}
		return out, err
	}
	s.Backup = func(ctx context.Context) (string, error) {
		r := backup.NewRunner(d.DB, d.Blobs, cfg.Backup, nil, nil, logging.Discard())
		e, err := r.Backup(ctx, backup.KindUpgrade, "cmsctl upgrade")
		if err != nil {
			return "", err
		}
		return r.Path(e.Name)
	}
	s.Restore = func(ctx context.Context, p string) error {
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = backup.Restore(ctx, d.DB, d.Blobs, bufio.NewReaderSize(f, 1<<20), backup.RestoreOptions{Force: true})
		return err
	}
	s.Migrate = func(ctx context.Context, bin string) error {
		cmd := exec.CommandContext(ctx, bin, "migrate")
		cmd.Env = os.Environ()
		if cfgPath != "" {
			cmd.Env = append(cmd.Env, "CMS_CONFIG="+cfgPath)
		}
		cmd.Stdout, cmd.Stderr = stdout, stderr
		return cmd.Run()
	}
}

// healthAddrs maps each service to the address that answers /healthz.
func healthAddrs(cfg *config.Config) map[string]string {
	return map[string]string{
		"contest-web": cfg.ContestWeb.Listen, "admin-web": cfg.AdminWeb.Listen, "ranking-web": cfg.RankingWeb.Listen,
		"dispatcher": cfg.Dispatcher.MetricsListen, "worker": cfg.Worker.MetricsListen, "monitor": cfg.Monitor.MetricsListen,
		"printing": cfg.Printing.MetricsListen, "blob-server": cfg.BlobServer.Listen,
	}
}

// healthURL turns a listen address (":8888", "0.0.0.0:8888", "10.0.0.1:8891")
// into the /healthz URL to ask on this machine.
func healthURL(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "http://" + listen + "/healthz"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/healthz"
}

// waitHealthy polls every URL until all answer 200 or the time is up.
func waitHealthy(ctx context.Context, urls []string, timeout time.Duration) error {
	c := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(timeout)
	for {
		var failing []string
		for _, u := range urls {
			resp, err := c.Get(u)
			if err == nil {
				resp.Body.Close()
			}
			if err != nil || resp.StatusCode != http.StatusOK {
				failing = append(failing, u)
			}
		}
		if len(failing) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("not healthy after %s: %s", timeout, strings.Join(failing, ", "))
		}
		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), fmt.Errorf("not healthy: %s", strings.Join(failing, ", ")))
		case <-time.After(time.Second):
		}
	}
}
