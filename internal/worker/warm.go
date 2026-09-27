package worker

import (
	"context"
	"time"
)

// warmShare is the part of the cache pre-warming may fill: the rest stays
// for executables and whatever the published set missed.
const warmShare = 0.8

// warmLoop downloads the blobs the dispatcher publishes for the running and
// upcoming contests (SPEC_IOI §11), so the first submissions of a contest
// do not wait for the testcases. One download at a time: it competes with
// nothing the judging cores do but the network.
func (s *Service) warmLoop(ctx context.Context, every time.Duration) {
	var done string
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if v, err := s.q.WarmVersion(ctx); err == nil && v != "" && v != done {
			if s.warm(ctx) == nil {
				done = v
			}
		} else if err == nil && v == "" {
			s.warmed.Store(0)
			s.warmTotal.Store(0)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Service) warm(ctx context.Context) error {
	digests, err := s.q.WarmDigests(ctx)
	if err != nil {
		return err
	}
	c := s.exec.cache
	budget := int64(warmShare * float64(s.exec.cacheMax))
	have, fetched, full := 0, 0, false
	start := time.Now()
	for _, d := range digests {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if c.Contains(d) {
			have++
			continue
		}
		if full || c.Size() >= budget {
			full = true
			continue
		}
		if _, err := c.Fetch(ctx, d); err != nil {
			s.log.Warn("pre-warm", "digest", d, "error", err)
			continue
		}
		have++
		fetched++
		s.warmed.Store(int64(have))
	}
	s.warmed.Store(int64(have))
	s.warmTotal.Store(int64(len(digests)))
	if fetched > 0 || full {
		s.log.Info("cache pre-warmed", "downloaded", fetched, "cached", have, "published", len(digests),
			"seconds", time.Since(start).Seconds(), "cache_full", full)
	}
	return nil
}
