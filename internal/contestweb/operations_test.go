package contestweb

import (
	"fmt"
	"io"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
)

// TestEmergencyControls (SPEC_IOI §9.3): while the organizers pause the
// contest, or close one task, submissions and user tests are refused with
// the reason; everything else keeps working.
func TestEmergencyControls(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	c := f.client()
	_, page := f.login(c, "ana", "secret")
	csrf := csrfOf(t, page)
	reload := func() { f.srv.cache.invalidateContest(0) }

	if err := f.q.SetContestPaused(bg, sqlc.SetContestPausedParams{ID: f.contest.ID, Paused: true, Message: "Checking a testcase."}); err != nil {
		t.Fatal(err)
	}
	reload()
	_, body := f.get(c, "/ioi/tasks/sum")
	if !strings.Contains(body, "Submissions are paused by the organizers. Checking a testcase.") || strings.Contains(body, `name="language"`) {
		t.Fatalf("paused task page:\n%s", body)
	}
	if _, body := f.get(c, "/ioi/"); !strings.Contains(body, `<p class="alert" role="status">Submissions are paused by the organizers. Checking a testcase.</p>`) {
		t.Fatalf("no pause banner:\n%s", body)
	}
	if code, body := f.submit(c, csrf, "c11", "int main(){}", true); code != 403 || !strings.Contains(body, "Submissions are paused by the organizers.") {
		t.Fatalf("submit while paused: %d %s", code, body)
	}
	if subs, _ := f.q.ListSubmissionsByParticipation(bg, f.part.ID); len(subs) != 0 {
		t.Fatal("a submission was stored during the pause")
	}
	// Questions still work.
	if code, _ := f.get(c, "/ioi/communication"); code != 200 {
		t.Fatalf("communication while paused: %d", code)
	}

	f.q.SetContestPaused(bg, sqlc.SetContestPausedParams{ID: f.contest.ID})
	f.q.SetTaskSubmissionsClosed(bg, sqlc.SetTaskSubmissionsClosedParams{ID: f.task.ID, Closed: true})
	reload()
	if _, body := f.get(c, "/ioi/tasks/sum"); !strings.Contains(body, "Submissions to this task are closed.") || strings.Contains(body, "paused") {
		t.Fatalf("closed task page:\n%s", body)
	}
	if code, _ := f.submit(c, csrf, "c11", "int main(){}", true); code != 403 {
		t.Fatalf("submit to a closed task: %d", code)
	}

	f.q.SetTaskSubmissionsClosed(bg, sqlc.SetTaskSubmissionsClosedParams{ID: f.task.ID})
	reload()
	if code, body := f.submit(c, csrf, "c11", "int main(){}", true); code != 200 {
		t.Fatalf("submit after reopening: %d %s", code, body)
	}

	// The submission's receipt is in the audit chain (SPEC_IOI §13).
	subs, _ := f.q.ListSubmissionsByParticipation(bg, f.part.ID)
	files, _ := f.q.ListSubmissionFiles(bg, subs[0].ID)
	var actor, details string
	if err := f.pool.QueryRow(bg, "SELECT actor, details::text FROM audit_log WHERE action = 'submission.received' AND target_id = $1",
		subs[0].ID).Scan(&actor, &details); err != nil {
		t.Fatalf("no receipt: %v", err)
	}
	if actor != "contestant:ana" || len(files) != 1 || !strings.Contains(details, files[0].Digest) || !strings.Contains(details, `"task": "sum"`) {
		t.Fatalf("receipt %s %s (files %+v)", actor, details, files)
	}
}

// TestAppeals (SPEC_IOI §14): after their contest a contestant appeals a
// task (optionally one of their submissions) until the deadline, sees the
// staff's answer, and cannot appeal once the deadline passed.
func TestAppeals(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	c := f.client()
	_, page := f.login(c, "ana", "secret")
	csrf := csrfOf(t, page)
	f.submit(c, csrf, "c11", "int main(){}", true)
	subs, _ := f.q.ListSubmissionsByParticipation(bg, f.part.ID)
	reload := func() { f.srv.cache.invalidateContest(0) }
	if code, _ := f.get(c, "/ioi/appeals"); code != 404 {
		t.Fatalf("appeals without a deadline = %d", code)
	}
	// The contest ends; appeals are open for a day.
	until := time.Now().Add(24 * time.Hour)
	f.pool.Exec(bg, "UPDATE contests SET start_time = now() - interval '3 hours', stop_time = now() - interval '1 minute', appeals_until = $1", until)
	reload()
	code, body := f.get(c, "/ioi/appeals")
	if code != 200 || !strings.Contains(body, "Send an appeal") || !strings.Contains(body, `href="/ioi/appeals"`) {
		t.Fatalf("appeals page: %d\n%s", code, body)
	}
	post := func(v url.Values) (int, string) {
		v.Set("csrf", csrf)
		resp, err := c.PostForm(f.url+"/ioi/appeals", v)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := post(url.Values{"task": {"sum"}, "submission": {"999999"}, "text": {"x"}}); code != 400 || !strings.Contains(body, "not yours") {
		t.Fatalf("foreign submission: %d", code)
	}
	if code, _ := post(url.Values{"task": {"sum"}, "text": {""}}); code != 400 {
		t.Fatalf("empty appeal: %d", code)
	}
	if code, body := post(url.Values{"task": {"sum"}, "submission": {fmt.Sprintf("#%d", subs[0].ID)}, "text": {"Testcase 3 is wrong."}}); code != 200 ||
		!strings.Contains(body, "Your appeal was sent.") || !strings.Contains(body, "waiting for an answer") {
		t.Fatalf("appeal: %d\n%s", code, body)
	}
	rows, _ := f.q.ListAppealsByParticipation(bg, f.part.ID)
	if len(rows) != 1 || rows[0].SubmissionID == nil || *rows[0].SubmissionID != subs[0].ID {
		t.Fatalf("appeals %+v", rows)
	}
	f.q.AnswerAppeal(bg, sqlc.AnswerAppealParams{ID: rows[0].ID, Status: "accepted", Response: "Fixed; the task was rejudged."})
	if _, body := f.get(c, "/ioi/appeals"); !strings.Contains(body, "Fixed; the task was rejudged.") || !strings.Contains(body, `<span class="tag ok">accepted</span>`) {
		t.Fatal("answer not shown")
	}
	// After the deadline: the page stays, new appeals are refused.
	f.pool.Exec(bg, "UPDATE contests SET appeals_until = now() - interval '1 second'")
	reload()
	if _, body := f.get(c, "/ioi/appeals"); !strings.Contains(body, "Appeals are closed.") || strings.Contains(body, "Send an appeal") {
		t.Fatal("closed appeals page")
	}
	if code, _ := post(url.Values{"task": {"sum"}, "text": {"late"}}); code != 403 {
		t.Fatalf("late appeal: %d", code)
	}
}
