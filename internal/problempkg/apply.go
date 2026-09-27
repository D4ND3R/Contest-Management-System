package problempkg

// Changing an existing task in place: a package that fills a task (its
// settings, statements, files and testcases) and problem.yaml edited in
// the administration (docs/en/creating-a-problem.md).

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/scoring"
	"github.com/D4ND3R/Contest-Management-System/internal/tasktypes"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PublicMatcher reports whether public_tests makes a codename public.
func (c *Config) PublicMatcher() func(string) bool {
	var res []*regexp.Regexp
	for _, re := range c.PublicTests {
		if r, err := regexp.Compile("^(?:" + re + ")$"); err == nil {
			res = append(res, r)
		}
	}
	return func(code string) bool {
		for _, r := range res {
			if r.MatchString(code) {
				return true
			}
		}
		return false
	}
}

// Validate checks the configuration for a dataset whose testcases are
// codes: the values, the task type parameters and the scoring.
func (c *Config) Validate(codes []string) []string {
	errs := c.Check()
	if len(errs) > 0 {
		return errs
	}
	if err := tasktypes.ValidateParams(c.TaskType(), c.TaskTypeParams()); err != nil {
		errs = append(errs, err.Error())
	}
	if len(codes) > 0 {
		public, pub := c.PublicMatcher(), make([]bool, len(codes))
		for i, code := range codes {
			pub[i] = public(code)
		}
		if _, err := scoring.New(c.ScoreType(), c.ScoreTypeParams(len(codes)), codes, pub, c.Precision()); err != nil {
			errs = append(errs, fmt.Sprintf("scoring: %v", err))
		}
	}
	return errs
}

// TaskUpdate is t with the task settings of problem.yaml. keepName keeps
// the task's name (contest configurations in Git name their tasks). What
// problem.yaml leaves unset (score mode and precision, submission files)
// stays as it is, except that a default submission format follows the
// name and the type.
func (c *Config) TaskUpdate(t sqlc.Task, keepName bool) sqlc.UpdateTaskParams {
	u := db.TaskToUpdate(t)
	if !keepName {
		u.Name = c.Name
	}
	u.Title = c.Title
	u.PrimaryStatements, u.Languages = nonNil(c.PrimaryStatements), nonNil(c.Languages)
	u.FeedbackLevel = c.Feedback
	u.HideCheckerMessages = c.CheckerMessages == "hide"
	if c.ScoreMode != "" {
		u.ScoreMode = c.ScoreMode
	}
	if c.ScorePrecision != nil {
		u.ScorePrecision = int32(*c.ScorePrecision)
	}
	switch {
	case c.SubmissionFormat != nil:
		u.SubmissionFormat = c.SubmissionFormat
	case len(t.SubmissionFormat) == 0 || slices.Equal(t.SubmissionFormat, []string{t.Name + ".%l"}):
		u.SubmissionFormat = []string{u.Name + ".%l"}
		if c.Type == "output_only" {
			u.SubmissionFormat = []string{} // one file per testcase, from the pattern
		}
	}
	return u
}

// DatasetUpdate is u with the dataset settings of problem.yaml for nTests
// testcases; the description and the background judging stay.
func (c *Config) DatasetUpdate(u sqlc.UpdateDatasetParams, nTests int) sqlc.UpdateDatasetParams {
	u.TimeLimitMs, u.WallTimeLimitMs, u.MemoryLimitBytes, u.SourceSizeLimitBytes = nil, nil, nil, nil
	if c.TimeLimit > 0 {
		tl := int32(ms(c.TimeLimit))
		u.TimeLimitMs = &tl
	}
	if c.WallTimeLimit > 0 {
		wl := int32(ms(c.WallTimeLimit))
		u.WallTimeLimitMs = &wl
	}
	if c.MemoryLimit > 0 {
		m := mib(c.MemoryLimit)
		u.MemoryLimitBytes = &m
	}
	u.OutputLimitBytes = 64 << 20
	if c.OutputLimit > 0 {
		u.OutputLimitBytes = mib(c.OutputLimit)
	}
	if c.SourceSizeLimit > 0 {
		sz := int64(c.SourceSizeLimit * 1024)
		u.SourceSizeLimitBytes = &sz
	}
	u.ProcessLimit = int32(c.ProcessLimit)
	u.TaskType, u.TaskTypeParams = c.TaskType(), c.TaskTypeParams()
	u.ScoreType, u.ScoreTypeParams = c.ScoreType(), c.ScoreTypeParams(nTests)
	u.ShortCircuit = c.ShortCircuit && (c.Scoring == "group_min" || c.Scoring == "group_mul")
	return u
}

