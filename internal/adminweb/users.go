package adminweb

import (
	"context"
	"crypto/rand"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/D4ND3R/Contest-Management-System/internal/auth"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/i18n"
	"github.com/jackc/pgx/v5"
)

const usersPerPage = 100

type usersPage struct {
	Users    []sqlc.AdminListUsersRow
	Search   string
	Page     int
	HasMore  bool
	Total    int64
	Contests []sqlc.Contest
	Teams    []sqlc.Team
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	d := &usersPage{Search: strings.TrimSpace(r.URL.Query().Get("q"))}
	d.Page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	if d.Page < 0 {
		d.Page = 0
	}
	var search *string
	if d.Search != "" {
		search = &d.Search
	}
	rows, err := s.q.AdminListUsers(r.Context(), sqlc.AdminListUsersParams{Search: search, Lim: usersPerPage + 1, Off: int32(d.Page * usersPerPage)})
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	if len(rows) > usersPerPage {
		d.HasMore, rows = true, rows[:usersPerPage]
	}
	d.Users = rows
	if d.Total, err = s.q.AdminCountUsers(r.Context()); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	if d.Contests, err = s.q.ListContests(r.Context()); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	if d.Teams, err = s.q.ListTeams(r.Context()); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	s.render(w, "users", http.StatusOK, s.newPage(w, r, rc, "Users", "users", d))
}

type userPage struct {
	U              sqlc.User
	New            bool
	Participations []sqlc.AdminListUserParticipationsRow
	Contests       []sqlc.Contest
	Localizations  []localization
}

func (s *Server) userPage(ctx context.Context, u sqlc.User, isNew bool) (*userPage, error) {
	d := &userPage{U: u, New: isNew}
	names := i18n.Names
	for _, code := range i18n.Languages() {
		d.Localizations = append(d.Localizations, localization{code, names[code]})
	}
	var err error
	if d.Contests, err = s.q.ListContests(ctx); err != nil {
		return nil, err
	}
	if !isNew {
		if d.Participations, err = s.q.AdminListUserParticipations(ctx, u.ID); err != nil {
			return nil, err
		}
	}
	return d, nil
}

func (s *Server) handleUserNew(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	d, err := s.userPage(r.Context(), sqlc.User{PreferredLanguages: []string{}}, true)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	s.render(w, "user", http.StatusOK, s.newPage(w, r, rc, "New user", "users", d).crumb("Users", "/users"))
}

func parseUser(f *form, u sqlc.User) sqlc.User {
	u.Username = f.required("username", "Username")
	if strings.ContainsAny(u.Username, " \t\r\n/") || len(u.Username) > 64 {
		f.fail("the username may not contain spaces or '/' (max 64 characters)")
	}
	u.FirstName = f.str("first_name")
	u.LastName = f.str("last_name")
	u.Email = f.str("email")
	u.Timezone = nil
	if tz := f.str("timezone"); tz != "" {
		f.timezone("timezone")
		u.Timezone = &tz
	}
	u.PreferredLanguages = f.multi("preferred_languages")
	return u
}

func (s *Server) handleUserCreate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	f := newForm(r)
	u := parseUser(f, sqlc.User{})
	password := r.FormValue("password")
	if password == "" {
		f.fail("a password is required")
	}
	if f.err == nil {
		if _, err := s.q.GetUserByUsername(r.Context(), u.Username); err == nil {
			f.fail("the username %q is taken", u.Username)
		}
	}
	var contestID int64
	if v := f.str("contest_id"); v != "" {
		contestID, _ = strconv.ParseInt(v, 10, 64)
	}
	if f.err != nil {
		d, _ := s.userPage(r.Context(), u, true)
		s.formError(w, r, rc, "user", s.newPage(w, r, rc, "New user", "users", d).crumb("Users", "/users"), f.err.Error())
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	var created sqlc.User
	err = db.InTx(r.Context(), s.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		var err error
		created, err = q.CreateUser(r.Context(), sqlc.CreateUserParams{Username: u.Username, FirstName: u.FirstName,
			LastName: u.LastName, Email: u.Email, PasswordHash: hash, Timezone: u.Timezone, PreferredLanguages: u.PreferredLanguages})
		if err != nil {
			return err
		}
		if contestID != 0 {
			_, err = q.CreateParticipation(r.Context(), sqlc.CreateParticipationParams{ContestID: contestID, UserID: created.ID, Ip: []netip.Prefix{}})
		}
		return err
	})
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("user", created.ID)
	s.done(w, r, "/users/"+strconv.FormatInt(created.ID, 10), "User created.")
}

