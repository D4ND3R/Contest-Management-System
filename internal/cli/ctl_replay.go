package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/deps"
	"github.com/D4ND3R/Contest-Management-System/internal/logging"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/D4ND3R/Contest-Management-System/internal/rehearsal"
)

func init() {
	ctlCommands["replay"] = ctlCommand{"rehearsal: replay a past contest's submissions into another contest and measure the judging", cmdReplay}
}

func cmdReplay(args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("replay", stderr)
	from := fs.String("from", "", "contest whose submissions are replayed (e.g. an imported archive)")
	to := fs.String("to", "", "rehearsal contest that receives them (e.g. a copy with its participants)")
	speed := fs.Float64("speed", 1, "how many times faster than the original")
	limit := fs.Int("limit", 0, "replay at most this many submissions (0: all)")
	wait := fs.Duration("wait", 10*time.Minute, "how long to wait for the last results (0: do not wait)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *from == "" || *to == "" {
		return errors.New("usage: cms ctl replay -from <contest> -to <rehearsal contest> [-speed N] [-limit N]")
	}
	ctx := context.Background()
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	d, err := deps.Open(ctx, cfg, logging.Discard(), deps.Need{DB: true, Redis: true})
	if err != nil {
		return err
	}
	defer d.Close()
	last := time.Now()
	rep, err := rehearsal.Replay(ctx, d.DB, queue.New(d.Redis, cfg.Redis.Namespace), *from, *to, rehearsal.Options{Speed: *speed, Limit: *limit,
		Progress: func(sent, total int) {
			if time.Since(last) > 5*time.Second || sent == total {
				last = time.Now()
				fmt.Fprintf(stdout, "%d/%d submissions sent\n", sent, total)
			}
		}})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "sent %d submissions in %s (%d skipped: contestant or task missing in %s)\n", rep.Sent, rep.Took.Round(time.Second), rep.Skipped, *to)
	deadline := time.Now().Add(*wait)
	for {
		l, err := rehearsal.Measure(ctx, d.DB, rep.IDs)
		if err != nil {
			return err
		}
		if l.Pending == 0 || time.Now().After(deadline) {
			fmt.Fprintf(stdout, "judged %d (failed %d, still waiting %d); time to result: median %s, p95 %s, max %s\n",
				l.Scored, l.Failed, l.Pending, l.P50.Round(100*time.Millisecond), l.P95.Round(100*time.Millisecond), l.Max.Round(100*time.Millisecond))
			if l.Pending > 0 || l.Failed > 0 {
				return fmt.Errorf("%d submissions were not judged", l.Pending+l.Failed)
			}
			return nil
		}
		time.Sleep(2 * time.Second)
	}
}
