// Package scoring turns testcase outcomes into submission scores (score
// types) and submission scores into task scores (score modes), including
// the ICPC aggregation.
package scoring

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
)

// Testcase is the outcome of one testcase as seen by a score type.
type Testcase struct {
	ID         int64   `json:"-"`
	Codename   string  `json:"codename"`
	Public     bool    `json:"public"`
	Evaluated  bool    `json:"-"`
	Outcome    float64 `json:"outcome"`
	Text       string  `json:"text"`
	Time       float64 `json:"time"`
	Memory     int64   `json:"memory"`
	ExitStatus string  `json:"status"`
}

// TestcaseDetail is one row of the details shown to contestants.
type TestcaseDetail struct {
	Codename string  `json:"codename"`
	Public   bool    `json:"public"`
	Outcome  float64 `json:"outcome"`
	Text     string  `json:"text"`
	Time     float64 `json:"time"`
	Memory   int64   `json:"memory"`
	Status   string  `json:"status"`
	// Points is set by the Sum score type (points of this testcase).
	Points *float64 `json:"points,omitempty"`
}

// SubtaskDetail is one subtask of a group score type.
type SubtaskDetail struct {
	Index     int              `json:"index"` // 1-based
	MaxScore  float64          `json:"max_score"`
	Score     float64          `json:"score"`
	Fraction  float64          `json:"fraction"`
	Public    bool             `json:"public"`
	Testcases []TestcaseDetail `json:"testcases"`
}

// Details is the score_details / public_score_details JSON document.
type Details struct {
	Type      string           `json:"type"` // "sum" or "group"
	MaxScore  float64          `json:"max_score"`
	Subtasks  []SubtaskDetail  `json:"subtasks,omitempty"`
	Testcases []TestcaseDetail `json:"testcases,omitempty"`
}

// Result of scoring one submission on one dataset.
type Result struct {
	Score         float64
	Details       Details
	PublicScore   float64
	PublicDetails Details
	// Subtask scores for the ranking and for max_subtask (one per subtask;
	// a single entry for Sum).
	RankingDetails []float64
}

// ScoreType computes scores for a dataset.
type ScoreType interface {
	MaxScore() float64
	MaxPublicScore() float64
	// NumSubtasks is the number of entries of Result.RankingDetails.
	NumSubtasks() int
	// SubtaskMaxScores are the maximum of each RankingDetails entry.
	SubtaskMaxScores() []float64
	// Compute scores testcases (which must be the dataset's testcases).
	Compute(tcs []Testcase) Result
}

// round rounds v to precision decimal digits.
func round(v float64, precision int) float64 {
	p := math.Pow(10, float64(precision))
	return math.Round(v*p) / p
}

// testcaseDef is the scoring-time view of a dataset's testcases.
type testcaseDef struct {
	codename string
	public   bool
}

// New builds the score type name with params for the dataset's testcases
// (in any order; they are sorted by codename).
func New(name string, params json.RawMessage, codenames []string, public []bool, precision int) (ScoreType, error) {
	if len(codenames) != len(public) {
		return nil, errors.New("codenames and public flags differ in length")
	}
	defs := make([]testcaseDef, len(codenames))
	for i := range codenames {
		defs[i] = testcaseDef{codenames[i], public[i]}
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].codename < defs[j].codename })
	switch name {
	case "Sum":
		return newSum(params, defs, precision)
	case "GroupMin", "GroupMul", "GroupThreshold":
		return newGroup(name, params, defs, precision)
	default:
		return nil, fmt.Errorf("unknown score type %q", name)
	}
}

// ---------------------------------------------------------------- Sum

type sum struct {
	points    float64
	defs      []testcaseDef
	precision int
}

func newSum(params json.RawMessage, defs []testcaseDef, precision int) (*sum, error) {
	s := &sum{defs: defs, precision: precision, points: 1}
	if len(params) > 0 && string(params) != "null" && string(params) != "{}" {
		var n float64
		if err := json.Unmarshal(params, &n); err != nil {
			var obj struct {
				PointsPerTestcase *float64 `json:"points_per_testcase"`
			}
			if err := json.Unmarshal(params, &obj); err != nil || obj.PointsPerTestcase == nil {
				return nil, fmt.Errorf("Sum parameters must be a number or {\"points_per_testcase\": n}")
			}
			n = *obj.PointsPerTestcase
		}
		if n < 0 {
			return nil, errors.New("Sum points must be non-negative")
		}
		s.points = n
	}
	return s, nil
}

func (s *sum) MaxScore() float64 { return round(s.points*float64(len(s.defs)), s.precision) }
func (s *sum) NumSubtasks() int  { return 1 }

