package adminweb

import (
	"context"
	"encoding/csv"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/auth"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/pdf"
	"github.com/D4ND3R/Contest-Management-System/internal/webkit"
	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------- photos

func (s *Server) handleUserPhoto(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	u, ok := s.loadUser(w, r, rc)
	if !ok {
		return
	}
	if u.PhotoDigest == nil {
		s.notFound(w, r, rc)
		return
	}
	s.serveImage(w, r, rc, *u.PhotoDigest)
}

// serveImage serves an uploaded image (only image types; anything else is
// sent as a download).
func (s *Server) serveImage(w http.ResponseWriter, r *http.Request, rc *reqCtx, digest string) {
	data, err := readBlobLimited(r.Context(), s, digest, 16<<20)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	ct := http.DetectContentType(data)
	if !strings.HasPrefix(ct, "image/") || ct == "image/svg+xml" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Write(data)
}

// ---------------------------------------------------------------- account state

// participationsOf returns the participations of a user.
func (s *Server) participationsOf(ctx context.Context, userID int64) []sqlc.Participation {
	ps, err := s.q.ListParticipationsByUser(ctx, userID)
	if err != nil {
		s.log.Warn("list participations", "error", err)
	}
	return ps
}

func (s *Server) handleUserDisable(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.setUserDisabled(w, r, rc, true)
}

func (s *Server) handleUserEnable(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.setUserDisabled(w, r, rc, false)
}

func (s *Server) setUserDisabled(w http.ResponseWriter, r *http.Request, rc *reqCtx, disabled bool) {
	u, ok := s.loadUser(w, r, rc)
	if !ok {
		return
	}
	if err := s.q.SetUserDisabled(r.Context(), sqlc.SetUserDisabledParams{ID: u.ID, Disabled: disabled}); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("user", u.ID)
	s.invalidateUser(r.Context(), u.ID)
	msg := "User enabled."
	if disabled {
		msg = "User disabled: they cannot log in and their sessions are rejected."
	}
	s.done(w, r, "/users/"+strconv.FormatInt(u.ID, 10), msg)
}

// logoutParticipation ends every session of a participation.
func (s *Server) logoutParticipation(ctx context.Context, p sqlc.Participation) error {
	if _, err := s.q.BumpLoginNonce(ctx, p.ID); err != nil {
		return err
	}
	s.sessions.Clear(ctx, p.ID)
	s.contestChanged(ctx, p.ContestID, p.ID)
	return nil
}

func (s *Server) handleUserLogout(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	u, ok := s.loadUser(w, r, rc)
	if !ok {
		return
	}
	for _, p := range s.participationsOf(r.Context(), u.ID) {
		if err := s.logoutParticipation(r.Context(), p); err != nil {
			s.internalError(w, r, rc, err)
			return
		}
	}
	rc.target("user", u.ID)
	s.done(w, r, "/users/"+strconv.FormatInt(u.ID, 10), "Every session of the user was closed.")
}

// sessionView is an active session shown to administrators.
type sessionView struct {
	Contest string
	webkit.ActiveSession
	Valid bool
}

func (s *Server) sessionsOf(ctx context.Context, ps []sqlc.Participation) []sessionView {
	names := map[int64]string{}
	if cs, err := s.q.ListContests(ctx); err == nil {
		for _, c := range cs {
			names[c.ID] = c.Name
		}
	}
	var out []sessionView
	for _, p := range ps {
		list, err := s.sessions.List(ctx, p.ID)
		if err != nil {
			continue
		}
		for _, a := range list {
			out = append(out, sessionView{Contest: names[p.ContestID], ActiveSession: a, Valid: a.Nonce == p.LoginNonce})
		}
	}
	return out
}

// ---------------------------------------------------------------- passwords

// credentialsPage shows freshly generated passwords (once) with the
// printable sheet form.
type credentialsPage struct {
	Credentials []credential
	CSV         string
	Title, URL  string
	Back        string
}

func credentialsCSV(cs []credential) string {
	var b strings.Builder
	cw := csv.NewWriter(&b)
	cw.Write([]string{"username", "password", "name", "site"})
	for _, c := range cs {
		cw.Write([]string{c.Username, c.Password, c.Name, c.Site})
	}
	cw.Flush()
	return b.String()
}

func (s *Server) handleUserResetPassword(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	u, ok := s.loadUser(w, r, rc)
	if !ok {
		return
	}
	pw := generatePassword()
	hash, err := auth.HashPassword(pw)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	if err := s.q.SetUserPassword(r.Context(), sqlc.SetUserPasswordParams{ID: u.ID, PasswordHash: hash}); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("user", u.ID)
	cs := []credential{{Username: u.Username, Password: pw, Name: strings.TrimSpace(u.FirstName + " " + u.LastName)}}
	s.render(w, "credentials", http.StatusOK, s.newPage(w, r, rc, "New password", "users",
		&credentialsPage{Credentials: cs, CSV: credentialsCSV(cs), Back: "/users/" + strconv.FormatInt(u.ID, 10)}).crumb("Users", "/users"))
}

// handleContestResetPasswords generates new passwords for every
// participant of a contest (typed confirmation) and shows the credentials.
func (s *Server) handleContestResetPasswords(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	if r.FormValue("confirm") != "RESET" {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Type RESET to confirm: every participant gets a new password.")
		return
	}
	parts, err := s.q.ListParticipationsByContest(r.Context(), c.ID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	users := make([]csvUser, 0, len(parts))
	for _, p := range parts {
		if p.Participation.Hidden && r.FormValue("include_hidden") == "" {
			continue
		}
		users = append(users, csvUser{username: p.Username, password: generatePassword(), first: p.FirstName, last: p.LastName, site: derefStr(p.SiteName)})
	}
	if err := hashAll(users); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	var cs []credential
	err = db.InTx(r.Context(), s.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		for _, u := range users {
			usr, err := q.GetUserByUsername(r.Context(), u.username)
			if err != nil {
				return err
			}
			if err := q.SetUserPassword(r.Context(), sqlc.SetUserPasswordParams{ID: usr.ID, PasswordHash: u.hash}); err != nil {
				return err
			}
			cs = append(cs, credential{Username: u.username, Password: u.password, Name: strings.TrimSpace(u.first + " " + u.last), Site: u.site})
		}
		return nil
	})
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("contest", c.ID)
	rc.note("users", len(cs))
	s.render(w, "credentials", http.StatusOK, s.newPage(w, r, rc, "New passwords", "contests",
		&credentialsPage{Credentials: cs, CSV: credentialsCSV(cs), Title: c.Description, URL: s.contestURL(r, c.Name),
			Back: "/contests/" + strconv.FormatInt(c.ID, 10) + "/participations"}))
}

// handleCredentialsPDF renders credential cards (1, 2, 4, 6 or 8 per A4
// page) from the credentials posted back by the page that showed them:
// passwords are stored hashed, so they can only be printed right after
// they were generated.
func (s *Server) handleCredentialsPDF(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	rd := csv.NewReader(strings.NewReader(r.FormValue("credentials")))
	rd.FieldsPerRecord = -1
	recs, err := rd.ReadAll()
	if err != nil || len(recs) < 2 {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "No credentials to print.")
		return
	}
	var cs []credential
	for _, rec := range recs[1:] {
		if len(rec) < 2 || rec[0] == "" {
			continue
		}
		c := credential{Username: rec[0], Password: rec[1]}
		if len(rec) > 2 {
			c.Name = rec[2]
		}
		if len(rec) > 3 {
			c.Site = rec[3]
		}
		cs = append(cs, c)
	}
	perPage, _ := strconv.Atoi(r.FormValue("per_page"))
	doc := credentialSheets(cs, perPage, r.FormValue("title"), r.FormValue("url"))
	rc.note("cards", len(cs))
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="credentials.pdf"`)
	w.Header().Set("Cache-Control", "no-store")
	doc.Write(w)
}

// credentialSheets lays out one card per credential.
func credentialSheets(cs []credential, perPage int, title, link string) *pdf.Doc {
	cols, rows := 1, 1
	switch perPage {
	case 2:
		cols, rows = 1, 2
	case 4:
		cols, rows = 2, 2
	case 6:
		cols, rows = 2, 3
	case 8:
		cols, rows = 2, 4
	default:
		perPage = 1
	}
	doc := pdf.New()
	doc.Title = "Credentials"
	margin := 28.0
	cw := (pdf.A4Width - 2*margin) / float64(cols)
	ch := (pdf.A4Height - 2*margin) / float64(rows)
	var page *pdf.Page
	for i, c := range cs {
		k := i % (cols * rows)
		if k == 0 {
			page = doc.AddPage()
			for x := 1; x < cols; x++ {
				page.DashedLine(margin+float64(x)*cw, margin, margin+float64(x)*cw, pdf.A4Height-margin, 0.5)
			}
			for y := 1; y < rows; y++ {
				page.DashedLine(margin, margin+float64(y)*ch, pdf.A4Width-margin, margin+float64(y)*ch, 0.5)
			}
		}
		x0 := margin + float64(k%cols)*cw
		top := pdf.A4Height - margin - float64(k/cols)*ch
		cx := x0 + cw/2
		// Every line of a card fits its cell (about 220 points at scale 1).
		scale := min(cw/270, (ch-10)/220, 1.6)
		y := top - 30*scale
		if title != "" {
			page.TextCentered(cx, y, 13*scale, true, pdf.Fit(title, cw-20, 13*scale, true))
			y -= 26 * scale
		}
		if c.Name != "" {
			page.TextCentered(cx, y, 12*scale, false, pdf.Fit(c.Name, cw-20, 12*scale, false))
			y -= 30 * scale
		}
		page.TextCentered(cx, y, 9*scale, false, "Usuario / Username")
		y -= 18 * scale
		page.TextCentered(cx, y, 16*scale, true, c.Username)
		y -= 28 * scale
		page.TextCentered(cx, y, 9*scale, false, "Contraseña / Password")
		y -= 18 * scale
		page.TextCentered(cx, y, 16*scale, true, c.Password)
		if c.Site != "" {
			y -= 24 * scale
			page.TextCentered(cx, y, 10*scale, false, pdf.Fit("Sede / Site: "+c.Site, cw-20, 10*scale, false))
		}
		if link != "" {
			y -= 24 * scale
			page.TextCentered(cx, y, 9*scale, false, pdf.Fit(link, cw-20, 9*scale, false))
		}
	}
	if len(cs) == 0 {
		doc.AddPage()
	}
	_ = perPage
	return doc
}

// ---------------------------------------------------------------- view as contestant

// contestURL is the public URL of the contest web server for a contest.
func (s *Server) contestURL(r *http.Request, contest string) string {
	base := strings.TrimSuffix(s.cfg.ContestURL, "/")
	if base == "" {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		port := "8888"
		if _, p, err := net.SplitHostPort(s.contestListen); err == nil && p != "" {
			port = p
		}
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		base = scheme + "://" + net.JoinHostPort(host, port)
	}
	return base + "/" + url.PathEscape(contest) + "/"
}

func (s *Server) handleViewAs(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	p, ok := s.loadParticipation(w, r, rc)
	if !ok {
		return
	}
	token := webkit.SignImpersonation(s.secret, webkit.Impersonation{ParticipationID: p.ID, AdminID: rc.admin.ID, Admin: rc.admin.Username}, time.Minute)
	rc.target("participation", p.ID)
	rc.note("username", p.Username)
	http.Redirect(w, r, s.contestURL(r, p.ContestName)+"impersonate?t="+url.QueryEscape(token), http.StatusSeeOther)
}

// ---------------------------------------------------------------- export

// handleUsersExport writes users as CSV in the import format (without
// passwords); with ?contest=ID only its participants, with their
// participation settings.
func (s *Server) handleUsersExport(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	contestID, _ := strconv.ParseInt(r.URL.Query().Get("contest"), 10, 64)
	head := []string{"username", "first_name", "last_name", "email", "institution", "country", "region", "timezone"}
	var rows [][]string
	name := "users.csv"
	if contestID != 0 {
		c, err := s.q.GetContest(r.Context(), contestID)
		if err != nil {
			s.notFound(w, r, rc)
			return
		}
		name = "users-" + c.Name + ".csv"
		head = append(head, "team", "site", "hidden", "unrestricted", "ip", "delay_time", "extra_time")
		parts, err := s.q.AdminExportParticipants(r.Context(), contestID)
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		for _, p := range parts {
			rows = append(rows, []string{p.Username, p.FirstName, p.LastName, p.Email, p.Institution, p.Country, p.Region,
				derefStr(p.Timezone), derefStr(p.TeamCode), derefStr(p.SiteName), boolStr(p.Hidden), boolStr(p.Unrestricted),
				strings.ReplaceAll(formatPrefixes(p.Ip), ", ", ";"), strconv.FormatInt(p.DelayTimeS, 10), strconv.FormatInt(p.ExtraTimeS, 10)})
		}
	} else {
		users, err := s.q.ListUsers(r.Context())
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		for _, u := range users {
			rows = append(rows, []string{u.Username, u.FirstName, u.LastName, u.Email, u.Institution, u.Country, u.Region, derefStr(u.Timezone)})
		}
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	cw := csv.NewWriter(w)
	cw.Write(head)
	cw.WriteAll(rows)
}

func boolStr(b bool) string {
	if b {
		return "yes"
	}
	return ""
}
