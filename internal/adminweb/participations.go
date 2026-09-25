package adminweb

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/D4ND3R/Contest-Management-System/internal/auth"
	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/jackc/pgx/v5"
)

func loadLocation(tz string) (*time.Location, error) { return time.LoadLocation(tz) }

func readBlobLimited(ctx context.Context, s *Server, digest string, limit int64) ([]byte, error) {
	b, truncated, err := blob.ReadLimited(ctx, s.blobs, digest, limit)
	if err != nil {
		return nil, err
	}
	if truncated {
		return nil, errors.New("file too large")
	}
	return b, nil
}

type participationsPage struct {
	Contest sqlc.Contest
	Rows    []sqlc.ListParticipationsByContestRow
	Teams   []sqlc.Team
	Hidden  int
	// Pending counts self-registrations waiting for approval.
	Pending int
}

func (s *Server) handleParticipations(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	d := &participationsPage{Contest: c}
	var err error
	if d.Rows, err = s.q.ListParticipationsByContest(r.Context(), c.ID); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	if d.Teams, err = s.q.ListTeams(r.Context()); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	for _, p := range d.Rows {
		if p.Participation.Hidden {
			d.Hidden++
		}
		if !p.Participation.Approved {
			d.Pending++
		}
	}
	s.render(w, "participations", http.StatusOK, s.newPage(w, r, rc, "Participations", "contests", d).
		crumb("Contests", "/contests").crumb(c.Name, "/contests/"+strconv.FormatInt(c.ID, 10)))
}

// handleParticipationCreate adds users (a list of usernames) to a contest.
func (s *Server) handleParticipationCreate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	names := splitList(r.FormValue("usernames"))
	if len(names) == 0 {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Enter at least one username.")
		return
	}
	var team *int64
	if v := r.FormValue("team_id"); v != "" {
		id, _ := strconv.ParseInt(v, 10, 64)
		team = &id
	}
	hidden, unrestricted := r.FormValue("hidden") != "", r.FormValue("unrestricted") != ""
	added := 0
	err := db.InTx(r.Context(), s.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		for _, n := range names {
			u, err := q.GetUserByUsername(r.Context(), n)
			if err != nil {
				return errors.New("unknown user " + n)
			}
			if _, err := q.GetParticipationByContestUser(r.Context(), sqlc.GetParticipationByContestUserParams{ContestID: c.ID, UserID: u.ID}); err == nil {
				continue
			}
			if _, err := q.CreateParticipation(r.Context(), sqlc.CreateParticipationParams{ContestID: c.ID, UserID: u.ID,
				TeamID: team, Ip: []netip.Prefix{}, Hidden: hidden, Unrestricted: unrestricted}); err != nil {
				return err
			}
			added++
		}
		return nil
	})
	if err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, err.Error())
		return
	}
	rc.target("contest", c.ID)
	rc.note("added", added)
	s.contestChanged(r.Context(), c.ID, 0)
	s.done(w, r, "/contests/"+strconv.FormatInt(c.ID, 10)+"/participations", strconv.Itoa(added)+" participations added.")
}

type participationPage struct {
	P        sqlc.AdminGetParticipationRow
	Contest  sqlc.Contest
	Teams    []sqlc.Team
	Sites    []sqlc.Site
	Scores   []scoreRow
	Sessions []sessionView
	Tasks    []sqlc.Task
	// Adjustments are the manual score changes, oldest first.
	Adjustments []sqlc.ListScoreAdjustmentsRow
}

type scoreRow struct {
	Task       string
	Score      float64
	Precision  int32
	Pending    int32
	Adjustment float64
}

func (s *Server) loadParticipation(w http.ResponseWriter, r *http.Request, rc *reqCtx) (sqlc.AdminGetParticipationRow, bool) {
	id, _ := pathID(r, "id")
	p, err := s.q.AdminGetParticipation(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r, rc)
		} else {
			s.internalError(w, r, rc, err)
		}
		return p, false
	}
	return p, true
}

