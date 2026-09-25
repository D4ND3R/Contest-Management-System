package contestweb

import (
	"errors"
	"net/http"
	"net/netip"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/auth"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/events"
	"github.com/D4ND3R/Contest-Management-System/internal/webkit"
	"github.com/jackc/pgx/v5"
)

// anonymous returns (creating it if needed) the pre-login session used to
// bind CSRF tokens on public forms.
func (s *Server) anonymous(w http.ResponseWriter, r *http.Request) *webkit.Session {
	c := s.anonCookie()
	if sess := c.Read(r); sess != nil {
		return sess
	}
	sess := &webkit.Session{}
	c.Write(w, sess)
	return sess
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if s.cfg.ContestID != 0 {
		if c, err := s.q.GetContest(r.Context(), s.cfg.ContestID); err == nil {
			http.Redirect(w, r, "/"+c.Name+"/", http.StatusSeeOther)
			return
		}
	}
	list, err := s.q.ListActiveContests(r.Context())
	if err != nil {
		s.log.Error("list contests", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	sess := s.anonymous(w, r)
	lang := s.language(r, nil, nil, "")
	p := &page{Lang: lang, CSRF: s.csrf.Token(sess.ID), loc: time.UTC, UILanguages: uiLanguages(nil), Data: list}
	p.Title = p.T("Contests")
	s.render(w, "index", http.StatusOK, p)
}

// handleLang stores the UI language in a cookie.
func (s *Server) handleLang(w http.ResponseWriter, r *http.Request) {
	lang := r.FormValue("lang")
	http.SetCookie(w, &http.Cookie{Name: "cms_lang", Value: lang, Path: "/", MaxAge: 365 * 24 * 3600,
		HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode})
	back := r.Header.Get("HX-Current-URL")
	if back == "" {
		back = r.Referer()
	}
	if back == "" {
		back = "/"
	}
	webkit.Redirect(w, r, back)
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request, cv *contestView, status int, msg string) {
	sess := s.anonymous(w, r)
	lang := s.language(r, cv, nil, "")
	p := &page{Lang: lang, CSRF: s.csrf.Token(sess.ID), Base: "/" + cv.Name + "/", Contest: cv, loc: cv.Loc,
		UILanguages: uiLanguages(cv.AllowedLocalizations)}
	p.Title = p.T("Log in")
	p.RegisterOpen = registrationOpen(cv, s.now())
	if msg != "" {
		p.Error = p.T(msg)
	}
	s.render(w, "login", status, p)
}

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	cv := contestOf(r)
	if sess := s.cookie(cv.ID).Read(r); sess != nil && sess.ParticipationID != 0 {
		http.Redirect(w, r, "/"+cv.Name+"/", http.StatusSeeOther)
		return
	}
	s.loginPage(w, r, cv, http.StatusOK, "")
}

// Login messages (translated in the page).
const (
	msgBadCredentials  = "Invalid username or password."
	msgTooManyLogins   = "Too many login attempts, please wait a minute."
	msgPasswordLogin   = "Password login is disabled for this contest."
	msgBadIP           = "You cannot log in from this address."
	msgPendingApproval = "Your registration is waiting for the organizers' approval."
)

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	cv := contestOf(r)
	anon := s.anonCookie().Read(r)
	if anon == nil || s.csrf.Check(r, anon.ID) != nil {
		s.loginPage(w, r, cv, http.StatusForbidden, "Your session expired; reload the page and try again.")
		return
	}
	ip := s.ips.ClientIP(r)
	if !s.limiter.Allow(r.Context(), "login:"+ip.String(), s.cfg.LoginRateLimit, time.Minute) {
		s.loginPage(w, r, cv, http.StatusTooManyRequests, msgTooManyLogins)
		return
	}
	if !cv.AllowPasswordAuthentication {
		s.loginPage(w, r, cv, http.StatusForbidden, msgPasswordLogin)
		return
	}
	cand, err := s.q.GetLoginCandidate(r.Context(), sqlc.GetLoginCandidateParams{ContestID: cv.ID, Username: r.FormValue("username")})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			s.log.Error("login lookup", "error", err)
		}
		// Burn comparable time to not reveal which usernames exist.
		_ = auth.VerifyPassword(dummyHash, r.FormValue("password"))
		s.loginPage(w, r, cv, http.StatusUnauthorized, msgBadCredentials)
		return
	}
	hash := cand.UserPasswordHash
	if cand.ParticipationPasswordHash != nil {
		hash = *cand.ParticipationPasswordHash
	}
	if auth.VerifyPassword(hash, r.FormValue("password")) != nil {
		s.loginPage(w, r, cv, http.StatusUnauthorized, msgBadCredentials)
		return
	}
	if cand.Disabled {
		s.loginPage(w, r, cv, http.StatusForbidden, "Your account is disabled.")
		return
	}
	if !cand.Approved {
		s.loginPage(w, r, cv, http.StatusForbidden, msgPendingApproval)
		return
	}
	if cv.BlockHiddenParticipations && cand.Hidden {
		s.loginPage(w, r, cv, http.StatusForbidden, msgBadCredentials)
		return
	}
	if cv.IpRestriction && len(cand.Ip) > 0 && !ipAllowed(cand.Ip, ip) {
		s.loginPage(w, r, cv, http.StatusForbidden, msgBadIP)
		return
	}
	s.startSession(w, r, cv, cand.ParticipationID, cand.UserID, cand.LoginNonce)
	http.Redirect(w, r, "/"+cv.Name+"/", http.StatusSeeOther)
}

