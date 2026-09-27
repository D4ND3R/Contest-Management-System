package contestweb

import (
	"math"
	"net/http"
	"sync"
	"time"
)

// testsCtx gives the test form its page, task data and task; From is
// where to come back after running a test ("testing": the Testing page).
type testsCtx struct {
	P    *page
	D    *taskData
	Task *taskView
	From string
}

// latencyCache keeps the typical judging time for a few seconds: every
// waiting contestant asks for it.
type latencyCache struct {
	mu  sync.Mutex
	at  time.Time
	sec int64
}

// typicalLatency is the median arrival-to-score time of the latest judged
// submissions, in whole seconds (0 when unknown).
func (s *Server) typicalLatency(r *http.Request) int64 {
	c := &s.latency
	c.mu.Lock()
	defer c.mu.Unlock()
	if now := time.Now(); now.Sub(c.at) > 10*time.Second {
		if v, err := s.q.RecentJudgingLatency(r.Context()); err == nil {
			c.sec, c.at = int64(math.Ceil(v)), now
		}
	}
	return c.sec
}
