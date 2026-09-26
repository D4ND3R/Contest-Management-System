package adminweb

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
)

// TestEmergencyControlsAdmin (SPEC_IOI §9.3): pausing the contest and
// closing one task are one click each, audited, shown on the dashboard,
// and reserved to full administrators.
func TestEmergencyControlsAdmin(t *testing.T) {
	f := newFixture(t)
	b := f.login("all")
	id := fmt.Sprint(f.contest.ID)
	if code, _ := f.login("read_only").Post("/contests/"+id+"/pause", url.Values{"paused": {"1"}}); code != http.StatusForbidden {
		t.Fatalf("read-only pause = %d", code)
	}
	if code, _ := b.Post("/contests/"+id+"/pause", url.Values{"paused": {"1"}, "message": {strings.Repeat("x", 501)}}); code != http.StatusUnprocessableEntity {
		t.Fatalf("long message = %d", code)
	}
	code, body := b.Post("/contests/"+id+"/pause", url.Values{"paused": {"1"}, "message": {"Checking testcase 7."}})
	if code != 200 || !strings.Contains(body, "Submissions paused") {
		t.Fatalf("pause = %d\n%s", code, body)
	}
	c, _ := f.q.GetContest(bg, f.contest.ID)
	if !c.SubmissionsPaused || c.PauseMessage != "Checking testcase 7." {
		t.Fatalf("contest %+v", c)
	}
	if !strings.Contains(body, "Submissions are paused") || !strings.Contains(body, "Resume submissions") {
		t.Fatal("the dashboard does not show the pause")
	}
	if _, body := b.Get("/contests/" + id + "/settings"); !strings.Contains(body, `id="emergency"`) || !strings.Contains(body, "“Checking testcase 7.”") {
		t.Fatal("settings page without the pause")
	}
	b.Post("/contests/"+id+"/pause", url.Values{"paused": {"0"}, "message": {"ignored"}})
	if c, _ = f.q.GetContest(bg, f.contest.ID); c.SubmissionsPaused || c.PauseMessage != "" {
		t.Fatalf("not resumed: %+v", c)
	}

	tid := fmt.Sprint(f.task.ID)
	if code, body := b.Post("/tasks/"+tid+"/close", url.Values{"closed": {"1"}}); code != 200 || !strings.Contains(body, `<span class="tag bad">closed</span>`) {
		t.Fatalf("close = %d\n%s", code, body)
	}
	if task, _ := f.q.GetTask(bg, f.task.ID); !task.SubmissionsClosed {
		t.Fatal("task not closed")
	}
	if _, body := b.Get("/contests/" + id); !strings.Contains(body, `<span class="tag bad">closed</span>`) {
		t.Fatal("dashboard problem status without the closed task")
	}
	b.Post("/tasks/"+tid+"/close", url.Values{"closed": {"0"}})
	if task, _ := f.q.GetTask(bg, f.task.ID); task.SubmissionsClosed {
		t.Fatal("task not reopened")
	}
	rows, _ := f.q.ListAuditLog(bg, sqlc.ListAuditLogParams{Limit: 20})
	seen := map[string]int{}
	for _, r := range rows {
		seen[r.Action]++
	}
	if seen["contest.pause"] != 2 || seen["task.close"] != 2 {
		t.Fatalf("audit %v", seen)
	}
}

