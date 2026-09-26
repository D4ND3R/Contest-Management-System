// Package rankingpush feeds the ranking web servers: it watches the score
// changes the dispatcher publishes, recomputes the public board of each
// affected contest from the database (applying the freeze, visibility and
// presentation settings) and pushes deltas — or full boards when a server
// is behind — to every configured ranking web server over HTTP.
package rankingpush

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/app"
	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/events"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/D4ND3R/Contest-Management-System/internal/ranking"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Options configure a pusher.
type Options struct {
	URLs      []string
	Token     string
	Secret    []byte // derives the keys of administrator-only boards
	Namespace string
	// LiveInterval and FrozenInterval bound how often a contest is
	// recomputed while its ranking is live or frozen; FullInterval is the
	// periodic full resynchronisation.
	LiveInterval, FrozenInterval, FullInterval time.Duration
}

// Pusher pushes boards to ranking web servers.
type Pusher struct {
	pool   *pgxpool.Pool
	q      *sqlc.Queries
	rdb    *redis.Client
	queue  *queue.Queue
	blobs  blob.Store
	log    *slog.Logger
	opts   Options
	client *http.Client
	now    func() time.Time

	mu       sync.Mutex
	contests map[int64]*contestState
	targets  []*target
}

type contestState struct {
	c        sqlc.Contest
	board    *ranking.Board
	history  map[string][]ranking.Point
	seq      int64
	dirty    bool
	full     bool // the next push recomputes history and sends full boards
	lastPush time.Time
	touched  map[int64]time.Time // participations with new results (history)
	listed   bool                // published on the servers
}

type target struct {
	url     string
	seq     map[string]int64 // last sequence acknowledged per contest
	assets  map[string]bool
	retryAt time.Time
}

// New creates a pusher.
func New(pool *pgxpool.Pool, rdb *redis.Client, blobs blob.Store, log *slog.Logger, o Options) *Pusher {
	if o.LiveInterval <= 0 {
		o.LiveInterval = 250 * time.Millisecond
	}
	if o.FrozenInterval <= 0 {
		o.FrozenInterval = 2 * time.Second
	}
	if o.FullInterval <= 0 {
		o.FullInterval = time.Minute
	}
	p := &Pusher{pool: pool, q: sqlc.New(pool), rdb: rdb, queue: queue.New(rdb, o.Namespace), blobs: blobs, log: log, opts: o,
		client: &http.Client{Timeout: 15 * time.Second}, now: time.Now, contests: map[int64]*contestState{}}
	for _, u := range o.URLs {
		p.targets = append(p.targets, &target{url: strings.TrimRight(u, "/"), seq: map[string]int64{}, assets: map[string]bool{}})
	}
	return p
}

// BoardKey is the key of an administrator-only board (visibility
// "admins"): viewers open the scoreboard with ?key=<BoardKey>.
func BoardKey(secret []byte, contest string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte("ranking-board:" + contest))
	return hex.EncodeToString(m.Sum(nil))[:32]
}

func keyHash(key string) string {
	s := sha256.Sum256([]byte(key))
	return hex.EncodeToString(s[:])
}

// Run pushes until ctx ends. Several pushers may run; a lease makes one
// active.
func (p *Pusher) Run(ctx context.Context) error {
	if len(p.targets) == 0 {
		<-ctx.Done()
		return nil
	}
	lease := p.queue.NewLease("ranking-pusher", 10*time.Second)
	return lease.Run(ctx, func(ctx context.Context) error {
		p.log.Info("ranking pusher active", "servers", len(p.targets))
		g, _ := app.NewGroup(ctx)
		g.Go(p.watchScores)
		g.Go(p.watchContests)
		g.Go(p.loop)
		return g.Wait()
	})
}

// watchScores marks contests dirty on every score change.
func (p *Pusher) watchScores(ctx context.Context) error {
	last, err := p.queue.LastRankingID(ctx)
	if err != nil {
		last = "$"
	}
	for ctx.Err() == nil {
		ups, next, err := p.queue.ReadRanking(ctx, last, 2*time.Second)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			p.log.Warn("read ranking updates", "error", err)
			time.Sleep(time.Second)
			continue
		}
		last = next
		p.mu.Lock()
		for _, u := range ups {
			st := p.state(u.ContestID)
			st.dirty = true
			if st.touched == nil {
				st.touched = map[int64]time.Time{}
			}
			st.touched[u.ParticipationID] = u.Time
		}
		p.mu.Unlock()
	}
	return nil
}

// watchContests reloads contests whose settings change.
func (p *Pusher) watchContests(ctx context.Context) error {
	for ctx.Err() == nil {
		err := events.Subscribe(ctx, p.rdb, p.opts.Namespace, func(e events.Event) {
			if e.Type == events.TypeContest && e.ContestID != 0 {
				p.mu.Lock()
				st := p.state(e.ContestID)
				st.dirty, st.full = true, true
				p.mu.Unlock()
			}
		})
		if ctx.Err() == nil {
			p.log.Warn("contest events subscription ended", "error", err)
			time.Sleep(time.Second)
		}
	}
	return nil
}

