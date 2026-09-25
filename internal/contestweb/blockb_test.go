package contestweb

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/auth"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/events"
)

// setContest changes contest columns and tells the server (as the admin
// web server does).
func (f *fixture) setContest(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(bg, "UPDATE contests SET "+sql+" WHERE id = $1", append([]any{f.contest.ID}, args...)...); err != nil {
		t.Fatal(err)
	}
	events.Publish(bg, f.rdb, f.ns, events.Event{Type: events.TypeContest, ContestID: f.contest.ID})
	time.Sleep(100 * time.Millisecond)
}

// TestContestStatus (SPEC_CLOSE B1): drafts do not exist for contestants,
// archived contests are read-only and unlisted; the interface languages and
// the timezone of the contest apply.
func TestContestStatus(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	c := f.client()
	if _, body := f.get(c, "/"); !strings.Contains(body, "/ioi/") {
		t.Fatalf("published contest not listed:\n%s", body)
	}
	f.setContest(t, "status = 'draft'")
	if code, _ := f.get(c, "/ioi/login"); code != 404 {
		t.Fatalf("draft login page = %d", code)
	}
	if _, body := f.get(c, "/"); strings.Contains(body, "/ioi/") {
		t.Fatal("draft contest listed")
	}
	f.setContest(t, "status = 'archived'")
	if _, body := f.get(c, "/"); strings.Contains(body, "/ioi/") {
		t.Fatal("archived contest listed")
	}
	_, body := f.login(c, "ana", "secret")
	if !strings.Contains(body, "archived") {
		t.Fatalf("no archived notice:\n%s", body)
	}
	if code, body := f.submit(c, csrfOf(t, body), "c11", "int main(){}", false); code != 403 || !strings.Contains(body, "closed") {
		t.Fatalf("submission to an archived contest = %d", code)
	}
	resp, _ := c.PostForm(f.url+"/ioi/questions", url.Values{"csrf": {csrfOf(t, body)}, "text": {"hola"}})
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode == 200 && strings.Contains(string(b), "hola") {
		t.Fatal("question accepted in an archived contest")
	}
	// Only Spanish allowed: the interface is Spanish whatever the browser asks.
	f.setContest(t, "status = 'published', allowed_localizations = '{es}', timezone = 'America/Mexico_City'")
	code, page := f.get(c, "/ioi/", "Accept-Language", "en")
	if code != 200 || !strings.Contains(page, `lang="es"`) {
		t.Fatalf("interface language = %d:\n%s", code, page)
	}
	if !strings.Contains(page, "America/Mexico_City") && !strings.Contains(page, "CST") && !strings.Contains(page, "-06") {
		t.Errorf("contest timezone not shown:\n%s", page)
	}
}

// TestPracticeMode (SPEC_CLOSE B2): after the contest, practice accepts
// submissions and marks them unofficial.
func TestPracticeMode(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.setContest(t, "start_time = now() - interval '3 hours', stop_time = now() - interval '1 hour'")
	c := f.client()
	_, body := f.login(c, "ana", "secret")
	if !strings.Contains(body, "The contest is over.") {
		t.Fatalf("finished page:\n%s", body)
	}
	if code, _ := f.submit(c, csrfOf(t, body), "c11", "int main(){}", false); code != 403 {
		t.Fatalf("submission after the end = %d", code)
	}
	f.setContest(t, "practice_enabled = true")
	_, body = f.get(c, "/ioi/")
	if !strings.Contains(body, "Practice mode") {
		t.Fatalf("practice page:\n%s", body)
	}
	if code, body := f.submit(c, csrfOf(t, body), "c11", "int main(){}", false); code != 200 {
		t.Fatalf("practice submission = %d\n%s", code, body)
	}
	subs, _ := f.q.ListSubmissionsByParticipation(bg, f.part.ID)
	if len(subs) != 1 || subs[0].Official {
		t.Fatalf("practice submission %+v", subs)
	}
}

