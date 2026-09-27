package dispatcher_test

import (
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
)

// TestTestcaseJobs (SPEC_MIN §13, §14): a typed input gets its output from
// the reference solution; a generator writes inputs from lines of
// parameters, answered by the solution or left with an empty output; a
// generator that does not compile fails its jobs with the compiler's
// message. The testcases appear in the dataset; nothing is left running.
func TestTestcaseJobs(t *testing.T) {
	e := newEnv(t, true)
	q := sqlc.New(e.pool)
	admin, err := q.CreateAdmin(ctx, sqlc.CreateAdminParams{Name: "Setter", Username: "setter", PasswordHash: "x", Enabled: true, Role: "all"})
	if err != nil {
		t.Fatal(err)
	}
	lang := "c11"
	sol, err := q.CreateTesterSubmission(ctx, sqlc.CreateTesterSubmissionParams{TaskID: e.task.ID, Language: &lang, AdminID: admin.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateSubmissionFiles(ctx, []sqlc.CreateSubmissionFilesParams{{SubmissionID: sol.ID, Filename: "sum.%l", Digest: e.put(srcAC)}}); err != nil {
		t.Fatal(err)
	}
	gen := e.put("#include <stdio.h>\nint main(void){long n;scanf(\"%ld\",&n);printf(\"%ld %ld\\n\",n,n);return 0;}\n")
	bad := e.put("int main(void){ this is not C }\n")
	e.put("") // the empty output

	// job stores a testcase job with its first run, as the admin does:
	// the generator gen on params, or the solution on input.
	job := func(code, output string, input *string, gen, params string) {
		t.Helper()
		p := sqlc.CreateAdminUserTestParams{TaskID: e.task.ID, AdminID: &admin.ID, DatasetID: &e.dataset.ID, Language: &lang}
		file, digest, state := "sum.%l", e.put(srcAC), "output"
		if gen != "" {
			p.Plain, p.InputDigest, file, digest, state = true, e.put(params), "gen.%l", gen, "input"
		} else {
			p.InputDigest = *input
		}
		ut, err := q.CreateAdminUserTest(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		q.CreateUserTestFiles(ctx, []sqlc.CreateUserTestFilesParams{{UserTestID: ut.ID, Filename: file, Digest: digest}})
		if _, err := q.CreateTestcaseJob(ctx, sqlc.CreateTestcaseJobParams{DatasetID: e.dataset.ID, Codename: code, AdminID: &admin.ID,
			InputDigest: input, Output: output, SolutionID: &sol.ID, UserTestID: &ut.ID, State: state}); err != nil {
			t.Fatal(err)
		}
		e.q.Notify(ctx, queue.Event{Kind: queue.EventUserTest, UserTestID: ut.ID})
	}
	typed := e.put("3 4\n")
	job("t1", "solution", &typed, "", "")
	job("g1", "solution", nil, gen, "5\n")
	job("g2", "empty", nil, gen, "6\n")
	job("x1", "solution", nil, bad, "7\n")

	deadline := time.Now().Add(90 * time.Second)
	for {
		if n, _ := q.CountTestcaseJobsRunning(ctx, e.dataset.ID); n == 0 {
			break
		}
		if time.Now().After(deadline) {
			jobs, _ := q.ListTestcaseJobs(ctx, e.dataset.ID)
			t.Fatalf("jobs still running: %+v", jobs)
		}
		time.Sleep(100 * time.Millisecond)
	}
	tcs, _ := q.ListTestcases(ctx, e.dataset.ID)
	got := map[string][2]string{}
	for _, tc := range tcs {
		in, _ := blob.ReadAll(ctx, e.store, tc.InputDigest)
		out, _ := blob.ReadAll(ctx, e.store, tc.OutputDigest)
		got[tc.Codename] = [2]string{string(in), string(out)}
	}
	for code, want := range map[string][2]string{"t1": {"3 4\n", "7\n"}, "g1": {"5 5\n", "10\n"}, "g2": {"6 6\n", ""}} {
		if got[code] != want {
			t.Errorf("testcase %s = %q, want %q", code, got[code], want)
		}
	}
	if _, ok := got["x1"]; ok {
		t.Error("a failed job made a testcase")
	}
	jobs, _ := q.ListTestcaseJobs(ctx, e.dataset.ID)
	if len(jobs) != 1 || jobs[0].Codename != "x1" || jobs[0].State != "failed" || !strings.Contains(jobs[0].Error, "generator: compilation failed") {
		t.Fatalf("left jobs %+v", jobs)
	}
}
