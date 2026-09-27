// Package selftest judges a fixed set of programs to prove that a worker
// host judges correctly and safely: a security battery (fork bombs, memory
// and output hogs, network, file system and privilege escape attempts) and
// AC/WA/TLE/MLE/RE/CE solutions in every configured language. The programs
// are embedded, so "cms ctl judge-selftest" runs on production hosts; the
// worker tests run the same cases.
package selftest

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
)

//go:embed testdata
var programs embed.FS

// Program returns an embedded program ("malicious/fork_bomb.c",
// "samples/c11/ac.c", ...).
func Program(name string) ([]byte, error) { return programs.ReadFile(path.Join("testdata", name)) }

// Executor runs judging jobs (worker.Executor).
type Executor interface {
	Execute(ctx context.Context, slot int, job *jobs.Job) *jobs.Result
}

// Judge runs the self-test on an executor.
type Judge struct {
	Exec  Executor
	Store blob.Store
	Langs *langs.Registry
	// Slots is how many executor slots may be used at once (samples run
	// in parallel, one language per slot; the battery uses slot 0).
	Slots int
	// UIDs is the range [first, last] of sandbox user ids of the boxes the
	// executor uses; a process of one of them surviving a run is a failure.
	UIDs [2]int
}

// Seccomp reports whether the executor runs programs behind the seccomp
// filter (the battery's seccomp cases are skipped otherwise).
func (j *Judge) Seccomp() bool {
	s, ok := j.Exec.(interface{ Seccomp() bool })
	return ok && s.Seccomp()
}

// Result is the outcome of one program.
type Result struct {
	Group string // "security" or "samples"
	Name  string
	// Want lists the accepted results; Got is the result.
	Want []string
	Got  string
	// Detail describes resource usage or the compiler's message.
	Detail   string
	Problems []string
	Took     time.Duration
}

// OK reports whether the program got an accepted result and no host check
// failed.
func (r Result) OK() bool {
	if len(r.Problems) > 0 {
		return false
	}
	for _, w := range r.Want {
		if w == r.Got {
			return true
		}
	}
	return false
}

// Class is the verdict a contestant sees (both time limits are a TLE), used
// to compare runs. A security case has one question, whether the sandbox
// held: any of its accepted outcomes is the same class (a fork bomb may be
// stopped by the time or the memory limit, depending on the kernel's
// timing).
func (r Result) Class() string {
	if r.Group == "security" && r.OK() {
		return "contained"
	}
	if r.Got == "timeout" || r.Got == "timeout_wall" {
		return "TLE"
	}
	return r.Got
}

// ---------------------------------------------------------------- battery

// hostEnv is what the host checks of the battery need.
type hostEnv struct {
	port  int
	conns *atomic.Int64
	uids  [2]int
}

type securityCase struct {
	name, file, input string
	limits            jobs.Limits
	want              []string
	outcome           float64
	check             func(env *hostEnv, output string) []string
	// seccomp: the case tests the seccomp filter (skipped without it).
	seccomp bool
}

func defaultLimits() jobs.Limits {
	return jobs.Limits{TimeMs: 1000, MemoryBytes: 256 << 20, OutputBytes: 16 << 20, Processes: 1}
}

// tle accepts both time limits for programs that burn CPU: on an
// overloaded machine the wall-clock limit (2×TL) can expire before the
// program accumulates TL of CPU time. Both are "time limit exceeded".
var tle = []string{"timeout", "timeout_wall"}

// contained is what stops a fork bomb: a time limit, or the box's memory
// limit. Each fork the process limit refuses still allocates the child's
// kernel structures, charged to the box's control group and freed only
// after an RCU grace period; with many processes forking in a loop they
// can pile up to the memory limit first. Either way the box was killed
// whole (noProcs checks that nothing survived).
var contained = []string{"timeout", "timeout_wall", "memory"}

// security is what the seccomp filter makes of a forbidden system call.
var security = []string{"security"}

// escapePaths are the files write_outside.c tries to create on the host.
var escapePaths = []string{"/cms_pwned", "/usr/cms_pwned", "/usr/bin/cms_pwned", "/etc/cms_pwned",
	"/dev/shm/cms_pwned", "/tmp/cms_pwned_tmp", "/var/local/lib/isolate/cms_pwned"}

