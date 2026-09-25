package plagiarism

import (
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"testing"
)

const original = `#include <cstdio>
// Sum of the prefix maxima.
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
        if (total > 1000000007LL) total -= 1000000007LL;
    }
    printf("%lld\n", total);
    return 0;
}
`

// disguised is original with other names, layout and comments.
const disguised = `#include <bits/stdc++.h>
using namespace std;
int mx[100005]; /* running maximum */
int main(){int cnt;long long acc=0;scanf("%d",&cnt);
  for(int j=0;j<cnt;j++){   // read and fold
    int v; scanf("%d",&v);
    mx[j]=j>0&&mx[j-1]>v?mx[j-1]:v; acc+=mx[j];
    if(acc>1000000007LL)acc-=1000000007LL;}
  printf("%lld\n",acc);return 0;}
`

const unrelated = `import sys
def solve(a):
    seen = {}
    for i, v in enumerate(a):
        if v in seen:
            return seen[v], i
        seen[v] = i
    return -1, -1
data = sys.stdin.read().split()
print(*solve(list(map(int, data[1:]))))
`

const different = `#include <cstdio>
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

func doc(id int64, name, src string) *Doc {
	return Fingerprint(id, 0, []Source{{Name: name, Data: src}}, nil)
}

func TestDisguisedCopyIsFound(t *testing.T) {
	docs := []*Doc{doc(1, "a.cpp", original), doc(2, "b.cpp", disguised), doc(3, "c.py", unrelated), doc(4, "d.cpp", different)}
	pairs := Compare(docs, Options{Threshold: 0.3})
	if len(pairs) != 1 || pairs[0].A.ID != 1 || pairs[0].B.ID != 2 {
		for _, p := range pairs {
			t.Logf("%d-%d %.2f (%d)", p.A.ID, p.B.ID, p.Similarity, p.Shared)
		}
		t.Fatalf("%d pairs", len(pairs))
	}
	if pairs[0].Similarity < 0.9 {
		t.Fatalf("similarity %.2f, want ≥ 0.9", pairs[0].Similarity)
	}
	a, b := Matches(docs[0], docs[1])
	if !a[10] || !b[6] || a[1] {
		t.Fatalf("matched lines: %v / %v", a, b)
	}
}

func TestTemplateAndCommonCodeIgnored(t *testing.T) {
	// A stub handed to everybody, plus a different solution each.
	stub := "#include <cstdio>\nint solve(int n, int* a);\nint a[100];\nint main() {\n  int n; scanf(\"%d\", &n);\n  for (int i = 0; i < n; i++) scanf(\"%d\", &a[i]);\n  printf(\"%d\\n\", solve(n, a));\n  return 0;\n}\n"
	sols := []string{
		"int solve(int n, int* a) { int s = 0; for (int i = 0; i < n; i++) s += a[i]; return s; }\n",
		"int solve(int n, int* a) { while (n > 1 && a[n-1] == a[n-2]) n--; if (n == 0) return -1; return a[0] * 2 + n; }\n",
	}
	var docs []*Doc
	for i, s := range sols {
		docs = append(docs, doc(int64(i+1), "s.cpp", stub+s))
	}
	if pairs := Compare(docs, Options{Threshold: 0.3}); len(pairs) == 0 {
		t.Fatal("without the template the shared stub must be reported")
	}
	base := NewBase([]Source{{Name: "stub.cpp", Data: stub}})
	for i, s := range sols {
		docs[i] = Fingerprint(int64(i+1), 0, []Source{{Name: "s.cpp", Data: stub + s}}, base)
	}
	if pairs := Compare(docs, Options{Threshold: 0.3}); len(pairs) != 0 {
		t.Fatalf("the template is ignored: %+v", pairs[0])
	}
	// A copy is still found when both start from the template.
	copied := Fingerprint(3, 0, []Source{{Name: "s.cpp", Data: stub + regexp.MustCompile(`\bn\b`).ReplaceAllString(sols[1], "len")}}, base)
	if pairs := Compare(append(docs, copied), Options{Threshold: 0.8}); len(pairs) != 1 || pairs[0].A.ID != 2 || pairs[0].B.ID != 3 {
		t.Fatalf("copy over the template: %d pairs", len(pairs))
	}
	// The same snippet in many submissions is an idiom, not a copy.
	many := make([]*Doc, 12)
	for i := range many {
		many[i] = doc(int64(i+1), "x.cpp", original)
	}
	if pairs := Compare(many, Options{Threshold: 0.3}); len(pairs) != 0 {
		t.Fatalf("code shared by 12 submissions is common: %d pairs", len(pairs))
	}
	// Members of one group (a team) are not paired.
	x, y := doc(1, "a.cpp", original), doc(2, "b.cpp", disguised)
	x.Group, y.Group = 7, 7
	if pairs := Compare([]*Doc{x, y}, Options{}); len(pairs) != 0 {
		t.Fatal("same group paired")
	}
}

func TestShortOrUnknownSources(t *testing.T) {
	if d := doc(1, "a.txt", original); d.Tokens != 0 || len(d.fps) != 0 {
		t.Fatalf("unknown language: %+v", d)
	}
	if d := doc(1, "a.c", "int main(){}"); len(d.fps) != 0 {
		t.Fatal("shorter than K tokens")
	}
	if d := doc(1, "a.c", strings.Repeat("x=1;", 3)); len(d.fps) != 1 {
		t.Fatalf("exactly K tokens: %d fingerprints", len(d.fps))
	}
}

// randomProgram builds a plausible, distinct C++ program.
func randomProgram(r *rand.Rand, lines int) string {
	var b strings.Builder
	b.WriteString("#include <cstdio>\nint main() {\n")
	ops := []string{"+", "-", "*", "^", "|", "&"}
	for i := 0; i < lines; i++ {
		switch r.Intn(4) {
		case 0:
			fmt.Fprintf(&b, "  int v%d = %d %s v%d;\n", i, r.Intn(100), ops[r.Intn(len(ops))], r.Intn(i+1))
		case 1:
			fmt.Fprintf(&b, "  for (int j = 0; j < %d; j++) v%d %s= j;\n", r.Intn(50), r.Intn(i+1), ops[r.Intn(len(ops))])
		case 2:
			fmt.Fprintf(&b, "  if (v%d > v%d) { printf(\"%%d\\n\", v%d); }\n", r.Intn(i+1), r.Intn(i+1), r.Intn(i+1))
		default:
			fmt.Fprintf(&b, "  while (v%d %s %d > 0) v%d--;\n", r.Intn(i+1), ops[r.Intn(len(ops))], r.Intn(9), r.Intn(i+1))
		}
	}
	b.WriteString("}\n")
	return b.String()
}

// BenchmarkReport500 fingerprints and compares 500 submissions of 150
// lines (a large contest's task).
func BenchmarkReport500(b *testing.B) {
	r := rand.New(rand.NewSource(1))
	srcs := make([]string, 500)
	for i := range srcs {
		srcs[i] = randomProgram(r, 150)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		docs := make([]*Doc, len(srcs))
		for i, s := range srcs {
			docs[i] = doc(int64(i), "a.cpp", s)
		}
		Compare(docs, Options{Threshold: 0.5})
	}
}
