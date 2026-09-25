package contestweb

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/contest"
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
	// byTeam: clients of team contests, by (contest, team).
	byTeam map[[2]int64]map[*sseClient]struct{}
	n      atomic.Int64
}

type sseClient struct {
	ch       chan []byte
	pid, cid int64
	team     int64 // team contests only
	dropped  atomic.Int64
}

func newHub() *hub {
	return &hub{byPart: map[int64]map[*sseClient]struct{}{}, byContest: map[int64]map[*sseClient]struct{}{},
		byTeam: map[[2]int64]map[*sseClient]struct{}{}}
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
	if c.team != 0 {
		k := [2]int64{c.cid, c.team}
		if h.byTeam[k] == nil {
			h.byTeam[k] = map[*sseClient]struct{}{}
		}
		h.byTeam[k][c] = struct{}{}
	}
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
	if c.team != 0 {
		k := [2]int64{c.cid, c.team}
		delete(h.byTeam[k], c)
		if len(h.byTeam[k]) == 0 {
			delete(h.byTeam, k)
		}
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
	h.send(frame, targets)
}

// publishTeam delivers a submission event to the submitter's pages and to
// those of the teammates (team contests).
func (h *hub) publishTeam(e events.Event, contestID, teamID int64) {
	frame := webkit.SSEFrame(e.Type, jsonBytes(e))
	h.mu.RLock()
	defer h.mu.RUnlock()
	h.send(frame, h.byPart[e.ParticipationID])
	for c := range h.byTeam[[2]int64{contestID, teamID}] {
		if c.pid != e.ParticipationID {
			h.sendOne(frame, c)
		}
	}
}

func (h *hub) send(frame []byte, targets map[*sseClient]struct{}) {
	for c := range targets {
		h.sendOne(frame, c)
	}
}

func (h *hub) sendOne(frame []byte, c *sseClient) {
	select {
	case c.ch <- frame:
	default:
		c.dropped.Add(1)
	}
}

// handleEvents streams events to a contestant.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c := &sseClient{ch: make(chan []byte, 32), pid: rc.part.ID, cid: rc.contest.ID}
	if rc.contest.TeamMode && rc.part.TeamID != nil {
		c.team = *rc.part.TeamID
	}
	s.hub.add(c)
	defer s.hub.remove(c)
	webkit.ServeSSE(w, r, c.ch, 25*time.Second, 3*time.Second)
}

// handleClock tells an open page the contestant's current window, after
// a "clock" event (the organizers changed the times).
func (s *Server) handleClock(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	var end int64
	if rc.status.Phase == contest.Running {
		end = rc.status.End.UnixMilli()
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, `{"phase":%q,"end":%d,"server":%d}`, rc.status.Phase.String(), end, rc.now.UnixMilli())
}
