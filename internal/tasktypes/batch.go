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

// BatchParams configure the Batch task type (datasets.task_type_params).
type BatchParams struct {
	// Compilation: "alone" (default) or "grader" (the manager
	// grader.<ext> is compiled together with the submission).
	Compilation string `json:"compilation,omitempty"`
	// InputFile / OutputFile: file names for file-based I/O; empty means
	// standard input / standard output.
	InputFile  string `json:"input_file,omitempty"`
	OutputFile string `json:"output_file,omitempty"`
	CheckerParams
}

func parseBatch(raw json.RawMessage) (*BatchParams, error) {
	p := &BatchParams{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, p); err != nil {
			return nil, fmt.Errorf("invalid Batch parameters: %w", err)
		}
	}
	if p.Compilation == "" {
		p.Compilation = "alone"
	}
	if p.Compilation != "alone" && p.Compilation != "grader" {
		return nil, fmt.Errorf("invalid Batch compilation %q", p.Compilation)
	}
	return p, nil
}

// Batch: the program reads a testcase (stdin or a file) and writes an
// answer (stdout or a file) that a checker compares with the expected one.
type Batch struct{}

func init() { register("Batch", Batch{}) }

func (Batch) sources(job *jobs.Job, p *BatchParams) (*sourceSet, error) {
	if job.Language == nil {
		return nil, errors.New("Batch submissions need a language")
	}
	return newSourceSet(job.Language, job.Files, job.Managers, p.Compilation == "grader", "grader")
}

func (b Batch) Compile(ctx context.Context, env *Env, job *jobs.Job) (*jobs.Compilation, error) {
	p, err := parseBatch(job.TaskTypeParams)
	if err != nil {
		return nil, err
	}
	s, err := b.sources(job, p)
	if err != nil {
		return nil, err
	}
	box, err := env.box(ctx, 0)
	if err != nil {
		return nil, err
	}
	return compile(ctx, env, box, s)
}

// execute runs the program on one input (a digest) and leaves the output
// in the returned box under the returned name.
func (b Batch) execute(ctx context.Context, env *Env, job *jobs.Job, p *BatchParams, input string) (*sandbox.Box, *sandbox.Result, string, error) {
	s, err := b.sources(job, p)
	if err != nil {
		return nil, nil, "", err
	}
	box, err := env.box(ctx, 0)
	if err != nil {
		return nil, nil, "", err
	}
	if err := env.Stage.Clear(); err != nil {
		return nil, nil, "", infra("clear stage: %v", err)
	}
	o := runOptions{lang: job.Language, executables: job.Executables, main: s.main(), limits: job.Limits}
	if p.InputFile == "" {
		in, err := env.stage(ctx, "input", input)
		if err != nil {
			return nil, nil, "", err
		}
		o.stdin = in
		o.dirs = []sandbox.Dir{env.Stage.Dir()}
	} else if err := env.put(ctx, box, p.InputFile, input, 0o444); err != nil {
		return nil, nil, "", err
	}
	output := p.OutputFile
	if output == "" {
		output = runStdout
		o.stdout = runStdout
	}
	res, err := run(ctx, env, box, o)
	if err != nil {
		return nil, nil, "", err
	}
	return box, res, output, nil
}

func (b Batch) Evaluate(ctx context.Context, env *Env, job *jobs.Job, tc jobs.Testcase) (*jobs.Evaluation, error) {
	p, err := parseBatch(job.TaskTypeParams)
	if err != nil {
		return nil, err
	}
	box, res, output, err := b.execute(ctx, env, job, p, tc.Input)
	if err != nil {
		return nil, err
	}
	ev := evaluationFromRun(tc, res)
	if res.Status != sandbox.StatusOK {
		ev.Text = executionText(res)
		return ev, nil
	}
	if f, _, err := box.OpenOutput(output); err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, sandbox.ErrUnsafeFile) {
			ev.Text = fmt.Sprintf(MsgMissingOutput, p.OutputFile)
			return ev, nil
		}
		return nil, infra("open output: %v", err)
	} else {
		f.Close()
	}
	o, err := check(ctx, env, job, &p.CheckerParams, tc, box, output)
	if err != nil {
		return nil, err
	}
	ev.Outcome, ev.Text = o.Score, o.Message
	return ev, nil
}

func (b Batch) UserTest(ctx context.Context, env *Env, job *jobs.Job) (*jobs.Compilation, *jobs.UserTestRun, error) {
	p, err := parseBatch(job.TaskTypeParams)
	if err != nil {
		return nil, nil, err
	}
	comp, err := b.Compile(ctx, env, job)
	if err != nil || !comp.Success {
		return comp, nil, err
	}
	j := *job
	j.Executables = comp.Executables
	box, res, output, err := b.execute(ctx, env, &j, p, job.Input)
	if err != nil {
		return nil, nil, err
	}
	run := &jobs.UserTestRun{
		Time: res.CPUTime.Seconds(), WallTime: res.WallTime.Seconds(), Memory: res.Memory,
		ExitStatus: string(res.Status), Text: executionText(res),
	}
	limit := job.Limits.OutputBytes
	if limit <= 0 {
		limit = 64 << 20
	}
	if d, err := env.upload(ctx, box, output, limit); err == nil {
		run.Output = d
	} else if errors.Is(err, errInfra) {
		return nil, nil, err
	}
	return comp, run, nil
}
