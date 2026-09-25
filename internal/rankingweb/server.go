// Package rankingweb is the ranking web server (RWS): public live
// scoreboards, decoupled from the rest of the system. It holds everything
// in memory (persisted to its data directory), receives boards and deltas
// pushed by the ranking pusher, and fans changes out to spectators over
// Server-Sent Events. It never touches the database, so it can run on
// another machine and serve thousands of spectators without load on the
// contest.
package rankingweb

import (
	"context"
	"crypto/subtle"
	"encoding/json"

	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/app"
	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/httpx"
	"github.com/D4ND3R/Contest-Management-System/internal/i18n"
	"github.com/D4ND3R/Contest-Management-System/internal/metrics"
	"github.com/D4ND3R/Contest-Management-System/internal/ranking"
	"github.com/D4ND3R/Contest-Management-System/internal/webkit"
	"github.com/D4ND3R/Contest-Management-System/web"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	spectators = metrics.NewGaugeVec(prometheus.GaugeOpts{Name: "cms_rws_spectators", Help: "Connected scoreboard spectators."}, []string{"contest"})
	pushes     = metrics.NewCounterVec(prometheus.CounterOpts{Name: "cms_rws_pushes_total", Help: "Pushes received by kind and outcome."}, []string{"kind", "outcome"})
)

