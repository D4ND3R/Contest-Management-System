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

// stored are the rows of a package whose files are already in the blob
// store.
type stored struct {
	tests       []sqlc.CreateTestcasesParams
	managers    []sqlc.CreateManagersParams
	statements  []sqlc.UpsertStatementParams
	attachments []sqlc.UpsertAttachmentParams
	examples    []sqlc.InsertTaskExampleParams
}

// storeFiles puts the package's files in the blob store: the testcases and
// managers, and with content the statements, attachments and examples.
func storeFiles(ctx context.Context, store blob.Store, p *Package, withContent bool) (*stored, error) {
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
	s := &stored{tests: make([]sqlc.CreateTestcasesParams, len(p.Tests)), managers: make([]sqlc.CreateManagersParams, len(p.Managers))}
	for i, t := range p.Tests {
		in, err := put(t.Input)
		if err != nil {
			return nil, err
		}
		out, err := put(t.Output)
		if err != nil {
			return nil, err
		}
		s.tests[i] = sqlc.CreateTestcasesParams{Codename: t.Codename, Public: t.Public, InputDigest: in, OutputDigest: out}
	}
	for i, m := range p.Managers {
		d, err := put(m)
		if err != nil {
			return nil, err
		}
		s.managers[i] = sqlc.CreateManagersParams{Filename: m.Name, Digest: d}
	}
	if !withContent {
		return s, nil
	}
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
		s.examples = append(s.examples, ex)
	}
	for _, st := range p.Statements {
		d, err := put(st.File)
		if err != nil {
			return nil, err
		}
		s.statements = append(s.statements, sqlc.UpsertStatementParams{Language: st.Language, Digest: d, ContentType: st.ContentType})
	}
	for _, a := range p.Attachments {
		d, err := put(a)
		if err != nil {
			return nil, err
		}
		s.attachments = append(s.attachments, sqlc.UpsertAttachmentParams{Filename: a.Name, Digest: d})
	}
	return s, nil
}

// writeDataset stores the testcases and managers of a dataset without any.
func (s *stored) writeDataset(ctx context.Context, q *sqlc.Queries, datasetID int64) error {
	for i := range s.tests {
		s.tests[i].DatasetID = datasetID
	}
	if _, err := q.CreateTestcases(ctx, s.tests); err != nil {
		return err
	}
	for i := range s.managers {
		s.managers[i].DatasetID = datasetID
	}
	_, err := q.CreateManagers(ctx, s.managers)
	return err
}

// writeContent stores the statements, attachments and examples of a task.
func (s *stored) writeContent(ctx context.Context, q *sqlc.Queries, taskID int64) error {
	for _, st := range s.statements {
		st.TaskID = taskID
		if _, err := q.UpsertStatement(ctx, st); err != nil {
			return err
		}
	}
	for _, a := range s.attachments {
		a.TaskID = taskID
		if _, err := q.UpsertAttachment(ctx, a); err != nil {
			return err
		}
	}
	for _, e := range s.examples {
		e.TaskID = taskID
		if _, err := q.InsertTaskExample(ctx, e); err != nil {
			return err
		}
	}
	return nil
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
	newTask := o.TaskID == 0
	withContent := newTask || o.Sync
	files, err := storeFiles(ctx, store, p, withContent)
	if err != nil {
		return nil, err
	}
	res := &ImportResult{NewTask: newTask}
	err = db.InTx(ctx, pool, func(tx pgx.Tx, q *sqlc.Queries) error {
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
		u := c.DatasetUpdate(sqlc.UpdateDatasetParams{}, len(p.Tests))
		dp := db.NewDatasetParams(task.ID, res.Dataset)
		dp.TimeLimitMs, dp.WallTimeLimitMs, dp.MemoryLimitBytes = u.TimeLimitMs, u.WallTimeLimitMs, u.MemoryLimitBytes
		dp.OutputLimitBytes, dp.SourceSizeLimitBytes, dp.ProcessLimit = u.OutputLimitBytes, u.SourceSizeLimitBytes, u.ProcessLimit
		dp.TaskType, dp.TaskTypeParams, dp.ScoreType, dp.ScoreTypeParams = u.TaskType, u.TaskTypeParams, u.ScoreType, u.ScoreTypeParams
		dp.ShortCircuit = u.ShortCircuit
		ds, err := q.CreateDataset(ctx, dp)
		if err != nil {
			return err
		}
		res.DatasetID = ds.ID
		if err := files.writeDataset(ctx, q, ds.ID); err != nil {
			return err
		}
		if !withContent {
			if o.Live {
				return q.SetActiveDataset(ctx, sqlc.SetActiveDatasetParams{ID: task.ID, ActiveDatasetID: &ds.ID})
			}
			return nil
		}
		if !newTask {
			up := c.TaskUpdate(task, true)
			if _, err := q.UpdateTask(ctx, up); err != nil {
				return err
			}
			if err := files.clearContent(ctx, q, task.ID); err != nil {
				return err
			}
		}
		if err := files.writeContent(ctx, q, task.ID); err != nil {
			return err
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

// clearContent removes the statements, attachments and examples of a task
// that the package does not have (writeContent then stores the package's).
func (s *stored) clearContent(ctx context.Context, q *sqlc.Queries, taskID int64) error {
	keep := map[string]bool{}
	for _, st := range s.statements {
		keep[st.Language] = true
	}
	old, err := q.ListStatements(ctx, taskID)
	if err != nil {
		return err
	}
	for _, st := range old {
		if !keep[st.Language] {
			if err := q.DeleteStatement(ctx, sqlc.DeleteStatementParams{TaskID: taskID, Language: st.Language}); err != nil {
				return err
			}
		}
	}
	keep = map[string]bool{}
	for _, a := range s.attachments {
		keep[a.Filename] = true
	}
	atts, err := q.ListAttachments(ctx, taskID)
	if err != nil {
		return err
	}
	for _, a := range atts {
		if !keep[a.Filename] {
			if err := q.DeleteAttachment(ctx, sqlc.DeleteAttachmentParams{TaskID: taskID, Filename: a.Filename}); err != nil {
				return err
			}
		}
	}
	return q.DeleteTaskExamples(ctx, taskID)
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
