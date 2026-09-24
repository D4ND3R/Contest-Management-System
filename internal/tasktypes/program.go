package tasktypes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/sandbox"
)

const (
	// compilerOutputLimit bounds the compiler messages stored and shown.
	compilerOutputLimit = 64 << 10
	// maxExecutableSize bounds each compilation artifact.
	maxExecutableSize = 256 << 20
	compileStdout     = ".compile.out"
	compileStderr     = ".compile.err"
	runStdout         = ".stdout"
	runStderr         = ".stderr"
)

// Messages for execution outcomes (translated by the web UI).
const (
	MsgCompilationSucceeded = "Compilation succeeded"
	MsgCompilationFailed    = "Compilation failed"
	MsgCompilationTimeout   = "Compilation timed out"
	MsgCompilationMemory    = "Compilation exceeded the memory limit"
	MsgCompilationOutput    = "Compilation produced too much output"
	MsgTimeout              = "Execution timed out"
	MsgWallTimeout          = "Execution timed out (wall clock limit exceeded)"
	MsgMemory               = "Memory limit exceeded"
	MsgOutputLimit          = "Output limit exceeded"
	MsgNonZero              = "Execution failed because the return code was nonzero"
	MsgSignal               = "Execution killed by signal %d"
	MsgMissingOutput        = "Evaluation didn't produce file %s"
	MsgExecutionOK          = "Execution completed successfully"
)

// executionText describes a failed run.
func executionText(r *sandbox.Result) string {
	switch r.Status {
	case sandbox.StatusTimeout:
		return MsgTimeout
	case sandbox.StatusWallTimeout:
		return MsgWallTimeout
	case sandbox.StatusMemory:
		return MsgMemory
	case sandbox.StatusOutputLimit:
		return MsgOutputLimit
	case sandbox.StatusSignal:
		return fmt.Sprintf(MsgSignal, r.Signal)
	case sandbox.StatusNonZero:
		return MsgNonZero
	}
	return MsgExecutionOK
}

// sanitize makes compiler output safe to store and display: valid UTF-8,
// bounded length (with a truncation marker).
func sanitize(b []byte, truncated bool) string {
	s := strings.ToValidUTF8(string(b), "�")
	if truncated {
		s += "\n[output truncated]"
	}
	return s
}

// sourceSet describes the files compiled together.
type sourceSet struct {
	lang *langs.Language
	// user files, names resolved (e.g. "sum.cpp").
	user []jobs.File
	// managers copied next to the sources: headers and sources of the
	// language (graders, stubs, libraries).
	managers []jobs.File
	// grader is the manager compiled in first ("" = none).
	grader string
}

// main returns the basename of the main source.
func (s *sourceSet) main() string {
	if s.grader != "" {
		return strings.TrimSuffix(s.grader, filepath.Ext(s.grader))
	}
	for _, f := range s.user {
		if s.lang.IsSource(f.Name) {
			return strings.TrimSuffix(f.Name, filepath.Ext(f.Name))
		}
	}
	if len(s.user) > 0 {
		return strings.TrimSuffix(s.user[0].Name, filepath.Ext(s.user[0].Name))
	}
	return "main"
}

// sources returns the files passed to the compiler, grader first.
func (s *sourceSet) sources() []string {
	var out []string
	if s.grader != "" {
		out = append(out, s.grader)
	}
	for _, f := range s.user {
		if s.lang.IsSource(f.Name) {
			out = append(out, f.Name)
		}
	}
	return out
}

// newSourceSet selects the managers relevant to a language. When
// withGrader is set, the manager named "<grader>.<ext>" for the language is
// compiled in first.
func newSourceSet(lang *langs.Language, user, managers []jobs.File, withGrader bool, graderBase string) (*sourceSet, error) {
	s := &sourceSet{lang: lang, user: user}
	for _, m := range managers {
		if lang.IsHeader(m.Name) || lang.IsSource(m.Name) {
			s.managers = append(s.managers, m)
		}
	}
	if withGrader {
		if graderBase == "" {
			graderBase = "grader"
		}
		for _, ext := range lang.SourceExtensions {
			for _, m := range s.managers {
				if m.Name == graderBase+ext {
					s.grader = m.Name
				}
			}
			if s.grader != "" {
				break
			}
		}
		if s.grader == "" {
			return nil, fmt.Errorf("no %s grader for language %s", graderBase, lang.ID)
		}
	}
	return s, nil
}

