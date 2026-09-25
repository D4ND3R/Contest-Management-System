package problempkg

import (
	"archive/zip"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/scoring"
	"github.com/D4ND3R/Contest-Management-System/internal/tasktypes"
)

// File is a file of the package, read lazily from the zip.
type File struct {
	Name string // name in its role: statement language, manager or attachment file name
	Path string // path in the package
	Size int64
	zf   *zip.File
}

// Open returns the file contents.
func (f File) Open() (io.ReadCloser, error) { return f.zf.Open() }

// Statement is a statement file in one language.
type Statement struct {
	File
	Language    string
	ContentType string
}

// Test is a testcase.
type Test struct {
	Codename      string
	Input, Output File
	Public        bool
}

// Solution is a reference solution with the verdict its name announces.
type Solution struct {
	Name     string // e.g. ac_main.cpp, or the directory of an output-only solution
	Expected string // ac, wa, tle, mle, re, ce, pa or any
	Language string // resolved from the extension ("" for output-only)
	Files    []File // one source, or the outputs of an output-only solution
}

// Issue is a problem found in the package, tied to a file when possible.
type Issue struct {
	Path    string
	Message string
}

func (i Issue) String() string {
	if i.Path == "" {
		return i.Message
	}
	return i.Path + ": " + i.Message
}

// Package is a read package with everything it contains and every problem
// found. Errors block the import; warnings do not.
type Package struct {
	Config      *Config
	Statements  []Statement
	Tests       []Test
	Managers    []File
	Attachments []File
	Solutions   []Solution
	Errors      []Issue
	Warnings    []Issue
	// MaxScore and SubtaskMatches preview the scoring on the tests.
	MaxScore float64
	Coverage scoring.Coverage
}

// OK reports whether the package can be imported.
func (p *Package) OK() bool { return len(p.Errors) == 0 }

func (p *Package) fail(path, format string, args ...any) {
	p.Errors = append(p.Errors, Issue{path, fmt.Sprintf(format, args...)})
}

func (p *Package) warn(path, format string, args ...any) {
	p.Warnings = append(p.Warnings, Issue{path, fmt.Sprintf(format, args...)})
}

// Options tune Read.
type Options struct {
	// MaxBytes bounds the uncompressed size (zip bombs); 0 = 4 GiB.
	MaxBytes int64
	// LanguagesByExt maps a source extension (".cpp") to the configured
	// languages using it, in preference order.
	LanguagesByExt map[string][]string
	// KnownLanguage reports whether a language id is configured.
	KnownLanguage func(string) bool
}

// Verdicts are the expected verdicts a solution name may start with.
var Verdicts = map[string]string{
	"ac": "accepted (full score)", "wa": "wrong answer on some testcase", "tle": "time limit exceeded on some testcase",
	"mle": "memory limit exceeded on some testcase", "re": "runtime error on some testcase", "ce": "compilation error",
	"pa": "partial score (more than 0, less than the maximum)", "any": "not checked",
}

var (
	langRe     = regexp.MustCompile(`^[a-z]{2,3}([_-][A-Za-z0-9]{2,8})?$`)
	codenameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
)

var statementTypes = map[string]string{".pdf": "application/pdf", ".html": "text/html; charset=utf-8",
	".htm": "text/html; charset=utf-8", ".md": "text/markdown; charset=utf-8", ".txt": "text/plain; charset=utf-8"}

// managerRoots are the executables a package may carry at its root (binary
// or a .c/.cpp source); headers such as testlib.h are managers too.
var managerRoots = map[string]bool{"checker": true, "interactor": true, "manager": true}

