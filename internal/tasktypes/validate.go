package tasktypes

import (
	"encoding/json"
	"fmt"
	"sort"
)

// DefaultParams are starting points for datasets.task_type_params, shown
// by the admin UI when the task type changes.
var DefaultParams = map[string]string{
	"Batch":         `{"compilation": "alone", "input_file": "", "output_file": "", "checker": "white_diff"}`,
	"OutputOnly":    `{"output_pattern": "output_%s.txt", "checker": "white_diff"}`,
	"TwoSteps":      `{"manager": "manager", "checker": "white_diff"}`,
	"Communication": `{"num_processes": 1, "compilation": "stub", "user_io": "fifos"}`,
}

// SortedNames returns the task type names in a stable order.
func SortedNames() []string {
	n := Names()
	sort.Strings(n)
	return n
}

// ValidateParams checks task_type_params for the task type name, so that
// configuration errors are reported when the dataset is saved rather than
// as system errors while judging.
func ValidateParams(name string, raw json.RawMessage) error {
	if _, err := Get(name); err != nil {
		return err
	}
	var cp *CheckerParams
	switch name {
	case "Batch":
		p, err := parseBatch(raw)
		if err != nil {
			return err
		}
		cp = &p.CheckerParams
	case "OutputOnly":
		p, err := parseOutputOnly(raw)
		if err != nil {
			return err
		}
		cp = &p.CheckerParams
	case "TwoSteps":
		p, err := parseTwoSteps(raw)
		if err != nil {
			return err
		}
		cp = &p.CheckerParams
	case "Communication":
		_, err := parseCommunication(raw)
		return err
	}
	if cp != nil {
		switch cp.Checker {
		case "", "white_diff", "exact", "float", "custom", "testlib":
		default:
			return fmt.Errorf("unknown checker %q (white_diff, exact, float, custom, testlib)", cp.Checker)
		}
		if cp.FloatAbsTol < 0 || cp.FloatRelTol < 0 {
			return fmt.Errorf("float tolerances must not be negative")
		}
	}
	return nil
}

// RequiredManagers lists the manager files a dataset needs for its task
// type parameters (informational; languages add their own extensions).
func RequiredManagers(name string, raw json.RawMessage) []string {
	switch name {
	case "Batch":
		if p, err := parseBatch(raw); err == nil {
			var out []string
			if p.Compilation == "grader" {
				out = append(out, "grader.<ext>")
			}
			if p.custom() {
				out = append(out, "checker")
			}
			return out
		}
	case "OutputOnly":
		if p, err := parseOutputOnly(raw); err == nil && p.custom() {
			return []string{"checker"}
		}
	case "TwoSteps":
		if p, err := parseTwoSteps(raw); err == nil {
			out := []string{p.Manager + ".<ext>"}
			if p.custom() {
				out = append(out, "checker")
			}
			return out
		}
	case "Communication":
		if p, err := parseCommunication(raw); err == nil {
			out := []string{"manager"}
			if p.Compilation == "stub" {
				out = append(out, "stub.<ext>")
			}
			return out
		}
	}
	return nil
}