// Server is the ranking web server.
type Server struct {
	cfg     config.RankingWeb
	log     *slog.Logger
	static  *webkit.Static
	pages   map[string]*template.Template
	mu      sync.RWMutex
	boards  map[string]*board
	clients atomic.Int64
	now     func() time.Time
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
var digestRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// New creates the server and loads the boards saved in the data directory.
func New(cfg config.RankingWeb, log *slog.Logger) (*Server, error) {
	static, err := webkit.NewStatic(web.Static, "/static")
	if err != nil {
		return nil, err
	}
	if cfg.MaxClients <= 0 {
		cfg.MaxClients = 20000
	}
	s := &Server{cfg: cfg, log: log, static: static, boards: map[string]*board{}, now: time.Now}
	if err := s.loadTemplates(); err != nil {
		return nil, err
	}
	if cfg.DataDir != "" {
		if err := os.MkdirAll(filepath.Join(cfg.DataDir, "assets"), 0o755); err != nil {
			return nil, err
		}
		if err := s.load(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Server) loadTemplates() error {
	funcs := template.FuncMap{
		"static": s.static.URL,
		"score":  ranking.FormatScore,
		"row":    func(b *ranking.Board, r ranking.BoardRow) rowView { return rowView{B: b, R: r} },
		"cell":   ranking.Display,
		"when":   func(t time.Time, tz string) string { return inZone(t, tz).Format("2006-01-02 15:04") },
		"deref": func(t *time.Time) time.Time {
			if t == nil {
				return time.Time{}
			}
			return *t
		},
	}
	base, err := template.New("").Funcs(funcs).ParseFS(web.Templates, "rws/layout.html", "rws/partials.html")
	if err != nil {
		return err
	}
	s.pages = map[string]*template.Template{"partials": base}
	names, _ := fs.Glob(web.Templates, "rws/*.html")
	for _, n := range names {
		short := strings.TrimSuffix(strings.TrimPrefix(n, "rws/"), ".html")
		if short == "layout" || short == "partials" {
			continue
		}
		t, err := template.Must(base.Clone()).ParseFS(web.Templates, n)
		if err != nil {
			return err
		}
		s.pages[short] = t
	}
	return nil
}

func inZone(t time.Time, tz string) time.Time {
	if loc, err := time.LoadLocation(tz); err == nil && tz != "" {
		return t.In(loc)
	}
	return t.UTC()
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	// Top level: assets, operations and the push API; everything else is
	// under a contest name ("static", "assets", "push", "healthz" and
	// "metrics" cannot be contest names in practice).
	top := http.NewServeMux()
	top.Handle("GET /static/", s.static)
	top.Handle("GET /healthz", httpx.HealthHandler("ranking-web"))
	top.Handle("GET /metrics", metrics.Handler())
	top.HandleFunc("POST /push", s.handlePush)
	top.HandleFunc("PUT /assets/{digest}", s.handleAssetPut)
	top.HandleFunc("GET /assets/{digest}", s.handleAsset)
	top.HandleFunc("GET /{$}", s.handleIndex)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{contest}/{$}", s.withBoard(s.handleBoard))
	mux.HandleFunc("GET /{contest}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/"+r.PathValue("contest")+"/", http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /{contest}/ranking.json", s.withBoard(s.handleJSON))
	mux.HandleFunc("GET /{contest}/events", s.withBoard(s.handleEvents))
	mux.HandleFunc("GET /{contest}/u/{key}", s.withBoard(s.handleUser))
	top.Handle("/", mux)
	var h http.Handler = top
	h = webkit.SecurityHeaders(h)
	return httpx.Observe("ranking-web", s.log, h)
}

// Run serves HTTP and saves changed boards periodically.
func (s *Server) Run(ctx context.Context, addr string, ready chan<- net.Addr) error {
	g, ctx := app.NewGroup(ctx)
	g.Go(func(ctx context.Context) error { return httpx.Serve(ctx, s.log, addr, s.Handler(), ready) })
	g.Go(func(ctx context.Context) error {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				s.saveAll()
				return nil
			case <-t.C:
				s.saveAll()
			}
		}
	})
	return g.Wait()
}

// ---------------------------------------------------------------- push

func (s *Server) authorized(r *http.Request) bool {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return s.cfg.PushToken != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(s.cfg.PushToken)) == 1
}

func (s *Server) handlePush(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var p ranking.Push
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<20)).Decode(&p); err != nil || !nameRe.MatchString(p.Contest) {
		http.Error(w, "bad push", http.StatusBadRequest)
		return
	}
	switch p.Kind {
	case "delete":
		s.mu.Lock()
		bd := s.boards[p.Contest]
		delete(s.boards, p.Contest)
		s.mu.Unlock()
		if bd != nil {
			bd.mu.Lock()
			bd.broadcast(webkit.SSEFrame("reload", []byte("{}")))
			bd.mu.Unlock()
		}
		if s.cfg.DataDir != "" {
			os.Remove(filepath.Join(s.cfg.DataDir, p.Contest+".json"))
		}
		pushes.WithLabelValues("delete", "ok").Inc()
		writeJSON(w, http.StatusOK, map[string]int64{"seq": 0})
	case "full":
		if p.Board == nil {
			http.Error(w, "full push without a board", http.StatusBadRequest)
			return
		}
		bd := s.board(p.Contest, true)
		bd.mu.Lock()
		old := bd.b
		bd.b, bd.seq, bd.keyHash = p.Board, p.Seq, p.KeyHash
		bd.b.SortRows()
		if p.History != nil {
			bd.history = p.History
		}
		bd.rebuild()
		s.announce(bd, old)
		seq := bd.seq
		bd.mu.Unlock()
		pushes.WithLabelValues("full", "ok").Inc()
		writeJSON(w, http.StatusOK, map[string]int64{"seq": seq})
	case "delta":
		bd := s.board(p.Contest, false)
		if bd == nil {
			pushes.WithLabelValues("delta", "conflict").Inc()
			writeJSON(w, http.StatusConflict, map[string]int64{"seq": 0})
			return
		}
		bd.mu.Lock()
		if bd.b == nil || bd.seq != p.Base {
			seq := bd.seq
			bd.mu.Unlock()
			pushes.WithLabelValues("delta", "conflict").Inc()
			writeJSON(w, http.StatusConflict, map[string]int64{"seq": seq})
			return
		}
		prev := *bd.b
		prev.Rows = append([]ranking.BoardRow(nil), bd.b.Rows...)
		bd.apply(&p)
		bd.rebuild()
		s.announce(bd, &prev)
		seq := bd.seq
		bd.mu.Unlock()
		pushes.WithLabelValues("delta", "ok").Inc()
		writeJSON(w, http.StatusOK, map[string]int64{"seq": seq})
	default:
		http.Error(w, "unknown kind", http.StatusBadRequest)
	}
}

