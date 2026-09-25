package ranking

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

// replayFixture is a contest with one task (GroupMin: 40 + 60) and scored
// submissions at given minutes.
type replayFixture struct {
	pool  *pgxpool.Pool
	q     *sqlc.Queries
	c     sqlc.Contest
	task  sqlc.Task
	ds    sqlc.Dataset
	start time.Time
	parts map[string]sqlc.Participation
}

func newReplayFixture(t *testing.T, team map[string]string) *replayFixture {
	pool := testutil.DB(t)
	f := &replayFixture{pool: pool, q: sqlc.New(pool), start: time.Date(2030, 1, 1, 10, 0, 0, 0, time.UTC), parts: map[string]sqlc.Participation{}}
	cp := db.NewContestParams("rp", f.start, f.start.Add(3*time.Hour))
	f.c, _ = f.q.CreateContest(bg, cp)
	tp := db.NewTaskParams("a", "A")
	tp.ContestID, tp.Num = &f.c.ID, ptr(int32(0))
	f.task, _ = f.q.CreateTask(bg, tp)
	dp := db.NewDatasetParams(f.task.ID, "d")
	dp.ScoreType, dp.ScoreTypeParams = "GroupMin", json.RawMessage(`[[40, "a.*"], [60, "b.*"]]`)
	f.ds, _ = f.q.CreateDataset(bg, dp)
	f.q.SetActiveDataset(bg, sqlc.SetActiveDatasetParams{ID: f.task.ID, ActiveDatasetID: &f.ds.ID})
	for _, c := range []string{"a1", "b1"} {
		f.q.UpsertTestcase(bg, sqlc.UpsertTestcaseParams{DatasetID: f.ds.ID, Codename: c, InputDigest: strings.Repeat("0", 64), OutputDigest: strings.Repeat("0", 64)})
	}
	teams := map[string]int64{}
	for _, name := range []string{"ana", "beto", "caro"} {
		u, _ := f.q.CreateUser(bg, sqlc.CreateUserParams{Username: name, FirstName: strings.ToUpper(name[:1]) + name[1:], PasswordHash: "x", PreferredLanguages: []string{}})
		var teamID *int64
		if code := team[name]; code != "" {
			if _, ok := teams[code]; !ok {
				tm, _ := f.q.CreateTeam(bg, sqlc.CreateTeamParams{Code: code, Name: "Team " + code})
				teams[code] = tm.ID
			}
			id := teams[code]
			teamID = &id
		}
		p, err := f.q.CreateParticipation(bg, sqlc.CreateParticipationParams{ContestID: f.c.ID, UserID: u.ID, Ip: []netip.Prefix{}, TeamID: teamID})
		if err != nil {
			t.Fatal(err)
		}
		f.parts[name] = p
	}
	return f
}

// submit adds an official submission at minute m scored with subtasks st
// (nil: not scored yet).
func (f *replayFixture) submit(t *testing.T, who string, m int, st []float64) {
	t.Helper()
	s, err := f.q.CreateSubmission(bg, sqlc.CreateSubmissionParams{ParticipationID: ptr(f.parts[who].ID), TaskID: f.task.ID,
		SubmittedAt: f.start.Add(time.Duration(m) * time.Minute), Official: true})
	if err != nil {
		t.Fatal(err)
	}
	if st == nil {
		return
	}
	total := 0.0
	for _, v := range st {
		total += v
	}
	det, _ := json.Marshal(st)
	if _, err := f.pool.Exec(bg, `INSERT INTO submission_results (submission_id, dataset_id, compilation_outcome, score, ranking_score_details, scored_at)
		VALUES ($1, $2, 'ok', $3, $4, now())`, s.ID, f.ds.ID, total, det); err != nil {
		t.Fatal(err)
	}
}

func rows(b *Board) string {
	var out []string
	for _, r := range b.Rows {
		out = append(out, fmt.Sprintf("%d:%s:%g", r.Rank, r.Name, r.Total))
	}
	return strings.Join(out, ",")
}

