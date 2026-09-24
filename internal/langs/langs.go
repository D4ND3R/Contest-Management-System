// Package langs loads programming-language definitions from YAML files
// (config/languages/*.yaml). Adding a language means adding a file; the
// worker only interprets the command templates below.
//
// Command templates are argv lists. Placeholders:
//
//	{sources}     every source file, grader first (expands to several args)
//	{main}        basename (without extension) of the main source: the
//	              grader when the task has one, else the first user file
//	{executable}  the value of `executable` after expansion
//	{memory_mb}   memory limit of the run in MiB (run commands only)
//	{memory_kb}   memory limit of the run in KiB (run commands only)
package langs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"gopkg.in/yaml.v3"
)

// Limits for compilation.
type Limits struct {
	Time      config.Duration `yaml:"time" json:"time"`
	Memory    config.ByteSize `yaml:"memory" json:"memory"`
	Processes int             `yaml:"processes" json:"processes"`
	Output    config.ByteSize `yaml:"output" json:"output"`
}

// Language is one programming language definition.
type Language struct {
	ID               string     `yaml:"id" json:"id"`
	Name             string     `yaml:"name" json:"name"`
	VersionCommand   []string   `yaml:"version_command" json:"version_command,omitempty"`
	SourceExtensions []string   `yaml:"source_extensions" json:"source_extensions"`
	HeaderExtensions []string   `yaml:"header_extensions" json:"header_extensions,omitempty"`
	Compile          [][]string `yaml:"compile" json:"compile,omitempty"`
	// Executable is the main artifact kept after compilation (template).
	Executable string `yaml:"executable" json:"executable"`
	// KeepFiles are glob patterns of extra artifacts to keep (e.g. "*.class").
	KeepFiles []string `yaml:"keep_files" json:"keep_files,omitempty"`
	Run       []string `yaml:"run" json:"run"`
	// CompileLimits apply to every compile command.
	CompileLimits Limits `yaml:"compile_limits" json:"compile_limits"`
	// RunProcesses is the minimum number of processes/threads the runtime
	// needs (JVM, .NET, Go runtime threads); the task's own limit is raised
	// to this value.
	RunProcesses int               `yaml:"run_processes" json:"run_processes,omitempty"`
	Env          map[string]string `yaml:"env" json:"env,omitempty"`
	// Dirs are extra host directories mounted read-only (missing ones are ignored).
	Dirs []string `yaml:"dirs" json:"dirs,omitempty"`
	// NoAddressSpaceLimit: never limit virtual memory for this language when
	// cgroups are unavailable (managed runtimes reserve huge address spaces).
	NoAddressSpaceLimit bool `yaml:"no_address_space_limit" json:"no_address_space_limit,omitempty"`
}

// DefaultEnv is the environment every sandboxed command gets unless the
// language overrides a key.
var DefaultEnv = map[string]string{
	"PATH": "/usr/local/bin:/usr/bin:/bin",
	"HOME": "/tmp",
	"LANG": "C.UTF-8",
}

// DefaultDirs are read-only host directories mounted in every sandbox that
// runs a language toolchain: Debian/Ubuntu reach compilers and runtimes
// through /etc/alternatives symlinks. They hold no secrets.
var DefaultDirs = []string{"/etc/alternatives"}

// SourceExtension is the canonical extension (used to resolve "%l").
func (l *Language) SourceExtension() string { return l.SourceExtensions[0] }

// Interpreted reports whether the language has no compile step.
func (l *Language) Interpreted() bool { return len(l.Compile) == 0 }

