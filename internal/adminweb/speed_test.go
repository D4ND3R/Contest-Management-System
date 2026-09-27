package adminweb

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/D4ND3R/Contest-Management-System/internal/webtest"
)

// TestShortCircuitOption (SPEC_IOI H4): the dataset form turns the
// short-circuit evaluation of GroupMin/GroupMul subtasks on and off.
func TestShortCircuitOption(t *testing.T) {
	f := newFixture(t)
	b := f.login("all")
	path := fmt.Sprintf("/datasets/%d", f.ds.ID)
	form := url.Values{"description": {"Default"}, "time_limit": {"1"}, "memory_limit_mib": {"256"}, "process_limit": {"1"},
		"task_type": {"Batch"}, "tt_checker": {"white_diff"}, "score_type": {"GroupMin"}, "score_type_params": {`[[100, ".*"]]`},
		"short_circuit": {"on"}}
	code, body := b.Post(path, form)
	webtest.MustOK(t, "save", code, body)
	if ds, _ := f.q.GetDataset(bg, f.ds.ID); !ds.ShortCircuit {
		t.Fatal("short-circuit not saved")
	}
	if _, body := b.Get(path); !strings.Contains(body, `name="short_circuit" checked`) {
		t.Fatal("the form does not show the option on")
	}
	form.Del("short_circuit")
	code, body = b.Post(path, form)
	webtest.MustOK(t, "save", code, body)
	if ds, _ := f.q.GetDataset(bg, f.ds.ID); ds.ShortCircuit {
		t.Fatal("short-circuit not cleared")
	}
}

// TestCalibrationsOnSystemPage (SPEC_IOI H4): the workers' calibrations
// are compared on the Judges page: a core far from its machine's median
// and a machine far from the others are marked.
func TestCalibrationsOnSystemPage(t *testing.T) {
	f := newFixture(t)
	b := f.login("all")
	if _, body := b.Get("/system"); !strings.Contains(body, "No calibration yet.") {
		t.Fatal("empty calibration section missing")
	}
	q := queue.New(f.rdb, f.ns)
	now := time.Now()
	q.SaveCalibration(bg, queue.Calibration{Worker: "judge-a", Hostname: "a", At: now, Median: 0.500, Slots: []float64{0.500, 0.501, 0.560},
		Cores: []int{1, 2, 3}, Tolerance: 0.03, Off: 1})
	q.SaveCalibration(bg, queue.Calibration{Worker: "judge-b", Hostname: "b", At: now, Median: 0.550, Slots: []float64{0.550, 0.551},
		Cores: []int{1, 2}, Tolerance: 0.03})
	q.SaveCalibration(bg, queue.Calibration{Worker: "judge-c", Hostname: "c", At: now, Median: 0.501, Slots: []float64{0.501},
		Cores: []int{1}, Tolerance: 0.03})
	_, body := b.Get("/system")
	for _, w := range []string{"judge-a", "judge-b", `<span class="tag bad" title="0.560 s">CPU 3 &#43;12.0%</span>`,
		`<span class="tag" title="0.501 s">CPU 2 &#43;0.2%</span>`, "1 cores differ by more than 3% from the machine&#39;s median",
		`<td class="num bad">&#43;9.8%</td>`, "the slowest takes 10.0% longer than the fastest"} {
		if !strings.Contains(body, w) {
			t.Errorf("system page lacks %s", w)
		}
	}
	if t.Failed() {
		i := strings.Index(body, "Calibration")
		t.Fatalf("%s", body[i:min(len(body), i+3000)])
	}
}
