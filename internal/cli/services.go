package cli

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
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
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/monitor"
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
	services["printing"] = stub("printing", func(c *config.Config) string { return c.Printing.MetricsListen })
}

// stub is a placeholder service exposing /healthz and /metrics; each one is
// replaced by the real implementation in its phase.
func stub(name string, listen func(*config.Config) string) ServiceFunc {
	return func(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
		d, err := deps.Open(ctx, cfg, log, deps.Need{DB: true, Redis: true})
		if err != nil {
			return err
		}
		defer d.Close()
		g, ctx := app.NewGroup(ctx)
		g.Go(func(ctx context.Context) error {
			return httpx.Serve(ctx, log, listen(cfg), httpx.OpsMux(name, d.Checks()...), nil)
		})
		return g.Wait()
	}
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

func runContestWeb(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
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
		Pool: d.DB, Redis: d.Redis, Blobs: d.Blobs, Langs: reg, Secret: cfg.Secret(), NS: cfg.Redis.Namespace, Checks: d.Checks(),
		ContestListen: cfg.ContestWeb.Listen, RankingURL: cfg.RankingWeb.PublicURL, Backups: backups,
	}, log)
	if err != nil {
		return err
	}
	g, ctx := app.NewGroup(ctx)
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
	g, ctx := app.NewGroup(ctx)
	g.Go(disp.Run)
	// The ranking pusher feeds the ranking web servers (when configured).
	pusher := rankingpush.New(d.DB, d.Redis, d.Blobs, log, rankingpush.Options{URLs: cfg.Dispatcher.RankingURLs,
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
	g, ctx := app.NewGroup(ctx)
	g.Go(svc.Run)
	g.Go(func(ctx context.Context) error {
		return httpx.Serve(ctx, log, cfg.Worker.MetricsListen, httpx.OpsMux("worker", d.Checks()...), nil)
	})
	return g.Wait()
}

func runMonitor(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	d, err := deps.Open(ctx, cfg, log, deps.Need{Redis: true})
	if err != nil {
		return err
	}
	defer d.Close()
	m := monitor.New(queue.New(d.Redis, cfg.Redis.Namespace), log, monitor.Options{
		CheckInterval: cfg.Monitor.CheckInterval.D(), JobTimeout: cfg.Monitor.JobTimeout.D(),
		DeadGrace: time.Second, MaxAttempts: cfg.Dispatcher.MaxAttempts,
	})
	g, ctx := app.NewGroup(ctx)
	g.Go(m.Run)
	g.Go(func(ctx context.Context) error {
		return httpx.Serve(ctx, log, cfg.Monitor.MetricsListen, httpx.OpsMux("monitor", d.Checks()...), nil)
	})
	return g.Wait()
}

// runRankingWeb serves the public scoreboards; it needs no database.
func runRankingWeb(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
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
