package backup_test

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/backup"
	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/logging"
	"github.com/D4ND3R/Contest-Management-System/internal/testutil"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/klauspost/compress/zstd"
)

var ctx = context.Background()

// fixture fills a database with a small contest touching most tables and
// returns the store holding its blobs.
func fixture(t *testing.T, pool *pgxpool.Pool) blob.Store {
	t.Helper()
	raw, err := blob.NewLocal(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	q := sqlc.New(pool)
	store := blob.NewTracked(raw, q)
	put := func(s string) string {
		info, err := store.PutBytes(ctx, []byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return info.Digest
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	admin, err := q.CreateAdmin(ctx, sqlc.CreateAdminParams{Name: "Admin", Username: "admin", PasswordHash: "x", Enabled: true, Role: "all"})
	must(err)
	c, err := q.CreateContest(ctx, db.NewContestParams("final", now.Add(-time.Hour), now.Add(time.Hour)))
	must(err)
	tp := db.NewTaskParams("suma", "Suma — ñandú")
	tp.ContestID = &c.ID
	task, err := q.CreateTask(ctx, tp)
	must(err)
	_, err = q.UpsertStatement(ctx, sqlc.UpsertStatementParams{TaskID: task.ID, Language: "es", Digest: put("%PDF enunciado"), ContentType: "application/pdf"})
	must(err)
	ds, err := q.CreateDataset(ctx, db.NewDatasetParams(task.ID, "v1"))
	must(err)
	must(q.SetActiveDataset(ctx, sqlc.SetActiveDatasetParams{ID: task.ID, ActiveDatasetID: &ds.ID}))
	var tcs []sqlc.Testcase
	for i, io := range [][2]string{{"1 2\n", "3\n"}, {"2 2\n", "4\n"}, {"-1 1\n", "0\n"}} {
		tc, err := q.UpsertTestcase(ctx, sqlc.UpsertTestcaseParams{DatasetID: ds.ID, Codename: string(rune('a' + i)), Public: i == 0,
			InputDigest: put(io[0]), OutputDigest: put(io[1])})
		must(err)
		tcs = append(tcs, tc)
	}
	team, err := q.CreateTeam(ctx, sqlc.CreateTeamParams{Code: "ARG", Name: "Argentina", Institution: "Escuela \"N°1\"\ttab"})
	must(err)
	u, err := q.CreateUser(ctx, sqlc.CreateUserParams{Username: "ana", FirstName: "Ana", LastName: "Pérez\nnewline", PasswordHash: "h",
		PreferredLanguages: []string{"es", "en"}})
	must(err)
	p, err := q.CreateParticipation(ctx, sqlc.CreateParticipationParams{ContestID: c.ID, UserID: u.ID, TeamID: &team.ID,
		Ip: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}})
	must(err)
	lang := "c11"
	sub, err := q.CreateSubmission(ctx, sqlc.CreateSubmissionParams{ParticipationID: &p.ID, TaskID: task.ID, SubmittedAt: now, Language: &lang, Official: true})
	must(err)
	_, err = q.CreateSubmissionFiles(ctx, []sqlc.CreateSubmissionFilesParams{{SubmissionID: sub.ID, Filename: "suma.%l", Digest: put("int main(){}\\ \x01")}})
	must(err)
	must(q.EnsureSubmissionResult(ctx, sqlc.EnsureSubmissionResultParams{SubmissionID: sub.ID, DatasetID: ds.ID}))
	tm := 0.012
	for _, tc := range tcs {
		must(q.UpsertEvaluation(ctx, sqlc.UpsertEvaluationParams{SubmissionID: sub.ID, DatasetID: ds.ID, TestcaseID: tc.ID, Outcome: 1,
			Text: "Output is correct", ExecutionTime: &tm, ExitStatus: "ok"}))
	}
	_, err = q.CreateToken(ctx, sqlc.CreateTokenParams{SubmissionID: sub.ID, PlayedAt: now})
	must(err)
	_, err = q.CreateQuestion(ctx, sqlc.CreateQuestionParams{ParticipationID: p.ID, TaskID: &task.ID, AskedAt: now, Subject: "¿n?", Text: "línea 1\nlínea 2"})
	must(err)
	_, err = q.CreateAnnouncement(ctx, sqlc.CreateAnnouncementParams{ContestID: c.ID, Subject: "Aviso", Text: "texto", AdminID: &admin.ID})
	must(err)
	must(q.InsertAuditLog(ctx, sqlc.InsertAuditLogParams{AdminID: &admin.ID, Action: "seed", Details: json.RawMessage(`{"k": "v"}`), Ip: "::1"}))
	_, err = q.UpsertParticipationTaskScore(ctx, sqlc.UpsertParticipationTaskScoreParams{ParticipationID: p.ID, TaskID: task.ID, Score: 100,
		SubtaskScores: json.RawMessage(`[30, 70]`)})
	must(err)
	// A deleted row leaves a gap in the sequence (restored exactly too).
	extra, err := q.CreateUser(ctx, sqlc.CreateUserParams{Username: "gone", PasswordHash: "h", PreferredLanguages: []string{}})
	must(err)
	_, err = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", extra.ID)
	must(err)
	return raw
}

// snapshot fingerprints every table, the sequences and the migrations.
func snapshot(t *testing.T, pool *pgxpool.Pool) map[string]string {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT tablename::text FROM pg_tables WHERE schemaname = current_schema() ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, tb := range tables {
		if tb == "schema_migrations" {
			continue
		}
		var n int64
		var sum string
		id := pgx.Identifier{tb}.Sanitize()
		if err := pool.QueryRow(ctx, `SELECT count(*), md5(coalesce(string_agg(x::text, E'\n' ORDER BY x::text), '')) FROM `+id+` x`).Scan(&n, &sum); err != nil {
			t.Fatal(err)
		}
		out["table "+tb] = fmt.Sprint(n, " rows ", sum)
	}
	rows, err = pool.Query(ctx, `SELECT sequencename::text || '=' || coalesce(last_value::text, 'null') FROM pg_sequences WHERE schemaname = current_schema()`)
	if err != nil {
		t.Fatal(err)
	}
	seqs, _ := pgx.CollectRows(rows, pgx.RowTo[string])
	for _, s := range seqs {
		out["seq "+s] = "ok"
	}
	rows, err = pool.Query(ctx, `SELECT string_agg(name, ',' ORDER BY version) FROM schema_migrations`)
	if err != nil {
		t.Fatal(err)
	}
	mig, _ := pgx.CollectExactlyOneRow(rows, pgx.RowTo[string])
	out["migrations"] = mig
	return out
}

func compare(t *testing.T, want, got map[string]string) {
	t.Helper()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s differs after the restore", k)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("%s appeared after the restore", k)
		}
	}
}