func securityCases() []securityCase {
	lim := defaultLimits()
	wide := lim
	wide.Processes = 64
	short := lim
	short.TimeMs = 500
	noProcs := func(env *hostEnv, _ string) []string { return leftoverProcesses(env.uids) }
	return []securityCase{
		{name: "control_ac", file: "sum_ok.c", input: "2 3\n", limits: lim, want: []string{"ok"}, outcome: 1},
		{name: "control_wa", file: "sum_wrong.c", input: "2 3\n", limits: lim, want: []string{"ok"}},
		{name: "control_ce", file: "syntax_error.c", want: []string{"compile_error"}},
		{name: "fork_bomb", file: "fork_bomb.c", limits: lim, want: contained, check: noProcs},
		{name: "fork_bomb_64_procs", file: "fork_bomb_wide.c", limits: wide, want: contained, check: noProcs},
		{name: "read_passwd", file: "read_passwd.c", limits: lim, want: []string{"ok"},
			check: func(_ *hostEnv, out string) []string {
				// /etc/alternatives is mounted on purpose (compiler symlinks),
				// so /etc lists exactly that entry.
				out = strings.ReplaceAll(out, "LEAK /etc/alternatives\n", "")
				var p []string
				if strings.Contains(out, "LEAK") {
					p = append(p, "host data readable from the sandbox: "+firstLine(out))
				}
				if !strings.Contains(out, "done") {
					p = append(p, fmt.Sprintf("program did not complete: %q", firstLine(out)))
				}
				return p
			}},
		{name: "network", file: "network.c", limits: lim, want: []string{"ok"},
			check: func(env *hostEnv, out string) []string {
				var p []string
				if strings.Contains(out, "CONNECTED") || strings.Contains(out, "UDP-SENT") {
					p = append(p, "network reachable from the sandbox: "+firstLine(out))
				}
				if n := env.conns.Load(); n != 0 {
					p = append(p, fmt.Sprintf("a host listener received %d connections", n))
				}
				return p
			}},
		{name: "write_outside_box", file: "write_outside.c", limits: lim, want: []string{"ok"},
			check: func(_ *hostEnv, out string) []string {
				var p []string
				if strings.Contains(out, "ESCAPED") {
					p = append(p, "wrote outside the box: "+firstLine(out))
				}
				for _, f := range escapePaths {
					if _, err := os.Stat(f); err == nil {
						os.Remove(f)
						p = append(p, "host file "+f+" was created")
					}
				}
				return p
			}},
		{name: "sleep_forever", file: "sleep_forever.c", limits: short, want: []string{"timeout_wall"}},
		{name: "memory_hog", file: "memory_hog.c", limits: lim, want: []string{"memory"}},
		{name: "huge_output", file: "huge_output.c", limits: lim, want: []string{"output_limit"}},
		// stderr is discarded (/dev/null): unlimited but harmless; the
		// program then prints the right answer.
		{name: "huge_stderr", file: "huge_stderr.c", input: "2 3\n", limits: lim, want: []string{"ok"}, outcome: 1},
		{name: "include_dev_random", file: "include_dev_random.c", want: []string{"compile_error"}},
		{name: "include_dev_zero", file: "include_dev_zero.c", want: []string{"compile_error"}},
		{name: "threads_no_allowance", file: "threads.cpp", limits: lim, want: []string{"signal"}},
		{name: "threads_with_allowance", file: "threads.cpp", limits: wide, want: tle, check: noProcs},
		// kill(1) is ignored (namespace init) and kill(-1) cannot reach
		// anything outside the sandbox's pid namespace.
		{name: "kill_all", file: "kill_all.c", limits: lim, want: []string{"ok"},
			check: func(env *hostEnv, out string) []string {
				var p []string
				if !strings.Contains(out, "survived") {
					p = append(p, fmt.Sprintf("unexpected output %q", firstLine(out)))
				}
				return append(p, leftoverProcesses(env.uids)...)
			}},
		{name: "stack_overflow", file: "stack_overflow.c", limits: lim, want: []string{"signal", "memory"}},
		{name: "seccomp_user_namespace", file: "forbidden.c", input: "unshare\n", limits: lim, want: security, seccomp: true},
		{name: "seccomp_clone_newnet", file: "forbidden.c", input: "clone_newnet\n", limits: lim, want: security, seccomp: true},
		{name: "seccomp_bpf", file: "forbidden.c", input: "bpf\n", limits: lim, want: security, seccomp: true},
		{name: "seccomp_io_uring", file: "forbidden.c", input: "io_uring\n", limits: lim, want: security, seccomp: true},
		{name: "seccomp_ptrace", file: "forbidden.c", input: "ptrace\n", limits: lim, want: security, seccomp: true},
		{name: "seccomp_keyctl", file: "forbidden.c", input: "keyctl\n", limits: lim, want: security, seccomp: true},
		{name: "seccomp_perf_event", file: "forbidden.c", input: "perf\n", limits: lim, want: security, seccomp: true},
		// setuid fails; chroot and mount are refused, or kill it with the
		// seccomp filter.
		{name: "privilege_escalation", file: "privilege.c", limits: lim, want: []string{"ok", "security"},
			check: func(_ *hostEnv, out string) []string {
				if strings.Contains(out, "ROOT") {
					return []string{"privilege escalation succeeded: " + firstLine(out)}
				}
				return nil
			}},
	}
}