// compile builds the source set in box and uploads the artifacts. Returned
// errors are infrastructure failures; compilation errors are reported in
// the Compilation.
func compile(ctx context.Context, env *Env, box *sandbox.Box, s *sourceSet) (*jobs.Compilation, error) {
	lang := s.lang
	names := map[string]bool{}
	for _, m := range s.managers {
		if err := env.put(ctx, box, m.Name, m.Digest, 0o644); err != nil {
			return nil, err
		}
		names[m.Name] = true
	}
	for _, f := range s.user {
		if names[f.Name] {
			return &jobs.Compilation{Success: false, Text: MsgCompilationFailed,
				Stderr: fmt.Sprintf("the file name %s is reserved by the task", f.Name)}, nil
		}
		if err := env.put(ctx, box, f.Name, f.Digest, 0o644); err != nil {
			return nil, err
		}
	}
	if seed := env.Seeds.get(ctx, env, lang); seed != "" {
		if err := copyTree(seed, box.Path(lang.CompileSeed.Dir)); err != nil {
			return nil, infra("copy compile seed: %v", err)
		}
	}
	main := s.main()
	exe := lang.ExecutableName(main)
	srcs := s.sources()
	vars := langs.Vars{Sources: srcs, Main: main, Executable: exe}
	if len(srcs) > 0 {
		vars.MainSource = srcs[0]
	}
	cl := lang.CompileLimits
	limits := sandbox.Limits{
		CPUTime:   cl.Time.D(),
		WallTime:  2*cl.Time.D() + time.Second,
		Memory:    int64(cl.Memory),
		Processes: cl.Processes,
		FileSize:  int64(cl.Output),
		OpenFiles: 512,
	}
	if !env.CG && lang.NoAddressSpaceLimit {
		limits.Memory = 0
	}
	c := &jobs.Compilation{Success: true, Text: MsgCompilationSucceeded}
	var stdout, stderr []string
	for _, tmpl := range lang.Compile {
		spec := &sandbox.Spec{
			Args:   langs.Expand(tmpl, vars),
			Stdout: compileStdout, Stderr: compileStderr,
			Env: lang.EnvList(), Dirs: env.dirs(lang), Limits: limits,
		}
		res, err := box.Run(ctx, spec)
		if err != nil {
			return nil, infra("compile: %v", err)
		}
		c.Time += res.CPUTime.Seconds()
		c.WallTime += res.WallTime.Seconds()
		c.Memory = max(c.Memory, res.Memory)
		if out, trunc, err := box.ReadFile(compileStdout, compilerOutputLimit); err == nil && len(out) > 0 {
			stdout = append(stdout, sanitize(out, trunc))
		}
		if out, trunc, err := box.ReadFile(compileStderr, compilerOutputLimit); err == nil && len(out) > 0 {
			stderr = append(stderr, sanitize(out, trunc))
		}
		os.Remove(box.Path(compileStdout))
		os.Remove(box.Path(compileStderr))
		if res.Status != sandbox.StatusOK {
			c.Success = false
			switch res.Status {
			case sandbox.StatusTimeout, sandbox.StatusWallTimeout:
				c.Text = MsgCompilationTimeout
			case sandbox.StatusMemory:
				c.Text = MsgCompilationMemory
			case sandbox.StatusOutputLimit:
				c.Text = MsgCompilationOutput
			default:
				c.Text = MsgCompilationFailed
			}
			break
		}
	}
	c.Stdout = truncateUTF8(strings.Join(stdout, "\n"), compilerOutputLimit)
	c.Stderr = truncateUTF8(strings.Join(stderr, "\n"), compilerOutputLimit)
	if !c.Success {
		return c, nil
	}
	artifacts, err := collectArtifacts(box, lang, exe)
	if err != nil {
		return nil, infra("collect artifacts: %v", err)
	}
	if len(artifacts) == 0 || artifacts[0] != exe {
		c.Success = false
		c.Text = MsgCompilationFailed
		c.Stderr = strings.TrimSpace(c.Stderr + "\nthe compiler did not produce " + exe)
		return c, nil
	}
	for _, name := range artifacts {
		d, err := env.upload(ctx, box, name, maxExecutableSize)
		if err != nil {
			if errors.Is(err, errInfra) {
				return nil, err
			}
			c.Success = false
			c.Text = MsgCompilationFailed
			c.Stderr = strings.TrimSpace(c.Stderr + "\n" + err.Error())
			return c, nil
		}
		c.Executables = append(c.Executables, jobs.File{Name: name, Digest: d})
	}
	return c, nil
}

