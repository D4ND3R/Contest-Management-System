package dispatcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/events"
	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/D4ND3R/Contest-Management-System/internal/scoring"
	"github.com/jackc/pgx/v5"
)

type resultT = jobs.Result

// effects are applied after a transaction commits.
type effects struct {
	jobs    []queue.Item
	events  []events.Event
	ranking []queue.RankingUpdate
}

func (d *Dispatcher) apply(ctx context.Context, e *effects) {
	if err := d.q.EnqueueMany(ctx, e.jobs); err != nil {
		// The sweeper re-enqueues after StaleAfter.
		d.log.Error("enqueue jobs", "error", err, "count", len(e.jobs))
	}
	for _, ev := range e.events {
		if err := events.Publish(ctx, d.rdb, d.opts.Namespace, ev); err != nil {
			d.log.Warn("publish event", "error", err)
		}
	}
	if err := d.q.PushRanking(ctx, e.ranking...); err != nil {
		d.log.Error("push ranking updates", "error", err)
	}
}

// newJobID returns a unique, readable job id.
func newJobID(kind jobs.Kind, target, dataset int64, gen int32, extra string) string {
	return fmt.Sprintf("%s-%d-%d-g%d%s-%d", kind, target, dataset, gen, extra, time.Now().UnixNano())
}

// resolveFiles maps stored submission file names ("sum.%l") to the names
// the language expects ("sum.cpp").
func resolveFiles(files []sqlc.SubmissionFile, ext string) []jobs.File {
	out := make([]jobs.File, len(files))
	for i, f := range files {
		name := f.Filename
		if ext != "" {
			name = strings.ReplaceAll(name, ".%l", ext)
		}
		out[i] = jobs.File{Name: name, Digest: f.Digest}
	}
	return out
}

// submissionCtx bundles the data needed to build jobs for a submission.
type submissionCtx struct {
	meta  sqlc.GetSubmissionMetaRow
	di    *datasetInfo
	files []jobs.File
	job   jobs.Job // template: target, task type, language, files, managers, limits
}

func (d *Dispatcher) loadSubmission(ctx context.Context, q *sqlc.Queries, subID, dsID int64) (*submissionCtx, error) {
	meta, err := q.GetSubmissionMeta(ctx, subID)
	if err != nil {
		return nil, wrap("submission", err)
	}
	di, err := d.datasets.get(ctx, q, dsID)
	if err != nil {
		return nil, err
	}
	files, err := q.ListSubmissionFiles(ctx, subID)
	if err != nil {
		return nil, err
	}
	sc := &submissionCtx{meta: meta, di: di}
	ext := ""
	if meta.Language != nil {
		l, ok := d.langs.Get(*meta.Language)
		if !ok {
			return nil, fmt.Errorf("language %q is not configured", *meta.Language)
		}
		sc.job.Language = l
		ext = l.SourceExtension()
	}
	sc.files = resolveFiles(files, ext)
	sc.job = jobs.Job{
		SubmissionID: subID, DatasetID: dsID, TaskType: di.ds.TaskType, TaskTypeParams: di.ds.TaskTypeParams,
		Language: sc.job.Language, Files: sc.files, Managers: di.managers, Limits: di.limits(),
	}
	return sc, nil
}

// priority picks the queue: live datasets use the normal priorities unless
// the work is a rejudge of an old submission.
func (sc *submissionCtx) priority(normal queue.Priority, rejudge bool) queue.Priority {
	if sc.meta.Tester {
		// Administrators' task tester runs: after contestants' submissions.
		return queue.PriorityUserTest
	}
	if !sc.di.live() || rejudge {
		return queue.PriorityBackground
	}
	return normal
}

func (sc *submissionCtx) event(status string) events.Event {
	if sc.meta.Tester {
		// Only administrators listen to events without a contest.
		return events.Event{Type: events.TypeSubmission, TaskID: sc.meta.TaskID, SubmissionID: sc.meta.ID, Status: status}
	}
	return events.Event{Type: events.TypeSubmission, ContestID: sc.meta.ContestID, ParticipationID: sc.meta.ParticipationID,
		TaskID: sc.meta.TaskID, SubmissionID: sc.meta.ID, Status: status}
}

func (sc *submissionCtx) compileJob(gen int32, attempt int, prio queue.Priority) queue.Item {
	j := sc.job
	j.ID = newJobID(jobs.KindCompile, sc.meta.ID, sc.di.ds.ID, gen, "")
	j.Kind, j.Generation, j.Attempt, j.Priority = jobs.KindCompile, gen, attempt, int(prio)
	return queue.Item{Priority: prio, Job: &j}
}

