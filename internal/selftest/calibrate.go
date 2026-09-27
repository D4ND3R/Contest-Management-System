package selftest

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
)

// CoreTime is the calibration benchmark on one judging slot.
type CoreTime struct {
	Slot int `json:"slot"`
	// Times are the CPU times of each run, in seconds; Median their median.
	Times  []float64 `json:"times"`
	Median float64   `json:"median"`
}

// Calibrate compiles the benchmark once and runs it runs times on every
// slot, one slot after the other (the machine otherwise idle), returning
// the CPU times (SPEC_IOI §2.2: a benchmark on every worker before the
// contest).
func (j *Judge) Calibrate(ctx context.Context, runs int) ([]CoreTime, error) {
	if runs < 1 {
		runs = 1
	}
	src, err := Program("calibrate/bench.c")
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{"bench.c": src}
	lim := jobs.Limits{TimeMs: 10000, MemoryBytes: 64 << 20, OutputBytes: 1 << 20, Processes: 1}
	cj, err := j.job(ctx, jobs.KindCompile, "c11", files, lim)
	if err != nil {
		return nil, err
	}
	res := j.Exec.Execute(ctx, 0, cj)
	if res.Error != "" {
		return nil, errors.New(res.Error)
	}
	if res.Compilation == nil || !res.Compilation.Success {
		return nil, errors.New("the benchmark does not compile")
	}
	ej, err := j.job(ctx, jobs.KindEvaluate, "c11", files, lim)
	if err != nil {
		return nil, err
	}
	ej.Executables = res.Compilation.Executables
	tc := jobs.Testcase{ID: 1, Codename: "bench"}
	if tc.Input, err = j.put(ctx, nil); err != nil {
		return nil, err
	}
	// The expected output does not matter: only the time is used.
	if tc.Output, err = j.put(ctx, []byte("0\n")); err != nil {
		return nil, err
	}
	ej.Testcases = []jobs.Testcase{tc}
	var out []CoreTime
	for slot := 0; slot < j.Slots; slot++ {
		ct := CoreTime{Slot: slot}
		for r := 0; r < runs; r++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			res := j.Exec.Execute(ctx, slot, ej)
			if res.Error != "" {
				return nil, fmt.Errorf("slot %d: %s", slot, res.Error)
			}
			if len(res.Evaluations) != 1 || res.Evaluations[0].ExitStatus != "ok" {
				return nil, fmt.Errorf("slot %d: the benchmark did not finish (%+v)", slot, res.Evaluations)
			}
			ct.Times = append(ct.Times, res.Evaluations[0].Time)
		}
		ct.Median = median(ct.Times)
		out = append(out, ct)
	}
	return out, nil
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

// Drift compares the slots: the median of their medians and the slots
// more than tolerance (a fraction, e.g. 0.03) away from it.
func Drift(ts []CoreTime, tolerance float64) (float64, []CoreTime) {
	ms := make([]float64, len(ts))
	for i, t := range ts {
		ms[i] = t.Median
	}
	m := median(ms)
	var off []CoreTime
	for _, t := range ts {
		if m > 0 && math.Abs(t.Median-m)/m > tolerance {
			off = append(off, t)
		}
	}
	return m, off
}

// Spread is the relative spread of one slot's runs (max-min over median):
// a noisy core (another load, frequency changes) shows here.
func Spread(t CoreTime) float64 {
	if len(t.Times) < 2 || t.Median == 0 {
		return 0
	}
	lo, hi := t.Times[0], t.Times[0]
	for _, v := range t.Times {
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	return (hi - lo) / t.Median
}