func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) && len(s) > 0 {
		s = s[:len(s)-1]
	}
	return s + "\n[output truncated]"
}

// collectArtifacts lists the main executable (first) plus files matching
// the language's keep patterns, as regular files only.
func collectArtifacts(box *sandbox.Box, lang *langs.Language, exe string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	add := func(name string) {
		if seen[name] {
			return
		}
		f, _, err := box.OpenOutput(name)
		if err != nil {
			return
		}
		f.Close()
		seen[name] = true
		out = append(out, name)
	}
	add(exe)
	if len(out) == 0 {
		return nil, nil
	}
	for _, pat := range lang.KeepFiles {
		matches, err := filepath.Glob(filepath.Join(box.Dir(), pat))
		if err != nil {
			return nil, err
		}
		sort.Strings(matches)
		for _, m := range matches {
			rel, err := filepath.Rel(box.Dir(), m)
			if err == nil && !strings.HasPrefix(rel, ".") {
				add(rel)
			}
		}
	}
	return out, nil
}

// runOptions configure one execution of a contestant program.
type runOptions struct {
	lang        *langs.Language
	executables []jobs.File
	main        string
	limits      jobs.Limits
	stdin       string // in-sandbox path, "" = /dev/null
	stdout      string // box-relative, "" = /dev/null
	stderr      string
	args        []string // extra arguments appended to the run command
	dirs        []sandbox.Dir
	// noExecCopy skips copying executables (already in the box).
	noExecCopy bool
}

// run executes the contestant program in box.
func run(ctx context.Context, env *Env, box *sandbox.Box, o runOptions) (*sandbox.Result, error) {
	if !o.noExecCopy {
		for _, f := range o.executables {
			if err := env.put(ctx, box, f.Name, f.Digest, 0o755); err != nil {
				return nil, err
			}
		}
	}
	exe := o.lang.ExecutableName(o.main)
	mem := o.limits.MemoryBytes
	args := langs.Expand(o.lang.Run, langs.Vars{Main: o.main, Executable: exe, Memory: mem})
	args = append(args, o.args...)
	procs := max(o.limits.Processes, o.lang.RunProcesses, 1)
	lim := sandbox.Limits{
		CPUTime:   time.Duration(o.limits.TimeMs) * time.Millisecond,
		WallTime:  WallLimit(o.limits.TimeMs, o.limits.WallTimeMs),
		Memory:    mem,
		Stack:     mem,
		Processes: procs,
		FileSize:  o.limits.OutputBytes,
		OpenFiles: 64,
	}
	if !env.CG && o.lang.NoAddressSpaceLimit {
		lim.Memory, lim.Stack = 0, 0
	}
	spec := &sandbox.Spec{
		Args: args, Stdin: o.stdin, Stdout: o.stdout, Stderr: o.stderr,
		Env: o.lang.EnvList(), Dirs: append(env.dirs(o.lang), o.dirs...), Limits: lim,
	}
	res, err := box.Run(ctx, spec)
	if err != nil {
		return nil, infra("run: %v", err)
	}
	// Without cgroups the address-space limit may be disabled: enforce the
	// memory limit on the measured peak.
	if mem > 0 && res.Memory > mem && res.Status != sandbox.StatusTimeout && res.Status != sandbox.StatusWallTimeout {
		res.Status = sandbox.StatusMemory
	}
	return res, nil
}

// evaluationFromRun fills the resource fields of an evaluation.
func evaluationFromRun(tc jobs.Testcase, r *sandbox.Result) *jobs.Evaluation {
	return &jobs.Evaluation{
		TestcaseID: tc.ID, Codename: tc.Codename,
		Time: r.CPUTime.Seconds(), WallTime: r.WallTime.Seconds(), Memory: r.Memory,
		ExitStatus: string(r.Status), ExitCode: r.ExitCode, Signal: r.Signal,
	}
}
