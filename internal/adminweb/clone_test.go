package adminweb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
)

// rowJSON returns a row as a map, without the given keys.
func (f *fixture) rowJSON(t *testing.T, table string, id int64, drop ...string) map[string]any {
	t.Helper()
	var raw []byte
	if err := f.pool.QueryRow(bg, "SELECT row_to_json(x) FROM "+table+" x WHERE id = $1", id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	m := map[string]any{}
	json.Unmarshal(raw, &m)
	for _, k := range drop {
		delete(m, k)
	}
	return m
}

// TestContestClone (SPEC_CLOSE B1): the copy is a draft with every setting,
// the sites and the tasks (datasets, testcases, managers, statements,
// attachments, live dataset) but no submissions; participants only when
// asked, with their sites mapped.
func TestContestClone(t *testing.T) {
	f := newFixture(t)
	site, err := f.q.CreateSite(bg, sqlc.CreateSiteParams{ContestID: f.contest.ID, Name: "Sede Norte"})
	if err != nil {
		t.Fatal(err)
	}
	f.pool.Exec(bg, "UPDATE participations SET site_id = $1, starting_time = now() WHERE id = $2", site.ID, f.part.ID)
	f.pool.Exec(bg, "UPDATE contests SET description = 'Final', icpc_penalty_minutes = 15, practice_enabled = true, ranking_unfrozen = true WHERE id = $1", f.contest.ID)
	if err := f.q.UpsertCertificateTemplate(bg, sqlc.UpsertCertificateTemplateParams{ContestID: f.contest.ID, Title: "Constancia", Body: "{name}",
		Signatures: json.RawMessage(`[]`), Awards: json.RawMessage(`[{"name":"Oro","up_to_rank":1}]`)}); err != nil {
		t.Fatal(err)
	}
	if code, _ := f.login("read_only").Post(fmt.Sprintf("/contests/%d/clone", f.contest.ID), url.Values{"name": {"copia"}}); code != http.StatusForbidden {
		t.Fatalf("read-only clone = %d", code)
	}
	a := f.login("all")
	code, body := a.Post(fmt.Sprintf("/contests/%d/clone", f.contest.ID), url.Values{"name": {"copia"}, "participations": {"on"}})
	if code != 200 || !strings.Contains(body, "copied as a draft") {
		t.Fatalf("clone = %d\n%s", code, body)
	}
	nc, err := f.q.GetContestByName(bg, "copia")
	if err != nil {
		t.Fatal(err)
	}
	skip := []string{"id", "name", "status", "created_at", "updated_at", "ranking_unfrozen"}
	old, cp := f.rowJSON(t, "contests", f.contest.ID, skip...), f.rowJSON(t, "contests", nc.ID, skip...)
	if fmt.Sprint(old) != fmt.Sprint(cp) {
		t.Errorf("settings differ:\n%v\n%v", old, cp)
	}
	if nc.Status != "draft" || nc.RankingUnfrozen {
		t.Errorf("copy status %s unfrozen %v", nc.Status, nc.RankingUnfrozen)
	}
	tasks, _ := f.q.ListTasksByContest(bg, &nc.ID)
	if len(tasks) != 1 || tasks[0].Name != "sum-copia" || tasks[0].ActiveDatasetID == nil {
		t.Fatalf("tasks %+v", tasks)
	}
	skip = []string{"id", "name", "contest_id", "active_dataset_id", "created_at", "updated_at"}
	if a, b := f.rowJSON(t, "tasks", f.task.ID, skip...), f.rowJSON(t, "tasks", tasks[0].ID, skip...); fmt.Sprint(a) != fmt.Sprint(b) {
		t.Errorf("task settings differ:\n%v\n%v", a, b)
	}
	skip = []string{"id", "task_id", "created_at"}
	if a, b := f.rowJSON(t, "datasets", f.ds.ID, skip...), f.rowJSON(t, "datasets", *tasks[0].ActiveDatasetID, skip...); fmt.Sprint(a) != fmt.Sprint(b) {
		t.Errorf("dataset differs:\n%v\n%v", a, b)
	}
	count := func(sql string, args ...any) int {
		var n int
		if err := f.pool.QueryRow(bg, sql, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	nt := tasks[0].ID
	for _, q := range []string{
		"SELECT count(*) FROM testcases WHERE dataset_id IN (SELECT id FROM datasets WHERE task_id = $1)",
		"SELECT count(*) FROM managers WHERE dataset_id IN (SELECT id FROM datasets WHERE task_id = $1)",
		"SELECT count(*) FROM statements WHERE task_id = $1",
		"SELECT count(*) FROM attachments WHERE task_id = $1",
	} {
		if a, b := count(q, f.task.ID), count(q, nt); a != b || a == 0 {
			t.Errorf("%s: %d vs %d", q, a, b)
		}
	}
	if ct, err := f.q.GetCertificateTemplate(bg, nc.ID); err != nil || ct.Title != "Constancia" || len(ct.Awards) < 10 {
		t.Errorf("certificate template copy: %+v %v", ct, err)
	}
	if n := count("SELECT count(*) FROM submissions WHERE task_id = $1", nt); n != 0 {
		t.Errorf("%d submissions copied", n)
	}
	var siteName string
	var started bool
	if err := f.pool.QueryRow(bg, `SELECT s.name, p.starting_time IS NOT NULL FROM participations p JOIN sites s ON s.id = p.site_id
WHERE p.contest_id = $1 AND p.user_id = $2 AND s.contest_id = $1`, nc.ID, f.user.ID).Scan(&siteName, &started); err != nil || siteName != "Sede Norte" || started {
		t.Errorf("participation copy: site %q started %v %v", siteName, started, err)
	}
	// Names are unique: a second copy with the same task suffix fails cleanly.
	if code, body := a.Post(fmt.Sprintf("/contests/%d/clone", f.contest.ID), url.Values{"name": {"copia2"}, "task_suffix": {"-copia"}}); code != 422 || !strings.Contains(body, "already taken") {
		t.Fatalf("duplicate task names = %d", code)
	}
	if _, err := f.q.GetContestByName(bg, "copia2"); err == nil {
		t.Fatal("a failed copy left a contest behind")
	}
	if code, _ := a.Post(fmt.Sprintf("/contests/%d/clone", f.contest.ID), url.Values{"name": {"copia"}}); code != 422 {
		t.Fatalf("duplicate contest name = %d", code)
	}
}

// TestContestExtend (SPEC_CLOSE B2): the end moves for everybody (and the
// per-user window too); bad amounts are refused; practice is a setting.
func TestContestExtend(t *testing.T) {
	f := newFixture(t)
	a := f.login("all")
	path := fmt.Sprintf("/contests/%d/extend", f.contest.ID)
	f.pool.Exec(bg, "UPDATE contests SET per_user_time_s = 3600 WHERE id = $1", f.contest.ID)
	if code, body := a.Post(path, url.Values{"minutes": {"15"}}); code != 200 || !strings.Contains(body, "moved by 15 minutes") {
		t.Fatalf("extend = %d\n%s", code, body)
	}
	c, _ := f.q.GetContest(bg, f.contest.ID)
	if got := c.StopTime.Sub(f.contest.StopTime); got.Minutes() != 15 || *c.PerUserTimeS != 3600+15*60 {
		t.Fatalf("stop moved by %v, per-user time %d", got, *c.PerUserTimeS)
	}
	for _, bad := range []string{"0", "x", "601", "-100000"} {
		if code, _ := a.Post(path, url.Values{"minutes": {bad}}); code != 422 {
			t.Errorf("minutes=%s: %d", bad, code)
		}
	}
	if code, _ := f.login("read_only").Post(path, url.Values{"minutes": {"5"}}); code != http.StatusForbidden {
		t.Fatalf("read-only extend = %d", code)
	}
	rows, _ := f.q.ListAuditLog(bg, sqlc.ListAuditLogParams{Limit: 20})
	found := false
	for _, r := range rows {
		found = found || r.Action == "contest.extend"
	}
	if !found {
		t.Error("extension not audited")
	}
}

// TestContestModality (SPEC_CLOSE B3): the contest form saves team mode,
// the maximum team size and the scoring defaults, and new tasks of the
// contest start with those defaults.
func TestContestModality(t *testing.T) {
	f := newFixture(t)
	a := f.login("all")
	now := time.Now().UTC()
	form := url.Values{"name": {"seeded"}, "timezone": {"UTC"},
		"start_time": {now.Add(-time.Hour).Format("2006-01-02T15:04:05")}, "stop_time": {now.Add(time.Hour).Format("2006-01-02T15:04:05")},
		"token_mode": {"disabled"}, "token_gen_interval_s": {"1800"}, "scoring_mode": {"icpc"}, "score_precision": {"2"},
		"default_score_mode": {"max"}, "team_mode": {"on"}, "max_team_size": {"3"}, "icpc_penalty_minutes": {"10"}}
	if code, body := a.Post(fmt.Sprintf("/contests/%d", f.contest.ID), form); code != 200 {
		t.Fatalf("save = %d\n%s", code, body)
	}
	c, _ := f.q.GetContest(bg, f.contest.ID)
	if !c.TeamMode || c.MaxTeamSize == nil || *c.MaxTeamSize != 3 || c.DefaultScoreMode != "max" || c.ScoringMode != "icpc" || c.IcpcPenaltyMinutes != 10 {
		t.Fatalf("contest %+v", c)
	}
	form.Set("max_team_size", "0")
	if code, _ := a.Post(fmt.Sprintf("/contests/%d", f.contest.ID), form); code != 422 {
		t.Fatalf("team size 0 = %d", code)
	}
	if code, body := a.Post("/tasks", url.Values{"name": {"nueva"}, "title": {"Nueva"}, "contest_id": {fmt.Sprint(f.contest.ID)}}); code != 200 {
		t.Fatalf("task = %d\n%s", code, body)
	}
	task, _ := f.q.GetTaskByName(bg, "nueva")
	if task.ScoreMode != "max" || task.ScorePrecision != 2 {
		t.Fatalf("new task %s %d", task.ScoreMode, task.ScorePrecision)
	}
}

// TestRegistrationSettings (SPEC_CLOSE B6): the contest form saves the
// registration mode, the invitation code, the password length and the
// session duration; pending registrations are approved or rejected.
func TestRegistrationSettings(t *testing.T) {
	f := newFixture(t)
	a := f.login("all")
	now := time.Now().UTC()
	form := url.Values{"name": {"seeded"}, "timezone": {"UTC"},
		"start_time": {now.Add(-time.Hour).Format("2006-01-02T15:04:05")}, "stop_time": {now.Add(time.Hour).Format("2006-01-02T15:04:05")},
		"token_mode": {"disabled"}, "token_gen_interval_s": {"1800"}, "scoring_mode": {"ioi"}, "icpc_penalty_minutes": {"20"},
		"registration": {"code"}, "invitation_code": {"abc"}, "password_min_length": {"12"}, "session_minutes": {"90"}}
	path := fmt.Sprintf("/contests/%d", f.contest.ID)
	if code, body := a.Post(path, form); code != 422 || !strings.Contains(body, "at least 6") {
		t.Fatalf("short invitation code = %d\n%s", code, body)
	}
	form.Set("invitation_code", "OMI-2026")
	form.Set("password_min_length", "200")
	if code, _ := a.Post(path, form); code != 422 {
		t.Fatalf("password length 200 = %d", code)
	}
	form.Set("password_min_length", "12")
	if code, body := a.Post(path, form); code != 200 {
		t.Fatalf("save = %d\n%s", code, body)
	}
	c, _ := f.q.GetContest(bg, f.contest.ID)
	if c.Registration != "code" || c.InvitationCode != "OMI-2026" || c.PasswordMinLength != 12 || c.SessionMinutes == nil || *c.SessionMinutes != 90 {
		t.Fatalf("contest %s %q %d %v", c.Registration, c.InvitationCode, c.PasswordMinLength, c.SessionMinutes)
	}
	form.Set("registration", "approval")
	form.Set("session_minutes", "")
	if code, _ := a.Post(path, form); code != 200 {
		t.Fatalf("save approval = %d", code)
	}
	if c, _ = f.q.GetContest(bg, f.contest.ID); c.Registration != "approval" || c.SessionMinutes != nil {
		t.Fatalf("contest %s %v", c.Registration, c.SessionMinutes)
	}

	// Two pending registrations: one approved, one rejected.
	var pending []sqlc.Participation
	for _, name := range []string{"nuevo1", "nuevo2"} {
		u, err := f.q.CreateUser(bg, sqlc.CreateUserParams{Username: name, PreferredLanguages: []string{}})
		if err != nil {
			t.Fatal(err)
		}
		p, _ := f.q.CreateParticipation(bg, sqlc.CreateParticipationParams{ContestID: f.contest.ID, UserID: u.ID, Ip: []netip.Prefix{}})
		p, _ = f.q.SetParticipationApproved(bg, sqlc.SetParticipationApprovedParams{ID: p.ID, Approved: false})
		pending = append(pending, p)
	}
	if _, body := a.Get(path); !strings.Contains(body, "2 waiting for approval") {
		t.Errorf("pending count missing on the contest page")
	}
	if _, body := a.Get(path + "/participations"); !strings.Contains(body, "2 registrations wait") ||
		!strings.Contains(body, fmt.Sprintf("/participations/%d/approve", pending[0].ID)) {
		t.Fatalf("participations page:\n%s", body)
	}
	if code, _ := f.login("read_only").Post(fmt.Sprintf("/participations/%d/approve", pending[0].ID), nil); code != http.StatusForbidden {
		t.Fatalf("read-only approve = %d", code)
	}
	if code, body := a.Post(fmt.Sprintf("/participations/%d/approve", pending[0].ID), nil); code != 200 || !strings.Contains(body, "approved") {
		t.Fatalf("approve = %d\n%s", code, body)
	}
	if code, _ := a.Post(fmt.Sprintf("/participations/%d/reject", pending[0].ID), nil); code != http.StatusConflict {
		t.Fatalf("reject an approved participation = %d", code)
	}
	if code, _ := a.Post(fmt.Sprintf("/participations/%d/reject", pending[1].ID), nil); code != 200 {
		t.Fatalf("reject = %d", code)
	}
	var approved bool
	f.pool.QueryRow(bg, "SELECT approved FROM participations WHERE id = $1", pending[0].ID).Scan(&approved)
	var left int
	f.pool.QueryRow(bg, "SELECT count(*) FROM participations WHERE id = $1", pending[1].ID).Scan(&left)
	if !approved || left != 0 {
		t.Fatalf("approved %v, rejected still there %d", approved, left)
	}
}
