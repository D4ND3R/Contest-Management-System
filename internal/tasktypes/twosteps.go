package tasktypes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
	"github.com/D4ND3R/Contest-Management-System/internal/sandbox"
)

// TwoStepsParams configure the TwoSteps task type.
type TwoStepsParams struct {
	// Manager is the basename of the manager source compiled with the
	// submission (default "manager"): it contains main() and dispatches to
	// the contestant's first or second step according to argv[1] ("0"/"1").
	Manager string `json:"manager,omitempty"`
	CheckerParams
}

func parseTwoSteps(raw json.RawMessage) (*TwoStepsParams, error) {
	p := &TwoStepsParams{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, p); err != nil {
			return nil, fmt.Errorf("invalid TwoSteps parameters: %w", err)
		}
	}
	if p.Manager == "" {
		p.Manager = "manager"
	}
	return p, nil
}

// TwoSteps: the submission (e.g. an encoder and a decoder) is compiled with
// a manager into one executable that runs twice. The first run reads the
// testcase and writes an intermediate message; the second run reads only
// that message and writes the answer, which is then checked. Each run has
// the full time and memory limits; the reported time is the sum and the
// memory the maximum. The intermediate message is bounded by the output
// limit, so the second step cannot see anything the first did not send.
type TwoSteps struct{}

func init() { register("TwoSteps", TwoSteps{}) }

const twoStepsMessage = "message.bin"

func (TwoSteps) sources(job *jobs.Job, p *TwoStepsParams) (*sourceSet, error) {
	if job.Language == nil {
		return nil, errors.New("TwoSteps submissions need a language")
	}
	return newSourceSet(job.Language, job.Files, job.Managers, true, p.Manager)
}

func (t TwoSteps) Compile(ctx context.Context, env *Env, job *jobs.Job) (*jobs.Compilation, error) {
	p, err := parseTwoSteps(job.TaskTypeParams)
	if err != nil {
		return nil, err
	}
	s, err := t.sources(job, p)
	if err != nil {
		return nil, err
	}
	box, err := env.box(ctx, 0)
	if err != nil {
		return nil, err
	}
	return compile(ctx, env, box, s)
}

// steps runs both steps on input; it returns the box holding the final
// output, the combined result and the failing step's result if any.
func (t TwoSteps) steps(ctx context.Context, env *Env, job *jobs.Job, p *TwoStepsParams, input string) (*sandbox.Box, *sandbox.Result, error) {
	s, err := t.sources(job, p)
	if err != nil {
		return nil, nil, err
	}
	main := s.main()
	// Step 1: input -> message.
	box1, err := env.box(ctx, 0)
	if err != nil {
		return nil, nil, err
	}
	if err := env.Stage.Clear(); err != nil {
		return nil, nil, infra("clear stage: %v", err)
	}
	in, err := env.stage(ctx, "input", input)
	if err != nil {
		return nil, nil, err
	}
	r1, err := run(ctx, env, box1, runOptions{
		lang: job.Language, executables: job.Executables, main: main, limits: job.Limits,
		stdin: in, stdout: twoStepsMessage, args: []string{"0"}, dirs: []sandbox.Dir{env.Stage.Dir()},
	})
	if err != nil {
		return nil, nil, err
	}
	if r1.Status != sandbox.StatusOK {
		return box1, r1, nil
	}
	msg, _, err := box1.OpenOutput(twoStepsMessage)
	if err != nil {
		return nil, nil, infra("read first step output: %v", err)
	}
	defer msg.Close()
	// Step 2: message -> answer, in a different box that never sees the input.
	box2, err := env.box(ctx, 3)
	if err != nil {
		return nil, nil, err
	}
	if err := box2.WriteFrom(twoStepsMessage, msg, 0o444); err != nil {
		return nil, nil, infra("copy message: %v", err)
	}
	r2, err := run(ctx, env, box2, runOptions{
		lang: job.Language, executables: job.Executables, main: main, limits: job.Limits,
		stdin: twoStepsMessage, stdout: runStdout, args: []string{"1"},
	})
	if err != nil {
		return nil, nil, err
	}
	combined := *r2
	combined.CPUTime += r1.CPUTime
	combined.WallTime += r1.WallTime
	combined.Memory = max(r1.Memory, r2.Memory)
	return box2, &combined, nil
}

func (t TwoSteps) Evaluate(ctx context.Context, env *Env, job *jobs.Job, tc jobs.Testcase) (*jobs.Evaluation, error) {
	p, err := parseTwoSteps(job.TaskTypeParams)
	if err != nil {
		return nil, err
	}
	box, res, err := t.steps(ctx, env, job, p, tc.Input)
	if err != nil {
		return nil, err
	}
	ev := evaluationFromRun(tc, res)
	if res.Status != sandbox.StatusOK {
		ev.Text = executionText(res)
		return ev, nil
	}
	o, err := check(ctx, env, job, &p.CheckerParams, tc, box, runStdout)
	if err != nil {
		return nil, err
	}
	ev.Outcome, ev.Text = o.Score, o.Message
	return ev, nil
}

func (t TwoSteps) UserTest(ctx context.Context, env *Env, job *jobs.Job) (*jobs.Compilation, *jobs.UserTestRun, error) {
	p, err := parseTwoSteps(job.TaskTypeParams)
	if err != nil {
		return nil, nil, err
	}
	comp, err := t.Compile(ctx, env, job)
	if err != nil || !comp.Success {
		return comp, nil, err
	}
	j := *job
	j.Executables = comp.Executables
	box, res, err := t.steps(ctx, env, &j, p, job.Input)
	if err != nil {
		return nil, nil, err
	}
	r := &jobs.UserTestRun{Time: res.CPUTime.Seconds(), WallTime: res.WallTime.Seconds(), Memory: res.Memory,
		ExitStatus: string(res.Status), Text: executionText(res)}
	if res.Status == sandbox.StatusOK {
		if d, err := env.upload(ctx, box, runStdout, max(job.Limits.OutputBytes, 1<<20)); err == nil {
			r.Output = d
		} else if errors.Is(err, errInfra) {
			return nil, nil, err
		} else if !errors.Is(err, os.ErrNotExist) {
			r.Text = err.Error()
		}
	}
	return comp, r, nil
}
