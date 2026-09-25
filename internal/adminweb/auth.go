package adminweb

import (
	"net/http"
	"strings"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/auth"
	"github.com/D4ND3R/Contest-Management-System/internal/webkit"
)

// dummyHash is verified for unknown usernames so that the response time
// does not reveal which administrators exist.
var dummyHash, _ = auth.HashPassword("dummy-password-for-timing")

// anonymous returns the pre-login session (CSRF binding of the login form).
func (s *Server) anonymous(w http.ResponseWriter, r *http.Request) *webkit.Session {
	if sess := s.cookie().Read(r); sess != nil {
		return sess
	}
	sess := &webkit.Session{}
	s.cookie().Write(w, sess)
	return sess
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request, status int, msg string) {
	sess := s.anonymous(w, r)
	p := s.newPage(w, r, nil, "Log in", "", safeNext(r.FormValue("next")))
	p.CSRF = s.csrf.Token(sess.ID)
	p.Error = msg
	s.render(w, "login", status, p)
}

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if sess := s.cookie().Read(r); sess != nil && sess.AdminID != 0 {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.loginPage(w, r, http.StatusOK, "")
}

// safeNext only allows local paths as post-login destinations.
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	return next
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	sess := s.cookie().Read(r)
	if sess == nil || s.csrf.Check(r, sess.ID) != nil {
		s.loginPage(w, r, http.StatusForbidden, "Your session expired; try again.")
		return
	}
	ip := s.ips.ClientIP(r)
	limit := s.cfg.LoginRateLimit
	if limit <= 0 {
		limit = 20
	}
	if !s.limiter.Allow(r.Context(), "admin-login:"+ip.String(), limit, time.Minute) {
		s.loginPage(w, r, http.StatusTooManyRequests, "Too many attempts; wait a minute.")
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	a, err := s.q.GetAdminByUsername(r.Context(), username)
	if err != nil {
		_ = auth.VerifyPassword(dummyHash, r.FormValue("password"))
		s.audit(r, nil, "login_failed", map[string]any{"username": username})
		s.loginPage(w, r, http.StatusUnauthorized, "Wrong username or password.")
		return
	}
	if auth.VerifyPassword(a.PasswordHash, r.FormValue("password")) != nil || !a.Enabled {
		s.audit(r, &a.ID, "login_failed", map[string]any{"username": username})
		s.loginPage(w, r, http.StatusUnauthorized, "Wrong username or password.")
		return
	}
	if a.TotpSecret != nil {
		s.startSecondFactor(w, r, a)
		return
	}
	// A fresh session id on login (no fixation).
	s.cookie().Write(w, &webkit.Session{AdminID: a.ID, Nonce: adminNonce(a)})
	s.audit(r, &a.ID, "login", nil)
	http.Redirect(w, r, safeNext(r.FormValue("next")), http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.cookie().Clear(w)
	s.audit(r, &rc.admin.ID, "logout", nil)
	webkit.Redirect(w, r, "/login")
}
