package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/logging"
	"github.com/D4ND3R/Contest-Management-System/internal/sandbox"
)

// harness is a worker executor backed by an in-memory blob store.
type harness struct {
	t     testing.TB
	exec  *Executor
	store *blob.Mem
	langs *langs.Registry
	// free holds the indexes of idle slots (multi-slot harnesses).
	free chan int
}

// Isolate box ids of this package: 400-599 (see internal/e2e for the
// other packages' ranges; test packages run in parallel).
var boxOffset = 400

func newHarness(t testing.TB) *harness { return newHarnessSlots(t, 1) }

// newHarnessSlots creates a harness with up to n slots (one per judging core).
func newHarnessSlots(t testing.TB, n int) *harness {
	t.Helper()
	iso := sandbox.TestIsolate(t)
	dir := t.TempDir()
	reg, err := langs.Load(filepath.Join("..", "..", "config", "languages"))
	if err != nil {
		t.Fatal(err)
	}
	store := blob.NewMem()
	cores := sandbox.DefaultCores()
	if n > len(cores) {
		n = len(cores)
	}
	if boxOffset+n*2*sandbox.BoxesPerSlot > 600 {
		boxOffset = 400 // harnesses run one after the other
	}
	cfg := config.Worker{
		Name: "test", IsolatePath: iso.Path, IsolateCG: iso.CG, IsolateBoxRoot: iso.BoxRoot,
		Cores: cores[:n], BoxIDOffset: boxOffset, WorkDir: filepath.Join(dir, "work"),
		CacheDir: filepath.Join(dir, "cache"), CacheMaxBytes: 1 << 30,
	}
	boxOffset += n * 2 * sandbox.BoxesPerSlot
	e, err := NewExecutor(cfg, store, logging.Discard())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	h := &harness{t: t, exec: e, store: store, langs: reg, free: make(chan int, n)}
	for i := 0; i < n; i++ {
		h.free <- i
	}
	return h
}

// execute runs a job on an idle slot.
func (h *harness) execute(j *jobs.Job) *jobs.Result {
	slot := <-h.free
	defer func() { h.free <- slot }()
	return h.exec.Execute(context.Background(), slot, j)
}

func (h *harness) put(data []byte) string {
	info, err := h.store.PutBytes(context.Background(), data)
	if err != nil {
		h.t.Fatal(err)
	}
	return info.Digest
}

func (h *harness) putFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		h.t.Fatal(err)
	}
	return h.put(data)
}

func (h *harness) lang(id string) *langs.Language {
	l, ok := h.langs.Get(id)
	if !ok {
		h.t.Fatalf("language %s not configured", id)
	}
	return l
}

// batchJob describes a Batch submission.
type batchJob struct {
	lang     string
	files    map[string][]byte // submission files, names already resolved
	managers map[string][]byte
	params   map[string]any
	limits   jobs.Limits
}

func (h *harness) files(m map[string][]byte) []jobs.File {
	var out []jobs.File
	for name, data := range m {
		out = append(out, jobs.File{Name: name, Digest: h.put(data)})
	}
	return out
}

func (h *harness) job(kind jobs.Kind, taskType string, b batchJob) *jobs.Job {
	params, _ := json.Marshal(b.params)
	j := &jobs.Job{
		ID: "test", Kind: kind, TaskType: taskType, TaskTypeParams: params,
		Files: h.files(b.files), Managers: h.files(b.managers), Limits: b.limits,
	}
	if b.lang != "" {
		j.Language = h.lang(b.lang)
	}
	return j
}

// compileAndRun compiles the submission and, when it compiles, evaluates
// it on the given testcases.
func (h *harness) compileAndRun(taskType string, b batchJob, tcs []jobs.Testcase) (*jobs.Compilation, []jobs.Evaluation, error) {
	cj := h.job(jobs.KindCompile, taskType, b)
	res := h.execute(cj)
	if res.Error != "" {
		return nil, nil, errString(res.Error)
	}
	if !res.Compilation.Success {
		return res.Compilation, nil, nil
	}
	ej := h.job(jobs.KindEvaluate, taskType, b)
	ej.Executables = res.Compilation.Executables
	ej.Testcases = tcs
	er := h.execute(ej)
	if er.Error != "" {
		return res.Compilation, nil, errString(er.Error)
	}
	return res.Compilation, er.Evaluations, nil
}

func (h *harness) testcase(id int64, in, out string) jobs.Testcase {
	return jobs.Testcase{ID: id, Codename: string(rune('a' + id)), Input: h.put([]byte(in)), Output: h.put([]byte(out))}
}

type errString string

func (e errString) Error() string { return string(e) }
