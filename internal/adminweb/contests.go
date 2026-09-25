package adminweb

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/contestweb"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/i18n"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/jackc/pgx/v5"
)

// contestForm is the data of the contest edit page.
type contestForm struct {
	C             sqlc.UpdateContestParams
	New           bool
	Languages     []*langs.Language
	Localizations []localization
	Tasks         []sqlc.Task
	Unassigned    []sqlc.Task
	Counts        *sqlc.AdminContestCountsRow
}

type localization struct{ Code, Name string }

func (s *Server) contestForm(ctx context.Context, c sqlc.UpdateContestParams, isNew bool) (*contestForm, error) {
	d := &contestForm{C: c, New: isNew, Languages: s.langs.All()}
	names := i18n.Names
	for _, code := range i18n.Languages() {
		d.Localizations = append(d.Localizations, localization{code, names[code]})
	}
	if isNew {
		return d, nil
	}
	var err error
	if d.Tasks, err = s.q.ListTasksByContest(ctx, &c.ID); err != nil {
		return nil, err
	}
	if d.Unassigned, err = s.q.AdminUnassignedTasks(ctx); err != nil {
		return nil, err
	}
	counts, err := s.q.AdminContestCounts(ctx)
	if err != nil {
		return nil, err
	}
	for i := range counts {
		if counts[i].ID == c.ID {
			d.Counts = &counts[i]
		}
	}
	return d, nil
}

type contestListItem struct {
	sqlc.Contest
	Participations, Tasks, Submissions int64
	Phase                              string
}

func (s *Server) handleContests(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	list, err := s.q.ListContests(r.Context())
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	counts, err := s.q.AdminContestCounts(r.Context())
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	byID := map[int64]sqlc.AdminContestCountsRow{}
	for _, c := range counts {
		byID[c.ID] = c
	}
	now := s.now()
	items := make([]contestListItem, 0, len(list))
	for _, c := range list {
		it := contestListItem{Contest: c, Participations: byID[c.ID].Participations, Tasks: byID[c.ID].Tasks,
			Submissions: byID[c.ID].Submissions}
		switch {
		case now.Before(c.StartTime):
			it.Phase = "upcoming"
		case now.Before(c.StopTime):
			it.Phase = "running"
		default:
			it.Phase = "finished"
		}
		items = append(items, it)
	}
	s.render(w, "contests", http.StatusOK, s.newPage(w, r, rc, "Contests", "contests", items))
}

func (s *Server) handleContestNew(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	now := s.now().Truncate(time.Hour).Add(time.Hour)
	c := db.NewContestUpdate()
	c.StartTime, c.StopTime = now, now.Add(5*time.Hour)
	// New contests start hidden from contestants until published.
	c.Status = "draft"
	d, _ := s.contestForm(r.Context(), c, true)
	s.render(w, "contest", http.StatusOK, s.newPage(w, r, rc, "New contest", "contests", d).crumb("Contests", "/contests"))
}

// tokenFields are the token rules shared by contests and tasks.
type tokenFields struct {
	Mode                  string
	MaxNumber             *int32
	MinIntervalS          int64
	GenInitial, GenNumber int32
	GenIntervalS          int64
	GenMax                *int32
}

func parseTokens(f *form) tokenFields {
	t := tokenFields{
		Mode:         f.oneOf("token_mode", "Token mode", "disabled", "finite", "infinite"),
		MaxNumber:    f.optInt32("token_max_number", "Maximum tokens"),
		MinIntervalS: f.nonNeg("token_min_interval_s", "Minimum interval between tokens", 0),
		GenInitial:   f.int32("token_gen_initial", "Initial tokens", 2),
		GenNumber:    f.int32("token_gen_number", "Tokens generated", 2),
		GenIntervalS: f.int64("token_gen_interval_s", "Generation interval", 1800),
		GenMax:       f.optInt32("token_gen_max", "Maximum accumulated tokens"),
	}
	if t.GenInitial < 0 || t.GenNumber < 0 {
		f.fail("token counts must not be negative")
	}
	if t.GenIntervalS <= 0 {
		f.fail("the token generation interval must be positive")
	}
	return t
}