func TestReplayFreezeAndHistory(t *testing.T) {
	f := newReplayFixture(t, nil)
	f.submit(t, "ana", 10, []float64{40, 0})
	f.submit(t, "ana", 50, []float64{0, 60}) // max per subtask: 100
	f.submit(t, "beto", 20, []float64{40, 0})
	f.submit(t, "beto", 170, []float64{40, 60}) // after the freeze
	f.submit(t, "caro", 175, nil)               // pending, after the freeze
	// Without cutoff the replay equals the aggregates the dispatcher keeps.
	r, hist, err := Replay(bg, f.q, f.c.ID, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	b := BuildBoard(r, f.c, f.start.Add(3*time.Hour))
	if got := rows(b); got != "1:Ana:100,1:Beto:100,3:Caro:0" {
		t.Fatalf("live %s", got)
	}
	pa := hist[f.parts["ana"].ID]
	if len(pa) != 2 || pa[0].Total != 40 || pa[1].Total != 100 || !pa[1].Time.Equal(f.start.Add(50*time.Minute)) {
		t.Fatalf("history %+v", pa)
	}
	// Frozen at minute 160 (the last 20 minutes).
	f.pool.Exec(bg, "UPDATE contests SET ranking_freeze_minutes = 20 WHERE id = $1", f.c.ID)
	f.c, _ = f.q.GetContest(bg, f.c.ID)
	cut := FreezeAt(f.c)
	if !cut.Equal(f.start.Add(160*time.Minute)) || !Frozen(f.c, f.start.Add(165*time.Minute)) || Frozen(f.c, f.start.Add(100*time.Minute)) {
		t.Fatalf("freeze at %v", cut)
	}
	r, hist, _ = Replay(bg, f.q, f.c.ID, cut, Options{})
	b = BuildBoard(r, f.c, f.start.Add(165*time.Minute))
	if got := rows(b); got != "1:Ana:100,2:Beto:40,3:Caro:0" || !b.Frozen {
		t.Fatalf("frozen %s", got)
	}
	if beto := b.Rows[1].Cells[0]; beto.Pending != 1 || len(beto.Subtasks) != 2 {
		t.Fatalf("beto's frozen cell %+v", beto)
	}
	if caro := b.Rows[2].Cells[0]; caro.Pending != 1 || !caro.Submitted {
		t.Fatalf("caro's frozen cell %+v", caro)
	}
	if len(hist[f.parts["beto"].ID]) != 1 {
		t.Fatalf("frozen history reveals later results: %+v", hist[f.parts["beto"].ID])
	}
	// Unfrozen by the administrator.
	f.pool.Exec(bg, "UPDATE contests SET ranking_unfrozen = true WHERE id = $1", f.c.ID)
	f.c, _ = f.q.GetContest(bg, f.c.ID)
	if Frozen(f.c, f.start.Add(165*time.Minute)) {
		t.Fatal("still frozen after unfreezing")
	}
	// Diff: only beto changes between the frozen and the live board.
	live, _, _ := Replay(bg, f.q, f.c.ID, nil, Options{})
	next := BuildBoard(live, f.c, f.start.Add(165*time.Minute))
	changed, removed := Diff(b, next)
	if len(changed) != 1 || changed[0].Name != "Beto" || changed[0].Rank != 1 || len(removed) != 0 {
		t.Fatalf("diff %+v %v", changed, removed)
	}
}

func TestBoardPresentation(t *testing.T) {
	f := newReplayFixture(t, map[string]string{"ana": "MX", "beto": "MX"})
	f.submit(t, "ana", 10, []float64{40, 0})
	f.submit(t, "beto", 20, []float64{0, 60})
	f.submit(t, "caro", 30, []float64{40, 0})
	// Teams: the best of each member per subtask.
	f.pool.Exec(bg, "UPDATE contests SET team_mode = true WHERE id = $1", f.c.ID)
	f.c, _ = f.q.GetContest(bg, f.c.ID)
	r, _, _ := Replay(bg, f.q, f.c.ID, nil, Options{})
	b := BuildBoard(r, f.c, f.start)
	if got := rows(b); got != "1:Team MX:100,2:Caro:40" {
		t.Fatalf("teams %s", got)
	}
	var team *BoardRow
	for i := range b.Rows {
		if b.Rows[i].Key == "tMX" {
			team = &b.Rows[i]
		}
	}
	if team == nil || team.Total != 100 || len(team.Members) != 2 || team.Cells[0].Subtasks[1] != 60 {
		t.Fatalf("team row %+v in %s", team, rows(b))
	}
	// Anonymous: no names, teams or institutions; labels by participation.
	f.pool.Exec(bg, "UPDATE contests SET team_mode = false, ranking_anonymous = true, ranking_show_subtasks = false WHERE id = $1", f.c.ID)
	f.c, _ = f.q.GetContest(bg, f.c.ID)
	b = BuildBoard(r, f.c, f.start)
	for _, row := range b.Rows {
		if !strings.HasPrefix(row.Name, "#") || row.Team != "" || row.Cells[0].Subtasks != nil {
			t.Fatalf("anonymous row %+v", row)
		}
	}
	if b.Tasks[0].SubtaskMax != nil {
		t.Fatal("subtask maxima shown with subtasks off")
	}
}
