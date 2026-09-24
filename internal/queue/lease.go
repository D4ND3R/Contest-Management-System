package queue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/redis/go-redis/v9"
)

// Lease is a Redis-based leadership lease: at most one holder at a time,
// lost automatically if the holder stops renewing (crash). The dispatcher
// and the monitor use it so that several replicas can run for availability
// while only one is active.
type Lease struct {
	rdb *redis.Client
	key string
	id  string
	ttl time.Duration
}

// NewLease creates a lease named name with the given time to live.
func (q *Queue) NewLease(name string, ttl time.Duration) *Lease {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return &Lease{rdb: q.rdb, key: q.Key("lease", name), id: hex.EncodeToString(b), ttl: ttl}
}

var renewScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("PEXPIRE", KEYS[1], ARGV[2])
end
return 0`)

var releaseScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0`)

// TryAcquire takes the lease if it is free.
func (l *Lease) TryAcquire(ctx context.Context) (bool, error) {
	return l.rdb.SetNX(ctx, l.key, l.id, l.ttl).Result()
}

// Renew extends the lease; false means it was lost.
func (l *Lease) Renew(ctx context.Context) (bool, error) {
	n, err := renewScript.Run(ctx, l.rdb, []string{l.key}, l.id, l.ttl.Milliseconds()).Int()
	return n == 1, err
}

// Release gives the lease up.
func (l *Lease) Release(ctx context.Context) {
	_ = releaseScript.Run(ctx, l.rdb, []string{l.key}, l.id).Err()
}

// Run blocks until the lease is acquired, then runs fn with a context that
// is cancelled if the lease is lost. When fn returns because of a lost
// lease, Run tries to acquire it again; it returns when ctx ends or fn
// returns an error while holding the lease.
func (l *Lease) Run(ctx context.Context, fn func(ctx context.Context) error) error {
	tick := l.ttl / 3
	for {
		ok, err := l.TryAcquire(ctx)
		if err == nil && ok {
			err := l.hold(ctx, fn)
			if err != nil || ctx.Err() != nil {
				return err
			}
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(tick):
		}
	}
}

func (l *Lease) hold(ctx context.Context, fn func(ctx context.Context) error) error {
	lctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer l.Release(context.WithoutCancel(ctx))
	done := make(chan error, 1)
	go func() { done <- fn(lctx) }()
	t := time.NewTicker(l.ttl / 3)
	defer t.Stop()
	for {
		select {
		case err := <-done:
			if lctx.Err() != nil && ctx.Err() == nil {
				return nil // lease lost: caller re-acquires
			}
			return err
		case <-t.C:
			if ok, err := l.Renew(ctx); err == nil && !ok {
				cancel()
			}
		}
	}
}
