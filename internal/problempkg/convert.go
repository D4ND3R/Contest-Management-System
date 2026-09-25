package problempkg

import (
	"encoding/xml"
	"fmt"
	"math"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Packages in other formats are converted to this system's layout before
// reading: their files keep pointing at the original zip members under new
// paths (nothing is copied) and a problem.yaml is generated. Supported:
// the CMS italy_yaml format (task.yaml) and Polygon packages (problem.xml).

type conversion struct {
	format           string
	entries          []entry
	warnings, errors []Issue
}

func (c *conversion) warn(path, format string, args ...any) {
	c.warnings = append(c.warnings, Issue{path, fmt.Sprintf(format, args...)})
}

func (c *conversion) fail(path, format string, args ...any) {
	c.errors = append(c.errors, Issue{path, fmt.Sprintf(format, args...)})
}

// add places a file of the original package at a path of the converted one.
func (c *conversion) add(name string, e entry) {
	c.entries = append(c.entries, entry{name: name, size: e.size, src: e.src})
}

// config generates the problem.yaml of the converted package.
func (c *conversion) config(cfg *Config) {
	cfg.Format = Version
	data, err := yaml.Marshal(cfg)
	if err != nil {
		c.fail("problem.yaml", "%v", err)
		return
	}
	c.entries = append(c.entries, entry{name: "problem.yaml", size: int64(len(data)), src: source{data: data}})
}

// convert detects the format of a package and converts it (format "" when
// it is already in this system's format or unknown).
func convert(entries []entry) conversion {
	switch {
	case hasRootFile(entries, "problem.yaml"):
		return conversion{}
	case hasRootFile(entries, "task.yaml"):
		return fromItaly(entries)
	case hasRootFile(entries, "problem.xml"):
		return fromPolygon(entries)
	}
	return conversion{}
}

// relative indexes the entries by their path under the folder holding
// marker.
func relative(entries []entry, marker string) map[string]entry {
	prefix := rootOf(entries, marker)
	out := make(map[string]entry, len(entries))
	for _, e := range entries {
		if strings.HasPrefix(e.name, prefix) {
			out[strings.TrimPrefix(e.name, prefix)] = e
		}
	}
	return out
}

func readEntry(e entry, limit int64) ([]byte, error) { return readAll(e.src, limit) }

// sourceExts are the extensions of solution and grader sources.
var sourceExts = map[string]bool{".c": true, ".cpp": true, ".cc": true, ".cxx": true, ".java": true, ".py": true,
	".pas": true, ".pp": true, ".go": true, ".rs": true, ".kt": true, ".cs": true, ".hs": true, ".js": true}

// safeName makes a file name acceptable to the package format.
func safeName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == '-' {
			return r
		}
		return '_'
	}, s)
	if s == "" {
		return "_"
	}
	return s
}

// ---------------------------------------------------------------- italy_yaml

type italyTask struct {
	Name            string    `yaml:"name"`
	Title           string    `yaml:"title"`
	TimeLimit       float64   `yaml:"time_limit"`
	MemoryLimit     float64   `yaml:"memory_limit"`
	NInput          int       `yaml:"n_input"`
	Infile          *string   `yaml:"infile"`
	Outfile         *string   `yaml:"outfile"`
	PublicTestcases yaml.Node `yaml:"public_testcases"`
	ScoreType       string    `yaml:"score_type"`
	ScoreParams     yaml.Node `yaml:"score_type_parameters"`
	TotalValue      *float64  `yaml:"total_value"`
	OutputOnly      bool      `yaml:"output_only"`
	PrimaryLanguage string    `yaml:"primary_language"`
	NumProcesses    int       `yaml:"num_processes"`
}

var stRe = regexp.MustCompile(`^#\s*ST\s*:\s*([0-9.]+)`)

