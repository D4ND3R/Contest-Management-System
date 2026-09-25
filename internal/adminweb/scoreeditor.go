package adminweb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/D4ND3R/Contest-Management-System/internal/i18n"
	"github.com/D4ND3R/Contest-Management-System/internal/scoring"
)

// scoreEditor is the visual editor of a dataset's score type parameters
// (K5): points per testcase for Sum, one row per subtask for Group*.
// Changes are previewed on the dataset's testcases by re-rendering the
// editor (htmx) and saved with the dataset form.
type scoreEditor struct {
	DatasetID int64
	Testcases []string
	SumPoints string
	SumMax    float64
	Rows      []subtaskRow
	Coverage  scoring.Coverage
	// Raw is set when the stored parameters cannot be shown as rows (the
	// JSON field must be used).
	Raw string
}

// subtaskRow keeps what was typed, so an invalid value is shown back.
type subtaskRow struct {
	Index     int // form field index
	Num       int // subtask number (0 for the blank row)
	Score     string
	Mode      string
	Regex     string
	Count     string
	List      []string
	Threshold string
	Match     *scoring.SubtaskMatch
}

func (r subtaskRow) blank() bool {
	return r.Score == "" && r.Regex == "" && r.Count == "" && len(r.List) == 0
}

func fmtNum(x float64) string { return strconv.FormatFloat(x, 'f', -1, 64) }

// editorFromParams shows stored parameters.
func editorFromParams(datasetID int64, scoreType string, params json.RawMessage, codes []string) *scoreEditor {
	e := &scoreEditor{DatasetID: datasetID, Testcases: sortedCopy(codes), SumPoints: "1"}
	switch scoreType {
	case "Sum":
		var n float64
		if json.Unmarshal(params, &n) == nil {
			e.SumPoints = fmtNum(n)
		} else if s := string(params); s != "{}" && s != "null" && s != "" {
			var obj struct {
				P *float64 `json:"points_per_testcase"`
			}
			if json.Unmarshal(params, &obj) == nil && obj.P != nil {
				e.SumPoints = fmtNum(*obj.P)
			}
		}
	default:
		if s := string(params); s != "{}" && s != "null" && s != "" {
			specs, err := scoring.ParseSubtasks(params)
			if err != nil {
				e.Raw = err.Error()
			}
			for _, sp := range specs {
				e.Rows = append(e.Rows, rowOf(sp))
			}
		}
	}
	e.finish()
	return e
}

func rowOf(sp scoring.SubtaskSpec) subtaskRow {
	r := subtaskRow{Score: fmtNum(sp.MaxScore), Mode: sp.Mode, Regex: sp.Regex, List: sp.List}
	if sp.Mode == "count" {
		r.Count = strconv.Itoa(sp.Count)
	}
	if sp.Threshold != nil {
		r.Threshold = fmtNum(*sp.Threshold)
	}
	return r
}

func sortedCopy(xs []string) []string {
	out := append([]string(nil), xs...)
	sort.Strings(out)
	return out
}

// editorFromForm reads the editor fields; blank and removed rows are
// dropped.
func editorFromForm(f *form, datasetID int64, codes []string) *scoreEditor {
	e := &scoreEditor{DatasetID: datasetID, Testcases: sortedCopy(codes), SumPoints: f.str("sum_points")}
	n, _ := strconv.Atoi(f.str("st_n"))
	if n > 500 {
		n = 500
	}
	for i := 0; i < n; i++ {
		p := "st" + strconv.Itoa(i) + "_"
		r := subtaskRow{Score: f.str(p + "score"), Mode: f.str(p + "mode"), Regex: f.str(p + "regex"),
			Count: f.str(p + "count"), List: f.multi(p + "tc"), Threshold: f.str(p + "threshold")}
		if f.check(p+"remove") || r.blank() {
			continue
		}
		e.Rows = append(e.Rows, r)
	}
	e.finish()
	return e
}

