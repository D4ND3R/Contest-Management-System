// Package e2e holds end-to-end tests running the whole system in-process:
// PostgreSQL, Redis, the blob store, the dispatcher, the monitor, workers
// (with isolate) and the web servers.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/adminweb"
	"github.com/D4ND3R/Contest-Management-System/internal/auth"
	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/blobserver"
	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/contestweb"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/dispatcher"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/logging"
	"github.com/D4ND3R/Contest-Management-System/internal/monitor"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/D4ND3R/Contest-Management-System/internal/rankingpush"
	"github.com/D4ND3R/Contest-Management-System/internal/rankingweb"
	"github.com/D4ND3R/Contest-Management-System/internal/sandbox"
	"github.com/D4ND3R/Contest-Management-System/internal/testutil"
	"github.com/D4ND3R/Contest-Management-System/internal/worker"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

var bg = context.Background()

// stack is a running system.
type stack struct {
	t       testing.TB
	pool    *pgxpool.Pool
	q       *sqlc.Queries
	rdb     *redis.Client
	ns      string
	store   blob.Store
	langs   *langs.Registry
	cwsURL  string
	awsURL  string
	rwsURL  string
	secret  []byte
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	contest sqlc.Contest
	task    sqlc.Task
	ds      sqlc.Dataset
}

type stackOpts struct {
	workers bool // start a worker (needs isolate + root)
	admin   bool // start the admin web server
	empty   bool // no contest fixture (created through the admin UI)
	ranking bool // start a ranking web server fed by the ranking pusher
	// remoteBlobs makes the worker reach the blob store only through a
	// blob server, like a worker on another machine.
	remoteBlobs bool
}

// Isolate box ids of this package: 100-299 (worker tests use 400-599,
// dispatcher tests 600-879 and sandbox tests 900+; packages run in
// parallel, so the ranges must not overlap).
var boxBase = 100

func newStack(t testing.TB, o stackOpts) *stack {
	t.Helper()
	if o.workers {
		sandbox.TestIsolate(t)
	}
	pool := testutil.DB(t)
	rdb, ns := testutil.Redis(t)
	st, err := blob.NewLocal(t.(interface{ TempDir() string }).TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := langs.Load(filepath.Join("..", "..", "config", "languages"))
	if err != nil {
		t.Fatal(err)
	}
	s := &stack{t: t, pool: pool, q: sqlc.New(pool), rdb: rdb, ns: ns, store: blob.NewTracked(st, sqlc.New(pool)), langs: reg,
		secret: bytes.Repeat([]byte("s"), 32)}
	ctx, cancel := context.WithCancel(bg)
	s.cancel = cancel
	qu := queue.New(rdb, ns)
	if err := qu.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	disp := dispatcher.New(pool, rdb, reg, logging.Discard(), dispatcher.Options{Namespace: ns, SweepInterval: time.Second, Consumer: "e2e"})
	s.run(func() { disp.Run(ctx) })
	mon := monitor.New(qu, logging.Discard(), monitor.Options{CheckInterval: 500 * time.Millisecond})
	s.run(func() { mon.Run(ctx) })
	if o.workers {
		iso := sandbox.TestIsolate(t)
		dir := t.(interface{ TempDir() string }).TempDir()
		cores := sandbox.DefaultCores()
		need := 2 * sandbox.BoxesPerSlot * len(cores)
		if boxBase+need > 300 {
			boxBase = 100
		}
		var workerStore blob.Store = st
		if o.remoteBlobs {
			bs, err := blobserver.New(st, "e2e-blob-server-token", 0, logging.Discard())
			if err != nil {
				t.Fatal(err)
			}
			ready := make(chan net.Addr, 1)
			s.run(func() { bs.Run(ctx, "127.0.0.1:0", ready) })
			workerStore = blob.NewHTTP("http://"+(<-ready).String(), "e2e-blob-server-token")
		}
		svc, err := worker.NewService(config.Worker{Name: "e2e-worker", IsolatePath: iso.Path, IsolateCG: iso.CG,
			IsolateBoxRoot: iso.BoxRoot, Cores: cores, BoxIDOffset: boxBase, WorkDir: filepath.Join(dir, "w"),
			CacheDir: filepath.Join(dir, "c"), CacheMaxBytes: 1 << 30}, workerStore, qu, logging.Discard())
		boxBase += need
		if err != nil {
			t.Fatal(err)
		}
		s.run(func() { svc.Run(ctx) })
	}
	cfg := config.Default().ContestWeb
	cfg.RateLimitPerMinute, cfg.LoginRateLimit = 100000, 100000
	cws, err := contestweb.New(cfg, contestweb.Deps{Pool: pool, Redis: rdb, Blobs: s.store, Langs: reg, Secret: s.secret, NS: ns}, logging.Discard())
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan net.Addr, 1)
	s.run(func() { cws.Run(ctx, "127.0.0.1:0", ready) })
	s.cwsURL = "http://" + (<-ready).String()
	if o.admin {
		acfg := config.Default().AdminWeb
		acfg.LoginRateLimit = 1000
		acfg.ContestURL = s.cwsURL
		aws, err := adminweb.New(acfg, adminweb.Deps{Pool: pool, Redis: rdb, Blobs: s.store, Langs: reg, Secret: s.secret, NS: ns}, logging.Discard())
		if err != nil {
			t.Fatal(err)
		}
		ready := make(chan net.Addr, 1)
		s.run(func() { aws.Run(ctx, "127.0.0.1:0", ready) })
		s.awsURL = "http://" + (<-ready).String()
	}
	if o.ranking {
		rws, err := rankingweb.New(config.RankingWeb{DataDir: t.(interface{ TempDir() string }).TempDir(), PushToken: "e2e-token"}, logging.Discard())
		if err != nil {
			t.Fatal(err)
		}
		ready := make(chan net.Addr, 1)
		s.run(func() { rws.Run(ctx, "127.0.0.1:0", ready) })
		s.rwsURL = "http://" + (<-ready).String()
		p := rankingpush.New(pool, rdb, s.store, logging.Discard(), rankingpush.Options{URLs: []string{s.rwsURL}, Token: "e2e-token",
			Secret: s.secret, Namespace: ns})
		s.run(func() { p.Run(ctx) })
	}
	t.Cleanup(func() {
		cancel()
		s.wg.Wait()
	})
	if !o.empty {
		s.fixture()
	}
	time.Sleep(100 * time.Millisecond)
	return s
}