func (s *Server) parseContest(f *form, c sqlc.UpdateContestParams) sqlc.UpdateContestParams {
	c.Name = f.identifier("name", "Name")
	if contains(contestweb.ReservedNames, c.Name) {
		f.fail("%q is reserved", c.Name)
	}
	c.Description = f.str("description")
	if f.str("status") != "" {
		c.Status = f.oneOf("status", "Status", "draft", "published", "archived")
	}
	c.Timezone = f.timezone("timezone")
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		loc = time.UTC
	}
	c.StartTime = f.time("start_time", "Start", loc)
	c.StopTime = f.time("stop_time", "End", loc)
	if c.StopTime.Before(c.StartTime) {
		f.fail("the contest ends before it starts")
	}
	c.Languages = f.multi("languages")
	for _, l := range c.Languages {
		if _, ok := s.langs.Get(l); !ok {
			f.fail("unknown language %q", l)
		}
	}
	c.AllowedLocalizations = f.multi("localizations")
	if m := f.optPositive64("per_user_time_min", "Per-user time"); m != nil {
		secs := *m * 60
		c.PerUserTimeS = &secs
	} else {
		c.PerUserTimeS = nil
	}
	c.AnalysisEnabled = f.check("analysis_enabled")
	c.AnalysisStart = f.optTime("analysis_start", "Analysis start", loc)
	c.AnalysisStop = f.optTime("analysis_stop", "Analysis end", loc)
	if c.AnalysisEnabled && (c.AnalysisStart == nil || c.AnalysisStop == nil) {
		f.fail("analysis mode needs a start and an end")
	}
	if c.AnalysisStart != nil && c.AnalysisStop != nil && c.AnalysisStop.Before(*c.AnalysisStart) {
		f.fail("the analysis ends before it starts")
	}
	c.PracticeEnabled = f.check("practice_enabled")
	c.SubmissionsDownloadAllowed = f.check("submissions_download_allowed")
	c.AllowQuestions = f.check("allow_questions")
	if f.str("questions_per_minute") != "" {
		c.QuestionsPerMinute = f.int32("questions_per_minute", "Questions per minute", 3)
		if c.QuestionsPerMinute < 0 {
			f.fail("%s must not be negative", "Questions per minute")
		}
	}
	c.AllowUserTests = f.check("allow_user_tests")
	c.AllowPrinting = f.check("allow_printing")
	c.BlockHiddenParticipations = f.check("block_hidden_participations")
	c.AllowPasswordAuthentication = f.check("allow_password_authentication")
	c.IpRestriction = f.check("ip_restriction")
	c.IpAutologin = f.check("ip_autologin")
	c.SingleLogin = f.check("single_login")
	t := parseTokens(f)
	c.TokenMode, c.TokenMaxNumber, c.TokenMinIntervalS = t.Mode, t.MaxNumber, t.MinIntervalS
	c.TokenGenInitial, c.TokenGenNumber, c.TokenGenIntervalS, c.TokenGenMax = t.GenInitial, t.GenNumber, t.GenIntervalS, t.GenMax
	c.MaxSubmissionNumber = f.optInt32("max_submission_number", "Maximum submissions")
	c.MaxUserTestNumber = f.optInt32("max_user_test_number", "Maximum user tests")
	c.MinSubmissionIntervalS = f.optInt64("min_submission_interval_s", "Minimum interval between submissions")
	c.MinUserTestIntervalS = f.optInt64("min_user_test_interval_s", "Minimum interval between user tests")
	c.ScorePrecision = f.int32("score_precision", "Score precision", 0)
	if c.ScorePrecision < 0 || c.ScorePrecision > 6 {
		f.fail("score precision must be between 0 and 6")
	}
	c.ScoringMode = f.oneOf("scoring_mode", "Scoring mode", "ioi", "icpc")
	c.IcpcPenaltyMinutes = f.int32("icpc_penalty_minutes", "ICPC penalty", 20)
	if c.IcpcPenaltyMinutes < 0 {
		f.fail("the ICPC penalty must not be negative")
	}
	c.RankingFreezeTime = f.optTime("ranking_freeze_time", "Ranking freeze", loc)
	if f.str("ranking_visibility") != "" {
		c.RankingVisibility = f.oneOf("ranking_visibility", "Ranking visibility", "public", "contestants", "admins", "hidden")
		c.RankingContestantView = f.oneOf("ranking_contestant_view", "What contestants see", "full", "own", "none")
		c.RankingWhen = f.oneOf("ranking_when", "When the ranking is shown", "always", "after")
		c.RankingFreezeMinutes = f.int32("ranking_freeze_minutes", "Freeze minutes", 0)
		if c.RankingFreezeMinutes < 0 {
			f.fail("%s must not be negative", "Freeze minutes")
		}
		c.RankingShowSubtasks = f.check("ranking_show_subtasks")
		c.RankingShowFlags = f.check("ranking_show_flags")
		c.RankingShowInstitutions = f.check("ranking_show_institutions")
		c.RankingShowHidden = f.check("ranking_show_hidden")
		c.RankingAnonymous = f.check("ranking_anonymous")
	}
	c.MaxPrintJobs = f.int32("max_print_jobs", "Maximum print jobs", 10)
	c.MaxPrintPages = f.int32("max_print_pages", "Maximum pages per job", 20)
	if c.MaxPrintJobs < 0 || c.MaxPrintPages < 0 {
		f.fail("printing limits must not be negative")
	}
	return c
}

