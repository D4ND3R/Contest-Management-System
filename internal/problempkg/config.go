// Package problempkg reads and writes problem packages: a zip with a
// problem.yaml, statements, testcases, managers, graders, attachments and
// reference solutions (docs/en/problem-package.md). It maps the package to
// the task and dataset settings of the CMS and back, so a task exported
// from one installation imports unchanged into another.
package problempkg

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"

	"github.com/D4ND3R/Contest-Management-System/internal/scoring"
	"gopkg.in/yaml.v3"
)

// Version is the package format written by Write and the newest accepted.
const Version = 1

// Config is problem.yaml. Times are in seconds, sizes in MiB (source size
// in KiB), as a problem setter writes them.
type Config struct {
	Format int    `yaml:"format,omitempty"`
	Name   string `yaml:"name"`
	Title  string `yaml:"title,omitempty"`
	// Type: batch, output_only, interactive, communication or two_steps.
	Type string `yaml:"type"`

	TimeLimit       float64 `yaml:"time_limit,omitempty"`
	WallTimeLimit   float64 `yaml:"wall_time_limit,omitempty"`
	MemoryLimit     float64 `yaml:"memory_limit,omitempty"`
	OutputLimit     float64 `yaml:"output_limit,omitempty"`
	ProcessLimit    int     `yaml:"process_limit,omitempty"`
	SourceSizeLimit float64 `yaml:"source_size_limit,omitempty"`

	// batch
	InputFile   string `yaml:"input_file,omitempty"`
	OutputFile  string `yaml:"output_file,omitempty"`
	Compilation string `yaml:"compilation,omitempty"` // batch: alone|grader; communication: stub|alone
	// batch, output_only, two_steps
	Checker            string     `yaml:"checker,omitempty"` // white_diff|exact|float|custom|testlib
	FloatTolerance     *Tolerance `yaml:"float_tolerance,omitempty"`
	CheckerTimeLimit   float64    `yaml:"checker_time_limit,omitempty"`
	CheckerMemoryLimit float64    `yaml:"checker_memory_limit,omitempty"`
	// output_only
	OutputPattern string `yaml:"output_pattern,omitempty"`
	MergePrevious bool   `yaml:"merge_previous,omitempty"`
	// two_steps
	Manager string `yaml:"manager,omitempty"`
	// communication
	Processes          int     `yaml:"processes,omitempty"`
	UserIO             string  `yaml:"user_io,omitempty"`
	LimitsMode         string  `yaml:"limits_mode,omitempty"`
	ManagerTimeLimit   float64 `yaml:"manager_time_limit,omitempty"`
	ManagerMemoryLimit float64 `yaml:"manager_memory_limit,omitempty"`
	// interactive
	InteractorTimeLimit   float64 `yaml:"interactor_time_limit,omitempty"`
	InteractorMemoryLimit float64 `yaml:"interactor_memory_limit,omitempty"`

	// Scoring: sum (points_per_test) or group_min, group_mul,
	// group_threshold (subtasks).
	Scoring        string    `yaml:"scoring,omitempty"`
	PointsPerTest  *float64  `yaml:"points_per_test,omitempty"`
	Subtasks       []Subtask `yaml:"subtasks,omitempty"`
	PublicTests    []string  `yaml:"public_tests,omitempty"` // regexes of public testcases
	ScoreMode      string    `yaml:"score_mode,omitempty"`   // max_subtask|max|max_tokened_last
	ScorePrecision int       `yaml:"score_precision,omitempty"`
	Feedback       string    `yaml:"feedback,omitempty"` // full|restricted

	Languages         []string `yaml:"languages,omitempty"`
	SubmissionFormat  []string `yaml:"submission_format,omitempty"`
	PrimaryStatements []string `yaml:"primary_statements,omitempty"`
	Dataset           string   `yaml:"dataset,omitempty"` // dataset description
}

// Tolerance of the float checker.
type Tolerance struct {
	Absolute float64 `yaml:"absolute,omitempty"`
	Relative float64 `yaml:"relative,omitempty"`
}

// Subtask of a group score type.
type Subtask struct {
	Points    float64  `yaml:"points"`
	Tests     Tests    `yaml:"tests"`
	Threshold *float64 `yaml:"threshold,omitempty"`
}

// Tests selects a subtask's testcases: a regex (string), a list of
// codenames or a count (the next testcases in codename order).
type Tests struct {
	Regex string
	List  []string
	Count int
}