func (sc *submissionCtx) evaluateJobs(gen int32, attempt int, prio queue.Priority, executables []jobs.File, ids []int64, perJob int) []queue.Item {
	tcs := sc.di.jobTestcases(ids)
	var out []queue.Item
	for i := 0; i < len(tcs); i += perJob {
		end := min(i+perJob, len(tcs))
		j := sc.job
		j.ID = newJobID(jobs.KindEvaluate, sc.meta.ID, sc.di.ds.ID, gen, "-"+tcs[i].Codename)
		j.Kind, j.Generation, j.Attempt, j.Priority = jobs.KindEvaluate, gen, attempt, int(prio)
		j.Executables, j.Testcases = executables, tcs[i:end]
		out = append(out, queue.Item{Priority: prio, Job: &j})
	}
	return out
}

// newSubmission creates the results of a new submission and starts judging.
func (d *Dispatcher) newSubmission(ctx context.Context, subID int64) error {
	q := sqlc.New(d.pool)
	meta, err := q.GetSubmissionMeta(ctx, subID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // deleted meanwhile
	}
	if err != nil {
		return err
	}
	var dss []sqlc.Dataset
	if meta.Tester {
		dss, err = q.ListDatasetsByTask(ctx, meta.TaskID) // tester runs: every dataset
	} else {
		dss, err = q.ListJudgedDatasetsByTask(ctx, meta.TaskID)
	}
	if err != nil {
		return err
	}
	for _, ds := range dss {
		if err := d.advance(ctx, subID, ds.ID, false); err != nil {
			return err
		}
	}
	return nil
}

// advance makes sure the result of (submission, dataset) progresses:
// it creates the result if needed and enqueues whatever stage is missing
// (compilation, the testcases not evaluated yet, or scoring).
func (d *Dispatcher) advance(ctx context.Context, subID, dsID int64, rejudge bool) error {
	var eff effects
	err := db.InTx(ctx, d.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		if err := q.EnsureSubmissionResult(ctx, sqlc.EnsureSubmissionResultParams{SubmissionID: subID, DatasetID: dsID}); err != nil {
			return err
		}
		st, err := q.LockSubmissionResult(ctx, sqlc.LockSubmissionResultParams{SubmissionID: subID, DatasetID: dsID})
		if err != nil {
			return err
		}
		if st.ScoredAt != nil || st.SystemError != nil {
			return nil
		}
		sc, err := d.loadSubmission(ctx, q, subID, dsID)
		if err != nil {
			return d.systemError(ctx, q, &eff, subID, dsID, nil, err.Error())
		}
		switch {
		case st.CompilationOutcome == nil:
			eff.jobs = append(eff.jobs, sc.compileJob(st.Generation, 0, sc.priority(queue.PriorityCompile, rejudge)))
			eff.events = append(eff.events, sc.event("compiling"))
		case *st.CompilationOutcome == "fail":
			return d.scoreCompilationFailure(ctx, q, &eff, sc)
		case st.EvaluationOutcome == nil:
			done, err := q.ListEvaluatedTestcaseIDs(ctx, sqlc.ListEvaluatedTestcaseIDsParams{SubmissionID: subID, DatasetID: dsID})
			if err != nil {
				return err
			}
			have := make(map[int64]bool, len(done))
			for _, id := range done {
				have[id] = true
			}
			var missing []int64
			for _, tc := range sc.di.testcases {
				if !have[tc.ID] {
					missing = append(missing, tc.ID)
				}
			}
			if len(missing) == 0 {
				if err := q.SetEvaluationDone(ctx, sqlc.SetEvaluationDoneParams{SubmissionID: subID, DatasetID: dsID, Generation: st.Generation}); err != nil {
					return err
				}
				return d.scoreResult(ctx, q, &eff, sc)
			}
			exes, err := q.ListExecutables(ctx, sqlc.ListExecutablesParams{SubmissionID: subID, DatasetID: dsID})
			if err != nil {
				return err
			}
			var files []jobs.File
			for _, e := range exes {
				files = append(files, jobs.File{Name: e.Filename, Digest: e.Digest})
			}
			if len(files) == 0 && sc.job.Language != nil {
				// Executables vanished (e.g. partial invalidation): recompile.
				if _, err := q.InvalidateSubmissionResult(ctx, sqlc.InvalidateSubmissionResultParams{SubmissionID: subID, DatasetID: dsID, Level: "compilation"}); err != nil {
					return err
				}
				return nil // picked up by the next sweep
			}
			eff.jobs = append(eff.jobs, sc.evaluateJobs(st.Generation, 0, sc.priority(queue.PriorityEvaluate, rejudge), files, missing, d.opts.TestcasesPerJob)...)
		default:
			// Evaluated but not scored (rescore).
			return d.scoreResult(ctx, q, &eff, sc)
		}
		return q.MarkJobsEnqueued(ctx, sqlc.MarkJobsEnqueuedParams{SubmissionID: subID, DatasetID: dsID})
	})
	if err != nil {
		return err
	}
	d.apply(ctx, &eff)
	return nil
}

