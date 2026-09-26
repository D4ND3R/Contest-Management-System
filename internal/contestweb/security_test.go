package contestweb

import (
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/suspicious"
)

// TestSuspiciousSourceFlagged (SPEC_IOI H3): a source that tries to run
// other programs is accepted and judged as usual, and flagged for the
// staff with where it was seen; a clean one is not.
func TestSuspiciousSourceFlagged(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	c := f.client()
	_, page := f.login(c, "ana", "secret")
	csrf := csrfOf(t, page)
	if code, body := f.submit(c, csrf, "c11", "#include <stdlib.h>\nint main(){\n  system(\"cat x\");\n}\n", false); code != 200 {
		t.Fatalf("submit: %d %s", code, body)
	}
	if code, _ := f.submit(c, csrf, "c11", "int main(){}\n", false); code != 200 {
		t.Fatalf("clean submit: %d", code)
	}
	subs, _ := f.q.ListSubmissionsByParticipation(bg, f.part.ID)
	if len(subs) != 2 {
		t.Fatalf("submissions %+v", subs)
	}
	flagged := 0
	for _, s := range subs {
		flags, err := f.q.ListSubmissionFlags(bg, s.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(flags) > 0 {
			flagged++
			if flags[0].Kind != "source" || flags[0].Reason != suspicious.Process || flags[0].Detail != `sum.%l:3: system("cat x");` {
				t.Fatalf("flag %+v", flags[0])
			}
		}
	}
	if flagged != 1 {
		t.Fatalf("%d flagged submissions, want 1", flagged)
	}
}
