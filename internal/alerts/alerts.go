// Package alerts evaluates the health rules of an installation (SPEC_IOI
// §12: queue depth, judging latency, workers, disks, timing drift, WAL
// archiving) and tells the administrators once when a rule starts firing
// and once when it stops: an admin notification and, optionally, a
// webhook (chat, phone push).
package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"
)

// Rule is one health check. For is how long it must keep failing before
// it fires (a queue may grow for a minute without anything being wrong).
type Rule struct {
	Name  string
	For   time.Duration
	Check func(ctx context.Context) (firing bool, detail string, err error)
}

// Alert is a rule's state.
type Alert struct {
	Name   string    `json:"name"`
	Detail string    `json:"detail"`
	Since  time.Time `json:"since"`
	// Fired: the alert was announced (it failed for longer than For).
	Fired bool `json:"fired"`
}

// Manager evaluates rules and keeps their state in Redis (so a restarted
// or replaced monitor does not announce the same alert twice).
type Manager struct {
	rdb     *redis.Client
	key     string
	rules   []Rule
	notify  func(ctx context.Context, text string)
	webhook string
	client  *http.Client
	log     *slog.Logger
	now     func() time.Time
}

// New creates a manager; key is the Redis hash of the states, notify
// publishes an admin notification.
func New(rdb *redis.Client, key string, rules []Rule, notify func(context.Context, string), webhook string, log *slog.Logger) *Manager {
	return &Manager{rdb: rdb, key: key, rules: rules, notify: notify, webhook: webhook,
		client: &http.Client{Timeout: 5 * time.Second}, log: log, now: time.Now}
}

// Evaluate runs every rule once. A rule that cannot be evaluated keeps its
// state (a database hiccup must not resolve or raise anything).
func (m *Manager) Evaluate(ctx context.Context) error {
	states, err := Active(ctx, m.rdb, m.key)
	if err != nil {
		return err
	}
	byName := map[string]Alert{}
	for _, a := range states {
		byName[a.Name] = a
	}
	var errs []error
	for _, r := range m.rules {
		firing, detail, err := r.Check(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.Name, err))
			continue
		}
		prev, had := byName[r.Name]
		switch {
		case !firing && had:
			if err := m.rdb.HDel(ctx, m.key, r.Name).Err(); err != nil {
				return err
			}
			if prev.Fired {
				m.send(ctx, "Resolved: "+prev.Detail)
			}
		case firing:
			a := Alert{Name: r.Name, Detail: detail, Since: m.now().UTC()}
			if had {
				a.Since, a.Fired = prev.Since, prev.Fired
			}
			announce := !a.Fired && m.now().Sub(a.Since) >= r.For
			if announce {
				a.Fired = true
			}
			data, _ := json.Marshal(a)
			if err := m.rdb.HSet(ctx, m.key, r.Name, data).Err(); err != nil {
				return err
			}
			if announce {
				m.send(ctx, "Alert: "+detail)
			}
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) send(ctx context.Context, text string) {
	m.log.Warn("alert", "text", text)
	if m.notify != nil {
		m.notify(ctx, text)
	}
	if m.webhook == "" {
		return
	}
	body, _ := json.Marshal(map[string]string{"text": text, "content": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.webhook, bytes.NewReader(body))
	if err != nil {
		m.log.Error("alert webhook", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		m.log.Error("alert webhook", "error", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		m.log.Error("alert webhook", "status", resp.StatusCode)
	}
}

// Active returns the rules failing now (fired or still within their For),
// oldest first.
func Active(ctx context.Context, rdb *redis.Client, key string) ([]Alert, error) {
	all, err := rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	out := make([]Alert, 0, len(all))
	for _, v := range all {
		var a Alert
		if json.Unmarshal([]byte(v), &a) == nil {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Since.Before(out[j].Since) })
	return out, nil
}
