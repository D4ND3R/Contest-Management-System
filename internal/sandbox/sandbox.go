// Package sandbox runs untrusted programs inside isolate
// (https://github.com/ioi/isolate) boxes.
//
// A Box is one isolate sandbox, freshly initialised for every run (see Slot
// for how the re-initialisation cost is hidden). Files leave the box only
// through ReadFile/OpenOutput, which refuse symlinks and non-regular files,
// so a program cannot trick the worker (running as root) into reading host
// files.
package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Status classifies how a sandboxed run ended.
type Status string

const (
	StatusOK           Status = "ok"            // exited with code 0
	StatusNonZero      Status = "nonzero"       // exited with a non-zero code
	StatusSignal       Status = "signal"        // killed by a signal (crash)
	StatusTimeout      Status = "timeout"       // CPU time limit exceeded
	StatusWallTimeout  Status = "timeout_wall"  // wall-clock limit exceeded
	StatusMemory       Status = "memory"        // memory limit exceeded
	StatusOutputLimit  Status = "output_limit"  // file size limit exceeded (SIGXFSZ)
	StatusSandboxError Status = "sandbox_error" // isolate itself failed
)

// Limits constrain one run. Zero values mean "no limit" except Processes,
// which defaults to 1.
type Limits struct {
	CPUTime   time.Duration
	WallTime  time.Duration
	Memory    int64 // bytes
	Stack     int64 // bytes (0 = same as Memory when Memory is set)
	Processes int   // processes + threads
	FileSize  int64 // bytes, per written file (stdout included)
	OpenFiles int
}

// Dir is an extra directory bound inside the sandbox.
type Dir struct {
	Inside  string // absolute path inside the sandbox
	Outside string // host path ("" = same as Inside)
	RW      bool
	Maybe   bool // ignore if the host path does not exist
	NoExec  bool
}

// Spec describes one program execution.
type Spec struct {
	Args           []string
	Stdin          string // path relative to the box ("" = /dev/null)
	Stdout         string // path relative to the box ("" = /dev/null)
	Stderr         string // path relative to the box ("" = /dev/null)
	StderrToStdout bool
	Env            []string // KEY=VALUE; the environment is otherwise empty
	Dirs           []Dir
	Limits         Limits
	// ShareNet keeps the host network namespace (never for contestants).
	ShareNet bool
}

// Result of a run.
type Result struct {
	Status   Status
	ExitCode int
	Signal   int
	CPUTime  time.Duration
	WallTime time.Duration
	Memory   int64 // peak bytes (cgroup when available, else max RSS)
	Message  string
	// Raw meta-file content, for debugging and logs.
	Meta map[string]string
}

// Success reports whether the program exited normally with code 0.
func (r *Result) Success() bool { return r.Status == StatusOK }

// Isolate locates and configures the isolate binary.
type Isolate struct {
	Path    string // path to the isolate binary
	CG      bool   // use control groups (--cg)
	BoxRoot string // box_root from the isolate configuration
}

// ErrSandbox wraps isolate failures (infrastructure errors, not verdicts).
var ErrSandbox = errors.New("sandbox error")

func (iso *Isolate) cmd(ctx context.Context, args ...string) *exec.Cmd {
	full := make([]string, 0, len(args)+1)
	if iso.CG {
		full = append(full, "--cg")
	}
	full = append(full, args...)
	return exec.CommandContext(ctx, iso.Path, full...)
}

// Box is an initialised isolate sandbox.
type Box struct {
	ID   int
	Root string // e.g. /var/local/lib/isolate/7
	// Core the box is pinned to (-1 = not pinned).
	Core int

	iso      *Isolate
	maint    []int
	metaPath string
}