// handleSubmissionResult applies a compile or evaluate result.
func (d *Dispatcher) handleSubmissionResult(ctx context.Context, r *resultT) error {
	var eff effects
	err := db.InTx(ctx, d.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		st, err := q.LockSubmissionResult(ctx, sqlc.LockSubmissionResultParams{SubmissionID: r.SubmissionID, DatasetID: r.DatasetID})
		if errors.Is(err, pgx.ErrNoRows) {
			return errStale // submission or dataset deleted
		}
		if err != nil {
			return err
		}
		if st.Generation != r.Generation || st.ScoredAt != nil || st.SystemError != nil {
			return errStale
		}
		sc, err := d.loadSubmission(ctx, q, r.SubmissionID, r.DatasetID)
		if err != nil {
			return d.systemError(ctx, q, &eff, r.SubmissionID, r.DatasetID, nil, err.Error())
		}
		if r.Error != "" {
			return d.retryOrFail(ctx, q, &eff, sc, st.Generation, r)
		}
		switch r.Kind {
		case jobs.KindCompile:
			if st.CompilationOutcome != nil || r.Compilation == nil {
				return errStale // duplicate delivery
			}
			return d.applyCompilation(ctx, q, &eff, sc, st.Generation, r)
		case jobs.KindEvaluate:
			if st.CompilationOutcome == nil || *st.CompilationOutcome != "ok" || st.EvaluationOutcome != nil {
				return errStale
			}
			return d.applyEvaluations(ctx, q, &eff, sc, st.Generation, r)
		}
		return fmt.Errorf("unexpected result kind %q", r.Kind)
	})
	if errors.Is(err, errStale) {
		return nil
	}
	if err != nil {
		return err
	}
	d.apply(ctx, &eff)
	return nil
}

func (d *Dispatcher) applyCompilation(ctx context.Context, q *sqlc.Queries, eff *effects, sc *submissionCtx, gen int32, r *resultT) error {
	c := r.Compilation
	outcome := "ok"
	if !c.Success {
		outcome = "fail"
	}
	worker := r.Worker
	if _, err := q.SetCompilationResult(ctx, sqlc.SetCompilationResultParams{
		SubmissionID: sc.meta.ID, DatasetID: sc.di.ds.ID, CompilationOutcome: &outcome, CompilationText: c.Text,
		CompilationStdout: c.Stdout, CompilationStderr: c.Stderr, CompilationTime: &c.Time, CompilationWallTime: &c.WallTime,
		CompilationMemory: &c.Memory, CompilationWorker: &worker, TestcasesTotal: int32(len(sc.di.testcases)), Generation: gen,
	}); err != nil {
		return wrap("set compilation", err)
	}
	if !c.Success {
		return d.scoreCompilationFailure(ctx, q, eff, sc)
	}
	for _, e := range c.Executables {
		if err := q.RegisterBlob(ctx, sqlc.RegisterBlobParams{Digest: e.Digest, Size: e.Size, Description: "executable"}); err != nil {
			return wrap("register executable", err)
		}
		if err := q.InsertExecutable(ctx, sqlc.InsertExecutableParams{SubmissionID: sc.meta.ID, DatasetID: sc.di.ds.ID, Filename: e.Name, Digest: e.Digest}); err != nil {
			return wrap("insert executable", err)
		}
	}
	ids := make([]int64, len(sc.di.testcases))
	for i, tc := range sc.di.testcases {
		ids[i] = tc.ID
	}
	if len(ids) == 0 {
		if err := q.SetEvaluationDone(ctx, sqlc.SetEvaluationDoneParams{SubmissionID: sc.meta.ID, DatasetID: sc.di.ds.ID, Generation: gen}); err != nil {
			return err
		}
		return d.scoreResult(ctx, q, eff, sc)
	}
	// Evaluations inherit the urgency of the compilation (rejudges and
	// non-live datasets stay in the background queue).
	prio := sc.priority(queue.PriorityEvaluate, queue.Priority(r.Priority) == queue.PriorityBackground)
	eff.jobs = append(eff.jobs, sc.evaluateJobs(gen, 0, prio, c.Executables, ids, d.opts.TestcasesPerJob)...)
	ev := sc.event("evaluating")
	ev.Total = int32(len(ids))
	eff.events = append(eff.events, ev)
	return q.MarkJobsEnqueued(ctx, sqlc.MarkJobsEnqueuedParams{SubmissionID: sc.meta.ID, DatasetID: sc.di.ds.ID})
}

