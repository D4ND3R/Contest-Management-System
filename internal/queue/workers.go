package queue

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/hoststat"
	"github.com/redis/go-redis/v9"
)

// SlotStatus describes what one worker slot is doing.
type SlotStatus struct {
	Slot         int       `json:"slot"`
	Core         int       `json:"core"`
	JobID        string    `json:"job_id,omitempty"`
	Kind         string    `json:"kind,omitempty"`
	SubmissionID int64     `json:"submission_id,omitempty"`
	UserTestID   int64     `json:"user_test_id,omitempty"`
	Since        time.Time `json:"since,omitempty"`
}

// WorkerStatus is published by every worker as a heartbeat.
type WorkerStatus struct {
	Name      string       `json:"name"`
	Hostname  string       `json:"hostname"`
	Version   string       `json:"version"`
	StartedAt time.Time    `json:"started_at"`
	Slots     []SlotStatus `json:"slots"`
	JobsDone  int64        `json:"jobs_done"`
	Errors    int64        `json:"errors"`
	LastSeen  time.Time    `json:"last_seen"`
	Alive     bool         `json:"alive"`
	// Host is the load of the worker's machine.
	Host *hoststat.Stats `json:"host,omitempty"`
}

func (q *Queue) workerKey(name string) string { return q.Key("worker", name) }
func (q *Queue) workersSet() string           { return q.Key("workers") }

// Heartbeat publishes a worker's status; it expires after ttl unless renewed.
func (q *Queue) Heartbeat(ctx context.Context, st *WorkerStatus, ttl time.Duration) error {
	st.LastSeen = time.Now().UTC()
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	pipe := q.rdb.Pipeline()
	pipe.Set(ctx, q.workerKey(st.Name), data, ttl)
	pipe.ZAdd(ctx, q.workersSet(), redis.Z{Score: float64(st.LastSeen.Unix()), Member: st.Name})
	_, err = pipe.Exec(ctx)
	return err
}

// Deregister removes a worker (clean shutdown).
func (q *Queue) Deregister(ctx context.Context, name string) error {
	pipe := q.rdb.Pipeline()
	pipe.Del(ctx, q.workerKey(name))
	pipe.ZRem(ctx, q.workersSet(), name)
	_, err := pipe.Exec(ctx)
	return err
}

// WorkerAlive reports whether a worker's heartbeat is current.
func (q *Queue) WorkerAlive(ctx context.Context, name string) (bool, error) {
	n, err := q.rdb.Exists(ctx, q.workerKey(name)).Result()
	return n == 1, err
}

// Workers lists workers seen within forget (dead ones have Alive=false);
// older entries are pruned.
func (q *Queue) Workers(ctx context.Context, forget time.Duration) ([]WorkerStatus, error) {
	cutoff := time.Now().Add(-forget).Unix()
	q.rdb.ZRemRangeByScore(ctx, q.workersSet(), "-inf", "("+itoa(cutoff))
	names, err := q.rdb.ZRange(ctx, q.workersSet(), 0, -1).Result()
	if err != nil || len(names) == 0 {
		return nil, err
	}
	keys := make([]string, len(names))
	for i, n := range names {
		keys[i] = q.workerKey(n)
	}
	vals, err := q.rdb.MGet(ctx, keys...).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	scores, _ := q.rdb.ZRangeWithScores(ctx, q.workersSet(), 0, -1).Result()
	last := map[string]time.Time{}
	for _, z := range scores {
		last[z.Member.(string)] = time.Unix(int64(z.Score), 0).UTC()
	}
	out := make([]WorkerStatus, 0, len(names))
	for i, n := range names {
		st := WorkerStatus{Name: n, LastSeen: last[n]}
		if s, ok := vals[i].(string); ok {
			if json.Unmarshal([]byte(s), &st) == nil {
				st.Alive = true
			}
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// WorkerOfConsumer returns the worker name of a stream consumer
// ("<worker>/<slot>").
func WorkerOfConsumer(consumer string) string {
	if i := strings.LastIndexByte(consumer, '/'); i > 0 {
		return consumer[:i]
	}
	return consumer
}

func itoa(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
