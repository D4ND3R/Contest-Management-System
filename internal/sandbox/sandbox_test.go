package sandbox

import (
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	lim := Limits{CPUTime: time.Second, WallTime: 3 * time.Second, Memory: 64 << 20}
	cases := []struct {
		name string
		meta map[string]string
		cg   bool
		want Status
	}{
		{"ok", map[string]string{"time": "0.010", "time-wall": "0.020", "cg-mem": "1000", "exitcode": "0"}, true, StatusOK},
		{"nonzero", map[string]string{"status": "RE", "exitcode": "3", "cg-mem": "1000"}, true, StatusNonZero},
		{"segv", map[string]string{"status": "SG", "exitsig": "11", "cg-mem": "1000"}, true, StatusSignal},
		{"cpu timeout", map[string]string{"status": "TO", "time": "1.001", "message": "Time limit exceeded"}, true, StatusTimeout},
		{"wall timeout", map[string]string{"status": "TO", "time": "0.001", "time-wall": "3.0", "message": "Time limit exceeded (wall clock)"}, true, StatusWallTimeout},
		{"wall kill but cpu over", map[string]string{"status": "TO", "time": "1.5", "message": "Time limit exceeded (wall clock)"}, true, StatusTimeout},
		{"cpu over without TO", map[string]string{"time": "1.2", "exitcode": "0"}, true, StatusTimeout},
		{"oom killed", map[string]string{"status": "SG", "exitsig": "9", "cg-oom-killed": "1", "cg-mem": "65536"}, true, StatusMemory},
		{"abort at ceiling", map[string]string{"status": "SG", "exitsig": "6", "cg-mem": "65500"}, true, StatusMemory},
		{"rss at ceiling no cg", map[string]string{"status": "RE", "exitcode": "1", "max-rss": "65536"}, false, StatusMemory},
		{"xfsz", map[string]string{"status": "SG", "exitsig": "25"}, true, StatusOutputLimit},
		{"internal", map[string]string{"status": "XX", "message": "boom"}, true, StatusSandboxError},
	}
	for _, c := range cases {
		r := classify(c.meta, lim, c.cg)
		if r.Status != c.want {
			t.Errorf("%s: status = %s, want %s", c.name, r.Status, c.want)
		}
	}
	r := classify(map[string]string{"time": "0.250", "time-wall": "0.500", "cg-mem": "2048", "max-rss": "9", "exitcode": "0"}, lim, true)
	if r.CPUTime != 250*time.Millisecond || r.WallTime != 500*time.Millisecond || r.Memory != 2048*1024 {
		t.Fatalf("parsed %+v", r)
	}
	if r := classify(map[string]string{"status": "SG", "exitsig": "11", "max-rss": "4"}, lim, false); r.Signal != int(syscall.SIGSEGV) || r.Memory != 4096 {
		t.Fatalf("no-cg parse %+v", r)
	}
}

func TestArgs(t *testing.T) {
	b := &Box{ID: 7, iso: &Isolate{CG: true}, metaPath: "/tmp/m"}
	spec := &Spec{
		Args:  []string{"./a", "x"},
		Stdin: "in.txt", Stdout: "out.txt", Stderr: "err.txt",
		Env:    []string{"PATH=/usr/bin"},
		Dirs:   []Dir{{Inside: "/etc/alternatives", Maybe: true}, {Inside: "/fifo", Outside: "/tmp/f", RW: true}},
		Limits: Limits{CPUTime: 1500 * time.Millisecond, WallTime: 3 * time.Second, Memory: 256 << 20, FileSize: 1 << 20, Processes: 4},
	}
	got := strings.Join(b.args(spec), " ")
	for _, want := range []string{"--box-id=7", "--meta=/tmp/m", "--time=1.500", "--wall-time=3.000", "--cg-mem=262144",
		"--stack=262144", "--processes=4", "--fsize=1024", "--stdin=in.txt", "--stdout=out.txt", "--stderr=err.txt",
		"--env=PATH=/usr/bin", "--dir=/etc/alternatives:maybe", "--dir=/fifo=/tmp/f:rw", "--run -- ./a x"} {
		if !strings.Contains(got, want) {
			t.Errorf("args %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "--share-net") {
		t.Error("network must be isolated by default")
	}
	b.iso.CG = false
	if got := strings.Join(b.args(&Spec{Args: []string{"x"}, Limits: Limits{Memory: 1 << 20}}), " "); !strings.Contains(got, "--mem=1024") || !strings.Contains(got, "--processes=1") ||
		!strings.Contains(got, "--stdin=/dev/null") || !strings.Contains(got, "--stdout=/dev/null") || !strings.Contains(got, "--stderr=/dev/null") || !strings.Contains(got, "--dir=/dev/shm:tmp") {
		t.Errorf("no-cg args %q", got)
	}
}

func TestCheckName(t *testing.T) {
	for _, ok := range []string{"a", "a.txt", "dir/b"} {
		if checkName(ok) != nil {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"", "/etc/passwd", "../x", "a/../../b", "./a"} {
		if checkName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestPhysicalCores(t *testing.T) {
	c := PhysicalCores()
	if len(c) == 0 {
		t.Fatal("no cores detected")
	}
	for i := 1; i < len(c); i++ {
		if c[i] <= c[i-1] {
			t.Fatalf("cores not sorted/unique: %v", c)
		}
	}
	if d := DefaultCores(); len(d) == 0 || len(d) > len(c) {
		t.Fatalf("default cores %v from %v", d, c)
	}
}
