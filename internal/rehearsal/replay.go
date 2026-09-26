// Package rehearsal replays the submissions of a past contest into a
// rehearsal contest (SPEC_IOI §12: a load generator with realistic
// submission patterns): the same sources, from the same contestants, to
// the same tasks, with the original gaps between them (optionally
// accelerated), through the real judging pipeline.
package rehearsal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Options tune a replay.
type Options struct {
	// Speed divides the original gaps (10: ten times faster; 0 or 1: real
	// time).
	Speed float64
	// Limit stops after that many submissions (0: all).
	Limit int
	// Progress, when set, is called after each submission.
	Progress func(sent, total int)
}

// Report is what a replay did.
type Report struct {
	Sent int
	// Skipped submissions have no counterpart in the target (their
	// contestant or task is missing).
	Skipped int
	IDs     []int64
	Took    time.Duration
}

// Replay sends the submissions of contest from into contest to.
// Contestants are matched by username, tasks by their place in the
// contest (a cloned contest renames them).
func Replay(ctx context.Context, pool *pgxpool.Pool, q *queue.Queue, from, to string, o Options) (*Report, error) {
	sq := sqlc.New(pool)
	src, err := sq.GetContestByName(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("contest %q: %w", from, err)
	}
	dst, err := sq.GetContestByName(ctx, to)
	if err != nil {
		return nil, fmt.Errorf("contest %q: %w", to, err)
	}
	if src.ID == dst.ID {
		return nil, errors.New("replay into another contest (a rehearsal copy), not the same one")
	}
	subs, err := sq.ListReplaySubmissions(ctx, src.ID)
	if err != nil {
		return nil, err
	}
	srcTasks, err := sq.ListTasksByContest(ctx, &src.ID)
	if err != nil {
		return nil, err
	}
	dstTasks, err := sq.ListTasksByContest(ctx, &dst.ID)
	if err != nil {
		return nil, err
	}
	taskAt := map[string]int64{} // source task name → target task id
	for i, t := range srcTasks {
		if i < len(dstTasks) {
			taskAt[t.Name] = dstTasks[i].ID
		}
	}
	parts, err := sq.ListParticipationsByContest(ctx, dst.ID)
	if err != nil {
		return nil, err
	}
	partOf := map[string]int64{}
	for _, p := range parts {
		partOf[p.Username] = p.Participation.ID
	}
	ids := make([]int64, len(subs))
	for i, s := range subs {
		ids[i] = s.ID
	}
	files, err := sq.ListSubmissionFilesBySubmissions(ctx, ids)
	if err != nil {
		return nil, err
	}
	filesOf := map[int64][]sqlc.SubmissionFile{}
	for _, f := range files {
		filesOf[f.SubmissionID] = append(filesOf[f.SubmissionID], f)
	}
	speed := o.Speed
	if speed <= 0 {
		speed = 1
	}
	rep := &Report{}
	start := time.Now()
	total := len(subs)
	if o.Limit > 0 && o.Limit < total {
		total = o.Limit
	}
	for i, s := range subs[:total] {
		at := start.Add(time.Duration(float64(s.SubmittedAt.Sub(subs[0].SubmittedAt)) / speed))
		if d := time.Until(at); d > 0 {
			select {
			case <-ctx.Done():
				return rep, ctx.Err()
			case <-time.After(d):
			}
		}
		part, ok1 := partOf[s.Username]
		task, ok2 := taskAt[s.TaskName]
		if !ok1 || !ok2 {
			rep.Skipped++
			continue
		}
		id, err := send(ctx, pool, dst.Name, part, task, s, filesOf[s.ID])
		if err != nil {
			return rep, fmt.Errorf("submission %d: %w", s.ID, err)
		}
		if err := q.Notify(ctx, queue.Event{Kind: queue.EventSubmission, SubmissionID: id}); err != nil {
			return rep, err
		}
		rep.Sent++
		rep.IDs = append(rep.IDs, id)
		if o.Progress != nil {
			o.Progress(i+1, total)
		}
	}
	rep.Took = time.Since(start)
	return rep, nil
}

// send stores one replayed submission as the contest site would, receipt
// included.
func send(ctx context.Context, pool *pgxpool.Pool, contest string, part, task int64, s sqlc.ListReplaySubmissionsRow,
	files []sqlc.SubmissionFile) (int64, error) {
	var id int64
	err := db.InTx(ctx, pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		now := time.Now()
		sub, err := q.CreateSubmission(ctx, sqlc.CreateSubmissionParams{ParticipationID: &part, TaskID: task, SubmittedAt: now,
			Language: s.Language, Official: true})
		if err != nil {
			return err
		}
		id = sub.ID
		params := make([]sqlc.CreateSubmissionFilesParams, len(files))
		digests := map[string]string{}
		for i, f := range files {
			params[i] = sqlc.CreateSubmissionFilesParams{SubmissionID: id, Filename: f.Filename, Digest: f.Digest}
			digests[f.Filename] = f.Digest
		}
		if _, err := q.CreateSubmissionFiles(ctx, params); err != nil {
			return err
		}
		det, _ := json.Marshal(map[string]any{"contest": contest, "participation": part, "replay_of": s.ID, "files": digests,
			"submitted_at": now.UTC()})
		return q.InsertSubmissionReceipt(ctx, sqlc.InsertSubmissionReceiptParams{Actor: "rehearsal:" + s.Username, SubmissionID: id, Details: det})
	})
	return id, err
}

// Latency summarises how long the replayed submissions took to be scored.
type Latency struct {
	Scored, Pending, Failed int
	P50, P95, Max           time.Duration
}

// Measure reads the latencies of the replayed submissions.
func Measure(ctx context.Context, pool *pgxpool.Pool, ids []int64) (Latency, error) {
	rows, err := sqlc.New(pool).ReplayLatencies(ctx, ids)
	if err != nil {
		return Latency{}, err
	}
	var l Latency
	var secs []float64
	for _, r := range rows {
		switch {
		case r.Failed:
			l.Failed++
		case r.Seconds < 0:
			l.Pending++
		default:
			l.Scored++
			secs = append(secs, r.Seconds)
		}
	}
	sort.Float64s(secs)
	q := func(p float64) time.Duration {
		if len(secs) == 0 {
			return 0
		}
		return time.Duration(secs[int(p*float64(len(secs)-1))] * float64(time.Second))
	}
	l.P50, l.P95, l.Max = q(0.5), q(0.95), q(1)
	return l, nil
}
