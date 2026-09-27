package adminweb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db"
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
	if code, body := b.Post("/tasks/"+tid+"/close", url.Values{"closed": {"1"}}); code != 200 || !strings.Contains(body, `<span class="tag bad">submissions closed</span>`) {
		t.Fatalf("close = %d\n%s", code, body)
	}
	if task, _ := f.q.GetTask(bg, f.task.ID); !task.SubmissionsClosed {
		t.Fatal("task not closed")
	}
	if _, body := b.Get("/contests/" + id); !strings.Contains(body, `<span class="tag bad">submissions closed</span>`) {
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
	if code != 200 || !strings.Contains(body, `action="/tasks/`+fmt.Sprint(f.task.ID)+`/files"`) || strings.Contains(body, `/activate"`) {
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

// TestUnofficialAndMedals (SPEC_IOI §9.2, §10): an unofficial participant
// is marked in the ranking and takes no place; medal cutoffs appear for
// the administrators once the contest awards medals.
func TestUnofficialAndMedals(t *testing.T) {
	f := newFixture(t)
	b := f.login("all")
	code, body := b.Post(fmt.Sprintf("/participations/%d", f.part.ID), url.Values{"team_id": {fmt.Sprint(f.team.ID)}, "ip": {""},
		"delay_time_s": {"0"}, "extra_time_s": {"0"}, "unofficial": {"on"}})
	if code != 200 {
		t.Fatalf("save participation = %d\n%s", code, body)
	}
	if p, _ := f.q.GetParticipation(bg, f.part.ID); !p.Unofficial {
		t.Fatal("participation not unofficial")
	}
	_, body = b.Get(fmt.Sprintf("/contests/%d/ranking", f.contest.ID))
	if !strings.Contains(body, `<td class="num">–</td>`) || !strings.Contains(body, `<span class="tag">unofficial</span>`) {
		t.Fatalf("ranking:\n%s", body)
	}
	c := db.ContestToUpdate(f.contest)
	c.Medals = "admins"
	f.q.UpdateContest(bg, c)
	f.pool.Exec(bg, "UPDATE participations SET unofficial = false")
	_, body = b.Get(fmt.Sprintf("/contests/%d/ranking", f.contest.ID))
	if strings.Contains(body, "unofficial</span>") {
		t.Fatal("still unofficial")
	}
	_, csv := b.Get(fmt.Sprintf("/contests/%d/ranking.csv", f.contest.ID))
	if !strings.Contains(csv, ",official,medal") {
		t.Fatalf("csv:\n%s", csv)
	}
}

// TestQuestionAssignment (SPEC_IOI §9.3): a staff member takes a question,
// the others see who has it (and may take it over), and each can list
// the questions they took.
func TestQuestionAssignment(t *testing.T) {
	f := newFixture(t)
	q, err := f.q.CreateQuestion(bg, sqlc.CreateQuestionParams{ParticipationID: f.part.ID, AskedAt: time.Now(), Subject: "s", Text: "Is n ≤ 10?"})
	if err != nil {
		t.Fatal(err)
	}
	m := f.login("messaging")
	path := fmt.Sprintf("/questions/%d/assign", q.ID)
	if code, body := m.Post(path, url.Values{"take": {"1"}}); code != 200 || !strings.Contains(body, "You took the question.") {
		t.Fatalf("take = %d", code)
	}
	if got, _ := f.q.GetQuestion(bg, q.ID); got.AssignedAdminID == nil || *got.AssignedAdminID != f.admins["messaging"].ID {
		t.Fatalf("not assigned: %+v", got)
	}
	if _, body := m.Get("/questions?mine=1"); !strings.Contains(body, "Is n ≤ 10?") || !strings.Contains(body, "give back") {
		t.Fatal("my questions")
	}
	a := f.login("all")
	_, body := a.Get("/questions")
	if !strings.Contains(body, "admin_messaging") || !strings.Contains(body, "take over") {
		t.Fatalf("other admin's view:\n%s", body)
	}
	if _, body := a.Get("/questions?mine=1"); strings.Contains(body, "Is n ≤ 10?") {
		t.Fatal("somebody else's question in my list")
	}
	if code, _ := f.login("read_only").Post(path, url.Values{"take": {"1"}}); code != http.StatusForbidden {
		t.Fatalf("read-only take = %d", code)
	}
	m.Post(path, url.Values{"take": {"0"}})
	if got, _ := f.q.GetQuestion(bg, q.ID); got.AssignedAdminID != nil {
		t.Fatal("not given back")
	}
}

// TestDatasetCompare (SPEC_IOI §6): a candidate dataset is compared with
// the live one: the task scores and the submissions that would change,
// and how many submissions it has not judged yet.
func TestDatasetCompare(t *testing.T) {
	f := newFixture(t)
	b := f.login("read_only")
	cand, err := f.q.CreateDataset(bg, db.NewDatasetParams(f.task.ID, "stricter"))
	if err != nil {
		t.Fatal(err)
	}
	// The first submission scores 40 on the candidate; the second is not
	// judged there yet.
	sid := f.subs[0]
	f.q.EnsureSubmissionResult(bg, sqlc.EnsureSubmissionResultParams{SubmissionID: sid, DatasetID: cand.ID})
	ok := "ok"
	f.q.SetCompilationResult(bg, sqlc.SetCompilationResultParams{SubmissionID: sid, DatasetID: cand.ID, CompilationOutcome: &ok, TestcasesTotal: 2})
	score := 40.0
	det := json.RawMessage(`{}`)
	if err := f.q.SetScore(bg, sqlc.SetScoreParams{SubmissionID: sid, DatasetID: cand.ID, Score: &score, ScoreDetails: det, PublicScore: &score,
		PublicScoreDetails: det, RankingScoreDetails: json.RawMessage(`[40]`)}); err != nil {
		t.Fatal(err)
	}
	code, body := b.Get(fmt.Sprintf("/tasks/%d/compare?b=%d", f.task.ID, cand.ID))
	if code != 200 {
		t.Fatalf("compare = %d\n%s", code, body)
	}
	for _, want := range []string{"1 contestants and 2 submissions compared: 1 task scores and 1 submission scores would change",
		"1 submissions are not judged on B yet", `<td class="num bad">-60</td>`, fmt.Sprintf(`href="/submissions/%d"`, sid)} {
		if !strings.Contains(body, want) {
			t.Errorf("compare page lacks %s", want)
		}
	}
	if t.Failed() {
		t.Log(body)
	}
	if code, _ := b.Get(fmt.Sprintf("/tasks/%d/compare?a=%d&b=%d", f.task.ID, cand.ID, cand.ID)); code != http.StatusUnprocessableEntity {
		t.Fatalf("same dataset twice = %d", code)
	}
	if _, body := b.Get(fmt.Sprintf("/tasks/%d", f.task.ID)); !strings.Contains(body, fmt.Sprintf(`href="/tasks/%d/compare?b=%d"`, f.task.ID, cand.ID)) {
		t.Fatal("no compare link on the task page")
	}
}

// TestAppealsAdmin (SPEC_IOI §14): the staff list a contest's appeals,
// answer them (an answer is required), and see open ones on the dashboard.
func TestAppealsAdmin(t *testing.T) {
	f := newFixture(t)
	until := time.Now().Add(time.Hour)
	c := db.ContestToUpdate(f.contest)
	c.AppealsUntil = &until
	if _, err := f.q.UpdateContest(bg, c); err != nil {
		t.Fatal(err)
	}
	a, err := f.q.CreateAppeal(bg, sqlc.CreateAppealParams{ParticipationID: f.part.ID, TaskID: &f.task.ID, Text: "Testcase 3 is wrong."})
	if err != nil {
		t.Fatal(err)
	}
	m := f.login("messaging")
	id := fmt.Sprint(f.contest.ID)
	if _, body := m.Get("/contests/" + id); !strings.Contains(body, "1 appeals waiting for an answer") || !strings.Contains(body, `href="/contests/`+id+`/appeals"`) {
		t.Fatal("dashboard without the appeal")
	}
	if _, body := m.Get("/contests/" + id + "/appeals"); !strings.Contains(body, "Testcase 3 is wrong.") || !strings.Contains(body, fmt.Sprintf(`action="/appeals/%d"`, a.ID)) {
		t.Fatal("appeals list")
	}
	path := fmt.Sprintf("/appeals/%d", a.ID)
	if code, _ := m.Post(path, url.Values{"status": {"rejected"}}); code != http.StatusUnprocessableEntity {
		t.Fatalf("answer without text = %d", code)
	}
	if code, _ := f.login("read_only").Post(path, url.Values{"status": {"rejected"}, "response": {"No."}}); code != http.StatusForbidden {
		t.Fatalf("read-only answer = %d", code)
	}
	if code, body := m.Post(path, url.Values{"status": {"rejected"}, "response": {"The testcase is right."}}); code != 200 || !strings.Contains(body, "Appeal answered.") {
		t.Fatalf("answer = %d", code)
	}
	rows, _ := f.q.AdminListAppeals(bg, sqlc.AdminListAppealsParams{ContestID: f.contest.ID})
	if len(rows) != 1 || rows[0].Status != "rejected" || rows[0].Handler == nil || *rows[0].Handler != "admin_messaging" {
		t.Fatalf("appeal %+v", rows)
	}
	open := "open"
	if rows, _ := f.q.AdminListAppeals(bg, sqlc.AdminListAppealsParams{ContestID: f.contest.ID, Status: &open}); len(rows) != 0 {
		t.Fatal("status filter")
	}
}