// Read parses a package. It never fails: problems are reported in the
// returned package's Errors and Warnings.
func Read(zr *zip.Reader, o Options) *Package {
	p := &Package{}
	max := o.MaxBytes
	if max <= 0 {
		max = 4 << 30
	}
	var total uint64
	for _, zf := range zr.File {
		total += zf.UncompressedSize64
	}
	if total > uint64(max) {
		p.fail("", "the package expands to more than %d bytes", max)
		return p
	}
	prefix := rootPrefix(zr)
	var config *zip.File
	inputs, outputs := map[string]File{}, map[string]File{}
	solutionDirs := map[string]*Solution{}
	seenManager, seenAttachment, seenStatement := map[string]string{}, map[string]string{}, map[string]string{}
	for _, zf := range zr.File {
		name := zf.Name
		if zf.FileInfo().IsDir() || ignored(name) {
			continue
		}
		if !strings.HasPrefix(name, prefix) {
			p.warn(name, "outside the package folder; ignored")
			continue
		}
		rel := strings.TrimPrefix(name, prefix)
		if strings.Contains(rel, "\\") || strings.HasPrefix(rel, "/") || strings.Contains("/"+rel+"/", "/../") {
			p.fail(name, "unsafe path")
			continue
		}
		f := File{Name: path.Base(rel), Path: rel, Size: int64(zf.UncompressedSize64), zf: zf}
		dir, _, _ := strings.Cut(rel, "/")
		if !strings.Contains(rel, "/") {
			dir = ""
		}
		ext := strings.ToLower(path.Ext(f.Name))
		stem := strings.TrimSuffix(f.Name, path.Ext(f.Name))
		switch dir {
		case "":
			switch {
			case rel == "problem.yaml":
				config = zf
			case managerRoots[stem] && (ext == "" || ext == ".c" || ext == ".cpp" || ext == ".cc"),
				ext == ".h" || ext == ".hpp":
				addUnique(p, seenManager, f, &p.Managers)
			default:
				p.warn(rel, "not part of the format; ignored")
			}
		case "statement", "statements":
			ct := statementTypes[ext]
			switch {
			case ct == "":
				p.fail(rel, "statements must be PDF, HTML, Markdown or text")
			case !langRe.MatchString(stem):
				p.fail(rel, "name the statement after its language (es.pdf, en.html, pt_BR.pdf)")
			case seenStatement[stem] != "":
				p.fail(rel, "a second statement in %q (also %s)", stem, seenStatement[stem])
			default:
				seenStatement[stem] = rel
				f.Name = stem
				p.Statements = append(p.Statements, Statement{File: f, Language: stem, ContentType: ct})
			}
		case "tests":
			if !codenameRe.MatchString(stem) {
				p.fail(rel, "testcase names may only contain letters, digits, '_', '.' and '-'")
				continue
			}
			var m map[string]File
			switch ext {
			case ".in":
				m = inputs
			case ".out", ".ans", ".sol":
				m = outputs
			default:
				p.fail(rel, "testcase files end in .in (input) and .out or .ans (expected output)")
				continue
			}
			if prev, dup := m[stem]; dup {
				p.fail(rel, "testcase %q appears twice (also %s)", stem, prev.Path)
				continue
			}
			m[stem] = f
		case "graders":
			addUnique(p, seenManager, f, &p.Managers)
		case "attachments":
			addUnique(p, seenAttachment, f, &p.Attachments)
		case "solutions":
			rest := strings.TrimPrefix(rel, "solutions/")
			if sub, file, nested := strings.Cut(rest, "/"); nested {
				// An output-only solution: a folder of output files.
				s := solutionDirs[sub]
				if s == nil {
					s = &Solution{Name: sub}
					solutionDirs[sub] = s
				}
				f.Name = path.Base(file)
				s.Files = append(s.Files, f)
				continue
			}
			p.Solutions = append(p.Solutions, Solution{Name: f.Name, Files: []File{f}})
		default:
			p.warn(rel, "not part of the format; ignored")
		}
	}
	for _, s := range solutionDirs {
		p.Solutions = append(p.Solutions, *s)
	}
	sort.Slice(p.Solutions, func(i, j int) bool { return p.Solutions[i].Name < p.Solutions[j].Name })

	if config == nil {
		p.fail("problem.yaml", "missing: every package needs a problem.yaml at its root")
		return p
	}
	data, err := readAll(config, 1<<20)
	if err != nil {
		p.fail("problem.yaml", "%v", err)
		return p
	}
	c, err := ParseConfig(data)
	if err != nil {
		p.fail("problem.yaml", "%v", err)
		return p
	}
	p.Config = c
	for _, e := range c.Check() {
		p.fail("problem.yaml", "%s", e)
	}
	for _, l := range c.Languages {
		if o.KnownLanguage != nil && !o.KnownLanguage(l) {
			p.fail("problem.yaml", "languages: %q is not configured in this CMS", l)
		}
	}
	for _, l := range c.PrimaryStatements {
		if seenStatement[l] == "" {
			p.fail("problem.yaml", "primary_statements: there is no statement in %q", l)
		}
	}
	p.pairTests(inputs, outputs)
	p.checkTaskType()
	p.checkScoring()
	p.checkSolutions(o)
	return p
}

// rootPrefix is the folder holding problem.yaml when the zip was made from
// a folder ("suma/problem.yaml"), or "".
func rootPrefix(zr *zip.Reader) string {
	for _, zf := range zr.File {
		if zf.Name == "problem.yaml" {
			return ""
		}
	}
	for _, zf := range zr.File {
		if dir, file := path.Split(zf.Name); file == "problem.yaml" && strings.Count(dir, "/") == 1 && !ignored(zf.Name) {
			return dir
		}
	}
	return ""
}

// ignored are files added by archivers and editors.
func ignored(name string) bool {
	base := path.Base(name)
	return strings.HasPrefix(name, "__MACOSX/") || base == ".DS_Store" || base == "Thumbs.db" || strings.HasSuffix(base, "~")
}

func addUnique(p *Package, seen map[string]string, f File, dst *[]File) {
	if prev := seen[f.Name]; prev != "" {
		p.fail(f.Path, "the file name %q is used twice (also %s)", f.Name, prev)
		return
	}
	if !codenameRe.MatchString(f.Name) {
		p.fail(f.Path, "file names may only contain letters, digits, '_', '.' and '-'")
		return
	}
	seen[f.Name] = f.Path
	*dst = append(*dst, f)
}