// init creates (or recreates) box id; maintenance commands are pinned to
// the maint CPUs so they never disturb a program running on a judging core.
func (iso *Isolate) init(ctx context.Context, id, core int, maint []int) (*Box, error) {
	// Clean first: a crashed worker may have left the box behind.
	_ = runPinned(iso.cmd(ctx, "--box-id="+strconv.Itoa(id), "--cleanup"), maint)
	cmd := iso.cmd(ctx, "--box-id="+strconv.Itoa(id), "--init")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := runPinned(cmd, maint); err != nil {
		return nil, fmt.Errorf("%w: isolate --init box %d: %v: %s", ErrSandbox, id, err, bytes.TrimSpace(out.Bytes()))
	}
	root := strings.TrimSpace(out.String())
	if root == "" || strings.Contains(root, "\n") {
		root = filepath.Join(iso.BoxRoot, strconv.Itoa(id))
	}
	b := &Box{ID: id, Root: root, Core: core, iso: iso, maint: maint}
	mf, err := os.CreateTemp("", fmt.Sprintf("cms-box%d-meta-*", id))
	if err != nil {
		return nil, err
	}
	mf.Close()
	b.metaPath = mf.Name()
	return b, nil
}

// Init creates a standalone box (outside any slot), e.g. for tools.
func (iso *Isolate) Init(ctx context.Context, id int) (*Box, error) {
	return iso.init(ctx, id, -1, nil)
}

func runPinned(cmd *exec.Cmd, cpus []int) error {
	if err := startPinnedSet(cmd, cpus); err != nil {
		return err
	}
	return cmd.Wait()
}

// Dir returns the host path of the box directory (/box inside).
func (b *Box) Dir() string { return filepath.Join(b.Root, "box") }

// Path returns the host path of a file inside the box.
func (b *Box) Path(name string) string { return filepath.Join(b.Dir(), name) }

// Cleanup destroys the box.
func (b *Box) Cleanup(ctx context.Context) error {
	os.Remove(b.metaPath)
	cmd := b.iso.cmd(ctx, "--box-id="+strconv.Itoa(b.ID), "--cleanup")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := runPinned(cmd, b.maint); err != nil {
		return fmt.Errorf("%w: isolate --cleanup: %v: %s", ErrSandbox, err, out.Bytes())
	}
	return nil
}

func (b *Box) dispose() { _ = b.Cleanup(context.Background()) }

