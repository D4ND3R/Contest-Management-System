package hoststat

import (
	"runtime"
	"testing"
	"time"
)

func TestSample(t *testing.T) {
	var s Sampler
	st := s.Sample([2]string{"temp", t.TempDir()}, [2]string{"none", ""}, [2]string{"missing", "/does/not/exist"})
	if st.CPUs != runtime.NumCPU() {
		t.Fatalf("cpus %d", st.CPUs)
	}
	if runtime.GOOS != "linux" {
		t.Skip("figures need /proc")
	}
	if st.MemTotal <= 0 || st.MemUsed <= 0 || st.MemUsed > st.MemTotal || st.MemPercent() <= 0 {
		t.Fatalf("memory %d/%d", st.MemUsed, st.MemTotal)
	}
	if len(st.Disks) != 1 || st.Disks[0].Name != "temp" || st.Disks[0].Total <= 0 || st.Disks[0].Free > st.Disks[0].Total {
		t.Fatalf("disks %+v", st.Disks)
	}
	// Burn a little CPU: the second sample measures it.
	end := time.Now().Add(50 * time.Millisecond)
	for time.Now().Before(end) {
	}
	st = s.Sample()
	if st.CPUPercent <= 0 || st.CPUPercent > 100 {
		t.Fatalf("cpu %v%%", st.CPUPercent)
	}
}