func (d *Dispatcher) applyEvaluations(ctx context.Context, q *sqlc.Queries, eff *effects, sc *submissionCtx, gen int32, r *resultT) error {
	for _, e := range r.Evaluations {
		if _, ok := sc.di.byID[e.TestcaseID]; !ok {
			continue // testcase deleted meanwhile
		}
		worker := r.Worker
		var code, sig *int32
		if e.ExitCode != 0 {
			v := int32(e.ExitCode)
			code = &v
		}
		if e.Signal != 0 {
			v := int32(e.Signal)
			sig = &v
		}
		if err := q.UpsertEvaluation(ctx, sqlc.UpsertEvaluationParams{
			SubmissionID: sc.meta.ID, DatasetID: sc.di.ds.ID, TestcaseID: e.TestcaseID, Outcome: e.Outcome, Text: e.Text,
			ExecutionTime: &e.Time, ExecutionWallTime: &e.WallTime, ExecutionMemory: &e.Memory, ExitStatus: e.ExitStatus,
			ExitCode: code, Signal: sig, Worker: &worker,
		}); err != nil {
			return wrap("upsert evaluation", err)
		}
	}
	prog, err := q.RefreshTestcasesDone(ctx, sqlc.RefreshTestcasesDoneParams{SubmissionID: sc.meta.ID, DatasetID: sc.di.ds.ID})
	if err != nil {
		return err
	}
	if prog.TestcasesDone < prog.TestcasesTotal {
		ev := sc.event("evaluating")
		ev.Done, ev.Total = prog.TestcasesDone, prog.TestcasesTotal
		eff.events = append(eff.events, ev)
		return nil
	}
	if err := q.SetEvaluationDone(ctx, sqlc.SetEvaluationDoneParams{SubmissionID: sc.meta.ID, DatasetID: sc.di.ds.ID, Generation: gen}); err != nil {
		return err
	}
	return d.scoreResult(ctx, q, eff, sc)
}

// retryOrFail re-enqueues a failed job or, after MaxAttempts, marks the
// submission as a system error and alerts the administrators.
func (d *Dispatcher) retryOrFail(ctx context.Context, q *sqlc.Queries, eff *effects, sc *submissionCtx, gen int32, r *resultT) error {
	attempt := r.Attempt + 1
	if attempt >= d.opts.MaxAttempts {
		return d.systemError(ctx, q, eff, sc.meta.ID, sc.di.ds.ID, sc,
			fmt.Sprintf("%s job failed %d times (last worker %s): %s", r.Kind, attempt, r.Worker, r.Error))
	}
	d.log.Warn("retrying job", "kind", r.Kind, "submission", sc.meta.ID, "dataset", sc.di.ds.ID, "attempt", attempt, "error", r.Error)
	rejudge := queue.Priority(r.Priority) == queue.PriorityBackground
	switch r.Kind {
	case jobs.KindCompile:
		eff.jobs = append(eff.jobs, sc.compileJob(gen, attempt, sc.priority(queue.PriorityCompile, rejudge)))
	case jobs.KindEvaluate:
		exes, err := q.ListExecutables(ctx, sqlc.ListExecutablesParams{SubmissionID: sc.meta.ID, DatasetID: sc.di.ds.ID})
		if err != nil {
			return err
		}
		var files []jobs.File
		for _, e := range exes {
			files = append(files, jobs.File{Name: e.Filename, Digest: e.Digest})
		}
		eff.jobs = append(eff.jobs, sc.evaluateJobs(gen, attempt, sc.priority(queue.PriorityEvaluate, rejudge), files, r.Testcases, d.opts.TestcasesPerJob)...)
	}
	return q.MarkJobsEnqueued(ctx, sqlc.MarkJobsEnqueuedParams{SubmissionID: sc.meta.ID, DatasetID: sc.di.ds.ID})
}

