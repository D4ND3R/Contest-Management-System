package scoring

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// SubtaskSpec is one subtask of a Group* score type in the form edited by
// the admin panel and problem packages.
type SubtaskSpec struct {
	MaxScore float64
	// Mode selects the testcases: "regex" (anchored), "list" (codenames) or
	// "count" (the next consecutive testcases in codename order).
	Mode      string
	Regex     string
	List      []string
	Count     int
	Threshold *float64 // GroupThreshold only
}

// ParseSubtasks decodes Group* parameters (array or object form).
func ParseSubtasks(raw json.RawMessage) ([]SubtaskSpec, error) {
	ps, err := parseGroupParams(raw)
	if err != nil {
		return nil, err
	}
	out := make([]SubtaskSpec, len(ps))
	for i, p := range ps {
		sp := SubtaskSpec{MaxScore: p.MaxScore, Threshold: p.Threshold}
		switch {
		case json.Unmarshal(p.Testcases, &sp.Count) == nil:
			sp.Mode = "count"
		case json.Unmarshal(p.Testcases, &sp.Regex) == nil:
			sp.Mode = "regex"
		case json.Unmarshal(p.Testcases, &sp.List) == nil:
			sp.Mode = "list"
		default:
			return nil, fmt.Errorf("subtask %d: testcases must be a count, a regex or a list", i+1)
		}
		out[i] = sp
	}
	return out, nil
}

// testcasesJSON is the "testcases" element of a spec.
func (sp SubtaskSpec) testcasesJSON() json.RawMessage {
	var v any
	switch sp.Mode {
	case "count":
		v = sp.Count
	case "list":
		v = sp.List
		if sp.List == nil {
			v = []string{}
		}
	default:
		v = sp.Regex
	}
	b, _ := json.Marshal(v)
	return b
}

// EncodeSubtasks returns Group* parameters in the CMS array form
// [[max_score, testcases(, threshold)], ...]; thresholds are written when
// withThreshold is set (GroupThreshold), 1 when missing.
func EncodeSubtasks(specs []SubtaskSpec, withThreshold bool) json.RawMessage {
	rows := make([][]json.RawMessage, len(specs))
	for i, sp := range specs {
		row := []json.RawMessage{json.RawMessage(strconv.FormatFloat(sp.MaxScore, 'f', -1, 64)), sp.testcasesJSON()}
		if withThreshold {
			th := 1.0
			if sp.Threshold != nil {
				th = *sp.Threshold
			}
			row = append(row, json.RawMessage(strconv.FormatFloat(th, 'f', -1, 64)))
		}
		rows[i] = row
	}
	b, _ := json.Marshal(rows)
	return b
}

// SubtaskMatch is the preview of one subtask on a dataset's testcases.
type SubtaskMatch struct {
	Testcases []string
	Error     string
}

// Coverage summarises how subtasks cover a dataset's testcases.
type Coverage struct {
	Subtasks  []SubtaskMatch
	Total     float64
	Uncovered []string // in no subtask
	Shared    []string // in more than one subtask
}

// MatchSubtasks resolves the specs on the testcases exactly as the score
// type does (codenames are sorted first).
func MatchSubtasks(specs []SubtaskSpec, codenames []string) Coverage {
	codes := append([]string(nil), codenames...)
	sort.Strings(codes)
	var cov Coverage
	uses := make([]int, len(codes))
	next := 0
	for i, sp := range specs {
		cov.Total += sp.MaxScore
		var m SubtaskMatch
		idx, err := members(i, sp.testcasesJSON(), codes, &next)
		switch {
		case err != nil:
			m.Error = err.Error()
		case len(idx) == 0:
			m.Error = fmt.Sprintf("subtask %d matches no testcase", i+1)
		}
		for _, k := range idx {
			m.Testcases = append(m.Testcases, codes[k])
			uses[k]++
		}
		cov.Subtasks = append(cov.Subtasks, m)
	}
	for k, n := range uses {
		switch {
		case n == 0:
			cov.Uncovered = append(cov.Uncovered, codes[k])
		case n > 1:
			cov.Shared = append(cov.Shared, codes[k])
		}
	}
	return cov
}
