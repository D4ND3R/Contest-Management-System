package adminweb

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/auth"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/hoststat"
	"github.com/D4ND3R/Contest-Management-System/internal/i18n"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
)

// systemStatus is the live view of workers and queues.
type systemStatus struct {
	Queues     *queue.Stats
	Priorities []string
	Workers    []queue.WorkerStatus
	Alive      int
	Busy       int
	Slots      int
	Time       time.Time
	QueueError string
	Lang       string
	// Host is this machine (the main server); workers report theirs.
	Host hoststat.Stats
	// InFlight are the jobs being run; Stuck counts those that look lost.
	InFlight []inFlightJob
	Stuck    int
	Storage  *sqlc.AdminStorageStatsRow
	// CanWrite: the viewer may requeue jobs; CSRF is its token.
	CanWrite bool
	CSRF     string
}

// inFlightJob is a job being run, as the panel shows it.
type inFlightJob struct {
	queue.InFlight
	Priority string
	Worker   string
	Alive    bool
	Stuck    bool
}

// stuckAfter is how long a job may run before the panel flags it (the
// monitor requeues it after its job timeout anyway).
const stuckAfter = 2 * time.Minute

// storageCache keeps the storage figures for a while: counting blobs and
// sizing the database is not for every poll.
type storageCache struct {
	mu   sync.Mutex
	at   time.Time
	stat *sqlc.AdminStorageStatsRow
}

func (s *Server) storage(ctx context.Context) *sqlc.AdminStorageStatsRow {
	c := &s.storageCache
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stat == nil || s.now().Sub(c.at) > 30*time.Second {
		if st, err := s.q.AdminStorageStats(ctx); err == nil {
			c.stat, c.at = &st, s.now()
		}
	}
	return c.stat
}

// T translates for the status partial (polled without a page).
func (st *systemStatus) T(msg string, args ...any) string { return i18n.T(st.Lang, msg, args...) }

func (s *Server) systemStatus(r *http.Request, rc *reqCtx) *systemStatus {
	ctx := r.Context()
	st := &systemStatus{Time: s.now(), Lang: adminLang(r), CanWrite: roleAllows(rc.admin.Role, permAll), CSRF: s.csrf.Token(rc.sess.ID)}
	for _, p := range queue.Priorities() {
		st.Priorities = append(st.Priorities, p.String())
	}
	var err error
	if st.Queues, err = s.queue.Stats(ctx); err != nil {
		st.QueueError = err.Error()
		st.Queues = &queue.Stats{Waiting: map[string]int64{}, Pending: map[string]int64{}}
	}
	if st.Workers, err = s.queue.Workers(ctx, 10*time.Minute); err != nil && st.QueueError == "" {
		st.QueueError = err.Error()
	}
	alive := map[string]bool{}
	for _, w := range st.Workers {
		alive[w.Name] = w.Alive
		if w.Alive {
			st.Alive++
			st.Slots += len(w.Slots)
			for _, sl := range w.Slots {
				if sl.JobID != "" {
					st.Busy++
				}
			}
		}
	}
	if fl, err := s.queue.InFlightJobs(ctx); err == nil {
		for _, j := range fl {
			v := inFlightJob{InFlight: j, Priority: j.Priority.String(), Worker: queue.WorkerOfConsumer(j.Consumer)}
			v.Alive = alive[v.Worker]
			v.Stuck = j.Idle > stuckAfter || !v.Alive
			if v.Stuck {
				st.Stuck++
			}
			st.InFlight = append(st.InFlight, v)
		}
		// Longest running first.
		sort.SliceStable(st.InFlight, func(i, j int) bool { return st.InFlight[i].Idle > st.InFlight[j].Idle })
	}
	st.Host = s.host.Sample(s.dirs...)
	st.Storage = s.storage(ctx)
	return st
}

// handleJobRequeue takes an in-flight job back and queues it again.
func (s *Server) handleJobRequeue(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	p, ok := queue.ParsePriority(r.FormValue("priority"))
	id := r.FormValue("id")
	if !ok || id == "" {
		s.errorPage(w, r, rc, http.StatusBadRequest, "Invalid request.")
		return
	}
	done, err := s.queue.RequeueByID(r.Context(), p, id)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.note("priority", p.String())
	rc.note("job", id)
	if !done {
		s.done(w, r, "/system", "The job had already finished.")
		return
	}
	s.done(w, r, "/system", "Job queued again.")
}