func (d *Dispatcher) systemError(ctx context.Context, q *sqlc.Queries, eff *effects, subID, dsID int64, sc *submissionCtx, msg string) error {
	d.log.Error("submission system error", "submission", subID, "dataset", dsID, "error", msg)
	if err := q.SetSubmissionSystemError(ctx, sqlc.SetSubmissionSystemErrorParams{SubmissionID: subID, DatasetID: dsID, SystemError: &msg}); err != nil {
		return err
	}
	alert := events.Event{Type: events.TypeAlert, SubmissionID: subID, Text: msg}
	if sc != nil {
		alert.ContestID = sc.meta.ContestID
		ev := sc.event("error")
		eff.events = append(eff.events, ev)
	}
	eff.events = append(eff.events, alert)
	return nil
}

// ---------------------------------------------------------------- scoring

func (d *Dispatcher) scoreCompilationFailure(ctx context.Context, q *sqlc.Queries, eff *effects, sc *submissionCtx) error {
	if err := q.SetCompilationFailedScore(ctx, sqlc.SetCompilationFailedScoreParams{SubmissionID: sc.meta.ID, DatasetID: sc.di.ds.ID}); err != nil {
		return err
	}
	eff.events = append(eff.events, sc.event("compilation_failed"))
	return d.aggregateIfLive(ctx, q, eff, sc)
}

func (d *Dispatcher) scoreResult(ctx context.Context, q *sqlc.Queries, eff *effects, sc *submissionCtx) error {
	if sc.di.scoreTypeErr != nil {
		return d.systemError(ctx, q, eff, sc.meta.ID, sc.di.ds.ID, sc, "invalid score type parameters: "+sc.di.scoreTypeErr.Error())
	}
	evs, err := q.ListEvaluationsWithTestcase(ctx, sqlc.ListEvaluationsWithTestcaseParams{SubmissionID: sc.meta.ID, DatasetID: sc.di.ds.ID})
	if err != nil {
		return err
	}
	tcs := make([]scoring.Testcase, len(evs))
	for i, e := range evs {
		tcs[i] = scoring.Testcase{ID: e.TestcaseID, Codename: e.Codename, Public: e.Public, Evaluated: true,
			Outcome: e.Outcome, Text: e.Text, ExitStatus: e.ExitStatus}
		if e.ExecutionTime != nil {
			tcs[i].Time = *e.ExecutionTime
		}
		if e.ExecutionMemory != nil {
			tcs[i].Memory = *e.ExecutionMemory
		}
	}
	res := sc.di.scoreType.Compute(tcs)
	verdict := scoring.ICPCVerdict(res.Details, res.Score, sc.di.scoreType.MaxScore())
	details, _ := json.Marshal(res.Details)
	pdetails, _ := json.Marshal(res.PublicDetails)
	ranking, _ := json.Marshal(res.RankingDetails)
	if err := q.SetScore(ctx, sqlc.SetScoreParams{
		SubmissionID: sc.meta.ID, DatasetID: sc.di.ds.ID, Score: &res.Score, ScoreDetails: details,
		PublicScore: &res.PublicScore, PublicScoreDetails: pdetails, RankingScoreDetails: ranking, Verdict: &verdict,
	}); err != nil {
		return err
	}
	eff.events = append(eff.events, sc.event("scored"))
	return d.aggregateIfLive(ctx, q, eff, sc)
}

func (d *Dispatcher) aggregateIfLive(ctx context.Context, q *sqlc.Queries, eff *effects, sc *submissionCtx) error {
	if !sc.di.live() || sc.meta.Tester {
		return nil
	}
	up, err := d.aggregate(ctx, q, sc.meta.ParticipationID, sc.di)
	if err != nil {
		return err
	}
	up.Time = sc.meta.SubmittedAt
	eff.ranking = append(eff.ranking, *up)
	if up.ICPCSolved && up.ICPCSolvedAt != nil && up.ICPCSolvedAt.Equal(sc.meta.SubmittedAt) {
		// This submission solved the task: a balloon for the staff.
		eff.events = append(eff.events, events.Event{Type: events.TypeBalloon, ContestID: sc.meta.ContestID,
			ParticipationID: sc.meta.ParticipationID, TaskID: sc.meta.TaskID, SubmissionID: sc.meta.ID})
	}
	return nil
}

