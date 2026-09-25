package tasktypes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/checkers"
	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
	"github.com/D4ND3R/Contest-Management-System/internal/sandbox"
)

// CommunicationParams configure the Communication task type.
type CommunicationParams struct {
	// NumProcesses is the number of contestant processes (1..4).
	NumProcesses int `json:"num_processes,omitempty"`
	// Compilation: "stub" (default; the manager-provided stub.<ext> is
	// compiled with the submission) or "alone".
	Compilation string `json:"compilation,omitempty"`
	// UserIO: "fifos" (default; FIFO paths are passed as arguments) or
	// "std_io" (the process's stdin/stdout are the FIFOs).
	UserIO string `json:"user_io,omitempty"`
	// Manager limits (defaults: CPU time 10 s plus the contestants' limit,
	// 1 GiB of memory).
	ManagerTimeLimitMs int64 `json:"manager_time_limit_ms,omitempty"`
	ManagerMemoryBytes int64 `json:"manager_memory_bytes,omitempty"`
	// LimitsMode: "per_process" (default; every process gets the task's
	// time and memory limits) or "total" (additionally, the sum of the
	// processes' CPU time and of their peak memory must fit the limits).
	LimitsMode string `json:"limits_mode,omitempty"`
}

func parseCommunication(raw json.RawMessage) (*CommunicationParams, error) {
	p := &CommunicationParams{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, p); err != nil {
			return nil, fmt.Errorf("invalid Communication parameters: %w", err)
		}
	}
	if p.NumProcesses == 0 {
		p.NumProcesses = 1
	}
	if p.NumProcesses < 1 || p.NumProcesses > sandbox.BoxesPerSlot-2 {
		return nil, fmt.Errorf("num_processes must be between 1 and %d", sandbox.BoxesPerSlot-2)
	}
	if p.Compilation == "" {
		p.Compilation = "stub"
	}
	if p.UserIO == "" {
		p.UserIO = "fifos"
	}
	if p.Compilation != "stub" && p.Compilation != "alone" {
		return nil, fmt.Errorf("invalid Communication compilation %q", p.Compilation)
	}
	if p.UserIO != "fifos" && p.UserIO != "std_io" {
		return nil, fmt.Errorf("invalid Communication user_io %q", p.UserIO)
	}
	if p.LimitsMode == "" {
		p.LimitsMode = "per_process"
	}
	if p.LimitsMode != "per_process" && p.LimitsMode != "total" {
		return nil, fmt.Errorf("invalid Communication limits_mode %q (per_process, total)", p.LimitsMode)
	}
	return p, nil
}

// Communication: a manager (provided by the task, run sandboxed) talks to
// NumProcesses contestant processes, each in its own sandbox, through a pair
// of FIFOs per process (CMS protocol):
//
//	manager  argv: u2m_0 m2u_0 [u2m_1 m2u_1 ...]; stdin: testcase input;
//	         stdout: score in [0,1]; stderr: message
//	process  argv: m2u u2m [index]      (user_io = "fifos")
//	         stdin = m2u, stdout = u2m   (user_io = "std_io")
//
// Every process gets the task's limits; with limits_mode "total" the sum of
// their CPU times and of their peak memory must also fit. The reported time
// is the sum of the contestant processes' CPU time and the memory their
// maximum (their sum in "total" mode). Contestant processes never see the
// testcase files.
type Communication struct{}

func init() { register("Communication", Communication{}) }

func (Communication) sources(job *jobs.Job, p *CommunicationParams) (*sourceSet, error) {
	if job.Language == nil {
		return nil, errors.New("Communication submissions need a language")
	}
	return newSourceSet(job.Language, job.Files, job.Managers, p.Compilation == "stub", "stub")
}

func (c Communication) Compile(ctx context.Context, env *Env, job *jobs.Job) (*jobs.Compilation, error) {
	p, err := parseCommunication(job.TaskTypeParams)
	if err != nil {
		return nil, err
	}
	s, err := c.sources(job, p)
	if err != nil {
		return nil, err
	}
	box, err := env.box(ctx, 0)
	if err != nil {
		return nil, err
	}
	return compile(ctx, env, box, s)
}

// commOutcome is the result of one interaction.
type commOutcome struct {
	users   []*sandbox.Result
	manager *sandbox.Result
	mbox    *sandbox.Box
}