// WriteFile creates name in the box with the given content. Files written
// by the worker are root-owned, so the sandboxed program can read (and, with
// mode 0755, execute) but not modify them.
func (b *Box) WriteFile(name string, data []byte, mode os.FileMode) error {
	f, err := b.create(name, mode)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func (b *Box) create(name string, mode os.FileMode) (*os.File, error) {
	if err := checkName(name); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(b.Path(name), os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, mode)
	if err != nil {
		return nil, err
	}
	// OpenFile applies the umask; set the exact mode.
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// CopyIn copies the host file src into the box as name. It always copies:
// isolate chowns every file of the box to the sandbox user when a run
// starts, so a hard link would hand a shared (cached) inode to untrusted code.
// Use a read-only Stage for zero-copy access to large inputs.
func (b *Box) CopyIn(src, name string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := b.create(name, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// WriteFrom creates name in the box with the content of r.
func (b *Box) WriteFrom(name string, r io.Reader, mode os.FileMode) error {
	out, err := b.create(name, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, r)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// Mkdir creates a subdirectory of the box writable by the sandboxed program.
func (b *Box) Mkdir(name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	// isolate hands the whole box to the sandbox user when a run starts.
	return os.Mkdir(b.Path(name), 0o755)
}

// ErrUnsafeFile is returned when a file produced in the box is a symlink,
// a device or anything but a regular file.
var ErrUnsafeFile = errors.New("unsafe file in sandbox")

// OpenOutput opens a file produced by the sandboxed program for reading. It
// refuses symlinks (O_NOFOLLOW) and non-regular files. Missing files return
// an error satisfying errors.Is(err, os.ErrNotExist).
func (b *Box) OpenOutput(name string) (*os.File, int64, error) {
	if err := checkName(name); err != nil {
		return nil, 0, err
	}
	f, err := os.OpenFile(b.Path(name), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, 0, ErrUnsafeFile
		}
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	if !st.Mode().IsRegular() {
		f.Close()
		return nil, 0, ErrUnsafeFile
	}
	return f, st.Size(), nil
}

// ReadFile reads at most limit bytes of a box file; truncated reports whether
// the file was longer.
func (b *Box) ReadFile(name string, limit int64) (data []byte, truncated bool, err error) {
	f, size, err := b.OpenOutput(name)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	n := size
	if n > limit {
		n = limit
	}
	data = make([]byte, n)
	m, err := io.ReadFull(f, data)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, false, err
	}
	return data[:m], size > limit, nil
}

// checkName accepts plain relative paths inside the box (no "..", no absolute).
func checkName(name string) error {
	if name == "" || filepath.IsAbs(name) || filepath.Clean(name) != name || strings.HasPrefix(name, "..") {
		return fmt.Errorf("invalid box file name %q", name)
	}
	return nil
}

func orDevNull(p string) string {
	if p == "" {
		return "/dev/null"
	}
	return p
}

// limitedBuffer keeps the first max bytes written to it.
type limitedBuffer struct {
	buf bytes.Buffer
	max int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := l.max - l.buf.Len(); room > 0 {
		if len(p) > room {
			l.buf.Write(p[:room])
		} else {
			l.buf.Write(p)
		}
	}
	return len(p), nil
}

func kb(bytes int64) string { return strconv.FormatInt((bytes+1023)/1024, 10) }

func secs(d time.Duration) string { return strconv.FormatFloat(d.Seconds(), 'f', 3, 64) }

// args builds the isolate --run command line.
func (b *Box) args(spec *Spec) []string {
	l := spec.Limits
	a := []string{"--box-id=" + strconv.Itoa(b.ID), "--meta=" + b.metaPath, "--silent"}
	if l.CPUTime > 0 {
		a = append(a, "--time="+secs(l.CPUTime))
	}
	if l.WallTime > 0 {
		a = append(a, "--wall-time="+secs(l.WallTime))
	}
	if l.Memory > 0 {
		if b.iso.CG {
			a = append(a, "--cg-mem="+kb(l.Memory))
		} else {
			a = append(a, "--mem="+kb(l.Memory))
		}
	}
	stack := l.Stack
	if stack == 0 {
		stack = l.Memory
	}
	if stack > 0 {
		a = append(a, "--stack="+kb(stack))
	}
	procs := l.Processes
	if procs < 1 {
		procs = 1
	}
	a = append(a, "--processes="+strconv.Itoa(procs))
	if l.FileSize > 0 {
		a = append(a, "--fsize="+kb(l.FileSize))
	}
	if l.OpenFiles > 0 {
		a = append(a, "--open-files="+strconv.Itoa(l.OpenFiles))
	}
	// Unset streams go to /dev/null: an inherited descriptor would let the
	// program write into the worker's own pipes.
	a = append(a, "--stdin="+orDevNull(spec.Stdin), "--stdout="+orDevNull(spec.Stdout))
	if spec.StderrToStdout {
		a = append(a, "--stderr-to-stdout")
	} else {
		a = append(a, "--stderr="+orDevNull(spec.Stderr))
	}
	for _, e := range spec.Env {
		a = append(a, "--env="+e)
	}
	shm := false
	for _, d := range spec.Dirs {
		shm = shm || d.Inside == "/dev/shm"
	}
	if !shm {
		// The host /dev/shm must never be shared (isolate < 2.0 binds it
		// through /dev): give every run a private, writable one.
		a = append(a, "--dir=/dev/shm:tmp")
	}
	for _, d := range spec.Dirs {
		v := d.Inside
		if d.Outside != "" && d.Outside != d.Inside {
			v += "=" + d.Outside
		}
		var opts []string
		if d.RW {
			opts = append(opts, "rw")
		}
		if d.Maybe {
			opts = append(opts, "maybe")
		}
		if d.NoExec {
			opts = append(opts, "noexec")
		}
		if len(opts) > 0 {
			v += ":" + strings.Join(opts, ":")
		}
		a = append(a, "--dir="+v)
	}
	if spec.ShareNet {
		a = append(a, "--share-net")
	}
	a = append(a, "--run", "--")
	return append(a, spec.Args...)
}

// Run executes spec in the box and classifies the outcome. An error is
// returned only for infrastructure failures (the verdict is in Result).
func (b *Box) Run(ctx context.Context, spec *Spec) (*Result, error) {
	if len(spec.Args) == 0 {
		return nil, errors.New("sandbox: empty command")
	}
	os.Truncate(b.metaPath, 0)
	cmd := b.iso.cmd(ctx, b.args(spec)...)
	stderr := &limitedBuffer{max: 4096}
	cmd.Stdout, cmd.Stderr = stderr, stderr
	var cpus []int
	if b.Core >= 0 {
		cpus = []int{b.Core}
	}
	err := startPinnedSet(cmd, cpus)
	if err == nil {
		err = cmd.Wait()
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	meta, merr := readMeta(b.metaPath)
	if merr != nil || len(meta) == 0 {
		return nil, fmt.Errorf("%w: no meta file (isolate: %v: %s)", ErrSandbox, err, bytes.TrimSpace(stderr.buf.Bytes()))
	}
	res := classify(meta, spec.Limits, b.iso.CG)
	if res.Status == StatusSandboxError {
		return res, fmt.Errorf("%w: %s", ErrSandbox, res.Message)
	}
	return res, nil
}

func readMeta(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if ok {
			m[k] = v
		}
	}
	return m, sc.Err()
}

func parseSeconds(s string) time.Duration {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return time.Duration(f * float64(time.Second))
}

// classify turns an isolate meta file into a Result. The order of checks
// matters: resource-limit verdicts take precedence over the raw exit
// status because exceeding a limit often manifests as a crash (e.g. a
// failed allocation leading to SIGSEGV or SIGABRT).
func classify(meta map[string]string, l Limits, cg bool) *Result {
	r := &Result{Meta: meta, Message: meta["message"]}
	r.CPUTime = parseSeconds(meta["time"])
	r.WallTime = parseSeconds(meta["time-wall"])
	if v, err := strconv.ParseInt(meta["cg-mem"], 10, 64); err == nil && cg {
		r.Memory = v * 1024
	} else if v, err := strconv.ParseInt(meta["max-rss"], 10, 64); err == nil {
		r.Memory = v * 1024
	}
	r.ExitCode, _ = strconv.Atoi(meta["exitcode"])
	r.Signal, _ = strconv.Atoi(meta["exitsig"])
	status := meta["status"]

	switch {
	case status == "XX":
		r.Status = StatusSandboxError
	case meta["cg-oom-killed"] == "1":
		r.Status = StatusMemory
	case status == "TO":
		if strings.Contains(r.Message, "wall") && (l.CPUTime == 0 || r.CPUTime < l.CPUTime) {
			r.Status = StatusWallTimeout
		} else {
			r.Status = StatusTimeout
		}
	case l.CPUTime > 0 && r.CPUTime > l.CPUTime:
		r.Status = StatusTimeout
	case status == "SG" && r.Signal == int(syscall.SIGXFSZ):
		r.Status = StatusOutputLimit
	case (status == "SG" || status == "RE") && l.Memory > 0 && r.Memory >= l.Memory*98/100:
		// Died while at the memory ceiling: the allocation failed.
		r.Status = StatusMemory
	case status == "SG":
		r.Status = StatusSignal
	case status == "RE":
		r.Status = StatusNonZero
	default:
		r.Status = StatusOK
	}
	return r
}
