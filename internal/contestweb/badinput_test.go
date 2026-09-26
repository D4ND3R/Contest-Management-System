package contestweb

import (
	"regexp"
	"strings"
	"testing"
)

// TestContestPagesSurviveBadInput requests every contestant GET route,
// logged in and anonymous, with unknown contests, tasks and ids and with
// nonsense query parameters: a page may refuse (4xx) but never fail (5xx).
func TestContestPagesSurviveBadInput(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	src := routeSource(t)
	logged := f.client()
	f.login(logged, "ana", "secret")
	junk := "?page=-1&fragment=1&task=x&since=nope&after=-5&limit=x&lang=zz&q=%25%27%22%3C&id=x&token=%00&download=zz"
	re := regexp.MustCompile(`Handle(?:Func)?\("GET (/[^"]*)"`)
	n := 0
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		route := m[1]
		if strings.HasSuffix(route, "/events") || strings.HasPrefix(route, "/static/") {
			continue // an endless event stream; files
		}
		var paths []string
		for _, c := range []string{"ioi", "nope"} {
			for _, task := range []string{"sum", "nope"} {
				for _, id := range []string{"999999", "x", "-1"} {
					paths = append(paths, strings.NewReplacer("{contest}", c, "{task}", task, "{id}", id, "{lang}", "zz",
						"{file}", "nope.txt", "{which}", "nope", "{name}", "nope", "{$}", "").Replace(route))
				}
			}
		}
		for _, p := range paths {
			for _, q := range []string{"", junk} {
				for _, c := range []string{"logged", "anonymous"} {
					cl := logged
					if c == "anonymous" {
						cl = f.client()
					}
					code, body := f.get(cl, p+q)
					n++
					if code >= 500 {
						t.Errorf("GET %s%s (%s) = %d\n%.300s", p, q, c, code, body)
					}
				}
			}
		}
	}
	if n < 300 {
		t.Fatalf("only %d requests", n)
	}
}