// prepareFifos creates one directory per process with the two FIFOs.
func prepareFifos(dir string, n int) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	for i := 0; i < n; i++ {
		d := filepath.Join(dir, strconv.Itoa(i))
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
		for _, name := range []string{"u2m", "m2u"} {
			p := filepath.Join(d, name)
			if err := syscall.Mkfifo(p, 0o666); err != nil {
				return err
			}
			if err := os.Chmod(p, 0o666); err != nil { // umask
				return err
			}
		}
	}
	return nil
}

func (c Communication) interact(ctx context.Context, env *Env, job *jobs.Job, p *CommunicationParams, input string) (*commOutcome, error) {
	s, err := c.sources(job, p)
	if err != nil {
		return nil, err
	}
	manager, err := env.managerExecutable(ctx, job, "manager")
	if err != nil {
		return nil, err
	}
	n := p.NumProcesses
	if err := prepareFifos(env.FifoDir, n); err != nil {
		return nil, infra("fifos: %v", err)
	}
	if err := env.Stage.Clear(); err != nil {
		return nil, infra("clear stage: %v", err)
	}
	in, err := env.stage(ctx, "input", input)
	if err != nil {
		return nil, err
	}

	// Manager in box 0 with every FIFO directory; contestants in boxes 2..n+1
	// with only their own.
	mbox, err := env.box(ctx, 0)
	if err != nil {
		return nil, err
	}
	if err := mbox.CopyIn(manager, "manager", 0o755); err != nil {
		return nil, infra("copy manager: %v", err)
	}
	margs := []string{"./manager"}
	mdirs := []sandbox.Dir{env.Stage.Dir()}
	for i := 0; i < n; i++ {
		inside := "/fifo" + strconv.Itoa(i)
		mdirs = append(mdirs, sandbox.Dir{Inside: inside, Outside: filepath.Join(env.FifoDir, strconv.Itoa(i)), RW: true})
		margs = append(margs, inside+"/u2m", inside+"/m2u")
	}
	ubox := make([]*sandbox.Box, n)
	for i := 0; i < n; i++ {
		if ubox[i], err = env.box(ctx, 2+i); err != nil {
			return nil, err
		}
		for _, f := range job.Executables {
			if err := env.put(ctx, ubox[i], f.Name, f.Digest, 0o755); err != nil {
				return nil, err
			}
		}
	}

	userWall := WallLimit(job.Limits.TimeMs, job.Limits.WallTimeMs)
	mtl := time.Duration(p.ManagerTimeLimitMs) * time.Millisecond
	if mtl <= 0 {
		mtl = 10*time.Second + time.Duration(job.Limits.TimeMs)*time.Millisecond*time.Duration(n)
	}
	mmem := p.ManagerMemoryBytes
	if mmem <= 0 {
		mmem = 1 << 30
	}
	out := &commOutcome{users: make([]*sandbox.Result, n), mbox: mbox}
	var wg sync.WaitGroup
	errs := make([]error, n+1)
	wg.Add(n + 1)
	go func() {
		defer wg.Done()
		out.manager, errs[n] = mbox.Run(ctx, &sandbox.Spec{
			Args: margs, Stdin: in, Stdout: ".manager.out", Stderr: ".manager.err",
			Env: []string{"PATH=/usr/bin:/bin"}, Dirs: mdirs,
			Limits: sandbox.Limits{CPUTime: mtl, WallTime: max(2*mtl, userWall+5*time.Second), Memory: mmem, Processes: 4, FileSize: 16 << 20},
		})
	}()
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			o := runOptions{
				lang: job.Language, executables: job.Executables, main: s.main(), limits: job.Limits, noExecCopy: true,
				dirs: []sandbox.Dir{{Inside: "/fifo", Outside: filepath.Join(env.FifoDir, strconv.Itoa(i)), RW: true}},
			}
			if p.UserIO == "std_io" {
				o.stdin, o.stdout = "/fifo/m2u", "/fifo/u2m"
			} else {
				o.args = []string{"/fifo/m2u", "/fifo/u2m"}
			}
			if n > 1 {
				o.args = append(o.args, strconv.Itoa(i))
			}
			out.users[i], errs[i] = run(ctx, env, ubox[i], o)
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return out, nil
}

