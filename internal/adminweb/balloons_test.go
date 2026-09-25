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

// TestBalloons (SPEC_CLOSE B7): every task solved by a team is a balloon,
// first solves marked, teammates' later solves not repeated; staff mark
// them delivered (and undo); hidden participations get none.
func TestBalloons(t *testing.T) {
	f := newFixture(t)
	f.pool.Exec(bg, "UPDATE contests SET scoring_mode = 'icpc', team_mode = true WHERE id = $1", f.contest.ID)
	site, _ := f.q.CreateSite(bg, sqlc.CreateSiteParams{ContestID: f.contest.ID, Name: "Norte"})
	arg, _ := f.q.CreateTeam(bg, sqlc.CreateTeamParams{Code: "ARG", Name: "Argentina"})
	part := func(name string, team *int64, hidden bool) sqlc.Participation {
		u, err := f.q.CreateUser(bg, sqlc.CreateUserParams{Username: name, PreferredLanguages: []string{}})
		if err != nil {
			t.Fatal(err)
		}
		p, err := f.q.CreateParticipation(bg, sqlc.CreateParticipationParams{ContestID: f.contest.ID, UserID: u.ID, TeamID: team,
			Ip: []netip.Prefix{}, Hidden: hidden})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	mex2 := part("beto", &f.team.ID, false)
	argP := part("caro", &arg.ID, false)
	hid := part("oculto", nil, true)
	f.pool.Exec(bg, "UPDATE participations SET site_id = $1 WHERE id = $2", site.ID, argP.ID)
	start := time.Now().Add(-50 * time.Minute)
	solve := func(p int64, minute int) {
		at := start.Add(time.Duration(minute) * time.Minute)
		if err := f.q.UpsertParticipationTaskScore(bg, sqlc.UpsertParticipationTaskScoreParams{ParticipationID: p, TaskID: f.task.ID,
			Score: 100, SubtaskScores: json.RawMessage(`[]`), IcpcSolved: true, IcpcSolvedAt: &at}); err != nil {
			t.Fatal(err)
		}
	}
	solve(hid.ID, 1)     // hidden: no balloon
	solve(argP.ID, 5)    // first solve
	solve(f.part.ID, 10) // MEX
	solve(mex2.ID, 20)   // MEX again: same balloon
	path := fmt.Sprintf("/contests/%d/balloons", f.contest.ID)
	a := f.login("messaging")
	code, body := a.Get(path)
	if code != 200 {
		t.Fatalf("balloons = %d\n%s", code, body)
	}
	pending := body[strings.Index(body, "To deliver"):strings.Index(body, "</table>")]
	if strings.Count(pending, `name="recipient"`) != 2 || !strings.Contains(pending, "ARG — Argentina") || !strings.Contains(pending, "MEX — Mexico") ||
		strings.Contains(pending, "oculto") || strings.Count(pending, "first solve") != 1 {
		t.Fatalf("pending balloons:\n%s", pending)
	}
	if strings.Index(pending, "ARG") > strings.Index(pending, "MEX") {
		t.Error("balloons not in solve order")
	}
	if _, cp := a.Get(fmt.Sprintf("/contests/%d", f.contest.ID)); !strings.Contains(cp, path) {
		t.Error("no balloons link on an ICPC contest")
	}
	// Site filter.
	if _, b := a.Get(path + "?site=Norte&fragment=1"); !strings.Contains(b, "ARG") || strings.Contains(b, "MEX") {
		t.Errorf("site filter:\n%s", b)
	}
	// Fresh values each time: the browser adds its CSRF token to them.
	form := func(extra ...string) url.Values {
		v := url.Values{"task_id": {fmt.Sprint(f.task.ID)}, "recipient": {"tARG"}}
		for i := 0; i+1 < len(extra); i += 2 {
			v.Set(extra[i], extra[i+1])
		}
		return v
	}
	if code, _ := f.login("read_only").Post(path+"/deliver", form()); code != http.StatusForbidden {
		t.Fatalf("read-only deliver = %d", code)
	}
	code, body = a.PostHTMX(path+"/deliver", form())
	if code != 200 || !strings.Contains(body, `id="balloons"`) {
		t.Fatalf("deliver = %d\n%s", code, body)
	}
	pending = body[strings.Index(body, "To deliver"):strings.Index(body, "</table>")]
	if strings.Contains(pending, "ARG") || !strings.Contains(body[strings.Index(body, "</table>"):], "admin_messaging") {
		t.Fatalf("after delivery:\n%s", body)
	}
	// Twice is harmless; undo brings it back.
	a.Post(path+"/deliver", form())
	if code, _ := a.Post(path+"/deliver", form("undo", "1")); code != 200 {
		t.Fatalf("undo = %d", code)
	}
	var n int
	f.pool.QueryRow(bg, "SELECT count(*) FROM balloons").Scan(&n)
	if n != 0 {
		t.Fatalf("%d deliveries after undo", n)
	}
	// A task of another contest is refused.
	if code, _ := a.Post(path+"/deliver", url.Values{"task_id": {"999999"}, "recipient": {"tARG"}}); code != http.StatusNotFound {
		t.Fatalf("foreign task = %d", code)
	}
}
