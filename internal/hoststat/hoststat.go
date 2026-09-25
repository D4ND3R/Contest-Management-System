// Package hoststat reads a machine's load for the admin's system panel:
// CPU use, load average, memory and the free space of some directories.
// It reads /proc and statfs (Linux); elsewhere the figures stay zero.
package hoststat

import (
	"bufio"
	"bytes"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// Stats is a snapshot of a machine.
type Stats struct {
	CPUs int `json:"cpus"`
	// CPUPercent is the busy share of all CPUs since the previous sample
	// of the same Sampler (0–100).
	CPUPercent float64 `json:"cpu_percent"`
	Load1      float64 `json:"load1"`
	Load5      float64 `json:"load5"`
	MemTotal   int64   `json:"mem_total"`
	MemUsed    int64   `json:"mem_used"` // total minus available
	Disks      []Disk  `json:"disks,omitempty"`
}

// MemPercent is the used share of memory.
func (s Stats) MemPercent() float64 {
	if s.MemTotal == 0 {
		return 0
	}
	return 100 * float64(s.MemUsed) / float64(s.MemTotal)
}

// Disk is the space of the file system holding a directory.
type Disk struct {
	Name  string `json:"name"` // what the directory holds
	Path  string `json:"path"`
	Total int64  `json:"total"`
	Free  int64  `json:"free"` // available to unprivileged users
}

// UsedPercent is the used share of the file system.
func (d Disk) UsedPercent() float64 {
	if d.Total == 0 {
		return 0
	}
	return 100 * float64(d.Total-d.Free) / float64(d.Total)
}

// Sampler takes snapshots; CPU use is measured between two of them.
type Sampler struct {
	mu                sync.Mutex
	prevBusy, prevAll uint64
}

// Sample reads the machine now; dirs maps a name to a directory whose
// file system is reported.
func (s *Sampler) Sample(dirs ...[2]string) Stats {
	st := Stats{CPUs: runtime.NumCPU()}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		f := strings.Fields(string(b))
		if len(f) >= 2 {
			st.Load1, _ = strconv.ParseFloat(f[0], 64)
			st.Load5, _ = strconv.ParseFloat(f[1], 64)
		}
	}
	st.MemTotal, st.MemUsed = memory()
	if busy, all, ok := cpuTimes(); ok {
		s.mu.Lock()
		if all > s.prevAll && s.prevAll != 0 {
			st.CPUPercent = 100 * float64(busy-s.prevBusy) / float64(all-s.prevAll)
		}
		s.prevBusy, s.prevAll = busy, all
		s.mu.Unlock()
	}
	for _, d := range dirs {
		if d[1] == "" {
			continue
		}
		var fs syscall.Statfs_t
		if syscall.Statfs(d[1], &fs) == nil {
			st.Disks = append(st.Disks, Disk{Name: d[0], Path: d[1], Total: int64(fs.Blocks) * int64(fs.Bsize),
				Free: int64(fs.Bavail) * int64(fs.Bsize)})
		}
	}
	return st
}

// cpuTimes returns the busy and total jiffies of all CPUs.
func cpuTimes() (busy, all uint64, ok bool) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	line, _, _ := bytes.Cut(b, []byte("\n"))
	f := strings.Fields(string(line))
	if len(f) < 5 || f[0] != "cpu" {
		return 0, 0, false
	}
	var idle uint64
	for i, v := range f[1:] {
		n, _ := strconv.ParseUint(v, 10, 64)
		all += n
		if i == 3 || i == 4 { // idle, iowait
			idle += n
		}
	}
	return all - idle, all, true
}

// memory returns the total and used (total minus available) bytes.
func memory() (total, used int64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	var avail int64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		n, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(v), " kB"), 10, 64)
		switch k {
		case "MemTotal":
			total = n << 10
		case "MemAvailable":
			avail = n << 10
		}
	}
	return total, total - avail
}
