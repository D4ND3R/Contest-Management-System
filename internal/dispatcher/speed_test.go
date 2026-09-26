package dispatcher_test

import (
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/dispatcher"
	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/D4ND3R/Contest-Management-System/internal/scoring"
)

// next takes the next job from the queues (the test plays the worker).
func (e *env) next() *queue.Delivery {
	e.t.Helper()
	d, err := e.q.Next(ctx, "fake/0", 10*time.Second, nil)
	if err != nil || d == nil {
		e.t.Fatalf("no job: %v", err)
	}
	return d
}

// answer completes a job: compilations succeed, testcases get outcome().
func (e *env) answer(d *queue.Delivery, outcome func(codename string) float64) {
	e.t.Helper()
	r := jobs.ForJob(d.Job, "fake")
	switch d.Job.Kind {
	case jobs.KindCompile:
		r.Compilation = &jobs.Compilation{Success: true, Text: "Compilation succeeded", Executables: []jobs.File{{Name: "sum", Digest: e.put("ELF"), Size: 3}}}
	case jobs.KindEvaluate:
		for _, tc := range d.Job.Testcases {
			r.Testcases = append(r.Testcases, tc.ID)
			r.Evaluations = append(r.Evaluations, jobs.Evaluation{TestcaseID: tc.ID, Codename: tc.Codename, Outcome: outcome(tc.Codename), Text: "Output is correct", ExitStatus: "ok"})
		}
	default:
		e.t.Fatalf("unexpected job %s", d.Job.Kind)
	}
	if err := e.q.Complete(ctx, d, r); err != nil {
		e.t.Fatal(err)
	}
}

// TestShortCircuit (SPEC_IOI H4): with the option on, once a testcase of
// a GroupMin subtask scores 0 the other testcases of the subtask are
// marked skipped (the workers are told not to run them) and the result
// completes with the same score as a full evaluation.
func TestShortCircuit(t *testing.T) {
	e := newEnv(t, false)
	q := sqlc.New(e.pool)
	u := db.DatasetToUpdate(e.dataset)
	u.ShortCircuit = true
	if _, err := q.UpdateDataset(ctx, u); err != nil {
		t.Fatal(err)
	}
	id := e.submit(srcWA, true)
	e.answer(e.next(), nil) // compile
	// Subtask 1 is testcases 00 and 01, subtask 2 is 02 and 03.
	tcs, err := q.ListTestcases(ctx, e.dataset.ID)
	if err != nil || len(tcs) != 4 {
		t.Fatalf("testcases %v %v", tcs, err)
	}
	outcome := map[string]float64{"00": 0, "01": 1, "02": 1, "03": 1}
	var first *queue.Delivery
	rest := map[string]*queue.Delivery{}
	for range 4 {
		d := e.next()
		if d.Job.Testcases[0].Codename == "00" {
			first = d
		} else {
			rest[d.Job.Testcases[0].Codename] = d
		}
	}
	e.answer(first, func(c string) float64 { return outcome[c] })
	gen := first.Job.Generation
	deadline := time.Now().Add(10 * time.Second)
	for !e.q.Skipped(ctx, id, e.dataset.ID, gen, tcs[1].ID) {
		if time.Now().After(deadline) {
			t.Fatal("testcase 01 not skipped")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if e.q.Skipped(ctx, id, e.dataset.ID, gen, tcs[2].ID) || e.q.Skipped(ctx, id, e.dataset.ID, gen, tcs[3].ID) {
		t.Fatal("subtask 2 skipped")
	}
	// 01 is not run (the worker sees the mark), the others are.
	e.q.Ack(ctx, rest["01"])
	e.answer(rest["02"], func(c string) float64 { return outcome[c] })
	e.answer(rest["03"], func(c string) float64 { return outcome[c] })
	r := e.waitScored(id, e.dataset.ID, 10*time.Second)
	if r.Score == nil || *r.Score != 70 || r.TestcasesDone != 4 {
		e.logResult(r)
		t.Fatalf("score %v done %d", derefF(r.Score), r.TestcasesDone)
	}
	evs, _ := q.ListEvaluations(ctx, sqlc.ListEvaluationsParams{SubmissionID: id, DatasetID: e.dataset.ID})
	skipped := 0
	for _, ev := range evs {
		if ev.ExitStatus == scoring.StatusSkipped {
			skipped++
			if ev.TestcaseID != tcs[1].ID || ev.Text != scoring.MsgSkipped || ev.Outcome != 0 {
				t.Fatalf("skipped evaluation %+v", ev)
			}
		}
	}
	if skipped != 1 {
		t.Fatalf("%d skipped evaluations", skipped)
	}
}

// TestCompilationCache (SPEC_IOI H4): a submission with the same source,
// language and dataset as an earlier one takes its executables: no
// compile job, straight to evaluation.
func TestCompilationCache(t *testing.T) {
	e := newEnv(t, false)
	all := func(string) float64 { return 1 }
	first := e.submit(srcAC, true)
	if d := e.next(); d.Job.Kind != jobs.KindCompile {
		t.Fatalf("first job %s", d.Job.Kind)
	} else {
		e.answer(d, nil)
	}
	for range 4 {
		e.answer(e.next(), all)
	}
	e.waitScored(first, e.dataset.ID, 10*time.Second)
	second := e.submit(srcAC, true)
	for range 4 {
		d := e.next()
		if d.Job.Kind != jobs.KindEvaluate || d.Job.SubmissionID != second || len(d.Job.Executables) != 1 || d.Job.Executables[0].Name != "sum" {
			t.Fatalf("job %s for %d: %+v", d.Job.Kind, d.Job.SubmissionID, d.Job.Executables)
		}
		e.answer(d, all)
	}
	r := e.waitScored(second, e.dataset.ID, 10*time.Second)
	if *r.Score != 100 || r.CompilationText != "Compilation succeeded" {
		t.Fatalf("second: %v %q", *r.Score, r.CompilationText)
	}
	// Another source compiles.
	third := e.submit(srcWA, true)
	if d := e.next(); d.Job.Kind != jobs.KindCompile || d.Job.SubmissionID != third {
		t.Fatalf("third: %s", d.Job.Kind)
	} else {
		e.answer(d, nil)
	}
	for range 4 {
		e.answer(e.next(), func(string) float64 { return 0 })
	}
	e.waitScored(third, e.dataset.ID, 10*time.Second)
	// An explicit recompilation runs the compiler again.
	if _, err := dispatcher.Invalidate(ctx, e.pool, e.q, dispatcher.Scope{SubmissionID: second}, dispatcher.Recompile); err != nil {
		t.Fatal(err)
	}
	if d := e.next(); d.Job.Kind != jobs.KindCompile || d.Job.SubmissionID != second {
		t.Fatalf("recompilation: %s for %d", d.Job.Kind, d.Job.SubmissionID)
	}
}

// TestNewerSubmissionSupersedes (SPEC_IOI H4): when a contestant submits
// again to a task, their earlier submissions still being judged are marked
// superseded (the workers move their jobs to the deferred queue).
func TestNewerSubmissionSupersedes(t *testing.T) {
	e := newEnv(t, false)
	old := e.submit(srcWA, true)
	e.next() // its compilation, not answered yet
	newer := e.submit(srcAC, true)
	for deadline := time.Now().Add(10 * time.Second); !e.q.Superseded(ctx, old); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("older submission not superseded")
		}
	}
	if e.q.Superseded(ctx, newer) {
		t.Fatal("newest submission superseded")
	}
}

