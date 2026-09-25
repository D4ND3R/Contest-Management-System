package ranking

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/testutil"
)

var bg = context.Background()

type cell struct {
	score    float64
	solved   bool
	attempts int32
	at       time.Duration
}

// setup creates a contest with two tasks (Sum over 4 testcases: 100 each)
// and the given per-user cells.
func setup(t *testing.T, mode string, users map[string][2]*cell, hidden string) (*sqlc.Queries, int64) {
	pool := testutil.DB(t)
	q := sqlc.New(pool)
	mem := blob.NewMem()
	start := time.Date(2030, 1, 1, 10, 0, 0, 0, time.UTC)
	cp := db.NewContestParams("r", start, start.Add(5*time.Hour))
	cp.ScoringMode = mode
	c, err := q.CreateContest(bg, cp)
	if err != nil {
		t.Fatal(err)
	}
	var tasks []sqlc.Task
	for i, name := range []string{"a", "b"} {
		tp := db.NewTaskParams(name, name)
		tp.ContestID, tp.Num = &c.ID, ptr(int32(i))
		tk, _ := q.CreateTask(bg, tp)
		dp := db.NewDatasetParams(tk.ID, "d")
		dp.ScoreType, dp.ScoreTypeParams = "Sum", json.RawMessage(`25`)
		ds, _ := q.CreateDataset(bg, dp)
		q.SetActiveDataset(bg, sqlc.SetActiveDatasetParams{ID: tk.ID, ActiveDatasetID: &ds.ID})
		for k := 0; k < 4; k++ {
			in, _ := mem.PutBytes(bg, []byte(fmt.Sprint(k)))
			q.UpsertTestcase(bg, sqlc.UpsertTestcaseParams{DatasetID: ds.ID, Codename: fmt.Sprint(k), InputDigest: in.Digest, OutputDigest: in.Digest})
		}
		tasks = append(tasks, tk)
	}
	for name, cells := range users {
		u, _ := q.CreateUser(bg, sqlc.CreateUserParams{Username: name, PasswordHash: "x", PreferredLanguages: []string{}})
		p, _ := q.CreateParticipation(bg, sqlc.CreateParticipationParams{ContestID: c.ID, UserID: u.ID, Ip: []netip.Prefix{}, Hidden: name == hidden})
		for i, cl := range cells {
			if cl == nil {
				continue
			}
			var solvedAt *time.Time
			if cl.solved {
				at := start.Add(cl.at)
				solvedAt = &at
			}
			last := start.Add(cl.at)
			if err := q.UpsertParticipationTaskScore(bg, sqlc.UpsertParticipationTaskScoreParams{ParticipationID: p.ID, TaskID: tasks[i].ID,
				Score: cl.score, SubtaskScores: json.RawMessage(`[]`), IcpcSolved: cl.solved, IcpcAttempts: cl.attempts,
				IcpcSolvedAt: solvedAt, LastSubmissionAt: &last}); err != nil {
				t.Fatal(err)
			}
		}
	}
	return q, c.ID
}

func ptr[T any](v T) *T { return &v }

func TestIOIRankingTiesAndHidden(t *testing.T) {
	q, id := setup(t, "ioi", map[string][2]*cell{
		"ana":  {{score: 100}, {score: 40}},
		"beto": {{score: 70}, {score: 70}},
		"caro": {{score: 100}, nil},
		"dani": {nil, nil},
		"eva":  {{score: 100}, {score: 100}},
	}, "eva")
	r, err := Compute(bg, q, id, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, row := range r.Rows {
		got = append(got, fmt.Sprintf("%d:%s:%g", row.Rank, row.Username, row.Total))
	}
	if want := "1:ana:140,1:beto:140,3:caro:100,4:dani:0"; strings.Join(got, ",") != want {
		t.Fatalf("ranking %s, want %s", strings.Join(got, ","), want)
	}
	if r.Tasks[0].MaxScore != 100 || r.Rows[3].Cells[0].Submitted {
		t.Fatalf("tasks %+v", r.Tasks)
	}
	r, _ = Compute(bg, q, id, Options{IncludeHidden: true})
	if r.Rows[0].Username != "eva" || !r.Rows[0].Hidden {
		t.Fatalf("hidden user not included first: %+v", r.Rows[0])
	}
	var buf bytes.Buffer
	if err := r.WriteCSV(&buf); err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(buf.String()), "\n"); lines[0] != "rank,username,first_name,last_name,team,a,b,total" || lines[1] != "1,eva,,,,100,100,200" {
		t.Fatalf("csv:\n%s", buf.String())
	}
}

func TestICPCRanking(t *testing.T) {
	// ana: 2 solved, penalty 30 + (90 + 20) = 140; beto: 2 solved at 50+60 = 110 → beto first.
	q, id := setup(t, "icpc", map[string][2]*cell{
		"ana":  {{score: 100, solved: true, at: 30 * time.Minute}, {score: 100, solved: true, attempts: 1, at: 90 * time.Minute}},
		"beto": {{score: 100, solved: true, at: 50 * time.Minute}, {score: 100, solved: true, at: 60 * time.Minute}},
		"caro": {{score: 100, solved: true, at: 10 * time.Minute}, {score: 0, attempts: 3, at: 20 * time.Minute}},
	}, "")
	r, err := Compute(bg, q, id, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, row := range r.Rows {
		got = append(got, fmt.Sprintf("%d:%s:%d:%d", row.Rank, row.Username, row.Solved, row.Penalty))
	}
	if want := "1:beto:2:110,2:ana:2:140,3:caro:1:10"; strings.Join(got, ",") != want {
		t.Fatalf("icpc ranking %s, want %s", strings.Join(got, ","), want)
	}
	if !r.ICPC || r.Rows[1].Cells[1].SolvedMinute != 90 || r.Rows[2].Cells[1].Attempts != 3 {
		t.Fatalf("cells %+v", r.Rows)
	}
}