func (s *Server) loadUser(w http.ResponseWriter, r *http.Request, rc *reqCtx) (sqlc.User, bool) {
	id, _ := pathID(r, "id")
	u, err := s.q.GetUser(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r, rc)
		} else {
			s.internalError(w, r, rc, err)
		}
		return u, false
	}
	return u, true
}

func (s *Server) handleUser(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	u, ok := s.loadUser(w, r, rc)
	if !ok {
		return
	}
	d, err := s.userPage(r.Context(), u, false)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	s.render(w, "user", http.StatusOK, s.newPage(w, r, rc, u.Username, "users", d).crumb("Users", "/users"))
}

func (s *Server) handleUserUpdate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	old, ok := s.loadUser(w, r, rc)
	if !ok {
		return
	}
	f := newForm(r)
	u := parseUser(f, old)
	if f.err == nil && u.Username != old.Username {
		if _, err := s.q.GetUserByUsername(r.Context(), u.Username); err == nil {
			f.fail("the username %q is taken", u.Username)
		}
	}
	if f.err != nil {
		d, _ := s.userPage(r.Context(), u, false)
		s.formError(w, r, rc, "user", s.newPage(w, r, rc, old.Username, "users", d).crumb("Users", "/users"), f.err.Error())
		return
	}
	if _, err := s.q.UpdateUser(r.Context(), sqlc.UpdateUserParams{ID: u.ID, Username: u.Username, FirstName: u.FirstName,
		LastName: u.LastName, Email: u.Email, Timezone: u.Timezone, PreferredLanguages: u.PreferredLanguages}); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	if pw := r.FormValue("password"); pw != "" {
		hash, err := auth.HashPassword(pw)
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		if err := s.q.SetUserPassword(r.Context(), sqlc.SetUserPasswordParams{ID: u.ID, PasswordHash: hash}); err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		rc.note("password_changed", true)
	}
	rc.target("user", u.ID)
	s.invalidateUser(r.Context(), u.ID)
	s.done(w, r, "/users/"+strconv.FormatInt(u.ID, 10), "User saved.")
}

// invalidateUser drops the cached participations of a user in the CWS.
func (s *Server) invalidateUser(ctx context.Context, userID int64) {
	parts, err := s.q.ListParticipationsByUser(ctx, userID)
	if err != nil {
		return
	}
	for _, p := range parts {
		s.contestChanged(ctx, p.ContestID, p.ID)
	}
}

