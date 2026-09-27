package contestweb

import (
	"bytes"
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/logging"
)

// TestSeveralContestWebServers (SPEC_IOI §1.1): contest web servers keep
// no state of their own, so a contestant logged in on one is served by
// any other behind the load balancer, submissions included.
func TestSeveralContestWebServers(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	reg, _ := langs.Load(filepath.Join("..", "..", "config", "languages"))
	cfg := config.Default().ContestWeb
	cfg.RateLimitPerMinute, cfg.LoginRateLimit = 1000, 1000
	second, err := New(cfg, Deps{Pool: f.pool, Redis: f.rdb, Blobs: f.store, Langs: reg, Secret: bytes.Repeat([]byte("k"), 32), NS: f.ns}, logging.Discard())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(bg)
	ready := make(chan net.Addr, 1)
	done := make(chan struct{})
	go func() { second.Run(ctx, "127.0.0.1:0", ready); close(done) }()
	secondURL := "http://" + (<-ready).String()
	t.Cleanup(func() { cancel(); <-done })

	c := f.client()
	if code, body := f.login(c, "ana", "secret"); code != 200 && code != 303 {
		t.Fatalf("login on the first server = %d\n%s", code, body)
	}
	first := f.url
	f.url = secondURL
	code, page := f.get(c, "/ioi/")
	if code != 200 || strings.Contains(page, `name="password"`) {
		t.Fatalf("the second server does not know the session: %d", code)
	}
	if code, body := f.submit(c, csrfOf(t, page), "c11", "int main(){}", false); code != 200 && code != 303 {
		t.Fatalf("submit on the second server = %d\n%s", code, body)
	}
	f.url = first
	deadline := time.Now().Add(5 * time.Second)
	for {
		var n int
		if f.pool.QueryRow(bg, "SELECT count(*) FROM submissions WHERE participation_id = $1", f.part.ID).Scan(&n); n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the submission made on the second server is not stored")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