func (s *Server) handleContestCreate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	f := newForm(r)
	c := s.parseContest(f, db.NewContestUpdate())
	if f.err == nil {
		if _, err := s.q.GetContestByName(r.Context(), c.Name); err == nil {
			f.fail("a contest named %q already exists", c.Name)
		}
	}
	if f.err != nil {
		d, _ := s.contestForm(r.Context(), c, true)
		s.formError(w, r, rc, "contest", s.newPage(w, r, rc, "New contest", "contests", d), f.err.Error())
		return
	}
	var id int64
	err := db.InTx(r.Context(), s.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		created, err := q.CreateContest(r.Context(), db.NewContestParams(c.Name, c.StartTime, c.StopTime))
		if err != nil {
			return err
		}
		c.ID, id = created.ID, created.ID
		_, err = q.UpdateContest(r.Context(), c)
		return err
	})
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("contest", id)
	s.done(w, r, "/contests/"+strconv.FormatInt(id, 10), "Contest created.")
}

func (s *Server) handleContest(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, _ := pathID(r, "id")
	c, err := s.q.GetContest(r.Context(), id)
	if err != nil {
		s.notFound(w, r, rc)
		return
	}
	d, err := s.contestForm(r.Context(), db.ContestToUpdate(c), false)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	s.render(w, "contest", http.StatusOK, s.newPage(w, r, rc, c.Name, "contests", d).crumb("Contests", "/contests"))
}

func (s *Server) handleContestUpdate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, _ := pathID(r, "id")
	old, err := s.q.GetContest(r.Context(), id)
	if err != nil {
		s.notFound(w, r, rc)
		return
	}
	f := newForm(r)
	c := s.parseContest(f, db.ContestToUpdate(old))
	if f.err == nil && c.Name != old.Name {
		if _, err := s.q.GetContestByName(r.Context(), c.Name); err == nil {
			f.fail("a contest named %q already exists", c.Name)
		}
	}
	if f.err != nil {
		d, _ := s.contestForm(r.Context(), c, false)
		s.formError(w, r, rc, "contest", s.newPage(w, r, rc, old.Name, "contests", d).crumb("Contests", "/contests"), f.err.Error())
		return
	}
	if _, err := s.q.UpdateContest(r.Context(), c); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("contest", id)
	s.contestChanged(r.Context(), id, 0)
	s.done(w, r, "/contests/"+strconv.FormatInt(id, 10), "Contest saved.")
}

func (s *Server) handleContestDelete(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, _ := pathID(r, "id")
	c, err := s.q.GetContest(r.Context(), id)
	if err != nil {
		s.notFound(w, r, rc)
		return
	}
	if r.FormValue("confirm") != c.Name {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Type the contest name to confirm the deletion.")
		return
	}
	if err := s.q.DeleteContest(r.Context(), id); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("contest", id)
	rc.note("name", c.Name)
	s.contestChanged(r.Context(), id, 0)
	s.done(w, r, "/contests", "Contest "+c.Name+" deleted.")
}

