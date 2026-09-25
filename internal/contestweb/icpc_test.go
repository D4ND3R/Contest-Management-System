package contestweb

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/i18n"
)

func mapValues(m map[string]string) []string {
	var out []string
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// setResult simulates the dispatcher scoring a submission.
func (f *fixture) setResult(t *testing.T, id int64, score float64, verdict *string, text, status string) {
	t.Helper()
	f.q.EnsureSubmissionResult(bg, sqlc.EnsureSubmissionResultParams{SubmissionID: id, DatasetID: f.ds.ID})
	ok := "ok"
	f.q.SetCompilationResult(bg, sqlc.SetCompilationResultParams{SubmissionID: id, DatasetID: f.ds.ID, CompilationOutcome: &ok,
		CompilationText: "Compilation succeeded", TestcasesTotal: 2})
	det := json.RawMessage(fmt.Sprintf(`{"type":"sum","max_score":100,"testcases":[{"codename":"0","public":true,"outcome":%v,"text":%q,"status":%q}]}`,
		score/100, text, status))
	if err := f.q.SetScore(bg, sqlc.SetScoreParams{SubmissionID: id, DatasetID: f.ds.ID, Score: &score, ScoreDetails: det, PublicScore: &score,
		PublicScoreDetails: det, RankingScoreDetails: json.RawMessage(`[]`), Verdict: verdict}); err != nil {
		t.Fatal(err)
	}
}

// TestICPCVerdicts (SPEC_CLOSE B7): contestants of ICPC contests see a
// binary verdict, never a score nor testcase details; the overview shows
// solved tasks and rejected attempts.
func TestICPCVerdicts(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.setContest(t, "scoring_mode = 'icpc'")
	c := f.client()
	_, page := f.login(c, "ana", "secret")
	for i := 0; i < 3; i++ {
		if code, body := f.submit(c, csrfOf(t, page), "c11", fmt.Sprintf("int main(){return %d;}", i), false); code != 200 && code != 303 {
			t.Fatalf("submit = %d\n%s", code, body)
		}
	}
	subs, _ := f.q.ListSubmissionsByParticipation(bg, f.part.ID)
	if len(subs) != 3 {
		t.Fatalf("%d submissions", len(subs))
	}
	tle, ac := "TLE", "AC"
	var order []int64
	for _, s := range subs {
		order = append(order, s.ID)
	}
	// Oldest: time limit; then an old result without a verdict; then AC.
	oldest, legacy, last := minID(order), midID(order), maxID(order)
	f.setResult(t, oldest, 50, &tle, "Execution timed out", "timeout")
	f.setResult(t, legacy, 30, nil, "Output isn't correct", "ok")
	f.setResult(t, last, 100, &ac, "Output is correct", "ok")
	solvedAt := time.Now()
	f.q.UpsertParticipationTaskScore(bg, sqlc.UpsertParticipationTaskScoreParams{ParticipationID: f.part.ID, TaskID: f.task.ID,
		Score: 100, SubtaskScores: json.RawMessage(`[]`), IcpcSolved: true, IcpcAttempts: 2, IcpcSolvedAt: &solvedAt})

	_, body := f.get(c, "/ioi/tasks/sum")
	for _, want := range []string{"Verdict", "Time limit exceeded", "Accepted", "Rejected"} {
		if !strings.Contains(body, want) {
			t.Errorf("task page lacks %q", want)
		}
	}
	if strings.Contains(body, "/ 100") {
		t.Errorf("task page shows scores:\n%s", body)
	}
	_, body = f.get(c, fmt.Sprintf("/ioi/submissions/%d", oldest))
	if !strings.Contains(body, "Time limit exceeded") || strings.Contains(body, "Execution timed out") || strings.Contains(body, "/ 100") {
		t.Errorf("submission page:\n%s", body)
	}
	_, body = f.get(c, fmt.Sprintf("/ioi/submissions/%d/row", last))
	if !strings.Contains(body, "Accepted") || strings.Contains(body, "/ 100") {
		t.Errorf("row:\n%s", body)
	}
	_, body = f.get(c, "/ioi/")
	if !strings.Contains(body, "Accepted") || !strings.Contains(body, "2 rejected") || strings.Contains(body, "/ 100") {
		t.Errorf("overview:\n%s", body)
	}
	// Spanish.
	for _, k := range append([]string{"Rejected"}, mapValues(verdictNames)...) {
		if !i18n.Has("es", k) {
			t.Errorf("verdict %q has no Spanish translation", k)
		}
	}
	_, body = f.get(c, "/ioi/tasks/sum", "Accept-Language", "es")
	if !strings.Contains(body, "Aceptado") || !strings.Contains(body, "Tiempo límite excedido") {
		t.Errorf("Spanish verdicts:\n%s", body)
	}
}

func minID(ids []int64) int64 {
	m := ids[0]
	for _, v := range ids {
		m = min(m, v)
	}
	return m
}

func maxID(ids []int64) int64 {
	m := ids[0]
	for _, v := range ids {
		m = max(m, v)
	}
	return m
}

func midID(ids []int64) int64 {
	for _, v := range ids {
		if v != minID(ids) && v != maxID(ids) {
			return v
		}
	}
	return 0
}

// TestScoreAdjustmentShown (SPEC_CLOSE D2): the contestant sees an adjusted
// score with the adjustment and its reason.
func TestScoreAdjustmentShown(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	c := f.client()
	f.login(c, "ana", "secret")
	f.q.UpsertParticipationTaskScore(bg, sqlc.UpsertParticipationTaskScoreParams{ParticipationID: f.part.ID, TaskID: f.task.ID,
		Score: 50, SubtaskScores: json.RawMessage(`[]`)})
	f.q.CreateScoreAdjustment(bg, sqlc.CreateScoreAdjustmentParams{ParticipationID: f.part.ID, TaskID: f.task.ID, Points: 10, Reason: "wrong test data"})
	f.q.ApplyScoreAdjustment(bg, sqlc.ApplyScoreAdjustmentParams{ParticipationID: f.part.ID, TaskID: f.task.ID, Points: 10})
	_, body := f.get(c, "/ioi/")
	if !strings.Contains(body, "60 / 100") || !strings.Contains(body, "adjusted by the organizers: &#43;10") || !strings.Contains(body, "10: wrong test data") {
		t.Fatalf("overview:\n%s", body)
	}
}
