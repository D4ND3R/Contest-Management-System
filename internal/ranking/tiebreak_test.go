package ranking

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
)

func setTieBreak(t *testing.T, q *sqlc.Queries, id int64, tieBreak string) sqlc.Contest {
	t.Helper()
	c, err := q.GetContest(bg, id)
	if err != nil {
		t.Fatal(err)
	}
	up := db.ContestToUpdate(c)
	up.RankingTieBreak = tieBreak
	if c, err = q.UpdateContest(bg, up); err != nil {
		t.Fatal(err)
	}
	return c
}

func ranked(r *Ranking) string {
	var out []string
	for _, row := range r.Rows {
		out = append(out, fmt.Sprintf("%d:%s", row.Place, row.Username))
	}
	return strings.Join(out, ",")
}

// TestTieBreakByTime: with the "time" tie-break, equal totals are ordered
// by when they were reached (IOI) or by the last problem solved (ICPC).
func TestTieBreakByTime(t *testing.T) {
	q, id := setup(t, "ioi", map[string][2]*cell{
		"ana":  {{score: 100, at: 30 * time.Minute}, {score: 40, at: 90 * time.Minute}},
		"beto": {{score: 70, at: 20 * time.Minute}, {score: 70, at: 60 * time.Minute}},
		"caro": {{score: 100, at: 10 * time.Minute}, nil},
	}, "")
	r, _ := Compute(bg, q, id, Options{})
	if got := ranked(r); got != "1:ana,1:beto,3:caro" {
		t.Fatalf("shared: %s", got)
	}
	setTieBreak(t, q, id, "time")
	r, _ = Compute(bg, q, id, Options{})
	if got := ranked(r); got != "1:beto,2:ana,3:caro" || *r.Rows[0].ReachedS != 3600 || r.TieBreak != "time" {
		t.Fatalf("by time: %s (%+v)", got, r.Rows[0])
	}

	// ICPC: equal problems and penalty (10+50 = 30+30), the earlier last
	// accepted submission first.
	q, id = setup(t, "icpc", map[string][2]*cell{
		"xavi": {{score: 100, solved: true, at: 10 * time.Minute}, {score: 100, solved: true, at: 50 * time.Minute}},
		"yago": {{score: 100, solved: true, at: 30 * time.Minute}, {score: 100, solved: true, at: 30 * time.Minute}},
	}, "")
	setTieBreak(t, q, id, "time")
	r, _ = Compute(bg, q, id, Options{})
	if got := ranked(r); got != "1:yago,2:xavi" || r.Rows[0].Penalty != r.Rows[1].Penalty {
		t.Fatalf("icpc by time: %s", got)
	}
}

// TestTieBreakReplayAndTeams: the replayed (frozen) ranking and the team
// scoreboards follow the tie-break too, with times counted from each
// participant's own start.
func TestTieBreakReplayAndTeams(t *testing.T) {
	f := newReplayFixture(t, nil)
	f.submit(t, "ana", 10, []float64{40, 0})
	f.submit(t, "ana", 50, []float64{0, 60})
	f.submit(t, "beto", 20, []float64{40, 60})
	f.submit(t, "caro", 5, []float64{40, 0})
	f.submit(t, "caro", 70, []float64{40, 0})
	f.c = setTieBreak(t, f.q, f.c.ID, "time")
	r, _, err := Replay(bg, f.q, f.c.ID, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := ranked(r); got != "1:beto,2:ana,3:caro" {
		t.Fatalf("replay: %s", got)
	}
	// Frozen at minute 30: ana and caro have 40 each; ana started 8
	// minutes late, so she reached it 2 minutes into her contest.
	f.pool.Exec(bg, "UPDATE participations SET delay_time_s = 480 WHERE id = $1", f.parts["ana"].ID)
	cutoff := f.start.Add(30 * time.Minute)
	r, _, _ = Replay(bg, f.q, f.c.ID, &cutoff, Options{})
	if got := ranked(r); got != "1:beto,2:ana,3:caro" {
		t.Fatalf("frozen replay: %s", got)
	}
	f.pool.Exec(bg, "UPDATE participations SET delay_time_s = 0 WHERE id = $1", f.parts["ana"].ID)
	r, _, _ = Replay(bg, f.q, f.c.ID, &cutoff, Options{})
	if got := ranked(r); got != "1:beto,2:caro,3:ana" {
		t.Fatalf("frozen replay without delay: %s", got)
	}

	// Teams: the team reached 100 when its later member scored subtask 2.
	g := newReplayFixture(t, map[string]string{"ana": "MX", "beto": "MX"})
	g.submit(t, "ana", 10, []float64{40, 0})
	g.submit(t, "beto", 30, []float64{0, 60})
	g.submit(t, "caro", 35, []float64{40, 60})
	g.pool.Exec(bg, "UPDATE contests SET team_mode = true WHERE id = $1", g.c.ID)
	g.c = setTieBreak(t, g.q, g.c.ID, "shared")
	r, _, _ = Replay(bg, g.q, g.c.ID, nil, Options{})
	if got := rows(BuildBoard(r, g.c, g.start)); got != "1:Caro:100,1:Team MX:100" {
		t.Fatalf("teams shared: %s", got)
	}
	g.c = setTieBreak(t, g.q, g.c.ID, "time")
	r, _, _ = Replay(bg, g.q, g.c.ID, nil, Options{})
	if got := rows(BuildBoard(r, g.c, g.start)); got != "1:Team MX:100,2:Caro:100" {
		t.Fatalf("teams by time: %s", got)
	}
	g.submit(t, "caro", 40, []float64{40, 60})
	g.submit(t, "ana", 25, []float64{40, 60})
	r, _, _ = Replay(bg, g.q, g.c.ID, nil, Options{})
	if got := rows(BuildBoard(r, g.c, g.start)); got != "1:Team MX:100,2:Caro:100" {
		t.Fatalf("teams after ana's full score at 25: %s", got)
	}
}
