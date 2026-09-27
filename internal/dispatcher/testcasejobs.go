package dispatcher

// Testcase jobs (SPEC_MIN §13, §14): an administrator types an input or
// has a generator write it, and a reference solution writes the output.
// Each step is a user test owned by the administrator; when one ends, the
// job moves on in the same transaction that stores the run's result, so a
// crash never loses or repeats a step (the sweeper restarts runs whose
// jobs were lost).

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
	"github.com/jackc/pgx/v5"
)

// emptyDigest is the SHA-256 of no bytes: the output of testcases whose
// output is empty (the admin web stores the blob when it creates the job).
const emptyDigest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// runEnd is how a run of a testcase job ended: an error, or an exit status
// with the digest of what it wrote.
type runEnd struct {
	err                  string
	output, status, text string
}

func (e runEnd) failure() string {
	switch {
	case e.err != "":
		return e.err
	case e.status != "ok":
		return fmt.Sprintf("the program did not end well (%s): %s", e.status, e.text)
	case e.output == "":
		return "the program wrote no output"
	}
	return ""
}

// compileFailure describes a compilation that failed, with the start of
// the compiler's messages.
func compileFailure(c *jobs.Compilation) string {
	msg := strings.TrimSpace(c.Stderr)
	if msg == "" {
		msg = strings.TrimSpace(c.Stdout)
	}
	if msg == "" {
		msg = c.Text
	}
	return "compilation failed: " + truncate(msg, 1500)
}

// testcaseJobStep moves the testcase job waiting for user test id, if
// any: a generated input goes to the reference solution (or becomes a
// testcase with an empty output); a solution's output completes the
// testcase; a failure stops the job with its reason.
func (d *Dispatcher) testcaseJobStep(ctx context.Context, q *sqlc.Queries, id int64, end runEnd, eff *effects) error {
	job, err := q.LockTestcaseJobByUserTest(ctx, &id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if msg := end.failure(); msg != "" {
		what := "generator"
		if job.State == "output" {
			what = "reference solution"
		}
		return q.SetTestcaseJobStep(ctx, sqlc.SetTestcaseJobStepParams{ID: job.ID, State: "failed",
			InputDigest: job.InputDigest, UserTestID: job.UserTestID, Error: truncate(what+": "+msg, 600)})
	}
	input := job.InputDigest
	if job.State == "input" {
		generated := end.output
		input = &generated
		if job.Output == "solution" {
			runID, err := d.startSolutionRun(ctx, q, job, end.output)
			if err != nil {
				return q.SetTestcaseJobStep(ctx, sqlc.SetTestcaseJobStepParams{ID: job.ID, State: "failed",
					InputDigest: input, UserTestID: job.UserTestID, Error: truncate(err.Error(), 600)})
			}
			eff.userTests = append(eff.userTests, runID)
			return q.SetTestcaseJobStep(ctx, sqlc.SetTestcaseJobStepParams{ID: job.ID, State: "output",
				InputDigest: input, UserTestID: &runID})
		}
		end.output = emptyDigest
	}
	if input == nil {
		return errors.New("testcase job without an input")
	}
	if _, err := q.UpsertTestcase(ctx, sqlc.UpsertTestcaseParams{DatasetID: job.DatasetID, Codename: job.Codename,
		Public: job.Public, InputDigest: *input, OutputDigest: end.output}); err != nil {
		return err
	}
	eff.datasets = append(eff.datasets, job.DatasetID)
	return q.DeleteTestcaseJob(ctx, job.ID)
}

// startSolutionRun creates the run of the job's reference solution on
// input (started after the commit).
func (d *Dispatcher) startSolutionRun(ctx context.Context, q *sqlc.Queries, job sqlc.TestcaseJob, input string) (int64, error) {
	if job.SolutionID == nil {
		return 0, errors.New("the reference solution was deleted")
	}
	sub, err := q.GetSubmission(ctx, *job.SolutionID)
	if err != nil {
		return 0, fmt.Errorf("the reference solution was deleted: %w", err)
	}
	files, err := q.ListSubmissionFiles(ctx, sub.ID)
	if err != nil {
		return 0, err
	}
	ut, err := q.CreateAdminUserTest(ctx, sqlc.CreateAdminUserTestParams{TaskID: sub.TaskID, AdminID: job.AdminID,
		DatasetID: &job.DatasetID, Language: sub.Language, InputDigest: input})
	if err != nil {
		return 0, err
	}
	params := make([]sqlc.CreateUserTestFilesParams, len(files))
	for i, f := range files {
		params[i] = sqlc.CreateUserTestFilesParams{UserTestID: ut.ID, Filename: f.Filename, Digest: f.Digest}
	}
	if _, err := q.CreateUserTestFiles(ctx, params); err != nil {
		return 0, err
	}
	return ut.ID, nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	for n > 0 && !isRuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
