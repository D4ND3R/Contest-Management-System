package dispatcher_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/dispatcher"
	"github.com/D4ND3R/Contest-Management-System/internal/events"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/logging"
	"github.com/D4ND3R/Contest-Management-System/internal/monitor"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/D4ND3R/Contest-Management-System/internal/sandbox"
	"github.com/D4ND3R/Contest-Management-System/internal/testutil"
	"github.com/D4ND3R/Contest-Management-System/internal/worker"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

var ctx = context.Background()

// env is a complete judging stack: database, Redis namespace, local blob
// store, dispatcher, monitor and (optionally) an in-process worker.
type env struct {
	t       *testing.T
	pool    *pgxpool.Pool
	dbURL   string
	rdb     *redis.Client
	ns      string
	q       *queue.Queue
	store   *blob.Local
	blobDir string
	disp    *dispatcher.Dispatcher
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	contest sqlc.Contest
	task    sqlc.Task
	dataset sqlc.Dataset
	part    sqlc.Participation
	evMu    sync.Mutex
	evs     []events.Event
	boxBase int
}

var boxBase = 600

func newEnv(t *testing.T, withWorker bool) *env {
	t.Helper()
	if withWorker {
		sandbox.TestIsolate(t) // skip without isolate/root
	}
	pool, dbURL := testutil.DBWithURL(t)
	rdb, ns := testutil.Redis(t)
	e := &env{t: t, pool: pool, dbURL: dbURL, rdb: rdb, ns: ns, q: queue.New(rdb, ns), blobDir: t.TempDir(), boxBase: boxBase}
	// isolate box ids must stay below num_boxes (1000); tests of this
	// package run sequentially, so ranges are reused after cleanup.
	boxBase += 40
	if boxBase >= 880 {
		boxBase = 600
	}
	var err error
	e.store, err = blob.NewLocal(e.blobDir, false)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := langs.Load(filepath.Join("..", "..", "config", "languages"))
	if err != nil {
		t.Fatal(err)
	}
	e.disp = dispatcher.New(pool, rdb, reg, logging.Discard(), dispatcher.Options{
		Namespace: ns, SweepInterval: 500 * time.Millisecond, MaxAttempts: 3, Consumer: "test",
	})
	runCtx, cancel := context.WithCancel(ctx)
	e.cancel = cancel
	if err := e.q.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	e.goRun(func() { e.disp.Run(runCtx) })
	mon := monitor.New(e.q, logging.Discard(), monitor.Options{CheckInterval: 300 * time.Millisecond, DeadGrace: 500 * time.Millisecond, MaxAttempts: 3})
	e.goRun(func() { mon.Run(runCtx) })
	e.goRun(func() {
		events.Subscribe(runCtx, rdb, ns, func(ev events.Event) {
			e.evMu.Lock()
			e.evs = append(e.evs, ev)
			e.evMu.Unlock()
		})
	})
	if withWorker {
		e.startWorker(runCtx, "w-inproc", e.boxBase)
	}
	t.Cleanup(func() {
		cancel()
		e.wg.Wait()
	})
	e.fixture()
	return e
}

func (e *env) goRun(fn func()) {
	e.wg.Add(1)
	go func() { defer e.wg.Done(); fn() }()
}

func (e *env) workerConfig(name string, boxOffset int) config.Worker {
	iso := sandbox.TestIsolate(e.t)
	cores := sandbox.DefaultCores()
	dir := e.t.TempDir()
	return config.Worker{
		Name: name, IsolatePath: iso.Path, IsolateCG: iso.CG, IsolateBoxRoot: iso.BoxRoot, Cores: cores[:1],
		BoxIDOffset: boxOffset, WorkDir: filepath.Join(dir, "work"), CacheDir: filepath.Join(dir, "cache"),
		CacheMaxBytes: 1 << 30, HeartbeatInterval: config.Duration(300 * time.Millisecond),
	}
}

func (e *env) startWorker(ctx context.Context, name string, boxOffset int) {
	svc, err := worker.NewService(e.workerConfig(name, boxOffset), e.store, e.q, logging.Discard())
	if err != nil {
		e.t.Fatal(err)
	}
	e.goRun(func() { svc.Run(ctx) })
}