func (s *Server) handleUserDelete(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	u, ok := s.loadUser(w, r, rc)
	if !ok {
		return
	}
	if r.FormValue("confirm") != u.Username {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Type the username to confirm the deletion (it removes all the user's submissions).")
		return
	}
	s.invalidateUser(r.Context(), u.ID)
	if err := s.q.DeleteUser(r.Context(), u.ID); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("user", u.ID)
	rc.note("username", u.Username)
	s.done(w, r, "/users", "User "+u.Username+" deleted.")
}

// ---------------------------------------------------------------- CSV import

// importResult is shown after a CSV import.
type importResult struct {
	Created, Updated, Participations int
	Errors                           []string
	Credentials                      []credential
	CSV                              string
}

type credential struct{ Username, Password string }

// csvUser is one row of a user import.
type csvUser struct {
	line                            int
	username, password, first, last string
	email, timezone, team, ip       string
	hidden, unrestricted, generated bool
	hash                            string
	delay, extra                    int64
}

// Columns understood by the importer (header row required, any order):
// username (required), password, first_name, last_name, email, timezone,
// team (code), ip, hidden, unrestricted, delay_time, extra_time.
var csvColumns = map[string]bool{"username": true, "password": true, "first_name": true, "last_name": true,
	"email": true, "timezone": true, "team": true, "ip": true, "hidden": true, "unrestricted": true,
	"delay_time": true, "extra_time": true}

func parseUsersCSV(rd io.Reader) ([]csvUser, []string) {
	cr := csv.NewReader(rd)
	cr.TrimLeadingSpace = true
	cr.FieldsPerRecord = -1
	head, err := cr.Read()
	if err != nil {
		return nil, []string{"cannot read the header row: " + err.Error()}
	}
	idx := map[string]int{}
	var errs []string
	for i, h := range head {
		h = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))
		if !csvColumns[h] {
			errs = append(errs, fmt.Sprintf("unknown column %q", h))
			continue
		}
		idx[h] = i
	}
	if _, ok := idx["username"]; !ok {
		return nil, append(errs, "the username column is required")
	}
	var out []csvUser
	seen := map[string]int{}
	line := 1
	for {
		rec, err := cr.Read()
		line++
		if err == io.EOF {
			break
		}
		if err != nil {
			errs = append(errs, fmt.Sprintf("line %d: %v", line, err))
			continue
		}
		get := func(col string) string {
			if i, ok := idx[col]; ok && i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}
		u := csvUser{line: line, username: get("username"), password: get("password"), first: get("first_name"),
			last: get("last_name"), email: get("email"), timezone: get("timezone"), team: get("team"), ip: get("ip")}
		if u.username == "" {
			if strings.Join(rec, "") != "" {
				errs = append(errs, fmt.Sprintf("line %d: empty username", line))
			}
			continue
		}
		if strings.ContainsAny(u.username, " \t/") {
			errs = append(errs, fmt.Sprintf("line %d: invalid username %q", line, u.username))
			continue
		}
		if prev, dup := seen[u.username]; dup {
			errs = append(errs, fmt.Sprintf("line %d: username %q repeated (line %d)", line, u.username, prev))
			continue
		}
		seen[u.username] = line
		u.hidden = truthy(get("hidden"))
		u.unrestricted = truthy(get("unrestricted"))
		for _, col := range []struct {
			name string
			dst  *int64
		}{{"delay_time", &u.delay}, {"extra_time", &u.extra}} {
			if v := get(col.name); v != "" {
				n, err := strconv.ParseInt(v, 10, 64)
				if err != nil || n < 0 {
					errs = append(errs, fmt.Sprintf("line %d: invalid %s %q (seconds)", line, col.name, v))
				}
				*col.dst = n
			}
		}
		out = append(out, u)
	}
	return out, errs
}

func truthy(v string) bool {
	switch strings.ToLower(v) {
	case "1", "true", "yes", "y", "x", "si", "sí":
		return true
	}
	return false
}

// generatePassword returns a readable random password (no ambiguous
// characters).
func generatePassword() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	b := make([]byte, 10)
	rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// hashAll hashes passwords in parallel (argon2id is deliberately slow).
func hashAll(users []csvUser) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var first error
	sem := make(chan struct{}, max(1, runtime.GOMAXPROCS(0)))
	for i := range users {
		if users[i].password == "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(u *csvUser) {
			defer wg.Done()
			defer func() { <-sem }()
			h, err := auth.HashPassword(u.password)
			mu.Lock()
			defer mu.Unlock()
			if err != nil && first == nil {
				first = err
			}
			u.hash = h
		}(&users[i])
	}
	wg.Wait()
	return first
}

