package db_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/testutil"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ctx = context.Background()

func digestOf(s string) string { return blob.Sum([]byte(s)) }

func must[T any](t *testing.T) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

// fixture builds a contest with one task, a live dataset with testcases and
// one participating user.
type fixture struct {
	pool    *pgxpool.Pool
	q       *sqlc.Queries
	contest sqlc.Contest
	task    sqlc.Task
	dataset sqlc.Dataset
	tcs     []sqlc.Testcase
	user    sqlc.User
	part    sqlc.Participation
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := testutil.DB(t)
	q := sqlc.New(pool)
	f := &fixture{pool: pool, q: q}
	now := time.Now().UTC().Truncate(time.Second)
	f.contest = must[sqlc.Contest](t)(q.CreateContest(ctx, db.NewContestParams("ioi", now.Add(-time.Hour), now.Add(4*time.Hour))))
	tp := db.NewTaskParams("sum", "Sum of two numbers")
	tp.ContestID, tp.Num = &f.contest.ID, ptr(int32(0))
	f.task = must[sqlc.Task](t)(q.CreateTask(ctx, tp))
	f.dataset = must[sqlc.Dataset](t)(q.CreateDataset(ctx, db.NewDatasetParams(f.task.ID, "v1")))
	if err := q.SetActiveDataset(ctx, sqlc.SetActiveDatasetParams{ID: f.task.ID, ActiveDatasetID: &f.dataset.ID}); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"000", "001", "002"} {
		tc := must[sqlc.Testcase](t)(q.UpsertTestcase(ctx, sqlc.UpsertTestcaseParams{
			DatasetID: f.dataset.ID, Codename: name, Public: i == 0,
			InputDigest: digestOf("in" + name), OutputDigest: digestOf("out" + name),
		}))
		f.tcs = append(f.tcs, tc)
	}
	f.user = must[sqlc.User](t)(q.CreateUser(ctx, sqlc.CreateUserParams{Username: "alice", PasswordHash: "plaintext:pw", PreferredLanguages: []string{"es"}}))
	f.part = must[sqlc.Participation](t)(q.CreateParticipation(ctx, sqlc.CreateParticipationParams{
		ContestID: f.contest.ID, UserID: f.user.ID, Ip: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")},
	}))
	return f
}

func ptr[T any](v T) *T { return &v }

