package adminweb

import (
	"net/http"
	"strconv"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/scoring"
)

// Delegation leaders (SPEC_IOI §9.2) follow their contestants: their
// submissions, sources and results as the contestants themselves see
// them (public scores, and only when the contest shows scores). They
// cannot submit, nor see anybody else. Other administrators reach the same
// view for any team (?team=).

type delegationPage struct {
	Team     *sqlc.Team
	Teams    []sqlc.Team
	Contests []*delegationContest
	// Unlinked: a leader account without a team.
	Unlinked bool
}

type delegationContest struct {
	Name, Title string
	People      []*delegationPerson
}

type delegationPerson struct {
	Username, Name string
	Subs           []delegationSub
}

type delegationSub struct {
	ID             int64
	At             time.Time
	Task, Language string
	Result, Class  string
}

// leaderScoresVisible: the contest shows scores now (contest setting).
func leaderScoresVisible(visibility string, stop, now time.Time) bool {
	switch visibility {
	case "never":
		return false
	case "after":
		return !now.Before(stop)
	}
	return true
}

// leaderResult is a submission's result as its contestant sees it.
func leaderResult(tr func(string, ...any) string, invalidated *time.Time, scored bool, compilation, verdict *string,
	score *float64, precision int32, visible, icpc bool) (string, string) {
	switch {
	case invalidated != nil:
		return tr("invalidated"), "muted"
	case compilation != nil && *compilation == "fail":
		return tr("Compilation failed"), "bad"
	case !scored:
		return tr("being judged"), "muted"
	case !visible:
		return tr("scored"), ""
	case icpc && verdict != nil:
		if *verdict == scoring.VerdictAccepted {
			return *verdict, "ok"
		}
		return *verdict, "bad"
	case score != nil:
		return strconv.FormatFloat(*score, 'f', int(precision), 64), ""
	}
	return "", ""
}

// leaderTeam is the team whose contestants the viewer may see: a leader's
// own, or (for other roles) the one asked for.
func (s *Server) leaderTeam(r *http.Request, rc *reqCtx) (*sqlc.Team, error) {
	id := int64(0)
	if rc.admin.Role == "leader" {
		if rc.admin.TeamID == nil {
			return nil, nil
		}
		id = *rc.admin.TeamID
	} else if v, err := strconv.ParseInt(r.URL.Query().Get("team"), 10, 64); err == nil {
		id = v
	}
	if id == 0 {
		return nil, nil
	}
	t, err := s.q.GetTeam(r.Context(), id)
	if isNotFound(err) {
		return nil, nil
	}
	return &t, err
}

func (s *Server) handleDelegation(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	ctx := r.Context()
	d := &delegationPage{}
	team, err := s.leaderTeam(r, rc)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	if rc.admin.Role != "leader" {
		if d.Teams, err = s.q.ListTeams(ctx); err != nil {
			s.internalError(w, r, rc, err)
			return
		}
	} else if team == nil {
		d.Unlinked = true
	}
	d.Team = team
	if team != nil {
		parts, err := s.q.LeaderParticipations(ctx, team.ID)
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		ids := make([]int64, len(parts))
		people := map[int64]*delegationPerson{}
		type visibility struct {
			visible, icpc bool
		}
		vis := map[int64]visibility{}
		byContest := map[int64]*delegationContest{}
		now := s.now()
		for i, p := range parts {
			ids[i] = p.ID
			c := byContest[p.ContestID]
			if c == nil {
				c = &delegationContest{Name: p.ContestName, Title: p.ContestTitle}
				byContest[p.ContestID] = c
				d.Contests = append(d.Contests, c)
			}
			person := &delegationPerson{Username: p.Username, Name: fullName(p.FirstName, p.LastName)}
			c.People = append(c.People, person)
			people[p.ID] = person
			vis[p.ID] = visibility{leaderScoresVisible(p.ScoreVisibility, p.StopTime, now), p.ScoringMode == "icpc"}
		}
		subs, err := s.q.LeaderSubmissions(ctx, sqlc.LeaderSubmissionsParams{ParticipationIds: ids, MaxRows: 2000})
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		tr := adminTr(r)
		for _, x := range subs {
			if x.ParticipationID == nil || people[*x.ParticipationID] == nil {
				continue
			}
			v := vis[*x.ParticipationID]
			sv := delegationSub{ID: x.ID, At: x.SubmittedAt, Task: x.TaskName}
			if x.Language != nil {
				sv.Language = *x.Language
			}
			sv.Result, sv.Class = leaderResult(tr, x.InvalidatedAt, x.Scored, x.CompilationOutcome, x.Verdict, x.PublicScore,
				x.ScorePrecision, v.visible, v.icpc)
			p := people[*x.ParticipationID]
			p.Subs = append(p.Subs, sv)
		}
	}
	s.render(w, "delegation", http.StatusOK, s.newPage(w, r, rc, "My delegation", "delegation", d))
}

type delegationSubmissionPage struct {
	S      sqlc.LeaderSubmissionRow
	Files  []fileView
	Result string
	Class  string
	Team   *sqlc.Team
}

func (s *Server) handleDelegationSubmission(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	team, err := s.leaderTeam(r, rc)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	id, _ := pathID(r, "id")
	if team == nil {
		s.notFound(w, r, rc)
		return
	}
	sub, err := s.q.LeaderSubmission(r.Context(), sqlc.LeaderSubmissionParams{ID: id, TeamID: team.ID})
	if isNotFound(err) {
		s.notFound(w, r, rc)
		return
	}
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	d := &delegationSubmissionPage{S: sub, Team: team}
	if d.Files, err = s.filesOf(r, sub.ID, sub.Language); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	d.Result, d.Class = leaderResult(adminTr(r), nil, sub.Scored, sub.CompilationOutcome, sub.Verdict, sub.PublicScore, sub.ScorePrecision,
		leaderScoresVisible(sub.ScoreVisibility, sub.StopTime, s.now()), sub.ScoringMode == "icpc")
	s.render(w, "delegation_submission", http.StatusOK, s.newPage(w, r, rc, "Submission", "delegation", d))
}

// fullName joins first and last names.
func fullName(first, last string) string {
	switch {
	case first == "":
		return last
	case last == "":
		return first
	}
	return first + " " + last
}
