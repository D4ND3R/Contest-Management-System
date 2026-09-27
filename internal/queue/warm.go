package queue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"

	"github.com/redis/go-redis/v9"
)

// Pre-warming (SPEC_IOI §11): the dispatcher publishes the blobs that the
// running and upcoming contests need (testcases, graders, checkers) and
// every worker downloads those its cache lacks, before a submission needs
// them. The set is replaced as a whole; its version tells the workers
// whether anything changed.

func (q *Queue) warmKey() string        { return q.Key("warm") }
func (q *Queue) warmVersionKey() string { return q.Key("warm", "version") }

// SetWarm publishes the digests to pre-warm; it reports whether the set
// changed (publishing the same set again does nothing).
func (q *Queue) SetWarm(ctx context.Context, digests []string) (bool, error) {
	ds := slices.Clone(digests)
	slices.Sort(ds)
	ds = slices.Compact(ds)
	h := sha256.New()
	for _, d := range ds {
		h.Write([]byte(d))
		h.Write([]byte{'\n'})
	}
	version := hex.EncodeToString(h.Sum(nil))[:16]
	if len(ds) == 0 {
		version = ""
	}
	cur, err := q.WarmVersion(ctx)
	if err != nil {
		return false, err
	}
	if cur == version {
		return false, nil
	}
	pipe := q.rdb.TxPipeline()
	pipe.Del(ctx, q.warmKey(), q.warmVersionKey())
	for i := 0; i < len(ds); i += 1000 {
		part := ds[i:min(i+1000, len(ds))]
		members := make([]any, len(part))
		for j, d := range part {
			members[j] = d
		}
		pipe.SAdd(ctx, q.warmKey(), members...)
	}
	if version != "" {
		pipe.Set(ctx, q.warmVersionKey(), version, 0)
	}
	_, err = pipe.Exec(ctx)
	return err == nil, err
}

// WarmVersion identifies the published set ("" when there is none).
func (q *Queue) WarmVersion(ctx context.Context) (string, error) {
	v, err := q.rdb.Get(ctx, q.warmVersionKey()).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return v, err
}

// WarmDigests returns the published digests.
func (q *Queue) WarmDigests(ctx context.Context) ([]string, error) {
	return q.rdb.SMembers(ctx, q.warmKey()).Result()
}
