package problempkg

import (
	"fmt"
	"strings"
)

// RunResult is a reference solution's result on one dataset.
type RunResult struct {
	Judged      bool   // a result exists
	Compiled    *bool  // nil while compiling
	Scored      bool   // evaluation finished
	SystemError string // the judge failed (e.g. a checker that does not compile)
	Score       float64
	MaxScore    float64
	// Failure kinds seen on some testcase.
	TLE, MLE, RE, WA bool
}

// Check compares a run with the expected verdict: "pass", "fail" or
// "pending", and what happened ("ac", "pa 30/100", "wa tle", "ce", ...).
func Check(expected string, r RunResult) (status, got string) {
	switch {
	case r.SystemError != "":
		return "fail", "system error: " + r.SystemError
	case !r.Judged || r.Compiled == nil:
		return "pending", ""
	case !*r.Compiled:
		return verdictIf(expected == "ce" || expected == "any"), "ce"
	case !r.Scored:
		return "pending", ""
	}
	const eps = 1e-9
	ac := r.Score >= r.MaxScore-eps
	var kinds []string
	for _, k := range []struct {
		on   bool
		name string
	}{{r.WA, "wa"}, {r.TLE, "tle"}, {r.MLE, "mle"}, {r.RE, "re"}} {
		if k.on {
			kinds = append(kinds, k.name)
		}
	}
	switch {
	case ac:
		got = "ac"
	case r.Score > eps:
		got = strings.TrimSpace(fmt.Sprintf("pa %s/%s %s", num(r.Score), num(r.MaxScore), strings.Join(kinds, " ")))
	default:
		got = strings.Join(kinds, " ")
		if got == "" {
			got = "0"
		}
	}
	switch expected {
	case "any":
		return "pass", got
	case "ac":
		return verdictIf(ac), got
	case "pa":
		return verdictIf(!ac && r.Score > eps), got
	case "ce":
		return "fail", got
	}
	for _, k := range kinds {
		if k == expected {
			return verdictIf(!ac), got
		}
	}
	return "fail", got
}

func verdictIf(ok bool) string {
	if ok {
		return "pass"
	}
	return "fail"
}

func num(x float64) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", x), "0"), ".")
}
