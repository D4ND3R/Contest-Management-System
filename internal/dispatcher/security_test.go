package dispatcher_test

import (
	"os/exec"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/suspicious"
)

// srcUnshare solves the task but first tries to create a user namespace.
const srcUnshare = "#define _GNU_SOURCE\n#include <sched.h>\n#include <stdio.h>\n" +
	"int main(void){long a,b;scanf(\"%ld %ld\",&a,&b);unshare(CLONE_NEWUSER);printf(\"%ld\\n\",a+b);return 0;}\n"

// TestSecurityViolation (SPEC_IOI H3): a program making a forbidden system
// call is killed by the seccomp filter: its testcases end in a security
// violation (verdict SV, no points) and the submission is flagged for the
// staff, once however many testcases saw it.
func TestSecurityViolation(t *testing.T) {
	if _, err := exec.LookPath("cc"); err != nil {
		t.Skip("no C compiler: the worker runs without the seccomp filter")
	}
	e := newEnv(t, true)
	id := e.submit(srcUnshare, true)
	r := e.waitScored(id, e.dataset.ID, 60*time.Second)
	if r.Verdict == nil || *r.Verdict != "SV" || r.Score == nil || *r.Score != 0 {
		e.logResult(r)
		e.logEvaluations(id, e.dataset.ID)
		t.Fatalf("verdict %v score %v", deref(r.Verdict), derefF(r.Score))
	}
	q := sqlc.New(e.pool)
	evs, err := q.ListEvaluations(ctx, sqlc.ListEvaluationsParams{SubmissionID: id, DatasetID: e.dataset.ID})
	if err != nil || len(evs) == 0 {
		t.Fatalf("evaluations %v %v", evs, err)
	}
	for _, ev := range evs {
		if ev.ExitStatus != "security" || ev.Text != "Security violation: the program made a forbidden system call" {
			t.Fatalf("evaluation %+v", ev)
		}
	}
	flags, err := q.ListSubmissionFlags(ctx, id)
	if err != nil || len(flags) != 1 || flags[0].Kind != "runtime" || flags[0].Reason != suspicious.Forbidden {
		t.Fatalf("flags %+v %v", flags, err)
	}
}
