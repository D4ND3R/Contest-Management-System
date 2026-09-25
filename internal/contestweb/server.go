// Package contestweb is the contestant portal (CWS): login, statements,
// submissions with live status over Server-Sent Events, user tests,
// communication and printing.
//
// Pages are server-rendered with html/template and enhanced with htmx; all
// hot data (contest settings, tasks, participations) is cached in memory and
// invalidated through Redis events, so most requests do at most one or two
// indexed queries.
package contestweb

import (
	"context"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/app"
	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/contest"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/events"
	"github.com/D4ND3R/Contest-Management-System/internal/httpx"
	"github.com/D4ND3R/Contest-Management-System/internal/i18n"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/metrics"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/D4ND3R/Contest-Management-System/internal/ranking"
	"github.com/D4ND3R/Contest-Management-System/internal/webkit"
	"github.com/D4ND3R/Contest-Management-System/web"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Server is the contest web server.
type Server struct {
	cfg      config.ContestWeb
	log      *slog.Logger
	pool     *pgxpool.Pool
	q        *sqlc.Queries
	rdb      *redis.Client
	queue    *queue.Queue
	ns       string
	blobs    blob.Store
	langs    *langs.Registry
	pages    map[string]*template.Template
	static   *webkit.Static
	csrf     *webkit.CSRF
	signer   *webkit.Signer
	ips      *webkit.IPResolver
	limiter  *webkit.Limiter
	cache    *cache
	secret   []byte
	hub      *hub
	sessions *webkit.SessionTracker
	boards   boardCache
	checks   []httpx.Check
	now      func() time.Time
}

// Deps are the dependencies of the server.
type Deps struct {
	Pool   *pgxpool.Pool
	Redis  *redis.Client
	Blobs  blob.Store
	Langs  *langs.Registry
	Secret []byte
	NS     string
	Checks []httpx.Check
}

// New builds a server.
func New(cfg config.ContestWeb, d Deps, log *slog.Logger) (*Server, error) {
	static, err := webkit.NewStatic(web.Static, "/static")
	if err != nil {
		return nil, err
	}
	ips, err := webkit.NewIPResolver(cfg.TrustedProxies)
	if err != nil {
		return nil, err
	}
	q := sqlc.New(d.Pool)
	s := &Server{
		cfg: cfg, log: log, pool: d.Pool, q: q, rdb: d.Redis, queue: queue.New(d.Redis, d.NS), ns: d.NS,
		blobs: d.Blobs, langs: d.Langs, static: static, csrf: webkit.NewCSRF(d.Secret),
		signer: webkit.NewSigner(d.Secret, "cws-session"), ips: ips, limiter: webkit.NewLimiter(d.Redis, d.NS),
		secret: d.Secret, cache: newCache(q, d.Langs, 3*time.Second), hub: newHub(), sessions: webkit.NewSessionTracker(d.Redis, d.NS),
		checks: d.Checks, now: time.Now,
	}
	if err := s.loadTemplates(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Server) loadTemplates() error {
	funcs := template.FuncMap{
		"static":  s.static.URL,
		"row":     func(p *page, sv subView) rowCtx { return rowCtx{P: p, S: sv} },
		"testrow": func(p *page, v testView) testCtx { return testCtx{P: p, T: v} },
		"cell":    ranking.Display,
		"fscore":  ranking.FormatScore,
		// signed writes a score change with its sign (+5, -2.5).
		"signed": func(v float64) string {
			if v > 0 {
				return "+" + strconv.FormatFloat(v, 'f', -1, 64)
			}
			return strconv.FormatFloat(v, 'f', -1, 64)
		},
	}
	base, err := template.New("").Funcs(funcs).ParseFS(web.Templates, "cws/layout.html", "cws/partials.html")
	if err != nil {
		return err
	}
	s.pages = map[string]*template.Template{}
	names, err := fs.Glob(web.Templates, "cws/*.html")
	if err != nil {
		return err
	}
	for _, n := range names {
		short := strings.TrimSuffix(strings.TrimPrefix(n, "cws/"), ".html")
		if short == "layout" || short == "partials" {
			continue
		}
		t, err := template.Must(base.Clone()).ParseFS(web.Templates, n)
		if err != nil {
			return err
		}
		s.pages[short] = t
	}
	s.pages["partials"] = base
	return nil
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	// Top level: assets and operations endpoints; everything else is under
	// a contest name (the names below are reserved, see ReservedNames).
	top := http.NewServeMux()
	top.Handle("GET /static/", s.static)
	top.Handle("GET /healthz", httpx.HealthHandler("contest-web", s.checks...))
	top.Handle("GET /metrics", metrics.Handler())
	top.HandleFunc("GET /{$}", s.handleIndex)
	top.HandleFunc("POST /lang", s.handleLang)
	mux := http.NewServeMux()
	top.Handle("/", mux)

	mux.HandleFunc("GET /{contest}/login", s.withContest(s.handleLoginForm))
	mux.HandleFunc("POST /{contest}/login", s.withContest(s.handleLogin))
	mux.HandleFunc("GET /{contest}/register", s.withContest(s.handleRegisterForm))
	mux.HandleFunc("POST /{contest}/register", s.withContest(s.handleRegister))
	mux.HandleFunc("POST /{contest}/lang", s.withContest(s.handleLang))
	mux.HandleFunc("POST /{contest}/logout", s.withContest(s.handleLogout))
	mux.HandleFunc("GET /{contest}/impersonate", s.withContest(s.handleImpersonate))

	auth := func(h func(http.ResponseWriter, *http.Request, *reqCtx)) http.HandlerFunc {
		return s.withContest(s.withAuth(h))
	}
	mux.HandleFunc("GET /{contest}/{$}", auth(s.handleOverview))
	mux.HandleFunc("GET /{contest}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/"+r.PathValue("contest")+"/", http.StatusMovedPermanently)
	})
	mux.HandleFunc("POST /{contest}/start", auth(s.handleStart))
	mux.HandleFunc("GET /{contest}/tasks/{task}", auth(s.handleTask))
	mux.HandleFunc("GET /{contest}/tasks/{task}/statement/{lang}", auth(s.handleStatement))
	mux.HandleFunc("GET /{contest}/tasks/{task}/attachments/{file}", auth(s.handleAttachment))
	mux.HandleFunc("POST /{contest}/tasks/{task}/submit", auth(s.handleSubmit))
	mux.HandleFunc("POST /{contest}/tasks/{task}/test", auth(s.handleUserTest))
	mux.HandleFunc("GET /{contest}/tests/{id}/row", auth(s.handleUserTestRow))
	mux.HandleFunc("GET /{contest}/tests/{id}/{which}", auth(s.handleUserTestFile))
	mux.HandleFunc("GET /{contest}/tasks/{task}/submissions", auth(s.handleSubmissionList))
	mux.HandleFunc("GET /{contest}/submissions/{id}", auth(s.handleSubmission))
	mux.HandleFunc("GET /{contest}/submissions/{id}/row", auth(s.handleSubmissionRow))
	mux.HandleFunc("POST /{contest}/submissions/{id}/token", auth(s.handleToken))
	mux.HandleFunc("GET /{contest}/submissions/{id}/file/{name}", auth(s.handleSubmissionFile))
	mux.HandleFunc("GET /{contest}/documentation", auth(s.handleDocumentation))
	mux.HandleFunc("GET /{contest}/events", auth(s.handleEvents))
	mux.HandleFunc("GET /{contest}/clock", auth(s.handleClock))
	mux.HandleFunc("GET /{contest}/printing", auth(s.handlePrinting))
	mux.HandleFunc("POST /{contest}/printing", auth(s.handlePrint))
	s.registerExtra(mux, auth)

	var h http.Handler = top
	h = webkit.SecurityHeaders(h)
	h = httpx.Observe("contest-web", s.log, h)
	return h
}