type dashboard struct {
	Contests []contestListItem
	Status   *systemStatus
	Errors   []sqlc.AdminListSystemErrorsRow
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	list, err := s.q.ListContests(r.Context())
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	counts, err := s.q.AdminContestCounts(r.Context())
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	byID := map[int64]sqlc.AdminContestCountsRow{}
	for _, c := range counts {
		byID[c.ID] = c
	}
	d := &dashboard{Status: s.systemStatus(r, rc)}
	now := s.now()
	for _, c := range list {
		// Current and upcoming contests first; old ones are on /contests.
		if c.StopTime.Before(now.Add(-7 * 24 * time.Hour)) {
			continue
		}
		it := contestListItem{Contest: c, Participations: byID[c.ID].Participations, Tasks: byID[c.ID].Tasks, Submissions: byID[c.ID].Submissions}
		switch {
		case now.Before(c.StartTime):
			it.Phase = "upcoming"
		case now.Before(c.StopTime):
			it.Phase = "running"
		default:
			it.Phase = "finished"
		}
		d.Contests = append(d.Contests, it)
	}
	if d.Errors, err = s.q.AdminListSystemErrors(r.Context()); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	s.render(w, "dashboard", http.StatusOK, s.newPage(w, r, rc, "Overview", "home", d))
}

type systemPage struct {
	Status *systemStatus
	Errors []sqlc.AdminListSystemErrorsRow
}

func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	d := &systemPage{Status: s.systemStatus(r, rc)}
	var err error
	if d.Errors, err = s.q.AdminListSystemErrors(r.Context()); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	s.render(w, "system", http.StatusOK, s.newPage(w, r, rc, "Workers and queues", "system", d))
}

// handleSystemStatus is polled by the system page (htmx).
func (s *Server) handleSystemStatus(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	w.Header().Set("Cache-Control", "no-store")
	s.renderPartial(w, "system-status", s.systemStatus(r, rc))
}

func (s *Server) handleLanguages(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	all := s.langs.All()
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	s.render(w, "languages", http.StatusOK, s.newPage(w, r, rc, "Languages", "system", struct{ Languages []*langs.Language }{all}))
}

// ---------------------------------------------------------------- audit

type auditPage struct {
	Rows   []sqlc.ListAuditLogRow
	Admins []sqlc.Admin
	Admin  int64
	// Action is a prefix ("contest." or a full action); From and To are
	// datetime-local values in UTC.
	Action, From, To string
	Actions          []string
	Next             string
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	const perPage = 200
	qv := r.URL.Query()
	d := &auditPage{Action: strings.TrimSpace(qv.Get("action")), From: qv.Get("from"), To: qv.Get("to")}
	p := sqlc.ListAuditLogParams{Limit: perPage + 1}
	if v, err := strconv.ParseInt(qv.Get("admin"), 10, 64); err == nil && v > 0 {
		d.Admin, p.AdminID = v, &v
	}
	if d.Action != "" {
		// LIKE wildcards in the filter are literal.
		esc := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(d.Action)
		p.Action = &esc
	}
	parse := func(v *string) *time.Time {
		for _, layout := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05", "2006-01-02"} {
			if t, err := time.ParseInLocation(layout, *v, time.UTC); err == nil {
				return &t
			}
		}
		*v = ""
		return nil
	}
	p.FromTime, p.ToTime = parse(&d.From), parse(&d.To)
	if v, err := strconv.ParseInt(qv.Get("before"), 10, 64); err == nil && v > 0 {
		p.BeforeID = &v
	}
	rows, err := s.q.ListAuditLog(r.Context(), p)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	if len(rows) > perPage {
		rows = rows[:perPage]
		next := url.Values{"before": {strconv.FormatInt(rows[len(rows)-1].ID, 10)}}
		if d.Admin != 0 {
			next.Set("admin", strconv.FormatInt(d.Admin, 10))
		}
		for k, v := range map[string]string{"action": d.Action, "from": d.From, "to": d.To} {
			if v != "" {
				next.Set(k, v)
			}
		}
		d.Next = next.Encode()
	}
	d.Rows = rows
	if d.Admins, err = s.q.ListAdmins(r.Context()); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	if d.Actions, err = s.q.ListAuditActions(r.Context()); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	s.render(w, "audit", http.StatusOK, s.newPage(w, r, rc, "Audit log", "admins", d))
}

// ---------------------------------------------------------------- admins

var roles = []string{"all", "messaging", "read_only"}

func (s *Server) handleAdmins(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	list, err := s.q.ListAdmins(r.Context())
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	s.render(w, "admins", http.StatusOK, s.newPage(w, r, rc, "Administrators", "admins", struct {
		Admins []sqlc.Admin
		Roles  []string
	}{list, roles}))
}

