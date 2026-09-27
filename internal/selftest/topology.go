package selftest

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SharedCores lists the judging CPUs that share a physical core with
// another judging CPU (hyperthread siblings): each one's running times
// then depend on what its sibling runs. sysfs is /sys/devices/system/cpu
// (tests pass a copy). CPUs without topology information are skipped.
func SharedCores(sysfs string, cores []int) [][2]int {
	in := map[int]bool{}
	for _, c := range cores {
		in[c] = true
	}
	var out [][2]int
	seen := map[[2]int]bool{}
	for _, c := range cores {
		for _, s := range siblings(sysfs, c) {
			if s == c || !in[s] {
				continue
			}
			p := [2]int{min(c, s), max(c, s)}
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// siblings reads a CPU's thread_siblings_list ("0,4" or "0-1").
func siblings(sysfs string, cpu int) []int {
	b, err := os.ReadFile(filepath.Join(sysfs, fmt.Sprintf("cpu%d", cpu), "topology", "thread_siblings_list"))
	if err != nil {
		return nil
	}
	var out []int
	for _, part := range strings.Split(strings.TrimSpace(string(b)), ",") {
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(lo)
		if err != nil {
			continue
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(hi); err != nil {
				continue
			}
		}
		for i := a; i <= b; i++ {
			out = append(out, i)
		}
	}
	return out
}