func (s *Server) handleUserImport(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	if err := s.parseUpload(w, r); err != nil {
		s.errorPage(w, r, rc, http.StatusBadRequest, "Upload failed: "+err.Error())
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Choose a CSV file.")
		return
	}
	defer f.Close()
	users, errs := parseUsersCSV(f)
	res := &importResult{Errors: errs}
	var contestID int64
	if v := r.FormValue("contest_id"); v != "" {
		contestID, _ = strconv.ParseInt(v, 10, 64)
		if _, err := s.q.GetContest(r.Context(), contestID); err != nil {
			s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Unknown contest.")
			return
		}
	}
	update := r.FormValue("update") != ""
	generate := r.FormValue("generate") != ""
	teams, err := s.q.ListTeams(r.Context())
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	teamIDs := map[string]int64{}
	for _, t := range teams {
		teamIDs[t.Code] = t.ID
	}
	valid := users[:0]
	for _, u := range users {
		if u.team != "" && teamIDs[u.team] == 0 {
			res.Errors = append(res.Errors, fmt.Sprintf("line %d: unknown team %q", u.line, u.team))
			continue
		}
		if u.timezone != "" {
			if _, err := loadLocation(u.timezone); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("line %d: unknown timezone %q", u.line, u.timezone))
				continue
			}
		}
		if u.ip != "" {
			if _, err := parsePrefixes(u.ip); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("line %d: invalid ip %q", u.line, u.ip))
				continue
			}
		}
		if u.password == "" && generate {
			u.password, u.generated = generatePassword(), true
		}
		valid = append(valid, u)
	}
	if len(res.Errors) > 0 {
		// Nothing is imported when the file has errors.
		rc.audit.skip = true
		s.render(w, "user_import", http.StatusUnprocessableEntity, s.newPage(w, r, rc, "Import users", "users", res).crumb("Users", "/users"))
		return
	}
	if err := hashAll(valid); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	err = db.InTx(r.Context(), s.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		for _, u := range valid {
			var tz *string
			if u.timezone != "" {
				tz = &u.timezone
			}
			existing, err := q.GetUserByUsername(r.Context(), u.username)
			var userID int64
			switch {
			case err == nil && !update:
				return fmt.Errorf("line %d: user %q already exists (tick \"update existing users\")", u.line, u.username)
			case err == nil:
				userID = existing.ID
				if _, err := q.UpdateUser(r.Context(), sqlc.UpdateUserParams{ID: existing.ID, Username: u.username,
					FirstName: u.first, LastName: u.last, Email: u.email, Timezone: tz, PreferredLanguages: existing.PreferredLanguages}); err != nil {
					return err
				}
				if u.hash != "" {
					if err := q.SetUserPassword(r.Context(), sqlc.SetUserPasswordParams{ID: existing.ID, PasswordHash: u.hash}); err != nil {
						return err
					}
				}
				res.Updated++
			case errors.Is(err, pgx.ErrNoRows):
				if u.hash == "" {
					return fmt.Errorf("line %d: user %q needs a password (or tick \"generate passwords\")", u.line, u.username)
				}
				created, err := q.CreateUser(r.Context(), sqlc.CreateUserParams{Username: u.username, FirstName: u.first,
					LastName: u.last, Email: u.email, PasswordHash: u.hash, Timezone: tz, PreferredLanguages: []string{}})
				if err != nil {
					return err
				}
				userID = created.ID
				res.Created++
			default:
				return err
			}
			if u.generated {
				res.Credentials = append(res.Credentials, credential{u.username, u.password})
			}
			if contestID == 0 {
				continue
			}
			ips, _ := parsePrefixes(u.ip)
			var team *int64
			if id := teamIDs[u.team]; id != 0 {
				team = &id
			}
			p, err := q.GetParticipationByContestUser(r.Context(), sqlc.GetParticipationByContestUserParams{ContestID: contestID, UserID: userID})
			switch {
			case err == nil:
				up := db.ParticipationToUpdate(p)
				up.TeamID, up.Ip, up.Hidden, up.Unrestricted, up.DelayTimeS, up.ExtraTimeS = team, ips, u.hidden, u.unrestricted, u.delay, u.extra
				if _, err := q.UpdateParticipation(r.Context(), up); err != nil {
					return err
				}
			case errors.Is(err, pgx.ErrNoRows):
				if _, err := q.CreateParticipation(r.Context(), sqlc.CreateParticipationParams{ContestID: contestID, UserID: userID,
					TeamID: team, Ip: ips, DelayTimeS: u.delay, ExtraTimeS: u.extra, Hidden: u.hidden, Unrestricted: u.unrestricted}); err != nil {
					return err
				}
				res.Participations++
			default:
				return err
			}
		}
		return nil
	})
	if err != nil {
		rc.audit.skip = true
		res.Errors = append(res.Errors, err.Error())
		res.Created, res.Updated, res.Participations, res.Credentials = 0, 0, 0, nil
		s.render(w, "user_import", http.StatusUnprocessableEntity, s.newPage(w, r, rc, "Import users", "users", res).crumb("Users", "/users"))
		return
	}
	if contestID != 0 {
		s.contestChanged(r.Context(), contestID, 0)
	}
	if len(res.Credentials) > 0 {
		var b strings.Builder
		cw := csv.NewWriter(&b)
		cw.Write([]string{"username", "password"})
		for _, c := range res.Credentials {
			cw.Write([]string{c.Username, c.Password})
		}
		cw.Flush()
		res.CSV = b.String()
	}
	rc.note("created", res.Created)
	rc.note("updated", res.Updated)
	rc.note("participations", res.Participations)
	s.render(w, "user_import", http.StatusOK, s.newPage(w, r, rc, "Import users", "users", res).crumb("Users", "/users"))
}

