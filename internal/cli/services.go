package cli

import (
	"context"
	"log/slog"
	"time"

	"encoding/json"
	"github.com/D4ND3R/Contest-Management-System/internal/app"
	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/deps"
	"github.com/D4ND3R/Contest-Management-System/internal/dispatcher"
	"github.com/D4ND3R/Contest-Management-System/internal/httpx"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/monitor"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/D4ND3R/Contest-Management-System/internal/worker"
)

func init() {
	services["contest-web"] = stub("contest-web", func(c *config.Config) string { return c.ContestWeb.Listen })
	services["admin-web"] = stub("admin-web", func(c *config.Config) string { return c.AdminWeb.Listen })
	services["ranking-web"] = stub("ranking-web", func(c *config.Config) string { return c.RankingWeb.Listen })
	services["dispatcher"] = runDispatcher
	services["worker"] = runWorker
	services["monitor"] = runMonitor
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

func runDispatcher(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	d, err := deps.Open(ctx, cfg, log, deps.Need{DB: true, Redis: true})
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
