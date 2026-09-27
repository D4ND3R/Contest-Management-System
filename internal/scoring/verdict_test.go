package scoring

import "testing"

func TestICPCVerdict(t *testing.T) {
	tc := func(outcome float64, status string) TestcaseDetail {
		return TestcaseDetail{Outcome: outcome, Status: status}
	}
	sum := func(tcs ...TestcaseDetail) Details { return Details{Type: "sum", Testcases: tcs} }
	for _, c := range []struct {
		name       string
		d          Details
		score, max float64
		want       string
	}{
		{"accepted", sum(tc(1, "ok"), tc(1, "ok")), 100, 100, VerdictAccepted},
		{"wrong", sum(tc(1, "ok"), tc(0, "ok"), tc(0, "timeout")), 50, 100, VerdictWrong},
		{"first failure wins", sum(tc(0, "timeout"), tc(0, "ok")), 0, 100, VerdictTime},
		{"wall time", sum(tc(0, "timeout_wall")), 0, 100, VerdictTime},
		{"memory", sum(tc(0, "memory")), 0, 100, VerdictMemory},
		{"crash", sum(tc(0, "signal")), 0, 100, VerdictRuntime},
		{"exit code", sum(tc(0, "nonzero")), 0, 100, VerdictRuntime},
		{"output limit", sum(tc(0, "output_limit")), 0, 100, VerdictOutputLimit},
		{"partial outcome", sum(tc(0.5, "ok")), 50, 100, VerdictWrong},
		{"no max score", sum(), 0, 0, VerdictWrong},
		{"groups", Details{Type: "group", Subtasks: []SubtaskDetail{
			{Testcases: []TestcaseDetail{tc(1, "ok")}},
			{Testcases: []TestcaseDetail{tc(1, "ok"), tc(0, "memory")}},
		}}, 40, 100, VerdictMemory},
	} {
		if got := ICPCVerdict(c.d, c.score, c.max); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

// TestIOIVerdicts (SPEC_MIN §9): partial scores are PA, full scores AC,
// and a zero keeps the kind of failure.
func TestIOIVerdicts(t *testing.T) {
	cases := []struct {
		stored     string
		score, max float64
		want       string
	}{
		{"AC", 100, 100, "AC"}, {"WA", 40, 100, "PA"}, {"TLE", 0.5, 100, "PA"}, {"TLE", 0, 100, "TLE"},
		{"", 0, 100, "WA"}, {"AC", 0, 100, "WA"},
	}
	for _, c := range cases {
		if got := Verdict(c.stored, c.score, c.max); got != c.want {
			t.Errorf("Verdict(%q, %v, %v) = %s, want %s", c.stored, c.score, c.max, got, c.want)
		}
	}
	tcs := []struct {
		tc   TestcaseDetail
		want string
	}{
		{TestcaseDetail{Outcome: 1, Status: "ok"}, "AC"}, {TestcaseDetail{Outcome: 0.3, Status: "ok"}, "PA"},
		{TestcaseDetail{Outcome: 0, Status: "ok"}, "WA"}, {TestcaseDetail{Outcome: 0, Status: "timeout"}, "TLE"},
		{TestcaseDetail{Status: StatusSkipped}, "SK"},
	}
	for _, c := range tcs {
		if got := TestcaseVerdict(c.tc); got != c.want {
			t.Errorf("TestcaseVerdict(%+v) = %s, want %s", c.tc, got, c.want)
		}
	}
	st := SubtaskDetail{Fraction: 0, Testcases: []TestcaseDetail{{Outcome: 1}, {Outcome: 0, Status: "memory"}}}
	if v := SubtaskVerdict(st); v != "MLE" || VerdictClass(v) != "bad" || VerdictClass("PA") != "pa" || VerdictClass("AC") != "ok" {
		t.Fatalf("subtask verdict %s", v)
	}
}
