package printing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/events"
	"github.com/D4ND3R/Contest-Management-System/internal/metrics"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

var printed = metrics.NewCounterVec(prometheus.CounterOpts{Name: "cms_print_jobs_total",
	Help: "Print jobs handled by the printing service, by outcome."}, []string{"status"})

// Service prints queued jobs one at a time (there is one printing service
// per installation; it takes back the jobs it was printing when it starts).
type Service struct {
	q     *sqlc.Queries
	rdb   *redis.Client
	ns    string
	store blob.Store
	cfg   config.Printing
	log   *slog.Logger
	// Idle is how long to wait for a job when no event arrives.
	Idle time.Duration
	// Attempts at running lp before a job fails.
	Attempts int
	backoff  time.Duration
}

// New returns a print service.
func New(q *sqlc.Queries, rdb *redis.Client, ns string, store blob.Store, cfg config.Printing, log *slog.Logger) *Service {
	return &Service{q: q, rdb: rdb, ns: ns, store: store, cfg: cfg, log: log, Idle: 5 * time.Second, Attempts: 3, backoff: 2 * time.Second}
}

// Run prints jobs until ctx ends.
func (s *Service) Run(ctx context.Context) error {
	if ids, err := s.q.RequeueInterruptedPrintJobs(ctx); err != nil {
		return err
	} else if len(ids) > 0 {
		s.log.Warn("print jobs interrupted by a restart were queued again", "jobs", ids)
	}
	if s.cfg.Printer == "" {
		s.log.Warn("no printer configured: jobs are marked done without printing")
	}
	wake := make(chan struct{}, 1)
	go func() {
		for ctx.Err() == nil {
			err := events.Subscribe(ctx, s.rdb, s.ns, func(e events.Event) {
				if e.Type == events.TypePrint && e.Status == "queued" {
					select {
					case wake <- struct{}{}:
					default:
					}
				}
			})
			if ctx.Err() == nil {
				s.log.Warn("event subscription ended; retrying", "error", err)
				time.Sleep(time.Second)
			}
		}
	}()
	for {
		job, err := s.q.ClaimPrintJob(ctx)
		switch {
		case ctx.Err() != nil:
			return nil
		case errors.Is(err, pgx.ErrNoRows):
			select {
			case <-ctx.Done():
				return nil
			case <-wake:
			case <-time.After(s.Idle):
			}
			continue
		case err != nil:
			s.log.Error("claim print job", "error", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(2 * time.Second):
			}
			continue
		}
		s.handle(ctx, job)
	}
}

// handle prints one claimed job and records the outcome.
func (s *Service) handle(ctx context.Context, job sqlc.PrintJob) {
	status, text := "done", ""
	info, err := s.q.GetPrintJobInfo(ctx, job.ID)
	if err == nil {
		text, err = s.print(ctx, info)
	}
	if err != nil {
		if ctx.Err() != nil {
			return // requeued at the next start
		}
		status, text = "failed", err.Error()
		s.log.Error("print job failed", "job", job.ID, "error", err)
	} else {
		s.log.Info("print job printed", "job", job.ID, "pages", derefInt(job.Pages), "printer", s.cfg.Printer)
	}
	if len(text) > 500 {
		text = text[:500]
	}
	if err := s.q.FinishPrintJob(ctx, sqlc.FinishPrintJobParams{ID: job.ID, Status: status, StatusText: text}); err != nil {
		s.log.Error("finish print job", "job", job.ID, "error", err)
		return
	}
	printed.WithLabelValues(status).Inc()
	events.Publish(ctx, s.rdb, s.ns, events.Event{Type: events.TypePrint, ContestID: info.ContestID,
		ParticipationID: job.ParticipationID, Status: status})
}

// print sends the banner page and the document to the printer and returns
// what lp answered.
func (s *Service) print(ctx context.Context, j sqlc.GetPrintJobInfoRow) (string, error) {
	doc, err := blob.ReadAll(ctx, s.store, j.Digest)
	if err != nil {
		return "", fmt.Errorf("read the document: %w", err)
	}
	banner := Banner(BannerInfo{JobID: j.ID, Username: j.Username, Name: strings.TrimSpace(j.FirstName + " " + j.LastName),
		Team: deref(j.TeamCode), Site: deref(j.SiteName), Contest: j.ContestName, Filename: j.Filename,
		Pages: derefInt(j.Pages), Submitted: j.CreatedAt})
	if s.cfg.MaxPages > 0 && derefInt(j.Pages) > s.cfg.MaxPages {
		return "", fmt.Errorf("the printer takes at most %d pages per job", s.cfg.MaxPages)
	}
	if s.cfg.Printer == "" {
		return "not printed: no printer configured", nil
	}
	dir, err := os.MkdirTemp("", "cms-print-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	bannerPath, docPath := filepath.Join(dir, "banner.pdf"), filepath.Join(dir, "document.pdf")
	if err := os.WriteFile(bannerPath, banner, 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(docPath, doc, 0o600); err != nil {
		return "", err
	}
	lp := s.cfg.LPPath
	if lp == "" {
		lp = "lp"
	}
	args := []string{"-d", s.cfg.Printer, "-t", fmt.Sprintf("cms-%d-%s", j.ID, j.Username)}
	if s.cfg.PaperSize != "" {
		args = append(args, "-o", "media="+s.cfg.PaperSize, "-o", "fit-to-page")
	}
	args = append(args, "--", bannerPath, docPath)
	var out []byte
	for attempt := 1; ; attempt++ {
		cctx, cancel := context.WithTimeout(ctx, time.Minute)
		out, err = exec.CommandContext(cctx, lp, args...).CombinedOutput()
		cancel()
		if err == nil || attempt >= s.Attempts || ctx.Err() != nil {
			break
		}
		s.log.Warn("lp failed; retrying", "job", j.ID, "attempt", attempt, "error", err, "output", string(out))
		select {
		case <-ctx.Done():
		case <-time.After(s.backoff * time.Duration(attempt)):
		}
	}
	msg := strings.TrimSpace(string(out))
	if err != nil {
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New("lp: " + msg)
	}
	return msg, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefInt(v *int32) int {
	if v == nil {
		return 0
	}
	return int(*v)
}