func (s *Server) handleAdminCreate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	f := newForm(r)
	username := f.required("username", "Username")
	name := f.str("name")
	role := f.oneOf("role", "Role", roles...)
	password := r.FormValue("password")
	if len(password) < 8 {
		f.fail("the password must have at least 8 characters")
	}
	if f.err == nil {
		if _, err := s.q.GetAdminByUsername(r.Context(), username); err == nil {
			f.fail("the username %q is taken", username)
		}
	}
	if f.err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, f.err.Error())
		return
	}
	if name == "" {
		name = username
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	a, err := s.q.CreateAdmin(r.Context(), sqlc.CreateAdminParams{Name: name, Username: username, PasswordHash: hash, Enabled: true, Role: role})
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("admin", a.ID)
	s.done(w, r, "/admins", "Administrator "+username+" created.")
}

func (s *Server) loadAdmin(w http.ResponseWriter, r *http.Request, rc *reqCtx) (sqlc.Admin, bool) {
	id, _ := pathID(r, "id")
	a, err := s.q.GetAdmin(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r, rc)
		} else {
			s.internalError(w, r, rc, err)
		}
		return a, false
	}
	return a, true
}

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	a, ok := s.loadAdmin(w, r, rc)
	if !ok {
		return
	}
	s.render(w, "admin", http.StatusOK, s.newPage(w, r, rc, a.Username, "admins", struct {
		A     sqlc.Admin
		Roles []string
	}{a, roles}).crumb("Administrators", "/admins"))
}

// otherFullAdmins counts enabled "all" administrators other than id, so
// that the last one cannot lock everybody out.
func (s *Server) otherFullAdmins(ctx context.Context, id int64) (int, error) {
	list, err := s.q.ListAdmins(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, a := range list {
		if a.ID != id && a.Enabled && a.Role == "all" {
			n++
		}
	}
	return n, nil
}

func (s *Server) handleAdminUpdate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	a, ok := s.loadAdmin(w, r, rc)
	if !ok {
		return
	}
	f := newForm(r)
	u := sqlc.UpdateAdminParams{ID: a.ID, Name: f.str("name"), Username: f.required("username", "Username"),
		Enabled: f.check("enabled"), Role: f.oneOf("role", "Role", roles...)}
	password := r.FormValue("password")
	if password != "" && len(password) < 8 {
		f.fail("the password must have at least 8 characters")
	}
	if f.err == nil && (!u.Enabled || u.Role != "all") && a.Enabled && a.Role == "all" {
		if n, err := s.otherFullAdmins(r.Context(), a.ID); err != nil || n == 0 {
			f.fail("at least one enabled administrator with the \"all\" role must remain")
		}
	}
	if f.err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, f.err.Error())
		return
	}
	if u.Name == "" {
		u.Name = u.Username
	}
	if _, err := s.q.UpdateAdmin(r.Context(), u); err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Could not save: "+err.Error())
		return
	}
	if password != "" {
		hash, err := auth.HashPassword(password)
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		if err := s.q.SetAdminPassword(r.Context(), sqlc.SetAdminPasswordParams{ID: a.ID, PasswordHash: hash}); err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		rc.note("password_changed", true)
	}
	if r.FormValue("reset_2fa") != "" && a.TotpSecret != nil {
		if err := s.q.SetAdminTOTP(r.Context(), sqlc.SetAdminTOTPParams{ID: a.ID}); err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		rc.note("2fa_reset", true)
	}
	s.admins.drop(a.ID)
	rc.target("admin", a.ID)
	to := "/admins/" + strconv.FormatInt(a.ID, 10)
	if a.ID == rc.admin.ID && (password != "" || u.Role != a.Role) {
		to = "/login" // the session was bound to the old password/role
	}
	s.done(w, r, to, "Administrator saved.")
}

func (s *Server) handleAdminDelete(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	a, ok := s.loadAdmin(w, r, rc)
	if !ok {
		return
	}
	if a.ID == rc.admin.ID {
		s.errorPage(w, r, rc, http.StatusConflict, "You cannot delete yourself.")
		return
	}
	if a.Enabled && a.Role == "all" {
		if n, err := s.otherFullAdmins(r.Context(), a.ID); err != nil || n == 0 {
			s.errorPage(w, r, rc, http.StatusConflict, "At least one enabled administrator with the \"all\" role must remain.")
			return
		}
	}
	if err := s.q.DeleteAdmin(r.Context(), a.ID); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	s.admins.drop(a.ID)
	rc.target("admin", a.ID)
	rc.note("username", a.Username)
	s.done(w, r, "/admins", "Administrator "+a.Username+" deleted.")
}

// langCommands renders a language's commands for the languages page.
func langCommands(cmds [][]string) string {
	var lines []string
	for _, c := range cmds {
		lines = append(lines, strings.Join(c, " "))
	}
	return strings.Join(lines, "\n")
}