// TestTimeMultiplier (SPEC_IOI §3): the jobs of a language with a
// time_multiplier carry the scaled time limits.
func TestTimeMultiplier(t *testing.T) {
	e := newEnv(t, false)
	e.submitIn("c11x2", srcAC, true)
	d := e.next()
	e.answer(d, nil)
	if d = e.next(); d.Job.Kind != jobs.KindEvaluate || d.Job.Limits.TimeMs != 6000 {
		t.Fatalf("%s limits %+v", d.Job.Kind, d.Job.Limits)
	}
	e.submitIn("c11", srcWA, true)
	for d = e.next(); d.Job.Kind != jobs.KindCompile; d = e.next() {
	}
	e.answer(d, nil)
	for d = e.next(); d.Job.Language.ID != "c11" || d.Job.Kind != jobs.KindEvaluate; d = e.next() {
	}
	if d.Job.Limits.TimeMs != 3000 {
		t.Fatalf("c11 limits %+v", d.Job.Limits)
	}
}

// TestPublishWarm (SPEC_IOI §11): the testcases and managers of the live
// datasets of running and upcoming contests are published for the workers
// to download; a contest far in the future is not.
func TestPublishWarm(t *testing.T) {
	e := newEnv(t, false)
	q := sqlc.New(e.pool)
	if _, err := q.UpsertManager(ctx, sqlc.UpsertManagerParams{DatasetID: e.dataset.ID, Filename: "checker", Digest: e.put("checker")}); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(48 * time.Hour)
	far, _ := q.CreateContest(ctx, db.NewContestParams("later", later, later.Add(time.Hour)))
	tp := db.NewTaskParams("later", "Later")
	tp.ContestID = &far.ID
	task, _ := q.CreateTask(ctx, tp)
	ds, _ := q.CreateDataset(ctx, db.NewDatasetParams(task.ID, "v1"))
	q.SetActiveDataset(ctx, sqlc.SetActiveDatasetParams{ID: task.ID, ActiveDatasetID: &ds.ID})
	q.UpsertTestcase(ctx, sqlc.UpsertTestcaseParams{DatasetID: ds.ID, Codename: "x", InputDigest: e.put("far in\n"), OutputDigest: e.put("far out\n")})
	if err := e.disp.PublishWarm(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := e.q.WarmDigests(ctx)
	want := map[string]bool{e.put("checker"): true}
	for _, s := range []string{"1 2", "3", "2 2", "4", "10 20", "30", "7 -2", "5"} {
		want[e.put(s+"\n")] = true
	}
	if len(got) != len(want) {
		t.Fatalf("%d digests published, want %d", len(got), len(want))
	}
	for _, d := range got {
		if !want[d] {
			t.Fatalf("unexpected digest %s", d)
		}
	}
}
