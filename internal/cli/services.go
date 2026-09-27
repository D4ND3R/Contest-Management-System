package cli

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/D4ND3R/Contest-Management-System/internal/alerts"
	"github.com/D4ND3R/Contest-Management-System/internal/hoststat"
	"log/slog"
	"os"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/adminweb"
	"github.com/D4ND3R/Contest-Management-System/internal/app"
	"github.com/D4ND3R/Contest-Management-System/internal/backup"
	"github.com/D4ND3R/Contest-Management-System/internal/blobserver"
	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/contestweb"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/deps"
	"github.com/D4ND3R/Contest-Management-System/internal/dispatcher"
	"github.com/D4ND3R/Contest-Management-System/internal/events"
	"github.com/D4ND3R/Contest-Management-System/internal/httpx"
	"github.com/D4ND3R/Contest-Management-System/internal/i18n"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/monitor"
	"github.com/D4ND3R/Contest-Management-System/internal/printing"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/D4ND3R/Contest-Management-System/internal/rankingpush"
	"github.com/D4ND3R/Contest-Management-System/internal/rankingweb"
	"github.com/D4ND3R/Contest-Management-System/internal/worker"
)

func init() {
	services["contest-web"] = runContestWeb
	services["admin-web"] = runAdminWeb
	services["ranking-web"] = runRankingWeb
	services["dispatcher"] = runDispatcher
	services["worker"] = runWorker
	services["monitor"] = runMonitor
	services["blob-server"] = runBlobServer
	services["printing"] = runPrinting
}

// runPrinting sends queued print jobs to CUPS.
func runPrinting(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	d, err := deps.Open(ctx, cfg, log, deps.Need{DB: true, Redis: true, Blobs: true})
	if err != nil {
		return err
	}
	defer d.Close()
	svc := printing.New(sqlc.New(d.DB), d.Redis, cfg.Redis.Namespace, d.Blobs, cfg.Printing, log)
	g, _ := app.NewGroup(ctx)
	g.Go(svc.Run)
	g.Go(func(ctx context.Context) error {
		return httpx.Serve(ctx, log, cfg.Printing.MetricsListen, httpx.OpsMux("printing", d.Checks()...), nil)
	})
	return g.Wait()
}

// loadLanguages reads the language definitions and mirrors them in the
// languages table (for the admin UI and exports).
func loadLanguages(ctx context.Context, cfg *config.Config, q *sqlc.Queries) (*langs.Registry, error) {
	reg, err := langs.Load(cfg.LanguagesDir)
	if err != nil {
		return nil, err
	}
	if q != nil {
		for _, l := range reg.All() {
			data, _ := json.Marshal(l)
			if err := q.UpsertLanguage(ctx, sqlc.UpsertLanguageParams{ID: l.ID, Name: l.Name, Config: data}); err != nil {
				return nil, err
			}
		}
	}
	return reg, nil
}

// loadLocales registers the installation's interface languages (before
// any request is served).
func loadLocales(cfg *config.Config, log *slog.Logger) error {
	if cfg.LocalesDir == "" {
		return nil
	}
	warnings, err := i18n.LoadDir(cfg.LocalesDir)
	for _, w := range warnings {
		log.Warn("locale", "problem", w)
	}
	return err
}

func runContestWeb(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	if err := loadLocales(cfg, log); err != nil {
		return err
	}
	d, err := deps.Open(ctx, cfg, log, deps.Need{DB: true, Redis: true, Blobs: true})
	if err != nil {
		return err
	}
	defer d.Close()
	reg, err := langs.Load(cfg.LanguagesDir)
	if err != nil {
		return err
	}
	srv, err := contestweb.New(cfg.ContestWeb, contestweb.Deps{
		Pool: d.DB, Redis: d.Redis, Blobs: d.Blobs, Langs: reg, Secret: cfg.Secret(), NS: cfg.Redis.Namespace, Checks: d.Checks(),
	}, log)
	if err != nil {
		return err
	}
	return srv.Run(ctx, cfg.ContestWeb.Listen, nil)
}

func runAdminWeb(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	if err := loadLocales(cfg, log); err != nil {
		return err
	}
	d, err := deps.Open(ctx, cfg, log, deps.Need{DB: true, Redis: true, Blobs: true})
	if err != nil {
		return err
	}
	defer d.Close()
	reg, err := langs.Load(cfg.LanguagesDir)
	if err != nil {
		return err
	}
	// Backups (scheduled and "Back up now") run here, one at a time across
	// admin servers thanks to a Redis lease.
	remote, err := backup.NewS3Remote(cfg.Backup.S3)
	if err != nil {
		return err
	}
	backups := backup.NewRunner(d.DB, d.Blobs, cfg.Backup, queue.New(d.Redis, cfg.Redis.Namespace), remote, log)
	backups.Alert = func(ctx context.Context, msg string) {
		_ = events.Publish(ctx, d.Redis, cfg.Redis.Namespace, events.Event{Type: events.TypeAlert, Text: msg})
	}
	srv, err := adminweb.New(cfg.AdminWeb, adminweb.Deps{
		Pool: d.DB, ReadPool: d.ReadDB, Redis: d.Redis, Blobs: d.Blobs, Langs: reg, Secret: cfg.Secret(), NS: cfg.Redis.Namespace, Checks: d.Checks(),
		ContestListen: cfg.ContestWeb.Listen, RankingURL: cfg.RankingWeb.PublicURL, Backups: backups,
		Dirs: [][2]string{{"blobs", localBlobDir(cfg)}, {"backups", cfg.Backup.Dir}, {"temporary files", os.TempDir()}},
	}, log)
	if err != nil {
		return err
	}
	g, _ := app.NewGroup(ctx)
	g.Go(backups.Run)
	g.Go(func(ctx context.Context) error { return srv.Run(ctx, cfg.AdminWeb.Listen, nil) })
	return g.Wait()
}

