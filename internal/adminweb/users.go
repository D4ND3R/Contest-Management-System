package adminweb

import (
	"context"
	"crypto/rand"
	"encoding/csv"
	"errors"
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
	Sessions       []sessionView
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
		d.Sessions = s.sessionsOf(ctx, s.participationsOf(ctx, u.ID))
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

// parseAnyForm parses urlencoded or multipart bodies.
func (s *Server) parseAnyForm(w http.ResponseWriter, r *http.Request) error {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		return s.parseUpload(w, r)
	}
	return r.ParseForm()
}

func parseUser(f *form, u sqlc.User) sqlc.User {
	u.Username = f.required("username", "Username")
	if strings.ContainsAny(u.Username, " \t\r\n/") || len(u.Username) > 64 {
		f.fail("the username may not contain spaces or '/' (max 64 characters)")
	}
	u.FirstName = f.str("first_name")
	u.LastName = f.str("last_name")
	u.Email = f.str("email")
	u.Institution = f.str("institution")
	u.Country = f.str("country")
	u.Region = f.str("region")
	u.Timezone = nil
	if tz := f.str("timezone"); tz != "" {
		f.timezone("timezone")
		u.Timezone = &tz
	}
	u.PreferredLanguages = f.multi("preferred_languages")
	return u
}

// storePhoto saves an uploaded user photo (field "photo"), if any.
func (s *Server) storePhoto(r *http.Request) (*string, error) {
	if r.MultipartForm == nil || len(r.MultipartForm.File["photo"]) == 0 {
		return nil, nil
	}
	digest, _, _, err := s.storeUpload(r, "photo")
	if err != nil {
		return nil, err
	}
	data, err := readBlobLimited(r.Context(), s, digest, 16<<20)
	if err != nil {
		return nil, errors.New("the photo is too large (16 MiB at most)")
	}
	if ct := http.DetectContentType(data); !strings.HasPrefix(ct, "image/") {
		return nil, errors.New("the photo must be an image")
	}
	return &digest, nil
}