func (s *Server) handleContestAddTask(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, _ := pathID(r, "id")
	taskID, err := strconv.ParseInt(r.FormValue("task_id"), 10, 64)
	if err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Choose a task.")
		return
	}
	t, err := s.q.GetTask(r.Context(), taskID)
	if err != nil {
		s.notFound(w, r, rc)
		return
	}
	if t.ContestID != nil {
		s.errorPage(w, r, rc, http.StatusConflict, "The task already belongs to a contest.")
		return
	}
	next, err := s.q.AdminNextTaskNum(r.Context(), &id)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	if err := s.q.SetTaskContest(r.Context(), sqlc.SetTaskContestParams{ID: taskID, ContestID: &id, Num: &next}); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("contest", id)
	s.contestChanged(r.Context(), id, 0)
	s.done(w, r, "/contests/"+strconv.FormatInt(id, 10), "Task "+t.Name+" added.")
}

func (s *Server) handleContestMoveTask(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, _ := pathID(r, "id")
	taskID, _ := pathID(r, "task")
	tasks, err := s.q.ListTasksByContest(r.Context(), &id)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	i := -1
	for k, t := range tasks {
		if t.ID == taskID {
			i = k
		}
	}
	j := i - 1
	if r.FormValue("dir") == "down" {
		j = i + 1
	}
	if i < 0 || j < 0 || j >= len(tasks) {
		s.done(w, r, "/contests/"+strconv.FormatInt(id, 10), "")
		return
	}
	a, b := tasks[i], tasks[j]
	err = db.InTx(r.Context(), s.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		// (contest_id, num) is unique and checked per row: park a first.
		if err := q.SetTaskContest(r.Context(), sqlc.SetTaskContestParams{ID: a.ID, ContestID: &id}); err != nil {
			return err
		}
		if err := q.SetTaskContest(r.Context(), sqlc.SetTaskContestParams{ID: b.ID, ContestID: &id, Num: a.Num}); err != nil {
			return err
		}
		return q.SetTaskContest(r.Context(), sqlc.SetTaskContestParams{ID: a.ID, ContestID: &id, Num: b.Num})
	})
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("contest", id)
	s.contestChanged(r.Context(), id, 0)
	s.done(w, r, "/contests/"+strconv.FormatInt(id, 10), "")
}

func (s *Server) handleContestRemoveTask(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, _ := pathID(r, "id")
	taskID, _ := pathID(r, "task")
	t, err := s.q.GetTask(r.Context(), taskID)
	if err != nil || t.ContestID == nil || *t.ContestID != id {
		s.notFound(w, r, rc)
		return
	}
	if err := s.q.SetTaskContest(r.Context(), sqlc.SetTaskContestParams{ID: taskID}); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("contest", id)
	rc.note("task", t.Name)
	s.contestChanged(r.Context(), id, 0)
	s.done(w, r, "/contests/"+strconv.FormatInt(id, 10), "Task "+t.Name+" removed from the contest.")
}

// loadContest resolves {id} or renders 404.
func (s *Server) loadContest(w http.ResponseWriter, r *http.Request, rc *reqCtx) (sqlc.Contest, bool) {
	id, _ := pathID(r, "id")
	c, err := s.q.GetContest(r.Context(), id)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			s.internalError(w, r, rc, err)
		} else {
			s.notFound(w, r, rc)
		}
		return c, false
	}
	return c, true
}

// handleContestExtend moves the end of the contest (and, with per-user
// time, every contestant's window) by a number of minutes, live: open
// contest pages update their clocks.
func (s *Server) handleContestExtend(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	f := newForm(r)
	minutes := f.int32("minutes", "Minutes", 0)
	if f.err == nil && (minutes == 0 || minutes < -600 || minutes > 600) {
		f.fail("give a number of minutes between -600 and 600 (not 0)")
	}
	if f.err == nil && c.StopTime.Add(time.Duration(minutes)*time.Minute).Before(c.StartTime) {
		f.fail("the contest ends before it starts")
	}
	if f.err == nil && c.PerUserTimeS != nil && *c.PerUserTimeS+int64(minutes)*60 <= 0 {
		f.fail("the per-user time would not be positive")
	}
	if f.err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, f.err.Error())
		return
	}
	if err := s.q.ExtendContest(r.Context(), sqlc.ExtendContestParams{ID: c.ID, Minutes: minutes}); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("contest", c.ID)
	s.contestChanged(r.Context(), c.ID, 0)
	s.done(w, r, "/contests/"+strconv.FormatInt(c.ID, 10), "End of the contest moved by %d minutes; contestants' clocks are updated.", minutes)
}