func runDispatcher(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	d, err := deps.Open(ctx, cfg, log, deps.Need{DB: true, Redis: true, Blobs: len(cfg.Dispatcher.RankingURLs) > 0})
	if err != nil {
		return err
	}
	defer d.Close()
	reg, err := loadLanguages(ctx, cfg, sqlc.New(d.DB))
	if err != nil {
		return err
	}
	disp := dispatcher.New(d.DB, d.Redis, reg, log, dispatcher.Options{
		Namespace: cfg.Redis.Namespace, SweepInterval: cfg.Dispatcher.SweepInterval.D(),
		MaxAttempts: cfg.Dispatcher.MaxAttempts, TestcasesPerJob: cfg.Dispatcher.TestcasesPerJob,
	})
	g, _ := app.NewGroup(ctx)
	g.Go(disp.Run)
	// The ranking pusher feeds the ranking web servers (when configured).
	// The scoreboards read from the replica when there is one.
	pusher := rankingpush.New(d.ReadDB, d.Redis, d.Blobs, log, rankingpush.Options{URLs: cfg.Dispatcher.RankingURLs,
		Token: cfg.RankingWeb.PushToken, Secret: cfg.Secret(), Namespace: cfg.Redis.Namespace})
	g.Go(pusher.Run)
	g.Go(func(ctx context.Context) error {
		return httpx.Serve(ctx, log, cfg.Dispatcher.MetricsListen, httpx.OpsMux("dispatcher", d.Checks()...), nil)
	})
	return g.Wait()
}

func runWorker(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	// Workers never connect to PostgreSQL: jobs are self-contained.
	d, err := deps.Open(ctx, cfg, log, deps.Need{Redis: true, Blobs: true})
	if err != nil {
		return err
	}
	defer d.Close()
	svc, err := worker.NewService(cfg.Worker, d.Blobs, queue.New(d.Redis, cfg.Redis.Namespace), log)
	if err != nil {
		return err
	}
	g, _ := app.NewGroup(ctx)
	g.Go(svc.Run)
	g.Go(func(ctx context.Context) error {
		return httpx.Serve(ctx, log, cfg.Worker.MetricsListen, httpx.OpsMux("worker", d.Checks()...), nil)
	})
	return g.Wait()
}

func runMonitor(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	d, err := deps.Open(ctx, cfg, log, deps.Need{Redis: true, DB: true})
	if err != nil {
		return err
	}
	defer d.Close()
	q := queue.New(d.Redis, cfg.Redis.Namespace)
	// Health rules (SPEC_IOI §12): the disks of this machine are those of
	// the main server's data.
	rules := alerts.Standard(alerts.Sources{Queue: q, Pool: d.DB, Host: &hoststat.Sampler{},
		Dirs: [][2]string{{"blobs", localBlobDir(cfg)}, {"backups", cfg.Backup.Dir}}}, cfg.Monitor.Alerts)
	notify := func(ctx context.Context, text string) {
		_ = events.Publish(ctx, d.Redis, cfg.Redis.Namespace, events.Event{Type: events.TypeAlert, Text: text})
	}
	m := monitor.New(q, log, monitor.Options{
		CheckInterval: cfg.Monitor.CheckInterval.D(), JobTimeout: cfg.Monitor.JobTimeout.D(),
		DeadGrace: time.Second, MaxAttempts: cfg.Dispatcher.MaxAttempts,
		Alerts: alerts.New(d.Redis, monitor.AlertsKey(q), rules, notify, cfg.Monitor.Alerts.WebhookURL, log),
	})
	g, _ := app.NewGroup(ctx)
	g.Go(m.Run)
	g.Go(func(ctx context.Context) error {
		return httpx.Serve(ctx, log, cfg.Monitor.MetricsListen, httpx.OpsMux("monitor", d.Checks()...), nil)
	})
	return g.Wait()
}

// runRankingWeb serves the public scoreboards; it needs no database.
func runRankingWeb(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	if err := loadLocales(cfg, log); err != nil {
		return err
	}
	srv, err := rankingweb.New(cfg.RankingWeb, log)
	if err != nil {
		return err
	}
	return srv.Run(ctx, cfg.RankingWeb.Listen, nil)
}

// runBlobServer serves the blob store to workers on other machines.
func runBlobServer(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	if cfg.Blob.Backend == "http" {
		return errors.New("the blob server needs the local or s3 blob backend")
	}
	d, err := deps.Open(ctx, cfg, log, deps.Need{Blobs: true})
	if err != nil {
		return err
	}
	defer d.Close()
	srv, err := blobserver.New(d.Blobs, cfg.BlobServer.Token, int64(cfg.BlobServer.MaxUploadBytes), log)
	if err != nil {
		return err
	}
	return srv.Run(ctx, cfg.BlobServer.Listen, nil)
}

// localBlobDir is the blob directory when blobs are stored on this machine.
func localBlobDir(cfg *config.Config) string {
	if cfg.Blob.Backend == "local" {
		return cfg.Blob.LocalDir
	}
	return ""
}
