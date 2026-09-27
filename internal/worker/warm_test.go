package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/logging"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/D4ND3R/Contest-Management-System/internal/testutil"
)

// TestWarm (SPEC_IOI §11): the worker downloads the published blobs its
// cache lacks, within its share of the cache, and reports how many it has.
func TestWarm(t *testing.T) {
	ctx := context.Background()
	store := blob.NewMem()
	var digests []string
	for _, s := range []string{"input one", "output one", "a grader"} {
		info, err := store.PutBytes(ctx, []byte(strings.Repeat(s, 100)))
		if err != nil {
			t.Fatal(err)
		}
		digests = append(digests, info.Digest)
	}
	rdb, ns := testutil.Redis(t)
	q := queue.New(rdb, ns)
	missing := strings.Repeat("ab", 32) // published but not in the store
	q.SetWarm(ctx, append(digests, missing))

	cache, err := blob.NewCache(store, t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{exec: &Executor{cache: cache, cacheMax: 1 << 20}, q: q, log: logging.Discard()}
	if err := s.warm(ctx); err != nil {
		t.Fatal(err)
	}
	for _, d := range digests {
		if !cache.Contains(d) {
			t.Fatalf("%s not warmed", d)
		}
	}
	if s.warmed.Load() != 3 || s.warmTotal.Load() != 4 {
		t.Fatalf("progress %d/%d", s.warmed.Load(), s.warmTotal.Load())
	}
	// A cache too small for the set stops at its share.
	small, _ := blob.NewCache(store, t.TempDir(), 1000)
	s = &Service{exec: &Executor{cache: small, cacheMax: 1000}, q: q, log: logging.Discard()}
	s.warm(ctx)
	if n := s.warmed.Load(); n != 1 || small.Size() > 1000 {
		t.Fatalf("small cache: %d warmed, %d bytes", n, small.Size())
	}
}