func (s *Server) handleParticipation(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	p, ok := s.loadParticipation(w, r, rc)
	if !ok {
		return
	}
	d := &participationPage{P: p}
	var err error
	if d.Contest, err = s.q.GetContest(r.Context(), p.ContestID); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	if d.Teams, err = s.q.ListTeams(r.Context()); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	if d.Sites, err = s.q.ListSites(r.Context(), p.ContestID); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	d.Sessions = s.sessionsOf(r.Context(), []sqlc.Participation{{ID: p.ID, ContestID: p.ContestID, LoginNonce: p.LoginNonce}})
	tasks, err := s.q.ListTasksByContest(r.Context(), &p.ContestID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	scores, err := s.q.ListScoresByParticipation(r.Context(), p.ID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	d.Tasks = tasks
	for _, t := range tasks {
		row := scoreRow{Task: t.Name, Precision: t.ScorePrecision}
		for _, sc := range scores {
			if sc.TaskID == t.ID {
				row.Score, row.Pending, row.Adjustment = sc.Score, sc.Pending, sc.Adjustment
			}
		}
		d.Scores = append(d.Scores, row)
	}
	if d.Adjustments, err = s.q.ListScoreAdjustments(r.Context(), []int64{p.ID}); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	s.render(w, "participation", http.StatusOK, s.newPage(w, r, rc, p.Username+" in "+p.ContestName, "contests", d).
		crumb("Contests", "/contests").crumb(p.ContestName, "/contests/"+strconv.FormatInt(p.ContestID, 10)).
		crumb("Participations", "/contests/"+strconv.FormatInt(p.ContestID, 10)+"/participations"))
}

func (s *Server) handleParticipationUpdate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	p, ok := s.loadParticipation(w, r, rc)
	if !ok {
		return
	}
	f := newForm(r)
	up := sqlc.UpdateParticipationParams{ID: p.ID, TeamID: nil, StartingTime: p.StartingTime}
	if v := f.str("team_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			f.fail("invalid team")
		}
		up.TeamID = &id
	}
	if v := f.str("site_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			f.fail("invalid site")
		}
		up.SiteID = &id
	}
	up.Ip = f.prefixes("ip", "Allowed addresses")
	up.DelayTimeS = f.nonNeg("delay_time_s", "Delay", 0)
	up.ExtraTimeS = f.nonNeg("extra_time_s", "Extra time", 0)
	up.Hidden = f.check("hidden")
	up.Unrestricted = f.check("unrestricted")
	if f.check("reset_start") {
		up.StartingTime = nil
	}
	if f.err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, f.err.Error())
		return
	}
	err := db.InTx(r.Context(), s.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		if _, err := q.UpdateParticipation(r.Context(), up); err != nil {
			return err
		}
		if up.TeamID != nil && (p.TeamID == nil || *p.TeamID != *up.TeamID) {
			c, err := q.GetContest(r.Context(), p.ContestID)
			if err != nil {
				return err
			}
			return checkTeamSize(r, q, c, *up.TeamID)
		}
		return nil
	})
	if err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, err.Error())
		return
	}
	switch pw := r.FormValue("participation_password"); {
	case f.check("clear_password"):
		if err := s.q.SetParticipationPassword(r.Context(), sqlc.SetParticipationPasswordParams{ID: p.ID}); err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		rc.note("password_cleared", true)
	case strings.TrimSpace(pw) != "":
		hash, err := auth.HashPassword(pw)
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		if err := s.q.SetParticipationPassword(r.Context(), sqlc.SetParticipationPasswordParams{ID: p.ID, PasswordHash: &hash}); err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		rc.note("password_changed", true)
	}
	if f.check("logout") {
		if err := s.logoutParticipation(r.Context(), sqlc.Participation{ID: p.ID, ContestID: p.ContestID}); err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		rc.note("logged_out", true)
	}

	rc.target("participation", p.ID)
	s.contestChanged(r.Context(), p.ContestID, p.ID)
	s.done(w, r, "/participations/"+strconv.FormatInt(p.ID, 10), "Participation saved.")
}

