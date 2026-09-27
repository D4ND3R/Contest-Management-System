package alerts

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/hoststat"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
	"github.com/jackc/pgx/v5/pgxpool"
)

// liveQueues are the queues contestants wait on.
var liveQueues = []queue.Priority{queue.PriorityEvaluate, queue.PriorityCompile, queue.PriorityDeferred}

// Sources are what the standard rules read.
type Sources struct {
	Queue *queue.Queue
	// Pool may be nil (the database rules are then left out).
	Pool *pgxpool.Pool
	// Host samples this machine's directories.
	Host *hoststat.Sampler
	Dirs [][2]string
}

// Standard returns the rules of SPEC_IOI §12 with the configured
// thresholds.
func Standard(src Sources, cfg config.Alerts) []Rule {
	rules := []Rule{
		{Name: "queue_backlog", For: 2 * time.Minute, Check: func(ctx context.Context) (bool, string, error) {
			st, err := src.Queue.Stats(ctx)
			if err != nil {
				return false, "", err
			}
			var n int64
			for _, p := range liveQueues {
				n += st.Waiting[p.String()]
			}
			return cfg.QueueDepth > 0 && n > cfg.QueueDepth,
				fmt.Sprintf("%d jobs are waiting to be judged (threshold %d): add judging power or look for a stuck worker", n, cfg.QueueDepth), nil
		}},
		{Name: "disk_space", Check: func(ctx context.Context) (bool, string, error) {
			var low []string
			check := func(where string, disks []hoststat.Disk) {
				for _, d := range disks {
					if d.Total > 0 && 100-d.UsedPercent() < cfg.DiskFreePercent {
						low = append(low, fmt.Sprintf("%s %s (%.0f%% free)", where, d.Name, 100-d.UsedPercent()))
					}
				}
			}
			if src.Host != nil {
				check("server", src.Host.Sample(src.Dirs...).Disks)
			}
			ws, err := src.Queue.Workers(ctx, time.Minute)
			if err != nil {
				return false, "", err
			}
			for _, w := range ws {
				if w.Alive && w.Host != nil {
					check("worker "+w.Name, w.Host.Disks)
				}
			}
			return len(low) > 0, "low disk space: " + strings.Join(low, ", "), nil
		}},
		{Name: "timing_drift", Check: func(ctx context.Context) (bool, string, error) {
			cs, err := src.Queue.Calibrations(ctx)
			if err != nil {
				return false, "", err
			}
			var off []string
			for name, c := range cs {
				if c.Off > 0 {
					off = append(off, fmt.Sprintf("%s (%d cores)", name, c.Off))
				}
			}
			return len(off) > 0, "judging cores measure time differently: " + strings.Join(off, ", ") +
				"; see the Judges page and run cms ctl calibrate again after fixing them", nil
		}},
	}
	if src.Pool == nil {
		return rules
	}
	q := sqlc.New(src.Pool)
	return append(rules,
		Rule{Name: "no_workers", For: 30 * time.Second, Check: func(ctx context.Context) (bool, string, error) {
			n, err := q.RunningContestCount(ctx)
			if err != nil || n == 0 {
				return false, "", err
			}
			ws, err := src.Queue.Workers(ctx, time.Minute)
			if err != nil {
				return false, "", err
			}
			for _, w := range ws {
				if w.Alive {
					return false, "", nil
				}
			}
			return true, "a contest is running and no worker is alive: submissions are not judged", nil
		}},
		Rule{Name: "judging_latency", For: 2 * time.Minute, Check: func(ctx context.Context) (bool, string, error) {
			l, err := q.JudgingLatencyWindow(ctx)
			if err != nil {
				return false, "", err
			}
			limit := cfg.JudgingLatency.D()
			got := time.Duration(l.Median * float64(time.Second))
			return limit > 0 && l.Submissions > 0 && got > limit,
				fmt.Sprintf("results take %s (median of the last 10 minutes, threshold %s)", got.Round(time.Second), limit), nil
		}},
		Rule{Name: "wal_archiving", Check: func(ctx context.Context) (bool, string, error) {
			var mode string
			var failed int64
			var lastFailed, lastArchived *time.Time
			var wal *string
			err := src.Pool.QueryRow(ctx, `SELECT current_setting('archive_mode'), failed_count, last_failed_time, last_archived_time, last_failed_wal
FROM pg_stat_archiver`).Scan(&mode, &failed, &lastFailed, &lastArchived, &wal)
			if err != nil {
				return false, "", err
			}
			if mode == "off" || lastFailed == nil || (lastArchived != nil && lastArchived.After(*lastFailed)) {
				return false, "", nil
			}
			name := ""
			if wal != nil {
				name = *wal
			}
			return true, fmt.Sprintf("PostgreSQL cannot archive its WAL (last failure %s, %s): point-in-time recovery is not possible until it works",
				lastFailed.UTC().Format(time.RFC3339), name), nil
		}},
	)
}
