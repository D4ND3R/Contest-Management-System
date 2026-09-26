package contestweb

import (
	"strings"
	"testing"

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