// ReservedNames cannot be used as contest names (they are CWS routes).
var ReservedNames = []string{"static", "healthz", "metrics", "lang"}

// Run serves HTTP and the event fan-out until ctx ends.
func (s *Server) Run(ctx context.Context, addr string, ready chan<- net.Addr) error {
	g, ctx := app.NewGroup(ctx)
	g.Go(func(ctx context.Context) error {
		for ctx.Err() == nil {
			err := events.Subscribe(ctx, s.rdb, s.ns, s.onEvent)
			if ctx.Err() == nil {
				s.log.Warn("event subscription ended; retrying", "error", err)
				time.Sleep(time.Second)
			}
		}
		return nil
	})
	g.Go(func(ctx context.Context) error { return httpx.Serve(ctx, s.log, addr, s.Handler(), ready) })
	return g.Wait()
}

// onEvent invalidates caches and forwards notifications to browsers.
func (s *Server) onEvent(e events.Event) {
	switch e.Type {
	case events.TypeContest:
		s.cache.invalidateContest(e.ContestID)
		if e.ParticipationID != 0 {
			s.cache.invalidateParticipation(e.ParticipationID)
		}
		// Times may have changed (an extension, extra time): open pages ask
		// for their clock again.
		s.hub.publish(events.Event{Type: "clock", ContestID: e.ContestID, ParticipationID: e.ParticipationID})
		return
	case events.TypeAlert, events.TypeQuestionNew, events.TypeBalloon:
		return // for admins only
	case events.TypeSubmission:
		// Team contests: teammates' pages follow the submission too.
		if pv, err := s.cache.participation(context.Background(), e.ParticipationID); err == nil && pv.TeamID != nil {
			s.hub.publishTeam(e, pv.ContestID, *pv.TeamID)
			return
		}
	}
	s.hub.publish(e)
}