// finish numbers the rows, adds the blank row and previews the matches.
func (e *scoreEditor) finish() {
	for i := range e.Rows {
		e.Rows[i].Index, e.Rows[i].Num = i, i+1
		if e.Rows[i].Mode == "" {
			e.Rows[i].Mode = "regex"
		}
	}
	if x, err := strconv.ParseFloat(e.SumPoints, 64); err == nil {
		e.SumMax = x * float64(len(e.Testcases))
	}
	specs := make([]scoring.SubtaskSpec, len(e.Rows))
	for i, r := range e.Rows {
		specs[i], _ = r.spec()
	}
	e.Coverage = scoring.MatchSubtasks(specs, e.Testcases)
	for i := range e.Rows {
		e.Rows[i].Match = &e.Coverage.Subtasks[i]
	}
	e.Rows = append(e.Rows, subtaskRow{Index: len(e.Rows), Mode: "regex"})
}

// spec converts a row, reporting the first invalid field.
func (r subtaskRow) spec() (scoring.SubtaskSpec, string) {
	sp := scoring.SubtaskSpec{Mode: r.Mode, Regex: r.Regex, List: r.List}
	x, err := strconv.ParseFloat(r.Score, 64)
	if err != nil || x < 0 {
		return sp, "the points must be a non-negative number"
	}
	sp.MaxScore = x
	switch r.Mode {
	case "count":
		n, err := strconv.Atoi(r.Count)
		if err != nil || n <= 0 {
			return sp, "the number of testcases must be a positive integer"
		}
		sp.Count = n
	case "list":
		if len(r.List) == 0 {
			return sp, "choose at least one testcase"
		}
	default:
		sp.Mode = "regex"
		if r.Regex == "" {
			return sp, "write a regular expression"
		}
	}
	if r.Threshold != "" {
		th, err := strconv.ParseFloat(r.Threshold, 64)
		if err != nil {
			return sp, "the threshold must be a number"
		}
		sp.Threshold = &th
	}
	return sp, ""
}

// params validates the edited rows and encodes the parameters of
// scoreType; problems go to f.
func (e *scoreEditor) params(f *form, scoreType string) json.RawMessage {
	if scoreType == "Sum" {
		x, err := strconv.ParseFloat(e.SumPoints, 64)
		if err != nil || x < 0 {
			f.fail("%s must be a number", "Points per testcase")
			return json.RawMessage("1")
		}
		return json.RawMessage(fmtNum(x))
	}
	var specs []scoring.SubtaskSpec
	for _, r := range e.Rows[:len(e.Rows)-1] { // without the blank row
		sp, msg := r.spec()
		if msg != "" {
			f.fail("subtask %d: %s", r.Num, i18n.T(f.lang, msg))
		}
		if scoreType == "GroupThreshold" && sp.Threshold == nil {
			f.fail("subtask %d: %s", r.Num, i18n.T(f.lang, "GroupThreshold needs a threshold"))
		}
		specs = append(specs, sp)
	}
	if len(specs) == 0 {
		f.fail("add at least one subtask")
	}
	return scoring.EncodeSubtasks(specs, scoreType == "GroupThreshold")
}

// handleScoreEditor re-renders the editor from the posted form (preview,
// nothing is saved).
func (s *Server) handleScoreEditor(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	d, ok := s.loadDataset(w, r, rc)
	if !ok {
		return
	}
	tcs, err := s.q.ListTestcases(r.Context(), d.ID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	codes := make([]string, len(tcs))
	for i, tc := range tcs {
		codes[i] = tc.Codename
	}
	e := editorFromForm(newForm(r), d.ID, codes)
	p := s.newPage(w, r, rc, "", "", nil)
	s.renderPartial(w, "score-editor", wrap{P: p, V: e})
}

// joinHead joins the first n values, with an ellipsis for the rest.
func joinHead(xs []string, n int) string {
	if len(xs) <= n {
		return strings.Join(xs, " ")
	}
	return strings.Join(xs[:n], " ") + fmt.Sprintf(" … (+%d)", len(xs)-n)
}
