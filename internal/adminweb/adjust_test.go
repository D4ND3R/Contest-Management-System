package adminweb

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
)

// TestScoreAdjust (SPEC_CLOSE D2): an adjustment needs a reason, changes
// the task score at once, stays in the participation's history and in the
// audit log.
func TestScoreAdjust(t *testing.T) {
	f := newFixture(t)
	path := fmt.Sprintf("/participations/%d/adjust", f.part.ID)
	form := func(points, reason string) url.Values {
		return url.Values{"task_id": {fmt.Sprint(f.task.ID)}, "points": {points}, "reason": {reason}}
	}
	if code, _ := f.login("messaging").Post(path, form("5", "checker bug on test 7")); code != http.StatusForbidden {
		t.Fatalf("messaging adjust = %d", code)
	}
	a := f.login("all")
	for _, bad := range []url.Values{form("5", "bug"), form("0", "checker bug on test 7"), form("x", "checker bug on test 7"),
		{"task_id": {"999999"}, "points": {"5"}, "reason": {"checker bug on test 7"}}} {
		if code, _ := a.Post(path, bad); code != http.StatusUnprocessableEntity {
			t.Errorf("%v = %d", bad, code)
		}
	}
	if code, body := a.Post(path, form("-2.5", "late submission accepted by mistake")); code != 200 || !strings.Contains(body, "Score adjusted") {
		t.Fatalf("adjust = %d\n%s", code, body)
	}
	if code, _ := a.Post(path, form("7.5", "checker bug on test 7")); code != 200 {
		t.Fatalf("adjust = %d", code)
	}
	ts, _ := f.q.GetParticipationTaskScore(bg, sqlc.GetParticipationTaskScoreParams{ParticipationID: f.part.ID, TaskID: f.task.ID})
	if ts.Score != 105 || ts.Adjustment != 5 {
		t.Fatalf("task score %v adjustment %v", ts.Score, ts.Adjustment)
	}
	_, body := a.Get(fmt.Sprintf("/participations/%d", f.part.ID))
	for _, want := range []string{"&#43;5", "-2.5", "&#43;7.5", "checker bug on test 7", "admin_all"} {
		if !strings.Contains(body, want) {
			t.Errorf("participation page lacks %q", want)
		}
	}
	rows, _ := f.q.ListAuditLog(bg, sqlc.ListAuditLogParams{Limit: 20})
	n := 0
	for _, r := range rows {
		if r.Action == "score.adjust" && strings.Contains(string(r.Details), "checker bug") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d audited adjustments with that reason, want 1", n)
	}
}
