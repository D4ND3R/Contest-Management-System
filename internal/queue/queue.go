// Package queue implements the durable work queues on Redis Streams.
//
// Layout (every key is prefixed by a namespace, "cms:" by default):
//
//	q:compile, q:evaluate, q:usertest, q:background   job streams, highest
//	                                                  priority first; consumer
//	                                                  group "workers"
//	results                                          job results; group "dispatcher"
//	dispatch                                         notifications for the
//	                                                 dispatcher (new submissions,
//	                                                 reevaluations, ...)
//	ranking                                          score changes for RWS pushers
//	worker:<name>                                    worker heartbeat (TTL)
//
// Delivery is at-least-once: a job stays in its stream's pending list until
// the worker acknowledges it after publishing the result, so a crashed
// worker's jobs are reclaimed by the monitor. Consumers must be idempotent
// (results carry the job's generation, see D10).
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
	"github.com/redis/go-redis/v9"
)

// Priority of a job; lower values are served first.
type Priority int

const (
	PriorityCompile    Priority = iota // submission compilation
	PriorityEvaluate                   // submission evaluation (live dataset)
	PriorityUserTest                   // user tests
	PriorityBackground                 // non-live datasets (autojudge), rejudges of old data
	numPriorities
)

var priorityNames = [...]string{"compile", "evaluate", "usertest", "background"}

func (p Priority) String() string {
	if p >= 0 && p < numPriorities {
		return priorityNames[p]
	}
	return fmt.Sprintf("priority(%d)", int(p))
}

// Priorities lists every priority in service order.
func Priorities() []Priority {
	out := make([]Priority, numPriorities)
	for i := range out {
		out[i] = Priority(i)
	}
	return out
}

// ParsePriority maps a stream suffix back to its priority.
func ParsePriority(s string) (Priority, bool) {
	for i, n := range priorityNames {
		if n == s {
			return Priority(i), true
		}
	}
	return 0, false
}

const (
	workersGroup    = "workers"
	dispatcherGroup = "dispatcher"
)

// Queue is a handle on the streams of one namespace.
type Queue struct {
	rdb *redis.Client
	ns  string
}

// New returns a queue using namespace ns (e.g. "cms:").
func New(rdb *redis.Client, ns string) *Queue {
	if ns == "" {
		ns = "cms:"
	}
	return &Queue{rdb: rdb, ns: ns}
}

// Redis returns the underlying client.
func (q *Queue) Redis() *redis.Client { return q.rdb }

// Key returns a namespaced key.
func (q *Queue) Key(parts ...string) string { return q.ns + strings.Join(parts, ":") }

// JobStream returns the stream of a priority.
func (q *Queue) JobStream(p Priority) string { return q.Key("q", p.String()) }

func (q *Queue) resultsStream() string  { return q.Key("results") }
func (q *Queue) dispatchStream() string { return q.Key("dispatch") }

// RankingStream is the stream of ranking updates.
func (q *Queue) RankingStream() string { return q.Key("ranking") }

// Setup creates the streams and consumer groups (idempotent).
func (q *Queue) Setup(ctx context.Context) error {
	create := func(stream, group string) error {
		err := q.rdb.XGroupCreateMkStream(ctx, stream, group, "0").Err()
		if err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP") {
			return fmt.Errorf("create group %s on %s: %w", group, stream, err)
		}
		return nil
	}
	for _, p := range Priorities() {
		if err := create(q.JobStream(p), workersGroup); err != nil {
			return err
		}
	}
	if err := create(q.resultsStream(), dispatcherGroup); err != nil {
		return err
	}
	return create(q.dispatchStream(), dispatcherGroup)
}

// Enqueue adds a job to the stream of priority p and returns the message id.
func (q *Queue) Enqueue(ctx context.Context, p Priority, j *jobs.Job) (string, error) {
	data, err := json.Marshal(j)
	if err != nil {
		return "", err
	}
	return q.rdb.XAdd(ctx, &redis.XAddArgs{Stream: q.JobStream(p), Values: []any{"job", data}}).Result()
}

// EnqueueMany adds several jobs in one round trip.
func (q *Queue) EnqueueMany(ctx context.Context, items []Item) error {
	if len(items) == 0 {
		return nil
	}
	pipe := q.rdb.Pipeline()
	for _, it := range items {
		data, err := json.Marshal(it.Job)
		if err != nil {
			return err
		}
		pipe.XAdd(ctx, &redis.XAddArgs{Stream: q.JobStream(it.Priority), Values: []any{"job", data}})
	}
	_, err := pipe.Exec(ctx)
	return err
}

