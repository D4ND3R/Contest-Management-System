package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/deps"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/logging"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/D4ND3R/Contest-Management-System/internal/sandbox"
	"github.com/D4ND3R/Contest-Management-System/internal/selftest"
	"github.com/D4ND3R/Contest-Management-System/internal/worker"
)

func init() {
	ctlCommands["calibrate"] = ctlCommand{"time a benchmark on every judging core of this host and compare them (and with the other workers)", cmdCalibrate}
}

func cmdCalibrate(args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("calibrate", stderr)
	runs := fs.Int("runs", 5, "runs per core (the median counts)")
	offset := fs.Int("box-offset", 500, "first isolate box id to use (must not overlap a running worker's boxes)")
	tolerance := fs.Float64("tolerance", 0.03, "relative difference from the median that is reported")
	publish := fs.Bool("publish", true, "store the result for the admin Judges page (needs Redis)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	reg, err := langs.Load(cfg.LanguagesDir)
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	name := cfg.Worker.Name
	if name == "" {
		name = host + "-" + strconv.Itoa(cfg.Worker.BoxIDOffset) // as the worker names itself
	}
	tmp, err := os.MkdirTemp("", "cms-calibrate-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	wc := cfg.Worker
	wc.Name, wc.BoxIDOffset, wc.WorkDir, wc.CacheDir = "calibrate", *offset, tmp+"/work", tmp+"/cache"
	if len(wc.Cores) == 0 {
		wc.Cores = sandbox.DefaultCores()
	}
	store := blob.NewMem()
	exec, err := worker.NewExecutor(wc, store, logging.Discard())
	if err != nil {
		return err
	}
	defer exec.Close()
	j := &selftest.Judge{Exec: exec, Store: store, Langs: reg, Slots: len(exec.Slots)}
	fmt.Fprintf(stdout, "calibration of %s: cores %v, %d runs each (stop the worker first: its jobs disturb the times)\n", name, wc.Cores, *runs)
	ctx := context.Background()
	ts, err := j.Calibrate(ctx, *runs)
	if err != nil {
		return err
	}
	med, off := selftest.Drift(ts, *tolerance)
	isOff := map[int]bool{}
	for _, t := range off {
		isOff[t.Slot] = true
	}
	for _, t := range ts {
		mark := "OK  "
		if isOff[t.Slot] {
			mark = "SLOW"
			if t.Median < med {
				mark = "FAST"
			}
		}
		note := ""
		if sp := selftest.Spread(t); sp > 2**tolerance {
			note = " noisy: something else runs on this core, or its frequency changes"
		}
		fmt.Fprintf(stdout, "  [%s] core %-3d %.3fs (%+.1f%%, runs spread %.1f%%)%s\n", mark, wc.Cores[t.Slot], t.Median,
			100*(t.Median-med)/med, 100*selftest.Spread(t), note)
	}
	fmt.Fprintf(stdout, "median %.3fs; %d of %d cores more than %.0f%% away\n", med, len(off), len(ts), 100**tolerance)
	if len(off) > 0 {
		fmt.Fprintln(stdout, "WARNING: cores that differ judge the same program in different times; check turbo, the governor and SMT (sudo cms-host-tuning enable), or remove them from worker.cores")
	}
	if !*publish {
		return nil
	}
	d, err := deps.Open(ctx, cfg, logging.Discard(), deps.Need{Redis: true})
	if err != nil {
		return fmt.Errorf("publish (use -publish=false to skip): %w", err)
	}
	defer d.Close()
	c := queue.Calibration{Worker: name, Hostname: host, At: time.Now().UTC(), Median: med, Off: len(off),
		Cores: wc.Cores, Tolerance: *tolerance}
	for _, t := range ts {
		c.Slots = append(c.Slots, t.Median)
	}
	if err := queue.New(d.Redis, cfg.Redis.Namespace).SaveCalibration(ctx, c); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "stored for the admin Judges page")
	return nil
}
