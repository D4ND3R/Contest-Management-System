package contestweb

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/events"
	"github.com/D4ND3R/Contest-Management-System/internal/metrics"
	"github.com/D4ND3R/Contest-Management-System/internal/webkit"
	"github.com/prometheus/client_golang/prometheus"
)

var sseClients = metrics.NewGaugeVec(prometheus.GaugeOpts{
	Name: "cms_sse_clients", Help: "Connected Server-Sent Events clients.",
}, []string{"service"})

// hub fans events out to the SSE connections of this process: one Redis
// subscription per process, then an in-memory map by participation and by
// contest (for announcements).
type hub struct {
	mu        sync.RWMutex
	byPart    map[int64]map[*sseClient]struct{}
	byContest map[int64]map[*sseClient]struct{}
	n         atomic.Int64
}

type sseClient struct {
	ch       chan []byte
	pid, cid int64
	dropped  atomic.Int64
}

func newHub() *hub {
	return &hub{byPart: map[int64]map[*sseClient]struct{}{}, byContest: map[int64]map[*sseClient]struct{}{}}
}

func (h *hub) add(c *sseClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.byPart[c.pid] == nil {
		h.byPart[c.pid] = map[*sseClient]struct{}{}
	}
	h.byPart[c.pid][c] = struct{}{}
	if h.byContest[c.cid] == nil {
		h.byContest[c.cid] = map[*sseClient]struct{}{}
	}
	h.byContest[c.cid][c] = struct{}{}
	sseClients.WithLabelValues("contest-web").Set(float64(h.n.Add(1)))
}

func (h *hub) remove(c *sseClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.byPart[c.pid], c)
	if len(h.byPart[c.pid]) == 0 {
		delete(h.byPart, c.pid)
	}
	delete(h.byContest[c.cid], c)
	if len(h.byContest[c.cid]) == 0 {
		delete(h.byContest, c.cid)
	}
	sseClients.WithLabelValues("contest-web").Set(float64(h.n.Add(-1)))
}

// publish delivers e to its recipients without ever blocking: a client that
// cannot keep up loses events (it re-syncs on reconnect).
func (h *hub) publish(e events.Event) {
	frame := webkit.SSEFrame(e.Type, jsonBytes(e))
	h.mu.RLock()
	defer h.mu.RUnlock()
	var targets map[*sseClient]struct{}
	if e.ParticipationID != 0 {
		targets = h.byPart[e.ParticipationID]
	} else if e.ContestID != 0 {
		targets = h.byContest[e.ContestID]
	}
	for c := range targets {
		select {
		case c.ch <- frame:
		default:
			c.dropped.Add(1)
		}
	}
}

// handleEvents streams events to a contestant.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c := &sseClient{ch: make(chan []byte, 32), pid: rc.part.ID, cid: rc.contest.ID}
	s.hub.add(c)
	defer s.hub.remove(c)
	webkit.ServeSSE(w, r, c.ch, 25*time.Second, 3*time.Second)
}
