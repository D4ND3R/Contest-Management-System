package dispatcher

import (
	"context"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/D4ND3R/Contest-Management-System/internal/scoring"
)

// shortCircuit marks as skipped the testcases that can no longer change
// the score (SPEC_IOI §6, D87): with GroupMin or GroupMul, a testcase whose
// every subtask already has a zero. They count as evaluated (the result
// completes sooner), and the workers are told not to run the ones still
// queued; a real result arriving anyway replaces the mark.
func (d *Dispatcher) shortCircuit(ctx context.Context, q *sqlc.Queries, eff *effects, sc *submissionCtx, gen int32) error {
	sk, ok := sc.di.scoreType.(scoring.Skipper)
	if !ok {
		return nil
	}
	evs, err := q.ListEvaluations(ctx, sqlc.ListEvaluationsParams{SubmissionID: sc.meta.ID, DatasetID: sc.di.ds.ID})
	if err != nil {
		return err
	}
	known := make(map[string]float64, len(evs))
	for _, e := range evs {
		if tc, ok := sc.di.byID[e.TestcaseID]; ok {
			known[tc.Codename] = e.Outcome
		}
	}
	names := sk.Skippable(known)
	if len(names) == 0 {
		return nil
	}
	byName := make(map[string]int64, len(sc.di.testcases))
	for _, tc := range sc.di.testcases {
		byName[tc.Codename] = tc.ID
	}
	skip := queue.Skip{SubmissionID: sc.meta.ID, DatasetID: sc.di.ds.ID, Generation: gen}
	worker := "short-circuit"
	for _, n := range names {
		id, ok := byName[n]
		if !ok {
			continue
		}
		if err := q.UpsertEvaluation(ctx, sqlc.UpsertEvaluationParams{SubmissionID: sc.meta.ID, DatasetID: sc.di.ds.ID, TestcaseID: id,
			Outcome: 0, Text: scoring.MsgSkipped, ExitStatus: scoring.StatusSkipped, Worker: &worker}); err != nil {
			return err
		}
		skip.Testcases = append(skip.Testcases, id)
	}
	eff.skips = append(eff.skips, skip)
	return nil
}
