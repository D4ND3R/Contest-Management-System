package scoring

// ICPC verdicts of a scored submission.
const (
	VerdictAccepted     = "AC"
	VerdictWrong        = "WA"
	VerdictTime         = "TLE"
	VerdictMemory       = "MLE"
	VerdictRuntime      = "RE"
	VerdictOutputLimit  = "OLE"
	VerdictCompileError = "CE"
)

// ICPCVerdict is the binary verdict of a submission scored with details d:
// accepted when it reaches maxScore (as ICPC aggregation counts it),
// otherwise the kind of failure of the first testcase that did not pass,
// in the order the details list them.
func ICPCVerdict(d Details, score, maxScore float64) string {
	if maxScore > 0 && score >= maxScore-1e-9 {
		return VerdictAccepted
	}
	for _, st := range d.Subtasks {
		for _, tc := range st.Testcases {
			if tc.Outcome < 1 {
				return failureVerdict(tc.Status)
			}
		}
	}
	for _, tc := range d.Testcases {
		if tc.Outcome < 1 {
			return failureVerdict(tc.Status)
		}
	}
	return VerdictWrong
}

// failureVerdict maps a sandbox exit status to a verdict.
func failureVerdict(status string) string {
	switch status {
	case "timeout", "timeout_wall":
		return VerdictTime
	case "memory":
		return VerdictMemory
	case "signal", "nonzero":
		return VerdictRuntime
	case "output_limit":
		return VerdictOutputLimit
	}
	return VerdictWrong
}
