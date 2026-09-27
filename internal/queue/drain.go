package queue

import "context"

// Drain deletes every queued job, result and dispatcher event and the
// per-submission hints (testcases to skip, superseded marks), then
// recreates the empty streams. It is for after the database was restored
// to an earlier moment: submission ids are then handed out again, and
// what the queues hold refers to the submissions the restore removed, so
// it could land on new ones with the same id. The dispatcher's sweep
// enqueues again everything the database still needs judged. Services
// must be stopped.
func (q *Queue) Drain(ctx context.Context) (int64, error) {
	keys := []string{q.resultsStream(), q.dispatchStream()}
	for _, p := range Priorities() {
		keys = append(keys, q.JobStream(p))
	}
	for _, pattern := range []string{q.Key("skip", "*"), q.Key("superseded", "*")} {
		it := q.rdb.Scan(ctx, 0, pattern, 1000).Iterator()
		for it.Next(ctx) {
			keys = append(keys, it.Val())
		}
		if err := it.Err(); err != nil {
			return 0, err
		}
	}
	var n int64
	for len(keys) > 0 {
		chunk := keys[:min(len(keys), 500)]
		keys = keys[len(chunk):]
		d, err := q.rdb.Del(ctx, chunk...).Result()
		if err != nil {
			return n, err
		}
		n += d
	}
	return n, q.Setup(ctx)
}