// TestTaskSetterRole (SPEC_IOI §9.4): task setters change tasks, datasets
// and testcases, but not what decides scores (making a dataset live,
// rejudging) nor contests, users or administrators.
func TestTaskSetterRole(t *testing.T) {
	f := newFixture(t)
	b := f.login("task_setter")
	ds := fmt.Sprint(f.ds.ID)
	code, body := b.Get("/datasets/" + ds)
	if code != 200 || !strings.Contains(body, `action="/datasets/`+ds+`/managers"`) || strings.Contains(body, `action="/datasets/`+ds+`/activate"`) {
		t.Fatalf("dataset page: %d", code)
	}
	form := url.Values{"description": {"Default"}, "time_limit": {"2"}, "memory_limit_mib": {"256"}, "process_limit": {"1"},
		"task_type": {"Batch"}, "tt_checker": {"white_diff"}, "score_type": {"Sum"}, "score_type_params": {"10"}}
	if code, body := b.Post("/datasets/"+ds, form); code != 200 {
		t.Fatalf("task setter saving a dataset = %d\n%s", code, body)
	}
	if d, _ := f.q.GetDataset(bg, f.ds.ID); *d.TimeLimitMs != 2000 {
		t.Fatal("dataset not saved")
	}
	for path, v := range map[string]url.Values{
		"/datasets/" + ds + "/activate":                    nil,
		"/reevaluate":                                      {"task_id": {fmt.Sprint(f.task.ID)}, "level": {"rescore"}},
		"/contests/" + fmt.Sprint(f.contest.ID) + "/pause": {"paused": {"1"}},
		"/tasks/" + fmt.Sprint(f.task.ID) + "/delete":      {"confirm": {f.task.Name}},
		"/admins": {"username": {"x"}, "password": {"long-enough-1"}, "role": {"all"}},
	} {
		if code, _ := b.Post(path, v); code != http.StatusForbidden {
			t.Errorf("task setter POST %s = %d", path, code)
		}
	}
}

// TestDelegationLeader (SPEC_IOI §9.2): a leader sees their team's
// contestants' submissions, sources and public results, and nothing else.
func TestDelegationLeader(t *testing.T) {
	f := newFixture(t)
	b := f.login("leader")
	code, body := b.Get("/")
	if code != 200 || !strings.Contains(body, "My delegation") || !strings.Contains(body, "MEX") || !strings.Contains(body, "ana") ||
		!strings.Contains(body, fmt.Sprintf(`href="/delegation/submissions/%d"`, f.subs[0])) {
		t.Fatalf("delegation page: %d\n%s", code, body)
	}
	if strings.Contains(body, `href="/contests"`) || strings.Contains(body, `name="cms-events"`) {
		t.Fatal("the leader's menu offers administration pages")
	}
	if code, body := b.Get(fmt.Sprintf("/delegation/submissions/%d", f.subs[0])); code != 200 || !strings.Contains(body, "<h2>sum.c</h2>") || !strings.Contains(body, "main") {
		t.Fatalf("source: %d\n%s", code, body)
	}
	for _, path := range []string{"/contests", fmt.Sprintf("/submissions/%d", f.subs[0]), "/users", "/questions", "/events",
		fmt.Sprintf("/contests/%d", f.contest.ID), fmt.Sprintf("/contests/%d/ranking", f.contest.ID)} {
		if code, _ := b.Get(path); code != http.StatusForbidden {
			t.Errorf("leader GET %s = %d", path, code)
		}
	}
	if code, _ := b.Get("/account"); code != 200 {
		t.Fatalf("account = %d", code)
	}
	// Another team's submission is invisible.
	other, _ := f.q.CreateTeam(bg, sqlc.CreateTeamParams{Code: "ARG", Name: "Argentina"})
	f.pool.Exec(bg, "UPDATE participations SET team_id = $1 WHERE id = $2", other.ID, f.part.ID)
	if code, _ := b.Get(fmt.Sprintf("/delegation/submissions/%d", f.subs[0])); code != http.StatusNotFound {
		t.Fatalf("other team's submission = %d", code)
	}
	// A leader account needs a team.
	all := f.login("all")
	if code, _ := all.Post("/admins", url.Values{"username": {"lead2"}, "password": {"long-enough-1"}, "role": {"leader"}}); code != http.StatusUnprocessableEntity {
		t.Fatalf("leader without team = %d", code)
	}
	if code, _ := all.Post("/admins", url.Values{"username": {"lead2"}, "password": {"long-enough-1"}, "role": {"leader"},
		"team_id": {fmt.Sprint(other.ID)}}); code != 200 {
		t.Fatalf("leader with team = %d", code)
	}
	// Full administrators see any team's page.
	if _, body := all.Get(fmt.Sprintf("/delegation?team=%d", other.ID)); !strings.Contains(body, "ana") {
		t.Fatal("admin view of a delegation")
	}
}