// leftoverProcesses lists the processes of the sandbox users still alive.
func leftoverProcesses(uids [2]int) []string {
	time.Sleep(50 * time.Millisecond)
	ents, _ := os.ReadDir("/proc")
	var out []string
	for _, e := range ents {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		st, err := os.Stat(filepath.Join("/proc", e.Name()))
		if err != nil {
			continue
		}
		if uid := statUID(st); uid >= uids[0] && uid <= uids[1] {
			cmd, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
			out = append(out, fmt.Sprintf("sandbox process %s (uid %d) survived: %q", e.Name(), uid, cmd))
		}
	}
	return out
}

// RunBattery judges the security battery (sequentially, on slot 0).
func (j *Judge) RunBattery(ctx context.Context) ([]Result, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer ln.Close()
	env := &hostEnv{port: ln.Addr().(*net.TCPAddr).Port, conns: &atomic.Int64{}, uids: j.UIDs}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			env.conns.Add(1)
			c.Close()
		}
	}()
	var out []Result
	for _, c := range securityCases() {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if c.seccomp && !j.Seccomp() {
			continue
		}
		src, err := Program("malicious/" + c.file)
		if err != nil {
			return nil, err
		}
		if c.name == "network" {
			c.input = strconv.Itoa(env.port)
		}
		lang := "c11"
		if strings.HasSuffix(c.file, ".cpp") {
			lang = "cpp17"
		}
		name := "sol" + path.Ext(c.file)
		start := time.Now()
		r := Result{Group: "security", Name: c.name, Want: c.want}
		comp, evs, err := j.compileAndRun(ctx, 0, lang, map[string][]byte{name: src}, c.limits, c.input, "5\n")
		if err != nil {
			r.Got, r.Problems = "infrastructure error", []string{err.Error()}
			out = append(out, r)
			continue
		}
		var output string
		if comp.Success {
			ev := evs[0]
			r.Got = ev.ExitStatus
			r.Detail = fmt.Sprintf("time=%.3fs wall=%.3fs mem=%dKiB", ev.Time, ev.WallTime, ev.Memory>>10)
			if ev.Outcome != c.outcome {
				r.Problems = append(r.Problems, fmt.Sprintf("outcome %v, want %v", ev.Outcome, c.outcome))
			}
			if c.check != nil {
				output = j.output(ctx, lang, name, src, c)
			}
		} else {
			r.Got = "compile_error"
			r.Detail = firstLine(comp.Text + ": " + comp.Stderr + comp.Stdout)
		}
		if c.check != nil {
			r.Problems = append(r.Problems, c.check(env, output)...)
		}
		r.Took = time.Since(start)
		out = append(out, r)
	}
	return out, nil
}

// output re-runs a program as a user test to capture its stdout (the
// evaluation itself only stores the verdict).
func (j *Judge) output(ctx context.Context, lang, name string, src []byte, c securityCase) string {
	job, err := j.job(ctx, jobs.KindUserTest, lang, map[string][]byte{name: src}, c.limits)
	if err != nil {
		return ""
	}
	if job.Input, err = j.put(ctx, []byte(c.input)); err != nil {
		return ""
	}
	r := j.Exec.Execute(ctx, 0, job)
	if r.Error != "" || r.UserTest == nil || r.UserTest.Output == "" {
		return ""
	}
	data, _, err := blob.ReadLimited(ctx, j.Store, r.UserTest.Output, 1<<20)
	if err != nil {
		return ""
	}
	return string(data)
}