func parsePrefixes(v string) ([]netip.Prefix, error) {
	out := []netip.Prefix{}
	for _, x := range splitList(strings.ReplaceAll(v, ";", ",")) {
		p, err := parsePrefix(x)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// ---------------------------------------------------------------- teams

func (s *Server) handleTeams(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	teams, err := s.q.ListTeams(r.Context())
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	s.render(w, "teams", http.StatusOK, s.newPage(w, r, rc, "Teams", "teams", teams))
}

func (s *Server) teamImages(w http.ResponseWriter, r *http.Request, t *sqlc.Team) error {
	if r.MultipartForm == nil {
		return nil
	}
	for _, kind := range []string{"flag", "photo"} {
		if len(r.MultipartForm.File[kind]) == 0 {
			continue
		}
		digest, _, _, err := s.storeUpload(r, kind)
		if err != nil {
			return err
		}
		if kind == "flag" {
			t.FlagDigest = &digest
		} else {
			t.PhotoDigest = &digest
		}
	}
	if r.FormValue("remove_flag") != "" {
		t.FlagDigest = nil
	}
	if r.FormValue("remove_photo") != "" {
		t.PhotoDigest = nil
	}
	return nil
}

func (s *Server) handleTeamCreate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	if err := s.parseUpload(w, r); err != nil {
		s.errorPage(w, r, rc, http.StatusBadRequest, "Upload failed: "+err.Error())
		return
	}
	f := newForm(r)
	t := sqlc.Team{Code: f.identifier("code", "Code"), Name: f.required("name", "Name")}
	if f.err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, f.err.Error())
		return
	}
	if _, err := s.q.GetTeamByCode(r.Context(), t.Code); err == nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "A team with code "+t.Code+" already exists.")
		return
	}
	if err := s.teamImages(w, r, &t); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	created, err := s.q.CreateTeam(r.Context(), sqlc.CreateTeamParams{Code: t.Code, Name: t.Name, FlagDigest: t.FlagDigest, PhotoDigest: t.PhotoDigest})
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("team", created.ID)
	s.done(w, r, "/teams", "Team "+t.Code+" created.")
}

func (s *Server) loadTeam(w http.ResponseWriter, r *http.Request, rc *reqCtx) (sqlc.Team, bool) {
	id, _ := pathID(r, "id")
	t, err := s.q.GetTeam(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r, rc)
		} else {
			s.internalError(w, r, rc, err)
		}
		return t, false
	}
	return t, true
}

func (s *Server) handleTeam(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, ok := s.loadTeam(w, r, rc)
	if !ok {
		return
	}
	s.render(w, "team", http.StatusOK, s.newPage(w, r, rc, t.Code, "teams", t).crumb("Teams", "/teams"))
}

func (s *Server) handleTeamUpdate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, ok := s.loadTeam(w, r, rc)
	if !ok {
		return
	}
	if err := s.parseUpload(w, r); err != nil {
		s.errorPage(w, r, rc, http.StatusBadRequest, "Upload failed: "+err.Error())
		return
	}
	f := newForm(r)
	t.Code, t.Name = f.identifier("code", "Code"), f.required("name", "Name")
	if f.err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, f.err.Error())
		return
	}
	if err := s.teamImages(w, r, &t); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	if _, err := s.q.UpdateTeam(r.Context(), sqlc.UpdateTeamParams{ID: t.ID, Code: t.Code, Name: t.Name, FlagDigest: t.FlagDigest, PhotoDigest: t.PhotoDigest}); err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Could not save: "+err.Error())
		return
	}
	rc.target("team", t.ID)
	s.done(w, r, "/teams/"+strconv.FormatInt(t.ID, 10), "Team saved.")
}

func (s *Server) handleTeamDelete(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, ok := s.loadTeam(w, r, rc)
	if !ok {
		return
	}
	if err := s.q.DeleteTeam(r.Context(), t.ID); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("team", t.ID)
	rc.note("code", t.Code)
	s.done(w, r, "/teams", "Team "+t.Code+" deleted.")
}

func (s *Server) handleTeamImage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, ok := s.loadTeam(w, r, rc)
	if !ok {
		return
	}
	var d *string
	switch r.PathValue("kind") {
	case "flag":
		d = t.FlagDigest
	case "photo":
		d = t.PhotoDigest
	}
	if d == nil {
		s.notFound(w, r, rc)
		return
	}
	data, err := readBlobLimited(r.Context(), s, *d, 16<<20)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	ct := http.DetectContentType(data)
	if !strings.HasPrefix(ct, "image/") {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Write(data)
}
