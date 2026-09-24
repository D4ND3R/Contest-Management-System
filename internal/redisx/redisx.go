// Package redisx opens the Redis (or Valkey) client used for queues,
// pub/sub, rate limiting and caching.
package redisx

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Open parses url (redis://[:password@]host:port/db) and pings the server.
func Open(ctx context.Context, url string, poolSize int) (*redis.Client, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	if poolSize > 0 {
		opt.PoolSize = poolSize
	}
	opt.ReadTimeout = 5 * time.Second
	opt.WriteTimeout = 5 * time.Second
	// Blocking stream reads set their own deadlines; see queue package.
	c := redis.NewClient(opt)
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := c.Ping(pingCtx).Err(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return c, nil
}
