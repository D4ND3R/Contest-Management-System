package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// startPinnedSet starts cmd with its CPU affinity restricted to cpus (all
// CPUs when empty). Affinity is inherited across fork/exec, so isolate and
// the sandboxed program run on those CPUs only. The calling goroutine is
// locked to its OS thread while the thread's affinity is temporarily changed.
func startPinnedSet(cmd *exec.Cmd, cpus []int) error {
	if len(cpus) == 0 {
		return cmd.Start()
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var old, set unix.CPUSet
	if err := unix.SchedGetaffinity(0, &old); err != nil {
		return cmd.Start()
	}
	set.Zero()
	for _, c := range cpus {
		set.Set(c)
	}
	if err := unix.SchedSetaffinity(0, &set); err != nil {
		return cmd.Start()
	}
	err := cmd.Start()
	_ = unix.SchedSetaffinity(0, &old)
	return err
}

// PhysicalCores returns one logical CPU id per physical core (the lowest
// sibling), in ascending order. Hyperthread siblings share execution units
// and would make timings noisy, so only one box runs per physical core.
func PhysicalCores() []int {
	var allowed unix.CPUSet
	_ = unix.SchedGetaffinity(0, &allowed)
	paths, _ := filepath.Glob("/sys/devices/system/cpu/cpu[0-9]*")
	seen := map[string]bool{}
	var cores []int
	for _, p := range paths {
		id, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(p), "cpu"))
		if err != nil || (allowed.Count() > 0 && !allowed.IsSet(id)) {
			continue
		}
		if online, err := os.ReadFile(filepath.Join(p, "online")); err == nil && strings.TrimSpace(string(online)) == "0" {
			continue
		}
		sib, err := os.ReadFile(filepath.Join(p, "topology", "thread_siblings_list"))
		key := strings.TrimSpace(string(sib))
		if err != nil || key == "" {
			key = strconv.Itoa(id)
		}
		pkg, _ := os.ReadFile(filepath.Join(p, "topology", "physical_package_id"))
		key = strings.TrimSpace(string(pkg)) + "/" + key
		if seen[key] {
			continue
		}
		seen[key] = true
		cores = append(cores, id)
	}
	sort.Ints(cores)
	if len(cores) == 0 {
		for i := 0; i < runtime.NumCPU(); i++ {
			cores = append(cores, i)
		}
	}
	return cores
}

// DefaultCores picks the cores used for boxes when none are configured:
// every physical core except the first one, which is left to the worker
// process, the kernel and I/O (when there are at least 3 physical cores).
func DefaultCores() []int {
	c := PhysicalCores()
	if len(c) >= 3 {
		return c[1:]
	}
	return c
}

// Complement returns the CPUs allowed for this process that are not in used
// (the maintenance CPUs when used are the judging cores).
func Complement(used []int) []int {
	var allowed unix.CPUSet
	if err := unix.SchedGetaffinity(0, &allowed); err != nil {
		return nil
	}
	in := map[int]bool{}
	for _, c := range used {
		in[c] = true
	}
	var out []int
	for c := 0; c < 1024; c++ {
		if allowed.IsSet(c) && !in[c] {
			out = append(out, c)
		}
	}
	return out
}