func pgCode(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

func TestContestTaskDatasetLifecycle(t *testing.T) {
	f := newFixture(t)
	q := f.q

	got := must[sqlc.Contest](t)(q.GetContestByName(ctx, "ioi"))
	if got.ID != f.contest.ID || got.TokenMode != "disabled" || got.ScoringMode != "ioi" {
		t.Fatalf("contest = %+v", got)
	}
	up := db.ContestToUpdate(got)
	up.TokenMode, up.TokenMaxNumber, up.ScorePrecision = "finite", ptr(int32(5)), 2
	up.Languages = []string{"cpp17", "python3"}
	upd := must[sqlc.Contest](t)(q.UpdateContest(ctx, up))
	if upd.TokenMode != "finite" || *upd.TokenMaxNumber != 5 || len(upd.Languages) != 2 || !upd.UpdatedAt.After(got.CreatedAt.Add(-time.Second)) {
		t.Fatalf("update = %+v", upd)
	}

	// Invalid windows are rejected by CHECK constraints.
	bad := db.NewContestParams("bad", time.Now(), time.Now().Add(-time.Hour))
	if _, err := q.CreateContest(ctx, bad); pgCode(err) != "23514" {
		t.Fatalf("start > stop must violate a check constraint, got %v", err)
	}

	tasks := must[[]sqlc.Task](t)(q.ListTasksByContest(ctx, &f.contest.ID))
	if len(tasks) != 1 || tasks[0].ActiveDatasetID == nil || *tasks[0].ActiveDatasetID != f.dataset.ID {
		t.Fatalf("tasks = %+v", tasks)
	}

	// A second, autojudged dataset cloned from the first.
	dp := db.NewDatasetParams(f.task.ID, "v2-tighter")
	dp.Autojudge, dp.TimeLimitMs = true, ptr(int32(500))
	d2 := must[sqlc.Dataset](t)(q.CreateDataset(ctx, dp))
	must[sqlc.Manager](t)(q.UpsertManager(ctx, sqlc.UpsertManagerParams{DatasetID: f.dataset.ID, Filename: "checker", Digest: digestOf("checker")}))
	if err := q.CloneDatasetContents(ctx, sqlc.CloneDatasetContentsParams{Dst: d2.ID, Src: f.dataset.ID}); err != nil {
		t.Fatal(err)
	}
	if n := must[int64](t)(q.CountTestcases(ctx, d2.ID)); n != 3 {
		t.Fatalf("cloned testcases = %d", n)
	}
	if ms := must[[]sqlc.Manager](t)(q.ListManagers(ctx, d2.ID)); len(ms) != 1 || ms[0].Filename != "checker" {
		t.Fatalf("cloned managers = %+v", ms)
	}
	judged := must[[]sqlc.Dataset](t)(q.ListJudgedDatasetsByTask(ctx, f.task.ID))
	if len(judged) != 2 || judged[0].ID != f.dataset.ID {
		t.Fatalf("judged datasets = %+v (live first expected)", judged)
	}

	// Upserting a testcase updates it in place.
	tc := must[sqlc.Testcase](t)(q.UpsertTestcase(ctx, sqlc.UpsertTestcaseParams{
		DatasetID: f.dataset.ID, Codename: "001", Public: true, InputDigest: digestOf("x"), OutputDigest: digestOf("y"),
	}))
	if tc.ID != f.tcs[1].ID || !tc.Public || tc.InputDigest != digestOf("x") {
		t.Fatalf("upsert testcase = %+v", tc)
	}
	// Digest domain rejects malformed values.
	_, err := q.UpsertTestcase(ctx, sqlc.UpsertTestcaseParams{DatasetID: f.dataset.ID, Codename: "bad", InputDigest: "nope", OutputDigest: digestOf("y")})
	if pgCode(err) != "23514" {
		t.Fatalf("malformed digest must be rejected, got %v", err)
	}

	// Statements and attachments.
	must[sqlc.Statement](t)(q.UpsertStatement(ctx, sqlc.UpsertStatementParams{TaskID: f.task.ID, Language: "es", Digest: digestOf("pdf-es"), ContentType: "application/pdf"}))
	must[sqlc.Statement](t)(q.UpsertStatement(ctx, sqlc.UpsertStatementParams{TaskID: f.task.ID, Language: "es", Digest: digestOf("pdf-es-2"), ContentType: "application/pdf"}))
	sts := must[[]sqlc.Statement](t)(q.ListStatementsByContest(ctx, &f.contest.ID))
	if len(sts) != 1 || sts[0].Digest != digestOf("pdf-es-2") {
		t.Fatalf("statements = %+v", sts)
	}
	must[sqlc.Attachment](t)(q.UpsertAttachment(ctx, sqlc.UpsertAttachmentParams{TaskID: f.task.ID, Filename: "sample.zip", Digest: digestOf("zip")}))

	// Deleting the live dataset clears the task pointer (ON DELETE SET NULL).
	if err := q.DeleteDataset(ctx, f.dataset.ID); err != nil {
		t.Fatal(err)
	}
	task := must[sqlc.Task](t)(q.GetTask(ctx, f.task.ID))
	if task.ActiveDatasetID != nil {
		t.Fatalf("active dataset should be cleared, got %v", *task.ActiveDatasetID)
	}
	// Deleting the task cascades to datasets, statements and attachments.
	if err := q.DeleteTask(ctx, f.task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetDataset(ctx, d2.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("dataset should be gone: %v", err)
	}
}

func TestUsersAndParticipations(t *testing.T) {
	f := newFixture(t)
	q := f.q
	if _, err := q.CreateUser(ctx, sqlc.CreateUserParams{Username: "alice", PasswordHash: "x", PreferredLanguages: []string{}}); pgCode(err) != "23505" {
		t.Fatalf("duplicate username must violate uniqueness, got %v", err)
	}
	if _, err := q.CreateParticipation(ctx, sqlc.CreateParticipationParams{ContestID: f.contest.ID, UserID: f.user.ID, Ip: []netip.Prefix{}}); pgCode(err) != "23505" {
		t.Fatalf("duplicate participation must violate uniqueness, got %v", err)
	}
	cand := must[sqlc.GetLoginCandidateRow](t)(q.GetLoginCandidate(ctx, sqlc.GetLoginCandidateParams{ContestID: f.contest.ID, Username: "alice"}))
	if cand.ParticipationID != f.part.ID || cand.UserPasswordHash != "plaintext:pw" || cand.ParticipationPasswordHash != nil {
		t.Fatalf("login candidate = %+v", cand)
	}
	if len(cand.Ip) != 1 || cand.Ip[0].String() != "10.0.0.0/24" {
		t.Fatalf("ip roundtrip = %v", cand.Ip)
	}

	start := time.Now().UTC().Truncate(time.Microsecond)
	p1 := must[sqlc.Participation](t)(q.StartParticipation(ctx, sqlc.StartParticipationParams{ID: f.part.ID, StartingTime: &start}))
	later := start.Add(time.Hour)
	p2 := must[sqlc.Participation](t)(q.StartParticipation(ctx, sqlc.StartParticipationParams{ID: f.part.ID, StartingTime: &later}))
	if !p1.StartingTime.Equal(start) || !p2.StartingTime.Equal(start) {
		t.Fatalf("starting time must be set once: %v %v", p1.StartingTime, p2.StartingTime)
	}
	n1 := must[int64](t)(q.BumpLoginNonce(ctx, f.part.ID))
	n2 := must[int64](t)(q.BumpLoginNonce(ctx, f.part.ID))
	if n2 != n1+1 {
		t.Fatalf("nonce %d -> %d", n1, n2)
	}

	team := must[sqlc.Team](t)(q.CreateTeam(ctx, sqlc.CreateTeamParams{Code: "MEX", Name: "México", FlagDigest: ptr(digestOf("flag"))}))
	up := db.ParticipationToUpdate(p2)
	up.TeamID, up.Hidden, up.ExtraTimeS = &team.ID, true, 600
	must[sqlc.Participation](t)(q.UpdateParticipation(ctx, up))
	rows := must[[]sqlc.ListParticipationsByContestRow](t)(q.ListParticipationsByContest(ctx, f.contest.ID))
	if len(rows) != 1 || rows[0].TeamCode == nil || *rows[0].TeamCode != "MEX" || !rows[0].Participation.Hidden || rows[0].Participation.ExtraTimeS != 600 {
		t.Fatalf("participations = %+v", rows)
	}
	withIP := must[[]sqlc.ListParticipationsWithIPRow](t)(q.ListParticipationsWithIP(ctx, f.contest.ID))
	if len(withIP) != 1 {
		t.Fatalf("participations with ip = %d", len(withIP))
	}
	// Deleting the team keeps the participation.
	if err := q.DeleteTeam(ctx, team.ID); err != nil {
		t.Fatal(err)
	}
	if p := must[sqlc.Participation](t)(q.GetParticipation(ctx, f.part.ID)); p.TeamID != nil {
		t.Fatal("team reference must be cleared")
	}
}

func TestSubmissionsResultsAndEvaluations(t *testing.T) {
	f := newFixture(t)
	q := f.q
	base := time.Now().UTC().Truncate(time.Microsecond)

	empty := must[sqlc.SubmissionStatsRow](t)(q.SubmissionStats(ctx, sqlc.SubmissionStatsParams{ParticipationIds: []int64{f.part.ID}, TaskID: f.task.ID}))
	if empty.ContestCount != 0 || !empty.ContestLast.Equal(time.Unix(0, 0)) {
		t.Fatalf("empty stats = %+v", empty)
	}

	var subs []sqlc.Submission
	for i := 0; i < 3; i++ {
		s := must[sqlc.Submission](t)(q.CreateSubmission(ctx, sqlc.CreateSubmissionParams{
			ParticipationID: &f.part.ID, TaskID: f.task.ID, SubmittedAt: base.Add(time.Duration(i) * time.Minute),
			Language: ptr("cpp17"), Official: true,
		}))
		n := must[int64](t)(q.CreateSubmissionFiles(ctx, []sqlc.CreateSubmissionFilesParams{{SubmissionID: s.ID, Filename: "sum.%l", Digest: digestOf("src" + string(rune('a'+i)))}}))
		if n != 1 {
			t.Fatalf("copyfrom inserted %d", n)
		}
		subs = append(subs, s)
	}
	stats := must[sqlc.SubmissionStatsRow](t)(q.SubmissionStats(ctx, sqlc.SubmissionStatsParams{ParticipationIds: []int64{f.part.ID}, TaskID: f.task.ID}))
	if stats.ContestCount != 3 || stats.TaskCount != 3 || !stats.TaskLast.Equal(base.Add(2*time.Minute)) {
		t.Fatalf("stats = %+v", stats)
	}
	files := must[[]sqlc.SubmissionFile](t)(q.ListSubmissionFilesBySubmissions(ctx, []int64{subs[0].ID, subs[2].ID}))
	if len(files) != 2 {
		t.Fatalf("files = %+v", files)
	}

	s := subs[0]
	key := sqlc.EnsureSubmissionResultParams{SubmissionID: s.ID, DatasetID: f.dataset.ID}
	for i := 0; i < 2; i++ { // idempotent
		if err := q.EnsureSubmissionResult(ctx, key); err != nil {
			t.Fatal(err)
		}
	}
	// A compilation result from a stale generation is ignored.
	_, err := q.SetCompilationResult(ctx, sqlc.SetCompilationResultParams{SubmissionID: s.ID, DatasetID: f.dataset.ID, CompilationOutcome: ptr("ok"), Generation: 7})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("stale compilation must not apply: %v", err)
	}
	must[int64](t)(q.SetCompilationResult(ctx, sqlc.SetCompilationResultParams{
		SubmissionID: s.ID, DatasetID: f.dataset.ID, CompilationOutcome: ptr("ok"), CompilationText: "Compilation succeeded",
		CompilationTime: ptr(0.42), TestcasesTotal: 3, Generation: 0,
	}))
	if err := q.InsertExecutable(ctx, sqlc.InsertExecutableParams{SubmissionID: s.ID, DatasetID: f.dataset.ID, Filename: "sum", Digest: digestOf("exe")}); err != nil {
		t.Fatal(err)
	}
	// Evaluations: duplicates (retried jobs) overwrite instead of failing.
	for round := 0; round < 2; round++ {
		for _, tc := range f.tcs {
			err := q.UpsertEvaluation(ctx, sqlc.UpsertEvaluationParams{
				SubmissionID: s.ID, DatasetID: f.dataset.ID, TestcaseID: tc.ID, Outcome: 1, Text: "Output is correct",
				ExecutionTime: ptr(0.01), ExecutionMemory: ptr(int64(1 << 20)), ExitStatus: "ok",
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	prog := must[sqlc.RefreshTestcasesDoneRow](t)(q.RefreshTestcasesDone(ctx, sqlc.RefreshTestcasesDoneParams{SubmissionID: s.ID, DatasetID: f.dataset.ID}))
	if prog.TestcasesDone != 3 || prog.TestcasesTotal != 3 {
		t.Fatalf("progress = %+v", prog)
	}
	evs := must[[]sqlc.ListEvaluationsWithTestcaseRow](t)(q.ListEvaluationsWithTestcase(ctx, sqlc.ListEvaluationsWithTestcaseParams{SubmissionID: s.ID, DatasetID: f.dataset.ID}))
	if len(evs) != 3 || evs[0].Codename != "000" || !evs[0].Public {
		t.Fatalf("evaluations = %+v", evs)
	}
	if err := q.SetEvaluationDone(ctx, sqlc.SetEvaluationDoneParams{SubmissionID: s.ID, DatasetID: f.dataset.ID, Generation: 0}); err != nil {
		t.Fatal(err)
	}
	details := json.RawMessage(`[{"score":100}]`)
	if err := q.SetScore(ctx, sqlc.SetScoreParams{SubmissionID: s.ID, DatasetID: f.dataset.ID, Score: ptr(100.0), ScoreDetails: details, PublicScore: ptr(100.0), PublicScoreDetails: details, RankingScoreDetails: json.RawMessage(`["100"]`)}); err != nil {
		t.Fatal(err)
	}
	pending := must[[]sqlc.ListPendingSubmissionResultsRow](t)(q.ListPendingSubmissionResults(ctx, 10))
	if len(pending) != 0 {
		t.Fatalf("scored result must not be pending: %+v", pending)
	}

	// Rescore keeps evaluations and generation; reevaluate bumps generation.
	gen := must[int32](t)(q.InvalidateSubmissionResult(ctx, sqlc.InvalidateSubmissionResultParams{SubmissionID: s.ID, DatasetID: f.dataset.ID, Level: "score"}))
	r := must[sqlc.SubmissionResult](t)(q.GetSubmissionResult(ctx, sqlc.GetSubmissionResultParams{SubmissionID: s.ID, DatasetID: f.dataset.ID}))
	if gen != 0 || r.EvaluationOutcome == nil || r.Score != nil || r.TestcasesDone != 3 {
		t.Fatalf("after rescore invalidation: gen=%d %+v", gen, r)
	}
	gen = must[int32](t)(q.InvalidateSubmissionResult(ctx, sqlc.InvalidateSubmissionResultParams{SubmissionID: s.ID, DatasetID: f.dataset.ID, Level: "evaluation"}))
	r = must[sqlc.SubmissionResult](t)(q.GetSubmissionResult(ctx, sqlc.GetSubmissionResultParams{SubmissionID: s.ID, DatasetID: f.dataset.ID}))
	if gen != 1 || r.EvaluationOutcome != nil || r.CompilationOutcome == nil || r.TestcasesDone != 0 {
		t.Fatalf("after reevaluate invalidation: gen=%d %+v", gen, r)
	}
	gen = must[int32](t)(q.InvalidateSubmissionResult(ctx, sqlc.InvalidateSubmissionResultParams{SubmissionID: s.ID, DatasetID: f.dataset.ID, Level: "compilation"}))
	r = must[sqlc.SubmissionResult](t)(q.GetSubmissionResult(ctx, sqlc.GetSubmissionResultParams{SubmissionID: s.ID, DatasetID: f.dataset.ID}))
	if gen != 2 || r.CompilationOutcome != nil || r.CompilationTries != 0 {
		t.Fatalf("after recompile invalidation: gen=%d %+v", gen, r)
	}
	pending = must[[]sqlc.ListPendingSubmissionResultsRow](t)(q.ListPendingSubmissionResults(ctx, 10))
	if len(pending) != 1 || pending[0].Generation != 2 {
		t.Fatalf("pending = %+v", pending)
	}

	// Tokens: at most one per submission.
	must[sqlc.Token](t)(q.CreateToken(ctx, sqlc.CreateTokenParams{SubmissionID: s.ID, PlayedAt: base}))
	if _, err := q.CreateToken(ctx, sqlc.CreateTokenParams{SubmissionID: s.ID, PlayedAt: base}); pgCode(err) != "23505" {
		t.Fatalf("second token must fail: %v", err)
	}
	toks := must[[]sqlc.ListTokenTimesByParticipationRow](t)(q.ListTokenTimesByParticipation(ctx, f.part.ID))
	if len(toks) != 1 || toks[0].TaskID != f.task.ID {
		t.Fatalf("tokens = %+v", toks)
	}

	// Deleting the submission cascades to files, results, executables, evaluations.
	if err := q.DeleteSubmission(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	var left int
	f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM submission_files WHERE submission_id=$1) +
		(SELECT count(*) FROM submission_results WHERE submission_id=$1) +
		(SELECT count(*) FROM executables WHERE submission_id=$1) +
		(SELECT count(*) FROM evaluations WHERE submission_id=$1)`, s.ID).Scan(&left)
	if left != 0 {
		t.Fatalf("%d dependent rows survived the cascade", left)
	}
}

func TestUserTests(t *testing.T) {
	f := newFixture(t)
	q := f.q
	ut := must[sqlc.UserTest](t)(q.CreateUserTest(ctx, sqlc.CreateUserTestParams{
		ParticipationID: f.part.ID, TaskID: f.task.ID, SubmittedAt: time.Now(), Language: ptr("python3"), InputDigest: digestOf("1 2"),
	}))
	must[int64](t)(q.CreateUserTestFiles(ctx, []sqlc.CreateUserTestFilesParams{{UserTestID: ut.ID, Filename: "sum.%l", Digest: digestOf("print(3)")}}))
	if err := q.EnsureUserTestResult(ctx, sqlc.EnsureUserTestResultParams{UserTestID: ut.ID, DatasetID: f.dataset.ID}); err != nil {
		t.Fatal(err)
	}
	must[int64](t)(q.SetUserTestCompilation(ctx, sqlc.SetUserTestCompilationParams{UserTestID: ut.ID, DatasetID: f.dataset.ID, CompilationOutcome: ptr("ok")}))
	pending := must[[]sqlc.ListPendingUserTestResultsRow](t)(q.ListPendingUserTestResults(ctx, 10))
	if len(pending) != 1 {
		t.Fatalf("pending user tests = %d", len(pending))
	}
	must[int64](t)(q.SetUserTestEvaluation(ctx, sqlc.SetUserTestEvaluationParams{
		UserTestID: ut.ID, DatasetID: f.dataset.ID, EvaluationText: "Execution completed successfully",
		OutputDigest: ptr(digestOf("3\n")), ExecutionTime: ptr(0.02), ExitStatus: ptr("ok"),
	}))
	res := must[sqlc.UserTestResult](t)(q.GetUserTestResult(ctx, sqlc.GetUserTestResultParams{UserTestID: ut.ID, DatasetID: f.dataset.ID}))
	if res.CompletedAt == nil || *res.OutputDigest != digestOf("3\n") {
		t.Fatalf("user test result = %+v", res)
	}
	stats := must[sqlc.UserTestStatsRow](t)(q.UserTestStats(ctx, sqlc.UserTestStatsParams{ParticipationID: f.part.ID, TaskID: f.task.ID}))
	if stats.TaskCount != 1 {
		t.Fatalf("user test stats = %+v", stats)
	}
}

func TestCommunicationAndPrinting(t *testing.T) {
	f := newFixture(t)
	q := f.q
	admin := must[sqlc.Admin](t)(q.CreateAdmin(ctx, sqlc.CreateAdminParams{Name: "Root", Username: "root", PasswordHash: "x", Enabled: true, Role: "messaging"}))
	q1 := must[sqlc.Question](t)(q.CreateQuestion(ctx, sqlc.CreateQuestionParams{ParticipationID: f.part.ID, AskedAt: time.Now().Add(-time.Minute), Subject: "A", Text: "Is n > 0?"}))
	must[sqlc.Question](t)(q.CreateQuestion(ctx, sqlc.CreateQuestionParams{ParticipationID: f.part.ID, AskedAt: time.Now(), Subject: "B", Text: "?"}))
	must[sqlc.Question](t)(q.ReplyQuestion(ctx, sqlc.ReplyQuestionParams{ID: q1.ID, ReplySubject: ptr("Yes"), ReplyText: ptr(""), ReplyAdminID: &admin.ID}))
	list := must[[]sqlc.ListQuestionsByContestRow](t)(q.ListQuestionsByContest(ctx, f.contest.ID))
	if len(list) != 2 || list[0].Subject != "B" || list[0].Username != "alice" {
		t.Fatalf("unanswered questions must come first: %+v", list)
	}
	must[sqlc.Announcement](t)(q.CreateAnnouncement(ctx, sqlc.CreateAnnouncementParams{ContestID: f.contest.ID, Subject: "Welcome", Text: "Good luck", AdminID: &admin.ID}))
	must[sqlc.Message](t)(q.CreateMessage(ctx, sqlc.CreateMessageParams{ParticipationID: f.part.ID, Subject: "Hi", Text: "Private"}))
	if a := must[[]sqlc.Announcement](t)(q.ListAnnouncements(ctx, f.contest.ID)); len(a) != 1 {
		t.Fatalf("announcements = %d", len(a))
	}
	if m := must[[]sqlc.Message](t)(q.ListMessagesByParticipation(ctx, f.part.ID)); len(m) != 1 {
		t.Fatalf("messages = %d", len(m))
	}

	// Print jobs: concurrent claimers never take the same job.
	for i := 0; i < 20; i++ {
		must[sqlc.PrintJob](t)(q.CreatePrintJob(ctx, sqlc.CreatePrintJobParams{ParticipationID: f.part.ID, CreatedAt: time.Now(), Filename: "a.txt", Digest: digestOf("p"), Pages: ptr(int32(1))}))
	}
	var mu sync.Mutex
	seen := map[int64]bool{}
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				j, err := q.ClaimPrintJob(ctx)
				if errors.Is(err, pgx.ErrNoRows) {
					return
				}
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				if seen[j.ID] {
					t.Errorf("job %d claimed twice", j.ID)
				}
				seen[j.ID] = true
				mu.Unlock()
				_ = q.FinishPrintJob(ctx, sqlc.FinishPrintJobParams{ID: j.ID, Status: "done"})
			}
		}()
	}
	wg.Wait()
	if len(seen) != 20 {
		t.Fatalf("claimed %d jobs, want 20", len(seen))
	}
}

func TestAdminsAndAuditLog(t *testing.T) {
	f := newFixture(t)
	q := f.q
	a := must[sqlc.Admin](t)(q.CreateAdmin(ctx, sqlc.CreateAdminParams{Name: "A", Username: "a", PasswordHash: "x", Enabled: true, Role: "all"}))
	b := must[sqlc.Admin](t)(q.CreateAdmin(ctx, sqlc.CreateAdminParams{Name: "B", Username: "b", PasswordHash: "x", Enabled: true, Role: "read_only"}))
	if _, err := q.CreateAdmin(ctx, sqlc.CreateAdminParams{Name: "C", Username: "c", PasswordHash: "x", Role: "god"}); pgCode(err) != "23514" {
		t.Fatalf("unknown role must be rejected: %v", err)
	}
	for i := 0; i < 5; i++ {
		id := a.ID
		if i%2 == 1 {
			id = b.ID
		}
		if err := q.InsertAuditLog(ctx, sqlc.InsertAuditLogParams{AdminID: &id, Action: "contest.update", TargetType: "contest", TargetID: &f.contest.ID, Details: json.RawMessage(`{"i":1}`), Ip: "127.0.0.1"}); err != nil {
			t.Fatal(err)
		}
	}
	all := must[[]sqlc.ListAuditLogRow](t)(q.ListAuditLog(ctx, sqlc.ListAuditLogParams{Limit: 10}))
	if len(all) != 5 || all[0].ID < all[4].ID {
		t.Fatalf("audit log must be newest first: %d rows", len(all))
	}
	onlyB := must[[]sqlc.ListAuditLogRow](t)(q.ListAuditLog(ctx, sqlc.ListAuditLogParams{Limit: 10, AdminID: &b.ID}))
	if len(onlyB) != 2 || *onlyB[0].AdminUsername != "b" {
		t.Fatalf("filtered audit log = %+v", onlyB)
	}
	page2 := must[[]sqlc.ListAuditLogRow](t)(q.ListAuditLog(ctx, sqlc.ListAuditLogParams{Limit: 10, BeforeID: &all[1].ID}))
	if len(page2) != 3 {
		t.Fatalf("keyset pagination returned %d rows", len(page2))
	}
}

func TestTrackedBlobDeduplicationAndGC(t *testing.T) {
	f := newFixture(t)
	q := f.q
	store, err := blob.NewLocal(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	tracked := blob.NewTracked(store, q)
	src := []byte("#include <cstdio>\nint main(){}\n")
	i1 := must[blob.Info](t)(tracked.PutBytes(ctx, src))
	i2 := must[blob.Info](t)(tracked.PutBytes(ctx, src))
	if !i1.Created || i2.Created || i1.Digest != i2.Digest {
		t.Fatalf("dedup: %+v %+v", i1, i2)
	}
	var rows int
	f.pool.QueryRow(ctx, "SELECT count(*) FROM blobs WHERE digest = $1", i1.Digest).Scan(&rows)
	if rows != 1 {
		t.Fatalf("blobs rows = %d", rows)
	}
	// Reference one blob from a submission, leave another orphaned.
	orphan := must[blob.Info](t)(tracked.PutBytes(ctx, []byte("orphan")))
	sub := must[sqlc.Submission](t)(q.CreateSubmission(ctx, sqlc.CreateSubmissionParams{ParticipationID: &f.part.ID, TaskID: f.task.ID, SubmittedAt: time.Now(), Official: true}))
	must[int64](t)(q.CreateSubmissionFiles(ctx, []sqlc.CreateSubmissionFilesParams{{SubmissionID: sub.ID, Filename: "a.cpp", Digest: i1.Digest}}))

	// Within the grace period nothing is collected.
	n, _, err := blob.GC(ctx, store, q, time.Now().Add(-time.Hour), 100)
	if err != nil || n != 0 {
		t.Fatalf("gc within grace period removed %d (%v)", n, err)
	}
	n, size, err := blob.GC(ctx, store, q, time.Now().Add(time.Second), 100)
	if err != nil || n != 1 || size != int64(len("orphan")) {
		t.Fatalf("gc removed %d blobs (%d bytes), err %v", n, size, err)
	}
	if _, err := store.Stat(ctx, orphan.Digest); !errors.Is(err, blob.ErrNotFound) {
		t.Fatal("orphan must be deleted from the store")
	}
	if _, err := store.Stat(ctx, i1.Digest); err != nil {
		t.Fatal("referenced blob must survive")
	}
}

func TestParticipationTaskScores(t *testing.T) {
	f := newFixture(t)
	q := f.q
	upsert := func(score float64) float64 {
		t.Helper()
		stored, err := q.UpsertParticipationTaskScore(ctx, sqlc.UpsertParticipationTaskScoreParams{
			ParticipationID: f.part.ID, TaskID: f.task.ID, Score: score, SubtaskScores: json.RawMessage(`[30,40]`),
		})
		if err != nil {
			t.Fatal(err)
		}
		return stored
	}
	for _, score := range []float64{30, 70} {
		upsert(score)
	}
	rows := must[[]sqlc.ParticipationTaskScore](t)(q.ListParticipationTaskScoresByContest(ctx, f.contest.ID))
	if len(rows) != 1 || rows[0].Score != 70 {
		t.Fatalf("scores = %+v", rows)
	}
	// Manual adjustments add up and survive a new aggregation.
	for _, pts := range []float64{5, -2.5} {
		must[sqlc.ScoreAdjustment](t)(q.CreateScoreAdjustment(ctx, sqlc.CreateScoreAdjustmentParams{ParticipationID: f.part.ID,
			TaskID: f.task.ID, Points: pts, Reason: "checker bug on test 7"}))
		if err := q.ApplyScoreAdjustment(ctx, sqlc.ApplyScoreAdjustmentParams{ParticipationID: f.part.ID, TaskID: f.task.ID, Points: pts}); err != nil {
			t.Fatal(err)
		}
	}
	rows = must[[]sqlc.ParticipationTaskScore](t)(q.ListParticipationTaskScoresByContest(ctx, f.contest.ID))
	if rows[0].Score != 72.5 || rows[0].Adjustment != 2.5 {
		t.Fatalf("adjusted = %v (%v)", rows[0].Score, rows[0].Adjustment)
	}
	if stored := upsert(60); stored != 62.5 {
		t.Fatalf("re-aggregated = %v", stored)
	}
	for _, bad := range []sqlc.CreateScoreAdjustmentParams{{Points: 0, Reason: "a valid reason"}, {Points: 1, Reason: "  x  "}} {
		bad.ParticipationID, bad.TaskID = f.part.ID, f.task.ID
		if _, err := q.CreateScoreAdjustment(ctx, bad); pgCode(err) != "23514" {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

func TestInTxRollsBack(t *testing.T) {
	f := newFixture(t)
	boom := errors.New("boom")
	err := db.InTx(ctx, f.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		if _, err := q.CreateUser(ctx, sqlc.CreateUserParams{Username: "bob", PasswordHash: "x", PreferredLanguages: []string{}}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if _, err := f.q.GetUserByUsername(ctx, "bob"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("user must not exist after rollback: %v", err)
	}
}

func TestEveryIndexIsDocumented(t *testing.T) {
	migs, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migs {
		lines := strings.Split(m.SQL, "\n")
		for i, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), "CREATE INDEX") || strings.HasPrefix(strings.TrimSpace(l), "CREATE UNIQUE INDEX") {
				if i == 0 || !strings.HasPrefix(strings.TrimSpace(lines[i-1]), "--") {
					t.Errorf("%s:%d: index without a justifying comment: %s", m.Name, i+1, l)
				}
			}
		}
	}
}