func (s *sum) SubtaskMaxScores() []float64 { return []float64{s.MaxScore()} }

func (s *sum) MaxPublicScore() float64 {
	n := 0
	for _, d := range s.defs {
		if d.public {
			n++
		}
	}
	return round(s.points*float64(n), s.precision)
}

func index(tcs []Testcase) map[string]Testcase {
	m := make(map[string]Testcase, len(tcs))
	for _, t := range tcs {
		m[t.Codename] = t
	}
	return m
}

func detail(d testcaseDef, t Testcase, ok bool) TestcaseDetail {
	if !ok {
		return TestcaseDetail{Codename: d.codename, Public: d.public, Status: "pending"}
	}
	return TestcaseDetail{Codename: d.codename, Public: d.public, Outcome: t.Outcome, Text: t.Text,
		Time: t.Time, Memory: t.Memory, Status: t.ExitStatus}
}

func (s *sum) Compute(tcs []Testcase) Result {
	byName := index(tcs)
	var score, pub float64
	det := Details{Type: "sum", MaxScore: s.MaxScore()}
	pdet := Details{Type: "sum", MaxScore: s.MaxPublicScore()}
	for _, d := range s.defs {
		t, ok := byName[d.codename]
		pts := 0.0
		if ok {
			pts = s.points * clamp01(t.Outcome)
		}
		td := detail(d, t, ok)
		p := round(pts, s.precision)
		td.Points = &p
		score += pts
		det.Testcases = append(det.Testcases, td)
		if d.public {
			pub += pts
			pdet.Testcases = append(pdet.Testcases, td)
		}
	}
	score, pub = round(score, s.precision), round(pub, s.precision)
	return Result{Score: score, Details: det, PublicScore: pub, PublicDetails: pdet, RankingDetails: []float64{score}}
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// ---------------------------------------------------------------- groups

type group struct {
	kind      string
	subtasks  []groupDef
	defs      []testcaseDef
	precision int
}

type groupDef struct {
	maxScore  float64
	threshold float64
	members   []int // indexes into defs
	public    bool
}

// groupParam is one subtask in either the CMS array form
// [max_score, testcases(, threshold)] or the object form.
type groupParam struct {
	MaxScore  float64         `json:"max_score"`
	Testcases json.RawMessage `json:"testcases"`
	Threshold *float64        `json:"threshold"`
}

func parseGroupParams(raw json.RawMessage) ([]groupParam, error) {
	var obj struct {
		Subtasks []groupParam `json:"subtasks"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.Subtasks != nil {
		return obj.Subtasks, nil
	}
	var arr [][]json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, errors.New(`group parameters must be [[max_score, testcases], ...] or {"subtasks": [...]}`)
	}
	out := make([]groupParam, len(arr))
	for i, a := range arr {
		if len(a) < 2 || len(a) > 3 {
			return nil, fmt.Errorf("subtask %d: expected [max_score, testcases(, threshold)]", i+1)
		}
		if err := json.Unmarshal(a[0], &out[i].MaxScore); err != nil {
			return nil, fmt.Errorf("subtask %d: max score: %w", i+1, err)
		}
		out[i].Testcases = a[1]
		if len(a) == 3 {
			var th float64
			if err := json.Unmarshal(a[2], &th); err != nil {
				return nil, fmt.Errorf("subtask %d: threshold: %w", i+1, err)
			}
			out[i].Threshold = &th
		}
	}
	return out, nil
}

func newGroup(kind string, params json.RawMessage, defs []testcaseDef, precision int) (*group, error) {
	ps, err := parseGroupParams(params)
	if err != nil {
		return nil, err
	}
	if len(ps) == 0 {
		return nil, errors.New("at least one subtask is required")
	}
	g := &group{kind: kind, defs: defs, precision: precision}
	codes := make([]string, len(defs))
	for k, d := range defs {
		codes[k] = d.codename
	}
	next := 0 // for count-based subtasks: consecutive testcases in codename order
	for i, p := range ps {
		sd := groupDef{maxScore: p.MaxScore}
		if p.MaxScore < 0 {
			return nil, fmt.Errorf("subtask %d: negative max score", i+1)
		}
		if kind == "GroupThreshold" {
			if p.Threshold == nil {
				return nil, fmt.Errorf("subtask %d: GroupThreshold needs a threshold", i+1)
			}
			sd.threshold = *p.Threshold
		}
		sd.members, err = members(i, p.Testcases, codes, &next)
		if err != nil {
			return nil, err
		}
		if len(sd.members) == 0 {
			return nil, fmt.Errorf("subtask %d matches no testcase", i+1)
		}
		sd.public = true
		for _, k := range sd.members {
			sd.public = sd.public && defs[k].public
		}
		g.subtasks = append(g.subtasks, sd)
	}
	return g, nil
}

// members resolves the testcases of subtask i (0-based) among codenames
// (sorted): a count takes the next consecutive testcases (next is advanced),
// a string is an anchored regex, a list names them.
func members(i int, spec json.RawMessage, codenames []string, next *int) ([]int, error) {
	var n int
	var pattern string
	var list []string
	var out []int
	switch {
	case json.Unmarshal(spec, &n) == nil:
		if n <= 0 || *next+n > len(codenames) {
			return nil, fmt.Errorf("subtask %d: %d testcases requested, %d available", i+1, n, len(codenames)-*next)
		}
		for k := *next; k < *next+n; k++ {
			out = append(out, k)
		}
		*next += n
	case json.Unmarshal(spec, &pattern) == nil:
		re, err := regexp.Compile("^(?:" + pattern + ")$")
		if err != nil {
			return nil, fmt.Errorf("subtask %d: invalid regex: %w", i+1, err)
		}
		for k, c := range codenames {
			if re.MatchString(c) {
				out = append(out, k)
			}
		}
	case json.Unmarshal(spec, &list) == nil:
		pos := make(map[string]int, len(codenames))
		for k, c := range codenames {
			pos[c] = k
		}
		for _, c := range list {
			k, ok := pos[c]
			if !ok {
				return nil, fmt.Errorf("subtask %d: unknown testcase %q", i+1, c)
			}
			out = append(out, k)
		}
	default:
		return nil, fmt.Errorf("subtask %d: testcases must be a count, a regex or a list", i+1)
	}
	return out, nil
}

func (g *group) NumSubtasks() int { return len(g.subtasks) }

func (g *group) SubtaskMaxScores() []float64 {
	out := make([]float64, len(g.subtasks))
	for i, st := range g.subtasks {
		out[i] = round(st.maxScore, g.precision)
	}
	return out
}

func (g *group) MaxScore() float64 {
	var s float64
	for _, st := range g.subtasks {
		s += st.maxScore
	}
	return round(s, g.precision)
}

// MaxPublicScore: a subtask is public only when all its testcases are.
func (g *group) MaxPublicScore() float64 {
	var s float64
	for _, st := range g.subtasks {
		if st.public {
			s += st.maxScore
		}
	}
	return round(s, g.precision)
}

// fraction reduces the outcomes of a subtask.
func (g *group) fraction(st groupDef, outcomes []float64) float64 {
	switch g.kind {
	case "GroupMin":
		f := 1.0
		for _, o := range outcomes {
			f = math.Min(f, clamp01(o))
		}
		return f
	case "GroupMul":
		f := 1.0
		for _, o := range outcomes {
			f *= clamp01(o)
		}
		return f
	default: // GroupThreshold: the checker reports a value (e.g. an error);
		// a testcase passes when 0 < value <= threshold.
		for _, o := range outcomes {
			if !(o > 0 && o <= st.threshold) {
				return 0
			}
		}
		return 1
	}
}

func (g *group) Compute(tcs []Testcase) Result {
	byName := index(tcs)
	det := Details{Type: "group", MaxScore: g.MaxScore()}
	pdet := Details{Type: "group", MaxScore: g.MaxPublicScore()}
	var score, pub float64
	ranking := make([]float64, len(g.subtasks))
	for i, st := range g.subtasks {
		outcomes := make([]float64, 0, len(st.members))
		sd := SubtaskDetail{Index: i + 1, MaxScore: st.maxScore, Public: st.public}
		complete := true
		for _, k := range st.members {
			d := g.defs[k]
			t, ok := byName[d.codename]
			complete = complete && ok
			if ok {
				outcomes = append(outcomes, t.Outcome)
			} else {
				outcomes = append(outcomes, 0)
			}
			sd.Testcases = append(sd.Testcases, detail(d, t, ok))
		}
		f := 0.0
		if complete || g.kind != "GroupThreshold" {
			f = g.fraction(st, outcomes)
		}
		sd.Fraction = f
		sd.Score = round(st.maxScore*f, g.precision)
		ranking[i] = sd.Score
		score += sd.Score
		det.Subtasks = append(det.Subtasks, sd)
		if st.public {
			pub += sd.Score
			pdet.Subtasks = append(pdet.Subtasks, sd)
		}
	}
	return Result{Score: round(score, g.precision), Details: det, PublicScore: round(pub, g.precision),
		PublicDetails: pdet, RankingDetails: ranking}
}
