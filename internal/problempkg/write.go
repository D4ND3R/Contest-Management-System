package problempkg

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/scoring"
	"gopkg.in/yaml.v3"
)

// Writer writes a package zip.
type Writer struct {
	zw  *zip.Writer
	now time.Time
}

// NewWriter starts a package on w.
func NewWriter(w io.Writer) *Writer { return &Writer{zw: zip.NewWriter(w), now: time.Now()} }

const header = "# Problem package (format 1): see docs/en/problem-package.md\n"

// WriteConfig writes problem.yaml.
func (w *Writer) WriteConfig(c *Config) error {
	c.Format = Version
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return w.Add("problem.yaml", strings.NewReader(header+string(b)))
}

// Add writes one file.
func (w *Writer) Add(name string, r io.Reader) error {
	f, err := w.zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: w.now})
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	return err
}

// Close finishes the zip.
func (w *Writer) Close() error { return w.zw.Close() }

// typeParams is the union of the task types' parameters (JSON names as in
// internal/tasktypes).
type typeParams struct {
	Compilation           string  `json:"compilation"`
	InputFile             string  `json:"input_file"`
	OutputFile            string  `json:"output_file"`
	Checker               string  `json:"checker"`
	FloatAbsTol           float64 `json:"float_abs_tol"`
	FloatRelTol           float64 `json:"float_rel_tol"`
	CheckerTimeLimitMs    int64   `json:"checker_time_limit_ms"`
	CheckerMemoryBytes    int64   `json:"checker_memory_bytes"`
	OutputPattern         string  `json:"output_pattern"`
	MergePrevious         bool    `json:"merge_previous"`
	Manager               string  `json:"manager"`
	NumProcesses          int     `json:"num_processes"`
	UserIO                string  `json:"user_io"`
	LimitsMode            string  `json:"limits_mode"`
	ManagerTimeLimitMs    int64   `json:"manager_time_limit_ms"`
	ManagerMemoryBytes    int64   `json:"manager_memory_bytes"`
	InteractorTimeLimitMs int64   `json:"interactor_time_limit_ms"`
	InteractorMemoryBytes int64   `json:"interactor_memory_bytes"`
}

func secs(ms int64) float64     { return float64(ms) / 1000 }
func mibOf(bytes int64) float64 { return float64(bytes) / (1 << 20) }

// ConfigFromCMS describes a task and one of its datasets as problem.yaml;
// tests are the dataset's testcases (codename → public).
func ConfigFromCMS(t sqlc.Task, d sqlc.Dataset, codes []string, public []bool) (*Config, error) {
	c := &Config{Format: Version, Name: t.Name, Title: t.Title, Type: inverse(TaskTypes, d.TaskType),
		Scoring: inverse(ScoreTypes, d.ScoreType), ProcessLimit: int(d.ProcessLimit), ScoreMode: t.ScoreMode,
		ScorePrecision: ptrInt(int(t.ScorePrecision)), Feedback: t.FeedbackLevel, Languages: t.Languages,
		SubmissionFormat: t.SubmissionFormat, PrimaryStatements: t.PrimaryStatements, Dataset: d.Description}
	if c.Type == "" || c.Scoring == "" {
		return nil, fmt.Errorf("task type %s / score type %s cannot be exported", d.TaskType, d.ScoreType)
	}
	if d.TimeLimitMs != nil {
		c.TimeLimit = secs(int64(*d.TimeLimitMs))
	}
	if d.WallTimeLimitMs != nil {
		c.WallTimeLimit = secs(int64(*d.WallTimeLimitMs))
	}
	if d.MemoryLimitBytes != nil {
		c.MemoryLimit = mibOf(*d.MemoryLimitBytes)
	}
	if d.OutputLimitBytes != 64<<20 {
		c.OutputLimit = mibOf(d.OutputLimitBytes)
	}
	if d.SourceSizeLimitBytes != nil {
		c.SourceSizeLimit = float64(*d.SourceSizeLimitBytes) / 1024
	}
	var tp typeParams
	if err := json.Unmarshal(d.TaskTypeParams, &tp); err != nil {
		return nil, fmt.Errorf("task type parameters: %w", err)
	}
	c.Compilation, c.InputFile, c.OutputFile, c.Checker = tp.Compilation, tp.InputFile, tp.OutputFile, tp.Checker
	if tp.FloatAbsTol != 0 || tp.FloatRelTol != 0 {
		c.FloatTolerance = &Tolerance{Absolute: tp.FloatAbsTol, Relative: tp.FloatRelTol}
	}
	c.CheckerTimeLimit, c.CheckerMemoryLimit = secs(tp.CheckerTimeLimitMs), mibOf(tp.CheckerMemoryBytes)
	c.OutputPattern, c.MergePrevious, c.Manager = tp.OutputPattern, tp.MergePrevious, tp.Manager
	c.Processes, c.UserIO, c.LimitsMode = tp.NumProcesses, tp.UserIO, tp.LimitsMode
	c.ManagerTimeLimit, c.ManagerMemoryLimit = secs(tp.ManagerTimeLimitMs), mibOf(tp.ManagerMemoryBytes)
	c.InteractorTimeLimit, c.InteractorMemoryLimit = secs(tp.InteractorTimeLimitMs), mibOf(tp.InteractorMemoryBytes)

	if c.Scoring == "sum" {
		n := 1.0
		if s := string(d.ScoreTypeParams); s != "{}" && s != "null" && s != "" {
			if json.Unmarshal(d.ScoreTypeParams, &n) != nil {
				var obj struct {
					P *float64 `json:"points_per_testcase"`
				}
				if json.Unmarshal(d.ScoreTypeParams, &obj) != nil || obj.P == nil {
					return nil, fmt.Errorf("invalid Sum parameters %s", s)
				}
				n = *obj.P
			}
		}
		c.PointsPerTest = &n
	} else {
		specs, err := scoring.ParseSubtasks(d.ScoreTypeParams)
		if err != nil {
			return nil, err
		}
		for _, sp := range specs {
			st := Subtask{Points: sp.MaxScore, Threshold: sp.Threshold}
			switch sp.Mode {
			case "list":
				st.Tests.List = sp.List
			case "count":
				st.Tests.Count = sp.Count
			default:
				st.Tests.Regex = sp.Regex
			}
			c.Subtasks = append(c.Subtasks, st)
		}
	}
	all, any := true, false
	var pub []string
	for i, code := range codes {
		all, any = all && public[i], any || public[i]
		if public[i] {
			pub = append(pub, regexp.QuoteMeta(code))
		}
	}
	switch {
	case all && any:
		c.PublicTests = []string{".*"}
	case any:
		c.PublicTests = pub
	}
	return c, nil
}

func ptrInt(v int) *int { return &v }