// reqCtx is the per-request state of an authenticated contestant.
type reqCtx struct {
	ctx     context.Context
	contest *contestView
	part    sqlc.GetParticipationViewRow
	// group is the participation, or every participation of its team in a
	// team contest (they share submissions, limits and scores).
	group  []int64
	sess   *webkit.Session
	lang   string
	status contest.Status
	ip     netip.Addr
	now    time.Time
}

// sessionTTL is how long a contestant session lasts: the contest's
// setting, 24 hours by default.
func sessionTTL(cv *contestView) time.Duration {
	if cv.SessionMinutes != nil {
		return time.Duration(*cv.SessionMinutes) * time.Minute
	}
	return 24 * time.Hour
}

// sessionCookie is the codec that writes new sessions of a contest.
func (s *Server) sessionCookie(cv *contestView) *webkit.CookieCodec {
	c := s.cookie(cv.ID)
	c.TTL = sessionTTL(cv)
	return c
}

func (s *Server) cookie(contestID int64) *webkit.CookieCodec {
	return &webkit.CookieCodec{Name: "cms_c" + itoa(contestID), Signer: s.signer, Secure: s.cfg.CookieSecure, TTL: 24 * time.Hour}
}

func (s *Server) anonCookie() *webkit.CookieCodec {
	return &webkit.CookieCodec{Name: "cms_anon", Signer: s.signer, Secure: s.cfg.CookieSecure, TTL: 24 * time.Hour}
}

type ctxKey int

const contestKey ctxKey = 0

// withContest resolves {contest}.
func (s *Server) withContest(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cv, err := s.cache.contest(r.Context(), r.PathValue("contest"))
		if err != nil {
			if errors.Is(err, errNotFound) {
				s.errorPage(w, r, nil, http.StatusNotFound, "Not found", "This contest does not exist.")
				return
			}
			s.log.Error("load contest", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if cv.Status == "draft" && !s.adminPreview(r, cv) {
			// Drafts exist only for the organizers' read-only preview.
			s.errorPage(w, r, nil, http.StatusNotFound, "Not found", "This contest does not exist.")
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), contestKey, cv)))
	}
}

