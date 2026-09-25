package adminweb

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
)

const plagOriginal = `#include <cstdio>
int best[100005];
int main() {
    int n;
    long long total = 0;
    scanf("%d", &n);
    for (int i = 0; i < n; i++) {
        int x;
        scanf("%d", &x);
        best[i] = i > 0 && best[i - 1] > x ? best[i - 1] : x;
        total += best[i];
    }
    printf("%lld\n", total);
    return 0;
}
`

const plagDisguised = `#include <bits/stdc++.h>
int mx[100005]; // running maximum
int main(){int cnt;long long acc=0;scanf("%d",&cnt);
  for(int j=0;j<cnt;j++){ int v; scanf("%d",&v);
    mx[j]=j>0&&mx[j-1]>v?mx[j-1]:v; acc+=mx[j]; }
  printf("%lld\n",acc);return 0;}
`

const plagOther = `#include <cstdio>
#include <algorithm>
int a[200005];
int main() {
    int n, k;
    scanf("%d %d", &n, &k);
    for (int i = 0; i < n; i++) scanf("%d", &a[i]);
    std::sort(a, a + n);
    int lo = 0, hi = n - 1, pairs = 0;
    while (lo < hi) {
        if (a[lo] + a[hi] <= k) { pairs++; lo++; hi--; }
        else hi--;
    }
    printf("%d\n", pairs);
}
`

// TestPlagiarismReport (SPEC_CLOSE E1): the report of a task pairs the
// disguised copy with its original, not the independent solution, and the
// side-by-side view marks the shared lines.
func TestPlagiarismReport(t *testing.T) {
	f := newFixture(t)
	lang := "cpp17"
	subs := map[string]int64{}
	for i, u := range []struct{ name, src string }{{"bob", plagOriginal}, {"carla", plagDisguised}, {"dan", plagOther}} {
		user, err := f.q.CreateUser(bg, sqlc.CreateUserParams{Username: u.name, PasswordHash: "x", PreferredLanguages: []string{}})
		if err != nil {
			t.Fatal(err)
		}
		p, err := f.q.CreateParticipation(bg, sqlc.CreateParticipationParams{ContestID: f.contest.ID, UserID: user.ID, Ip: []netip.Prefix{}})
		if err != nil {
			t.Fatal(err)
		}
		// An older, unrelated submission: the report takes the latest.
		for k, src := range []string{plagOther + "// v1\n", u.src} {
			sub, err := f.q.CreateSubmission(bg, sqlc.CreateSubmissionParams{ParticipationID: &p.ID, TaskID: f.task.ID,
				SubmittedAt: time.Now().Add(time.Duration(i*10+k) * time.Second), Language: &lang, Official: true})
			if err != nil {
				t.Fatal(err)
			}
			f.q.CreateSubmissionFiles(bg, []sqlc.CreateSubmissionFilesParams{{SubmissionID: sub.ID, Filename: "sum.%l", Digest: f.put(src)}})
			subs[u.name] = sub.ID
		}
	}
	// Without a language (output files): nothing to compare.
	eve, _ := f.q.CreateUser(bg, sqlc.CreateUserParams{Username: "eve", PasswordHash: "x", PreferredLanguages: []string{}})
	pe, _ := f.q.CreateParticipation(bg, sqlc.CreateParticipationParams{ContestID: f.contest.ID, UserID: eve.ID, Ip: []netip.Prefix{}})
	se, _ := f.q.CreateSubmission(bg, sqlc.CreateSubmissionParams{ParticipationID: &pe.ID, TaskID: f.task.ID, SubmittedAt: time.Now(), Official: true})
	f.q.CreateSubmissionFiles(bg, []sqlc.CreateSubmissionFilesParams{{SubmissionID: se.ID, Filename: "output_0.txt", Digest: f.put(plagOriginal)}})
	b := f.login("read_only")
	path := fmt.Sprintf("/contests/%d/plagiarism", f.contest.ID)
	code, body := b.Get(path)
	if code != 200 || !strings.Contains(body, `name="threshold"`) {
		t.Fatalf("form = %d\n%s", code, body)
	}
	code, body = b.Get(fmt.Sprintf("%s?task=%d&threshold=50", path, f.task.ID))
	if code != 200 {
		t.Fatalf("report = %d\n%s", code, body)
	}
	compare := fmt.Sprintf("compare?a=%d&amp;b=%d", subs["bob"], subs["carla"])
	if strings.Count(body, "/plagiarism/compare?") != 1 || !strings.Contains(body, compare) || !strings.Contains(body, "4 submissions compared") {
		t.Fatalf("report:\n%s", body)
	}
	if !strings.Contains(body, "without source code in a known language or too short") {
		t.Fatal("the submission without a language is not reported as skipped")
	}
	code, body = b.Get(fmt.Sprintf("%s/compare?a=%d&b=%d", path, subs["bob"], subs["carla"]))
	if code != 200 || !strings.Contains(body, `class="l m"`) || !strings.Contains(body, "carla") || !strings.Contains(body, "similarity 100%") {
		t.Fatalf("compare = %d\n%s", code, body)
	}
	// A submission of another contest is not shown here.
	now := time.Now()
	other, _ := f.q.CreateContest(bg, db.NewContestParams("otro", now, now.Add(time.Hour)))
	code, _ = b.Get(fmt.Sprintf("/contests/%d/plagiarism/compare?a=%d&b=%d", other.ID, subs["bob"], subs["carla"]))
	if code != 404 {
		t.Fatalf("compare in another contest = %d", code)
	}
	_, body = b.Get(fmt.Sprintf("/contests/%d/stats", f.contest.ID))
	if !strings.Contains(body, fmt.Sprintf("plagiarism?task=%d", f.task.ID)) {
		t.Fatal("no link from the statistics")
	}
}
