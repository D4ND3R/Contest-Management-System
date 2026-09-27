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
	// VerdictPartial: some points, not all (IOI contests; never stored,
	// derived from the score when shown).
	VerdictPartial = "PA"
	// VerdictSkipped: a testcase the short-circuit did not run.
	VerdictSkipped = "SK"
)

// Verdict is the verdict of a scored submission as an IOI contest shows it:
// accepted with every point, partial with some, otherwise the kind of
// failure stored at scoring time (stored: the ICPCVerdict of the result).
func Verdict(stored string, score, maxScore float64) string {
	switch {
	case maxScore > 0 && score >= maxScore-1e-9:
		return VerdictAccepted
	case score > 1e-9:
		return VerdictPartial
	case stored == "" || stored == VerdictAccepted:
		return VerdictWrong
	}
	return stored
}

// TestcaseVerdict is the verdict of one evaluated testcase.
func TestcaseVerdict(tc TestcaseDetail) string {
	switch {
	case tc.Status == StatusSkipped:
		return VerdictSkipped
	case tc.Outcome >= 1-1e-9:
		return VerdictAccepted
	case tc.Outcome > 1e-9:
		return VerdictPartial
	}
	return failureVerdict(tc.Status)
}

// SubtaskVerdict is the verdict of a subtask: accepted, partial, or the
// failure of its first testcase that did not pass.
func SubtaskVerdict(st SubtaskDetail) string {
	switch {
	case st.Fraction >= 1-1e-9:
		return VerdictAccepted
	case st.Fraction > 1e-9:
		return VerdictPartial
	}
	for _, tc := range st.Testcases {
		if tc.Outcome < 1 && tc.Status != StatusSkipped {
			if v := TestcaseVerdict(tc); v != VerdictPartial {
				return v
			}
		}
	}
	return VerdictWrong
}

// VerdictClass is the colour class of a verdict: ok (green), pa (yellow),
// bad (red) or pending (grey).
func VerdictClass(v string) string {
	switch v {
	case VerdictAccepted:
		return "ok"
	case VerdictPartial:
		return "pa"
	case VerdictSkipped, "":
		return "pending"
	}
	return "bad"
}

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
