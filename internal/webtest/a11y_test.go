package webtest

import (
	"fmt"
	"strings"
	"testing"
)

// recorder collects what A11y reports.
type recorder struct {
	testing.TB
	errs []string
}

func (r *recorder) Helper() {}
func (r *recorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}
func (r *recorder) Fatalf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

func TestA11yFindsProblems(t *testing.T) {
	bad := `<!doctype html><html lang="en"><head><title> </title></head><body>
<img src="x.png">
<input name="q"><select name="s"></select><textarea name="t"></textarea>
<button><svg aria-hidden="true"></svg></button>
<a href="/x"></a>
<p id="d">1</p><p id="d">2</p>
<div tabindex="3" aria-describedby="nope">x</div>
<label for="ghost">G</label>
</body></html>`
	r := &recorder{TB: t}
	A11y(r, "bad", bad, true)
	all := strings.Join(r.errs, "\n")
	for _, want := range []string{"lang and dir", "no title", "0 main", "no h1", "has no alt", "control input q has no label",
		"control select s", "control textarea t", "button has no text", "link to \"/x\"", `id "d" is used 2 times`, "tabindex=3",
		`missing id "nope"`, `missing id "ghost"`} {
		if !strings.Contains(all, want) {
			t.Errorf("not reported: %s\n%s", want, all)
		}
	}

	good := `<!doctype html><html lang="ar" dir="rtl"><head><title>T</title></head><body><main><h1>H</h1>
<img src="f.png" alt="">
<label for="u">User</label><input id="u" name="u">
<label>Pass <input type="password" name="p"></label>
<select name="l" aria-label="Language"></select>
<input type="hidden" name="csrf" value="x"><button title="Menu"><svg aria-hidden="true"></svg></button>
<a href="/y" title="Bell"><svg aria-hidden="true"></svg></a><a href="/z"><img src="a.png" alt="Home"></a>
<span id="d1">x</span><div aria-labelledby="d1" tabindex="0">y</div>
<input id="late" name="late"><label for="late">After</label>
</main></body></html>`
	r = &recorder{TB: t}
	A11y(r, "good", good, true)
	if len(r.errs) > 0 {
		t.Errorf("false alarms:\n%s", strings.Join(r.errs, "\n"))
	}
}