func (s *Server) handleUserCreate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	if err := s.parseAnyForm(w, r); err != nil {
		s.errorPage(w, r, rc, http.StatusBadRequest, "Invalid form: "+err.Error())
		return
	}
	f := newForm(r)
	u := parseUser(f, sqlc.User{})
	password := r.FormValue("password")
	if r.FormValue("generate_password") != "" {
		password = generatePassword()
	}
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
	photo, perr := s.storePhoto(r)
	if perr != nil {
		f.fail("%v", perr)
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
			LastName: u.LastName, Email: u.Email, PasswordHash: hash, Timezone: u.Timezone, PreferredLanguages: u.PreferredLanguages,
			Institution: u.Institution, Country: u.Country, Region: u.Region})
		if err != nil {
			return err
		}
		if photo != nil {
			if err := q.SetUserPhoto(r.Context(), sqlc.SetUserPhotoParams{ID: created.ID, PhotoDigest: photo}); err != nil {
				return err
			}
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
	if r.FormValue("generate_password") != "" {
		cs := []credential{{Username: created.Username, Password: password, Name: strings.TrimSpace(created.FirstName + " " + created.LastName)}}
		s.render(w, "credentials", http.StatusOK, s.newPage(w, r, rc, "User created", "users",
			&credentialsPage{Credentials: cs, CSV: credentialsCSV(cs), Back: "/users/" + strconv.FormatInt(created.ID, 10)}).crumb("Users", "/users"))
		return
	}
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
	if err := s.parseAnyForm(w, r); err != nil {
		s.errorPage(w, r, rc, http.StatusBadRequest, "Invalid form: "+err.Error())
		return
	}
	f := newForm(r)
	u := parseUser(f, old)
	if f.err == nil && u.Username != old.Username {
		if _, err := s.q.GetUserByUsername(r.Context(), u.Username); err == nil {
			f.fail("the username %q is taken", u.Username)
		}
	}
	photo, perr := s.storePhoto(r)
	if perr != nil {
		f.fail("%v", perr)
	}
	if f.err != nil {
		d, _ := s.userPage(r.Context(), u, false)
		s.formError(w, r, rc, "user", s.newPage(w, r, rc, old.Username, "users", d).crumb("Users", "/users"), f.err.Error())
		return
	}
	if _, err := s.q.UpdateUser(r.Context(), sqlc.UpdateUserParams{ID: u.ID, Username: u.Username, FirstName: u.FirstName,
		LastName: u.LastName, Email: u.Email, Timezone: u.Timezone, PreferredLanguages: u.PreferredLanguages,
		Institution: u.Institution, Country: u.Country, Region: u.Region}); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	switch {
	case photo != nil:
		if err := s.q.SetUserPhoto(r.Context(), sqlc.SetUserPhotoParams{ID: u.ID, PhotoDigest: photo}); err != nil {
			s.internalError(w, r, rc, err)
			return
		}
	case r.FormValue("remove_photo") != "":
		if err := s.q.SetUserPhoto(r.Context(), sqlc.SetUserPhotoParams{ID: u.ID}); err != nil {
			s.internalError(w, r, rc, err)
			return
		}
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
	for _, p := range s.participationsOf(ctx, userID) {
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

// importPage is the preview (before anything is written) or the result.
type importPage struct {
	Preview                          bool
	Rows                             []importRow
	Errors                           []string // file-level problems
	Valid, Invalid                   int
	Created, Updated, Participations int
	Credentials                      []credential
	CSV                              string
	Digest                           string
	Title                            string
	Contest                          *sqlc.Contest
	Update, Generate                 bool
	URL                              string
}

// importRow is one CSV row with what the import does with it.
type importRow struct {
	Line     int
	Username string
	Name     string
	Action   string // create, update, error
	Error    string
	Team     string
	Site     string
}

type credential struct{ Username, Password, Name, Site string }

// csvUser is one row of a user import.
type csvUser struct {
	line                            int
	username, password, first, last string
	email, timezone, team, ip, site string
	institution, country, region    string
	hidden, unrestricted, generated bool
	hash                            string
	delay, extra                    int64
	err                             string
}

// Columns understood by the importer (header row required, any order).
var csvColumns = map[string]bool{"username": true, "password": true, "first_name": true, "last_name": true,
	"email": true, "timezone": true, "team": true, "ip": true, "hidden": true, "unrestricted": true,
	"delay_time": true, "extra_time": true, "institution": true, "country": true, "region": true, "site": true}

// parseUsersCSV reads the rows; row problems are attached to the row,
// file problems returned separately.
func parseUsersCSV(rd io.Reader, tr func(string, ...any) string) ([]csvUser, []string) {
	cr := csv.NewReader(rd)
	cr.TrimLeadingSpace = true
	cr.FieldsPerRecord = -1
	head, err := cr.Read()
	if err != nil {
		return nil, []string{tr("cannot read the header row: %v", err)}
	}
	idx := map[string]int{}
	var errs []string
	for i, h := range head {
		h = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))
		if !csvColumns[h] {
			errs = append(errs, tr("unknown column %q", h))
			continue
		}
		idx[h] = i
	}
	if _, ok := idx["username"]; !ok {
		return nil, append(errs, tr("the username column is required"))
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
			errs = append(errs, tr("line %d: %v", line, err))
			continue
		}
		get := func(col string) string {
			if i, ok := idx[col]; ok && i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}
		u := csvUser{line: line, username: get("username"), password: get("password"), first: get("first_name"),
			last: get("last_name"), email: get("email"), timezone: get("timezone"), team: get("team"), ip: get("ip"),
			institution: get("institution"), country: get("country"), region: get("region"), site: get("site")}
		if u.username == "" {
			if strings.Join(rec, "") != "" {
				u.err = tr("empty username")
				out = append(out, u)
			}
			continue
		}
		switch {
		case strings.ContainsAny(u.username, " \t/"):
			u.err = tr("invalid username")
		case seen[u.username] != 0:
			u.err = tr("username repeated (line %d)", seen[u.username])
		}
		if seen[u.username] == 0 {
			seen[u.username] = line
		}
		u.hidden = truthy(get("hidden"))
		u.unrestricted = truthy(get("unrestricted"))
		for _, col := range []struct {
			name string
			dst  *int64
		}{{"delay_time", &u.delay}, {"extra_time", &u.extra}} {
			if v := get(col.name); v != "" {
				n, err := strconv.ParseInt(v, 10, 64)
				if err != nil || n < 0 {
					u.err = tr("invalid %s %q (seconds)", col.name, v)
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

// handleUserImport runs in two steps: the uploaded file is stored and
// previewed (every row with its action or error, nothing written); the
// confirmation imports the stored file atomically.
func (s *Server) handleUserImport(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	if err := s.parseAnyForm(w, r); err != nil {
		s.errorPage(w, r, rc, http.StatusBadRequest, "Upload failed: "+err.Error())
		return
	}
	confirm := r.FormValue("step") == "confirm"
	var data []byte
	digest := r.FormValue("digest")
	if confirm {
		b, err := readBlobLimited(r.Context(), s, digest, 64<<20)
		if err != nil || !blobDigestOK(digest) {
			s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "The previewed file is gone; upload it again.")
			return
		}
		data = b
	} else {
		f, _, err := r.FormFile("file")
		if err != nil {
			s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Choose a CSV file.")
			return
		}
		data, err = io.ReadAll(io.LimitReader(f, 64<<20))
		f.Close()
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		info, err := s.blobs.PutBytes(r.Context(), data)
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		digest = info.Digest
	}
	res := &importPage{Preview: !confirm, Digest: digest, Update: r.FormValue("update") != "", Generate: r.FormValue("generate") != ""}
	var contestID int64
	if v := r.FormValue("contest_id"); v != "" {
		contestID, _ = strconv.ParseInt(v, 10, 64)
		c, err := s.q.GetContest(r.Context(), contestID)
		if err != nil {
			s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Unknown contest.")
			return
		}
		res.Contest = &c
		res.URL = s.contestURL(r, c.Name)
		res.Title = c.Description
		if res.Title == "" {
			res.Title = c.Name
		}
	}
	users, fileErrs := parseUsersCSV(strings.NewReader(string(data)), adminTr(r))
	res.Errors = fileErrs
	if err := s.validateImport(r, users, res, contestID); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	page := func(status int) {
		p := s.newPage(w, r, rc, "Import users", "users", res).crumb("Users", "/users")
		s.render(w, "user_import", status, p)
	}
	if !confirm || res.Invalid > 0 || len(res.Errors) > 0 {
		// The preview (and a confirmation of a file that became invalid)
		// writes nothing.
		rc.audit.skip = true
		status := http.StatusOK
		if confirm {
			status = http.StatusUnprocessableEntity
		}
		page(status)
		return
	}
	if err := s.runImport(r, users, res, contestID); err != nil {
		rc.audit.skip = true
		res.Errors = append(res.Errors, err.Error())
		res.Created, res.Updated, res.Participations, res.Credentials = 0, 0, 0, nil
		page(http.StatusUnprocessableEntity)
		return
	}
	if contestID != 0 {
		s.invalidateContestParticipations(r, contestID)
	}
	if len(res.Credentials) > 0 {
		res.CSV = credentialsCSV(res.Credentials)
	}
	rc.note("created", res.Created)
	rc.note("updated", res.Updated)
	rc.note("participations", res.Participations)
	page(http.StatusOK)
}

func blobDigestOK(d string) bool { return len(d) == 64 }

// validateImport fills the per-row preview.
func (s *Server) validateImport(r *http.Request, users []csvUser, res *importPage, contestID int64) error {
	teams, err := s.q.ListTeams(r.Context())
	if err != nil {
		return err
	}
	teamIDs := map[string]bool{}
	for _, t := range teams {
		teamIDs[t.Code] = true
	}
	siteIDs := map[string]bool{}
	if contestID != 0 {
		sites, err := s.q.ListSites(r.Context(), contestID)
		if err != nil {
			return err
		}
		for _, st := range sites {
			siteIDs[st.Name] = true
		}
	}
	names := make([]string, 0, len(users))
	for _, u := range users {
		names = append(names, u.username)
	}
	existing, err := s.q.ListExistingUsernames(r.Context(), names)
	if err != nil {
		return err
	}
	exists := make(map[string]bool, len(existing))
	for _, n := range existing {
		exists[n] = true
	}
	tr := adminTr(r)
	for i := range users {
		u := &users[i]
		row := importRow{Line: u.line, Username: u.username, Name: strings.TrimSpace(u.first + " " + u.last), Team: u.team, Site: u.site}
		switch {
		case u.err != "":
		case u.team != "" && !teamIDs[u.team]:
			u.err = tr("unknown team %q", u.team)
		case u.site != "" && contestID == 0:
			u.err = tr("a site needs a contest")
		case u.site != "" && !siteIDs[u.site]:
			u.err = tr("unknown site %q", u.site)
		case u.timezone != "" && !validTZ(u.timezone):
			u.err = tr("unknown timezone %q", u.timezone)
		case u.ip != "" && !validIPs(u.ip):
			u.err = tr("invalid ip %q", u.ip)
		}
		if u.err == "" {
			switch {
			case exists[u.username] && !res.Update:
				u.err = tr("the user exists (tick “update existing”)")
			case exists[u.username]:
				row.Action = "update"
			default:
				row.Action = "create"
				if u.password == "" && !res.Generate {
					u.err = tr("a password is required (or tick “generate missing passwords”)")
				}
			}
		}
		if u.err != "" {
			row.Action, row.Error = "error", u.err
			res.Invalid++
		} else {
			res.Valid++
		}
		res.Rows = append(res.Rows, row)
	}
	return nil
}

func validTZ(tz string) bool {
	_, err := loadLocation(tz)
	return err == nil
}

func validIPs(v string) bool {
	_, err := parsePrefixes(v)
	return err == nil
}

// runImport writes the validated rows in one transaction.
func (s *Server) runImport(r *http.Request, users []csvUser, res *importPage, contestID int64) error {
	for i := range users {
		if users[i].password == "" && res.Generate {
			if _, err := s.q.GetUserByUsername(r.Context(), users[i].username); errors.Is(err, pgx.ErrNoRows) {
				users[i].password, users[i].generated = generatePassword(), true
			}
		}
	}
	if err := hashAll(users); err != nil {
		return err
	}
	teams, err := s.q.ListTeams(r.Context())
	if err != nil {
		return err
	}
	teamIDs := map[string]int64{}
	for _, t := range teams {
		teamIDs[t.Code] = t.ID
	}
	siteIDs := map[string]int64{}
	if contestID != 0 {
		sites, err := s.q.ListSites(r.Context(), contestID)
		if err != nil {
			return err
		}
		for _, st := range sites {
			siteIDs[st.Name] = st.ID
		}
	}
	return db.InTx(r.Context(), s.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		for _, u := range users {
			var tz *string
			if u.timezone != "" {
				tz = &u.timezone
			}
			existing, err := q.GetUserByUsername(r.Context(), u.username)
			var userID int64
			switch {
			case err == nil:
				userID = existing.ID
				if _, err := q.UpdateUser(r.Context(), sqlc.UpdateUserParams{ID: existing.ID, Username: u.username,
					FirstName: u.first, LastName: u.last, Email: u.email, Timezone: tz, PreferredLanguages: existing.PreferredLanguages,
					Institution: u.institution, Country: u.country, Region: u.region}); err != nil {
					return err
				}
				if u.hash != "" {
					if err := q.SetUserPassword(r.Context(), sqlc.SetUserPasswordParams{ID: existing.ID, PasswordHash: u.hash}); err != nil {
						return err
					}
				}
				res.Updated++
			case errors.Is(err, pgx.ErrNoRows):
				created, err := q.CreateUser(r.Context(), sqlc.CreateUserParams{Username: u.username, FirstName: u.first,
					LastName: u.last, Email: u.email, PasswordHash: u.hash, Timezone: tz, PreferredLanguages: []string{},
					Institution: u.institution, Country: u.country, Region: u.region})
				if err != nil {
					return err
				}
				userID = created.ID
				res.Created++
			default:
				return err
			}
			if u.generated {
				res.Credentials = append(res.Credentials, credential{u.username, u.password, strings.TrimSpace(u.first + " " + u.last), u.site})
			}
			if contestID == 0 {
				continue
			}
			ips, _ := parsePrefixes(u.ip)
			var team, site *int64
			if id := teamIDs[u.team]; id != 0 {
				team = &id
			}
			if id := siteIDs[u.site]; id != 0 {
				site = &id
			}
			p, err := q.GetParticipationByContestUser(r.Context(), sqlc.GetParticipationByContestUserParams{ContestID: contestID, UserID: userID})
			switch {
			case err == nil:
				up := db.ParticipationToUpdate(p)
				up.TeamID, up.Ip, up.Hidden, up.Unrestricted, up.DelayTimeS, up.ExtraTimeS, up.SiteID = team, ips, u.hidden, u.unrestricted, u.delay, u.extra, site
				if _, err := q.UpdateParticipation(r.Context(), up); err != nil {
					return err
				}
			case errors.Is(err, pgx.ErrNoRows):
				np, err := q.CreateParticipation(r.Context(), sqlc.CreateParticipationParams{ContestID: contestID, UserID: userID,
					TeamID: team, Ip: ips, DelayTimeS: u.delay, ExtraTimeS: u.extra, Hidden: u.hidden, Unrestricted: u.unrestricted})
				if err != nil {
					return err
				}
				if site != nil {
					up := db.ParticipationToUpdate(np)
					up.SiteID = site
					if _, err := q.UpdateParticipation(r.Context(), up); err != nil {
						return err
					}
				}
				res.Participations++
			default:
				return err
			}
		}
		return nil
	})
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
	t := sqlc.Team{Code: f.identifier("code", "Code"), Name: f.required("name", "Name"), Institution: f.str("institution")}
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
	created, err := s.q.CreateTeam(r.Context(), sqlc.CreateTeamParams{Code: t.Code, Name: t.Name, FlagDigest: t.FlagDigest,
		PhotoDigest: t.PhotoDigest, Institution: t.Institution})
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
	members, err := s.q.ListTeamMembers(r.Context(), &t.ID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	contests, err := s.q.ListContests(r.Context())
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	s.render(w, "team", http.StatusOK, s.newPage(w, r, rc, t.Code, "teams", struct {
		T        sqlc.Team
		Members  []sqlc.ListTeamMembersRow
		Contests []sqlc.Contest
	}{t, members, contests}).crumb("Teams", "/teams"))
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
	t.Code, t.Name, t.Institution = f.identifier("code", "Code"), f.required("name", "Name"), f.str("institution")
	if f.err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, f.err.Error())
		return
	}
	if err := s.teamImages(w, r, &t); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	if _, err := s.q.UpdateTeam(r.Context(), sqlc.UpdateTeamParams{ID: t.ID, Code: t.Code, Name: t.Name, FlagDigest: t.FlagDigest,
		PhotoDigest: t.PhotoDigest, Institution: t.Institution}); err != nil {
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
