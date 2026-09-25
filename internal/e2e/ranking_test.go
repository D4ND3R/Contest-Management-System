package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/ranking"
)

func rwsBoard(t *testing.T, url string) *ranking.Board {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	var b ranking.Board
	json.NewDecoder(resp.Body).Decode(&b)
	return &b
}

// TestRankingWebLive judges a real submission and measures how long the
// public scoreboard takes to show it after scoring (SPEC §2: < 1 s).
func TestRankingWebLive(t *testing.T) {
	s := newStack(t, stackOpts{workers: true, ranking: true})
	s.addContestant("ana")
	s.addContestant("beto")
	c := s.login("ana")
	url := s.rwsURL + "/e2e/ranking.json"
	deadline := time.Now().Add(20 * time.Second)
	for b := rwsBoard(t, url); b == nil; b = rwsBoard(t, url) {
		if time.Now().After(deadline) {
			t.Fatal("the board never reached the ranking web server")
		}
		time.Sleep(100 * time.Millisecond)
	}
	src := "#include <stdio.h>\nint main(void){long a,b;scanf(\"%ld %ld\",&a,&b);printf(\"%ld\\n\",a+b);return 0;}\n"
	if code := c.submit("c11", "sum.c", src); code != 200 {
		t.Fatalf("submit = %d", code)
	}
	deadline = time.Now().Add(60 * time.Second)
	var shown time.Time
	for {
		if b := rwsBoard(t, url); b != nil && len(b.Rows) > 0 && b.Rows[0].Name == "ana" && b.Rows[0].Total == 100 {
			shown = time.Now()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the score never reached the scoreboard")
		}
		time.Sleep(20 * time.Millisecond)
	}
	var scored time.Time
	if err := s.pool.QueryRow(bg, `SELECT max(scored_at) FROM submission_results`).Scan(&scored); err != nil {
		t.Fatal(err)
	}
	lag := shown.Sub(scored)
	t.Logf("scoreboard updated %v after scoring", lag)
	if lag > time.Second {
		t.Fatalf("scoreboard lag %v > 1 s", lag)
	}
	// The HTML page shows it too, and the per-user history has the point.
	resp, _ := http.Get(s.rwsURL + "/e2e/")
	var sb strings.Builder
	buf := make([]byte, 1<<16)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	resp.Body.Close()
	if !strings.Contains(sb.String(), `id="r-p`) || !strings.Contains(sb.String(), ">100<") {
		t.Fatalf("board page:\n%s", sb.String())
	}
	p, _ := s.q.GetParticipationByContestUser(bg, sqlc.GetParticipationByContestUserParams{ContestID: s.contest.ID, UserID: mustUser(t, s, "ana")})
	resp, _ = http.Get(s.rwsURL + "/e2e/u/" + ranking.ParticipationKey(p.ID) + "?format=json")
	var pts []ranking.Point
	json.NewDecoder(resp.Body).Decode(&pts)
	resp.Body.Close()
	if len(pts) != 1 || pts[0].Total != 100 {
		t.Fatalf("history %+v", pts)
	}
}

func mustUser(t *testing.T, s *stack, name string) int64 {
	u, err := s.q.GetUserByUsername(bg, name)
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}
