package contestarchive

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ctx = context.Background()

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func must[T any](t *testing.T) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		check(t, err)
		return v
	}
}

func ptr[T any](v T) *T { return &v }

// seed builds a contest "ioi" with every kind of row an archive carries,
// rows it leaves out (executables, user tests, print jobs, balloons) and a
// second contest whose rows must stay out.
func seed(t *testing.T, pool *pgxpool.Pool, store blob.Store) int64 {
	t.Helper()
	q := sqlc.New(pool)
	put := func(s string) string { return must[blob.Info](t)(store.PutBytes(ctx, []byte(s))).Digest }
	now := time.Now().UTC().Truncate(time.Second)
	admin := must[sqlc.Admin](t)(q.CreateAdmin(ctx, sqlc.CreateAdminParams{Name: "Root", Username: "root", PasswordHash: "x", Enabled: true, Role: "all"}))
	c := must[sqlc.Contest](t)(q.CreateContest(ctx, db.NewContestParams("ioi", now.Add(-5*time.Hour), now.Add(-time.Hour))))
	other := must[sqlc.Contest](t)(q.CreateContest(ctx, db.NewContestParams("other", now, now.Add(time.Hour))))
	site := must[sqlc.Site](t)(q.CreateSite(ctx, sqlc.CreateSiteParams{ContestID: c.ID, Name: "north", StartTime: ptr(now.Add(-4 * time.Hour))}))
	team := must[sqlc.Team](t)(q.CreateTeam(ctx, sqlc.CreateTeamParams{Code: "ARG", Name: "Argentina", FlagDigest: ptr(put("flag"))}))
	alice := must[sqlc.User](t)(q.CreateUser(ctx, sqlc.CreateUserParams{Username: "alice", FirstName: "Alice", PasswordHash: "plaintext:a", PreferredLanguages: []string{"es", "en"}, Country: "AR"}))
	bob := must[sqlc.User](t)(q.CreateUser(ctx, sqlc.CreateUserParams{Username: "bob", PasswordHash: "plaintext:b", PreferredLanguages: []string{}}))
	carol := must[sqlc.User](t)(q.CreateUser(ctx, sqlc.CreateUserParams{Username: "carol", PasswordHash: "plaintext:c", PreferredLanguages: []string{}}))
	check(t, func() error {
		_, err := pool.Exec(ctx, "UPDATE users SET photo_digest = $1 WHERE id = $2", put("photo"), alice.ID)
		return err
	}())

	var tasks []sqlc.Task
	var active []sqlc.Dataset
	var tcs [][]sqlc.Testcase
	for i, name := range []string{"sum", "max"} {
		tp := db.NewTaskParams(name, strings.ToUpper(name))
		tp.ContestID, tp.Num = &c.ID, ptr(int32(i))
		task := must[sqlc.Task](t)(q.CreateTask(ctx, tp))
		must[sqlc.Statement](t)(q.UpsertStatement(ctx, sqlc.UpsertStatementParams{TaskID: task.ID, Language: "es", Digest: put("statement " + name), ContentType: "application/pdf"}))
		must[sqlc.Attachment](t)(q.UpsertAttachment(ctx, sqlc.UpsertAttachmentParams{TaskID: task.ID, Filename: "sample.txt", Digest: put("sample " + name)}))
		var ds sqlc.Dataset
		for _, desc := range []string{"v1", "v2"} {
			ds = must[sqlc.Dataset](t)(q.CreateDataset(ctx, db.NewDatasetParams(task.ID, desc)))
			must[sqlc.Manager](t)(q.UpsertManager(ctx, sqlc.UpsertManagerParams{DatasetID: ds.ID, Filename: "checker", Digest: put("checker " + name + desc)}))
			var list []sqlc.Testcase
			for k, code := range []string{"1", "2"} {
				list = append(list, must[sqlc.Testcase](t)(q.UpsertTestcase(ctx, sqlc.UpsertTestcaseParams{DatasetID: ds.ID, Codename: code, Public: k == 0,
					InputDigest: put(name + desc + code + ".in"), OutputDigest: put(name + desc + code + ".out")})))
			}
			if desc == "v1" {
				continue
			}
			tcs = append(tcs, list)
		}
		// The live dataset is the second one, created after the task.
		check(t, q.SetActiveDataset(ctx, sqlc.SetActiveDatasetParams{ID: task.ID, ActiveDatasetID: &ds.ID}))
		tasks, active = append(tasks, task), append(active, ds)
	}
	otp := db.NewTaskParams("elsewhere", "Elsewhere")
	otp.ContestID = &other.ID
	must[sqlc.Task](t)(q.CreateTask(ctx, otp))

	pa := must[sqlc.Participation](t)(q.CreateParticipation(ctx, sqlc.CreateParticipationParams{ContestID: c.ID, UserID: alice.ID, TeamID: &team.ID,
		Ip: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")}, ExtraTimeS: 600}))
	check(t, func() error {
		_, err := pool.Exec(ctx, "UPDATE participations SET site_id = $1, starting_time = $2 WHERE id = $3", site.ID, now.Add(-4*time.Hour), pa.ID)
		return err
	}())
	pb := must[sqlc.Participation](t)(q.CreateParticipation(ctx, sqlc.CreateParticipationParams{ContestID: c.ID, UserID: bob.ID, Hidden: true, Ip: []netip.Prefix{}}))
	pc := must[sqlc.Participation](t)(q.CreateParticipation(ctx, sqlc.CreateParticipationParams{ContestID: other.ID, UserID: carol.ID, Ip: []netip.Prefix{}}))
	must[sqlc.Participation](t)(q.CreateParticipation(ctx, sqlc.CreateParticipationParams{ContestID: other.ID, UserID: alice.ID, Ip: []netip.Prefix{}}))

	must[sqlc.Announcement](t)(q.CreateAnnouncement(ctx, sqlc.CreateAnnouncementParams{ContestID: c.ID, Subject: "Welcome", Text: "Good luck", AdminID: &admin.ID}))
	qu := must[sqlc.Question](t)(q.CreateQuestion(ctx, sqlc.CreateQuestionParams{ParticipationID: pa.ID, TaskID: &tasks[0].ID, AskedAt: now.Add(-3 * time.Hour), Subject: "n?", Text: "Is n > 0?"}))
	must[sqlc.Question](t)(q.ReplyQuestion(ctx, sqlc.ReplyQuestionParams{ID: qu.ID, ReplySubject: ptr("Yes"), ReplyText: ptr(""), ReplyAdminID: &admin.ID}))
	must[sqlc.Message](t)(q.CreateMessage(ctx, sqlc.CreateMessageParams{ParticipationID: pb.ID, Subject: "Hi", Text: "Private", AdminID: &admin.ID}))
	must[sqlc.Question](t)(q.CreateQuestion(ctx, sqlc.CreateQuestionParams{ParticipationID: pc.ID, AskedAt: now, Subject: "other", Text: "?"}))

	submit := func(p sqlc.Participation, ti int, src string, score float64) sqlc.Submission {
		s := must[sqlc.Submission](t)(q.CreateSubmission(ctx, sqlc.CreateSubmissionParams{ParticipationID: &p.ID, TaskID: tasks[ti].ID,
			SubmittedAt: now.Add(-2 * time.Hour), Language: ptr("cpp17"), Official: true}))
		must[int64](t)(q.CreateSubmissionFiles(ctx, []sqlc.CreateSubmissionFilesParams{{SubmissionID: s.ID, Filename: tasks[ti].Name + ".%l", Digest: put(src)}}))
		key := sqlc.EnsureSubmissionResultParams{SubmissionID: s.ID, DatasetID: active[ti].ID}
		check(t, q.EnsureSubmissionResult(ctx, key))
		must[int64](t)(q.SetCompilationResult(ctx, sqlc.SetCompilationResultParams{SubmissionID: s.ID, DatasetID: active[ti].ID,
			CompilationOutcome: ptr("ok"), CompilationText: "Compilation succeeded", TestcasesTotal: 2}))
		check(t, q.InsertExecutable(ctx, sqlc.InsertExecutableParams{SubmissionID: s.ID, DatasetID: active[ti].ID, Filename: "exe", Digest: put("exe " + src)}))
		for _, tc := range tcs[ti] {
			check(t, q.UpsertEvaluation(ctx, sqlc.UpsertEvaluationParams{SubmissionID: s.ID, DatasetID: active[ti].ID, TestcaseID: tc.ID,
				Outcome: score / 100, Text: "Output is correct", ExecutionTime: ptr(0.01), ExitStatus: "ok"}))
		}
		check(t, q.SetEvaluationDone(ctx, sqlc.SetEvaluationDoneParams{SubmissionID: s.ID, DatasetID: active[ti].ID}))
		details := json.RawMessage(`[{"score":1}]`)
		check(t, q.SetScore(ctx, sqlc.SetScoreParams{SubmissionID: s.ID, DatasetID: active[ti].ID, Score: &score, ScoreDetails: details,
			PublicScore: &score, PublicScoreDetails: details, RankingScoreDetails: json.RawMessage(`["1"]`)}))
		must[float64](t)(q.UpsertParticipationTaskScore(ctx, sqlc.UpsertParticipationTaskScoreParams{ParticipationID: p.ID, TaskID: tasks[ti].ID,
			Score: score, SubtaskScores: json.RawMessage(`[1]`), LastSubmissionAt: &s.SubmittedAt}))
		return s
	}
	s1 := submit(pa, 0, "int main(){}", 100)
	submit(pa, 1, "int main(){return 0;}", 50)
	submit(pb, 0, "wrong", 0)
	must[sqlc.Token](t)(q.CreateToken(ctx, sqlc.CreateTokenParams{SubmissionID: s1.ID, PlayedAt: now.Add(-90 * time.Minute)}))
	check(t, func() error {
		_, err := pool.Exec(ctx, "UPDATE submissions SET invalidated_by = $1 WHERE id = $2", admin.ID, s1.ID)
		return err
	}())
	must[sqlc.ScoreAdjustment](t)(q.CreateScoreAdjustment(ctx, sqlc.CreateScoreAdjustmentParams{ParticipationID: pa.ID, TaskID: tasks[1].ID, Points: 5, Reason: "appeal", AdminID: &admin.ID}))
	must[sqlc.PrintJob](t)(q.CreatePrintJob(ctx, sqlc.CreatePrintJobParams{ParticipationID: pa.ID, CreatedAt: now, Filename: "a.txt", Digest: put("print")}))
	must[sqlc.UserTest](t)(q.CreateUserTest(ctx, sqlc.CreateUserTestParams{ParticipationID: pa.ID, TaskID: tasks[0].ID, SubmittedAt: now, InputDigest: put("user test")}))
	return c.ID
}

func export(t *testing.T, pool *pgxpool.Pool, store blob.Store, id int64, o Options) (*Header, []byte) {
	t.Helper()
	var buf bytes.Buffer
	h, err := Export(ctx, pool, store, id, &buf, o)
	check(t, err)
	return h, buf.Bytes()
}

func open(t *testing.T, b []byte) *zip.Reader {
	t.Helper()
	return must[*zip.Reader](t)(zip.NewReader(bytes.NewReader(b), int64(len(b))))
}

func newStore(t *testing.T) blob.Store {
	return must[*blob.Local](t)(blob.NewLocal(t.TempDir(), false))
}

// normalized reads the tables of an archive with every id replaced by its
// position in its table, so two archives of the same contest compare
// equal whatever ids each installation gave the rows.
func normalized(t *testing.T, b []byte) map[string][]string {
	t.Helper()
	zr := open(t, b)
	rows := map[string][]map[string]any{}
	ord := map[string]map[string]int{}
	for _, f := range zr.File {
		name, ok := strings.CutPrefix(f.Name, tablesDir)
		if !ok {
			continue
		}
		name = strings.TrimSuffix(name, ".jsonl")
		rc := must[io.ReadCloser](t)(f.Open())
		dec := json.NewDecoder(rc)
		dec.UseNumber()
		ord[name] = map[string]int{}
		for dec.More() {
			var row map[string]any
			check(t, dec.Decode(&row))
			if v, ok := row["id"]; ok {
				ord[name][v.(json.Number).String()] = len(rows[name])
			}
			rows[name] = append(rows[name], row)
		}
		rc.Close()
	}
	out := map[string][]string{}
	for name, list := range rows {
		for _, row := range list {
			for col, v := range row {
				if adminRefs[col] {
					row[col] = nil
				}
				if target, ok := refs[col]; ok && v != nil {
					if i, ok := ord[target][v.(json.Number).String()]; ok {
						row[col] = target + "#" + itoa(i)
					} else {
						row[col] = "outside:" + v.(json.Number).String()
					}
				}
			}
			if v, ok := row["id"]; ok {
				row["id"] = ord[name][v.(json.Number).String()]
			}
			b, _ := json.Marshal(row)
			out[name] = append(out[name], string(b))
		}
	}
	return out
}

func itoa(i int) string { return strconv.Itoa(i) }

func TestExportImportRoundTrip(t *testing.T) {
	src, srcStore := testutil.DB(t), newStore(t)
	id := seed(t, src, srcStore)
	h, arch := export(t, src, srcStore, id, Options{Submissions: true})
	want := map[string]int64{"contests": 1, "sites": 1, "users": 2, "teams": 1, "tasks": 2, "statements": 2, "attachments": 2,
		"datasets": 4, "managers": 4, "testcases": 8, "participations": 2, "announcements": 1, "questions": 1, "messages": 1,
		"submissions": 3, "submission_files": 3, "tokens": 1, "submission_results": 3, "evaluations": 6,
		"participation_task_scores": 3, "score_adjustments": 1}
	for name, n := range want {
		if h.Rows(name) != n {
			t.Errorf("%s: %d rows, want %d", name, h.Rows(name), n)
		}
	}
	if len(h.Tables) != len(want) || h.Contest != "ioi" || !h.Submissions || len(h.Missing) != 0 {
		t.Fatalf("header = %+v", h)
	}
	// Files: flag, photo, 2 statements, 2 attachments, 4 checkers, 16
	// testcase files, 3 sources; never executables, print jobs or user
	// tests.
	if h.Blobs != 29 {
		t.Fatalf("%d files archived, want 29", h.Blobs)
	}
	zr := open(t, arch)
	res := find(zr, resultsName)
	if res == nil {
		t.Fatal("no results.csv")
	}
	rc := must[io.ReadCloser](t)(res.Open())
	csv := string(must[[]byte](t)(io.ReadAll(rc)))
	if !strings.Contains(csv, "alice") || strings.Contains(csv, "carol") {
		t.Fatalf("results.csv:\n%s", csv)
	}

	// Another installation: every row comes back, with fresh ids and the
	// references rewritten; references to administrators are emptied.
	dst, dstStore := testutil.DB(t), newStore(t)
	r, err := Import(ctx, dst, dstStore, open(t, arch), ImportOptions{})
	check(t, err)
	var total int64
	for _, n := range want {
		total += n
	}
	if r.Rows != total || r.Blobs != 29 || r.ReusedUsers != 0 {
		t.Fatalf("result = %+v, want %d rows", r, total)
	}
	c := must[sqlc.Contest](t)(sqlc.New(dst).GetContest(ctx, r.ContestID))
	if c.Name != "ioi" || c.Status != "archived" {
		t.Fatalf("imported contest %s is %s", c.Name, c.Status)
	}
	h2, again := export(t, dst, dstStore, r.ContestID, Options{Submissions: true})
	if h2.Blobs != 29 || len(h2.Missing) != 0 {
		t.Fatalf("re-export: %+v", h2)
	}
	a, b := normalized(t, arch), normalized(t, again)
	for i := range a["contests"] {
		a["contests"][i] = strings.Replace(a["contests"][i], `"status":"published"`, `"status":"archived"`, 1)
	}
	for name, n := range want {
		if int64(len(b[name])) != n {
			t.Errorf("%s: %d rows re-exported, want %d", name, len(b[name]), n)
		}
		if strings.Join(a[name], "\n") != strings.Join(b[name], "\n") {
			t.Errorf("%s differs:\n%s\n---\n%s", name, strings.Join(a[name], "\n"), strings.Join(b[name], "\n"))
		}
	}
	var live int
	check(t, dst.QueryRow(ctx, `SELECT count(*) FROM tasks t JOIN datasets d ON d.id = t.active_dataset_id AND d.task_id = t.id
		WHERE t.contest_id = $1 AND d.description = 'v2'`, r.ContestID).Scan(&live))
	if live != 2 {
		t.Fatalf("%d tasks keep their live dataset", live)
	}

	// Names already taken: nothing is written.
	_, err = Import(ctx, dst, dstStore, open(t, arch), ImportOptions{})
	var ce *ConflictError
	if !errors.As(err, &ce) || ce.Contest != "ioi" || strings.Join(ce.Tasks, ",") != "max,sum" {
		t.Fatalf("second import: %v", err)
	}
	// The same installation, renamed: users and teams are reused.
	r2, err := Import(ctx, src, srcStore, open(t, arch), ImportOptions{Name: "ioi-copy", TaskSuffix: "-copy", Status: "draft"})
	check(t, err)
	if r2.ReusedUsers != 2 || r2.ReusedTeams != 1 {
		t.Fatalf("copy: %+v", r2)
	}
	var users, parts int
	check(t, src.QueryRow(ctx, "SELECT (SELECT count(*) FROM users), (SELECT count(*) FROM participations WHERE contest_id = $1)", r2.ContestID).Scan(&users, &parts))
	if users != 3 || parts != 2 {
		t.Fatalf("users = %d, participations = %d", users, parts)
	}
	st := must[sqlc.Task](t)(sqlc.New(src).GetTaskByName(ctx, "sum-copy"))
	if st.ContestID == nil || *st.ContestID != r2.ContestID {
		t.Fatalf("task sum-copy = %+v", st)
	}
}

func TestExportWithoutSubmissions(t *testing.T) {
	src, store := testutil.DB(t), newStore(t)
	id := seed(t, src, store)
	h, arch := export(t, src, store, id, Options{})
	if h.Submissions || h.Rows("submissions") != 0 || h.Rows("tasks") != 2 || find(open(t, arch), resultsName) != nil {
		t.Fatalf("header = %+v", h)
	}
	if h.Blobs != 26 {
		t.Fatalf("%d files, want 26 (no sources)", h.Blobs)
	}
	dst := testutil.DB(t)
	r, err := Import(ctx, dst, newStore(t), open(t, arch), ImportOptions{})
	check(t, err)
	c := must[sqlc.Contest](t)(sqlc.New(dst).GetContest(ctx, r.ContestID))
	if c.Status != "draft" {
		t.Fatalf("a contest without submissions is imported as %s", c.Status)
	}
}

// rewrite copies an archive, replacing members.
func rewrite(t *testing.T, b []byte, patch map[string][]byte) []byte {
	t.Helper()
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range open(t, b).File {
		data, ok := patch[f.Name]
		if !ok {
			rc := must[io.ReadCloser](t)(f.Open())
			data = must[[]byte](t)(io.ReadAll(rc))
			rc.Close()
		}
		w := must[io.Writer](t)(zw.Create(f.Name))
		w.Write(data)
	}
	check(t, zw.Close())
	return out.Bytes()
}

func TestImportRejectsDamagedAndNewerArchives(t *testing.T) {
	src, store := testutil.DB(t), newStore(t)
	id := seed(t, src, store)
	h, arch := export(t, src, store, id, Options{Submissions: true})
	dst := testutil.DB(t)
	count := func() int {
		var n int
		check(t, dst.QueryRow(ctx, "SELECT count(*) FROM contests").Scan(&n))
		return n
	}

	var blobName string
	for _, f := range open(t, arch).File {
		if strings.HasPrefix(f.Name, blobsDir) {
			blobName = f.Name
			break
		}
	}
	_, err := Import(ctx, dst, newStore(t), open(t, rewrite(t, arch, map[string][]byte{blobName: []byte("tampered")})), ImportOptions{})
	if err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Fatalf("tampered file: %v", err)
	}

	newer := *h
	newer.Migrations = append(append([]string{}, h.Migrations...), "9999_future.sql")
	hb, _ := json.Marshal(newer)
	_, err = Import(ctx, dst, newStore(t), open(t, rewrite(t, arch, map[string][]byte{headerName: hb})), ImportOptions{})
	if !errors.Is(err, ErrNewer) {
		t.Fatalf("newer archive: %v", err)
	}

	_, err = Import(ctx, dst, newStore(t), open(t, rewrite(t, arch, map[string][]byte{tablesDir + "evaluations.jsonl": []byte(`{"submission_id":1,"dataset_id":1,"testcase_id":1,"future_column":1}` + "\n")})), ImportOptions{})
	if !errors.Is(err, ErrNewer) {
		t.Fatalf("unknown column: %v", err)
	}
	// The failed imports left nothing behind.
	if count() != 0 {
		t.Fatalf("%d contests after failed imports", count())
	}
	if _, err := Import(ctx, dst, newStore(t), open(t, arch), ImportOptions{Name: "bad name"}); err == nil {
		t.Fatal("invalid name accepted")
	}
}

// TestEveryTableIsDecided keeps the archive in step with the schema: every
// table is either archived or left out on purpose, and every reference of
// an archived table is rewritten or emptied on import.
func TestEveryTableIsDecided(t *testing.T) {
	pool := testutil.DB(t)
	exported := map[string]bool{}
	for _, tb := range tables {
		if _, ok := skipped[tb.name]; ok {
			t.Errorf("%s is both archived and skipped", tb.name)
		}
		exported[tb.name] = true
	}
	rows := must[[]string](t)(func() ([]string, error) {
		r, err := pool.Query(ctx, `SELECT relname::text FROM pg_class WHERE relnamespace = current_schema()::regnamespace AND relkind = 'r' ORDER BY 1`)
		if err != nil {
			return nil, err
		}
		defer r.Close()
		var out []string
		for r.Next() {
			var s string
			if err := r.Scan(&s); err != nil {
				return nil, err
			}
			out = append(out, s)
		}
		return out, r.Err()
	}())
	for _, name := range rows {
		if _, ok := skipped[name]; !ok && !exported[name] {
			t.Errorf("table %s is neither archived nor listed in skipped", name)
		}
	}
	r, err := pool.Query(ctx, `SELECT c.conrelid::regclass::text, a.attname::text, c.confrelid::regclass::text, cardinality(c.conkey)
FROM pg_constraint c JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
WHERE c.contype = 'f' ORDER BY 1, 2`)
	check(t, err)
	defer r.Close()
	for r.Next() {
		var table, col, target string
		var n int
		check(t, r.Scan(&table, &col, &target, &n))
		if !exported[table] {
			continue
		}
		switch {
		case adminRefs[col]:
			if target != "admins" {
				t.Errorf("%s.%s refers to %s, not admins", table, col, target)
			}
		case refs[col] == "":
			t.Errorf("%s.%s (-> %s) is not rewritten on import: add it to refs", table, col, target)
		case n == 1 && refs[col] != target:
			t.Errorf("%s.%s refers to %s, refs says %s", table, col, target, refs[col])
		case !exported[refs[col]]:
			t.Errorf("%s.%s refers to %s, which is not archived", table, col, refs[col])
		}
	}
	check(t, r.Err())
}
