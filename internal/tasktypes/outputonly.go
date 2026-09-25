package tasktypes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/D4ND3R/Contest-Management-System/internal/checkers"
	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
)

// OutputOnlyParams configure the OutputOnly task type.
type OutputOnlyParams struct {
	// OutputPattern names the submitted file of a testcase; "%s" is replaced
	// by the testcase codename (default "output_%s.txt", as in CMS).
	OutputPattern string `json:"output_pattern,omitempty"`
	// MergePrevious: testcases missing from a submission take the output
	// file with the best result among the contestant's previous
	// submissions (applied by the contest web server when submitting).
	MergePrevious bool `json:"merge_previous,omitempty"`
	CheckerParams
}

// OutputOnlyConfig returns the file name pattern and the merge option of
// an OutputOnly dataset.
func OutputOnlyConfig(raw json.RawMessage) (pattern string, merge bool, err error) {
	p, err := parseOutputOnly(raw)
	if err != nil {
		return "", false, err
	}
	return p.OutputPattern, p.MergePrevious, nil
}

func parseOutputOnly(raw json.RawMessage) (*OutputOnlyParams, error) {
	p := &OutputOnlyParams{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, p); err != nil {
			return nil, fmt.Errorf("invalid OutputOnly parameters: %w", err)
		}
	}
	if p.OutputPattern == "" {
		p.OutputPattern = "output_%s.txt"
	}
	if strings.Count(p.OutputPattern, "%s") != 1 {
		return nil, fmt.Errorf("output_pattern must contain exactly one %%s")
	}
	return p, nil
}

// OutputFileName returns the submitted file name for a testcase codename.
func OutputFileName(pattern, codename string) string {
	if pattern == "" {
		pattern = "output_%s.txt"
	}
	return strings.Replace(pattern, "%s", codename, 1)
}

// OutputOnly: contestants submit the output of every testcase; nothing is
// compiled or executed except, optionally, a custom checker.
type OutputOnly struct{}

func init() { register("OutputOnly", OutputOnly{}) }

// MsgFileNotSubmitted is the text of testcases without a submitted output.
const MsgFileNotSubmitted = "File not submitted"

func (OutputOnly) Compile(ctx context.Context, env *Env, job *jobs.Job) (*jobs.Compilation, error) {
	return &jobs.Compilation{Success: true, Text: "No compilation needed"}, nil
}

func (OutputOnly) Evaluate(ctx context.Context, env *Env, job *jobs.Job, tc jobs.Testcase) (*jobs.Evaluation, error) {
	p, err := parseOutputOnly(job.TaskTypeParams)
	if err != nil {
		return nil, err
	}
	ev := &jobs.Evaluation{TestcaseID: tc.ID, Codename: tc.Codename, ExitStatus: "ok"}
	name := OutputFileName(p.OutputPattern, tc.Codename)
	var digest string
	for _, f := range job.Files {
		if f.Name == name {
			digest = f.Digest
		}
	}
	if digest == "" {
		ev.Text = MsgFileNotSubmitted
		return ev, nil
	}
	if digest == tc.Output && !p.custom() {
		// Byte-identical content: every comparator accepts it.
		ev.Outcome, ev.Text = 1, checkers.MsgCorrect
		return ev, nil
	}
	box, err := env.box(ctx, 0)
	if err != nil {
		return nil, err
	}
	if err := env.put(ctx, box, "output.txt", digest, 0o644); err != nil {
		return nil, err
	}
	o, err := check(ctx, env, job, &p.CheckerParams, tc, box, "output.txt")
	if err != nil {
		return nil, err
	}
	ev.Outcome, ev.Text = o.Score, o.Message
	return ev, nil
}

func (OutputOnly) UserTest(ctx context.Context, env *Env, job *jobs.Job) (*jobs.Compilation, *jobs.UserTestRun, error) {
	return nil, nil, errors.New("user tests are not available for OutputOnly tasks")
}
