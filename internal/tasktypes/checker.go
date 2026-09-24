package tasktypes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/checkers"
	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
	"github.com/D4ND3R/Contest-Management-System/internal/sandbox"
)

// CheckerParams select how outputs are judged. They are shared by every
// task type that compares outputs.
type CheckerParams struct {
	// Checker: "white_diff" (default), "exact", "float", "custom" (CMS
	// protocol: checker input correct contestant; stdout = score in [0,1],
	// stderr = message) or "testlib" (checker input contestant correct;
	// verdict from the exit code).
	Checker     string  `json:"checker,omitempty"`
	FloatAbsTol float64 `json:"float_abs_tol,omitempty"`
	FloatRelTol float64 `json:"float_rel_tol,omitempty"`
	// CheckerTimeLimitMs bounds custom checkers (default 10 s).
	CheckerTimeLimitMs int64 `json:"checker_time_limit_ms,omitempty"`
	// CheckerMemoryBytes bounds custom checkers (default 1 GiB).
	CheckerMemoryBytes int64 `json:"checker_memory_bytes,omitempty"`
}

func (p *CheckerParams) custom() bool { return p.Checker == "custom" || p.Checker == "testlib" }

// check judges the contestant output stored in box (box-relative name)
// against tc. Custom checkers run in box 1 of the slot.
func check(ctx context.Context, env *Env, job *jobs.Job, p *CheckerParams, tc jobs.Testcase, box *sandbox.Box, output string) (checkers.Outcome, error) {
	if !p.custom() {
		expPath, err := env.fetch(ctx, tc.Output)
		if err != nil {
			return checkers.Outcome{}, err
		}
		exp, err := os.Open(expPath)
		if err != nil {
			return checkers.Outcome{}, infra("open expected output: %v", err)
		}
		defer exp.Close()
		act, _, err := box.OpenOutput(output)
		if err != nil {
			return checkers.Outcome{}, infra("open contestant output: %v", err)
		}
		defer act.Close()
		o, err := checkers.Compare(checkers.Kind(p.Checker), exp, act, checkers.Params{AbsTol: p.FloatAbsTol, RelTol: p.FloatRelTol})
		if err != nil {
			return checkers.Outcome{}, fmt.Errorf("compare: %w", err)
		}
		return o, nil
	}
	return runChecker(ctx, env, job, p, tc, box, output)
}

// runChecker runs a custom checker in its own box. The testcase input and
// the correct output are staged read-only; the contestant output is copied.
func runChecker(ctx context.Context, env *Env, job *jobs.Job, p *CheckerParams, tc jobs.Testcase, progBox *sandbox.Box, output string) (checkers.Outcome, error) {
	exe, err := env.checkerExecutable(ctx, job)
	if err != nil {
		return checkers.Outcome{}, err
	}
	// Copy the contestant output out of the program box before resetting
	// anything (OpenOutput refuses symlinks and special files).
	src, _, err := progBox.OpenOutput(output)
	if err != nil {
		return checkers.Outcome{}, infra("open contestant output: %v", err)
	}
	defer src.Close()

	cbox, err := env.box(ctx, 1)
	if err != nil {
		return checkers.Outcome{}, err
	}
	if err := cbox.CopyIn(exe, "checker", 0o755); err != nil {
		return checkers.Outcome{}, infra("copy checker: %v", err)
	}
	if err := cbox.WriteFrom("contestant", src, 0o644); err != nil {
		return checkers.Outcome{}, infra("copy output: %v", err)
	}
	if err := env.Stage.Clear(); err != nil {
		return checkers.Outcome{}, infra("clear stage: %v", err)
	}
	in, err := env.stage(ctx, "input", tc.Input)
	if err != nil {
		return checkers.Outcome{}, err
	}
	correct, err := env.stage(ctx, "correct", tc.Output)
	if err != nil {
		return checkers.Outcome{}, err
	}
	args := []string{"./checker", in, correct, "contestant"}
	if p.Checker == "testlib" {
		args = []string{"./checker", in, "contestant", correct}
	}
	tl := time.Duration(p.CheckerTimeLimitMs) * time.Millisecond
	if tl <= 0 {
		tl = 10 * time.Second
	}
	mem := p.CheckerMemoryBytes
	if mem <= 0 {
		mem = 1 << 30
	}
	lim := sandbox.Limits{CPUTime: tl, WallTime: 2*tl + time.Second, Memory: mem, Processes: 1, FileSize: 16 << 20}
	res, err := cbox.Run(ctx, &sandbox.Spec{
		Args: args, Stdout: ".checker.out", Stderr: ".checker.err",
		Env: []string{"PATH=/usr/bin:/bin"}, Dirs: []sandbox.Dir{env.Stage.Dir()}, Limits: lim,
	})
	if err != nil {
		return checkers.Outcome{}, infra("run checker: %v", err)
	}
	stdout, _, _ := cbox.ReadFile(".checker.out", 64<<10)
	stderr, _, _ := cbox.ReadFile(".checker.err", 64<<10)
	if p.Checker == "testlib" {
		if res.Status != sandbox.StatusOK && res.Status != sandbox.StatusNonZero {
			return checkers.Outcome{}, fmt.Errorf("checker failed: %s", executionText(res))
		}
		return checkers.ParseTestlib(res.ExitCode, stderr)
	}
	if res.Status != sandbox.StatusOK {
		return checkers.Outcome{}, fmt.Errorf("checker failed: %s: %s", executionText(res), stderr)
	}
	return checkers.ParseCMSChecker(stdout, stderr)
}