// adminPreview reports whether the request is an administrator's read-only
// view of the contest (or the link that opens one).
func (s *Server) adminPreview(r *http.Request, cv *contestView) bool {
	if strings.HasSuffix(r.URL.Path, "/impersonate") {
		return true
	}
	sess := s.cookie(cv.ID).Read(r)
	return sess != nil && sess.ReadOnly && sess.ContestID == cv.ID
}

func contestOf(r *http.Request) *contestView { return r.Context().Value(contestKey).(*contestView) }

// withAuth requires a valid contestant session (or IP autologin).
func (s *Server) withAuth(h func(http.ResponseWriter, *http.Request, *reqCtx)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cv := contestOf(r)
		ip := s.ips.ClientIP(r)
		sess := s.cookie(cv.ID).Read(r)
		if sess == nil && cv.IpAutologin {
			sess = s.autologin(w, r, cv, ip)
		}
		if sess == nil || sess.ContestID != cv.ID || sess.ParticipationID == 0 {
			s.redirectLogin(w, r, cv)
			return
		}
		part, err := s.cache.participation(r.Context(), sess.ParticipationID)
		if err != nil {
			s.cookie(cv.ID).Clear(w)
			s.redirectLogin(w, r, cv)
			return
		}
		if sess.Nonce != part.LoginNonce {
			// The nonce is bumped by a new login (single-login contests) and
			// when the organizers close the participation's sessions.
			s.cookie(cv.ID).Clear(w)
			msg := "Your session was closed by the organizers."
			if cv.SingleLogin {
				msg = "You logged in from another place."
			}
			s.errorPage(w, r, cv, http.StatusUnauthorized, "Logged out", msg)
			return
		}
		if part.Disabled {
			s.cookie(cv.ID).Clear(w)
			s.errorPage(w, r, cv, http.StatusForbidden, "Forbidden", "Your account is disabled.")
			return
		}
		if !part.Approved && !sess.ReadOnly {
			s.cookie(cv.ID).Clear(w)
			s.errorPage(w, r, cv, http.StatusForbidden, "Forbidden", msgPendingApproval)
			return
		}
		// A shorter session duration applies to sessions opened before
		// the setting changed too.
		if !sess.ReadOnly && time.Since(time.Unix(sess.Issued, 0)) > sessionTTL(cv) {
			s.cookie(cv.ID).Clear(w)
			s.redirectLogin(w, r, cv)
			return
		}
		if cv.IpRestriction && len(part.Ip) > 0 && !ipAllowed(part.Ip, ip) {
			s.errorPage(w, r, cv, http.StatusForbidden, "Forbidden", "You cannot access the contest from this address.")
			return
		}
		now := s.now()
		rc := &reqCtx{ctx: r.Context(), contest: cv, part: part, sess: sess, ip: ip, now: now,
			lang: s.language(r, cv, part.PreferredLanguages, sess.Lang)}
		rc.group = []int64{part.ID}
		if cv.TeamMode && part.TeamID != nil {
			if ids, err := s.cache.team(r.Context(), cv.ID, *part.TeamID); err == nil && len(ids) > 0 {
				rc.group = ids
			}
		}
		rc.status = contest.Compute(cv.Rules, participantOf(part), now)
		if cv.Status == "archived" {
			// Archived contests are read-only for everybody.
			rc.status = contest.Status{Phase: contest.Finished, Begin: rc.status.Begin, End: rc.status.End}
		}
		if r.Method == http.MethodPost && sess.ReadOnly {
			s.errorPage(w, r, cv, http.StatusForbidden, "Forbidden", "This is a read-only view for administrators.")
			return
		}
		if r.Method == http.MethodPost {
			if err := s.csrf.Check(r, sess.ID); err != nil {
				s.errorPage(w, r, cv, http.StatusForbidden, "Forbidden", "Your session expired; reload the page and try again.")
				return
			}
		}
		if !sess.ReadOnly {
			s.sessions.Touch(part.ID, sess, ip, r.UserAgent())
		}
		h(w, r, rc)
	}
}