// ---------------------------------------------------------------- samples

// SampleLanguages lists the languages with sample solutions.
func SampleLanguages() []string {
	des, _ := fs.ReadDir(programs, "testdata/samples")
	var out []string
	for _, d := range des {
		out = append(out, d.Name())
	}
	sort.Strings(out)
	return out
}

var sampleKinds = []struct{ file, want string }{
	{"ac", "AC"}, {"wa", "WA"}, {"tle", "TLE"}, {"mle", "MLE"}, {"re", "RE"}, {"ce", "CE"},
}

// Verdict maps a judged submission to the verdict a contestant sees.
func Verdict(comp *jobs.Compilation, evs []jobs.Evaluation) string {
	if comp != nil && !comp.Success {
		return "CE"
	}
	if len(evs) == 0 {
		return "?"
	}
	e := evs[0]
	switch e.ExitStatus {
	case "ok":
		if e.Outcome >= 1 {
			return "AC"
		}
		return "WA"
	case "timeout", "timeout_wall":
		return "TLE"
	case "memory":
		return "MLE"
	case "nonzero", "signal":
		return "RE"
	case "output_limit":
		return "OLE"
	}
	return "SE"
}

// ToolchainAvailable reports whether the language's compiler (or
// interpreter) exists on this host.
func ToolchainAvailable(l *langs.Language) bool {
	cmd := l.Run
	if len(l.Compile) > 0 {
		cmd = l.Compile[0]
	}
	if len(cmd) == 0 {
		return false
	}
	bin := cmd[0]
	if bin == "/usr/bin/env" && len(cmd) > 1 {
		// Resolve against the PATH the sandbox will use.
		p := l.Env["PATH"]
		if p == "" {
			p = "/usr/local/bin:/usr/bin:/bin"
		}
		for _, dir := range filepath.SplitList(p) {
			if st, err := os.Stat(filepath.Join(dir, cmd[1])); err == nil && !st.IsDir() {
				return true
			}
		}
		return false
	}
	_, err := os.Stat(bin)
	return err == nil
}

// RunSamples judges the sample solutions of the given languages (one
// language per slot at a time). Languages without a toolchain on this host
// are returned in skipped.
func (j *Judge) RunSamples(ctx context.Context, languages []string) (results []Result, skipped []string, err error) {
	lim := jobs.Limits{TimeMs: 2000, MemoryBytes: 256 << 20, OutputBytes: 16 << 20, Processes: 1}
	var todo []string
	for _, id := range languages {
		l, ok := j.Langs.Get(id)
		if !ok || !ToolchainAvailable(l) {
			skipped = append(skipped, id)
			continue
		}
		todo = append(todo, id)
	}
	slots := max(j.Slots, 1)
	var mu sync.Mutex
	var wg sync.WaitGroup
	next := make(chan string)
	var firstErr error
	for s := 0; s < slots; s++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			for lang := range next {
				rs, err := j.runLanguage(ctx, slot, lang, lim)
				mu.Lock()
				results = append(results, rs...)
				if err != nil && firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}(s)
	}
	for _, id := range todo {
		next <- id
	}
	close(next)
	wg.Wait()
	sort.Slice(results, func(a, b int) bool { return results[a].Name < results[b].Name })
	return results, skipped, firstErr
}

