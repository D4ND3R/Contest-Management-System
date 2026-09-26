package adminweb

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestAdminPagesSurviveBadInput requests every admin GET route with real,
// missing and malformed ids and with nonsense query parameters: a page may
// refuse (4xx) but never fail (5xx).
func TestAdminPagesSurviveBadInput(t *testing.T) {
	f := newFixture(t)
	a := f.login("all")
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	var tc int64
	f.pool.QueryRow(bg, "SELECT id FROM testcases WHERE dataset_id = $1 LIMIT 1", f.ds.ID).Scan(&tc)
	real := map[string]string{
		"/contests/": fmt.Sprint(f.contest.ID), "/tasks/": fmt.Sprint(f.task.ID), "/datasets/": fmt.Sprint(f.ds.ID),
		"/users/": fmt.Sprint(f.user.ID), "/participations/": fmt.Sprint(f.part.ID), "/submissions/": fmt.Sprint(f.subs[0]),
		"/admins/": fmt.Sprint(f.admins["all"].ID), "/teams/": fmt.Sprint(f.team.ID), "/testcases/": fmt.Sprint(tc),
	}
	junk := "?page=-1&fragment=1&dataset=abc&task=x&user=x&admin=x&participation=-3&status=zz&verdict=zz&since=nope&until=13-99&from=x&to=x" +
		"&limit=-5&offset=x&sort=%27&order=zz&q=%25%27%22%3C&kind=x&a=x&b=x&site=%00&lang=zz&action=zz&which=zz&threshold=abc&min=x"
	re := regexp.MustCompile(`(?:get\("|route\("GET )(/[^"]*)"`)
	n := 0
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		route := m[1]
		if route == "/events" || route == "/{$}" {
			continue // an endless event stream; the dashboard is covered elsewhere
		}
		ids := []string{"999999", "x", "-1", "0"}
		for prefix, id := range real {
			if strings.HasPrefix(route, prefix) {
				ids = append(ids, id)
			}
		}
		for _, id := range ids {
			path := strings.NewReplacer("{id}", id, "{lang}", "zz", "{file}", "nope.txt", "{which}", "input", "{name}", "nope",
				"{kind}", "nope", "{digest}", strings.Repeat("0", 64)).Replace(route)
			for _, q := range []string{"", junk} {
				code, body := a.Get(path + q)
				n++
				if code >= 500 || strings.Contains(body, "Internal error") {
					t.Errorf("GET %s%s = %d\n%.300s", path, q, code, body)
				}
			}
		}
	}
	if n < 300 {
		t.Fatalf("only %d requests", n)
	}
}