// aggregate recomputes the task score of a participation on the task of di
// (which must be the live dataset) and stores it.
func (d *Dispatcher) aggregate(ctx context.Context, q *sqlc.Queries, participationID int64, di *datasetInfo) (*queue.RankingUpdate, error) {
	rows, err := q.ListTaskSubmissionsForScore(ctx, sqlc.ListTaskSubmissionsForScoreParams{
		DatasetID: di.ds.ID, ParticipationID: participationID, TaskID: di.task.ID,
	})
	if err != nil {
		return nil, err
	}
	subs := make([]scoring.Submission, len(rows))
	for i, r := range rows {
		s := scoring.Submission{ID: r.ID, Time: r.SubmittedAt, Official: r.Official, Tokened: r.Tokened, Scored: r.ScoredAt != nil}
		if r.CompilationOutcome != nil && *r.CompilationOutcome == "fail" {
			s.CompileError = true
		}
		if r.Score != nil {
			s.Score = *r.Score
		}
		if len(r.RankingScoreDetails) > 0 {
			_ = json.Unmarshal(r.RankingScoreDetails, &s.Subtasks)
		}
		subs[i] = s
	}
	ts := scoring.Aggregate(di.task.ScoreMode, subs, int(di.task.ScorePrecision))
	maxScore := 0.0
	if di.scoreType != nil {
		maxScore = di.scoreType.MaxScore()
	}
	icpc := scoring.ICPC(subs, maxScore)
	subtasks := ts.Subtasks
	if subtasks == nil {
		subtasks = []float64{}
	}
	st, _ := json.Marshal(subtasks)
	if err := q.UpsertParticipationTaskScore(ctx, sqlc.UpsertParticipationTaskScoreParams{
		ParticipationID: participationID, TaskID: di.task.ID, Score: ts.Score, SubtaskScores: st,
		IcpcSolved: icpc.Solved, IcpcAttempts: int32(icpc.Attempts), IcpcSolvedAt: icpc.SolvedAt,
		Pending: int32(ts.Pending), LastSubmissionAt: ts.LastSubmission,
	}); err != nil {
		return nil, err
	}
	var contestID int64
	if di.task.ContestID != nil {
		contestID = *di.task.ContestID
	}
	return &queue.RankingUpdate{
		ContestID: contestID, ParticipationID: participationID, TaskID: di.task.ID, Score: ts.Score,
		Subtasks: subtasks, Pending: ts.Pending, ICPCSolved: icpc.Solved, ICPCAttempts: icpc.Attempts, ICPCSolvedAt: icpc.SolvedAt,
		Time: time.Now().UTC(),
	}, nil
}

// reaggregateTask recomputes every participation's score on a task (after
// the live dataset changed).
func (d *Dispatcher) reaggregateTask(ctx context.Context, taskID int64) error {
	q := sqlc.New(d.pool)
	task, err := q.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if task.ActiveDatasetID == nil {
		return nil
	}
	di, err := d.datasets.get(ctx, q, *task.ActiveDatasetID)
	if err != nil {
		return err
	}
	parts, err := q.ListParticipationsWithSubmissions(ctx, taskID)
	if err != nil {
		return err
	}
	var eff effects
	for _, p := range parts {
		up, err := d.aggregate(ctx, q, p, di)
		if err != nil {
			return err
		}
		eff.ranking = append(eff.ranking, *up)
	}
	d.apply(ctx, &eff)
	return nil
}

// reaggregateSubmission recomputes the task score of a submission's
// participation (after the submission was invalidated or restored).
func (d *Dispatcher) reaggregateSubmission(ctx context.Context, submissionID int64) error {
	q := sqlc.New(d.pool)
	s, err := q.GetSubmission(ctx, submissionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // deleted meanwhile
	}
	if err != nil || s.ParticipationID == nil {
		return err
	}
	task, err := q.GetTask(ctx, s.TaskID)
	if err != nil || task.ActiveDatasetID == nil {
		return err
	}
	di, err := d.datasets.get(ctx, q, *task.ActiveDatasetID)
	if err != nil {
		return err
	}
	up, err := d.aggregate(ctx, q, *s.ParticipationID, di)
	if err != nil {
		return err
	}
	d.apply(ctx, &effects{ranking: []queue.RankingUpdate{*up}})
	return nil
}