func (t *Tests) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.SequenceNode:
		return n.Decode(&t.List)
	case yaml.ScalarNode:
		if n.Tag == "!!int" {
			return n.Decode(&t.Count)
		}
		return n.Decode(&t.Regex)
	}
	return errors.New("tests must be a regex, a list of testcases or a count")
}

func (t Tests) MarshalYAML() (any, error) {
	switch {
	case t.List != nil:
		return t.List, nil
	case t.Count > 0:
		return t.Count, nil
	}
	return t.Regex, nil
}

// TaskTypes maps package types to CMS task types.
var TaskTypes = map[string]string{"batch": "Batch", "output_only": "OutputOnly", "interactive": "Interactive",
	"communication": "Communication", "two_steps": "TwoSteps"}

// ScoreTypes maps package scorings to CMS score types.
var ScoreTypes = map[string]string{"sum": "Sum", "group_min": "GroupMin", "group_mul": "GroupMul", "group_threshold": "GroupThreshold"}

func inverse(m map[string]string, v string) string {
	for k, x := range m {
		if x == v {
			return k
		}
	}
	return ""
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// ParseConfig decodes problem.yaml strictly (unknown keys are errors) and
// fills defaults.
func ParseConfig(data []byte) (*Config, error) {
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, err
	}
	if c.Format > Version {
		return nil, fmt.Errorf("format %d is newer than this CMS understands (%d)", c.Format, Version)
	}
	if c.Title == "" {
		c.Title = c.Name
	}
	if c.Type == "" {
		c.Type = "batch"
	}
	if c.Scoring == "" {
		c.Scoring = "sum"
		if len(c.Subtasks) > 0 {
			c.Scoring = "group_min"
		}
	}
	if c.ProcessLimit == 0 {
		c.ProcessLimit = 1
	}
	if c.Feedback == "" {
		c.Feedback = "full"
	}
	if c.ScoreMode == "" {
		c.ScoreMode = "max_subtask"
	}
	if c.Dataset == "" {
		c.Dataset = "Default"
	}
	return &c, nil
}

// Check validates the values that do not depend on the package files.
func (c *Config) Check() []string {
	var errs []string
	bad := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }
	if !nameRe.MatchString(c.Name) {
		bad("name %q: use letters, digits, '_', '.' and '-'", c.Name)
	}
	if TaskTypes[c.Type] == "" {
		bad("type %q: use batch, output_only, interactive, communication or two_steps", c.Type)
	}
	if ScoreTypes[c.Scoring] == "" {
		bad("scoring %q: use sum, group_min, group_mul or group_threshold", c.Scoring)
	}
	if c.Type != "output_only" && c.TimeLimit <= 0 {
		bad("time_limit (seconds) is required")
	}
	if c.TimeLimit < 0 || c.TimeLimit > 3600 || c.WallTimeLimit < 0 || c.WallTimeLimit > 3600 {
		bad("time limits must be between 0 and 3600 seconds")
	}
	if c.Type != "output_only" && c.MemoryLimit <= 0 {
		bad("memory_limit (MiB) is required")
	}
	if c.MemoryLimit < 0 || c.MemoryLimit > 65536 || c.OutputLimit < 0 || c.SourceSizeLimit < 0 {
		bad("sizes must be positive (memory_limit at most 65536 MiB)")
	}
	if c.ProcessLimit < 1 || c.ProcessLimit > 256 {
		bad("process_limit must be between 1 and 256")
	}
	if c.ScorePrecision < 0 || c.ScorePrecision > 6 {
		bad("score_precision must be between 0 and 6")
	}
	switch c.ScoreMode {
	case "max_subtask", "max", "max_tokened_last":
	default:
		bad("score_mode %q: use max_subtask, max or max_tokened_last", c.ScoreMode)
	}
	switch c.Feedback {
	case "full", "restricted":
	default:
		bad("feedback %q: use full or restricted", c.Feedback)
	}
	if c.Scoring == "sum" && len(c.Subtasks) > 0 {
		bad("subtasks need a group scoring (group_min, group_mul or group_threshold)")
	}
	if c.Scoring != "sum" && len(c.Subtasks) == 0 && ScoreTypes[c.Scoring] != "" {
		bad("scoring %s needs subtasks", c.Scoring)
	}
	if c.PointsPerTest != nil && *c.PointsPerTest < 0 {
		bad("points_per_test must not be negative")
	}
	for i, st := range c.Subtasks {
		if st.Points < 0 {
			bad("subtask %d: points must not be negative", i+1)
		}
		if c.Scoring == "group_threshold" && st.Threshold == nil {
			bad("subtask %d: group_threshold needs a threshold", i+1)
		}
	}
	for _, re := range c.PublicTests {
		if _, err := regexp.Compile("^(?:" + re + ")$"); err != nil {
			bad("public_tests: invalid regex %q", re)
		}
	}
	for _, f := range c.SubmissionFormat {
		if !nameRe.MatchString(stripL(f)) {
			bad("submission_format: invalid file name %q", f)
		}
	}
	return errs
}

