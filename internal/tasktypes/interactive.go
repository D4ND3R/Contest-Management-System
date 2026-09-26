package tasktypes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/checkers"
	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
	"github.com/D4ND3R/Contest-Management-System/internal/sandbox"
)

// InteractiveParams configure the Interactive task type.
type InteractiveParams struct {
	// InteractorTimeLimitMs bounds the interactor's CPU time (default: 10 s
	// plus the contestant's time limit). The interactor's time is never
	// charged to the contestant.
	InteractorTimeLimitMs int64 `json:"interactor_time_limit_ms,omitempty"`
	// InteractorMemoryBytes bounds the interactor's memory (default 1 GiB).
	InteractorMemoryBytes int64 `json:"interactor_memory_bytes,omitempty"`
}

func parseInteractive(raw json.RawMessage) (*InteractiveParams, error) {
	p := &InteractiveParams{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, p); err != nil {
			return nil, fmt.Errorf("invalid Interactive parameters: %w", err)
		}
	}
	if p.InteractorTimeLimitMs < 0 || p.InteractorMemoryBytes < 0 {
		return nil, errors.New("interactor limits must not be negative")
	}
	return p, nil
}

// Interactive (ICPC / Codeforces style): an interactor provided by the task
// talks to ONE contestant process. The interactor's standard output is the
// contestant's standard input and vice versa (two anonymous pipes crossing
// the two sandboxes, so there is no open-order deadlock and end-of-file
// propagates when either side exits). The interactor runs in its own
// sandbox, with its own limits, and follows the testlib convention:
//
//	interactor argv: <input> <output> <answer>   (in-box paths)
//	stdin: contestant's stdout; stdout: contestant's stdin
//	exit code: 0 accepted, 1/2/4 wrong answer, 3 judge failure,
//	7 partial ("points X" in the message), 16+n: n percent;
//	the first stderr line is the message shown to the contestant.
//
// Verdict priority: a contestant that exceeded a limit (time, wall time,
// memory, output) gets that verdict — e.g. a contestant that never flushes
// blocks both sides and ends on the wall-clock limit; then a contestant
// crash (a signal other than SIGPIPE, or a non-zero exit) is a runtime
// error; otherwise the interactor decides, including when the contestant
// was killed by SIGPIPE because the interactor had already quit, or ended
// before the interaction was complete. An interactor that crashes, runs out
// of limits or reports a judge failure is a system error (retried, then
// reported to the administrators).
type Interactive struct{}

func init() { register("Interactive", Interactive{}) }

func (Interactive) sources(job *jobs.Job) (*sourceSet, error) {
	if job.Language == nil {
		return nil, errors.New("Interactive submissions need a language")
	}
	return newSourceSet(job.Language, job.Files, job.Managers, false, "")
}

func (it Interactive) Compile(ctx context.Context, env *Env, job *jobs.Job) (*jobs.Compilation, error) {
	if _, err := parseInteractive(job.TaskTypeParams); err != nil {
		return nil, err
	}
	s, err := it.sources(job)
	if err != nil {
		return nil, err
	}
	box, err := env.box(ctx, 0)
	if err != nil {
		return nil, err
	}
	return compile(ctx, env, box, s)
}

// interaction is the raw result of one run.
type interaction struct {
	user, interactor *sandbox.Result
	ibox             *sandbox.Box
}

func (it Interactive) interact(ctx context.Context, env *Env, job *jobs.Job, p *InteractiveParams, input, answer string) (*interaction, error) {
	s, err := it.sources(job)
	if err != nil {
		return nil, err
	}
	interactor, err := env.managerExecutable(ctx, job, "interactor")
	if err != nil {
		return nil, err
	}
	if err := env.Stage.Clear(); err != nil {
		return nil, infra("clear stage: %v", err)
	}
	in, err := env.stage(ctx, "input", input)
	if err != nil {
		return nil, err
	}
	ans := "/dev/null"
	if answer != "" {
		if ans, err = env.stage(ctx, "answer", answer); err != nil {
			return nil, err
		}
	}
	ibox, err := env.box(ctx, 0)
	if err != nil {
		return nil, err
	}
	if err := ibox.CopyIn(interactor, "interactor", 0o755); err != nil {
		return nil, infra("copy interactor: %v", err)
	}
	ubox, err := env.box(ctx, 2)
	if err != nil {
		return nil, err
	}
	for _, f := range job.Executables {
		if err := env.put(ctx, ubox, f.Name, f.Digest, 0o755); err != nil {
			return nil, err
		}
	}
	// toUser: interactor → contestant; toInteractor: contestant → interactor.
	toUserR, toUserW, err := os.Pipe()
	if err != nil {
		return nil, infra("pipe: %v", err)
	}
	toIntR, toIntW, err := os.Pipe()
	if err != nil {
		toUserR.Close()
		toUserW.Close()
		return nil, infra("pipe: %v", err)
	}
	// The worker's copies are closed once both sandboxes started, so that
	// each side sees end-of-file (or SIGPIPE) as soon as the other exits.
	var once sync.Once
	var started sync.WaitGroup
	started.Add(2)
	closeAll := func() {
		once.Do(func() { toUserR.Close(); toUserW.Close(); toIntR.Close(); toIntW.Close() })
	}
	go func() { started.Wait(); closeAll() }()
	afterStart := started.Done
	defer closeAll()

	userWall := WallLimit(job.Limits.TimeMs, job.Limits.WallTimeMs)
	itl := time.Duration(p.InteractorTimeLimitMs) * time.Millisecond
	if itl <= 0 {
		itl = 10*time.Second + time.Duration(job.Limits.TimeMs)*time.Millisecond
	}
	imem := p.InteractorMemoryBytes
	if imem <= 0 {
		imem = 1 << 30
	}
	out := &interaction{ibox: ibox}
	var wg sync.WaitGroup
	var ierr, uerr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		out.interactor, ierr = ibox.Run(ctx, &sandbox.Spec{
			Args:      []string{"./interactor", in, "tout", ans},
			StdinFile: toIntR, StdoutFile: toUserW, Stderr: ".interactor.err",
			Env: []string{"PATH=/usr/bin:/bin"}, Dirs: []sandbox.Dir{env.Stage.Dir()},
			Limits:     sandbox.Limits{CPUTime: itl, WallTime: max(2*itl, userWall+5*time.Second), Memory: imem, Processes: 4, FileSize: 64 << 20},
			AfterStart: afterStart,
		})
	}()
	go func() {
		defer wg.Done()
		out.user, uerr = run(ctx, env, ubox, runOptions{
			lang: job.Language, executables: job.Executables, main: s.main(), limits: job.Limits, noExecCopy: true,
			stdinFile: toUserR, stdoutFile: toIntW, afterStart: afterStart,
		})
	}()
	wg.Wait()
	if err := errors.Join(ierr, uerr); err != nil {
		return nil, err
	}
	return out, nil
}