func (j *Judge) runLanguage(ctx context.Context, slot int, lang string, lim jobs.Limits) ([]Result, error) {
	des, err := fs.ReadDir(programs, "testdata/samples/"+lang)
	if err != nil || len(des) == 0 {
		return nil, fmt.Errorf("no samples for %s", lang)
	}
	ext := path.Ext(des[0].Name())
	var out []Result
	for _, k := range sampleKinds {
		src, err := Program("samples/" + lang + "/" + k.file + ext)
		if err != nil {
			return out, fmt.Errorf("missing sample %s/%s%s", lang, k.file, ext)
		}
		start := time.Now()
		r := Result{Group: "samples", Name: lang + "/" + k.file, Want: []string{k.want}}
		comp, evs, err := j.compileAndRun(ctx, slot, lang, map[string][]byte{"sol" + ext: src}, lim, "20 22\n", "42\n")
		if err != nil {
			r.Got, r.Problems = "infrastructure error", []string{err.Error()}
		} else {
			r.Got = Verdict(comp, evs)
			if !comp.Success {
				r.Detail = firstLine(comp.Stderr + comp.Stdout)
			} else if len(evs) > 0 {
				r.Detail = fmt.Sprintf("%s time=%.2fs mem=%dMiB", evs[0].ExitStatus, evs[0].Time, evs[0].Memory>>20)
			}
			if r.Got != k.want && comp.Success {
				r.Detail += fmt.Sprintf(" (compiled: %s time=%.2fs)", comp.Text, comp.Time)
			}
		}
		r.Took = time.Since(start)
		out = append(out, r)
	}
	return out, nil
}

// ---------------------------------------------------------------- comparing

// Compare returns the programs whose verdict differs between two runs.
func Compare(a, b []Result) []string {
	second := map[string]Result{}
	for _, r := range b {
		second[r.Group+"/"+r.Name] = r
	}
	var out []string
	for _, r := range a {
		o, ok := second[r.Group+"/"+r.Name]
		switch {
		case !ok:
			out = append(out, r.Name+": missing from the second run")
		case r.Class() != o.Class():
			out = append(out, fmt.Sprintf("%s: %s, then %s", r.Name, r.describe(), o.describe()))
		}
	}
	return out
}

// describe is the outcome as Compare reports it.
func (r Result) describe() string {
	if r.Group == "security" && !r.OK() {
		return r.Got + " (not contained)"
	}
	return r.Got
}

// ---------------------------------------------------------------- jobs

func (j *Judge) put(ctx context.Context, b []byte) (string, error) {
	info, err := j.Store.PutBytes(ctx, b)
	return info.Digest, err
}

func (j *Judge) job(ctx context.Context, kind jobs.Kind, lang string, files map[string][]byte, lim jobs.Limits) (*jobs.Job, error) {
	l, ok := j.Langs.Get(lang)
	if !ok {
		return nil, fmt.Errorf("language %s is not configured", lang)
	}
	job := &jobs.Job{ID: "selftest", Kind: kind, TaskType: "Batch", TaskTypeParams: json.RawMessage(`null`), Language: l, Limits: lim}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		d, err := j.put(ctx, files[n])
		if err != nil {
			return nil, err
		}
		job.Files = append(job.Files, jobs.File{Name: n, Digest: d})
	}
	return job, nil
}

// compileAndRun compiles a Batch submission and, when it compiles,
// evaluates it on one testcase.
func (j *Judge) compileAndRun(ctx context.Context, slot int, lang string, files map[string][]byte, lim jobs.Limits, in, want string) (*jobs.Compilation, []jobs.Evaluation, error) {
	cj, err := j.job(ctx, jobs.KindCompile, lang, files, lim)
	if err != nil {
		return nil, nil, err
	}
	res := j.Exec.Execute(ctx, slot, cj)
	if res.Error != "" {
		return nil, nil, errors.New(res.Error)
	}
	if res.Compilation == nil {
		return nil, nil, errors.New("no compilation result")
	}
	if !res.Compilation.Success {
		return res.Compilation, nil, nil
	}
	ej, err := j.job(ctx, jobs.KindEvaluate, lang, files, lim)
	if err != nil {
		return nil, nil, err
	}
	ej.Executables = res.Compilation.Executables
	tc := jobs.Testcase{ID: 1, Codename: "a"}
	if tc.Input, err = j.put(ctx, []byte(in)); err != nil {
		return nil, nil, err
	}
	if tc.Output, err = j.put(ctx, []byte(want)); err != nil {
		return nil, nil, err
	}
	ej.Testcases = []jobs.Testcase{tc}
	er := j.Exec.Execute(ctx, slot, ej)
	if er.Error != "" {
		return res.Compilation, nil, errors.New(er.Error)
	}
	if len(er.Evaluations) == 0 {
		return res.Compilation, nil, errors.New("no evaluation result")
	}
	return res.Compilation, er.Evaluations, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}
