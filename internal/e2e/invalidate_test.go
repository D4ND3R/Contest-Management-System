package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
)

// TestInvalidateSubmissionFromAdminUI (SPEC_CLOSE A3): an accepted
// submission invalidated from the admin UI stops counting at once, the
// contestant sees the mark and the reason, and restoring it brings the
// score back. Both actions are audited.
func TestInvalidateSubmissionFromAdminUI(t *testing.T) {
	s := newStack(t, stackOpts{workers: true, admin: true})
	p := s.addContestant("ana")
	c := s.login("ana")
	src := "#include <stdio.h>\nint main(void){long a,b;scanf(\"%ld %ld\",&a,&b);printf(\"%ld\\n\",a+b);return 0;}\n"
	if code := c.submit("c11", "sum.c", src); code != 200 {
		t.Fatalf("submit = %d", code)
	}
	score := func(want float64, what string) {
		t.Helper()
		deadline := time.Now().Add(60 * time.Second)
		for {
			ts, err := s.q.GetParticipationTaskScore(bg, sqlc.GetParticipationTaskScoreParams{ParticipationID: p.ID, TaskID: s.task.ID})
			if err == nil && ts.Score == want && ts.Pending == 0 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: task score %+v %v, want %v", what, ts, err, want)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	score(100, "judged")
	subs, _ := s.q.ListSubmissionsByParticipationTask(bg, sqlc.ListSubmissionsByParticipationTaskParams{ParticipationID: p.ID, TaskID: s.task.ID})
	id := subs[0].ID
	a := s.adminBrowser(t)
	path := fmt.Sprintf("/submissions/%d", id)
	if code, _ := a.Post(path+"/invalidate", url.Values{"reason": {"  "}}); code != http.StatusUnprocessableEntity {
		t.Fatalf("invalidate without a reason = %d", code)
	}
	if code, body := a.Post(path+"/invalidate", url.Values{"reason": {"Solución copiada"}}); code != 200 || !strings.Contains(body, "Solución copiada") {
		t.Fatalf("invalidate = %d\n%s", code, body)
	}
	score(0, "invalidated")
	if _, body := c.get("/e2e/tasks/sum"); !strings.Contains(body, ">invalidated<") {
		t.Fatalf("task page lacks the mark:\n%s", body)
	}
	if _, body := c.get(fmt.Sprintf("/e2e/submissions/%d", id)); !strings.Contains(body, "invalidated this submission") || !strings.Contains(body, "Solución copiada") {
		t.Fatalf("submission page lacks the reason:\n%s", body)
	}
	if _, body := a.Get(fmt.Sprintf("/contests/%d/submissions", s.contest.ID)); !strings.Contains(body, ">invalidated<") {
		t.Fatalf("admin list lacks the mark:\n%s", body)
	}
	if code, _ := a.Post(path+"/restore", nil); code != 200 {
		t.Fatalf("restore = %d", code)
	}
	score(100, "restored")
	if _, body := c.get("/e2e/tasks/sum"); strings.Contains(body, ">invalidated<") {
		t.Fatal("the mark survived the restore")
	}
	rows, _ := s.q.ListAuditLog(bg, sqlc.ListAuditLogParams{Limit: 50})
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.Action] = true
		if r.Action == "submission.invalidate" && !strings.Contains(string(r.Details), "Solución copiada") {
			t.Errorf("audit row lacks the reason: %s", r.Details)
		}
	}
	for _, act := range []string{"submission.invalidate", "submission.restore"} {
		if !seen[act] {
			t.Errorf("audit log misses %s", act)
		}
	}
}
