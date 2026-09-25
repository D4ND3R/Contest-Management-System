// Package testutil provides isolated PostgreSQL databases and Redis
// namespaces for integration tests.
//
// Each test gets its own database cloned from a migrated template (fast:
// CREATE DATABASE ... TEMPLATE) and its own Redis key namespace, so test
// packages can run in parallel against a single server.
package testutil

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// RandomID returns a short random lowercase identifier.
func RandomID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func requireEnv(t testing.TB, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		if testing.Short() {
			t.Skipf("%s not set (short mode)", key)
		}
		t.Skipf("%s not set; run the tests with `make test`", key)
	}
	return v
}

var (
	tplOnce sync.Once
	tplName string
	tplErr  error
)

// templateName derives the template database name from the migrations so
// that schema changes automatically produce a fresh template.
func templateName() (string, error) {
	migs, err := db.Migrations()
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, m := range migs {
		h.Write([]byte(m.Name))
		h.Write([]byte(m.SQL))
	}
	return "cms_tpl_" + hex.EncodeToString(h.Sum(nil))[:16], nil
}

func withDatabase(raw, name string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Path = "/" + name
	return u.String()
}

// ensureTemplate creates the migrated template database once per server
// (guarded by an advisory lock because test binaries run concurrently).
func ensureTemplate(ctx context.Context, admin *pgx.Conn, baseURL string) (string, error) {
	name, err := templateName()
	if err != nil {
		return "", err
	}
	if _, err := admin.Exec(ctx, "SELECT pg_advisory_lock(4242)"); err != nil {
		return "", err
	}
	defer admin.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock(4242)") //nolint:errcheck
	var exists bool
	if err := admin.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists); err != nil {
		return "", err
	}
	if exists {
		return name, nil
	}
	tmp := name + "_building"
	_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+tmp+" WITH (FORCE)")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+tmp); err != nil {
		return "", err
	}
	pool, err := pgxpool.New(ctx, withDatabase(baseURL, tmp))
	if err != nil {
		return "", err
	}
	_, err = db.Migrate(ctx, pool)
	pool.Close()
	if err != nil {
		return "", fmt.Errorf("migrate template: %w", err)
	}
	if _, err := admin.Exec(ctx, "ALTER DATABASE "+tmp+" RENAME TO "+name); err != nil {
		return "", err
	}
	return name, nil
}

// DB returns a pool connected to a fresh, fully migrated database that is
// dropped when the test ends. Skips the test when CMS_TEST_DATABASE_URL is unset.
func DB(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool, _ := DBWithURL(t)
	return pool
}

// DBWithURL is DB but also returns the connection URL (for services that
// open their own pools).
func DBWithURL(t testing.TB) (*pgxpool.Pool, string) {
	t.Helper()
	base := requireEnv(t, "CMS_TEST_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect admin database: %v", err)
	}
	defer admin.Close(context.Background())
	tplOnce.Do(func() { tplName, tplErr = ensureTemplate(ctx, admin, base) })
	if tplErr != nil {
		t.Fatalf("template database: %v", tplErr)
	}
	name := "cms_t_" + RandomID()
	for attempt := 0; ; attempt++ {
		_, err = admin.Exec(ctx, "CREATE DATABASE "+name+" TEMPLATE "+tplName)
		if err == nil {
			break
		}
		// Concurrent clones of the same template can transiently collide.
		if attempt < 20 && strings.Contains(err.Error(), "being accessed by other users") {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		t.Fatalf("create test database: %v", err)
	}
	dbURL := withDatabase(base, name)
	pool, err := db.Open(ctx, dbURL, 16)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		c, err := pgx.Connect(context.Background(), base)
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		_, _ = c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	return pool, dbURL
}

// EmptyDB returns a pool and URL for a fresh database without any
// migration applied (restore targets); it is dropped when the test ends.
func EmptyDB(t testing.TB) (*pgxpool.Pool, string) {
	t.Helper()
	base := requireEnv(t, "CMS_TEST_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect admin database: %v", err)
	}
	defer admin.Close(context.Background())
	name := "cms_e_" + RandomID()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name+" TEMPLATE template0"); err != nil {
		t.Fatalf("create empty database: %v", err)
	}
	dbURL := withDatabase(base, name)
	pool, err := db.Open(ctx, dbURL, 16)
	if err != nil {
		t.Fatalf("open empty database: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		c, err := pgx.Connect(context.Background(), base)
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		_, _ = c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	return pool, dbURL
}

// Redis returns a client and a unique key namespace (ending in ":"); every
// key under the namespace is deleted when the test ends.
func Redis(t testing.TB) (*redis.Client, string) {
	t.Helper()
	raw := requireEnv(t, "CMS_TEST_REDIS_URL")
	opt, err := redis.ParseURL(raw)
	if err != nil {
		t.Fatalf("parse redis url: %v", err)
	}
	c := redis.NewClient(opt)
	if err := c.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("ping redis: %v", err)
	}
	ns := "t" + RandomID() + ":"
	t.Cleanup(func() {
		ctx := context.Background()
		iter := c.Scan(ctx, 0, ns+"*", 1000).Iterator()
		var keys []string
		for iter.Next(ctx) {
			keys = append(keys, iter.Val())
		}
		if len(keys) > 0 {
			c.Del(ctx, keys...)
		}
		_ = c.Close()
	})
	return c, ns
}

// RedisURL returns the raw Redis URL for services that open their own client.
func RedisURL(t testing.TB) string { return requireEnv(t, "CMS_TEST_REDIS_URL") }