// EnvList returns KEY=VALUE pairs (defaults overridden by the language), sorted.
func (l *Language) EnvList() []string {
	m := map[string]string{}
	for k, v := range DefaultEnv {
		m[k] = v
	}
	for k, v := range l.Env {
		m[k] = v
	}
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// IsSource reports whether filename has one of the language's source extensions.
func (l *Language) IsSource(name string) bool {
	for _, e := range l.SourceExtensions {
		if strings.HasSuffix(name, e) {
			return true
		}
	}
	return false
}

// IsHeader reports whether filename has one of the language's header extensions.
func (l *Language) IsHeader(name string) bool {
	for _, e := range l.HeaderExtensions {
		if strings.HasSuffix(name, e) {
			return true
		}
	}
	return false
}

// Vars are the values substituted in command templates.
type Vars struct {
	Sources    []string
	Main       string
	Executable string
	Memory     int64 // bytes
}

// Expand substitutes placeholders in an argv template.
func Expand(tmpl []string, v Vars) []string {
	out := make([]string, 0, len(tmpl)+len(v.Sources))
	r := strings.NewReplacer(
		"{main}", v.Main,
		"{executable}", v.Executable,
		"{memory_mb}", strconv.FormatInt(v.Memory>>20, 10),
		"{memory_kb}", strconv.FormatInt(v.Memory>>10, 10),
		"{sources_joined}", strings.Join(v.Sources, " "),
	)
	for _, a := range tmpl {
		if a == "{sources}" {
			out = append(out, v.Sources...)
			continue
		}
		out = append(out, r.Replace(a))
	}
	return out
}

// ExecutableName expands the executable template for a main basename.
func (l *Language) ExecutableName(main string) string {
	return Expand([]string{l.Executable}, Vars{Main: main})[0]
}

// Validate checks a definition for mistakes that would only show up when
// judging.
func (l *Language) Validate() error {
	var errs []error
	if l.ID == "" || strings.ContainsAny(l.ID, " /\\") {
		errs = append(errs, fmt.Errorf("invalid id %q", l.ID))
	}
	if l.Name == "" {
		errs = append(errs, errors.New("name is required"))
	}
	if len(l.SourceExtensions) == 0 {
		errs = append(errs, errors.New("source_extensions is required"))
	}
	for _, e := range append(append([]string{}, l.SourceExtensions...), l.HeaderExtensions...) {
		if !strings.HasPrefix(e, ".") {
			errs = append(errs, fmt.Errorf("extension %q must start with a dot", e))
		}
	}
	if len(l.Run) == 0 {
		errs = append(errs, errors.New("run is required"))
	}
	if l.Executable == "" {
		errs = append(errs, errors.New("executable is required"))
	}
	for i, c := range l.Compile {
		if len(c) == 0 {
			errs = append(errs, fmt.Errorf("compile[%d] is empty", i))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("language %q: %w", l.ID, err)
	}
	return nil
}

// withDefaults fills unset compile limits.
func (l *Language) withDefaults() {
	if l.CompileLimits.Time == 0 {
		l.CompileLimits.Time = config.Duration(10 * time.Second)
	}
	if l.CompileLimits.Memory == 0 {
		l.CompileLimits.Memory = 1 << 30
	}
	if l.CompileLimits.Processes == 0 {
		l.CompileLimits.Processes = 32
	}
	if l.CompileLimits.Output == 0 {
		l.CompileLimits.Output = 256 << 20
	}
}

// Registry holds the loaded languages.
type Registry struct {
	byID  map[string]*Language
	order []string
}

// Load reads every *.yaml file in dir.
func Load(dir string) (*Registry, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	r := &Registry{byID: map[string]*Language{}}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var l Language
		dec := yaml.NewDecoder(strings.NewReader(string(data)))
		dec.KnownFields(true)
		if err := dec.Decode(&l); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		if err := r.Add(&l); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
	}
	return r, nil
}

// Add registers a language (validating it).
func (r *Registry) Add(l *Language) error {
	l.withDefaults()
	if err := l.Validate(); err != nil {
		return err
	}
	if _, dup := r.byID[l.ID]; dup {
		return fmt.Errorf("duplicate language id %q", l.ID)
	}
	r.byID[l.ID] = l
	r.order = append(r.order, l.ID)
	return nil
}

// Get returns a language by id.
func (r *Registry) Get(id string) (*Language, bool) {
	l, ok := r.byID[id]
	return l, ok
}

// All returns the languages in load order.
func (r *Registry) All() []*Language {
	out := make([]*Language, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.byID[id])
	}
	return out
}

// IDs returns every language id in load order.
func (r *Registry) IDs() []string { return append([]string(nil), r.order...) }
