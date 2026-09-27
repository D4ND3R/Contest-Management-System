package rehearsal

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/D4ND3R/Contest-Management-System/internal/testutil"
)

var bg = context.Background()

func contest(t *testing.T, q *sqlc.Queries, name string, tasks []string, users ...string) (sqlc.Contest, []sqlc.Task, map[string]int64) {
	t.Helper()
	start := time.Date(2030, 1, 1, 10, 0, 0, 0, time.UTC)
	c, err := q.CreateContest(bg, db.NewContestParams(name, start, start.Add(5*time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	var ts []sqlc.Task
	for i, tn := range tasks {
		tp := db.NewTaskParams(tn, tn)
		n := int32(i)
		tp.ContestID, tp.Num = &c.ID, &n
		tk, err := q.CreateTask(bg, tp)
		if err != nil {
			t.Fatal(err)
		}
		ds, err := q.CreateDataset(bg, db.NewDatasetParams(tk.ID, "d"))
		if err != nil {
			t.Fatal(err)
		}
		if err := q.SetActiveDataset(bg, sqlc.SetActiveDatasetParams{ID: tk.ID, ActiveDatasetID: &ds.ID}); err != nil {
			t.Fatal(err)
		}
		ts = append(ts, tk)
	}
	parts := map[string]int64{}
	for _, un := range users {
		u, err := q.GetUserByUsername(bg, un)
		if err != nil {
			u, err = q.CreateUser(bg, sqlc.CreateUserParams{Username: un, PasswordHash: "x", PreferredLanguages: []string{}})
			if err != nil {
				t.Fatal(err)
			}
		}
		p, err := q.CreateParticipation(bg, sqlc.CreateParticipationParams{ContestID: c.ID, UserID: u.ID, Ip: []netip.Prefix{}})
		if err != nil {
			t.Fatal(err)
		}
		parts[un] = p.ID
	}
	return c, ts, parts
}

func TestReplay(t *testing.T) {
	pool := testutil.DB(t)
	rdb, ns := testutil.Redis(t)
	qu := queue.New(rdb, ns)
	if err := qu.Setup(bg); err != nil {
		t.Fatal(err)
	}
	q := sqlc.New(pool)
	_, srcTasks, srcParts := contest(t, q, "ioi2029", []string{"sum", "tree"}, "alice", "bob")
	_, dstTasks, dstParts := contest(t, q, "rehearsal", []string{"sum-r", "tree-r"}, "alice") // bob is not in the rehearsal

	base := time.Date(2029, 7, 1, 9, 0, 0, 0, time.UTC)
	lang := "C++17 / g++"
	sub := func(user string, task int, after time.Duration, src string) {
		p := srcParts[user]
		s, err := q.CreateSubmission(bg, sqlc.CreateSubmissionParams{ParticipationID: &p, TaskID: srcTasks[task].ID,
			SubmittedAt: base.Add(after), Language: &lang, Official: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := q.CreateSubmissionFiles(bg, []sqlc.CreateSubmissionFilesParams{{SubmissionID: s.ID, Filename: "sol.%l", Digest: src}}); err != nil {
			t.Fatal(err)
		}
	}
	d1, d2, d3 := "1111111111111111111111111111111111111111111111111111111111111111",
		"2222222222222222222222222222222222222222222222222222222222222222", "3333333333333333333333333333333333333333333333333333333333333333"
	sub("alice", 0, 0, d1)
	sub("bob", 0, 10*time.Second, d2)
	sub("alice", 1, 50*time.Second, d3)

	// 50 s of contest at 100×: about half a second.
	rep, err := Replay(bg, pool, qu, "ioi2029", "rehearsal", Options{Speed: 100})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Sent != 2 || rep.Skipped != 1 || len(rep.IDs) != 2 {
		t.Fatalf("report %+v", rep)
	}
	if rep.Took < 400*time.Millisecond || rep.Took > 5*time.Second {
		t.Fatalf("replay took %s, want the original 50 s gap divided by 100", rep.Took)
	}
	for i, id := range rep.IDs {
		s, err := q.GetSubmission(bg, id)
		if err != nil {
			t.Fatal(err)
		}
		if *s.ParticipationID != dstParts["alice"] || s.TaskID != dstTasks[i].ID || !s.Official {
			t.Fatalf("submission %d went to participation %d task %d", id, *s.ParticipationID, s.TaskID)
		}
	}
	files, err := q.ListSubmissionFilesBySubmissions(bg, rep.IDs)
	if err != nil || len(files) != 2 || files[0].Digest != d1 || files[1].Digest != d3 {
		t.Fatalf("files %+v %v", files, err)
	}
	var receipts int
	if err := pool.QueryRow(bg, `SELECT count(*) FROM audit_log WHERE action = 'submission.received' AND actor = 'rehearsal:alice'`).Scan(&receipts); err != nil || receipts != 2 {
		t.Fatalf("receipts %d %v", receipts, err)
	}
	evs, err := qu.ReadEvents(bg, "test", 10, time.Second)
	if err != nil || len(evs) != 2 || evs[0].Event.SubmissionID != rep.IDs[0] {
		t.Fatalf("events %+v %v", evs, err)
	}

	l, err := Measure(bg, pool, rep.IDs)
	if err != nil || l.Pending != 2 || l.Scored != 0 {
		t.Fatalf("latency before judging %+v %v", l, err)
	}
	if _, err := pool.Exec(bg, `INSERT INTO submission_results (submission_id, dataset_id, score, scored_at)
		SELECT s.id, t.active_dataset_id, 100, s.submitted_at + interval '3 seconds'
		FROM submissions s JOIN tasks t ON t.id = s.task_id WHERE s.id = $1`, rep.IDs[0]); err != nil {
		t.Fatal(err)
	}
	l, err = Measure(bg, pool, rep.IDs)
	if err != nil || l.Pending != 1 || l.Scored != 1 || l.Max != 3*time.Second {
		t.Fatalf("latency %+v %v", l, err)
	}

	if _, err := Replay(bg, pool, qu, "ioi2029", "ioi2029", Options{}); err == nil {
		t.Fatal("replaying a contest into itself was accepted")
	}
	rep, err = Replay(bg, pool, qu, "ioi2029", "rehearsal", Options{Speed: 1000, Limit: 1})
	if err != nil || rep.Sent != 1 {
		t.Fatalf("limit: %+v %v", rep, err)
	}
}