func (s *Server) handleParticipationDelete(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	p, ok := s.loadParticipation(w, r, rc)
	if !ok {
		return
	}
	if r.FormValue("confirm") != p.Username {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Type the username to confirm (it removes the participation's submissions).")
		return
	}
	if err := s.q.DeleteParticipation(r.Context(), p.ID); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("participation", p.ID)
	rc.note("username", p.Username)
	s.contestChanged(r.Context(), p.ContestID, p.ID)
	s.done(w, r, "/contests/"+strconv.FormatInt(p.ContestID, 10)+"/participations", "Participation deleted.")
}

// handleParticipationApprove admits a self-registered contestant.
func (s *Server) handleParticipationApprove(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	p, ok := s.loadParticipation(w, r, rc)
	if !ok {
		return
	}
	if _, err := s.q.SetParticipationApproved(r.Context(), sqlc.SetParticipationApprovedParams{ID: p.ID, Approved: true}); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("participation", p.ID)
	rc.note("username", p.Username)
	s.contestChanged(r.Context(), p.ContestID, p.ID)
	s.done(w, r, "/contests/"+strconv.FormatInt(p.ContestID, 10)+"/participations", "Registration approved.")
}

// handleParticipationReject removes a registration that was not approved
// (the account stays, without this contest).
func (s *Server) handleParticipationReject(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	p, ok := s.loadParticipation(w, r, rc)
	if !ok {
		return
	}
	if p.Approved {
		s.errorPage(w, r, rc, http.StatusConflict, "Only registrations waiting for approval can be rejected.")
		return
	}
	if err := s.q.DeleteParticipation(r.Context(), p.ID); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("participation", p.ID)
	rc.note("username", p.Username)
	s.contestChanged(r.Context(), p.ContestID, p.ID)
	s.done(w, r, "/contests/"+strconv.FormatInt(p.ContestID, 10)+"/participations", "Registration rejected.")
}

// handleScoreAdjust adds points to (or removes them from) a contestant's
// task score, with a mandatory reason. Adjustments are never edited or
// deleted: a correction is another adjustment.
func (s *Server) handleScoreAdjust(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	p, ok := s.loadParticipation(w, r, rc)
	if !ok {
		return
	}
	f := newForm(r)
	taskID, _ := strconv.ParseInt(f.str("task_id"), 10, 64)
	task, err := s.q.GetTask(r.Context(), taskID)
	if err != nil || task.ContestID == nil || *task.ContestID != p.ContestID {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Choose a task of the contest.")
		return
	}
	points, ok := f.float("points", "Points")
	reason := f.str("reason")
	switch {
	case f.err != nil:
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, f.err.Error())
		return
	case !ok || points == 0 || math.IsNaN(points) || math.Abs(points) > 1e6:
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Write the points to add (negative to remove), not zero.")
		return
	case utf8.RuneCountInString(reason) < 5 || utf8.RuneCountInString(reason) > 1000:
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "The reason is required (5 to 1000 characters).")
		return
	}
	err = db.InTx(r.Context(), s.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		if _, err := q.CreateScoreAdjustment(r.Context(), sqlc.CreateScoreAdjustmentParams{ParticipationID: p.ID, TaskID: task.ID,
			Points: points, Reason: reason, AdminID: &rc.admin.ID}); err != nil {
			return err
		}
		return q.ApplyScoreAdjustment(r.Context(), sqlc.ApplyScoreAdjustmentParams{ParticipationID: p.ID, TaskID: task.ID, Points: points})
	})
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	// The dispatcher recomputes the score (with the adjustment) and tells
	// the rankings.
	if err := s.queue.Notify(r.Context(), queue.Event{Kind: queue.EventReaggregate, ParticipationID: p.ID, TaskID: task.ID}); err != nil {
		s.log.Warn("notify dispatcher", "error", err)
	}
	rc.target("participation", p.ID)
	rc.note("task", task.Name)
	rc.note("points", points)
	rc.note("reason", reason)
	s.contestChanged(r.Context(), p.ContestID, p.ID)
	s.done(w, r, "/participations/"+strconv.FormatInt(p.ID, 10), "Score adjusted.")
}
