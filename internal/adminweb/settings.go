package adminweb

// Server settings (SPEC_MIN §4): the time zone every site shows times in.
// Administrators change it at any moment; the contests follow it (a
// trigger keeps contests.timezone equal to it), so the contest pages,
// rankings and certificates show the new zone as soon as their caches
// hear of the change.

import (
	"bufio"
	"context"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

// zoneCache holds the server's zone for a few seconds (another admin
// server may change it; every page and every time shown needs it).
type zoneCache struct {
	mu      sync.Mutex
	loc     *time.Location
	fetched time.Time
}

const zoneTTL = 5 * time.Second

// zone is the server's time zone (UTC when it cannot be read).
func (s *Server) zone(ctx context.Context) *time.Location {
	s.tz.mu.Lock()
	defer s.tz.mu.Unlock()
	if s.tz.loc != nil && time.Since(s.tz.fetched) < zoneTTL {
		return s.tz.loc
	}
	loc := time.UTC
	if st, err := s.q.GetServerSettings(ctx); err == nil {
		if l, err := time.LoadLocation(st.Timezone); err == nil {
			loc = l
		}
	} else if s.tz.loc != nil {
		return s.tz.loc // keep the last known zone while the database is away
	}
	s.tz.loc, s.tz.fetched = loc, time.Now()
	return loc
}

func (s *Server) forgetZone() {
	s.tz.mu.Lock()
	s.tz.loc = nil
	s.tz.mu.Unlock()
}

// serverPage is the data of the server settings page.
type serverPage struct {
	Timezone string
	Now      time.Time
	Zones    []string
	Updated  time.Time
}

func (s *Server) handleServerSettings(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	st, err := s.q.GetServerSettings(r.Context())
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	d := serverPage{Timezone: st.Timezone, Now: s.now(), Zones: zoneNames(), Updated: st.UpdatedAt}
	s.render(w, "server", http.StatusOK, s.newPage(w, r, rc, "Server settings", "server", d))
}

func (s *Server) handleServerSettingsSave(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	f := newForm(r)
	tz := f.timezone("timezone")
	if f.err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, f.err.Error())
		return
	}
	var ids []int64
	err := db.InTx(r.Context(), s.pool, func(_ pgx.Tx, q *sqlc.Queries) error {
		if err := q.SetServerTimezone(r.Context(), tz); err != nil {
			return err
		}
		var err error
		ids, err = q.SyncContestsTimezone(r.Context(), tz)
		return err
	})
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	s.forgetZone()
	for _, id := range ids {
		s.contestChanged(r.Context(), id, 0)
	}
	rc.note("timezone", tz)
	s.done(w, r, "/server", "Every site now shows times in %s.", tz)
}

// zoneNames lists the time zones to choose from: the system's zone table
// when there is one, else a short list of common zones.
func zoneNames() []string {
	names := map[string]bool{"UTC": true}
	for _, path := range []string{"/usr/share/zoneinfo/tzdata.zi", "/usr/share/zoneinfo/zone1970.tab", "/usr/share/zoneinfo/zone.tab"} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := sc.Text()
			fields := strings.Fields(line)
			switch {
			case strings.HasSuffix(path, ".zi"):
				// "Z Area/City ..." zones and "L Target Area/Old" links.
				if len(fields) >= 2 && fields[0] == "Z" && strings.Contains(fields[1], "/") {
					names[fields[1]] = true
				}
			case len(fields) >= 3 && !strings.HasPrefix(line, "#"):
				names[fields[2]] = true
			}
		}
		f.Close()
		if len(names) > 1 {
			break
		}
	}
	if len(names) == 1 {
		for _, z := range commonZones {
			names[z] = true
		}
	}
	out := make([]string, 0, len(names))
	for n := range names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

var commonZones = []string{
	"America/Argentina/Buenos_Aires", "America/Bogota", "America/Caracas", "America/Chicago", "America/Denver",
	"America/Guatemala", "America/La_Paz", "America/Lima", "America/Los_Angeles", "America/Mexico_City",
	"America/Montevideo", "America/New_York", "America/Santiago", "America/Sao_Paulo", "America/Tijuana",
	"Asia/Bangkok", "Asia/Dubai", "Asia/Jakarta", "Asia/Kolkata", "Asia/Seoul", "Asia/Shanghai", "Asia/Tehran",
	"Asia/Tokyo", "Australia/Sydney", "Africa/Cairo", "Africa/Johannesburg", "Europe/Berlin", "Europe/Istanbul",
	"Europe/Kyiv", "Europe/London", "Europe/Madrid", "Europe/Moscow", "Europe/Paris", "Europe/Rome", "Europe/Warsaw",
}
