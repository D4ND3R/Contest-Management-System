package rankingweb

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/logging"
)

// TestLivePageInBrowser runs the live ranking page (rws.js) in headless
// Chromium through testdata/live.js: random updates keep every rank and
// the order right without reloading, a page that missed updates reloads,
// and unfreezing reveals the rows. It needs node with Playwright and a
// Chromium it can launch (as `make test` has where they are installed);
// elsewhere it is skipped.
func TestLivePageInBrowser(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	env := os.Environ()
	if out, err := exec.Command("npm", "root", "-g").Output(); err == nil {
		env = append(env, "NODE_PATH="+strings.TrimSpace(string(out)))
	}
	probe := exec.Command(node, "-e", "require('playwright')")
	probe.Env = env
	if probe.Run() != nil {
		t.Skip("Playwright is not installed")
	}

	s, err := New(config.RankingWeb{DataDir: t.TempDir(), PushToken: token, Title: "Rankings", MaxClients: 100}, logging.Discard())
	if err != nil {
		t.Fatal(err)
	}
	// /test/drop cuts the browser's connections only: cutting every one
	// would also break the requests of the script driving the test.
	type connKey struct{}
	var mu sync.Mutex
	browser := map[net.Conn]bool{}
	mux := http.NewServeMux()
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.UserAgent(), "Chrome") {
			mu.Lock()
			browser[r.Context().Value(connKey{}).(net.Conn)] = true
			mu.Unlock()
		}
		mux.ServeHTTP(w, r)
	}))
	ts.Config.ConnContext = func(ctx context.Context, c net.Conn) context.Context { return context.WithValue(ctx, connKey{}, c) }
	mux.Handle("/", s.Handler())
	mux.HandleFunc("GET /test/spectators", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, s.clients.Load()) })
	mux.HandleFunc("POST /test/drop", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		for c := range browser {
			c.Close()
			delete(browser, c)
		}
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	ts.Start()
	defer ts.Close()

	cmd := exec.Command(node, "testdata/live.js")
	cmd.Env = append(env, "RWS_URL="+ts.URL, "RWS_TOKEN="+token)
	out, err := cmd.CombinedOutput()
	if strings.Contains(string(out), "Executable doesn't exist") || strings.Contains(string(out), "Failed to launch") {
		t.Skip("Playwright cannot launch Chromium here")
	}
	if err != nil || strings.TrimSpace(string(out)) != "PASS" {
		t.Fatalf("%v\n%s", err, out)
	}
}