func stripL(f string) string {
	if len(f) > 3 && f[len(f)-3:] == ".%l" {
		return f[:len(f)-3] + ".x"
	}
	return f
}

// TaskType is the CMS task type.
func (c *Config) TaskType() string { return TaskTypes[c.Type] }

// ScoreType is the CMS score type.
func (c *Config) ScoreType() string { return ScoreTypes[c.Scoring] }

func ms(seconds float64) int64 { return int64(math.Round(seconds * 1000)) }
func mib(x float64) int64      { return int64(math.Round(x * (1 << 20))) }

// TaskTypeParams are the dataset's task_type_params.
func (c *Config) TaskTypeParams() json.RawMessage {
	m := map[string]any{}
	set := func(k string, v any, ok bool) {
		if ok {
			m[k] = v
		}
	}
	checker := func() {
		set("checker", c.Checker, c.Checker != "")
		if t := c.FloatTolerance; t != nil {
			set("float_abs_tol", t.Absolute, t.Absolute != 0)
			set("float_rel_tol", t.Relative, t.Relative != 0)
		}
		set("checker_time_limit_ms", ms(c.CheckerTimeLimit), c.CheckerTimeLimit > 0)
		set("checker_memory_bytes", mib(c.CheckerMemoryLimit), c.CheckerMemoryLimit > 0)
	}
	switch c.Type {
	case "batch":
		set("compilation", c.Compilation, c.Compilation != "")
		set("input_file", c.InputFile, c.InputFile != "")
		set("output_file", c.OutputFile, c.OutputFile != "")
		checker()
	case "output_only":
		set("output_pattern", c.OutputPattern, c.OutputPattern != "")
		set("merge_previous", true, c.MergePrevious)
		checker()
	case "two_steps":
		set("manager", c.Manager, c.Manager != "")
		checker()
	case "communication":
		set("num_processes", c.Processes, c.Processes > 0)
		set("compilation", c.Compilation, c.Compilation != "")
		set("user_io", c.UserIO, c.UserIO != "")
		set("limits_mode", c.LimitsMode, c.LimitsMode != "")
		set("manager_time_limit_ms", ms(c.ManagerTimeLimit), c.ManagerTimeLimit > 0)
		set("manager_memory_bytes", mib(c.ManagerMemoryLimit), c.ManagerMemoryLimit > 0)
	case "interactive":
		set("interactor_time_limit_ms", ms(c.InteractorTimeLimit), c.InteractorTimeLimit > 0)
		set("interactor_memory_bytes", mib(c.InteractorMemoryLimit), c.InteractorMemoryLimit > 0)
	}
	b, _ := json.Marshal(m)
	return b
}

// Specs are the subtasks as score editor specs.
func (c *Config) Specs() []scoring.SubtaskSpec {
	out := make([]scoring.SubtaskSpec, len(c.Subtasks))
	for i, st := range c.Subtasks {
		sp := scoring.SubtaskSpec{MaxScore: st.Points, Threshold: st.Threshold}
		switch {
		case st.Tests.List != nil:
			sp.Mode, sp.List = "list", st.Tests.List
		case st.Tests.Count > 0:
			sp.Mode, sp.Count = "count", st.Tests.Count
		default:
			sp.Mode, sp.Regex = "regex", st.Tests.Regex
		}
		out[i] = sp
	}
	return out
}

// ScoreTypeParams are the dataset's score_type_params for nTests
// testcases: Sum defaults to 100 points split evenly.
func (c *Config) ScoreTypeParams(nTests int) json.RawMessage {
	if c.Scoring == "sum" {
		p := 1.0
		switch {
		case c.PointsPerTest != nil:
			p = *c.PointsPerTest
		case nTests > 0:
			p = 100 / float64(nTests)
		}
		return json.RawMessage(strconv.FormatFloat(p, 'f', -1, 64))
	}
	return scoring.EncodeSubtasks(c.Specs(), c.Scoring == "group_threshold")
}
