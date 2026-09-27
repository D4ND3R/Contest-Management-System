package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/logging"
	"github.com/D4ND3R/Contest-Management-System/internal/sandbox"
	"github.com/D4ND3R/Contest-Management-System/internal/selftest"
	"github.com/D4ND3R/Contest-Management-System/internal/worker"
)

func init() {
	ctlCommands["judge-selftest"] = ctlCommand{"judge the security battery and sample solutions on this host (twice, verdicts must match)", cmdJudgeSelftest}
}

func cmdJudgeSelftest(args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("judge-selftest", stderr)
	runs := fs.Int("runs", 2, "how many times everything is judged (verdicts must be identical)")
	only := fs.String("languages", "", "comma-separated languages for the sample solutions (default: all with a toolchain)")
	offset := fs.Int("box-offset", 500, "first isolate box id to use (must not overlap a running worker's boxes)")
	firstUID := fs.Int("first-uid", 60000, "first_uid of the isolate configuration (leftover process check)")
	security := fs.Bool("security", true, "run the security battery")
	samples := fs.Bool("samples", true, "judge the sample solutions")
	requireAll := fs.Bool("require-all-languages", false, "fail when a language with samples has no toolchain")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *runs < 1 {
		return errors.New("-runs must be at least 1")
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	reg, err := langs.Load(cfg.LanguagesDir)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "cms-selftest-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	wc := cfg.Worker
	wc.Name, wc.BoxIDOffset, wc.WorkDir, wc.CacheDir = "selftest", *offset, tmp+"/work", tmp+"/cache"
	if len(wc.Cores) == 0 {
		wc.Cores = sandbox.DefaultCores()
	}
	store := blob.NewMem()
	exec, err := worker.NewExecutor(wc, store, logging.Discard())
	if err != nil {
		return err
	}
	defer exec.Close()
	boxes := len(exec.Slots) * 2 * sandbox.BoxesPerSlot
	j := &selftest.Judge{Exec: exec, Store: store, Langs: reg, Slots: len(exec.Slots),
		UIDs: [2]int{*firstUID + *offset, *firstUID + *offset + boxes - 1}}
	languages := selftest.SampleLanguages()
	if *only != "" {
		languages = splitCSV(*only)
	}
	fmt.Fprintf(stdout, "judge self-test: cores %v, isolate %s (control groups %v), boxes %d-%d\n",
		wc.Cores, wc.IsolatePath, wc.IsolateCG, *offset, *offset+boxes-1)
	if j.Seccomp() {
		fmt.Fprintln(stdout, "seccomp filter: on (forbidden system calls end in a security violation)")
	} else {
		fmt.Fprintln(stdout, "WARNING: the seccomp filter is off: programs are confined by isolate alone and its checks are skipped; "+
			"install a C compiler (gcc) and restart the worker, or see worker.seccomp")
	}
	for _, p := range selftest.SharedCores("/sys/devices/system/cpu", wc.Cores) {
		fmt.Fprintf(stdout, "WARNING: judging CPUs %d and %d are hyperthreads of one physical core: each slows the other down; "+
			"keep one CPU per core in worker.cores (the installer does)\n", p[0], p[1])
	}

	ctx := context.Background()
	var problems []string
	var prev []selftest.Result
	var skipped []string
	for run := 1; run <= *runs; run++ {
		fmt.Fprintf(stdout, "\nrun %d of %d\n", run, *runs)
		start := time.Now()
		var results []selftest.Result
		if *security {
			rs, err := j.RunBattery(ctx)
			if err != nil {
				return err
			}
			results = append(results, rs...)
		}
		if *samples {
			rs, sk, err := j.RunSamples(ctx, languages)
			if err != nil {
				return err
			}
			results, skipped = append(results, rs...), sk
		}
		for _, r := range results {
			mark := "OK  "
			if !r.OK() {
				mark = "FAIL"
				why := fmt.Sprintf("want %s", strings.Join(r.Want, " or "))
				if len(r.Problems) > 0 {
					why += "; " + strings.Join(r.Problems, "; ")
				}
				problems = append(problems, fmt.Sprintf("run %d: %s %s got %s (%s)", run, r.Group, r.Name, r.Got, why))
			}
			fmt.Fprintf(stdout, "  [%s] %-8s %-24s %-14s %s\n", mark, r.Group, r.Name, r.Got, r.Detail)
			for _, p := range r.Problems {
				fmt.Fprintf(stdout, "         %s\n", p)
			}
		}
		fmt.Fprintf(stdout, "  %d programs in %.1fs\n", len(results), time.Since(start).Seconds())
		if prev != nil {
			for _, d := range selftest.Compare(prev, results) {
				problems = append(problems, "verdict changed between runs: "+d)
			}
		}
		prev = results
	}
	if len(skipped) > 0 {
		fmt.Fprintf(stdout, "\nlanguages without a toolchain on this host (not tested): %s\n", strings.Join(skipped, ", "))
		if *requireAll {
			problems = append(problems, "toolchains missing: "+strings.Join(skipped, ", "))
		}
	}
	if len(problems) > 0 {
		fmt.Fprintf(stdout, "\nRESULT: FAIL (%d problems)\n", len(problems))
		for _, p := range problems {
			fmt.Fprintf(stdout, "  - %s\n", p)
		}
		return fmt.Errorf("judge self-test failed (%d problems)", len(problems))
	}
	fmt.Fprintf(stdout, "\nRESULT: OK (%d runs, identical verdicts)\n", *runs)
	return nil
}