// TestClockFollowsExtension (SPEC_CLOSE B2): changing the times sends a
// "clock" event to open pages, whose clock endpoint returns the new end.
func TestClockFollowsExtension(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	c := f.client()
	f.login(c, "ana", "secret")
	clock := func() (string, int64) {
		t.Helper()
		_, body := f.get(c, "/ioi/clock")
		var v struct {
			Phase string
			End   int64
		}
		if err := json.Unmarshal([]byte(body), &v); err != nil {
			t.Fatalf("clock %q: %v", body, err)
		}
		return v.Phase, v.End
	}
	phase, end := clock()
	if phase != "running" || end == 0 {
		t.Fatalf("clock %s %d", phase, end)
	}
	c.Timeout = 0
	req, _ := http.NewRequest("GET", f.url+"/ioi/events", nil)
	ctx, cancel := context.WithTimeout(bg, 5*time.Second)
	defer cancel()
	resp, err := c.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	br.ReadString('\n')
	time.Sleep(100 * time.Millisecond)
	f.setContest(t, "stop_time = stop_time + interval '10 minutes'")
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("no clock event: %v", err)
		}
		if strings.HasPrefix(line, "event: clock") {
			break
		}
	}
	if _, end2 := clock(); end2-end != 10*60*1000 {
		t.Fatalf("clock moved by %d ms", end2-end)
	}
}

// TestTeamSharedSubmissions (SPEC_CLOSE B3): in a team contest members
// see and open each other's submissions (with the author), share the
// submission limits and the task score, and get each other's live
// updates; in an individual contest they do not.
func TestTeamSharedSubmissions(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	team, err := f.q.CreateTeam(bg, sqlc.CreateTeamParams{Code: "ARG", Name: "Argentina"})
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashPassword("secret")
	beto, _ := f.q.CreateUser(bg, sqlc.CreateUserParams{Username: "beto", PasswordHash: hash, PreferredLanguages: []string{}})
	bp, err := f.q.CreateParticipation(bg, sqlc.CreateParticipationParams{ContestID: f.contest.ID, UserID: beto.ID, TeamID: &team.ID, Ip: []netip.Prefix{}})
	if err != nil {
		t.Fatal(err)
	}
	f.pool.Exec(bg, "UPDATE participations SET team_id = $1 WHERE id = $2", team.ID, f.part.ID)
	f.setContest(t, "team_mode = true, max_submission_number = 2")

	ana, bc := f.client(), f.client()
	_, body := f.login(ana, "ana", "secret")
	if code, _ := f.submit(ana, csrfOf(t, body), "c11", "int main(){}", false); code != 200 {
		t.Fatalf("ana submits = %d", code)
	}
	subs, _ := f.q.ListSubmissionsByParticipation(bg, f.part.ID)
	f.login(bc, "beto", "secret")
	_, page := f.get(bc, "/ioi/tasks/sum")
	if !strings.Contains(page, fmt.Sprintf(`id="sub-%d"`, subs[0].ID)) || !strings.Contains(page, "· ana") {
		t.Fatalf("beto does not see ana's submission:\n%s", page)
	}
	if code, _ := f.get(bc, fmt.Sprintf("/ioi/submissions/%d", subs[0].ID)); code != 200 {
		t.Fatalf("beto opens ana's submission = %d", code)
	}
	// Live: beto's page hears about ana's submissions.
	bc.Timeout = 0
	req, _ := http.NewRequest("GET", f.url+"/ioi/events", nil)
	ctx, cancel := context.WithTimeout(bg, 5*time.Second)
	defer cancel()
	resp, err := bc.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(resp.Body)
	br.ReadString('\n')
	time.Sleep(100 * time.Millisecond)
	events.Publish(bg, f.rdb, f.ns, events.Event{Type: events.TypeSubmission, ParticipationID: f.part.ID, SubmissionID: subs[0].ID, Status: "scored"})
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("no team event: %v", err)
		}
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, fmt.Sprintf(`"submission_id":%d`, subs[0].ID)) {
			break
		}
	}
	resp.Body.Close()
	bc.Timeout = 10 * time.Second
	// The limit counts the team: one more, then no more.
	if code, _ := f.submit(bc, csrfOf(t, page), "c11", "int main(){}", false); code != 200 {
		t.Fatalf("beto's first submission = %d", code)
	}
	if code, body := f.submit(bc, csrfOf(t, page), "c11", "int main(){}", false); code != 429 && code != 403 || !strings.Contains(body, "maximum") {
		t.Fatalf("third team submission = %d\n%s", code, body)
	}
	// Scores merge per subtask: 30 + 70 = 100 for both.
	f.q.UpsertParticipationTaskScore(bg, sqlc.UpsertParticipationTaskScoreParams{ParticipationID: f.part.ID, TaskID: f.task.ID, Score: 30, SubtaskScores: json.RawMessage(`[30, 0]`)})
	f.q.UpsertParticipationTaskScore(bg, sqlc.UpsertParticipationTaskScoreParams{ParticipationID: bp.ID, TaskID: f.task.ID, Score: 70, SubtaskScores: json.RawMessage(`[0, 70]`)})
	for _, c := range []*http.Client{ana, bc} {
		if _, body := f.get(c, "/ioi/"); !strings.Contains(body, ">100 / 100<") {
			t.Fatalf("team score not merged:\n%s", body)
		}
	}
	// Individual contest: nothing is shared.
	f.setContest(t, "team_mode = false")
	if code, _ := f.get(bc, fmt.Sprintf("/ioi/submissions/%d", subs[0].ID)); code != 404 {
		t.Fatalf("individual contest: beto opens ana's submission = %d", code)
	}
	if _, body := f.get(bc, "/ioi/"); !strings.Contains(body, ">70 / 100<") {
		t.Fatal("individual contest shows the team score")
	}
}