func fromItaly(entries []entry) conversion {
	c := conversion{format: "italy_yaml"}
	files := relative(entries, "task.yaml")
	data, err := readEntry(files["task.yaml"], 1<<20)
	if err != nil {
		c.fail("task.yaml", "%v", err)
		return c
	}
	var t italyTask
	if err := yaml.Unmarshal(data, &t); err != nil {
		c.fail("task.yaml", "%v", err)
		return c
	}
	cfg := &Config{Name: t.Name, Title: t.Title, Type: "batch", TimeLimit: t.TimeLimit, MemoryLimit: t.MemoryLimit,
		ProcessLimit: 1, Feedback: "full", Scoring: "sum"}
	if cfg.Name == "" {
		c.fail("task.yaml", "name is required")
	}
	// Files named input.txt/output.txt by default; "" means stdin/stdout.
	cfg.InputFile, cfg.OutputFile = "input.txt", "output.txt"
	if t.Infile != nil {
		cfg.InputFile = *t.Infile
	}
	if t.Outfile != nil {
		cfg.OutputFile = *t.Outfile
	}
	if t.OutputOnly {
		cfg.Type, cfg.InputFile, cfg.OutputFile = "output_only", "", ""
	}

	// Testcases: input/inputN.txt and output/outputN.txt from 0.
	n := t.NInput
	if n == 0 {
		for {
			if _, ok := files[fmt.Sprintf("input/input%d.txt", n)]; !ok {
				break
			}
			n++
		}
	}
	width := max(3, len(strconv.Itoa(n-1)))
	code := func(i int) string { return fmt.Sprintf("%0*d", width, i) }
	for i := 0; i < n; i++ {
		in, okIn := files[fmt.Sprintf("input/input%d.txt", i)]
		out, okOut := files[fmt.Sprintf("output/output%d.txt", i)]
		if !okIn || !okOut {
			c.fail(fmt.Sprintf("input/input%d.txt", i), "testcase %d is missing its input or output file (n_input is %d)", i, n)
			continue
		}
		c.add("tests/"+code(i)+".in", in)
		c.add("tests/"+code(i)+".out", out)
	}
	cfg.PublicTests = italyPublic(t.PublicTestcases, n, code)

	// Scoring: subtasks from gen/GEN ("# ST: points" headers, one testcase
	// per other line) or explicit parameters; otherwise total_value split.
	total := 100.0
	if t.TotalValue != nil {
		total = *t.TotalValue
	}
	subtasks := italySubtasks(&c, files, t)
	switch {
	case len(subtasks) > 0:
		cfg.Scoring, cfg.Subtasks = "group_min", subtasks
		if strings.EqualFold(t.ScoreType, "GroupMul") {
			cfg.Scoring = "group_mul"
		}
	case n > 0:
		p := total / float64(n)
		cfg.PointsPerTest = &p
	}

	// Checker (check/checker, cor/correttore), Communication manager,
	// graders and stubs (sol/grader.*, sol/stub.*, headers).
	for _, cand := range []string{"check/checker", "cor/correttore"} {
		for _, ext := range []string{"", ".cpp", ".cc", ".c"} {
			if e, ok := files[cand+ext]; ok && cfg.Checker == "" {
				c.add("checker"+ext, e)
				cfg.Checker = "custom"
			}
		}
	}
	for _, ext := range []string{"", ".cpp", ".cc", ".c"} {
		if e, ok := files["check/manager"+ext]; ok {
			c.add("manager"+ext, e)
			cfg.Type, cfg.Processes, cfg.InputFile, cfg.OutputFile, cfg.Checker = "communication", max(t.NumProcesses, 1), "", "", ""
		}
	}
	var sols []string
	for rel := range files {
		if strings.HasPrefix(rel, "sol/") && !strings.Contains(strings.TrimPrefix(rel, "sol/"), "/") {
			sols = append(sols, rel)
		}
	}
	sort.Strings(sols)
	for _, rel := range sols {
		base := path.Base(rel)
		stem, ext := strings.TrimSuffix(base, path.Ext(base)), strings.ToLower(path.Ext(base))
		switch {
		case ext == ".h" || ext == ".hpp":
			c.add("graders/"+safeName(base), files[rel])
		case stem == "grader" && sourceExts[ext]:
			c.add("graders/"+safeName(base), files[rel])
			if cfg.Type == "batch" {
				cfg.Compilation = "grader"
			}
		case stem == "stub" && sourceExts[ext]:
			c.add("graders/"+safeName(base), files[rel])
			if cfg.Type == "communication" {
				cfg.Compilation = "stub"
			}
		case sourceExts[ext]:
			verdict := "any"
			if stem == "soluzione" || stem == "solution" || stem == "sol" {
				verdict = "ac"
			}
			c.add("solutions/"+verdict+"_"+safeName(base), files[rel])
		}
	}
	// Statement and attachments.
	lang := t.PrimaryLanguage
	if !langRe.MatchString(lang) {
		lang = "it"
	}
	for _, s := range []string{"statement/statement.pdf", "testo/testo.pdf"} {
		if e, ok := files[s]; ok {
			c.add("statement/"+lang+".pdf", e)
			cfg.PrimaryStatements = []string{lang}
			break
		}
	}
	for rel, e := range files {
		if strings.HasPrefix(rel, "att/") && !strings.HasSuffix(rel, "/") {
			c.add("attachments/"+safeName(path.Base(rel)), e)
		}
	}
	c.warn("task.yaml", "converted from the CMS italy_yaml format: check the settings before importing")
	c.config(cfg)
	return c
}

