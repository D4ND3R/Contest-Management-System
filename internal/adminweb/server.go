// Package adminweb is the administration web server (AWS): contests,
// tasks, datasets, users, teams and participations; submissions with
// filters, source download and diffs; reevaluations; live worker and queue
// status; ranking exports and statistics; administrators with roles and an
// audit log of every administrative action.
package adminweb

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/D4ND3R/Contest-Management-System/internal/backup"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/app"
	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/events"
	"github.com/D4ND3R/Contest-Management-System/internal/httpx"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/metrics"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/D4ND3R/Contest-Management-System/internal/webkit"
	"github.com/D4ND3R/Contest-Management-System/web"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Server is the admin web server.
type Server struct {
	cfg      config.AdminWeb
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
	flash    *webkit.Signer
	ips      *webkit.IPResolver
	limiter  *webkit.Limiter
	admins   *adminCache
	hub      *adminHub
	sessions *webkit.SessionTracker
	secret   []byte
	// contestListen is the contest web server's listen address (links).
	contestListen string
	// rankingURL is the public address of the ranking web server (links).
	rankingURL string
	// backups takes and lists backups (nil: not configured).
	backups *backup.Runner
	checks  []httpx.Check
	now     func() time.Time
	// MaxUploadBytes bounds multipart requests (testcase archives).
	maxUpload int64
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
	// ContestListen is the contest web server's listen address, used to
	// build links when admin_web.contest_url is not set.
	ContestListen string
	// RankingURL is the public address of the ranking web server.
	RankingURL string
	// Backups takes the backups ("Back up now") and lists them.
	Backups *backup.Runner
}