// scoreSubmission simulates the dispatcher: compiled (with a compiler
// warning) and scored 100 (public 50).
func (f *fixture) scoreSubmission(t *testing.T, id int64) {
	t.Helper()
	f.q.EnsureSubmissionResult(bg, sqlc.EnsureSubmissionResultParams{SubmissionID: id, DatasetID: f.ds.ID})
	ok := "ok"
	f.q.SetCompilationResult(bg, sqlc.SetCompilationResultParams{SubmissionID: id, DatasetID: f.ds.ID, CompilationOutcome: &ok,
		CompilationText: "Compilation succeeded", CompilationStderr: "warning: unused variable", TestcasesTotal: 2})
	full, pub := 100.0, 50.0
	det := json.RawMessage(`{"type":"sum","max_score":100,"testcases":[{"codename":"0","public":true,"outcome":1,"text":"Output is correct","time":0.01,"memory":1024,"status":"ok"}]}`)
	f.q.SetScore(bg, sqlc.SetScoreParams{SubmissionID: id, DatasetID: f.ds.ID, Score: &full, ScoreDetails: det, PublicScore: &pub,
		PublicScoreDetails: det, RankingScoreDetails: json.RawMessage(`[100]`)})
	f.q.UpsertParticipationTaskScore(bg, sqlc.UpsertParticipationTaskScoreParams{ParticipationID: f.part.ID, TaskID: f.task.ID,
		Score: 100, SubtaskScores: json.RawMessage(`[]`)})
}