// judge turns an interaction into an evaluation (or an error when the
// manager failed without the contestant being at fault).
func (c Communication) judge(tc jobs.Testcase, o *commOutcome, p *CommunicationParams, lim jobs.Limits) (*jobs.Evaluation, error) {
	ev := &jobs.Evaluation{TestcaseID: tc.ID, Codename: tc.Codename, ExitStatus: string(sandbox.StatusOK)}
	var failed *sandbox.Result
	var totalMem int64
	for _, u := range o.users {
		ev.Time += u.CPUTime.Seconds()
		ev.WallTime = max(ev.WallTime, u.WallTime.Seconds())
		ev.Memory = max(ev.Memory, u.Memory)
		totalMem += u.Memory
		if u.Status == sandbox.StatusOK || failed != nil {
			continue
		}
		// A broken pipe after the manager finished is the manager's choice.
		if o.manager.Status == sandbox.StatusOK && u.Status == sandbox.StatusSignal && u.Signal == int(syscall.SIGPIPE) {
			continue
		}
		failed = u
	}
	if o.manager.Status != sandbox.StatusOK {
		// Contestants only blocked (waiting on a FIFO) → the manager is at fault.
		if failed == nil || failed.Status == sandbox.StatusWallTimeout {
			return nil, fmt.Errorf("manager failed: %s", executionText(o.manager))
		}
	}
	if failed != nil {
		ev.ExitStatus, ev.ExitCode, ev.Signal = string(failed.Status), failed.ExitCode, failed.Signal
		ev.Text = executionText(failed)
		return ev, nil
	}
	if p.LimitsMode == "total" {
		ev.Memory = totalMem
		switch {
		case lim.TimeMs > 0 && ev.Time > float64(lim.TimeMs)/1000:
			ev.ExitStatus, ev.Text = string(sandbox.StatusTimeout), MsgTimeout
			return ev, nil
		case lim.MemoryBytes > 0 && totalMem > lim.MemoryBytes:
			ev.ExitStatus, ev.Text = string(sandbox.StatusMemory), executionText(&sandbox.Result{Status: sandbox.StatusMemory})
			return ev, nil
		}
	}
	stdout, _, _ := o.mbox.ReadFile(".manager.out", 64<<10)
	stderr, _, _ := o.mbox.ReadFile(".manager.err", 64<<10)
	res, err := checkers.ParseCMSChecker(stdout, stderr)
	if err != nil {
		return nil, fmt.Errorf("manager output: %w", err)
	}
	ev.Outcome, ev.Text = res.Score, res.Message
	return ev, nil
}

func (c Communication) Evaluate(ctx context.Context, env *Env, job *jobs.Job, tc jobs.Testcase) (*jobs.Evaluation, error) {
	p, err := parseCommunication(job.TaskTypeParams)
	if err != nil {
		return nil, err
	}
	o, err := c.interact(ctx, env, job, p, tc.Input)
	if err != nil {
		return nil, err
	}
	return c.judge(tc, o, p, job.Limits)
}

// UserTest runs the interaction on the contestant's input; the output shown
// is the manager's standard output.
func (c Communication) UserTest(ctx context.Context, env *Env, job *jobs.Job) (*jobs.Compilation, *jobs.UserTestRun, error) {
	p, err := parseCommunication(job.TaskTypeParams)
	if err != nil {
		return nil, nil, err
	}
	comp, err := c.Compile(ctx, env, job)
	if err != nil || !comp.Success {
		return comp, nil, err
	}
	j := *job
	j.Executables = comp.Executables
	o, err := c.interact(ctx, env, &j, p, job.Input)
	if err != nil {
		return nil, nil, err
	}
	ev, jerr := c.judge(jobs.Testcase{}, o, p, job.Limits)
	r := &jobs.UserTestRun{ExitStatus: string(sandbox.StatusOK), Text: MsgExecutionOK}
	if jerr != nil {
		r.ExitStatus, r.Text = string(sandbox.StatusSandboxError), jerr.Error()
	} else {
		r.Time, r.WallTime, r.Memory, r.ExitStatus = ev.Time, ev.WallTime, ev.Memory, ev.ExitStatus
		if ev.ExitStatus != string(sandbox.StatusOK) {
			r.Text = ev.Text
		}
	}
	if d, err := env.upload(ctx, o.mbox, ".manager.out", 1<<20); err == nil {
		r.Output = d
	} else if errors.Is(err, errInfra) {
		return nil, nil, err
	}
	return comp, r, nil
}