// dummyHash is verified when the username does not exist.
var dummyHash, _ = auth.HashPassword("dummy-password-for-timing")

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, cv *contestView, pid, uid, nonce int64) *webkit.Session {
	if cv.SingleLogin {
		// A new login invalidates every other session of the participation.
		if n, err := s.q.BumpLoginNonce(r.Context(), pid); err == nil {
			nonce = n
		}
		s.cache.invalidateParticipation(pid)
		_ = events.Publish(r.Context(), s.rdb, s.ns, events.Event{Type: events.TypeContest, ParticipationID: pid})
	}
	sess := &webkit.Session{ParticipationID: pid, UserID: uid, ContestID: cv.ID, Nonce: nonce}
	s.sessionCookie(cv).Write(w, sess)
	return sess
}

// autologin logs in the participation whose allowed network contains the
// client address, when exactly one does.
func (s *Server) autologin(w http.ResponseWriter, r *http.Request, cv *contestView, ip netip.Addr) *webkit.Session {
	rows, err := s.q.ListParticipationsWithIP(r.Context(), cv.ID)
	if err != nil {
		return nil
	}
	var match *sqlc.ListParticipationsWithIPRow
	for i := range rows {
		if ipAllowed(rows[i].Ip, ip) {
			if match != nil {
				return nil // ambiguous
			}
			match = &rows[i]
		}
	}
	if match == nil {
		return nil
	}
	p, err := s.cache.participation(r.Context(), match.ID)
	if err != nil || (cv.BlockHiddenParticipations && p.Hidden) {
		return nil
	}
	return s.startSession(w, r, cv, p.ID, p.UserID, p.LoginNonce)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	cv := contestOf(r)
	if sess := s.cookie(cv.ID).Read(r); sess != nil && s.csrf.Check(r, sess.ID) != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.cookie(cv.ID).Clear(w)
	webkit.Redirect(w, r, "/"+cv.Name+"/login")
}

// handleImpersonate opens a read-only session for an administrator who
// clicked "view as contestant" in the admin web server (a signed link valid
// for a minute; the admin server records it in the audit log).
func (s *Server) handleImpersonate(w http.ResponseWriter, r *http.Request) {
	cv := contestOf(r)
	imp, ok := webkit.VerifyImpersonation(s.secret, r.URL.Query().Get("t"))
	if !ok {
		s.errorPage(w, r, cv, http.StatusForbidden, "Forbidden", "The link expired; open it again from the admin panel.")
		return
	}
	part, err := s.cache.participation(r.Context(), imp.ParticipationID)
	if err != nil || part.ContestID != cv.ID {
		s.errorPage(w, r, cv, http.StatusNotFound, "Not found", "This contest does not exist.")
		return
	}
	s.sessionCookie(cv).Write(w, &webkit.Session{ParticipationID: part.ID, UserID: part.UserID, ContestID: cv.ID,
		Nonce: part.LoginNonce, ReadOnly: true, AdminID: imp.AdminID})
	http.Redirect(w, r, "/"+cv.Name+"/", http.StatusSeeOther)
}
