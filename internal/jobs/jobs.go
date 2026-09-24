// Package jobs defines the messages exchanged between the dispatcher and
// the workers. Jobs are self-contained: they carry every digest, limit and
// command a worker needs, so workers only talk to Redis and the blob store
// (never to PostgreSQL).
package jobs

import (
	"encoding/json"

	"github.com/D4ND3R/Contest-Management-System/internal/langs"
)

// Kind of work.
type Kind string

const (
	// KindCompile compiles a submission for one dataset.
	KindCompile Kind = "compile"
	// KindEvaluate evaluates a submission on one or more testcases.
	KindEvaluate Kind = "evaluate"
	// KindUserTest compiles (if needed) and runs a user test.
	KindUserTest Kind = "usertest"
)

// File is a named blob.
type File struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

// Limits of the contestant's program on a dataset.
type Limits struct {
	TimeMs      int64 `json:"time_ms,omitempty"`
	WallTimeMs  int64 `json:"wall_time_ms,omitempty"`
	MemoryBytes int64 `json:"memory_bytes,omitempty"`
	OutputBytes int64 `json:"output_bytes,omitempty"`
	Processes   int   `json:"processes,omitempty"`
}

// Testcase to evaluate.
type Testcase struct {
	ID       int64  `json:"id"`
	Codename string `json:"codename"`
	Input    string `json:"input"`
	Output   string `json:"output"`
}

// Job is a unit of work for a worker.
type Job struct {
	ID      string `json:"id"`
	Kind    Kind   `json:"kind"`
	Attempt int    `json:"attempt"`

	SubmissionID int64 `json:"submission_id,omitempty"`
	UserTestID   int64 `json:"user_test_id,omitempty"`
	DatasetID    int64 `json:"dataset_id"`
	Generation   int32 `json:"generation"`

	TaskType       string          `json:"task_type"`
	TaskTypeParams json.RawMessage `json:"task_type_params,omitempty"`
	// Language is nil for tasks without code (OutputOnly).
	Language *langs.Language `json:"language,omitempty"`

	Files       []File     `json:"files,omitempty"`       // submitted files
	Managers    []File     `json:"managers,omitempty"`    // dataset managers (grader, checker, ...)
	Executables []File     `json:"executables,omitempty"` // compilation artifacts
	Limits      Limits     `json:"limits"`
	Testcases   []Testcase `json:"testcases,omitempty"`
	// Input is the digest of a user test's input.
	Input string `json:"input,omitempty"`
}

// Compilation outcome.
type Compilation struct {
	Success     bool    `json:"success"`
	Text        string  `json:"text"`
	Stdout      string  `json:"stdout,omitempty"`
	Stderr      string  `json:"stderr,omitempty"`
	Time        float64 `json:"time"`
	WallTime    float64 `json:"wall_time"`
	Memory      int64   `json:"memory"`
	Executables []File  `json:"executables,omitempty"`
}

// Evaluation of one testcase.
type Evaluation struct {
	TestcaseID int64   `json:"testcase_id"`
	Codename   string  `json:"codename"`
	Outcome    float64 `json:"outcome"` // fraction of the testcase score in [0,1]
	Text       string  `json:"text"`
	Time       float64 `json:"time"`
	WallTime   float64 `json:"wall_time"`
	Memory     int64   `json:"memory"`
	ExitStatus string  `json:"exit_status"`
	ExitCode   int     `json:"exit_code,omitempty"`
	Signal     int     `json:"signal,omitempty"`
}

// UserTestRun is the outcome of running a user test.
type UserTestRun struct {
	Output     string  `json:"output,omitempty"` // digest of the produced output
	Text       string  `json:"text"`
	Time       float64 `json:"time"`
	WallTime   float64 `json:"wall_time"`
	Memory     int64   `json:"memory"`
	ExitStatus string  `json:"exit_status"`
}

// Result of a job, published by the worker.
type Result struct {
	JobID        string `json:"job_id"`
	Kind         Kind   `json:"kind"`
	Attempt      int    `json:"attempt"`
	Worker       string `json:"worker"`
	SubmissionID int64  `json:"submission_id,omitempty"`
	UserTestID   int64  `json:"user_test_id,omitempty"`
	DatasetID    int64  `json:"dataset_id"`
	Generation   int32  `json:"generation"`

	Compilation *Compilation `json:"compilation,omitempty"`
	Evaluations []Evaluation `json:"evaluations,omitempty"`
	UserTest    *UserTestRun `json:"user_test,omitempty"`
	// Error reports an infrastructure failure (the job should be retried);
	// it never describes the contestant's program.
	Error string `json:"error,omitempty"`
	// DurationMs is the worker-side processing time (for metrics).
	DurationMs int64 `json:"duration_ms"`
}

// ForJob returns an empty result addressed to the job's target.
func ForJob(j *Job, worker string) *Result {
	return &Result{JobID: j.ID, Kind: j.Kind, Attempt: j.Attempt, Worker: worker,
		SubmissionID: j.SubmissionID, UserTestID: j.UserTestID, DatasetID: j.DatasetID, Generation: j.Generation}
}
