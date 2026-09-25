package adminweb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

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
