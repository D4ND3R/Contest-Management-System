package contestweb

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/events"
	"github.com/D4ND3R/Contest-Management-System/internal/metrics"
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

// sseFrame formats one event.
func sseFrame(kind string, data []byte) []byte {
	b := make([]byte, 0, len(kind)+len(data)+16)
	b = append(b, "event: "...)
	b = append(b, kind...)
	b = append(b, "\ndata: "...)
	b = append(b, data...)
	return append(b, "\n\n"...)
}

// publish delivers e to its recipients without ever blocking: a client that
// cannot keep up loses events (it re-syncs on reconnect).
func (h *hub) publish(e events.Event) {
	frame := sseFrame(e.Type, jsonBytes(e))
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
	rcx := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	// Ask the browser to wait a little before reconnecting (thundering herd).
	w.Write([]byte("retry: 3000\n\n"))
	if err := rcx.Flush(); err != nil {
		return
	}
	c := &sseClient{ch: make(chan []byte, 32), pid: rc.part.ID, cid: rc.contest.ID}
	s.hub.add(c)
	defer s.hub.remove(c)
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case frame := <-c.ch:
			rcx.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := w.Write(frame); err != nil {
				return
			}
			if err := rcx.Flush(); err != nil {
				return
			}
		case <-ping.C:
			rcx.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			if err := rcx.Flush(); err != nil {
				return
			}
		}
	}
}
