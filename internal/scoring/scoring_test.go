package scoring

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func tcs(outcomes map[string]float64) []Testcase {
	var out []Testcase
	for c, o := range outcomes {
		out = append(out, Testcase{Codename: c, Outcome: o, Evaluated: true, ExitStatus: "ok"})
	}
	return out
}

func mustNew(t *testing.T, name, params string, codes []string, public []bool, prec int) ScoreType {
	t.Helper()
	st, err := New(name, json.RawMessage(params), codes, public, prec)
	if err != nil {
		t.Fatalf("New(%s, %s): %v", name, params, err)
	}
	return st
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestSum(t *testing.T) {
	codes := []string{"a", "b", "c", "d"}
	pub := []bool{true, false, true, false}
	st := mustNew(t, "Sum", `25`, codes, pub, 0)
	if st.MaxScore() != 100 || st.MaxPublicScore() != 50 {
		t.Fatalf("max %v public %v", st.MaxScore(), st.MaxPublicScore())
	}
	r := st.Compute(tcs(map[string]float64{"a": 1, "b": 0.5, "c": 0, "d": 1}))
	if !near(r.Score, 63) || !near(r.PublicScore, 25) { // 25+12.5+0+25 = 62.5 → 63 (precision 0)
		t.Fatalf("score %v public %v", r.Score, r.PublicScore)
	}
	if len(r.PublicDetails.Testcases) != 2 || len(r.Details.Testcases) != 4 {
		t.Fatalf("details %+v", r.Details)
	}
	st2 := mustNew(t, "Sum", `{"points_per_testcase": 2.5}`, codes, pub, 2)
	if r := st2.Compute(tcs(map[string]float64{"a": 1, "b": 1, "c": 1})); !near(r.Score, 7.5) {
		t.Fatalf("partial evaluation score %v (missing testcase counts 0)", r.Score)
	}
}

func TestGroupMinByCountAndPublicSubtasks(t *testing.T) {
	codes := []string{"01", "02", "03", "04", "05"}
	pub := []bool{true, true, false, false, true}
	st := mustNew(t, "GroupMin", `[[30, 2], [70, 3]]`, codes, pub, 0)
	if st.MaxScore() != 100 || st.MaxPublicScore() != 30 || st.NumSubtasks() != 2 {
		t.Fatalf("max %v public %v", st.MaxScore(), st.MaxPublicScore())
	}
	r := st.Compute(tcs(map[string]float64{"01": 1, "02": 1, "03": 1, "04": 0.5, "05": 1}))
	if r.Score != 65 || r.PublicScore != 30 {
		t.Fatalf("score %v public %v", r.Score, r.PublicScore)
	}
	if r.RankingDetails[0] != 30 || r.RankingDetails[1] != 35 {
		t.Fatalf("ranking details %v", r.RankingDetails)
	}
	if !r.Details.Subtasks[0].Public || r.Details.Subtasks[1].Public || len(r.PublicDetails.Subtasks) != 1 {
		t.Fatalf("public flags %+v", r.Details.Subtasks)
	}
}

func TestGroupMulRegexAndList(t *testing.T) {
	codes := []string{"s1_a", "s1_b", "s2_a", "s2_b", "extra"}
	pub := make([]bool, 5)
	st := mustNew(t, "GroupMul", `{"subtasks": [{"max_score": 50, "testcases": "s1_.*"}, {"max_score": 50, "testcases": ["s2_a", "extra"]}]}`, codes, pub, 1)
	r := st.Compute(tcs(map[string]float64{"s1_a": 0.5, "s1_b": 0.5, "s2_a": 1, "extra": 0.9}))
	if !near(r.Score, 12.5+45) {
		t.Fatalf("score %v", r.Score)
	}
}

func TestGroupThreshold(t *testing.T) {
	codes := []string{"a", "b", "c"}
	st := mustNew(t, "GroupThreshold", `[[40, 2, 0.001], [60, 1, 0.5]]`, codes, []bool{false, false, false}, 0)
	r := st.Compute(tcs(map[string]float64{"a": 0.0005, "b": 0.001, "c": 0.7}))
	if r.Score != 40 {
		t.Fatalf("score %v", r.Score)
	}
	// Outcome 0 means failure (not "zero error").
	r = st.Compute(tcs(map[string]float64{"a": 0, "b": 0.001, "c": 0.2}))
	if r.Score != 60 {
		t.Fatalf("score %v", r.Score)
	}
}

func TestScoreTypeErrors(t *testing.T) {
	codes := []string{"a", "b"}
	pub := []bool{false, false}
	for _, c := range []struct{ name, params string }{
		{"Nope", `{}`},
		{"Sum", `"x"`},
		{"Sum", `-1`},
		{"GroupMin", `[[10, 3]]`},
		{"GroupMin", `[[10, "zzz.*"]]`},
		{"GroupMin", `[[10, ["missing"]]]`},
		{"GroupMin", `[]`},
		{"GroupThreshold", `[[10, 2]]`},
		{"GroupMin", `[[10, "("]]`},
	} {
		if _, err := New(c.name, json.RawMessage(c.params), codes, pub, 0); err == nil {
			t.Errorf("%s %s accepted", c.name, c.params)
		}
	}
}

func sub(id int64, min int, score float64, subtasks []float64, tokened bool) Submission {
	return Submission{ID: id, Time: time.Unix(0, 0).Add(time.Duration(min) * time.Minute), Official: true, Scored: true,
		Score: score, Subtasks: subtasks, Tokened: tokened}
}

func TestScoreModes(t *testing.T) {
	subs := []Submission{
		sub(1, 1, 40, []float64{40, 0}, false),
		sub(2, 2, 60, []float64{0, 60}, false),
		sub(3, 3, 30, []float64{30, 0}, true),
		sub(4, 4, 10, []float64{10, 0}, false),
	}
	if ts := Aggregate(ModeMax, subs, 0); ts.Score != 60 {
		t.Errorf("max = %v", ts.Score)
	}
	if ts := Aggregate(ModeMaxSubtask, subs, 0); ts.Score != 100 || ts.Subtasks[0] != 40 || ts.Subtasks[1] != 60 {
		t.Errorf("max_subtask = %+v", ts)
	}
	// max(last = 10, tokened = 30) = 30.
	if ts := Aggregate(ModeMaxTokenedLast, subs, 0); ts.Score != 30 {
		t.Errorf("max_tokened_last = %v", ts.Score)
	}
	// Unofficial (analysis mode) submissions are ignored; pending counted.
	extra := append(append([]Submission{}, subs...), Submission{ID: 5, Time: time.Unix(0, 0).Add(10 * time.Minute), Official: false, Scored: true, Score: 100, Subtasks: []float64{40, 60}},
		Submission{ID: 6, Time: time.Unix(0, 0).Add(11 * time.Minute), Official: true})
	ts := Aggregate(ModeMax, extra, 0)
	if ts.Score != 60 || ts.Pending != 1 || !ts.LastSubmission.Equal(time.Unix(0, 0).Add(11*time.Minute)) {
		t.Errorf("with unofficial/pending: %+v", ts)
	}
	// max_tokened_last with the last submission still pending uses tokened ones.
	if ts := Aggregate(ModeMaxTokenedLast, extra, 0); ts.Score != 30 {
		t.Errorf("max_tokened_last with pending last = %v", ts.Score)
	}
	if ts := Aggregate(ModeMax, nil, 0); ts.Score != 0 || ts.LastSubmission != nil {
		t.Errorf("empty = %+v", ts)
	}
	if ts := Aggregate(ModeMax, []Submission{sub(1, 1, 33.335, nil, false)}, 2); ts.Score != 33.34 {
		t.Errorf("precision = %v", ts.Score)
	}
}

func TestICPC(t *testing.T) {
	start := time.Unix(0, 0)
	ce := sub(1, 5, 0, nil, false)
	ce.CompileError = true
	subs := []Submission{ce, sub(2, 10, 50, nil, false), sub(3, 20, 70, nil, false), sub(4, 42, 100, nil, false), sub(5, 50, 20, nil, false)}
	r := ICPC(subs, 100)
	if !r.Solved || r.Attempts != 2 || !r.SolvedAt.Equal(start.Add(42*time.Minute)) {
		t.Fatalf("icpc = %+v", r)
	}
	if p := ICPCPenalty(r, start, 20); p != 42+40 {
		t.Fatalf("penalty = %d", p)
	}
	unsolved := ICPC(subs[:3], 100)
	if unsolved.Solved || unsolved.Attempts != 2 || ICPCPenalty(unsolved, start, 20) != 0 {
		t.Fatalf("unsolved = %+v", unsolved)
	}
	pending := sub(9, 1, 0, nil, false)
	pending.Scored = false
	if r := ICPC([]Submission{pending}, 100); r.Pending != 1 || r.Solved {
		t.Fatalf("pending = %+v", r)
	}
}

func BenchmarkGroupMin(b *testing.B) {
	var codes []string
	var pub []bool
	outcomes := map[string]float64{}
	for i := 0; i < 100; i++ {
		c := string(rune('A'+i/10)) + string(rune('0'+i%10))
		codes = append(codes, c)
		pub = append(pub, i%3 == 0)
		outcomes[c] = 1
	}
	st, _ := New("GroupMin", json.RawMessage(`[[10,10],[10,10],[10,10],[10,10],[10,10],[10,10],[10,10],[10,10],[10,10],[10,10]]`), codes, pub, 0)
	in := tcs(outcomes)
	for b.Loop() {
		st.Compute(in)
	}
}
