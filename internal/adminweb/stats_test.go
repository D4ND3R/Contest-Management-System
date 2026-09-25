package adminweb

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
)

// TestTaskStatistics (SPEC_CLOSE D4): per task, the submissions by verdict
// and the first accepted submission (hidden contestants do not count).
func TestTaskStatistics(t *testing.T) {
	f := newFixture(t)
	start := f.contest.StartTime
	score := 0.0
	f.pool.Exec(bg, "UPDATE submission_results SET verdict = 'AC' WHERE submission_id = $1", f.subs[0])
	f.q.SetScore(bg, sqlc.SetScoreParams{SubmissionID: f.subs[1], DatasetID: f.ds.ID, Score: &score, ScoreDetails: []byte(`{}`),
		PublicScore: &score, PublicScoreDetails: []byte(`{}`), RankingScoreDetails: []byte(`[]`), Verdict: ptr("WA")})
	f.pool.Exec(bg, "UPDATE submissions SET submitted_at = $2 WHERE id = $1", f.subs[0], start.Add(37*time.Minute))
	// A hidden tester solved it earlier: not the first AC.
	u, _ := f.q.CreateUser(bg, sqlc.CreateUserParams{Username: "tester", PreferredLanguages: []string{}})
	hp, _ := f.q.CreateParticipation(bg, sqlc.CreateParticipationParams{ContestID: f.contest.ID, UserID: u.ID, Ip: []netip.Prefix{}, Hidden: true})
	lang := "c11"
	hs, _ := f.q.CreateSubmission(bg, sqlc.CreateSubmissionParams{ParticipationID: &hp.ID, TaskID: f.task.ID, SubmittedAt: start.Add(time.Minute),
		Language: &lang, Official: true})
	f.q.EnsureSubmissionResult(bg, sqlc.EnsureSubmissionResultParams{SubmissionID: hs.ID, DatasetID: f.ds.ID})
	full := 100.0
	f.q.SetScore(bg, sqlc.SetScoreParams{SubmissionID: hs.ID, DatasetID: f.ds.ID, Score: &full, ScoreDetails: []byte(`{}`),
		PublicScore: &full, PublicScoreDetails: []byte(`{}`), RankingScoreDetails: []byte(`[]`), Verdict: ptr("AC")})

	code, body := f.login("read_only").Get(fmt.Sprintf("/contests/%d/stats", f.contest.ID))
	if code != 200 {
		t.Fatalf("stats = %d\n%s", code, body)
	}
	i := strings.Index(body, "Submissions by verdict")
	if i < 0 {
		t.Fatalf("no verdict table:\n%s", body)
	}
	table := body[i : i+strings.Index(body[i:], "</table>")]
	// AC twice (ana and the hidden tester), WA once.
	for _, want := range []string{"<td>AC</td><td class=\"num\">2</td>", "<td>WA</td><td class=\"num\">1</td>"} {
		if !strings.Contains(table, want) {
			t.Errorf("verdict table lacks %s:\n%s", want, table)
		}
	}
	if !strings.Contains(body, fmt.Sprintf(`<a href="/submissions/%d">ana</a>`, f.subs[0])) || !strings.Contains(body, "(minute 37)") {
		t.Errorf("first accepted:\n%s", body)
	}
}