// state returns the state of a contest (under p.mu).
func (p *Pusher) state(id int64) *contestState {
	st := p.contests[id]
	if st == nil {
		st = &contestState{full: true, dirty: true}
		p.contests[id] = st
	}
	return st
}

func (p *Pusher) loop(ctx context.Context) error {
	tick := time.NewTicker(p.opts.LiveInterval)
	defer tick.Stop()
	var lastFull, lastScan time.Time
	for {
		now := p.now()
		switch {
		case now.Sub(lastFull) >= p.opts.FullInterval:
			// Periodic resynchronisation of every board.
			if err := p.discover(ctx, true); err != nil && ctx.Err() == nil {
				p.log.Warn("list contests", "error", err)
			}
			lastFull, lastScan = now, now
		case now.Sub(lastScan) >= 3*time.Second:
			// New contests appear within seconds.
			if err := p.discover(ctx, false); err != nil && ctx.Err() == nil {
				p.log.Warn("list contests", "error", err)
			}
			lastScan = now
		}
		p.flush(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

// discover schedules a full push of new contests, or of every contest
// when all is set.
func (p *Pusher) discover(ctx context.Context, all bool) error {
	cs, err := p.q.ListContests(ctx)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range cs {
		if _, known := p.contests[c.ID]; known && !all {
			continue
		}
		st := p.state(c.ID)
		st.dirty, st.full = true, true
	}
	return nil
}

// Flush recomputes and pushes every dirty contest now (tests).
func (p *Pusher) Flush(ctx context.Context) { p.flush(ctx) }

func (p *Pusher) flush(ctx context.Context) {
	now := p.now()
	p.mu.Lock()
	var ids []int64
	for id, st := range p.contests {
		// The freeze and "after the contest" begin with the clock.
		if st.board != nil && (ranking.Frozen(st.c, now) != st.board.Frozen || st.c.RankingWhen == "after" && !st.listed && !now.Before(st.c.StopTime)) {
			st.dirty, st.full = true, true
		}
		interval := p.opts.LiveInterval
		if ranking.Frozen(st.c, now) {
			interval = p.opts.FrozenInterval
		}
		if st.dirty && (st.full || now.Sub(st.lastPush) >= interval) {
			ids = append(ids, id)
		}
	}
	p.mu.Unlock()
	for _, id := range ids {
		if err := p.pushContest(ctx, id); err != nil && ctx.Err() == nil {
			p.log.Warn("push ranking", "contest", id, "error", err)
		}
	}
}

// published reports whether c's board goes to the ranking web servers.
func published(c sqlc.Contest, now time.Time) bool {
	if c.Status == "draft" {
		return false
	}
	switch c.RankingVisibility {
	case "public", "admins":
	default:
		return false
	}
	return c.RankingWhen != "after" || !now.Before(c.StopTime)
}

func (p *Pusher) pushContest(ctx context.Context, id int64) error {
	p.mu.Lock()
	st := p.contests[id]
	full, touched := st.full, st.touched
	st.dirty, st.full, st.touched = false, false, nil
	st.lastPush = p.now()
	p.mu.Unlock()
	requeue := func() {
		p.mu.Lock()
		st.dirty, st.full = true, true
		p.mu.Unlock()
	}

	c, err := p.q.GetContest(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) { // deleted
		p.remove(ctx, st, id)
		return nil
	}
	if err != nil {
		requeue()
		return err
	}
	now := p.now()
	if st.listed && st.c.Name != "" && st.c.Name != c.Name {
		p.deleteBoard(ctx, st.c.Name) // renamed
		st.listed = false
	}
	st.c = c
	if !published(c, now) {
		if st.listed || full {
			p.deleteBoard(ctx, c.Name)
			st.listed, st.board, st.history = false, nil, nil
		}
		return nil
	}
	opt := ranking.Options{IncludeHidden: c.RankingShowHidden}
	frozen := ranking.Frozen(c, now)
	var r *ranking.Ranking
	var hist map[int64][]ranking.Point
	switch {
	case frozen:
		r, hist, err = ranking.Replay(ctx, p.q, id, ranking.FreezeAt(c), opt)
	case full || st.history == nil:
		r, hist, err = ranking.Replay(ctx, p.q, id, nil, opt)
	default:
		r, err = ranking.Compute(ctx, p.q, id, opt)
	}
	if err != nil {
		requeue()
		return err
	}
	next := ranking.BuildBoard(r, c, now)
	var appended map[string][]ranking.Point
	if hist != nil {
		st.history = map[string][]ranking.Point{}
		for pid, pts := range hist {
			st.history[ranking.ParticipationKey(pid)] = pts
		}
	} else {
		appended = map[string][]ranking.Point{}
		totals := map[int64]float64{}
		for _, row := range r.Rows {
			totals[row.ParticipationID] = row.Total
			if r.ICPC {
				totals[row.ParticipationID] = float64(row.Solved)
			}
		}
		for pid, t := range touched {
			total, ok := totals[pid]
			if !ok {
				continue
			}
			k := ranking.ParticipationKey(pid)
			if pts := st.history[k]; len(pts) > 0 && pts[len(pts)-1].Total == total {
				continue
			}
			pt := ranking.Point{Time: t.UTC(), Total: total}
			st.history[k] = append(st.history[k], pt)
			appended[k] = append(appended[k], pt)
		}
	}
	changed, removed := ranking.Diff(st.board, next)
	headerChanged := st.board == nil || !sameHeader(st.board, next)
	if !full && !headerChanged && len(changed) == 0 && len(removed) == 0 && len(appended) == 0 {
		return nil
	}
	kh := ""
	if c.RankingVisibility == "admins" {
		kh = keyHash(BoardKey(p.opts.Secret, c.Name))
	}
	st.seq++
	seq := st.seq
	p.uploadFlags(ctx, next)
	var firstErr error
	for _, t := range p.targets {
		if now.Before(t.retryAt) {
			t.seq[c.Name] = -1
			firstErr = fmt.Errorf("%s: waiting to retry", t.url)
			continue
		}
		msg := ranking.Push{Contest: c.Name, Seq: seq, KeyHash: kh}
		if full || headerChanged || t.seq[c.Name] != seq-1 {
			msg.Kind, msg.Board, msg.History = "full", next, st.history
		} else {
			msg.Kind, msg.Base, msg.Rows, msg.Removed, msg.History = "delta", seq-1, changed, removed, appended
		}
		acked, err := p.send(ctx, t, msg)
		if err != nil {
			t.seq[c.Name] = -1
			if acked < 0 {
				t.retryAt = now.Add(2 * time.Second)
			}
			firstErr = err
			continue
		}
		t.seq[c.Name] = seq
	}
	st.board, st.listed = next, true
	if firstErr != nil {
		requeue()
	}
	return firstErr
}

func sameHeader(a, b *ranking.Board) bool {
	ha, _ := json.Marshal(a.Header())
	hb, _ := json.Marshal(b.Header())
	return string(ha) == string(hb)
}

// send posts a message; acked is the server's sequence (-1 when the server
// could not be reached).
func (p *Pusher) send(ctx context.Context, t *target, msg ranking.Push) (int64, error) {
	body, err := json.Marshal(msg)
	if err != nil {
		return 0, err
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", t.url+"/push", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.opts.Token)
	resp, err := p.client.Do(req)
	if err != nil {
		return -1, err
	}
	defer resp.Body.Close()
	var ack struct {
		Seq int64 `json:"seq"`
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	_ = json.Unmarshal(data, &ack)
	switch resp.StatusCode {
	case http.StatusOK:
		return ack.Seq, nil
	case http.StatusConflict:
		return ack.Seq, fmt.Errorf("%s is at sequence %d; a full board follows", t.url, ack.Seq)
	default:
		return -1, fmt.Errorf("%s: %s: %s", t.url, resp.Status, strings.TrimSpace(string(data)))
	}
}

func (p *Pusher) deleteBoard(ctx context.Context, name string) {
	for _, t := range p.targets {
		if _, err := p.send(ctx, t, ranking.Push{Contest: name, Kind: "delete"}); err != nil {
			p.log.Warn("delete board", "contest", name, "error", err)
		}
		delete(t.seq, name)
	}
}

func (p *Pusher) remove(ctx context.Context, st *contestState, id int64) {
	if st.listed && st.c.Name != "" {
		p.deleteBoard(ctx, st.c.Name)
	}
	p.mu.Lock()
	delete(p.contests, id)
	p.mu.Unlock()
}

// uploadFlags sends the flags the board shows to servers that lack them.
func (p *Pusher) uploadFlags(ctx context.Context, b *ranking.Board) {
	if p.blobs == nil || !b.Flags {
		return
	}
	for _, row := range b.Rows {
		if row.Flag == "" {
			continue
		}
		var data []byte
		for _, t := range p.targets {
			if t.assets[row.Flag] {
				continue
			}
			if data == nil {
				var err error
				if data, err = blob.ReadAll(ctx, p.blobs, row.Flag); err != nil {
					p.log.Warn("read flag", "digest", row.Flag, "error", err)
					break
				}
			}
			req, _ := http.NewRequestWithContext(ctx, "PUT", t.url+"/assets/"+row.Flag, bytes.NewReader(data))
			req.Header.Set("Authorization", "Bearer "+p.opts.Token)
			resp, err := p.client.Do(req)
			if err != nil {
				continue
			}
			resp.Body.Close()
			if resp.StatusCode < 300 {
				t.assets[row.Flag] = true
			}
		}
	}
}