func participantOf(p sqlc.GetParticipationViewRow) contest.Participant {
	return contest.Participant{StartingTime: p.StartingTime, SiteStart: p.SiteStartTime, Delay: time.Duration(p.DelayTimeS) * time.Second,
		Extra: time.Duration(p.ExtraTimeS) * time.Second, Unrestricted: p.Unrestricted}
}

func ipAllowed(prefixes []netip.Prefix, ip netip.Addr) bool {
	for _, p := range prefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

func (s *Server) redirectLogin(w http.ResponseWriter, r *http.Request, cv *contestView) {
	url := "/" + cv.Name + "/login"
	if webkit.IsHTMX(r) || strings.HasSuffix(r.URL.Path, "/events") {
		w.Header().Set("HX-Redirect", url)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, url, http.StatusSeeOther)
}

// language negotiates the UI language of a request.
func (s *Server) language(r *http.Request, cv *contestView, preferred []string, explicit string) string {
	if c, err := r.Cookie("cms_lang"); err == nil && explicit == "" {
		explicit = c.Value
	}
	var allowed []string
	if cv != nil {
		allowed = cv.AllowedLocalizations
	}
	return i18n.Negotiate(explicit, preferred, r.Header.Get("Accept-Language"), allowed)
}

// newPage builds the template data of an authenticated page.
func (s *Server) newPage(rc *reqCtx, title, active string) *page {
	loc := rc.contest.Loc
	if rc.part.Timezone != nil {
		if l, err := time.LoadLocation(*rc.part.Timezone); err == nil {
			loc = l
		}
	}
	p := &page{
		Lang: rc.lang, Title: title, CSRF: s.csrf.Token(rc.sess.ID), Base: "/" + rc.contest.Name + "/",
		Contest: rc.contest, Part: &rc.part, Status: statusView{rc.status}, Active: active,
		EventsURL: "/" + rc.contest.Name + "/events", ServerTime: rc.now.UnixMilli(),
		UILanguages: uiLanguages(rc.contest.AllowedLocalizations), loc: loc,
	}
	if rc.sess.ReadOnly {
		p.ViewAs = rc.part.Username
	}
	// Unread announcements, messages and answers (the nav badge; live
	// updates arrive over SSE).
	if n, err := s.q.CountUnreadCommunication(rc.ctx, sqlc.CountUnreadCommunicationParams{ParticipationID: rc.part.ID,
		ContestID: rc.contest.ID}); err == nil {
		p.Unread = n
	}
	if rc.status.Phase != contest.NotStarted && rc.status.Phase != contest.WaitingStart || rc.part.Unrestricted {
		p.Tasks = rc.contest.Tasks
	}
	p.Ranking = rankingVisible(rc.contest.Contest, rc.now)
	return p
}

func (s *Server) render(w http.ResponseWriter, name string, status int, p *page) {
	t := s.pages[name]
	if t == nil {
		s.log.Error("unknown page template", "name", name)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	webkit.Render(w, s.log, t, "layout", status, p)
}

func (s *Server) renderPartial(w http.ResponseWriter, name string, data any) {
	webkit.Render(w, s.log, s.pages["partials"], name, http.StatusOK, data)
}

// errorPage renders an error for authenticated or anonymous visitors.
func (s *Server) errorPage(w http.ResponseWriter, r *http.Request, cv *contestView, status int, title, msg string) {
	lang := s.language(r, cv, nil, "")
	p := &page{Lang: lang, Contest: cv, loc: time.UTC, UILanguages: uiLanguages(nil)}
	p.Title, p.Error = p.T(title), i18n.TDetail(lang, msg)
	if cv != nil {
		p.Base = "/" + cv.Name + "/"
		p.loc = cv.Loc
	}
	if webkit.IsHTMX(r) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		w.Write([]byte(p.Error))
		return
	}
	s.render(w, "error", status, p)
}

func itoa(v int64) string {
	b := make([]byte, 0, 20)
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	for v > 0 {
		b = append(b, byte('0'+v%10))
		v /= 10
	}
	if neg {
		b = append(b, '-')
	}
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}
