package problempkg

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	// Description names the new dataset instead of the package's dataset
	// field (made unique among the task's datasets either way).
	Description string
	// Sync, with TaskID, makes the task match the package (the contest
	// configuration kept in Git is the source of truth): its settings from
	// problem.yaml, and exactly the package's statements, attachments and
	// examples.
	Sync bool
	// Live, with TaskID, makes the new dataset the live one.
	Live bool
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
	withContent := newTask || o.Sync
	var statements []sqlc.UpsertStatementParams
	var attachments []sqlc.UpsertAttachmentParams
	var examples []sqlc.InsertTaskExampleParams
	if withContent {
		for _, e := range p.Examples {
			in, err := put(e.Input)
			if err != nil {
				return nil, err
			}
			out, err := put(e.Output)
			if err != nil {
				return nil, err
			}
			ex := sqlc.InsertTaskExampleParams{InputDigest: in, OutputDigest: out}
			if e.Note != nil {
				note, err := readAll(e.Note.src, 64<<10)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", e.Note.Path, err)
				}
				ex.Note = strings.TrimSpace(string(note))
			}
			examples = append(examples, ex)
		}
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
			tp.FeedbackLevel, tp.ScoreMode, tp.ScorePrecision = c.Feedback, c.ScoreMode, int32(c.Precision())
			tp.HideCheckerMessages = c.CheckerMessages == "hide"
			if o.ContestID != nil {
				// What the package leaves unset comes from the contest.
				ct, err := q.GetContest(ctx, *o.ContestID)
				if err != nil {
					return err
				}
				if c.ScoreMode == "" {
					tp.ScoreMode = ct.DefaultScoreMode
				}
				if c.ScorePrecision == nil {
					tp.ScorePrecision = ct.ScorePrecision
				}
			}
			if tp.ScoreMode == "" {
				tp.ScoreMode = "max_subtask"
			}
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
		desc := c.Dataset
		if o.Description != "" {
			desc = o.Description
		}
		res.Dataset = uniqueDescription(desc, existing)
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
		if !withContent {
			if o.Live {
				return q.SetActiveDataset(ctx, sqlc.SetActiveDatasetParams{ID: task.ID, ActiveDatasetID: &ds.ID})
			}
			return nil
		}
		if !newTask {
			if err := syncTask(ctx, q, task, c, statements, attachments); err != nil {
				return err
			}
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
		for _, e := range examples {
			e.TaskID = task.ID
			if _, err := q.InsertTaskExample(ctx, e); err != nil {
				return err
			}
		}
		if !newTask && !o.Live {
			return nil
		}
		return q.SetActiveDataset(ctx, sqlc.SetActiveDatasetParams{ID: task.ID, ActiveDatasetID: &ds.ID})
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// syncTask applies problem.yaml's task settings to an existing task and
// removes the statements, attachments and examples the package no longer
// has (the caller then stores the package's).
func syncTask(ctx context.Context, q *sqlc.Queries, t sqlc.Task, c *Config, statements []sqlc.UpsertStatementParams,
	attachments []sqlc.UpsertAttachmentParams) error {
	up := sqlc.UpdateTaskParams{ID: t.ID, ContestID: t.ContestID, Num: t.Num, Name: t.Name, Title: c.Title,
		PrimaryStatements: nonNil(c.PrimaryStatements), SubmissionFormat: t.SubmissionFormat, TokenMode: t.TokenMode,
		TokenMaxNumber: t.TokenMaxNumber, TokenMinIntervalS: t.TokenMinIntervalS, TokenGenInitial: t.TokenGenInitial,
		TokenGenNumber: t.TokenGenNumber, TokenGenIntervalS: t.TokenGenIntervalS, TokenGenMax: t.TokenGenMax,
		MaxSubmissionNumber: t.MaxSubmissionNumber, MaxUserTestNumber: t.MaxUserTestNumber,
		MinSubmissionIntervalS: t.MinSubmissionIntervalS, MinUserTestIntervalS: t.MinUserTestIntervalS,
		FeedbackLevel: c.Feedback, ScorePrecision: t.ScorePrecision, ScoreMode: t.ScoreMode, Languages: nonNil(c.Languages),
		HideCheckerMessages: c.CheckerMessages == "hide"}
	if c.SubmissionFormat != nil {
		up.SubmissionFormat = c.SubmissionFormat
	}
	if c.ScoreMode != "" {
		up.ScoreMode = c.ScoreMode
	}
	if c.ScorePrecision != nil {
		up.ScorePrecision = int32(c.Precision())
	}
	if _, err := q.UpdateTask(ctx, up); err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, s := range statements {
		keep[s.Language] = true
	}
	old, err := q.ListStatements(ctx, t.ID)
	if err != nil {
		return err
	}
	for _, s := range old {
		if !keep[s.Language] {
			if err := q.DeleteStatement(ctx, sqlc.DeleteStatementParams{TaskID: t.ID, Language: s.Language}); err != nil {
				return err
			}
		}
	}
	keep = map[string]bool{}
	for _, a := range attachments {
		keep[a.Filename] = true
	}
	atts, err := q.ListAttachments(ctx, t.ID)
	if err != nil {
		return err
	}
	for _, a := range atts {
		if !keep[a.Filename] {
			if err := q.DeleteAttachment(ctx, sqlc.DeleteAttachmentParams{TaskID: t.ID, Filename: a.Filename}); err != nil {
				return err
			}
		}
	}
	return q.DeleteTaskExamples(ctx, t.ID)
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