// limitExceeded reports whether a run ended on a resource limit (or was
// stopped by the seccomp filter): its verdict stands whatever the
// interactor says.
func limitExceeded(r *sandbox.Result) bool {
	switch r.Status {
	case sandbox.StatusTimeout, sandbox.StatusWallTimeout, sandbox.StatusMemory, sandbox.StatusOutputLimit, sandbox.StatusSecurity:
		return true
	}
	return false
}

func (Interactive) judge(tc jobs.Testcase, o *interaction) (*jobs.Evaluation, error) {
	u := o.user
	ev := evaluationFromRun(tc, u)
	if limitExceeded(u) {
		ev.Text = executionText(u)
		return ev, nil
	}
	ir := o.interactor
	if ir.Status != sandbox.StatusOK && ir.Status != sandbox.StatusNonZero {
		return nil, fmt.Errorf("interactor failed: %s", executionText(ir))
	}
	stderr, _, _ := o.ibox.ReadFile(".interactor.err", 64<<10)
	res, err := checkers.ParseTestlib(ir.ExitCode, stderr)
	if err != nil {
		return nil, fmt.Errorf("interactor: %w", err)
	}
	brokenPipe := u.Status == sandbox.StatusSignal && u.Signal == int(syscall.SIGPIPE)
	if u.Status != sandbox.StatusOK && !brokenPipe {
		// A crash: the interactor only saw end-of-file.
		ev.Text = executionText(u)
		return ev, nil
	}
	// Normal end, or SIGPIPE because the interactor already quit: the
	// interactor's verdict stands.
	ev.ExitStatus, ev.ExitCode, ev.Signal = string(sandbox.StatusOK), 0, 0
	ev.Outcome, ev.Text = res.Score, res.Message
	return ev, nil
}

func (it Interactive) Evaluate(ctx context.Context, env *Env, job *jobs.Job, tc jobs.Testcase) (*jobs.Evaluation, error) {
	p, err := parseInteractive(job.TaskTypeParams)
	if err != nil {
		return nil, err
	}
	o, err := it.interact(ctx, env, job, p, tc.Input, tc.Output)
	if err != nil {
		return nil, err
	}
	return it.judge(tc, o)
}

// UserTest runs the interaction on the contestant's input (without an
// answer file); the output shown is what the interactor wrote to its
// output file.
func (it Interactive) UserTest(ctx context.Context, env *Env, job *jobs.Job) (*jobs.Compilation, *jobs.UserTestRun, error) {
	p, err := parseInteractive(job.TaskTypeParams)
	if err != nil {
		return nil, nil, err
	}
	comp, err := it.Compile(ctx, env, job)
	if err != nil || !comp.Success {
		return comp, nil, err
	}
	j := *job
	j.Executables = comp.Executables
	o, err := it.interact(ctx, env, &j, p, job.Input, "")
	if err != nil {
		return nil, nil, err
	}
	r := &jobs.UserTestRun{Time: o.user.CPUTime.Seconds(), WallTime: o.user.WallTime.Seconds(), Memory: o.user.Memory,
		ExitStatus: string(o.user.Status), Text: MsgExecutionOK}
	if ev, jerr := it.judge(jobs.Testcase{}, o); jerr != nil {
		r.ExitStatus, r.Text = string(sandbox.StatusSandboxError), jerr.Error()
	} else {
		r.ExitStatus, r.Text = ev.ExitStatus, ev.Text
	}
	if d, err := env.upload(ctx, o.ibox, "tout", 1<<20); err == nil {
		r.Output = d
	} else if errors.Is(err, errInfra) {
		return nil, nil, err
	}
	return comp, r, nil
}
