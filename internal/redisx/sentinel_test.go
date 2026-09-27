package redisx

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/redis/go-redis/v9"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// server starts a Valkey (or Redis) process; the test ends it.
func server(t *testing.T, bin string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = t.TempDir()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	return cmd
}

func waitFor(t *testing.T, what string, d time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestSentinelFailover (SPEC_IOI §12): with sentinels configured, the
// services follow the primary: after it dies and a replica is promoted,
// the same client keeps reading and writing.
func TestSentinelFailover(t *testing.T) {
	bin := ""
	for _, b := range []string{"valkey-server", "redis-server"} {
		if p, err := exec.LookPath(b); err == nil {
			bin = p
			break
		}
	}
	if bin == "" {
		t.Skip("no valkey-server or redis-server")
	}
	ctx := context.Background()
	pp, rp, sp := freePort(t), freePort(t), freePort(t)
	primary := server(t, bin, "--port", fmt.Sprint(pp), "--save", "", "--appendonly", "no")
	server(t, bin, "--port", fmt.Sprint(rp), "--save", "", "--appendonly", "no", "--replicaof", "127.0.0.1", fmt.Sprint(pp))
	conf := filepath.Join(t.TempDir(), "sentinel.conf")
	os.WriteFile(conf, []byte(fmt.Sprintf(`port %d
sentinel monitor cms 127.0.0.1 %d 1
sentinel down-after-milliseconds cms 500
sentinel failover-timeout cms 2000
`, sp, pp)), 0o644)
	server(t, bin, conf, "--sentinel")

	replica := redis.NewClient(&redis.Options{Addr: fmt.Sprintf("127.0.0.1:%d", rp)})
	defer replica.Close()
	waitFor(t, "replication", 10*time.Second, func() bool {
		info, _ := replica.Info(ctx, "replication").Result()
		return strings.Contains(info, "master_link_status:up")
	})
	sentinel := redis.NewSentinelClient(&redis.Options{Addr: fmt.Sprintf("127.0.0.1:%d", sp)})
	defer sentinel.Close()
	waitFor(t, "the sentinel to see the replica", 10*time.Second, func() bool {
		r, _ := sentinel.Replicas(ctx, "cms").Result()
		return len(r) > 0
	})

	c, err := Open(ctx, config.Redis{URL: "redis://ignored:1/0", Sentinels: []string{fmt.Sprintf("127.0.0.1:%d", sp)}, SentinelMaster: "cms"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Set(ctx, "before", "1", 0).Err(); err != nil {
		t.Fatal(err)
	}
	c.Do(ctx, "WAIT", 1, 2000) // on the replica before the primary dies
	primary.Process.Kill()
	primary.Wait()
	waitFor(t, "the failover", 20*time.Second, func() bool {
		return c.Set(ctx, "after", "2", 0).Err() == nil
	})
	if v, err := c.Get(ctx, "before").Result(); err != nil || v != "1" {
		t.Fatalf("data written before the failover: %q %v", v, err)
	}
	if info, _ := replica.Info(ctx, "replication").Result(); !strings.Contains(info, "role:master") {
		t.Fatalf("the replica was not promoted:\n%s", info)
	}
	if _, err := Open(ctx, config.Redis{URL: "redis://x/0", Sentinels: []string{fmt.Sprintf("127.0.0.1:%d", sp)}, SentinelMaster: "nope"}); err == nil {
		t.Fatal("an unknown master name connected")
	}
}