// italyPublic reads public_testcases: "all" or a comma list of indexes.
func italyPublic(n yaml.Node, count int, code func(int) string) []string {
	v := strings.TrimSpace(n.Value)
	if v == "" {
		return nil
	}
	if strings.EqualFold(v, "all") {
		return []string{".*"}
	}
	var out []string
	for _, part := range strings.Split(v, ",") {
		if i, err := strconv.Atoi(strings.TrimSpace(part)); err == nil && i >= 0 && i < count {
			out = append(out, regexp.QuoteMeta(code(i)))
		}
	}
	return out
}

// italySubtasks reads the subtasks from gen/GEN or score_type_parameters
// ([[points, count], ...]).
func italySubtasks(c *conversion, files map[string]entry, t italyTask) []Subtask {
	var out []Subtask
	if e, ok := files["gen/GEN"]; ok {
		data, err := readEntry(e, 1<<20)
		if err != nil {
			c.fail("gen/GEN", "%v", err)
			return nil
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if m := stRe.FindStringSubmatch(line); m != nil {
				pts, _ := strconv.ParseFloat(m[1], 64)
				out = append(out, Subtask{Points: pts})
				continue
			}
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if len(out) > 0 {
				out[len(out)-1].Tests.Count++
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if t.ScoreParams.Kind == yaml.SequenceNode {
		for _, item := range t.ScoreParams.Content {
			if item.Kind != yaml.SequenceNode || len(item.Content) < 2 {
				continue
			}
			pts, err1 := strconv.ParseFloat(item.Content[0].Value, 64)
			cnt, err2 := strconv.Atoi(item.Content[1].Value)
			if err1 != nil || err2 != nil {
				c.warn("task.yaml", "score_type_parameters: only [points, number of testcases] pairs are converted")
				return nil
			}
			out = append(out, Subtask{Points: pts, Tests: Tests{Count: cnt}})
		}
	}
	return out
}

// ---------------------------------------------------------------- Polygon

type polyFile struct {
	Path string `xml:"path,attr"`
	Type string `xml:"type,attr"`
}

type polyTestset struct {
	Name          string `xml:"name,attr"`
	TimeLimit     int64  `xml:"time-limit"`
	MemoryLimit   int64  `xml:"memory-limit"`
	TestCount     int    `xml:"test-count"`
	InputPattern  string `xml:"input-path-pattern"`
	AnswerPattern string `xml:"answer-path-pattern"`
	Tests         []struct {
		Points *float64 `xml:"points,attr"`
		Group  string   `xml:"group,attr"`
		Sample bool     `xml:"sample,attr"`
	} `xml:"tests>test"`
	Groups []struct {
		Name   string   `xml:"name,attr"`
		Points *float64 `xml:"points,attr"`
		Policy string   `xml:"points-policy,attr"`
	} `xml:"groups>group"`
}

type polyProblem struct {
	ShortName string `xml:"short-name,attr"`
	Names     []struct {
		Language string `xml:"language,attr"`
		Value    string `xml:"value,attr"`
	} `xml:"names>name"`
	Statements []struct {
		Language string `xml:"language,attr"`
		Path     string `xml:"path,attr"`
		Type     string `xml:"type,attr"`
	} `xml:"statements>statement"`
	Judging struct {
		InputFile  string        `xml:"input-file,attr"`
		OutputFile string        `xml:"output-file,attr"`
		Testsets   []polyTestset `xml:"testset"`
	} `xml:"judging"`
	Resources []polyFile `xml:"files>resources>file"`
	Checker   struct {
		Name   string   `xml:"name,attr"`
		Source polyFile `xml:"source"`
	} `xml:"assets>checker"`
	Interactor *struct {
		Source polyFile `xml:"source"`
	} `xml:"assets>interactor"`
	Solutions []struct {
		Tag    string   `xml:"tag,attr"`
		Source polyFile `xml:"source"`
	} `xml:"assets>solutions>solution"`
}

// polyLanguages maps Polygon statement languages to codes.
var polyLanguages = map[string]string{"english": "en", "russian": "ru", "spanish": "es", "portuguese": "pt", "french": "fr",
	"italian": "it", "german": "de", "chinese": "zh", "ukrainian": "uk", "polish": "pl", "japanese": "ja", "korean": "ko",
	"vietnamese": "vi", "romanian": "ro", "turkish": "tr", "arabic": "ar", "hungarian": "hu", "czech": "cs"}

// polyVerdicts maps solution tags to expected verdicts.
var polyVerdicts = map[string]string{"main": "ac", "accepted": "ac", "wrong-answer": "wa", "presentation-error": "wa",
	"time-limit-exceeded": "tle", "memory-limit-exceeded": "mle", "rejected": "any", "failed": "any",
	"time-limit-exceeded-or-accepted": "any", "time-limit-exceeded-or-memory-limit-exceeded": "any"}

func fromPolygon(entries []entry) conversion {
	c := conversion{format: "polygon"}
	files := relative(entries, "problem.xml")
	data, err := readEntry(files["problem.xml"], 8<<20)
	if err != nil {
		c.fail("problem.xml", "%v", err)
		return c
	}
	var p polyProblem
	if err := xml.Unmarshal(data, &p); err != nil {
		c.fail("problem.xml", "%v", err)
		return c
	}
	var ts *polyTestset
	for i := range p.Judging.Testsets {
		if p.Judging.Testsets[i].Name == "tests" || ts == nil {
			ts = &p.Judging.Testsets[i]
		}
	}
	if ts == nil {
		c.fail("problem.xml", "no testset")
		return c
	}
	cfg := &Config{Name: safeName(p.ShortName), Type: "batch", ProcessLimit: 1, Feedback: "full", Scoring: "sum",
		TimeLimit: float64(ts.TimeLimit) / 1000, MemoryLimit: math.Round(float64(ts.MemoryLimit)/(1<<20)*100) / 100,
		InputFile: p.Judging.InputFile, OutputFile: p.Judging.OutputFile}
	if len(p.Names) > 0 {
		cfg.Title = p.Names[0].Value
	}

	// Testcases (the full package carries them; the plain one must be
	// generated first with doall.sh).
	n := max(ts.TestCount, len(ts.Tests))
	width := max(2, len(strconv.Itoa(n)))
	code := func(i int) string { return fmt.Sprintf("%0*d", width, i+1) }
	missing := 0
	for i := 0; i < n; i++ {
		in, okIn := files[polyPath(ts.InputPattern, i+1)]
		out, okOut := files[polyPath(ts.AnswerPattern, i+1)]
		if !okIn || !okOut {
			missing++
			continue
		}
		c.add("tests/"+code(i)+".in", in)
		c.add("tests/"+code(i)+".out", out)
	}
	if missing > 0 {
		c.fail("tests/", "%d of %d testcases are not in the package: download the full package from Polygon (or run doall.sh) so the generated tests are included", missing, n)
	}
	polyScoring(cfg, ts, code)

	// Checker and interactor (their testlib sources), headers.
	for _, r := range p.Resources {
		if strings.HasSuffix(r.Path, ".h") {
			if e, ok := files[r.Path]; ok {
				c.add(safeName(path.Base(r.Path)), e)
			}
		}
	}
	if src := p.Checker.Source.Path; src != "" {
		if e, ok := files[src]; ok {
			c.add("checker"+strings.ToLower(path.Ext(src)), e)
			cfg.Checker = "testlib"
		} else {
			c.fail(src, "the checker source is not in the package")
		}
	}
	if p.Interactor != nil && p.Interactor.Source.Path != "" {
		if e, ok := files[p.Interactor.Source.Path]; ok {
			c.add("interactor"+strings.ToLower(path.Ext(p.Interactor.Source.Path)), e)
			cfg.Type, cfg.InputFile, cfg.OutputFile, cfg.Checker = "interactive", "", "", ""
		} else {
			c.fail(p.Interactor.Source.Path, "the interactor source is not in the package")
		}
	}
	seen := map[string]bool{}
	for _, s := range p.Solutions {
		e, ok := files[s.Source.Path]
		if !ok {
			c.warn(s.Source.Path, "solution not in the package; skipped")
			continue
		}
		v := polyVerdicts[s.Tag]
		if v == "" {
			v = "any"
		}
		name := v + "_" + safeName(path.Base(s.Source.Path))
		for seen[name] {
			name = v + "_" + "x" + strings.TrimPrefix(name, v+"_")
		}
		seen[name] = true
		c.add("solutions/"+name, e)
	}
	// Statements: the PDF of each language, else its HTML (without images).
	byLang := map[string]string{}
	for _, st := range p.Statements {
		lang := polyLanguages[strings.ToLower(st.Language)]
		if lang == "" {
			continue
		}
		if _, ok := files[st.Path]; !ok {
			continue
		}
		if prev := byLang[lang]; prev == "" || (strings.HasSuffix(st.Path, ".pdf") && !strings.HasSuffix(prev, ".pdf")) {
			byLang[lang] = st.Path
		}
	}
	for lang, sp := range byLang {
		ext := strings.ToLower(path.Ext(sp))
		c.add("statement/"+lang+ext, files[sp])
		if ext != ".pdf" {
			c.warn(sp, "HTML statement imported without its images; export PDF statements from Polygon for the full layout")
		}
	}
	c.warn("problem.xml", "converted from a Polygon package: check the settings before importing")
	c.config(cfg)
	return c
}

// polyPath expands a Polygon path pattern ("tests/%02d") for test i.
func polyPath(pattern string, i int) string {
	if pattern == "" {
		pattern = "tests/%02d"
	}
	return fmt.Sprintf(pattern, i)
}

// polyScoring maps test points and groups to the package scoring.
func polyScoring(cfg *Config, ts *polyTestset, code func(int) string) {
	var hasPoints, hasGroups bool
	for i, t := range ts.Tests {
		hasPoints = hasPoints || t.Points != nil
		hasGroups = hasGroups || t.Group != ""
		if t.Sample {
			cfg.PublicTests = append(cfg.PublicTests, regexp.QuoteMeta(code(i)))
		}
	}
	pts := func(i int) float64 {
		if p := ts.Tests[i].Points; p != nil {
			return *p
		}
		return 0
	}
	switch {
	case hasGroups:
		type group struct {
			codes  []string
			points float64
			each   bool
		}
		var order []string
		groups := map[string]*group{}
		for i, t := range ts.Tests {
			g := groups[t.Group]
			if g == nil {
				g = &group{}
				groups[t.Group] = g
				order = append(order, t.Group)
			}
			g.codes = append(g.codes, code(i))
			g.points += pts(i)
		}
		for _, gd := range ts.Groups {
			if g := groups[gd.Name]; g != nil {
				if gd.Points != nil {
					g.points = *gd.Points
				}
				g.each = gd.Policy == "each-test"
			}
		}
		cfg.Scoring = "group_min"
		for _, name := range order {
			g := groups[name]
			if g.each {
				for _, cd := range g.codes {
					i, _ := strconv.Atoi(cd)
					cfg.Subtasks = append(cfg.Subtasks, Subtask{Points: pts(i - 1), Tests: Tests{List: []string{cd}}})
				}
				continue
			}
			cfg.Subtasks = append(cfg.Subtasks, Subtask{Points: g.points, Tests: Tests{List: g.codes}})
		}
	case hasPoints:
		first, same := pts(0), true
		for i := range ts.Tests {
			same = same && pts(i) == first
		}
		if same {
			cfg.PointsPerTest = &first
			return
		}
		cfg.Scoring = "group_min"
		for i := range ts.Tests {
			cfg.Subtasks = append(cfg.Subtasks, Subtask{Points: pts(i), Tests: Tests{List: []string{code(i)}}})
		}
	}
}
