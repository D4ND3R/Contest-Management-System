package problempkg

import (
	"context"
	"errors"
	"fmt"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNameTaken is returned when a new task would reuse a task name.
var ErrNameTaken = errors.New("a task with this name already exists")

// ImportOptions choose where a package goes.
type ImportOptions struct {
	// TaskID adds the package as a new (not live) dataset of an existing
	// task; the task's settings, statements and attachments are kept.
	TaskID int64
	// ContestID appends a new task to a contest; nil leaves it outside any
	// contest (unpublished) until an administrator adds it.
	ContestID *int64
}

// ImportResult is what an import created.
type ImportResult struct {
	TaskID, DatasetID int64
	NewTask           bool
	Dataset           string
}

// Import stores a package that has no errors. Files go to the blob store
// first (content-addressed, so an aborted import only leaves unreferenced
// blobs for the garbage collector); every row is then written in one
// transaction, so nothing is half-created.
func Import(ctx context.Context, pool *pgxpool.Pool, store blob.Store, p *Package, o ImportOptions) (*ImportResult, error) {
	if !p.OK() || p.Config == nil {
		return nil, errors.New("the package has errors")
	}
	c := p.Config
	put := func(f File) (string, error) {
		rd, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("%s: %w", f.Path, err)
		}
		defer rd.Close()
		info, err := store.Put(ctx, rd)
		if err != nil {
			return "", fmt.Errorf("%s: %w", f.Path, err)
		}
		return info.Digest, nil
	}
	tests := make([]sqlc.CreateTestcasesParams, len(p.Tests))
	for i, t := range p.Tests {
		in, err := put(t.Input)
		if err != nil {
			return nil, err
		}
		out, err := put(t.Output)
		if err != nil {
			return nil, err
		}
		tests[i] = sqlc.CreateTestcasesParams{Codename: t.Codename, Public: t.Public, InputDigest: in, OutputDigest: out}
	}
	managers := make([]sqlc.CreateManagersParams, len(p.Managers))
	for i, m := range p.Managers {
		d, err := put(m)
		if err != nil {
			return nil, err
		}
		managers[i] = sqlc.CreateManagersParams{Filename: m.Name, Digest: d}
	}
	newTask := o.TaskID == 0
	var statements []sqlc.UpsertStatementParams
	var attachments []sqlc.UpsertAttachmentParams
	if newTask {
		for _, s := range p.Statements {
			d, err := put(s.File)
			if err != nil {
				return nil, err
			}
			statements = append(statements, sqlc.UpsertStatementParams{Language: s.Language, Digest: d, ContentType: s.ContentType})
		}
		for _, a := range p.Attachments {
			d, err := put(a)
			if err != nil {
				return nil, err
			}
			attachments = append(attachments, sqlc.UpsertAttachmentParams{Filename: a.Name, Digest: d})
		}
	}

	res := &ImportResult{NewTask: newTask}
	err := db.InTx(ctx, pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		var task sqlc.Task
		var err error
		if newTask {
			if _, err := q.GetTaskByName(ctx, c.Name); err == nil {
				return ErrNameTaken
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			tp := db.NewTaskParams(c.Name, c.Title)
			tp.PrimaryStatements = nonNil(c.PrimaryStatements)
			tp.SubmissionFormat = c.SubmissionFormat
			if tp.SubmissionFormat == nil {
				tp.SubmissionFormat = []string{c.Name + ".%l"}
				if c.Type == "output_only" {
					tp.SubmissionFormat = []string{} // one file per testcase, from the pattern
				}
			}
			tp.Languages = nonNil(c.Languages)
			tp.FeedbackLevel, tp.ScoreMode, tp.ScorePrecision = c.Feedback, c.ScoreMode, int32(c.ScorePrecision)
			if o.ContestID != nil {
				next, err := q.AdminNextTaskNum(ctx, o.ContestID)
				if err != nil {
					return err
				}
				tp.ContestID, tp.Num = o.ContestID, &next
			}
			if task, err = q.CreateTask(ctx, tp); err != nil {
				return err
			}
		} else if task, err = q.GetTask(ctx, o.TaskID); err != nil {
			return err
		}
		res.TaskID = task.ID
		existing, err := q.ListDatasetsByTask(ctx, task.ID)
		if err != nil {
			return err
		}
		res.Dataset = uniqueDescription(c.Dataset, existing)
		dp := db.NewDatasetParams(task.ID, res.Dataset)
		tl := int32(ms(c.TimeLimit))
		dp.TimeLimitMs = &tl
		if c.TimeLimit == 0 {
			dp.TimeLimitMs = nil
		}
		if c.WallTimeLimit > 0 {
			wl := int32(ms(c.WallTimeLimit))
			dp.WallTimeLimitMs = &wl
		}
		if c.MemoryLimit > 0 {
			m := mib(c.MemoryLimit)
			dp.MemoryLimitBytes = &m
		} else {
			dp.MemoryLimitBytes = nil
		}
		if c.OutputLimit > 0 {
			dp.OutputLimitBytes = mib(c.OutputLimit)
		}
		if c.SourceSizeLimit > 0 {
			sz := int64(c.SourceSizeLimit * 1024)
			dp.SourceSizeLimitBytes = &sz
		}
		dp.ProcessLimit = int32(c.ProcessLimit)
		dp.TaskType, dp.TaskTypeParams = c.TaskType(), c.TaskTypeParams()
		dp.ScoreType, dp.ScoreTypeParams = c.ScoreType(), c.ScoreTypeParams(len(p.Tests))
		ds, err := q.CreateDataset(ctx, dp)
		if err != nil {
			return err
		}
		res.DatasetID = ds.ID
		for i := range tests {
			tests[i].DatasetID = ds.ID
		}
		if _, err := q.CreateTestcases(ctx, tests); err != nil {
			return err
		}
		for i := range managers {
			managers[i].DatasetID = ds.ID
		}
		if _, err := q.CreateManagers(ctx, managers); err != nil {
			return err
		}
		if !newTask {
			return nil
		}
		for _, s := range statements {
			s.TaskID = task.ID
			if _, err := q.UpsertStatement(ctx, s); err != nil {
				return err
			}
		}
		for _, a := range attachments {
			a.TaskID = task.ID
			if _, err := q.UpsertAttachment(ctx, a); err != nil {
				return err
			}
		}
		return q.SetActiveDataset(ctx, sqlc.SetActiveDatasetParams{ID: task.ID, ActiveDatasetID: &ds.ID})
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

// uniqueDescription avoids clashing with the task's dataset descriptions.
func uniqueDescription(want string, existing []sqlc.Dataset) string {
	taken := map[string]bool{}
	for _, d := range existing {
		taken[d.Description] = true
	}
	desc := want
	for i := 2; taken[desc]; i++ {
		desc = fmt.Sprintf("%s (%d)", want, i)
	}
	return desc
}