// TestScoreVisibilityAndCompilerOutput (SPEC_CLOSE B4): scores shown
// always, only after the end or never; the compiler's messages can be
// hidden.
func TestScoreVisibilityAndCompilerOutput(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	c := f.client()
	_, page := f.login(c, "ana", "secret")
	f.submit(c, csrfOf(t, page), "c11", "int main(){}", false)
	subs, _ := f.q.ListSubmissionsByParticipation(bg, f.part.ID)
	id := subs[0].ID
	f.scoreSubmission(t, id)
	detail := fmt.Sprintf("/ioi/submissions/%d", id)
	if _, body := f.get(c, "/ioi/"); !strings.Contains(body, "100 / 100") {
		t.Fatalf("score not shown:\n%s", body)
	}
	if _, body := f.get(c, detail); !strings.Contains(body, "warning: unused variable") || !strings.Contains(body, "Output is correct") {
		t.Fatalf("details:\n%s", body)
	}
	f.setContest(t, "score_visibility = 'never', show_compilation_output = false")
	for _, path := range []string{"/ioi/", "/ioi/tasks/sum", detail, detail + "/row"} {
		_, body := f.get(c, path)
		if strings.Contains(body, "/ 100") || strings.Contains(body, "Output is correct") {
			t.Errorf("%s shows scores:\n%s", path, body)
		}
	}
	_, body := f.get(c, detail)
	if strings.Contains(body, "warning: unused variable") || !strings.Contains(body, "Compilation succeeded") {
		t.Fatalf("compiler output toggle:\n%s", body)
	}
	if _, body := f.get(c, "/ioi/"); !strings.Contains(body, "Scores are not shown") {
		t.Fatalf("no notice:\n%s", body)
	}
	f.setContest(t, "score_visibility = 'after'")
	if _, body := f.get(c, "/ioi/"); !strings.Contains(body, "when the contest is over") || strings.Contains(body, "/ 100") {
		t.Fatalf("after, during the contest:\n%s", body)
	}
	f.setContest(t, "start_time = now() - interval '3 hours', stop_time = now() - interval '1 hour'")
	if _, body := f.get(c, "/ioi/"); !strings.Contains(body, "100 / 100") {
		t.Fatalf("after, once over:\n%s", body)
	}
}

// TestTokens (SPEC_CLOSE B4): the contestant sees the tokens available and
// plays one; both the contest and the task rules apply; no double spending.
func TestTokens(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	c := f.client()
	_, page := f.login(c, "ana", "secret")
	if strings.Contains(page, "use a token") {
		t.Fatal("token offered while tokens are disabled")
	}
	f.submit(c, csrfOf(t, page), "c11", "int main(){}", false)
	f.submit(c, csrfOf(t, page), "c11", "int main(){return 0;}", false)
	subs, _ := f.q.ListSubmissionsByParticipation(bg, f.part.ID)
	for _, s := range subs {
		f.scoreSubmission(t, s.ID)
	}
	f.pool.Exec(bg, "UPDATE tasks SET token_mode = 'infinite' WHERE id = $1", f.task.ID)
	f.setContest(t, "token_mode = 'finite', token_gen_initial = 1, token_gen_number = 0")
	_, page = f.get(c, "/ioi/tasks/sum")
	if !strings.Contains(page, "Tokens available: 1.") || strings.Count(page, "use a token") != 2 {
		t.Fatalf("token offer:\n%s", page)
	}
	play := func(id int64) (int, string) {
		resp, err := c.PostForm(fmt.Sprintf("%s/ioi/submissions/%d/token", f.url, id), url.Values{"csrf": {csrfOf(t, page)}})
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	code, body := play(subs[0].ID)
	if code != 200 || !strings.Contains(body, "Tokens available: 0.") || strings.Contains(body, "use a token") {
		t.Fatalf("after the token = %d:\n%s", code, body)
	}
	// The tokened submission shows its full score.
	if _, row := f.get(c, fmt.Sprintf("/ioi/submissions/%d/row", subs[0].ID)); !strings.Contains(row, "100 / 100") || !strings.Contains(row, "★") {
		t.Fatalf("tokened row:\n%s", row)
	}
	if code, _ := play(subs[1].ID); code != 409 {
		t.Fatalf("second token = %d", code)
	}
	if code, _ := play(subs[0].ID); code != 409 {
		t.Fatalf("token on a tokened submission = %d", code)
	}
	var n int
	f.pool.QueryRow(bg, "SELECT count(*) FROM tokens").Scan(&n)
	if n != 1 {
		t.Fatalf("%d tokens stored", n)
	}
	// The task's own rules apply too.
	f.pool.Exec(bg, "UPDATE tasks SET token_mode = 'disabled' WHERE id = $1", f.task.ID)
	f.setContest(t, "token_gen_initial = 5")
	if _, page := f.get(c, "/ioi/tasks/sum"); strings.Contains(page, "use a token") {
		t.Fatal("token offered on a task without tokens")
	}
}
