package selftest

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestCompare: a security case stopped by different limits in two runs
// (a fork bomb: time, then memory) held both times and is no change; a
// security case that failed, or a sample whose verdict moved, is.
func TestCompare(t *testing.T) {
	sec := func(name, got string, want ...string) Result {
		return Result{Group: "security", Name: name, Got: got, Want: want}
	}
	smp := func(name, got string) Result {
		return Result{Group: "samples", Name: name, Got: got, Want: []string{"TLE"}}
	}
	first := []Result{
		sec("fork_bomb_64_procs", "timeout", contained...),
		sec("stack_overflow", "signal", "signal", "memory"),
		sec("network", "ok", "ok"),
		smp("c11/tle", "timeout"),
		smp("cpp17/tle", "timeout"),
	}
	second := []Result{
		sec("fork_bomb_64_procs", "memory", contained...),
		sec("stack_overflow", "memory", "signal", "memory"),
		{Group: "security", Name: "network", Got: "ok", Want: []string{"ok"}, Problems: []string{"network reachable"}},
		smp("c11/tle", "timeout_wall"),
		smp("cpp17/tle", "memory"),
	}
	got := Compare(first, second)
	want := []string{"network: ok, then ok (not contained)", "cpp17/tle: timeout, then memory"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Compare = %q, want %q", got, want)
	}
	if r := sec("fork_bomb", "ok", contained...); r.OK() {
		t.Fatal("a fork bomb that ended normally passed")
	}
}

// TestSharedCores: hyperthread siblings among the judging CPUs are found
// in both sysfs list formats.
func TestSharedCores(t *testing.T) {
	dir := t.TempDir()
	write := func(cpu int, list string) {
		d := filepath.Join(dir, "cpu"+itoa(cpu), "topology")
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "thread_siblings_list"), []byte(list+"\n"), 0o644)
	}
	for i := 0; i < 8; i++ {
		write(i, itoa(i%4)+","+itoa(i%4+4)) // Intel style: 0,4 1,5 ...
	}
	if got := SharedCores(dir, []int{1, 2, 3}); len(got) != 0 {
		t.Fatalf("one CPU per core: %v", got)
	}
	if got, want := SharedCores(dir, []int{2, 3, 4, 5, 6, 7}), [][2]int{{2, 6}, {3, 7}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("siblings: %v, want %v", got, want)
	}
	for i := 0; i < 4; i++ {
		write(i, "0-1") // ranges
	}
	if got, want := SharedCores(dir, []int{0, 1}), [][2]int{{0, 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("range format: %v, want %v", got, want)
	}
}

func itoa(i int) string { return string(rune('0' + i)) }