// TestDumpRestoreIdentical is the SPEC_CLOSE A4 acceptance test: dump,
// restore into an empty database and a new store, identical data.
func TestDumpRestoreIdentical(t *testing.T) {
	src := testutil.DB(t)
	store := fixture(t, src)
	var buf bytes.Buffer
	st, err := backup.Dump(ctx, src, store, &buf, backup.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if st.Blobs != 8 || len(st.Missing) != 0 || st.Rows == 0 {
		t.Fatalf("dump stats %+v", st)
	}
	vs, err := backup.Verify(ctx, bytes.NewReader(buf.Bytes()))
	if err != nil || vs.Blobs != st.Blobs || vs.Rows != st.Rows {
		t.Fatalf("verify %+v %v", vs, err)
	}

	dst, _ := testutil.EmptyDB(t)
	dstStore, _ := blob.NewLocal(t.TempDir(), false)
	rs, err := backup.Restore(ctx, dst, blob.NewTracked(dstStore, sqlc.New(dst)), bytes.NewReader(buf.Bytes()), backup.RestoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rs.Rows != st.Rows || rs.Blobs != st.Blobs {
		t.Fatalf("restore stats %+v vs %+v", rs, st)
	}
	compare(t, snapshot(t, src), snapshot(t, dst))
	// Every blob came back, byte for byte.
	digests, _ := pgx.CollectRows(must(dst.Query(ctx, "SELECT digest FROM blobs")), pgx.RowTo[string])
	for _, d := range digests {
		a, err1 := blob.ReadAll(ctx, store, d)
		b, err2 := blob.ReadAll(ctx, dstStore, d)
		if err1 != nil || err2 != nil || !bytes.Equal(a, b) {
			t.Fatalf("blob %s: %v %v", d, err1, err2)
		}
	}
	// Sequences continue after the restored ids.
	q := sqlc.New(dst)
	u, err := q.CreateUser(ctx, sqlc.CreateUserParams{Username: "new", PasswordHash: "h", PreferredLanguages: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	var maxID int64
	src.QueryRow(ctx, "SELECT max(id) FROM users").Scan(&maxID)
	if u.ID <= maxID+1 {
		t.Fatalf("new user id %d, source max %d (+1 deleted)", u.ID, maxID)
	}
	// Foreign keys are back.
	if _, err := dst.Exec(ctx, "INSERT INTO participations (contest_id, user_id) VALUES (999999, 999999)"); err == nil {
		t.Fatal("foreign keys were not restored")
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// TestRestoreRefusesUsedDatabase: a database with data is only replaced
// when forced, and then ends up identical to the backup.
func TestRestoreRefusesUsedDatabase(t *testing.T) {
	src := testutil.DB(t)
	store := fixture(t, src)
	var buf bytes.Buffer
	if _, err := backup.Dump(ctx, src, store, &buf, backup.Options{}); err != nil {
		t.Fatal(err)
	}
	want := snapshot(t, src)
	dst := testutil.DB(t)
	dstStore := fixture(t, dst)
	sqlc.New(dst).CreateUser(ctx, sqlc.CreateUserParams{Username: "local", PasswordHash: "h", PreferredLanguages: []string{}})
	_, err := backup.Restore(ctx, dst, dstStore, bytes.NewReader(buf.Bytes()), backup.RestoreOptions{})
	if err == nil || !strings.Contains(err.Error(), "already holds data") {
		t.Fatalf("restore into a used database: %v", err)
	}
	if _, err := backup.Restore(ctx, dst, dstStore, bytes.NewReader(buf.Bytes()), backup.RestoreOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	compare(t, want, snapshot(t, dst))
}

// rewrite rebuilds an archive changing members with fn (nil drops one).
func rewrite(t *testing.T, archive []byte, fn func(name string, b []byte) []byte) []byte {
	t.Helper()
	zr, _ := zstd.NewReader(bytes.NewReader(archive))
	defer zr.Close()
	tr := tar.NewReader(zr)
	var out bytes.Buffer
	zw, _ := zstd.NewWriter(&out)
	tw := tar.NewWriter(zw)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		if b = fn(h.Name, b); b == nil {
			continue
		}
		h.Size = int64(len(b))
		tw.WriteHeader(h)
		tw.Write(b)
	}
	tw.Close()
	zw.Close()
	return out.Bytes()
}

// TestVerifyDetectsDamage: a changed row, a changed blob, a missing member
// and a truncated file are all reported, and a damaged backup never
// commits anything.
func TestVerifyDetectsDamage(t *testing.T) {
	src := testutil.DB(t)
	store := fixture(t, src)
	var buf bytes.Buffer
	if _, err := backup.Dump(ctx, src, store, &buf, backup.Options{}); err != nil {
		t.Fatal(err)
	}
	good := buf.Bytes()
	cases := map[string][]byte{
		"row changed": rewrite(t, good, func(n string, b []byte) []byte {
			if strings.HasPrefix(n, "db/users/") {
				return bytes.Replace(b, []byte("ana"), []byte("eva"), 1)
			}
			return b
		}),
		"blob changed": rewrite(t, good, func(n string, b []byte) []byte {
			if strings.HasPrefix(n, "blobs/") {
				return append([]byte("x"), b...)
			}
			return b
		}),
		"member missing": rewrite(t, good, func(n string, b []byte) []byte {
			if n == "db/sequences.json" {
				return nil
			}
			return b
		}),
		"no manifest": rewrite(t, good, func(n string, b []byte) []byte {
			if n == "manifest.json" {
				return nil
			}
			return b
		}),
		"truncated":    good[:len(good)*2/3],
		"not a backup": []byte("hello"),
	}
	for name, archive := range cases {
		if _, err := backup.Verify(ctx, bytes.NewReader(archive)); err == nil {
			t.Errorf("%s: verify passed", name)
		}
		dst, _ := testutil.EmptyDB(t)
		dstStore, _ := blob.NewLocal(t.TempDir(), false)
		if _, err := backup.Restore(ctx, dst, dstStore, bytes.NewReader(archive), backup.RestoreOptions{}); err == nil {
			t.Errorf("%s: restore passed", name)
		}
		var users int
		if err := dst.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&users); err == nil && users != 0 {
			t.Errorf("%s: a damaged restore committed %d users", name, users)
		}
	}
}

// TestRestoreOlderBackup: a backup of an older schema is restored into it
// and then migrated to the current one.
func TestRestoreOlderBackup(t *testing.T) {
	migs, _ := db.Migrations()
	old, _ := testutil.EmptyDB(t)
	if _, err := db.MigrateTo(ctx, old, migs[len(migs)-2].Version); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(ctx, "INSERT INTO users (username, password_hash, preferred_languages) VALUES ('vieja', 'h', '{}')"); err != nil {
		t.Fatal(err)
	}
	raw, _ := blob.NewLocal(t.TempDir(), false)
	var buf bytes.Buffer
	st, err := backup.Dump(ctx, old, raw, &buf, backup.Options{})
	if err != nil || len(st.Header.Migrations) != len(migs)-1 {
		t.Fatalf("dump %+v %v", st, err)
	}
	dst, _ := testutil.EmptyDB(t)
	if _, err := backup.Restore(ctx, dst, raw, bytes.NewReader(buf.Bytes()), backup.RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	var n, applied int
	dst.QueryRow(ctx, "SELECT count(*) FROM users WHERE username = 'vieja'").Scan(&n)
	dst.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&applied)
	if n != 1 || applied != len(migs) {
		t.Fatalf("users %d, migrations %d/%d", n, applied, len(migs))
	}
	// A database with a newer schema is refused (unless forced).
	if _, err := backup.Restore(ctx, testutil.DB(t), raw, bytes.NewReader(buf.Bytes()), backup.RestoreOptions{}); err == nil || !strings.Contains(err.Error(), "newer schema") {
		t.Fatalf("restore into a newer schema: %v", err)
	}
}

// TestDumpThrottle: max_rate bounds the read speed.
func TestDumpThrottle(t *testing.T) {
	src := testutil.DB(t)
	store := fixture(t, src)
	var buf bytes.Buffer
	var last int64
	start := time.Now()
	st, err := backup.Dump(ctx, src, store, &buf, backup.Options{MaxRate: 64 << 10, Progress: func(n int64) { last = n }})
	if err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	if last < st.DBBytes+st.BlobBytes {
		t.Fatalf("progress %d < %d", last, st.DBBytes+st.BlobBytes)
	}
	if want := time.Duration(float64(last) / float64(64<<10) * float64(time.Second)); took < want*8/10 {
		t.Fatalf("dump of %d bytes took %v at 64 KiB/s (want >= %v)", last, took, want)
	}
}

type fakeRemote struct {
	mu      sync.Mutex
	objects map[string]int64
	fail    bool
}

func (f *fakeRemote) Upload(_ context.Context, name, path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return io.ErrUnexpectedEOF
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	f.objects[name] = info.Size()
	return nil
}

func (f *fakeRemote) Delete(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, name)
	return nil
}

// TestRunnerScheduleAndRotation: the contest schedule applies while a
// contest runs, scheduled backups rotate (locally and off-site), manual
// ones stay, and a failed off-site copy keeps the local one.
func TestRunnerScheduleAndRotation(t *testing.T) {
	pool := testutil.DB(t)
	store := fixture(t, pool) // the fixture contest is running now
	dir := t.TempDir()
	remote := &fakeRemote{objects: map[string]int64{}}
	cfg := config.Backup{Dir: dir, Interval: config.Duration(time.Hour), ContestInterval: config.Duration(time.Hour), Keep: 2}
	r := backup.NewRunner(pool, store, cfg, nil, remote, logging.Discard())
	var alerts []string
	r.Alert = func(_ context.Context, msg string) { alerts = append(alerts, msg) }
	if err := r.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.Tick(ctx); err != nil { // not due yet
		t.Fatal(err)
	}
	list, _ := r.List()
	if len(list) != 1 || list[0].Kind != backup.KindScheduled || list[0].Status != "done" || !list[0].Remote {
		t.Fatalf("after two ticks: %+v", list)
	}
	for i := 0; i < 3; i++ {
		time.Sleep(2 * time.Millisecond) // distinct names
		if _, err := r.Backup(ctx, backup.KindScheduled, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Backup(ctx, backup.KindManual, "admin"); err != nil {
		t.Fatal(err)
	}
	list, _ = r.List()
	var sched, manual int
	for _, e := range list {
		switch e.Kind {
		case backup.KindScheduled:
			sched++
		case backup.KindManual:
			manual++
			if e.By != "admin" {
				t.Errorf("manual backup by %q", e.By)
			}
		}
		p, err := r.Path(e.Name)
		if err != nil {
			t.Fatal(err)
		}
		f, _ := os.Open(p)
		if _, err := backup.Verify(ctx, f); err != nil {
			t.Errorf("%s: %v", e.Name, err)
		}
		f.Close()
	}
	if sched != 2 || manual != 1 {
		t.Fatalf("kept %d scheduled and %d manual: %+v", sched, manual, list)
	}
	if len(remote.objects) != 2*3 { // archive + metadata each
		t.Fatalf("remote objects %v", remote.objects)
	}
	// The contest interval applies while the contest runs.
	r2 := backup.NewRunner(pool, store, config.Backup{Dir: dir, Interval: config.Duration(time.Hour), ContestInterval: config.Duration(time.Millisecond), Keep: 10}, nil, nil, logging.Discard())
	time.Sleep(2 * time.Millisecond)
	if err := r2.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if l, _ := r2.List(); len(l) != len(list)+1 {
		t.Fatalf("contest interval ignored: %d backups", len(l))
	}
	// Off-site failures alert but keep the local copy.
	remote.fail = true
	e, err := r.Backup(ctx, backup.KindManual, "admin")
	if err != nil || e.Remote || e.RemoteError == "" || len(alerts) != 1 {
		t.Fatalf("failed upload: %+v %v %v", e, err, alerts)
	}
	// Delete removes the files; bad names are rejected.
	if err := r.Delete(ctx, e.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Path(e.Name); err == nil {
		t.Fatal("deleted backup still there")
	}
	if _, err := r.Path("../etc/passwd"); err == nil {
		t.Fatal("path traversal accepted")
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 2*(len(list)+1) {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Fatalf("files left: %v", names)
	}
	// Archives copied in by hand are listed.
	os.WriteFile(filepath.Join(dir, "cms-backup-20200101T000000.000Z-cli.tar.zst"), []byte("x"), 0o600)
	l, _ := r.List()
	if l[len(l)-1].Kind != "external" {
		t.Fatalf("external archive not listed last: %+v", l[len(l)-1])
	}
}
