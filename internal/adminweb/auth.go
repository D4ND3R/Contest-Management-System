package adminweb

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/auth"
	"github.com/D4ND3R/Contest-Management-System/internal/i18n"
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

// loginFailuresPerUser bounds wrong passwords (and second-factor codes)
// per administrator and minute, from any address.
const loginFailuresPerUser = 10

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
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
	username := strings.TrimSpace(r.FormValue("username"))
	// Only failures count, per address and per username.
	ipKey, userKey := "admin-login-fail:"+ip.String(), "admin-login-fail-user:"+strings.ToLower(username)
	if s.limiter.Over(r.Context(), ipKey, limit, time.Minute) || s.limiter.Over(r.Context(), userKey, loginFailuresPerUser, time.Minute) {
		s.loginPage(w, r, http.StatusTooManyRequests, "Too many attempts; wait a minute.")
		return
	}
	failed := func() {
		s.limiter.Hit(r.Context(), ipKey, time.Minute)
		s.limiter.Hit(r.Context(), userKey, time.Minute)
	}
	a, err := s.q.GetAdminByUsername(r.Context(), username)
	if err != nil {
		_ = auth.VerifyPassword(dummyHash, r.FormValue("password"))
		failed()
		s.audit(r, nil, "login_failed", map[string]any{"username": username})
		s.loginPage(w, r, http.StatusUnauthorized, "Wrong username or password.")
		return
	}
	if auth.VerifyPassword(a.PasswordHash, r.FormValue("password")) != nil || !a.Enabled {
		failed()
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

// handleLang stores the interface language (the cookie is shared with the
// contest web server on the same host).
func (s *Server) handleLang(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if lang := r.FormValue("lang"); slices.Contains(i18n.Languages(), lang) {
		http.SetCookie(w, &http.Cookie{Name: "cms_lang", Value: lang, Path: "/", MaxAge: 365 * 24 * 3600,
			HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode})
	}
	back := r.Header.Get("HX-Current-URL")
	if back == "" {
		back = r.Referer()
	}
	if u, err := url.Parse(back); err == nil && u.Path != "" {
		back = u.RequestURI()
	} else {
		back = "/"
	}
	webkit.Redirect(w, r, safeNext(back))
}