// checkerExecutable returns a host path to the dataset's checker: the
// "checker" manager (a compiled binary) or, when only a C++ source is
// provided ("checker.cpp", e.g. testlib checkers from Polygon), the result
// of compiling it once per worker.
func (e *Env) checkerExecutable(ctx context.Context, job *jobs.Job) (string, error) {
	var bin, src *jobs.File
	var headers []jobs.File
	for i := range job.Managers {
		m := &job.Managers[i]
		switch {
		case m.Name == "checker":
			bin = m
		case m.Name == "checker.cpp":
			src = m
		case filepath.Ext(m.Name) == ".h" || filepath.Ext(m.Name) == ".hpp":
			headers = append(headers, *m)
		}
	}
	if bin != nil {
		return e.fetch(ctx, bin.Digest)
	}
	if src == nil {
		return "", fmt.Errorf("dataset has no checker (manager \"checker\" or \"checker.cpp\")")
	}
	return e.Checkers.build(ctx, e, *src, headers)
}

// build compiles a checker source with the host toolchain inside the
// sandbox and caches the executable.
func (c *CheckerCache) build(ctx context.Context, env *Env, src jobs.File, headers []jobs.File) (string, error) {
	key := src.Digest
	for _, h := range headers {
		key += h.Name + h.Digest
	}
	key = shortHash(key)
	c.mu.Lock()
	defer c.mu.Unlock()
	if p, ok := c.built[key]; ok {
		return p, nil
	}
	box, err := env.box(ctx, 2)
	if err != nil {
		return "", err
	}
	if err := env.put(ctx, box, "checker.cpp", src.Digest, 0o644); err != nil {
		return "", err
	}
	for _, h := range headers {
		if err := env.put(ctx, box, h.Name, h.Digest, 0o644); err != nil {
			return "", err
		}
	}
	gpp, err := exec.LookPath("g++")
	if err != nil {
		return "", fmt.Errorf("cannot compile checker.cpp: g++ not found")
	}
	res, err := box.Run(ctx, &sandbox.Spec{
		Args:   []string{gpp, "-O2", "-std=gnu++17", "-static", "-o", "checker", "checker.cpp"},
		Stderr: ".err", Env: []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/tmp"},
		Limits: sandbox.Limits{CPUTime: 60 * time.Second, WallTime: 120 * time.Second, Memory: 2 << 30, Processes: 32, FileSize: 256 << 20},
	})
	if err != nil {
		return "", infra("compile checker: %v", err)
	}
	if res.Status != sandbox.StatusOK {
		msg, _, _ := box.ReadFile(".err", 4096)
		return "", fmt.Errorf("checker.cpp does not compile: %s", msg)
	}
	f, _, err := box.OpenOutput("checker")
	if err != nil {
		return "", infra("read compiled checker: %v", err)
	}
	defer f.Close()
	dst := filepath.Join(c.dir, key)
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o555)
	if err != nil {
		return "", err
	}
	if _, err := out.ReadFrom(f); err != nil {
		out.Close()
		return "", err
	}
	out.Close()
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	c.built[key] = dst
	return dst, nil
}

func shortHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:16])
}
