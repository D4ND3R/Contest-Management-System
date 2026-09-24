package cli

import (
	"context"
	"log/slog"

	"github.com/D4ND3R/Contest-Management-System/internal/app"
	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/deps"
	"github.com/D4ND3R/Contest-Management-System/internal/httpx"
)

func init() {
	services["contest-web"] = stub("contest-web", func(c *config.Config) string { return c.ContestWeb.Listen })
	services["admin-web"] = stub("admin-web", func(c *config.Config) string { return c.AdminWeb.Listen })
	services["ranking-web"] = stub("ranking-web", func(c *config.Config) string { return c.RankingWeb.Listen })
	services["dispatcher"] = stub("dispatcher", func(c *config.Config) string { return c.Dispatcher.MetricsListen })
	services["worker"] = stub("worker", func(c *config.Config) string { return c.Worker.MetricsListen })
	services["monitor"] = stub("monitor", func(c *config.Config) string { return c.Monitor.MetricsListen })
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