func (e *env) put(s string) string {
	info, err := e.store.PutBytes(ctx, []byte(s))
	if err != nil {
		e.t.Fatal(err)
	}
	return info.Digest
}

// fixture: contest, A+B task with GroupMin [[30, 2], [70, 2]], 4 testcases.
func (e *env) fixture() {
	q := sqlc.New(e.pool)
	now := time.Now()
	var err error
	e.contest, err = q.CreateContest(ctx, db.NewContestParams("c", now.Add(-time.Hour), now.Add(time.Hour)))
	if err != nil {
		e.t.Fatal(err)
	}
	tp := db.NewTaskParams("sum", "Sum")
	tp.ContestID = &e.contest.ID
	tp.ScoreMode = "max_subtask"
	e.task, err = q.CreateTask(ctx, tp)
	if err != nil {
		e.t.Fatal(err)
	}
	e.dataset = e.newDataset("v1", false, [][2]string{{"1 2", "3"}, {"2 2", "4"}, {"10 20", "30"}, {"7 -2", "5"}}, `[[30, 2], [70, 2]]`)
	if err := q.SetActiveDataset(ctx, sqlc.SetActiveDatasetParams{ID: e.task.ID, ActiveDatasetID: &e.dataset.ID}); err != nil {
		e.t.Fatal(err)
	}
	u, err := q.CreateUser(ctx, sqlc.CreateUserParams{Username: "u", PasswordHash: "x", PreferredLanguages: []string{}})
	if err != nil {
		e.t.Fatal(err)
	}
	e.part, err = q.CreateParticipation(ctx, sqlc.CreateParticipationParams{ContestID: e.contest.ID, UserID: u.ID, Ip: []netip.Prefix{}})
	if err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) newDataset(desc string, autojudge bool, cases [][2]string, groups string) sqlc.Dataset {
	q := sqlc.New(e.pool)
	dp := db.NewDatasetParams(e.task.ID, desc)
	dp.ScoreType, dp.ScoreTypeParams, dp.Autojudge = "GroupMin", json.RawMessage(groups), autojudge
	ds, err := q.CreateDataset(ctx, dp)
	if err != nil {
		e.t.Fatal(err)
	}
	for i, c := range cases {
		if _, err := q.UpsertTestcase(ctx, sqlc.UpsertTestcaseParams{DatasetID: ds.ID, Codename: fmt.Sprintf("%02d", i),
			Public: i == 0, InputDigest: e.put(c[0] + "\n"), OutputDigest: e.put(c[1] + "\n")}); err != nil {
			e.t.Fatal(err)
		}
	}
	return ds
}

const (
	srcAC  = "#include <stdio.h>\nint main(void){long a,b;scanf(\"%ld %ld\",&a,&b);printf(\"%ld\\n\",a+b);return 0;}\n"
	srcWA  = "#include <stdio.h>\nint main(void){long a,b;scanf(\"%ld %ld\",&a,&b);printf(\"%ld\\n\",a-b);return 0;}\n"
	srcCE  = "int main(void){ return x; }\n"
	srcPos = "#include <stdio.h>\nint main(void){long a,b;scanf(\"%ld %ld\",&a,&b);printf(\"%ld\\n\",a==b?a+b:(a<b?a+b:0));return 0;}\n"
)

// submit stores a submission and notifies the dispatcher.
func (e *env) submit(src string, notify bool) int64 {
	q := sqlc.New(e.pool)
	lang := "c11"
	s, err := q.CreateSubmission(ctx, sqlc.CreateSubmissionParams{ParticipationID: e.part.ID, TaskID: e.task.ID,
		SubmittedAt: time.Now(), Language: &lang, Official: true})
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := q.CreateSubmissionFiles(ctx, []sqlc.CreateSubmissionFilesParams{{SubmissionID: s.ID, Filename: "sum.%l", Digest: e.put(src)}}); err != nil {
		e.t.Fatal(err)
	}
	if notify {
		if err := e.q.Notify(ctx, queue.Event{Kind: queue.EventSubmission, SubmissionID: s.ID}); err != nil {
			e.t.Fatal(err)
		}
	}
	return s.ID
}

