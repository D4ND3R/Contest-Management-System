// Package deps opens the shared infrastructure a service needs (database,
// Redis, blob store) from the configuration and closes it on shutdown.
package deps

import (
	"context"
	"errors"
	"log/slog"

	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/httpx"
	"github.com/D4ND3R/Contest-Management-System/internal/redisx"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Need selects which dependencies to open.
type Need struct {
	DB    bool
	Redis bool
}

// Deps bundles opened dependencies. Fields for unrequested dependencies are nil.
type Deps struct {
	Cfg   *config.Config
	Log   *slog.Logger
	DB    *pgxpool.Pool
	Redis *redis.Client
}

// Open connects to the requested dependencies.
func Open(ctx context.Context, cfg *config.Config, log *slog.Logger, need Need) (*Deps, error) {
	d := &Deps{Cfg: cfg, Log: log}
	if need.DB {
		pool, err := db.Open(ctx, cfg.Database.URL, cfg.Database.MaxConns)
		if err != nil {
			return nil, err
		}
		d.DB = pool
	}
	if need.Redis {
		rc, err := redisx.Open(ctx, cfg.Redis.URL, cfg.Redis.PoolSize)
		if err != nil {
			d.Close()
			return nil, err
		}
		d.Redis = rc
	}
	return d, nil
}

// Checks returns health checks for the opened dependencies.
func (d *Deps) Checks() []httpx.Check {
	var cs []httpx.Check
	if d.DB != nil {
		cs = append(cs, httpx.Check{Name: "database", Fn: d.DB.Ping})
	}
	if d.Redis != nil {
		cs = append(cs, httpx.Check{Name: "redis", Fn: func(ctx context.Context) error { return d.Redis.Ping(ctx).Err() }})
	}
	return cs
}

// Close releases every opened dependency.
func (d *Deps) Close() error {
	var errs []error
	if d.DB != nil {
		d.DB.Close()
	}
	if d.Redis != nil {
		errs = append(errs, d.Redis.Close())
	}
	return errors.Join(errs...)
}