// New builds a server.
func New(cfg config.AdminWeb, d Deps, log *slog.Logger) (*Server, error) {
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
		signer: webkit.NewSigner(d.Secret, "aws-session"), flash: webkit.NewSigner(d.Secret, "aws-flash"),
		ips: ips, limiter: webkit.NewLimiter(d.Redis, d.NS), admins: &adminCache{q: q, m: map[int64]adminEntry{}},
		hub: &adminHub{clients: map[chan []byte]struct{}{}}, checks: d.Checks, now: time.Now,
		sessions: webkit.NewSessionTracker(d.Redis, d.NS), secret: d.Secret, contestListen: d.ContestListen, rankingURL: d.RankingURL, backups: d.Backups,
		maxUpload: 1 << 30,
	}
	if err := s.loadTemplates(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Server) loadTemplates() error {
	base, err := template.New("").Funcs(s.funcs()).ParseFS(web.Templates, "aws/layout.html", "aws/partials.html")
	if err != nil {
		return err
	}
	s.pages = map[string]*template.Template{}
	names, err := fs.Glob(web.Templates, "aws/*.html")
	if err != nil {
		return err
	}
	for _, n := range names {
		short := strings.TrimSuffix(strings.TrimPrefix(n, "aws/"), ".html")
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

// perm is what a route requires.
type perm int

const (
	permRead      perm = iota // every enabled administrator
	permMessaging             // "messaging" and "all"
	permAll                   // "all" only
)

func roleAllows(role string, p perm) bool {
	switch role {
	case "all":
		return true
	case "messaging":
		return p <= permMessaging
	case "read_only":
		return p == permRead
	}
	return false
}

// handler is an authenticated admin handler.
type handler func(w http.ResponseWriter, r *http.Request, rc *reqCtx)

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", s.static)
	mux.Handle("GET /healthz", httpx.HealthHandler("admin-web", s.checks...))
	mux.Handle("GET /metrics", metrics.Handler())
	mux.HandleFunc("GET /login", s.handleLoginForm)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("POST /login/2fa", s.handleLogin2FA)
	mux.HandleFunc("POST /lang", s.handleLang)

	// route registers pattern with the permission it needs; mutating
	// requests are audited under action.
	route := func(pattern string, p perm, action string, h handler) {
		mux.HandleFunc(pattern, s.withAdmin(p, action, h))
	}
	get := func(pattern string, h handler) { route("GET "+pattern, permRead, "", h) }
	post := func(pattern string, p perm, action string, h handler) { route("POST "+pattern, p, action, h) }

	post("/logout", permRead, "", s.handleLogout)
	get("/account", s.handleAccount)
	post("/account/2fa/start", permRead, "", s.handle2FAStart)
	post("/account/2fa/enable", permRead, "account.2fa_enable", s.handle2FAEnable)
	post("/account/2fa/disable", permRead, "account.2fa_disable", s.handle2FADisable)
	post("/account/password", permRead, "account.password", s.handleAccountPassword)
	get("/{$}", s.handleDashboard)
	get("/events", s.handleEvents)

	get("/contests", s.handleContests)
	get("/contests/new", s.handleContestNew)
	post("/contests", permAll, "contest.create", s.handleContestCreate)
	get("/contests/{id}", s.handleContest)
	post("/contests/{id}", permAll, "contest.update", s.handleContestUpdate)
	post("/contests/{id}/delete", permAll, "contest.delete", s.handleContestDelete)
	post("/contests/{id}/clone", permAll, "contest.clone", s.handleContestClone)
	get("/contests/{id}/balloons", s.handleBalloons)
	post("/contests/{id}/balloons/deliver", permMessaging, "balloon.deliver", s.handleBalloonDeliver)
	get("/contests/{id}/printing", s.handlePrintQueue)
	get("/print-jobs/{id}/pdf", s.handlePrintJobPDF)
	post("/print-jobs/{id}/{action}", permMessaging, "print_job.action", s.handlePrintJobAction)
	post("/contests/{id}/extend", permAll, "contest.extend", s.handleContestExtend)
	post("/contests/{id}/tasks", permAll, "contest.add_task", s.handleContestAddTask)
	post("/contests/{id}/tasks/{task}/move", permAll, "contest.move_task", s.handleContestMoveTask)
	post("/contests/{id}/tasks/{task}/remove", permAll, "contest.remove_task", s.handleContestRemoveTask)
	get("/contests/{id}/participations", s.handleParticipations)
	post("/contests/{id}/reset-passwords", permAll, "contest.reset_passwords", s.handleContestResetPasswords)
	get("/contests/{id}/sites", s.handleSites)
	post("/contests/{id}/sites", permAll, "site.create", s.handleSiteCreate)
	post("/sites/{id}", permAll, "site.update", s.handleSiteUpdate)
	post("/sites/{id}/delete", permAll, "site.delete", s.handleSiteDelete)
	post("/contests/{id}/participations", permAll, "participation.create", s.handleParticipationCreate)
	get("/contests/{id}/submissions", s.handleSubmissions)
	get("/contests/{id}/ranking", s.handleRanking)
	post("/contests/{id}/ranking/freeze", permAll, "contest.ranking_freeze", s.handleRankingFreeze)
	get("/contests/{id}/ranking.csv", s.handleRankingCSV)
	get("/contests/{id}/ranking.json", s.handleRankingJSON)
	get("/contests/{id}/stats", s.handleStats)

	get("/questions", s.handleQuestions)
	get("/questions/count", s.handleQuestionCount)
	post("/questions/{id}/reply", permMessaging, "question.reply", s.handleQuestionReply)
	post("/questions/{id}/ignore", permMessaging, "question.ignore", s.handleQuestionIgnore)
	get("/contests/{id}/communication", s.handleContestCommunication)
	post("/contests/{id}/announcements", permMessaging, "announcement.create", s.handleAnnouncementCreate)
	post("/announcements/{id}/delete", permMessaging, "announcement.delete", s.handleAnnouncementDelete)
	post("/contests/{id}/messages", permMessaging, "message.create", s.handleMessageCreate)

	get("/participations/{id}", s.handleParticipation)
	post("/submissions/{id}/invalidate", permAll, "submission.invalidate", s.handleSubmissionInvalidate)
	post("/submissions/{id}/restore", permAll, "submission.restore", s.handleSubmissionRestore)
	post("/participations/{id}", permAll, "participation.update", s.handleParticipationUpdate)
	post("/participations/{id}/delete", permAll, "participation.delete", s.handleParticipationDelete)
	post("/participations/{id}/approve", permAll, "participation.approve", s.handleParticipationApprove)
	post("/participations/{id}/reject", permAll, "participation.reject", s.handleParticipationReject)
	post("/participations/{id}/view-as", permRead, "participation.view_as", s.handleViewAs)

	get("/tasks", s.handleTasks)
	post("/tasks", permAll, "task.create", s.handleTaskCreate)
	get("/tasks/import", s.handlePackageForm)
	post("/tasks/import", permAll, "task.import", s.handlePackageImport)
	get("/tasks/{id}/export.zip", s.handlePackageExport)
	get("/tasks/{id}/validation", s.handleValidation)
	post("/tasks/{id}/validation/rerun", permAll, "task.validation_rerun", s.handleValidationRerun)
	get("/tasks/{id}", s.handleTask)
	post("/tasks/{id}", permAll, "task.update", s.handleTaskUpdate)
	post("/tasks/{id}/delete", permAll, "task.delete", s.handleTaskDelete)
	post("/tasks/{id}/statements", permAll, "statement.upload", s.handleStatementUpload)
	get("/tasks/{id}/statements/{lang}", s.handleStatementDownload)
	post("/tasks/{id}/statements/{lang}/delete", permAll, "statement.delete", s.handleStatementDelete)
	post("/tasks/{id}/attachments", permAll, "attachment.upload", s.handleAttachmentUpload)
	get("/tasks/{id}/attachments/{file}", s.handleAttachmentDownload)
	post("/tasks/{id}/attachments/{file}/delete", permAll, "attachment.delete", s.handleAttachmentDelete)
	post("/tasks/{id}/datasets", permAll, "dataset.create", s.handleDatasetCreate)
	post("/tasks/{id}/tester", permAll, "task.test", s.handleTesterSubmit)

	get("/datasets/{id}", s.handleDataset)
	post("/datasets/{id}", permAll, "dataset.update", s.handleDatasetUpdate)
	post("/datasets/{id}/score-editor", permAll, "", s.handleScoreEditor)
	post("/datasets/{id}/activate", permAll, "dataset.activate", s.handleDatasetActivate)
	post("/datasets/{id}/delete", permAll, "dataset.delete", s.handleDatasetDelete)
	post("/datasets/{id}/managers", permAll, "manager.upload", s.handleManagerUpload)
	get("/datasets/{id}/managers/{file}", s.handleManagerDownload)
	post("/datasets/{id}/managers/{file}/delete", permAll, "manager.delete", s.handleManagerDelete)
	post("/datasets/{id}/testcases", permAll, "testcase.upload", s.handleTestcaseUpload)
	post("/datasets/{id}/testcases/archive", permAll, "testcase.upload_archive", s.handleTestcaseArchive)
	post("/testcases/{id}/public", permAll, "testcase.set_public", s.handleTestcasePublic)
	post("/testcases/{id}/delete", permAll, "testcase.delete", s.handleTestcaseDelete)
	get("/testcases/{id}/{which}", s.handleTestcaseDownload)

	get("/backups", s.handleBackups)
	post("/backups", permAll, "backup.create", s.handleBackupCreate)
	route("GET /backups/{name}/download", permAll, "", s.handleBackupDownload)
	post("/backups/{name}/delete", permAll, "backup.delete", s.handleBackupDelete)
	get("/submissions/diff", s.handleSubmissionDiff)
	get("/submissions/{id}", s.handleSubmission)
	get("/submissions/{id}/files/{name}", s.handleSubmissionFile)
	post("/reevaluate", permAll, "reevaluate", s.handleReevaluate)

	get("/users", s.handleUsers)
	get("/users/new", s.handleUserNew)
	post("/users", permAll, "user.create", s.handleUserCreate)
	post("/users/import", permAll, "user.import", s.handleUserImport)
	get("/users/export.csv", s.handleUsersExport)
	post("/credentials.pdf", permAll, "credentials.print", s.handleCredentialsPDF)
	get("/users/{id}", s.handleUser)
	get("/users/{id}/photo", s.handleUserPhoto)
	post("/users/{id}", permAll, "user.update", s.handleUserUpdate)
	post("/users/{id}/delete", permAll, "user.delete", s.handleUserDelete)
	post("/users/{id}/disable", permAll, "user.disable", s.handleUserDisable)
	post("/users/{id}/enable", permAll, "user.enable", s.handleUserEnable)
	post("/users/{id}/logout", permAll, "user.logout", s.handleUserLogout)
	post("/users/{id}/reset-password", permAll, "user.reset_password", s.handleUserResetPassword)

	get("/teams", s.handleTeams)
	post("/teams", permAll, "team.create", s.handleTeamCreate)
	get("/teams/{id}", s.handleTeam)
	post("/teams/{id}", permAll, "team.update", s.handleTeamUpdate)
	post("/teams/{id}/delete", permAll, "team.delete", s.handleTeamDelete)
	post("/teams/{id}/members", permAll, "team.add_members", s.handleTeamMembers)
	get("/teams/{id}/{kind}", s.handleTeamImage)

	get("/admins", s.handleAdmins)
	post("/admins", permAll, "admin.create", s.handleAdminCreate)
	get("/admins/{id}", s.handleAdmin)
	post("/admins/{id}", permAll, "admin.update", s.handleAdminUpdate)
	post("/admins/{id}/delete", permAll, "admin.delete", s.handleAdminDelete)

	get("/system", s.handleSystem)
	get("/system/status", s.handleSystemStatus)
	get("/languages", s.handleLanguages)
	get("/audit", s.handleAudit)
	s.registerExtra(route)

	var h http.Handler = mux
	h = webkit.SecurityHeaders(h)
	h = httpx.Observe("admin-web", s.log, h)
	return h
}

// Run serves HTTP and forwards live events to connected administrators.
func (s *Server) Run(ctx context.Context, addr string, ready chan<- net.Addr) error {
	g, ctx := app.NewGroup(ctx)
	g.Go(func(ctx context.Context) error {
		for ctx.Err() == nil {
			err := events.Subscribe(ctx, s.rdb, s.ns, s.hub.publish)
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

// ---------------------------------------------------------------- sessions

// reqCtx is the per-request state of an authenticated administrator.
type reqCtx struct {
	admin sqlc.Admin
	sess  *webkit.Session
	audit *auditEntry
}

func (s *Server) cookie() *webkit.CookieCodec {
	return &webkit.CookieCodec{Name: "cms_admin", Signer: s.signer, Secure: s.cfg.CookieSecure, TTL: 12 * time.Hour}
}

type adminEntry struct {
	a      sqlc.Admin
	loaded time.Time
}

// adminCache avoids a query per request; disabling or deleting an admin
// takes effect within the TTL.
type adminCache struct {
	q  *sqlc.Queries
	mu sync.Mutex
	m  map[int64]adminEntry
}

func (c *adminCache) get(ctx context.Context, id int64) (sqlc.Admin, error) {
	c.mu.Lock()
	e, ok := c.m[id]
	c.mu.Unlock()
	if ok && time.Since(e.loaded) < 3*time.Second {
		return e.a, nil
	}
	a, err := c.q.GetAdmin(ctx, id)
	if err != nil {
		return a, err
	}
	c.mu.Lock()
	c.m[id] = adminEntry{a: a, loaded: time.Now()}
	c.mu.Unlock()
	return a, nil
}

func (c *adminCache) drop(id int64) {
	c.mu.Lock()
	delete(c.m, id)
	c.mu.Unlock()
}

// withAdmin authenticates, authorises, checks CSRF on POST and records
// successful mutating requests in the audit log.
func (s *Server) withAdmin(p perm, action string, h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := s.cookie().Read(r)
		if sess == nil || sess.AdminID == 0 {
			s.toLogin(w, r)
			return
		}
		a, err := s.admins.get(r.Context(), sess.AdminID)
		if err != nil || !a.Enabled || sess.Nonce != adminNonce(a) {
			s.cookie().Clear(w)
			s.toLogin(w, r)
			return
		}
		rc := &reqCtx{admin: a, sess: sess}
		if r.Method == http.MethodPost {
			if err := s.csrf.Check(r, sess.ID); err != nil {
				s.errorPage(w, r, rc, http.StatusForbidden, "Your session expired; reload the page and try again.")
				return
			}
		}
		if !roleAllows(a.Role, p) {
			s.errorPage(w, r, rc, http.StatusForbidden, "Your role ("+a.Role+") does not allow this action.")
			return
		}
		if r.Method != http.MethodPost || action == "" {
			h(w, r, rc)
			return
		}
		rc.audit = &auditEntry{action: action}
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		h(sw, r, rc)
		if sw.status < 400 && !rc.audit.skip {
			s.writeAudit(r, rc)
		}
	}
}

// adminNonce binds sessions to the password hash: changing the password
// (or the role) logs every session of the admin out.
func adminNonce(a sqlc.Admin) int64 {
	var h int64 = 1469598103934665603
	for _, b := range []byte(a.PasswordHash + "|" + a.Role) {
		h ^= int64(b)
		h *= 1099511628211
	}
	return h
}

func (s *Server) toLogin(w http.ResponseWriter, r *http.Request) {
	if webkit.IsHTMX(r) || r.URL.Path == "/events" {
		w.Header().Set("HX-Redirect", "/login")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	next := r.URL.RequestURI()
	if r.Method != http.MethodGet || next == "/" {
		next = ""
	}
	u := "/login"
	if next != "" {
		u += "?next=" + urlQueryEscape(next)
	}
	http.Redirect(w, r, u, http.StatusSeeOther)
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.status, w.wrote = code, true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// ---------------------------------------------------------------- audit

// auditEntry is filled by handlers with what they changed.
type auditEntry struct {
	action     string
	targetType string
	targetID   *int64
	details    map[string]any
	skip       bool
}

// target records the object an action applies to.
func (rc *reqCtx) target(kind string, id int64) {
	if rc.audit != nil {
		rc.audit.targetType, rc.audit.targetID = kind, &id
	}
}

// note adds a detail to the audit entry.
func (rc *reqCtx) note(key string, v any) {
	if rc.audit == nil {
		return
	}
	if rc.audit.details == nil {
		rc.audit.details = map[string]any{}
	}
	rc.audit.details[key] = v
}

// sensitive form fields never stored in the audit log.
var sensitive = map[string]bool{"csrf": true, "password": true, "password2": true, "participation_password": true,
	"credentials": true, "totp_code": true}

func (s *Server) writeAudit(r *http.Request, rc *reqCtx) {
	e := rc.audit
	details := map[string]any{}
	if r.MultipartForm != nil {
		for k, v := range r.MultipartForm.Value {
			if !sensitive[k] {
				details[k] = clipValues(v)
			}
		}
		for k, fhs := range r.MultipartForm.File {
			var names []string
			for _, fh := range fhs {
				names = append(names, fh.Filename+" ("+strconv.FormatInt(fh.Size, 10)+" B)")
			}
			details[k] = names
		}
	} else if r.PostForm != nil {
		for k, v := range r.PostForm {
			if !sensitive[k] {
				details[k] = clipValues(v)
			}
		}
	}
	for k, v := range e.details {
		details[k] = v
	}
	for _, name := range []string{"id", "task", "file", "lang"} {
		if v := r.PathValue(name); v != "" {
			details["path_"+name] = v
		}
	}
	raw, _ := json.Marshal(details)
	id := rc.admin.ID
	err := s.q.InsertAuditLog(context.WithoutCancel(r.Context()), sqlc.InsertAuditLogParams{
		AdminID: &id, Action: e.action, TargetType: e.targetType, TargetID: e.targetID,
		Details: raw, Ip: s.ips.ClientIP(r).String()})
	if err != nil {
		s.log.Error("audit log", "error", err, "action", e.action)
	}
}

func clipValues(v []string) any {
	out := make([]string, len(v))
	for i, x := range v {
		if len(x) > 300 {
			x = x[:300] + "…"
		}
		out[i] = x
	}
	if len(out) == 1 {
		return out[0]
	}
	return out
}

// audit records an action outside the request middleware (logins).
func (s *Server) audit(r *http.Request, adminID *int64, action string, details map[string]any) {
	raw, _ := json.Marshal(details)
	if err := s.q.InsertAuditLog(context.WithoutCancel(r.Context()), sqlc.InsertAuditLogParams{
		AdminID: adminID, Action: action, Details: raw, Ip: s.ips.ClientIP(r).String()}); err != nil {
		s.log.Error("audit log", "error", err, "action", action)
	}
}

// ---------------------------------------------------------------- helpers

func pathID(r *http.Request, name string) (int64, bool) {
	v, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return v, err == nil && v > 0
}

func isNotFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// notify tells the web servers that contest data changed (cache
// invalidation) and, for dataset changes, the dispatcher.
func (s *Server) contestChanged(ctx context.Context, contestID, participationID int64) {
	if err := events.Publish(ctx, s.rdb, s.ns, events.Event{Type: events.TypeContest, ContestID: contestID,
		ParticipationID: participationID}); err != nil {
		s.log.Warn("publish contest change", "error", err)
	}
}

func (s *Server) datasetChanged(ctx context.Context, taskID, datasetID int64) {
	if err := s.queue.Notify(ctx, queue.Event{Kind: queue.EventDatasetChanged, DatasetID: datasetID}); err != nil {
		s.log.Warn("notify dispatcher", "error", err)
	}
	if t, err := s.q.GetTask(ctx, taskID); err == nil && t.ContestID != nil {
		s.contestChanged(ctx, *t.ContestID, 0)
	}
}

// internalError logs err and renders a generic error page.
func (s *Server) internalError(w http.ResponseWriter, r *http.Request, rc *reqCtx, err error) {
	s.log.Error("admin request failed", "error", err, "path", r.URL.Path)
	s.errorPage(w, r, rc, http.StatusInternalServerError, "Internal error: "+err.Error())
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.errorPage(w, r, rc, http.StatusNotFound, "Not found.")
}

// ---------------------------------------------------------------- live events

// adminHub forwards every event to connected administrators (there are
// few of them).
type adminHub struct {
	mu      sync.RWMutex
	clients map[chan []byte]struct{}
}

// publish forwards the events administrators see live (system alerts, new
// questions, balloons); submission events are far too many to fan out here.
func (h *adminHub) publish(e events.Event) {
	if e.Type != events.TypeAlert && e.Type != events.TypeQuestionNew && e.Type != events.TypeBalloon && e.Type != events.TypePrint {
		return
	}
	data, _ := json.Marshal(e)
	frame := webkit.SSEFrame(e.Type, data)
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		select {
		case c <- frame:
		default:
		}
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	ch := make(chan []byte, 64)
	s.hub.mu.Lock()
	s.hub.clients[ch] = struct{}{}
	s.hub.mu.Unlock()
	defer func() {
		s.hub.mu.Lock()
		delete(s.hub.clients, ch)
		s.hub.mu.Unlock()
	}()
	webkit.ServeSSE(w, r, ch, 25*time.Second, 3*time.Second)
}
