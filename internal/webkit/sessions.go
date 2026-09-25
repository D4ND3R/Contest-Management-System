package webkit

import (
	"context"
	"encoding/json"
	"net/netip"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// SessionTracker records the active sessions of each participation in
// Redis (IP, user agent, first and last activity) for the administrators'
// view. Each session is written at most once per interval, asynchronously,
// so page views never wait for it.
type SessionTracker struct {
	rdb      *redis.Client
	ns       string
	interval time.Duration
	mu       sync.Mutex
	last     map[string]time.Time
}

// ActiveSession is one tracked session.
type ActiveSession struct {
	ID        string    `json:"id"`
	IP        string    `json:"ip"`
	UserAgent string    `json:"ua"`
	Issued    time.Time `json:"issued"`
	LastSeen  time.Time `json:"last"`
	Nonce     int64     `json:"nonce"`
}

// NewSessionTracker returns a tracker writing under namespace ns.
func NewSessionTracker(rdb *redis.Client, ns string) *SessionTracker {
	return &SessionTracker{rdb: rdb, ns: ns, interval: time.Minute, last: map[string]time.Time{}}
}

func (t *SessionTracker) key(pid int64) string {
	return t.ns + "sessions:" + strconv.FormatInt(pid, 10)
}

// Touch records activity of a session.
func (t *SessionTracker) Touch(pid int64, s *Session, ip netip.Addr, ua string) {
	if t == nil || t.rdb == nil || s == nil || s.ID == "" {
		return
	}
	now := time.Now()
	t.mu.Lock()
	if prev, ok := t.last[s.ID]; ok && now.Sub(prev) < t.interval {
		t.mu.Unlock()
		return
	}
	if len(t.last) > 200000 {
		t.last = map[string]time.Time{}
	}
	t.last[s.ID] = now
	t.mu.Unlock()
	if len(ua) > 200 {
		ua = ua[:200]
	}
	data, _ := json.Marshal(ActiveSession{ID: s.ID, IP: ip.String(), UserAgent: ua, Issued: time.Unix(s.Issued, 0), LastSeen: now, Nonce: s.Nonce})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		pipe := t.rdb.Pipeline()
		pipe.HSet(ctx, t.key(pid), s.ID, data)
		pipe.Expire(ctx, t.key(pid), 24*time.Hour)
		pipe.Exec(ctx)
	}()
}

// List returns the sessions of a participation seen in the last 24 hours,
// most recent first.
func (t *SessionTracker) List(ctx context.Context, pid int64) ([]ActiveSession, error) {
	m, err := t.rdb.HGetAll(ctx, t.key(pid)).Result()
	if err != nil {
		return nil, err
	}
	out := make([]ActiveSession, 0, len(m))
	for _, v := range m {
		var s ActiveSession
		if json.Unmarshal([]byte(v), &s) == nil && time.Since(s.LastSeen) < 24*time.Hour {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out, nil
}

// Clear forgets the sessions of a participation (after a forced logout).
func (t *SessionTracker) Clear(ctx context.Context, pid int64) error {
	return t.rdb.Del(ctx, t.key(pid)).Err()
}
