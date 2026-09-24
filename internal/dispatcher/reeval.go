package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Level of a reevaluation.
type Level string

const (
	// Recompile discards compilation, evaluations and score.
	Recompile Level = "compilation"
	// Reevaluate keeps the executables and re-runs every testcase.
	Reevaluate Level = "evaluation"
	// Rescore recomputes scores from the stored evaluations (no workers).
	Rescore Level = "score"
)

// ParseLevel accepts the level names used by the UI and the CLI.
func ParseLevel(s string) (Level, error) {
	switch strings.ToLower(s) {
	case "recompile", "compilation", "compile":
		return Recompile, nil
	case "reevaluate", "evaluation", "evaluate":
		return Reevaluate, nil
	case "rescore", "score":
		return Rescore, nil
	}
	return "", fmt.Errorf("unknown reevaluation level %q (recompile, reevaluate, rescore)", s)
}

// Scope selects the submission results to reevaluate. Zero fields are
// ignored; at least one must be set.
type Scope struct {
	ContestID       int64
	TaskID          int64
	DatasetID       int64
	ParticipationID int64
	UserID          int64
	SubmissionID    int64
}

func (s Scope) empty() bool {
	return s.ContestID == 0 && s.TaskID == 0 && s.DatasetID == 0 && s.ParticipationID == 0 && s.UserID == 0 && s.SubmissionID == 0
}

// Invalidate marks the selected results for reevaluation at the given
// level and notifies the dispatcher, which re-derives the work in the
// background. Contest traffic is not interrupted: other submissions keep
// being judged, and rejudges use the background queue. It returns the
// number of results invalidated.
func Invalidate(ctx context.Context, pool *pgxpool.Pool, q *queue.Queue, scope Scope, level Level) (int, error) {
	if scope.empty() {
		return 0, errors.New("empty reevaluation scope")
	}
	var where []string
	var args []any
	add := func(cond string, v int64) {
		if v != 0 {
			args = append(args, v)
			where = append(where, fmt.Sprintf(cond, len(args)))
		}
	}
	add("t.contest_id = $%d", scope.ContestID)
	add("s.task_id = $%d", scope.TaskID)
	add("sr.dataset_id = $%d", scope.DatasetID)
	add("s.participation_id = $%d", scope.ParticipationID)
	add("p.user_id = $%d", scope.UserID)
	add("s.id = $%d", scope.SubmissionID)
	sql := `SELECT sr.submission_id, sr.dataset_id
		FROM submission_results sr
		JOIN submissions s ON s.id = sr.submission_id
		JOIN tasks t ON t.id = s.task_id
		JOIN participations p ON p.id = s.participation_id
		WHERE ` + strings.Join(where, " AND ") + ` ORDER BY sr.submission_id`
	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		return 0, err
	}
	keys, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) ([2]int64, error) {
		var k [2]int64
		err := r.Scan(&k[0], &k[1])
		return k, err
	})
	if err != nil {
		return 0, err
	}
	const batch = 200
	for i := 0; i < len(keys); i += batch {
		part := keys[i:min(i+batch, len(keys))]
		err := db.InTx(ctx, pool, func(tx pgx.Tx, qq *sqlc.Queries) error {
			for _, k := range part {
				if _, err := qq.InvalidateSubmissionResult(ctx, sqlc.InvalidateSubmissionResultParams{SubmissionID: k[0], DatasetID: k[1], Level: string(level)}); err != nil {
					return err
				}
				if level == Recompile || level == Reevaluate {
					if err := qq.DeleteEvaluations(ctx, sqlc.DeleteEvaluationsParams{SubmissionID: k[0], DatasetID: k[1]}); err != nil {
						return err
					}
				}
				if level == Recompile {
					if err := qq.DeleteExecutables(ctx, sqlc.DeleteExecutablesParams{SubmissionID: k[0], DatasetID: k[1]}); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			return i, err
		}
	}
	if err := q.Notify(ctx, queue.Event{Kind: queue.EventReevaluate}); err != nil {
		return len(keys), err
	}
	return len(keys), nil
}

// ChangeLiveDataset activates a dataset for its task and notifies the
// dispatcher, which re-aggregates every score on the new live dataset and
// judges submissions that have no result on it yet.
func ChangeLiveDataset(ctx context.Context, pool *pgxpool.Pool, q *queue.Queue, datasetID int64) error {
	qq := sqlc.New(pool)
	ds, err := qq.GetDataset(ctx, datasetID)
	if err != nil {
		return err
	}
	if err := qq.SetActiveDataset(ctx, sqlc.SetActiveDatasetParams{ID: ds.TaskID, ActiveDatasetID: &ds.ID}); err != nil {
		return err
	}
	return q.Notify(ctx, queue.Event{Kind: queue.EventDatasetChanged, TaskID: ds.TaskID, DatasetID: ds.ID})
}
