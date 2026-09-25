package contestweb

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/contest"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/events"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/D4ND3R/Contest-Management-System/internal/webkit"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// scoresVisible reports whether the contestant may see scores now.
func scoresVisible(rc *reqCtx) bool {
	switch rc.contest.ScoreVisibility {
	case "never":
		return false
	case "after":
		return !rc.now.Before(rc.contest.StopTime) && rc.status.Phase != contest.Running
	}
	return true
}

func tokenRules(mode string, max *int32, minInterval int64, genInitial, genNumber int32, genInterval int64, genMax *int32) contest.TokenRules {
	return contest.TokenRules{Mode: mode, MaxNumber: max, MinInterval: time.Duration(minInterval) * time.Second,
		GenInitial: genInitial, GenNumber: genNumber, GenInterval: time.Duration(genInterval) * time.Second, GenMax: genMax}
}

// tokenView is the contestant's token situation on a task: both the
// contest's pool and the task's must allow a play.
type tokenView struct {
	Unlimited bool
	Available int
	Next      time.Time
	Wait      time.Duration
	CanPlay   bool
}

// tokens computes the token view of a task (nil when tokens do not apply:
// disabled, outside the contest, or while scores are hidden).
func (s *Server) tokens(rc *reqCtx, t *taskView, played []sqlc.ListTokenTimesByParticipationRow) *tokenView {
	c := rc.contest
	if c.TokenMode == "disabled" || t.TokenMode == "disabled" || rc.status.Phase != contest.Running || !scoresVisible(rc) {
		return nil
	}
	var all, onTask []time.Time
	for _, p := range played {
		all = append(all, p.PlayedAt)
		if p.TaskID == t.ID {
			onTask = append(onTask, p.PlayedAt)
		}
	}
	start := rc.status.Begin
	cs := contest.Tokens(tokenRules(c.TokenMode, c.TokenMaxNumber, c.TokenMinIntervalS, c.TokenGenInitial, c.TokenGenNumber,
		c.TokenGenIntervalS, c.TokenGenMax), start, all, rc.now)
	ts := contest.Tokens(tokenRules(t.TokenMode, t.TokenMaxNumber, t.TokenMinIntervalS, t.TokenGenInitial, t.TokenGenNumber,
		t.TokenGenIntervalS, t.TokenGenMax), start, onTask, rc.now)
	v := &tokenView{CanPlay: cs.CanPlay() && ts.CanPlay(), Wait: max(cs.Wait, ts.Wait)}
	switch {
	case cs.Unlimited && ts.Unlimited:
		v.Unlimited = true
	case cs.Unlimited:
		v.Available, v.Next = ts.Available, ts.Next
	case ts.Unlimited:
		v.Available, v.Next = cs.Available, cs.Next
	default:
		v.Available = min(cs.Available, ts.Available)
		v.Next = cs.Next
		if cs.Available >= ts.Available {
			v.Next = ts.Next
		}
	}
	if cs.Exhausted || ts.Exhausted {
		v.Available, v.Unlimited, v.Next = 0, false, time.Time{}
	}
	return v
}

// errNoToken is returned when a play is not allowed.
var errNoToken = errors.New("no token")

// handleToken plays a token on one of the contestant's submissions: its
// full result becomes visible and, with "max of tokened and last" scoring,
// it counts.
func (s *Server) handleToken(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	sub, err := s.q.GetSubmission(r.Context(), id)
	if err != nil || sub.ParticipationID == nil || *sub.ParticipationID != rc.part.ID {
		http.NotFound(w, r)
		return
	}
	t := rc.contest.TaskByID[sub.TaskID]
	if t == nil {
		http.NotFound(w, r)
		return
	}
	back := "/" + rc.contest.Name + "/tasks/" + t.Name
	if !sub.Official || sub.InvalidatedAt != nil {
		s.errorPage(w, r, rc.contest, http.StatusConflict, "Token", "Tokens can only be played on official submissions.")
		return
	}
	err = db.InTx(r.Context(), s.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		if err := q.LockParticipation(r.Context(), rc.part.ID); err != nil {
			return err
		}
		played, err := q.ListTokenTimesByParticipation(r.Context(), rc.part.ID)
		if err != nil {
			return err
		}
		if v := s.tokens(rc, t, played); v == nil || !v.CanPlay {
			return errNoToken
		}
		_, err = q.CreateToken(r.Context(), sqlc.CreateTokenParams{SubmissionID: sub.ID, PlayedAt: rc.now})
		return err
	})
	var pe *pgconn.PgError
	switch {
	case errors.Is(err, errNoToken):
		s.errorPage(w, r, rc.contest, http.StatusConflict, "Token", "No token is available now.")
		return
	case errors.As(err, &pe) && pe.Code == "23505":
		s.errorPage(w, r, rc.contest, http.StatusConflict, "Token", "A token was already played on this submission.")
		return
	case err != nil:
		s.fail(w, err)
		return
	}
	// The task score may change ("max of tokened and last"); the row shows
	// the full result.
	if err := s.queue.Notify(r.Context(), queue.Event{Kind: queue.EventReaggregate, SubmissionID: sub.ID}); err != nil {
		s.log.Warn("notify dispatcher", "error", err)
	}
	_ = events.Publish(r.Context(), s.rdb, s.ns, events.Event{Type: events.TypeSubmission, ParticipationID: rc.part.ID,
		TaskID: t.ID, SubmissionID: sub.ID, Status: "scored"})
	s.log.Info("token played", "participation", rc.part.ID, "submission", sub.ID)
	webkit.Redirect(w, r, back)
}
