package dispatcher

import (
	"context"
	"errors"
	"fmt"

	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/events"
	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/jackc/pgx/v5"
)

// newUserTest starts a user test on the task's live dataset.
func (d *Dispatcher) newUserTest(ctx context.Context, id int64) error {
	q := sqlc.New(d.pool)
	meta, err := q.GetUserTestMeta(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if meta.ActiveDatasetID == nil {
		return nil
	}
	return d.advanceUserTest(ctx, id, *meta.ActiveDatasetID)
}

func (d *Dispatcher) userTestJob(ctx context.Context, q *sqlc.Queries, id, dsID int64, gen int32, attempt int) (*jobs.Job, sqlc.GetUserTestMetaRow, error) {
	meta, err := q.GetUserTestMeta(ctx, id)
	if err != nil {
		return nil, meta, err
	}
	di, err := d.datasets.get(ctx, q, dsID)
	if err != nil {
		return nil, meta, err
	}
	files, err := q.ListUserTestFiles(ctx, id)
	if err != nil {
		return nil, meta, err
	}
	j := &jobs.Job{
		ID: newJobID(jobs.KindUserTest, id, dsID, gen, ""), Kind: jobs.KindUserTest, Attempt: attempt,
		Priority: int(queue.PriorityUserTest), UserTestID: id, DatasetID: dsID, Generation: gen,
		TaskType: di.ds.TaskType, TaskTypeParams: di.ds.TaskTypeParams, Managers: di.managers,
		Limits: di.limits(), Input: meta.InputDigest,
	}
	ext := ""
	if meta.Language != nil {
		l, ok := d.langs.Get(*meta.Language)
		if !ok {
			return nil, meta, fmt.Errorf("language %q is not configured", *meta.Language)
		}
		j.Language, ext = l, l.SourceExtension()
		j.Limits = j.Limits.ForLanguage(l)
	}
	sf := make([]sqlc.SubmissionFile, len(files))
	for i, f := range files {
		sf[i] = sqlc.SubmissionFile{Filename: f.Filename, Digest: f.Digest}
	}
	j.Files = resolveFiles(sf, ext)
	return j, meta, nil
}

func userTestEvent(meta sqlc.GetUserTestMetaRow, status string) events.Event {
	return events.Event{Type: events.TypeUserTest, ParticipationID: meta.ParticipationID, TaskID: meta.TaskID,
		UserTestID: meta.ID, Status: status}
}

func (d *Dispatcher) advanceUserTest(ctx context.Context, id, dsID int64) error {
	var eff effects
	err := db.InTx(ctx, d.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		if err := q.EnsureUserTestResult(ctx, sqlc.EnsureUserTestResultParams{UserTestID: id, DatasetID: dsID}); err != nil {
			return err
		}
		st, err := q.LockUserTestResult(ctx, sqlc.LockUserTestResultParams{UserTestID: id, DatasetID: dsID})
		if err != nil {
			return err
		}
		if st.CompletedAt != nil {
			return nil
		}
		j, meta, err := d.userTestJob(ctx, q, id, dsID, st.Generation, 0)
		if err != nil {
			msg := err.Error()
			return q.SetUserTestSystemError(ctx, sqlc.SetUserTestSystemErrorParams{UserTestID: id, DatasetID: dsID, SystemError: &msg})
		}
		eff.jobs = append(eff.jobs, queue.Item{Priority: queue.PriorityUserTest, Job: j})
		eff.events = append(eff.events, userTestEvent(meta, "compiling"))
		return q.SetUserTestJobsEnqueued(ctx, sqlc.SetUserTestJobsEnqueuedParams{UserTestID: id, DatasetID: dsID})
	})
	if err != nil {
		return err
	}
	d.apply(ctx, &eff)
	return nil
}

func (d *Dispatcher) handleUserTestResult(ctx context.Context, r *resultT) error {
	var eff effects
	err := db.InTx(ctx, d.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		st, err := q.LockUserTestResult(ctx, sqlc.LockUserTestResultParams{UserTestID: r.UserTestID, DatasetID: r.DatasetID})
		if errors.Is(err, pgx.ErrNoRows) {
			return errStale
		}
		if err != nil {
			return err
		}
		if st.Generation != r.Generation || st.CompletedAt != nil {
			return errStale
		}
		meta, err := q.GetUserTestMeta(ctx, r.UserTestID)
		if err != nil {
			return err
		}
		if r.Error != "" {
			if r.Attempt+1 >= d.opts.MaxAttempts {
				msg := fmt.Sprintf("user test job failed %d times: %s", r.Attempt+1, r.Error)
				eff.events = append(eff.events, userTestEvent(meta, "error"))
				return q.SetUserTestSystemError(ctx, sqlc.SetUserTestSystemErrorParams{UserTestID: r.UserTestID, DatasetID: r.DatasetID, SystemError: &msg})
			}
			j, _, err := d.userTestJob(ctx, q, r.UserTestID, r.DatasetID, st.Generation, r.Attempt+1)
			if err != nil {
				return err
			}
			eff.jobs = append(eff.jobs, queue.Item{Priority: queue.PriorityUserTest, Job: j})
			return q.SetUserTestJobsEnqueued(ctx, sqlc.SetUserTestJobsEnqueuedParams{UserTestID: r.UserTestID, DatasetID: r.DatasetID})
		}
		c := r.Compilation
		if c == nil {
			return errors.New("user test result without compilation")
		}
		outcome := "ok"
		if !c.Success {
			outcome = "fail"
		}
		if _, err := q.SetUserTestCompilation(ctx, sqlc.SetUserTestCompilationParams{
			UserTestID: r.UserTestID, DatasetID: r.DatasetID, CompilationOutcome: &outcome, CompilationText: c.Text,
			CompilationStdout: c.Stdout, CompilationStderr: c.Stderr, CompilationTime: &c.Time, CompilationWallTime: &c.WallTime,
			CompilationMemory: &c.Memory, Generation: st.Generation,
		}); err != nil {
			return err
		}
		if !c.Success || r.UserTest == nil {
			eff.events = append(eff.events, userTestEvent(meta, "done"))
			return nil
		}
		for _, e := range c.Executables {
			if err := q.RegisterBlob(ctx, sqlc.RegisterBlobParams{Digest: e.Digest, Size: e.Size, Description: "executable"}); err != nil {
				return err
			}
			if err := q.InsertUserTestExecutable(ctx, sqlc.InsertUserTestExecutableParams{UserTestID: r.UserTestID, DatasetID: r.DatasetID, Filename: e.Name, Digest: e.Digest}); err != nil {
				return err
			}
		}
		u := r.UserTest
		var out *string
		if u.Output != "" {
			out = &u.Output
			if err := q.RegisterBlob(ctx, sqlc.RegisterBlobParams{Digest: u.Output, Description: "user test output"}); err != nil {
				return err
			}
		}
		if _, err := q.SetUserTestEvaluation(ctx, sqlc.SetUserTestEvaluationParams{
			UserTestID: r.UserTestID, DatasetID: r.DatasetID, EvaluationText: u.Text, OutputDigest: out,
			ExecutionTime: &u.Time, ExecutionWallTime: &u.WallTime, ExecutionMemory: &u.Memory, ExitStatus: &u.ExitStatus,
			Generation: st.Generation,
		}); err != nil {
			return err
		}
		eff.events = append(eff.events, userTestEvent(meta, "done"))
		return nil
	})
	if errors.Is(err, errStale) {
		return nil
	}
	if err != nil {
		return err
	}
	d.apply(ctx, &eff)
	return nil
}
