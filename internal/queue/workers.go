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
	// Seccomp: programs run behind the seccomp filter.
	Seccomp bool `json:"seccomp"`
	// Warmed of WarmTotal published blobs are in the worker's cache.
	Warmed    int `json:"warmed,omitempty"`
	WarmTotal int `json:"warm_total,omitempty"`
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

// Calibration is the latest calibration benchmark of a worker machine
// ("cms ctl calibrate"): the median CPU time of each judging slot, in
// seconds.
type Calibration struct {
	Worker   string    `json:"worker"`
	Hostname string    `json:"hostname"`
	At       time.Time `json:"at"`
	Median   float64   `json:"median"`
	Slots    []float64 `json:"slots"`
	// Cores are the CPUs of Slots; Tolerance the fraction that counts as off.
	Cores     []int   `json:"cores"`
	Tolerance float64 `json:"tolerance"`
	// Off counts the slots more than the tolerance away from Median.
	Off int `json:"off"`
}

func (q *Queue) calibrationKey(worker string) string { return q.Key("calibration", worker) }
func (q *Queue) calibrationsSet() string             { return q.Key("calibrations") }

// SaveCalibration stores a worker's calibration (kept 30 days).
func (q *Queue) SaveCalibration(ctx context.Context, c Calibration) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	pipe := q.rdb.Pipeline()
	pipe.Set(ctx, q.calibrationKey(c.Worker), data, 30*24*time.Hour)
	pipe.SAdd(ctx, q.calibrationsSet(), c.Worker)
	_, err = pipe.Exec(ctx)
	return err
}

// Calibrations returns the stored calibrations, by worker name.
func (q *Queue) Calibrations(ctx context.Context) (map[string]Calibration, error) {
	names, err := q.rdb.SMembers(ctx, q.calibrationsSet()).Result()
	if err != nil {
		return nil, err
	}
	out := map[string]Calibration{}
	for _, n := range names {
		data, err := q.rdb.Get(ctx, q.calibrationKey(n)).Bytes()
		if errors.Is(err, redis.Nil) {
			q.rdb.SRem(ctx, q.calibrationsSet(), n)
			continue
		}
		if err != nil {
			return nil, err
		}
		var c Calibration
		if json.Unmarshal(data, &c) == nil {
			out[n] = c
		}
	}
	return out, nil
}
