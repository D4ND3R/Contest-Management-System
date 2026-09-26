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
	VerdictSecurity     = "SV"
)

// ICPCVerdict is the binary verdict of a submission scored with details d:
// accepted when it reaches maxScore (as ICPC aggregation counts it),
// otherwise the kind of failure of the first testcase that did not pass,
// in the order the details list them (testcases skipped by the
// short-circuit are not failures of their own).
func ICPCVerdict(d Details, score, maxScore float64) string {
	if maxScore > 0 && score >= maxScore-1e-9 {
		return VerdictAccepted
	}
	for _, st := range d.Subtasks {
		for _, tc := range st.Testcases {
			if tc.Outcome < 1 && tc.Status != StatusSkipped {
				return failureVerdict(tc.Status)
			}
		}
	}
	for _, tc := range d.Testcases {
		if tc.Outcome < 1 && tc.Status != StatusSkipped {
			return failureVerdict(tc.Status)
		}
	}
	return VerdictWrong
}

// StatusSkipped is the exit status of a testcase the short-circuit did not
// run, and MsgSkipped its text (translated by the web UI).
const (
	StatusSkipped = "skipped"
	MsgSkipped    = "Skipped: another testcase of the subtask failed"
)

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
	case "security":
		return VerdictSecurity
	}
	return VerdictWrong
}