func readAll(zf *zip.File, limit int64) ([]byte, error) {
	rd, err := zf.Open()
	if err != nil {
		return nil, err
	}
	defer rd.Close()
	b, err := io.ReadAll(io.LimitReader(rd, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("larger than %d bytes", limit)
	}
	return b, nil
}

func (p *Package) pairTests(inputs, outputs map[string]File) {
	var public []*regexp.Regexp
	for _, re := range p.Config.PublicTests {
		if r, err := regexp.Compile("^(?:" + re + ")$"); err == nil {
			public = append(public, r)
		}
	}
	for code, in := range inputs {
		out, ok := outputs[code]
		if !ok {
			p.fail(in.Path, "no expected output (tests/%s.out)", code)
			continue
		}
		t := Test{Codename: code, Input: in, Output: out}
		for _, r := range public {
			t.Public = t.Public || r.MatchString(code)
		}
		p.Tests = append(p.Tests, t)
	}
	for code, out := range outputs {
		if _, ok := inputs[code]; !ok {
			p.fail(out.Path, "no input (tests/%s.in)", code)
		}
	}
	sort.Slice(p.Tests, func(i, j int) bool { return p.Tests[i].Codename < p.Tests[j].Codename })
	if len(p.Tests) == 0 && len(inputs) == 0 {
		p.fail("tests/", "no testcases: add tests/<name>.in and tests/<name>.out pairs")
	}
}

func (p *Package) checkTaskType() {
	c := p.Config
	if c.TaskType() == "" {
		return
	}
	params := c.TaskTypeParams()
	if err := tasktypes.ValidateParams(c.TaskType(), params); err != nil {
		p.fail("problem.yaml", "%v", err)
		return
	}
	have := map[string]bool{}
	for _, m := range p.Managers {
		have[m.Name] = true
		have[strings.TrimSuffix(m.Name, path.Ext(m.Name))] = true
	}
	for _, req := range tasktypes.RequiredManagers(c.TaskType(), params) {
		base := strings.TrimSuffix(req, ".<ext>")
		if !have[base] && !have[req] {
			where := base + ", " + base + ".c or " + base + ".cpp at the root"
			if strings.HasSuffix(req, ".<ext>") {
				where = "graders/" + base + ".<language extension>"
			}
			p.fail("problem.yaml", "this configuration needs %s (%s)", req, where)
		}
	}
}

func (p *Package) checkScoring() {
	c := p.Config
	if c.ScoreType() == "" || len(p.Tests) == 0 {
		return
	}
	codes, pub := make([]string, len(p.Tests)), make([]bool, len(p.Tests))
	for i, t := range p.Tests {
		codes[i], pub[i] = t.Codename, t.Public
	}
	st, err := scoring.New(c.ScoreType(), c.ScoreTypeParams(len(p.Tests)), codes, pub, c.Precision())
	if err != nil {
		p.fail("problem.yaml", "scoring: %v", err)
		return
	}
	p.MaxScore = st.MaxScore()
	if c.Scoring != "sum" {
		p.Coverage = scoring.MatchSubtasks(c.Specs(), codes)
		if len(p.Coverage.Uncovered) > 0 {
			p.warn("problem.yaml", "testcases in no subtask (they never count): %s", strings.Join(p.Coverage.Uncovered, " "))
		}
	}
}

func (p *Package) checkSolutions(o Options) {
	outputOnly := p.Config.Type == "output_only"
	for i := range p.Solutions {
		s := &p.Solutions[i]
		where := "solutions/" + s.Name
		prefix, _, ok := strings.Cut(s.Name, "_")
		if !ok || Verdicts[strings.ToLower(prefix)] == "" {
			p.fail(where, "start the name with the expected verdict: ac_, wa_, tle_, mle_, re_, ce_, pa_ or any_")
			continue
		}
		s.Expected = strings.ToLower(prefix)
		if outputOnly {
			continue
		}
		if len(s.Files) != 1 {
			p.fail(where, "a solution is one source file")
			continue
		}
		ext := strings.ToLower(path.Ext(s.Name))
		cands := o.LanguagesByExt[ext]
		for _, l := range cands {
			if len(p.Config.Languages) == 0 || contains(p.Config.Languages, l) {
				s.Language = l
				break
			}
		}
		if s.Language == "" && len(cands) > 0 {
			s.Language = cands[0]
		}
		if s.Language == "" && o.LanguagesByExt != nil {
			p.fail(where, "no configured language uses the extension %q", ext)
		}
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// OptionsFor builds the language options from the configured languages
// (their load order is the preference for extensions shared by several).
func OptionsFor(reg *langs.Registry) Options {
	o := Options{LanguagesByExt: map[string][]string{}, KnownLanguage: func(id string) bool { _, ok := reg.Get(id); return ok }}
	for _, l := range reg.All() {
		for _, e := range l.SourceExtensions {
			o.LanguagesByExt[strings.ToLower(e)] = append(o.LanguagesByExt[strings.ToLower(e)], l.ID)
		}
	}
	return o
}
