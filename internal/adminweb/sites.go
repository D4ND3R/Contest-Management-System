package adminweb

import (
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

// Sites (venues) of multi-site contests: each may start at its own time
// (same duration); participants are assigned one site.

type sitesPage struct {
	Contest sqlc.Contest
	Sites   []siteRow
}

type siteRow struct {
	sqlc.Site
	Participants int
}

func (s *Server) handleSites(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	sites, err := s.q.ListSites(r.Context(), c.ID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	parts, err := s.q.ListParticipationsByContest(r.Context(), c.ID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	count := map[int64]int{}
	for _, p := range parts {
		if p.Participation.SiteID != nil {
			count[*p.Participation.SiteID]++
		}
	}
	d := &sitesPage{Contest: c}
	for _, st := range sites {
		d.Sites = append(d.Sites, siteRow{Site: st, Participants: count[st.ID]})
	}
	s.render(w, "sites", http.StatusOK, s.newPage(w, r, rc, "Sites", "contests", d).
		crumb("Contests", "/contests").crumb(c.Name, "/contests/"+strconv.FormatInt(c.ID, 10)))
}

func (s *Server) contestLocation(c sqlc.Contest) *time.Location {
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

func (s *Server) handleSiteCreate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	f := newForm(r)
	name := f.required("name", "Name")
	start := f.optTime("start_time", "Start", s.contestLocation(c))
	if f.err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, f.err.Error())
		return
	}
	site, err := s.q.CreateSite(r.Context(), sqlc.CreateSiteParams{ContestID: c.ID, Name: name, StartTime: start})
	if err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Could not create the site: "+err.Error())
		return
	}
	rc.target("site", site.ID)
	s.contestChanged(r.Context(), c.ID, 0)
	s.done(w, r, "/contests/"+strconv.FormatInt(c.ID, 10)+"/sites", "Site "+name+" created.")
}

func (s *Server) loadSite(w http.ResponseWriter, r *http.Request, rc *reqCtx) (sqlc.Site, sqlc.Contest, bool) {
	id, _ := pathID(r, "id")
	st, err := s.q.GetSite(r.Context(), id)
	if err != nil {
		s.notFound(w, r, rc)
		return st, sqlc.Contest{}, false
	}
	c, err := s.q.GetContest(r.Context(), st.ContestID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return st, c, false
	}
	return st, c, true
}

func (s *Server) handleSiteUpdate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	st, c, ok := s.loadSite(w, r, rc)
	if !ok {
		return
	}
	f := newForm(r)
	name := f.required("name", "Name")
	start := f.optTime("start_time", "Start", s.contestLocation(c))
	if f.err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, f.err.Error())
		return
	}
	if _, err := s.q.UpdateSite(r.Context(), sqlc.UpdateSiteParams{ID: st.ID, Name: name, StartTime: start}); err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Could not save: "+err.Error())
		return
	}
	rc.target("site", st.ID)
	s.invalidateContestParticipations(r, c.ID)
	s.done(w, r, "/contests/"+strconv.FormatInt(c.ID, 10)+"/sites", "Site saved.")
}

func (s *Server) handleSiteDelete(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	st, c, ok := s.loadSite(w, r, rc)
	if !ok {
		return
	}
	if err := s.q.DeleteSite(r.Context(), st.ID); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("site", st.ID)
	rc.note("name", st.Name)
	s.invalidateContestParticipations(r, c.ID)
	s.done(w, r, "/contests/"+strconv.FormatInt(c.ID, 10)+"/sites", "Site deleted.")
}

// invalidateContestParticipations drops every cached participation of a
// contest in the contest web servers (site times changed).
func (s *Server) invalidateContestParticipations(r *http.Request, contestID int64) {
	parts, err := s.q.ListParticipationsByContest(r.Context(), contestID)
	if err != nil {
		return
	}
	s.contestChanged(r.Context(), contestID, 0)
	for _, p := range parts {
		s.contestChanged(r.Context(), contestID, p.Participation.ID)
	}
}

// handleTeamMembers assigns users to a team in a contest (creating their
// participations when needed).
func (s *Server) handleTeamMembers(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, ok := s.loadTeam(w, r, rc)
	if !ok {
		return
	}
	contestID, err := strconv.ParseInt(r.FormValue("contest_id"), 10, 64)
	if err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Choose a contest.")
		return
	}
	c, err := s.q.GetContest(r.Context(), contestID)
	if err != nil {
		s.notFound(w, r, rc)
		return
	}
	names := splitList(r.FormValue("usernames"))
	if len(names) == 0 {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Enter at least one username.")
		return
	}
	err = db.InTx(r.Context(), s.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		for _, n := range names {
			u, err := q.GetUserByUsername(r.Context(), n)
			if err != nil {
				return errors.New("unknown user " + n)
			}
			p, err := q.GetParticipationByContestUser(r.Context(), sqlc.GetParticipationByContestUserParams{ContestID: c.ID, UserID: u.ID})
			switch {
			case err == nil:
				up := db.ParticipationToUpdate(p)
				up.TeamID = &t.ID
				if _, err := q.UpdateParticipation(r.Context(), up); err != nil {
					return err
				}
			case errors.Is(err, pgx.ErrNoRows):
				if _, err := q.CreateParticipation(r.Context(), sqlc.CreateParticipationParams{ContestID: c.ID, UserID: u.ID,
					TeamID: &t.ID, Ip: []netip.Prefix{}}); err != nil {
					return err
				}
			default:
				return err
			}
		}
		return checkTeamSize(r, q, c, t.ID)
	})
	if err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, err.Error())
		return
	}
	rc.target("team", t.ID)
	rc.note("contest", c.Name)
	s.invalidateContestParticipations(r, c.ID)
	s.done(w, r, "/teams/"+strconv.FormatInt(t.ID, 10), "Members added.")
}

// checkTeamSize enforces the contest's maximum team size.
func checkTeamSize(r *http.Request, q *sqlc.Queries, c sqlc.Contest, teamID int64) error {
	if c.MaxTeamSize == nil {
		return nil
	}
	n, err := q.CountTeamMembers(r.Context(), sqlc.CountTeamMembersParams{ContestID: c.ID, TeamID: &teamID})
	if err != nil {
		return err
	}
	if n > int64(*c.MaxTeamSize) {
		return errors.New("the team would have " + strconv.FormatInt(n, 10) + " members; the contest allows " + strconv.Itoa(int(*c.MaxTeamSize)))
	}
	return nil
}