// Item is a job with its priority.
type Item struct {
	Priority Priority
	Job      *jobs.Job
}

// Delivery is a job handed to a worker.
type Delivery struct {
	Priority Priority
	ID       string
	Job      *jobs.Job
	// Raw is the undecodable payload when Job is nil.
	Raw string
}

func decodeJob(msg redis.XMessage) (*jobs.Job, string) {
	raw, _ := msg.Values["job"].(string)
	var j jobs.Job
	if err := json.Unmarshal([]byte(raw), &j); err != nil {
		return nil, raw
	}
	return &j, raw
}

// Next returns the next job for consumer, honouring priorities: every
// stream is polled in priority order without blocking, then the consumer
// blocks on all of them at once for at most block. It returns (nil, nil)
// when nothing arrived.
func (q *Queue) Next(ctx context.Context, consumer string, block time.Duration, allowed []Priority) (*Delivery, error) {
	if len(allowed) == 0 {
		allowed = Priorities()
	}
	for _, p := range allowed {
		res, err := q.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group: workersGroup, Consumer: consumer, Streams: []string{q.JobStream(p), ">"}, Count: 1, Block: -1,
		}).Result()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if d := q.firstDelivery(res); d != nil {
			return d, nil
		}
	}
	streams := make([]string, 0, 2*len(allowed))
	for _, p := range allowed {
		streams = append(streams, q.JobStream(p))
	}
	for range allowed {
		streams = append(streams, ">")
	}
	res, err := q.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: workersGroup, Consumer: consumer, Streams: streams, Count: 1, Block: block,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return q.firstDelivery(res), nil
}

func (q *Queue) firstDelivery(res []redis.XStream) *Delivery {
	for _, s := range res {
		if len(s.Messages) == 0 {
			continue
		}
		p, _ := ParsePriority(s.Stream[strings.LastIndex(s.Stream, ":")+1:])
		j, raw := decodeJob(s.Messages[0])
		return &Delivery{Priority: p, ID: s.Messages[0].ID, Job: j, Raw: raw}
	}
	return nil
}

// Ack acknowledges a delivery and deletes the message.
func (q *Queue) Ack(ctx context.Context, d *Delivery) error {
	stream := q.JobStream(d.Priority)
	pipe := q.rdb.TxPipeline()
	pipe.XAck(ctx, stream, workersGroup, d.ID)
	pipe.XDel(ctx, stream, d.ID)
	_, err := pipe.Exec(ctx)
	return err
}

// Complete publishes a result and acknowledges its job atomically (one
// MULTI/EXEC), so a crash can never leave an acknowledged job without its
// result.
func (q *Queue) Complete(ctx context.Context, d *Delivery, res *jobs.Result) error {
	data, err := json.Marshal(res)
	if err != nil {
		return err
	}
	stream := q.JobStream(d.Priority)
	pipe := q.rdb.TxPipeline()
	pipe.XAdd(ctx, &redis.XAddArgs{Stream: q.resultsStream(), Values: []any{"result", data}})
	pipe.XAck(ctx, stream, workersGroup, d.ID)
	pipe.XDel(ctx, stream, d.ID)
	_, err = pipe.Exec(ctx)
	return err
}

// PublishResult adds a result without acknowledging any job (used by the
// monitor to report jobs that exhausted their attempts).
func (q *Queue) PublishResult(ctx context.Context, res *jobs.Result) error {
	data, err := json.Marshal(res)
	if err != nil {
		return err
	}
	return q.rdb.XAdd(ctx, &redis.XAddArgs{Stream: q.resultsStream(), Values: []any{"result", data}}).Err()
}

// ResultDelivery is a result handed to the dispatcher.
type ResultDelivery struct {
	ID     string
	Result *jobs.Result // nil when undecodable
}

// ReadResults returns up to count results for the dispatcher. Pending
// (delivered but unacknowledged) results of this consumer are returned
// first, so a restarted dispatcher resumes where it stopped.
func (q *Queue) ReadResults(ctx context.Context, consumer string, count int64, block time.Duration) ([]ResultDelivery, error) {
	return q.readGroup(ctx, q.resultsStream(), consumer, count, block, func(m redis.XMessage) ResultDelivery {
		raw, _ := m.Values["result"].(string)
		var r jobs.Result
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			return ResultDelivery{ID: m.ID}
		}
		return ResultDelivery{ID: m.ID, Result: &r}
	})
}