// announce tells spectators what changed from old (under bd.mu): the
// changed rows as ready-made HTML, or a reload when the header (tasks,
// settings, freezing) changed. Unfreezing sends the rows bottom-up, marked
// "unfrozen", and the page reveals them one by one.
func (s *Server) announce(bd *board, old *ranking.Board) {
	if len(bd.subs) == 0 {
		return
	}
	unfrozen := old != nil && old.Frozen && !bd.b.Frozen && sameHeader(unfreezeHeader(old), unfreezeHeader(bd.b))
	if !unfrozen && (old == nil || !sameHeader(old, bd.b)) {
		bd.broadcast(webkit.SSEFrame("reload", []byte("{}")))
		return
	}
	changed, removed := ranking.Diff(old, bd.b)
	if len(changed) == 0 && len(removed) == 0 && !unfrozen {
		return
	}
	if unfrozen {
		// Worst previous rank first, as a resolver reveals them.
		prevRank := make(map[string]int, len(old.Rows))
		for _, r := range old.Rows {
			prevRank[r.Key] = r.Rank
		}
		sort.SliceStable(changed, func(i, j int) bool { return prevRank[changed[i].Key] > prevRank[changed[j].Key] })
	}
	type rowHTML struct {
		Key  string `json:"key"`
		Rank int    `json:"rank"`
		Name string `json:"name"`
		HTML string `json:"html"`
	}
	msg := struct {
		Seq      int64     `json:"seq"`
		Rows     []rowHTML `json:"rows"`
		Removed  []string  `json:"removed,omitempty"`
		Unfrozen bool      `json:"unfrozen,omitempty"`
	}{Seq: bd.seq, Removed: removed, Unfrozen: unfrozen}
	for _, r := range changed {
		var sb strings.Builder
		if err := s.pages["partials"].ExecuteTemplate(&sb, "row", rowView{B: bd.b, R: r}); err != nil {
			s.log.Error("render row", "error", err)
			continue
		}
		msg.Rows = append(msg.Rows, rowHTML{Key: r.Key, Rank: r.Rank, Name: r.Name, HTML: sb.String()})
	}
	data, _ := json.Marshal(msg)
	bd.broadcast(webkit.SSEFrame("rows", data))
}

// unfreezeHeader is b without its freeze state.
func unfreezeHeader(b *ranking.Board) *ranking.Board {
	h := b.Header()
	h.Frozen, h.FreezeAt = false, nil
	return &h
}

func sameHeader(a, b *ranking.Board) bool {
	ha, _ := json.Marshal(a.Header())
	hb, _ := json.Marshal(b.Header())
	return string(ha) == string(hb)
}

func (s *Server) board(name string, create bool) *board {
	s.mu.Lock()
	defer s.mu.Unlock()
	bd := s.boards[name]
	if bd == nil && create {
		bd = newBoard()
		s.boards[name] = bd
	}
	return bd
}

