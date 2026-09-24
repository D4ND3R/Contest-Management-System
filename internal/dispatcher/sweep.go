package dispatcher

import (
	"context"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
)

// sweepPage bounds each query of the sweeper.
const sweepPage = 500

// Sweep re-derives work from the database: results that were never
// created (lost notifications), invalidated (reevaluations) or whose jobs
// were enqueued longer ago than StaleAfter (lost queue messages, crashed
// dispatcher). It is safe to run at any time.
func (d *Dispatcher) Sweep(ctx context.Context) error {
	q := sqlc.New(d.pool)
	recent := time.Now().Add(-10 * time.Minute)
	seen := map[[2]int64]bool{}
	for page := 0; page < 1000 && ctx.Err() == nil; page++ {
		rows, err := q.ListSubmissionsMissingResults(ctx, sweepPage)
		if err != nil {
			return err
		}
		progress := false
		for _, r := range rows {
			k := [2]int64{r.SubmissionID, r.DatasetID}
			if seen[k] {
				continue
			}
			seen[k] = true
			progress = true
			if err := d.advance(ctx, r.SubmissionID, r.DatasetID, !d.isRecent(ctx, q, r.SubmissionID, recent)); err != nil {
				d.log.Error("sweep: create result", "submission", r.SubmissionID, "dataset", r.DatasetID, "error", err)
			}
		}
		if len(rows) < sweepPage || !progress {
			break
		}
	}
	for page := 0; page < 1000 && ctx.Err() == nil; page++ {
		rows, err := q.ListSweepableResults(ctx, sqlc.ListSweepableResultsParams{StaleBefore: time.Now().Add(-d.opts.StaleAfter), MaxRows: sweepPage})
		if err != nil {
			return err
		}
		progress := false
		for _, r := range rows {
			k := [2]int64{r.SubmissionID, r.DatasetID}
			if seen[k] {
				continue
			}
			seen[k] = true
			progress = true
			// Work never enqueued before (NULL) is either new or an explicit
			// reevaluation; lost work of fresh submissions keeps its urgency.
			rejudge := r.JobsEnqueuedAt == nil || !d.isRecent(ctx, q, r.SubmissionID, recent)
			if r.JobsEnqueuedAt != nil {
				d.log.Warn("sweep: re-enqueueing stale work", "submission", r.SubmissionID, "dataset", r.DatasetID)
			}
			if err := d.advance(ctx, r.SubmissionID, r.DatasetID, rejudge); err != nil {
				d.log.Error("sweep: advance", "submission", r.SubmissionID, "dataset", r.DatasetID, "error", err)
			}
		}
		if len(rows) < sweepPage || !progress {
			break
		}
	}
	return d.sweepUserTests(ctx, q)
}

func (d *Dispatcher) isRecent(ctx context.Context, q *sqlc.Queries, subID int64, after time.Time) bool {
	m, err := q.GetSubmissionMeta(ctx, subID)
	return err == nil && m.SubmittedAt.After(after)
}

func (d *Dispatcher) sweepUserTests(ctx context.Context, q *sqlc.Queries) error {
	seen := map[[2]int64]bool{}
	missing, err := q.ListUserTestsMissingResults(ctx, sweepPage)
	if err != nil {
		return err
	}
	for _, r := range missing {
		seen[[2]int64{r.UserTestID, r.DatasetID}] = true
		if err := d.advanceUserTest(ctx, r.UserTestID, r.DatasetID); err != nil {
			d.log.Error("sweep: user test", "user_test", r.UserTestID, "error", err)
		}
	}
	rows, err := q.ListSweepableUserTests(ctx, sqlc.ListSweepableUserTestsParams{StaleBefore: time.Now().Add(-d.opts.StaleAfter), MaxRows: sweepPage})
	if err != nil {
		return err
	}
	for _, r := range rows {
		if seen[[2]int64{r.UserTestID, r.DatasetID}] {
			continue
		}
		if err := d.advanceUserTest(ctx, r.UserTestID, r.DatasetID); err != nil {
			d.log.Error("sweep: user test", "user_test", r.UserTestID, "error", err)
		}
	}
	return nil
}