func readGroup[T any](ctx context.Context, rdb *redis.Client, stream, group, consumer string, count int64, block time.Duration, conv func(redis.XMessage) T) ([]T, error) {
	// Own pending entries first (crash recovery), then new ones.
	res, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: group, Consumer: consumer, Streams: []string{stream, "0"}, Count: count, Block: -1,
	}).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	if len(res) == 0 || len(res[0].Messages) == 0 {
		res, err = rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group: group, Consumer: consumer, Streams: []string{stream, ">"}, Count: count, Block: block,
		}).Result()
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
	}
	var out []T
	for _, s := range res {
		for _, m := range s.Messages {
			out = append(out, conv(m))
		}
	}
	return out, nil
}

func (q *Queue) readGroup(ctx context.Context, stream, consumer string, count int64, block time.Duration, conv func(redis.XMessage) ResultDelivery) ([]ResultDelivery, error) {
	return readGroup(ctx, q.rdb, stream, dispatcherGroup, consumer, count, block, conv)
}

// AckResults acknowledges and deletes processed results.
func (q *Queue) AckResults(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	pipe := q.rdb.TxPipeline()
	pipe.XAck(ctx, q.resultsStream(), dispatcherGroup, ids...)
	pipe.XDel(ctx, q.resultsStream(), ids...)
	_, err := pipe.Exec(ctx)
	return err
}

// Event is a notification for the dispatcher.
type Event struct {
	Kind string `json:"kind"`
	// For "submission" and "user_test".
	SubmissionID int64 `json:"submission_id,omitempty"`
	UserTestID   int64 `json:"user_test_id,omitempty"`
	// Scope of reevaluations and dataset changes.
	TaskID    int64 `json:"task_id,omitempty"`
	DatasetID int64 `json:"dataset_id,omitempty"`
	ContestID int64 `json:"contest_id,omitempty"`
}

// Event kinds.
const (
	EventSubmission     = "submission"
	EventUserTest       = "user_test"
	EventReevaluate     = "reevaluate"      // results were invalidated: enqueue what is missing
	EventDatasetChanged = "dataset_changed" // dataset content or the live dataset changed
)

// Notify sends an event to the dispatcher.
func (q *Queue) Notify(ctx context.Context, e Event) error {
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return q.rdb.XAdd(ctx, &redis.XAddArgs{Stream: q.dispatchStream(), Values: []any{"event", data}}).Err()
}

// EventDelivery is an event handed to the dispatcher.
type EventDelivery struct {
	ID    string
	Event *Event
}

// ReadEvents returns up to count dispatcher events.
func (q *Queue) ReadEvents(ctx context.Context, consumer string, count int64, block time.Duration) ([]EventDelivery, error) {
	return readGroup(ctx, q.rdb, q.dispatchStream(), dispatcherGroup, consumer, count, block, func(m redis.XMessage) EventDelivery {
		raw, _ := m.Values["event"].(string)
		var e Event
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			return EventDelivery{ID: m.ID}
		}
		return EventDelivery{ID: m.ID, Event: &e}
	})
}

// AckEvents acknowledges and deletes processed events.
func (q *Queue) AckEvents(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	pipe := q.rdb.TxPipeline()
	pipe.XAck(ctx, q.dispatchStream(), dispatcherGroup, ids...)
	pipe.XDel(ctx, q.dispatchStream(), ids...)
	_, err := pipe.Exec(ctx)
	return err
}

// Pending describes a delivered, unacknowledged job.
type Pending struct {
	Priority   Priority
	ID         string
	Consumer   string
	Idle       time.Duration
	Deliveries int64
}

// PendingJobs lists the unacknowledged jobs of every priority.
func (q *Queue) PendingJobs(ctx context.Context) ([]Pending, error) {
	var out []Pending
	for _, p := range Priorities() {
		ps, err := q.rdb.XPendingExt(ctx, &redis.XPendingExtArgs{
			Stream: q.JobStream(p), Group: workersGroup, Start: "-", End: "+", Count: 10000,
		}).Result()
		if err != nil {
			return nil, err
		}
		for _, e := range ps {
			out = append(out, Pending{Priority: p, ID: e.ID, Consumer: e.Consumer, Idle: e.Idle, Deliveries: e.RetryCount})
		}
	}
	return out, nil
}