func (e *env) waitScored(sub, ds int64, timeout time.Duration) sqlc.SubmissionResult {
	e.t.Helper()
	q := sqlc.New(e.pool)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		r, err := q.GetSubmissionResult(ctx, sqlc.GetSubmissionResultParams{SubmissionID: sub, DatasetID: ds})
		if err == nil && (r.ScoredAt != nil || r.SystemError != nil) {
			return r
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.t.Fatalf("submission %d not scored on dataset %d within %v", sub, ds, timeout)
	return sqlc.SubmissionResult{}
}

func (e *env) taskScore() sqlc.ParticipationTaskScore {
	s, err := sqlc.New(e.pool).GetParticipationTaskScore(ctx, sqlc.GetParticipationTaskScoreParams{ParticipationID: e.part.ID, TaskID: e.task.ID})
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

func TestEndToEndScoring(t *testing.T) {
	e := newEnv(t, true)
	ac := e.submit(srcAC, true)
	r := e.waitScored(ac, e.dataset.ID, 60*time.Second)
	if r.Score == nil || *r.Score != 100 || *r.PublicScore != 0 || r.CompilationOutcome == nil || *r.CompilationOutcome != "ok" {
		t.Fatalf("AC result: score=%v public=%v %+v", r.Score, r.PublicScore, r)
	}
	var details map[string]any
	json.Unmarshal(r.ScoreDetails, &details)
	if details["type"] != "group" || r.TestcasesDone != 4 {
		t.Fatalf("details %v done %d", details, r.TestcasesDone)
	}
	wa := e.submit(srcWA, true)
	if r := e.waitScored(wa, e.dataset.ID, 60*time.Second); *r.Score != 0 {
		t.Fatalf("WA score %v", *r.Score)
	}
	ce := e.submit(srcCE, true)
	r = e.waitScored(ce, e.dataset.ID, 60*time.Second)
	if *r.CompilationOutcome != "fail" || *r.Score != 0 || !strings.Contains(r.CompilationStderr, "x") {
		t.Fatalf("CE result %+v", r)
	}
	ts := e.taskScore()
	if ts.Score != 100 || ts.Pending != 0 {
		t.Fatalf("task score %+v", ts)
	}
	// Ranking updates were pushed.
	n, _ := e.rdb.XLen(ctx, e.q.RankingStream()).Result()
	if n < 3 {
		t.Fatalf("ranking updates = %d", n)
	}
	// Status events reached subscribers.
	time.Sleep(100 * time.Millisecond)
	e.evMu.Lock()
	seen := map[string]bool{}
	for _, ev := range e.evs {
		if ev.SubmissionID == ac {
			seen[ev.Status] = true
		}
	}
	e.evMu.Unlock()
	for _, st := range []string{"compiling", "evaluating", "scored"} {
		if !seen[st] {
			t.Errorf("no %q event for the AC submission (got %v)", st, seen)
		}
	}
}

func TestMaxSubtaskAcrossSubmissions(t *testing.T) {
	e := newEnv(t, true)
	// Testcases 00,01 (subtask 1) have a<=b; 02 has a<b; 03 has a>b (fails).
	p := e.submit(srcPos, true)
	r := e.waitScored(p, e.dataset.ID, 60*time.Second)
	if *r.Score != 30 {
		t.Fatalf("partial score %v", *r.Score)
	}
	var sub []float64
	json.Unmarshal(r.RankingScoreDetails, &sub)
	if len(sub) != 2 || sub[0] != 30 || sub[1] != 0 {
		t.Fatalf("ranking details %v", sub)
	}
	if ts := e.taskScore(); ts.Score != 30 {
		t.Fatalf("task score %v", ts.Score)
	}
}

func TestReevaluationLevels(t *testing.T) {
	e := newEnv(t, true)
	id := e.submit(srcAC, true)
	first := e.waitScored(id, e.dataset.ID, 60*time.Second)
	for _, lv := range []dispatcher.Level{dispatcher.Rescore, dispatcher.Reevaluate, dispatcher.Recompile} {
		n, err := dispatcher.Invalidate(ctx, e.pool, e.q, dispatcher.Scope{TaskID: e.task.ID}, lv)
		if err != nil || n != 1 {
			t.Fatalf("%s: invalidated %d, %v", lv, n, err)
		}
		r := e.waitScored(id, e.dataset.ID, 60*time.Second)
		if *r.Score != 100 {
			t.Fatalf("%s: score %v", lv, *r.Score)
		}
		switch lv {
		case dispatcher.Rescore:
			if r.Generation != first.Generation || r.CompilationTries != first.CompilationTries {
				t.Fatalf("rescore must not recompile or reevaluate: %+v", r)
			}
		case dispatcher.Reevaluate:
			if r.Generation != first.Generation+1 || r.CompilationTries != first.CompilationTries {
				t.Fatalf("reevaluate must keep the compilation: gen %d tries %d", r.Generation, r.CompilationTries)
			}
		case dispatcher.Recompile:
			if r.Generation != first.Generation+2 || r.CompilationTries != 1 {
				t.Fatalf("recompile: gen %d tries %d", r.Generation, r.CompilationTries)
			}
		}
	}
	if _, err := dispatcher.Invalidate(ctx, e.pool, e.q, dispatcher.Scope{}, dispatcher.Rescore); err == nil {
		t.Fatal("empty scope must be rejected")
	}
}

func TestLiveDatasetChange(t *testing.T) {
	e := newEnv(t, true)
	id := e.submit(srcAC, true)
	e.waitScored(id, e.dataset.ID, 60*time.Second)
	// A stricter dataset where the expected answers are doubled: the AC
	// submission becomes wrong on it.
	v2 := e.newDataset("v2", false, [][2]string{{"1 2", "6"}, {"2 2", "8"}}, `[[100, 2]]`)
	if err := dispatcher.ChangeLiveDataset(ctx, e.pool, e.q, v2.ID); err != nil {
		t.Fatal(err)
	}
	r := e.waitScored(id, v2.ID, 60*time.Second)
	if *r.Score != 0 {
		t.Fatalf("score on v2 = %v", *r.Score)
	}
	deadline := time.Now().Add(10 * time.Second)
	for e.taskScore().Score != 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if ts := e.taskScore(); ts.Score != 0 {
		t.Fatalf("task score after switching the live dataset = %v", ts.Score)
	}
}

func TestAutojudgeBackgroundDataset(t *testing.T) {
	e := newEnv(t, true)
	bg := e.newDataset("bg", true, [][2]string{{"3 4", "7"}}, `[[100, 1]]`)
	id := e.submit(srcAC, true)
	e.waitScored(id, e.dataset.ID, 60*time.Second)
	if r := e.waitScored(id, bg.ID, 60*time.Second); *r.Score != 100 {
		t.Fatalf("background dataset score %v", *r.Score)
	}
	// The background dataset never changes the ranking.
	if ts := e.taskScore(); ts.Score != 100 {
		t.Fatalf("task score %v", ts.Score)
	}
}

func TestRetriesThenSystemError(t *testing.T) {
	e := newEnv(t, true)
	// A dataset whose task type the workers do not know: every job fails.
	q := sqlc.New(e.pool)
	ds := db.DatasetToUpdate(e.dataset)
	ds.TaskType = "NoSuchType"
	if _, err := q.UpdateDataset(ctx, ds); err != nil {
		t.Fatal(err)
	}
	id := e.submit(srcAC, true)
	r := e.waitScored(id, e.dataset.ID, 60*time.Second)
	if r.SystemError == nil || !strings.Contains(*r.SystemError, "failed 3 times") {
		t.Fatalf("system error = %v", r.SystemError)
	}
	time.Sleep(100 * time.Millisecond)
	e.evMu.Lock()
	defer e.evMu.Unlock()
	alert := false
	for _, ev := range e.evs {
		alert = alert || (ev.Type == events.TypeAlert && ev.SubmissionID == id)
	}
	if !alert {
		t.Fatal("no admin alert for the system error")
	}
}

func TestSweeperRecoversLostNotification(t *testing.T) {
	e := newEnv(t, true)
	id := e.submit(srcAC, false) // the notification is "lost"
	if r := e.waitScored(id, e.dataset.ID, 60*time.Second); *r.Score != 100 {
		t.Fatalf("score %v", *r.Score)
	}
}

func TestUserTest(t *testing.T) {
	e := newEnv(t, true)
	q := sqlc.New(e.pool)
	lang := "c11"
	ut, err := q.CreateUserTest(ctx, sqlc.CreateUserTestParams{ParticipationID: e.part.ID, TaskID: e.task.ID,
		SubmittedAt: time.Now(), Language: &lang, InputDigest: e.put("40 2\n")})
	if err != nil {
		t.Fatal(err)
	}
	q.CreateUserTestFiles(ctx, []sqlc.CreateUserTestFilesParams{{UserTestID: ut.ID, Filename: "sum.%l", Digest: e.put(srcAC)}})
	e.q.Notify(ctx, queue.Event{Kind: queue.EventUserTest, UserTestID: ut.ID})
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		r, err := q.GetUserTestResult(ctx, sqlc.GetUserTestResultParams{UserTestID: ut.ID, DatasetID: e.dataset.ID})
		if err == nil && r.CompletedAt != nil {
			out, _ := blob.ReadAll(ctx, e.store, *r.OutputDigest)
			if string(out) != "42\n" || *r.ExitStatus != "ok" {
				t.Fatalf("user test output %q status %v", out, *r.ExitStatus)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("user test not completed")
}

// TestKillWorkerMidEvaluation is the F4 exit criterion: a worker process is
// killed with SIGKILL while evaluating; the monitor hands its job to another
// worker and the submission ends up fully and correctly scored, with every
// testcase evaluated.
func TestKillWorkerMidEvaluation(t *testing.T) {
	e := newEnv(t, false)
	bin := buildCMS(t)
	// Slow testcases: each run burns ~0.4 s of CPU.
	q := sqlc.New(e.pool)
	var cases [][2]string
	for i := 0; i < 8; i++ {
		cases = append(cases, [2]string{fmt.Sprintf("%d %d", i, i), fmt.Sprintf("%d", 2*i)})
	}
	slow := e.newDataset("slow", false, cases, `[[50, 4], [50, 4]]`)
	if err := q.SetActiveDataset(ctx, sqlc.SetActiveDatasetParams{ID: e.task.ID, ActiveDatasetID: &slow.ID}); err != nil {
		t.Fatal(err)
	}
	src := "#include <stdio.h>\n#include <time.h>\nint main(void){long a,b;scanf(\"%ld %ld\",&a,&b);" +
		"clock_t s=clock(); while(clock()-s < CLOCKS_PER_SEC*4/10); printf(\"%ld\\n\",a+b);return 0;}\n"

	w1 := e.spawnWorker(t, bin, "victim", e.boxBase)
	id := e.submit(src, true)
	// Wait until the victim has produced at least one evaluation, then kill it.
	deadline := time.Now().Add(60 * time.Second)
	for {
		r, err := q.GetSubmissionResult(ctx, sqlc.GetSubmissionResultParams{SubmissionID: id, DatasetID: slow.ID})
		if err == nil && r.TestcasesDone >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("victim worker never evaluated a testcase")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := w1.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	w1.Wait()
	t.Log("victim worker killed mid-evaluation")
	// A replacement worker picks up the reclaimed job and the rest.
	w2 := e.spawnWorker(t, bin, "rescuer", e.boxBase+20)
	defer func() { w2.Process.Signal(syscall.SIGTERM); w2.Wait() }()
	r := e.waitScored(id, slow.ID, 120*time.Second)
	if r.SystemError != nil || r.Score == nil || *r.Score != 100 {
		t.Fatalf("after the kill: score=%v error=%v", r.Score, r.SystemError)
	}
	evs, _ := q.ListEvaluations(ctx, sqlc.ListEvaluationsParams{SubmissionID: id, DatasetID: slow.ID})
	if len(evs) != 8 {
		t.Fatalf("%d evaluations stored, want 8", len(evs))
	}
	workers := map[string]int{}
	for _, ev := range evs {
		workers[*ev.Worker]++
	}
	t.Logf("evaluations per worker: %v", workers)
	if workers["rescuer"] == 0 {
		t.Fatal("the replacement worker did not take over")
	}
}

var (
	buildOnce sync.Once
	buildPath string
	buildErr  error
)

func buildCMS(t *testing.T) string {
	sandbox.TestIsolate(t)
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "cms-bin-")
		if err != nil {
			buildErr = err
			return
		}
		buildPath = filepath.Join(dir, "cms")
		out, err := exec.Command("go", "build", "-o", buildPath, "../../cmd/cms").CombinedOutput()
		if err != nil {
			buildErr = fmt.Errorf("go build: %v %s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return buildPath
}

func (e *env) spawnWorker(t *testing.T, bin, name string, boxOffset int) *exec.Cmd {
	wc := e.workerConfig(name, boxOffset)
	cfg := map[string]any{
		"log":           map[string]any{"level": "warn", "format": "text"},
		"redis":         map[string]any{"url": testutil.RedisURL(t), "namespace": e.ns},
		"blob":          map[string]any{"backend": "local", "local_dir": e.blobDir},
		"languages_dir": filepath.Join("..", "..", "config", "languages"),
		"worker": map[string]any{
			"name": name, "metrics_listen": "127.0.0.1:0", "isolate_path": wc.IsolatePath, "isolate_cg": wc.IsolateCG,
			"isolate_box_root": wc.IsolateBoxRoot, "cores": wc.Cores, "box_id_offset": boxOffset,
			"work_dir": wc.WorkDir, "cache_dir": wc.CacheDir, "heartbeat_interval": "300ms",
		},
	}
	data, _ := json.Marshal(cfg) // JSON is valid YAML
	path := filepath.Join(t.TempDir(), "cms.yaml")
	os.WriteFile(path, data, 0o644)
	cmd := exec.Command(bin, "worker", "-config", path)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	cmd.Env = append(os.Environ(), "CMS_CONFIG=")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})
	return cmd
}

// TestDispatcherRestart stops the active dispatcher in the middle of an
// evaluation and starts a replica with another consumer name: the replica
// adopts the pending results and finishes the submission.
func TestDispatcherRestart(t *testing.T) {
	e := newEnv(t, true)
	e.cancel() // stop the env's dispatcher, monitor and worker
	e.wg.Wait()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	e.startWorker(runCtx, "w2", e.boxBase+20)
	q := sqlc.New(e.pool)
	var cases [][2]string
	for i := 0; i < 6; i++ {
		cases = append(cases, [2]string{fmt.Sprintf("%d 1", i), fmt.Sprintf("%d", i+1)})
	}
	slow := e.newDataset("slow", false, cases, `[[100, 6]]`)
	q.SetActiveDataset(ctx, sqlc.SetActiveDatasetParams{ID: e.task.ID, ActiveDatasetID: &slow.ID})
	src := "#include <stdio.h>\n#include <time.h>\nint main(void){long a,b;scanf(\"%ld %ld\",&a,&b);" +
		"clock_t s=clock(); while(clock()-s < CLOCKS_PER_SEC*3/10); printf(\"%ld\\n\",a+b);return 0;}\n"
	reg, _ := langs.Load(filepath.Join("..", "..", "config", "languages"))
	d1ctx, stop1 := context.WithCancel(ctx)
	d1 := dispatcher.New(e.pool, e.rdb, reg, logging.Discard(), dispatcher.Options{Namespace: e.ns, Consumer: "first", SweepInterval: time.Hour})
	done1 := make(chan struct{})
	go func() { d1.Run(d1ctx); close(done1) }()
	id := e.submit(src, true)
	deadline := time.Now().Add(60 * time.Second)
	for {
		r, err := q.GetSubmissionResult(ctx, sqlc.GetSubmissionResultParams{SubmissionID: id, DatasetID: slow.ID})
		if err == nil && r.TestcasesDone >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no progress before the restart")
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop1()
	<-done1
	// Results keep arriving while no dispatcher runs; a replica takes over.
	time.Sleep(time.Second)
	d2 := dispatcher.New(e.pool, e.rdb, reg, logging.Discard(), dispatcher.Options{Namespace: e.ns, Consumer: "second", SweepInterval: time.Hour})
	go d2.Run(runCtx)
	r := e.waitScored(id, slow.ID, 60*time.Second)
	if r.Score == nil || *r.Score != 100 {
		t.Fatalf("score after restart %v (error %v)", r.Score, r.SystemError)
	}
}