func (s *stack) run(fn func()) {
	s.wg.Add(1)
	go func() { defer s.wg.Done(); fn() }()
}

func (s *stack) put(b string) string {
	info, err := s.store.PutBytes(bg, []byte(b))
	if err != nil {
		s.t.Fatal(err)
	}
	return info.Digest
}

// fixture: a running contest with the A+B task (4 testcases, 25 each).
func (s *stack) fixture() {
	now := time.Now()
	cp := db.NewContestParams("e2e", now.Add(-time.Hour), now.Add(3*time.Hour))
	cp.Languages = []string{"c11", "cpp17", "python3"}
	var err error
	s.contest, err = s.q.CreateContest(bg, cp)
	if err != nil {
		s.t.Fatal(err)
	}
	tp := db.NewTaskParams("sum", "Sum")
	tp.ContestID = &s.contest.ID
	s.task, _ = s.q.CreateTask(bg, tp)
	dp := db.NewDatasetParams(s.task.ID, "v1")
	dp.ScoreType, dp.ScoreTypeParams = "Sum", json.RawMessage(`25`)
	s.ds, _ = s.q.CreateDataset(bg, dp)
	s.q.SetActiveDataset(bg, sqlc.SetActiveDatasetParams{ID: s.task.ID, ActiveDatasetID: &s.ds.ID})
	for i, c := range [][2]string{{"1 2", "3"}, {"5 5", "10"}, {"-1 1", "0"}, {"100 23", "123"}} {
		s.q.UpsertTestcase(bg, sqlc.UpsertTestcaseParams{DatasetID: s.ds.ID, Codename: fmt.Sprint(i), Public: i == 0,
			InputDigest: s.put(c[0] + "\n"), OutputDigest: s.put(c[1] + "\n")})
	}
}

var hashOnce sync.Once
var sharedHash string

// addContestant creates a user with password "pw" and a participation.
func (s *stack) addContestant(name string) sqlc.Participation {
	hashOnce.Do(func() { sharedHash, _ = auth.HashPassword("pw") })
	u, err := s.q.CreateUser(bg, sqlc.CreateUserParams{Username: name, PasswordHash: sharedHash, PreferredLanguages: []string{}})
	if err != nil {
		s.t.Fatal(err)
	}
	p, err := s.q.CreateParticipation(bg, sqlc.CreateParticipationParams{ContestID: s.contest.ID, UserID: u.ID, Ip: []netip.Prefix{}})
	if err != nil {
		s.t.Fatal(err)
	}
	return p
}

// browser is a contestant's HTTP client.
type browser struct {
	s    *stack
	c    *http.Client
	csrf string
}

var csrfRe = regexp.MustCompile(`name="csrf-token" content="([^"]+)"`)

func (s *stack) login(name string) *browser {
	jar, _ := cookiejar.New(nil)
	b := &browser{s: s, c: &http.Client{Jar: jar, Timeout: 30 * time.Second}}
	_, page := b.get("/e2e/login")
	b.csrf = csrfRe.FindStringSubmatch(page)[1]
	resp, err := b.c.PostForm(s.cwsURL+"/e2e/login", url.Values{"csrf": {b.csrf}, "username": {name}, "password": {"pw"}})
	if err != nil {
		s.t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m := csrfRe.FindStringSubmatch(string(body))
	if resp.StatusCode != 200 || m == nil {
		s.t.Fatalf("login %s failed: %d", name, resp.StatusCode)
	}
	b.csrf = m[1]
	return b
}

func (b *browser) get(path string) (int, string) {
	resp, err := b.c.Get(b.s.cwsURL + path)
	if err != nil {
		b.s.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func (b *browser) submit(lang, filename, src string) int {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("csrf", b.csrf)
	mw.WriteField("language", lang)
	fw, _ := mw.CreateFormFile("sum.%l", filename)
	fw.Write([]byte(src))
	mw.Close()
	req, _ := http.NewRequest("POST", b.s.cwsURL+"/e2e/tasks/sum/submit", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("HX-Request", "true")
	resp, err := b.c.Do(req)
	if err != nil {
		b.s.t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}