// Requeue takes a pending job away from its consumer (dead or stuck) and
// re-adds it with its attempt counter incremented. The copy is added before
// the original is acknowledged, so a crash in between duplicates the job
// rather than losing it. When the job has already used maxAttempts it is
// not re-added and is returned as exhausted. Both results are nil when the
// job was acknowledged in the meantime.
func (q *Queue) Requeue(ctx context.Context, pe Pending, maxAttempts int) (requeued, exhausted *jobs.Job, err error) {
	stream := q.JobStream(pe.Priority)
	msgs, err := q.rdb.XClaim(ctx, &redis.XClaimArgs{
		Stream: stream, Group: workersGroup, Consumer: "monitor", MinIdle: pe.Idle / 2, Messages: []string{pe.ID},
	}).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, nil, err
	}
	if len(msgs) == 0 {
		return nil, nil, nil
	}
	j, _ := decodeJob(msgs[0])
	pipe := q.rdb.TxPipeline()
	if j != nil {
		j.Attempt++
		if j.Attempt < maxAttempts {
			data, err := json.Marshal(j)
			if err != nil {
				return nil, nil, err
			}
			pipe.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: []any{"job", data}})
			requeued = j
		} else {
			exhausted = j
		}
	}
	pipe.XAck(ctx, stream, workersGroup, pe.ID)
	pipe.XDel(ctx, stream, pe.ID)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, nil, err
	}
	return requeued, exhausted, nil
}

// Stats summarises the queues.
type Stats struct {
	Waiting map[string]int64 `json:"waiting"` // not yet delivered
	Pending map[string]int64 `json:"pending"` // delivered, not acknowledged
	Results int64            `json:"results"` // results waiting for the dispatcher
	Events  int64            `json:"events"`
}

// Stats returns queue lengths. Acknowledged messages are deleted, so a
// stream's length is waiting + pending.
func (q *Queue) Stats(ctx context.Context) (*Stats, error) {
	st := &Stats{Waiting: map[string]int64{}, Pending: map[string]int64{}}
	pipe := q.rdb.Pipeline()
	lens := make([]*redis.IntCmd, numPriorities)
	pends := make([]*redis.XPendingCmd, numPriorities)
	for _, p := range Priorities() {
		lens[p] = pipe.XLen(ctx, q.JobStream(p))
		pends[p] = pipe.XPending(ctx, q.JobStream(p), workersGroup)
	}
	res := pipe.XLen(ctx, q.resultsStream())
	ev := pipe.XLen(ctx, q.dispatchStream())
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	for _, p := range Priorities() {
		var pend int64
		if v, err := pends[p].Result(); err == nil {
			pend = v.Count
		}
		st.Pending[p.String()] = pend
		st.Waiting[p.String()] = max(lens[p].Val()-pend, 0)
	}
	st.Results, st.Events = res.Val(), ev.Val()
	return st, nil
}

// RankingUpdate is a change of a participation's task score, consumed by
// the ranking pushers (RWS).
type RankingUpdate struct {
	ContestID       int64      `json:"contest_id"`
	ParticipationID int64      `json:"participation_id"`
	TaskID          int64      `json:"task_id"`
	Score           float64    `json:"score"`
	Subtasks        []float64  `json:"subtasks,omitempty"`
	Pending         int        `json:"pending,omitempty"`
	ICPCSolved      bool       `json:"icpc_solved,omitempty"`
	ICPCAttempts    int        `json:"icpc_attempts,omitempty"`
	ICPCSolvedAt    *time.Time `json:"icpc_solved_at,omitempty"`
	// Time is when the change happened in contest time (the submission
	// time), used for the score history and the freeze.
	Time time.Time `json:"time"`
}

// PushRanking appends ranking updates (the stream is capped).
func (q *Queue) PushRanking(ctx context.Context, ups ...RankingUpdate) error {
	if len(ups) == 0 {
		return nil
	}
	pipe := q.rdb.Pipeline()
	for _, u := range ups {
		data, err := json.Marshal(u)
		if err != nil {
			return err
		}
		pipe.XAdd(ctx, &redis.XAddArgs{Stream: q.RankingStream(), MaxLen: 200000, Approx: true, Values: []any{"update", data}})
	}
	_, err := pipe.Exec(ctx)
	return err
}

// AdoptPending transfers every unacknowledged result and event of other
// dispatcher consumers to consumer. The active dispatcher calls it when it
// takes the lease, so work delivered to a dead replica is not stranded.
func (q *Queue) AdoptPending(ctx context.Context, consumer string) (int, error) {
	n := 0
	for _, stream := range []string{q.resultsStream(), q.dispatchStream()} {
		start := "0-0"
		for {
			msgs, next, err := q.rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
				Stream: stream, Group: dispatcherGroup, Consumer: consumer, MinIdle: 0, Start: start, Count: 1000,
			}).Result()
			if err != nil {
				return n, err
			}
			n += len(msgs)
			if next == "0-0" || next == "" {
				break
			}
			start = next
		}
	}
	return n, nil
}