// handleAssetPut stores a flag or photo by its SHA-256.
func (s *Server) handleAssetPut(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	digest := r.PathValue("digest")
	if !digestRe.MatchString(digest) || s.cfg.DataDir == "" {
		http.Error(w, "bad asset", http.StatusBadRequest)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
	if err != nil || !strings.HasPrefix(http.DetectContentType(data), "image/") || sha256hex(data) != digest {
		http.Error(w, "bad asset", http.StatusBadRequest)
		return
	}
	path := filepath.Join(s.cfg.DataDir, "assets", digest)
	if err := os.WriteFile(path+".tmp", data, 0o644); err != nil || os.Rename(path+".tmp", path) != nil {
		http.Error(w, "cannot store", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAsset(w http.ResponseWriter, r *http.Request) {
	digest := r.PathValue("digest")
	if !digestRe.MatchString(digest) || s.cfg.DataDir == "" {
		http.NotFound(w, r)
		return
	}
	data, err := os.ReadFile(filepath.Join(s.cfg.DataDir, "assets", digest))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(data))
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Write(data)
}

// ---------------------------------------------------------------- persistence

func (s *Server) load() error {
	names, err := filepath.Glob(filepath.Join(s.cfg.DataDir, "*.json"))
	if err != nil {
		return err
	}
	for _, n := range names {
		data, err := os.ReadFile(n)
		if err != nil {
			return err
		}
		var p ranking.Push
		if err := json.Unmarshal(data, &p); err != nil || p.Board == nil || !nameRe.MatchString(p.Contest) {
			s.log.Warn("skipping unreadable board", "file", n, "error", err)
			continue
		}
		bd := newBoard()
		bd.b, bd.seq, bd.keyHash = p.Board, p.Seq, p.KeyHash
		if p.History != nil {
			bd.history = p.History
		}
		bd.rebuild()
		bd.dirty = false
		s.boards[p.Contest] = bd
	}
	return nil
}

func (s *Server) saveAll() {
	if s.cfg.DataDir == "" {
		return
	}
	s.mu.RLock()
	names := make([]string, 0, len(s.boards))
	for n := range s.boards {
		names = append(names, n)
	}
	s.mu.RUnlock()
	for _, n := range names {
		bd := s.board(n, false)
		if bd == nil {
			continue
		}
		bd.mu.Lock()
		if !bd.dirty || bd.b == nil {
			bd.mu.Unlock()
			continue
		}
		data, err := json.Marshal(ranking.Push{Contest: n, Kind: "full", Seq: bd.seq, Board: bd.b, History: bd.history, KeyHash: bd.keyHash})
		bd.dirty = false
		bd.mu.Unlock()
		if err == nil {
			path := filepath.Join(s.cfg.DataDir, n+".json")
			if err = os.WriteFile(path+".tmp", data, 0o644); err == nil {
				err = os.Rename(path+".tmp", path)
			}
		}
		if err != nil {
			s.log.Error("save board", "contest", n, "error", err)
		}
	}
}

// ---------------------------------------------------------------- pages

// page is the template data of a scoreboard page.
type page struct {
	Lang  string
	Title string
	Board *ranking.Board
	Name  string // contest
	Key   string
	Data  any
	Live  bool
}

func (p *page) T(msg string, args ...any) string { return i18n.T(p.Lang, msg, args...) }

func lang(r *http.Request) string {
	explicit := r.URL.Query().Get("lang")
	if explicit == "" {
		if c, err := r.Cookie("cms_lang"); err == nil {
			explicit = c.Value
		}
	}
	return i18n.Negotiate(explicit, nil, r.Header.Get("Accept-Language"), nil)
}

// withBoard resolves the contest's board and checks the key of
// administrator-only boards (?key=..., then remembered in a cookie).
func (s *Server) withBoard(h func(http.ResponseWriter, *http.Request, string, *board, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("contest")
		bd := s.board(name, false)
		if bd == nil || !nameRe.MatchString(name) {
			http.NotFound(w, r)
			return
		}
		key := r.URL.Query().Get("key")
		if key == "" {
			if c, err := r.Cookie("rws_key_" + name); err == nil {
				key = c.Value
			}
		}
		bd.mu.RLock()
		ok := bd.b != nil && bd.allowed(key)
		private := bd.keyHash != ""
		bd.mu.RUnlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		if private && r.URL.Query().Get("key") != "" {
			http.SetCookie(w, &http.Cookie{Name: "rws_key_" + name, Value: key, Path: "/" + name + "/", HttpOnly: true,
				SameSite: http.SameSiteLaxMode, MaxAge: 7 * 24 * 3600})
		}
		h(w, r, name, bd, key)
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	type item struct{ Name, Title string }
	var list []item
	s.mu.RLock()
	for n, bd := range s.boards {
		bd.mu.RLock()
		if bd.b != nil && bd.keyHash == "" {
			list = append(list, item{n, bd.b.Title})
		}
		bd.mu.RUnlock()
	}
	s.mu.RUnlock()
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	p := &page{Lang: lang(r), Title: s.cfg.Title, Data: list}
	webkit.Render(w, s.log, s.pages["index"], "layout", http.StatusOK, p)
}

func (s *Server) handleBoard(w http.ResponseWriter, r *http.Request, name string, bd *board, key string) {
	l := lang(r)
	bd.mu.RLock()
	cached, etag := bd.pages[l], bd.etag
	bd.mu.RUnlock()
	w.Header().Set("Cache-Control", "no-cache")
	if cached == nil {
		bd.mu.Lock()
		p := &page{Lang: l, Title: bd.b.Title, Board: bd.b, Name: name, Live: true}
		var sb strings.Builder
		if err := s.pages["board"].ExecuteTemplate(&sb, "layout", p); err != nil {
			bd.mu.Unlock()
			s.log.Error("render board", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		cached = []byte(sb.String())
		bd.pages[l] = cached
		etag = bd.etag
		bd.mu.Unlock()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("ETag", etag+l)
	w.Write(cached)
}

func (s *Server) handleJSON(w http.ResponseWriter, r *http.Request, name string, bd *board, key string) {
	bd.mu.RLock()
	data, gz, etag := bd.json, bd.gz, bd.etag
	bd.mu.RUnlock()
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-cache")
	h.Set("ETag", etag)
	h.Set("Vary", "Accept-Encoding")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		h.Set("Content-Encoding", "gzip")
		w.Write(gz)
		return
	}
	w.Write(data)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, name string, bd *board, key string) {
	if s.clients.Load() >= int64(s.cfg.MaxClients) {
		w.Header().Set("Retry-After", "30")
		http.Error(w, "too many spectators", http.StatusServiceUnavailable)
		return
	}
	ch := make(chan []byte, 16)
	bd.mu.Lock()
	bd.subs[ch] = struct{}{}
	bd.mu.Unlock()
	s.clients.Add(1)
	spectators.WithLabelValues(name).Inc()
	defer func() {
		bd.mu.Lock()
		delete(bd.subs, ch)
		bd.mu.Unlock()
		s.clients.Add(-1)
		spectators.WithLabelValues(name).Dec()
	}()
	webkit.ServeSSE(w, r, ch, 25*time.Second, 3*time.Second)
}

// userPage is a row's score history.
type userPage struct {
	Row    ranking.BoardRow
	Points []ranking.Point
	Chart  template.HTML
}

func (s *Server) handleUser(w http.ResponseWriter, r *http.Request, name string, bd *board, key string) {
	k := r.PathValue("key")
	bd.mu.RLock()
	var row *ranking.BoardRow
	for i := range bd.b.Rows {
		if bd.b.Rows[i].Key == k {
			row = &bd.b.Rows[i]
		}
	}
	var d userPage
	if row != nil {
		d = userPage{Row: *row, Points: append([]ranking.Point(nil), bd.history[k]...)}
	}
	b := bd.b
	bd.mu.RUnlock()
	if row == nil {
		http.NotFound(w, r)
		return
	}
	if r.URL.Query().Get("format") == "json" {
		writeJSON(w, http.StatusOK, d.Points)
		return
	}
	d.Chart = chart(d.Points, b.Start, b.Stop, maxTotal(b))
	p := &page{Lang: lang(r), Title: d.Row.Name, Board: b, Name: name, Data: d}
	webkit.Render(w, s.log, s.pages["user"], "layout", http.StatusOK, p)
}

func maxTotal(b *ranking.Board) float64 {
	if b.ICPC {
		return float64(len(b.Tasks))
	}
	t := 0.0
	for _, task := range b.Tasks {
		t += task.MaxScore
	}
	return t
}

// chart draws a step chart of a score history as inline SVG.
func chart(pts []ranking.Point, start, stop time.Time, maxY float64) template.HTML {
	const w, h = 600.0, 200.0
	if maxY <= 0 || !stop.After(start) {
		return ""
	}
	x := func(t time.Time) float64 {
		f := float64(t.Sub(start)) / float64(stop.Sub(start))
		return max(0, min(1, f)) * w
	}
	y := func(v float64) float64 { return h - max(0, min(1, v/maxY))*h }
	var sb strings.Builder
	fmt.Fprintf(&sb, `<svg class="chart" viewBox="0 0 %g %g" role="img" preserveAspectRatio="none"><polyline class="line" points="0,%g`, w, h, h)
	prev := 0.0
	for _, p := range pts {
		fmt.Fprintf(&sb, " %.1f,%.1f %.1f,%.1f", x(p.Time), y(prev), x(p.Time), y(p.Total))
		prev = p.Total
	}
	fmt.Fprintf(&sb, ` %g,%.1f"/></svg>`, w, y(prev))
	return template.HTML(sb.String())
}

// ---------------------------------------------------------------- helpers

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
