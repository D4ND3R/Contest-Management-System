package adminweb

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/auth"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/webkit"
)

// Optional TOTP second factor for administrators. Pending states (a login
// waiting for its code, a secret waiting for confirmation) travel in
// signed, short-lived form fields, so no server-side state is needed.

type pending2FA struct {
	AdminID int64  `json:"a"`
	Next    string `json:"n,omitempty"`
	Secret  string `json:"s,omitempty"`
	Expires int64  `json:"e"`
}

func (s *Server) twoFASigner() *webkit.Signer { return webkit.NewSigner(s.secret, "aws-2fa") }

func (s *Server) signPending(p pending2FA, ttl time.Duration) string {
	p.Expires = time.Now().Add(ttl).Unix()
	b, _ := json.Marshal(p)
	return s.twoFASigner().Sign(b)
}

func (s *Server) readPending(token string) (*pending2FA, bool) {
	b, ok := s.twoFASigner().Verify(token)
	if !ok {
		return nil, false
	}
	var p pending2FA
	if json.Unmarshal(b, &p) != nil || time.Now().Unix() > p.Expires {
		return nil, false
	}
	return &p, true
}

type secondStep struct {
	Token string
}

// startSecondFactor renders the code form after a correct password.
func (s *Server) startSecondFactor(w http.ResponseWriter, r *http.Request, a sqlc.Admin) {
	sess := s.anonymous(w, r)
	p := s.newPage(w, r, nil, "Two-factor authentication", "", &secondStep{Token: s.signPending(pending2FA{AdminID: a.ID, Next: safeNext(r.FormValue("next"))}, 5*time.Minute)})
	p.CSRF = s.csrf.Token(sess.ID)
	s.render(w, "login_2fa", http.StatusOK, p)
}

func (s *Server) handleLogin2FA(w http.ResponseWriter, r *http.Request) {
	sess := s.cookie().Read(r)
	if sess == nil || s.csrf.Check(r, sess.ID) != nil {
		s.loginPage(w, r, http.StatusForbidden, "Your session expired; try again.")
		return
	}
	ip := s.ips.ClientIP(r)
	if !s.limiter.Allow(r.Context(), "admin-login:"+ip.String(), max(s.cfg.LoginRateLimit, 1), time.Minute) {
		s.loginPage(w, r, http.StatusTooManyRequests, "Too many attempts; wait a minute.")
		return
	}
	pend, ok := s.readPending(r.FormValue("token"))
	if !ok {
		s.loginPage(w, r, http.StatusUnauthorized, "The login expired; enter your password again.")
		return
	}
	a, err := s.q.GetAdmin(r.Context(), pend.AdminID)
	if err != nil || !a.Enabled || a.TotpSecret == nil {
		s.loginPage(w, r, http.StatusUnauthorized, "Wrong username or password.")
		return
	}
	if !auth.VerifyTOTP(*a.TotpSecret, r.FormValue("totp_code"), time.Now()) {
		s.audit(r, &a.ID, "login_failed", map[string]any{"username": a.Username, "reason": "2fa"})
		p := s.newPage(w, r, nil, "Two-factor authentication", "", &secondStep{Token: r.FormValue("token")})
		p.CSRF = s.csrf.Token(sess.ID)
		p.Error = "Wrong code."
		s.render(w, "login_2fa", http.StatusUnauthorized, p)
		return
	}
	s.cookie().Write(w, &webkit.Session{AdminID: a.ID, Nonce: adminNonce(a)})
	s.audit(r, &a.ID, "login", map[string]any{"2fa": true})
	http.Redirect(w, r, safeNext(pend.Next), http.StatusSeeOther)
}

// accountPage is the current administrator's own settings.
type accountPage struct {
	TwoFA   bool
	Pending string        // signed pending secret while enrolling
	Secret  string        // shown for manual entry
	QR      template.HTML // otpauth URI as an inline SVG
}

func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.render(w, "account", http.StatusOK, s.newPage(w, r, rc, "My account", "", &accountPage{TwoFA: rc.admin.TotpSecret != nil}))
}

func (s *Server) handle2FAStart(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	secret := auth.NewTOTPSecret()
	qr, err := webkit.QRSVG(auth.TOTPURI("CMS", rc.admin.Username, secret), 200)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	d := &accountPage{Pending: s.signPending(pending2FA{AdminID: rc.admin.ID, Secret: secret}, 15*time.Minute),
		Secret: groupSecret(secret), QR: qr}
	s.render(w, "account", http.StatusOK, s.newPage(w, r, rc, "My account", "", d))
}

func groupSecret(s string) string {
	var parts []string
	for i := 0; i < len(s); i += 4 {
		parts = append(parts, s[i:min(i+4, len(s))])
	}
	return strings.Join(parts, " ")
}

func (s *Server) handle2FAEnable(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	pend, ok := s.readPending(r.FormValue("pending"))
	if !ok || pend.AdminID != rc.admin.ID || pend.Secret == "" {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "The enrolment expired; start again.")
		return
	}
	if !auth.VerifyTOTP(pend.Secret, r.FormValue("totp_code"), time.Now()) {
		qr, _ := webkit.QRSVG(auth.TOTPURI("CMS", rc.admin.Username, pend.Secret), 200)
		p := s.newPage(w, r, rc, "My account", "", &accountPage{Pending: r.FormValue("pending"), Secret: groupSecret(pend.Secret), QR: qr})
		s.formError(w, r, rc, "account", p, "Wrong code: check the clock of your device and try again.")
		return
	}
	if err := s.q.SetAdminTOTP(r.Context(), sqlc.SetAdminTOTPParams{ID: rc.admin.ID, TotpSecret: &pend.Secret}); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	s.admins.drop(rc.admin.ID)
	rc.target("admin", rc.admin.ID)
	s.done(w, r, "/account", "Two-factor authentication enabled.")
}

func (s *Server) handle2FADisable(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	if rc.admin.TotpSecret == nil || !auth.VerifyTOTP(*rc.admin.TotpSecret, r.FormValue("totp_code"), time.Now()) {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Wrong code.")
		return
	}
	if err := s.q.SetAdminTOTP(r.Context(), sqlc.SetAdminTOTPParams{ID: rc.admin.ID}); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	s.admins.drop(rc.admin.ID)
	rc.target("admin", rc.admin.ID)
	s.done(w, r, "/account", "Two-factor authentication disabled.")
}

// handleAccountPassword changes the administrator's own password (which
// ends their other sessions).
func (s *Server) handleAccountPassword(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	if auth.VerifyPassword(rc.admin.PasswordHash, r.FormValue("current_password")) != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "The current password is wrong.")
		return
	}
	pw := r.FormValue("password")
	if len(pw) < 8 {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "The password must have at least 8 characters.")
		return
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	if err := s.q.SetAdminPassword(r.Context(), sqlc.SetAdminPasswordParams{ID: rc.admin.ID, PasswordHash: hash}); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	s.admins.drop(rc.admin.ID)
	rc.target("admin", rc.admin.ID)
	a := rc.admin
	a.PasswordHash = hash
	s.cookie().Write(w, &webkit.Session{AdminID: a.ID, Nonce: adminNonce(a)})
	s.done(w, r, "/account", "Password changed; your other sessions were closed.")
}