// nameFree reports whether no task other than taskID is called name.
func nameFree(ctx context.Context, q *sqlc.Queries, name string, taskID int64) (bool, error) {
	other, err := q.GetTaskByName(ctx, name)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return true, nil
	case err != nil:
		return false, err
	}
	return other.ID == taskID, nil
}

// ApplyConfig writes problem.yaml edited in the administration to a task
// and one of its datasets: the task settings, the dataset settings and
// which testcases are public. The caller validated c (Validate).
func ApplyConfig(ctx context.Context, q *sqlc.Queries, t sqlc.Task, d sqlc.Dataset, c *Config, codes []string) error {
	if d.TaskID != t.ID {
		return fmt.Errorf("dataset %d is not a dataset of %s", d.ID, t.Name)
	}
	if free, err := nameFree(ctx, q, c.Name, t.ID); err != nil {
		return err
	} else if !free {
		return ErrNameTaken
	}
	if _, err := q.UpdateTask(ctx, c.TaskUpdate(t, false)); err != nil {
		return err
	}
	if _, err := q.UpdateDataset(ctx, c.DatasetUpdate(db.DatasetToUpdate(d), len(codes))); err != nil {
		return err
	}
	public, pub := c.PublicMatcher(), []string{}
	for _, code := range codes {
		if public(code) {
			pub = append(pub, code)
		}
	}
	return q.SetDatasetPublicTestcases(ctx, sqlc.SetDatasetPublicTestcasesParams{DatasetID: d.ID, Public: pub})
}

// FillResult is what Fill changed.
type FillResult struct {
	TaskID, DatasetID int64
	// KeptName is set when the package's name belongs to another task: the
	// task keeps its own.
	KeptName bool
}

// Fill makes a task and one of its datasets what a package without errors
// describes: the task settings, statements, attachments and examples, and
// the dataset's settings, testcases and managers, all replaced in one
// transaction. Existing submissions keep their results until they are
// reevaluated, as after changing the dataset by hand.
func Fill(ctx context.Context, pool *pgxpool.Pool, store blob.Store, p *Package, taskID, datasetID int64) (*FillResult, error) {
	if !p.OK() || p.Config == nil {
		return nil, errors.New("the package has errors")
	}
	c := p.Config
	files, err := storeFiles(ctx, store, p, true)
	if err != nil {
		return nil, err
	}
	res := &FillResult{TaskID: taskID, DatasetID: datasetID}
	err = db.InTx(ctx, pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		t, err := q.GetTask(ctx, taskID)
		if err != nil {
			return err
		}
		d, err := q.GetDataset(ctx, datasetID)
		if err != nil {
			return err
		}
		if d.TaskID != t.ID {
			return fmt.Errorf("dataset %d is not a dataset of %s", d.ID, t.Name)
		}
		free, err := nameFree(ctx, q, c.Name, t.ID)
		if err != nil {
			return err
		}
		res.KeptName = !free
		if _, err := q.UpdateTask(ctx, c.TaskUpdate(t, !free)); err != nil {
			return err
		}
		if _, err := q.UpdateDataset(ctx, c.DatasetUpdate(db.DatasetToUpdate(d), len(p.Tests))); err != nil {
			return err
		}
		if err := q.DeleteDatasetTestcases(ctx, d.ID); err != nil {
			return err
		}
		if err := q.DeleteDatasetManagers(ctx, d.ID); err != nil {
			return err
		}
		if err := files.writeDataset(ctx, q, d.ID); err != nil {
			return err
		}
		if err := files.clearContent(ctx, q, t.ID); err != nil {
			return err
		}
		return files.writeContent(ctx, q, t.ID)
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}
