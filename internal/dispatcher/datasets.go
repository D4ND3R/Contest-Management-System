package dispatcher

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
	"github.com/D4ND3R/Contest-Management-System/internal/scoring"
)

// datasetInfo is everything needed to build jobs and score results for a
// dataset. It is cached: datasets change rarely and every change is
// followed by a dataset_changed event that drops the cache.
type datasetInfo struct {
	ds        sqlc.Dataset
	task      sqlc.Task
	managers  []jobs.File
	testcases []sqlc.Testcase
	byID      map[int64]sqlc.Testcase
	scoreType scoring.ScoreType
	// scoreTypeErr is set when the score type parameters are invalid.
	scoreTypeErr error
	loadedAt     time.Time
}

func (di *datasetInfo) live() bool {
	return di.task.ActiveDatasetID != nil && *di.task.ActiveDatasetID == di.ds.ID
}

func (di *datasetInfo) limits() jobs.Limits {
	l := jobs.Limits{OutputBytes: di.ds.OutputLimitBytes, Processes: int(di.ds.ProcessLimit)}
	if di.ds.TimeLimitMs != nil {
		l.TimeMs = int64(*di.ds.TimeLimitMs)
	}
	if di.ds.WallTimeLimitMs != nil {
		l.WallTimeMs = int64(*di.ds.WallTimeLimitMs)
	}
	if di.ds.MemoryLimitBytes != nil {
		l.MemoryBytes = *di.ds.MemoryLimitBytes
	}
	return l
}

func (di *datasetInfo) jobTestcases(ids []int64) []jobs.Testcase {
	out := make([]jobs.Testcase, 0, len(ids))
	for _, id := range ids {
		if tc, ok := di.byID[id]; ok {
			out = append(out, jobs.Testcase{ID: tc.ID, Codename: tc.Codename, Input: tc.InputDigest, Output: tc.OutputDigest})
		}
	}
	return out
}

type datasetCache struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[int64]*datasetInfo
}

func newDatasetCache(ttl time.Duration) *datasetCache {
	return &datasetCache{ttl: ttl, m: map[int64]*datasetInfo{}}
}

func (c *datasetCache) invalidate() {
	c.mu.Lock()
	c.m = map[int64]*datasetInfo{}
	c.mu.Unlock()
}

func (c *datasetCache) get(ctx context.Context, q *sqlc.Queries, id int64) (*datasetInfo, error) {
	c.mu.Lock()
	di, ok := c.m[id]
	c.mu.Unlock()
	if ok && time.Since(di.loadedAt) < c.ttl {
		return di, nil
	}
	ds, err := q.GetDataset(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("dataset %d: %w", id, err)
	}
	task, err := q.GetTask(ctx, ds.TaskID)
	if err != nil {
		return nil, fmt.Errorf("task %d: %w", ds.TaskID, err)
	}
	ms, err := q.ListManagers(ctx, id)
	if err != nil {
		return nil, err
	}
	tcs, err := q.ListTestcases(ctx, id)
	if err != nil {
		return nil, err
	}
	di = &datasetInfo{ds: ds, task: task, testcases: tcs, byID: make(map[int64]sqlc.Testcase, len(tcs)), loadedAt: time.Now()}
	for _, m := range ms {
		di.managers = append(di.managers, jobs.File{Name: m.Filename, Digest: m.Digest})
	}
	codes := make([]string, len(tcs))
	public := make([]bool, len(tcs))
	for i, tc := range tcs {
		di.byID[tc.ID] = tc
		codes[i], public[i] = tc.Codename, tc.Public
	}
	di.scoreType, di.scoreTypeErr = scoring.New(ds.ScoreType, ds.ScoreTypeParams, codes, public, int(task.ScorePrecision))
	c.mu.Lock()
	c.m[id] = di
	c.mu.Unlock()
	return di, nil
}
