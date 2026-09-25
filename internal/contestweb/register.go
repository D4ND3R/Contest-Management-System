package contestweb

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/D4ND3R/Contest-Management-System/internal/auth"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Self-registration (C7): with registration "approval" a new account waits
// for an administrator; with "code" the invitation code admits it at once.

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,32}$`)

// registerForm is the data of the registration page.
type registerForm struct {
	Username, FirstName, LastName, Email, Institution string
	NeedsCode                                         bool
	MinLength                                         int32
	Done                                              bool
}

// registrationOpen reports whether contestants may register now.
func registrationOpen(cv *contestView, now time.Time) bool {
	if cv.Registration == "admin" || cv.Status != "published" {
		return false
	}
	return now.Before(cv.StopTime) || cv.PracticeEnabled
}

func (s *Server) registerPage(w http.ResponseWriter, r *http.Request, cv *contestView, status int, d *registerForm, msg string) {
	sess := s.anonymous(w, r)
	lang := s.language(r, cv, nil, "")
	p := &page{Lang: lang, CSRF: s.csrf.Token(sess.ID), Base: "/" + cv.Name + "/", Contest: cv, loc: cv.Loc,
		UILanguages: uiLanguages(cv.AllowedLocalizations), Data: d}
	p.Title = p.T("Register")
	if msg != "" {
		if strings.Contains(msg, "%d") {
			p.Error = p.T(msg, d.MinLength)
		} else {
			p.Error = p.T(msg)
		}
	}
	s.render(w, "register", status, p)
}

func (s *Server) handleRegisterForm(w http.ResponseWriter, r *http.Request) {
	cv := contestOf(r)
	if !registrationOpen(cv, s.now()) {
		s.errorPage(w, r, cv, http.StatusNotFound, "Not found", "Registration is closed for this contest.")
		return
	}
	s.registerPage(w, r, cv, http.StatusOK, &registerForm{NeedsCode: cv.Registration == "code", MinLength: cv.PasswordMinLength}, "")
}

// passwordProblem checks the contest's password policy: a minimum length,
// letters and digits, not the username.
func passwordProblem(pw, confirm, username string, minLen int32) string {
	switch {
	case utf8.RuneCountInString(pw) < int(minLen):
		return "The password must have at least %d characters."
	case pw != confirm:
		return "The passwords do not match."
	case strings.EqualFold(pw, username):
		return "The password cannot be the username."
	}
	var letter, digit bool
	for _, c := range pw {
		letter = letter || unicode.IsLetter(c)
		digit = digit || unicode.IsDigit(c)
	}
	if !letter || !digit {
		return "The password must have letters and digits."
	}
	return ""
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	cv := contestOf(r)
	if !registrationOpen(cv, s.now()) {
		s.errorPage(w, r, cv, http.StatusNotFound, "Not found", "Registration is closed for this contest.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	d := &registerForm{NeedsCode: cv.Registration == "code", MinLength: cv.PasswordMinLength,
		Username: strings.TrimSpace(r.FormValue("username")), FirstName: strings.TrimSpace(r.FormValue("first_name")),
		LastName: strings.TrimSpace(r.FormValue("last_name")), Email: strings.TrimSpace(r.FormValue("email")),
		Institution: strings.TrimSpace(r.FormValue("institution"))}
	anon := s.anonCookie().Read(r)
	if anon == nil || s.csrf.Check(r, anon.ID) != nil {
		s.registerPage(w, r, cv, http.StatusForbidden, d, "Your session expired; reload the page and try again.")
		return
	}
	ip := s.ips.ClientIP(r)
	if !s.limiter.Allow(r.Context(), "register:"+ip.String(), max(s.cfg.LoginRateLimit/2, 1), time.Minute) {
		s.registerPage(w, r, cv, http.StatusTooManyRequests, d, "Too many requests, please slow down.")
		return
	}
	fail := func(msg string) { s.registerPage(w, r, cv, http.StatusUnprocessableEntity, d, msg) }
	switch {
	case !usernameRe.MatchString(d.Username):
		fail("The username must have 3 to 32 letters, digits, '.', '_' or '-'.")
		return
	case d.FirstName == "" || len(d.FirstName) > 100 || len(d.LastName) > 100 || len(d.Institution) > 200:
		fail("Please write your name (at most 100 characters).")
		return
	case d.Email != "" && (len(d.Email) > 200 || !strings.Contains(d.Email, "@")):
		fail("The email address is not valid.")
		return
	}
	if msg := passwordProblem(r.FormValue("password"), r.FormValue("password2"), d.Username, cv.PasswordMinLength); msg != "" {
		fail(msg)
		return
	}
	if d.NeedsCode && (cv.InvitationCode == "" ||
		subtle.ConstantTimeCompare([]byte(strings.TrimSpace(r.FormValue("code"))), []byte(cv.InvitationCode)) != 1) {
		fail("The invitation code is not valid.")
		return
	}
	hash, err := auth.HashPassword(r.FormValue("password"))
	if err != nil {
		s.fail(w, err)
		return
	}
	approved := cv.Registration == "code"
	var part sqlc.Participation
	var user sqlc.User
	err = db.InTx(r.Context(), s.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		var err error
		user, err = q.CreateUser(r.Context(), sqlc.CreateUserParams{Username: d.Username, FirstName: d.FirstName, LastName: d.LastName,
			Email: d.Email, Institution: d.Institution, PasswordHash: hash, PreferredLanguages: []string{}})
		if err != nil {
			return err
		}
		if part, err = q.CreateParticipation(r.Context(), sqlc.CreateParticipationParams{ContestID: cv.ID, UserID: user.ID,
			Ip: []netip.Prefix{}}); err != nil {
			return err
		}
		if !approved {
			part, err = q.SetParticipationApproved(r.Context(), sqlc.SetParticipationApprovedParams{ID: part.ID, Approved: false})
		}
		return err
	})
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		fail("This username is taken. If it is yours, ask the organizers to add you to the contest.")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.log.Info("contestant registered", "contest", cv.Name, "user", d.Username, "approved", approved, "ip", ip.String())
	if approved {
		s.startSession(w, r, cv, part.ID, user.ID, part.LoginNonce)
		http.Redirect(w, r, "/"+cv.Name+"/", http.StatusSeeOther)
		return
	}
	d.Done = true
	s.registerPage(w, r, cv, http.StatusOK, d, "")
}
