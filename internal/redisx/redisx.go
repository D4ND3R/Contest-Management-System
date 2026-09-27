// Package redisx opens the Redis (or Valkey) client used for queues,
// pub/sub, rate limiting and caching.
package redisx

import (
	"context"
	"fmt"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/redis/go-redis/v9"
)

// Open connects to c.URL (redis://[:password@]host:port/db), or through
// c.Sentinels to whichever server is the primary of c.SentinelMaster, and
// pings it.
func Open(ctx context.Context, c config.Redis) (*redis.Client, error) {
	opt, err := redis.ParseURL(c.URL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	if c.PoolSize > 0 {
		opt.PoolSize = c.PoolSize
	}
	opt.ReadTimeout = 5 * time.Second
	opt.WriteTimeout = 5 * time.Second
	// Blocking stream reads set their own deadlines; see queue package.
	var client *redis.Client
	if len(c.Sentinels) > 0 {
		client = redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName: c.SentinelMaster, SentinelAddrs: c.Sentinels, SentinelPassword: c.SentinelPassword,
			Username: opt.Username, Password: opt.Password, DB: opt.DB, TLSConfig: opt.TLSConfig,
			PoolSize: opt.PoolSize, ReadTimeout: opt.ReadTimeout, WriteTimeout: opt.WriteTimeout,
		})
	} else {
		client = redis.NewClient(opt)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return client, nil
}
