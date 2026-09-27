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
)

func init() {
	ctlCommands["queue-drain"] = ctlCommand{"empty the judging queues (after restoring the database to an earlier moment)", cmdQueueDrain}
}

func cmdQueueDrain(args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("queue-drain", stderr)
	force := fs.Bool("force", false, "drain even though workers are running")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	d, err := deps.Open(ctx, cfg, logging.Discard(), deps.Need{Redis: true})
	if err != nil {
		return err
	}
	defer d.Close()
	q := queue.New(d.Redis, cfg.Redis.Namespace)
	ws, err := q.Workers(ctx, time.Minute)
	if err != nil {
		return err
	}
	for _, w := range ws {
		if w.Alive && !*force {
			return errors.New("workers are running: stop the CMS services first (systemctl stop cms.target, and the workers), or use -force")
		}
	}
	n, err := q.Drain(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "drained (%d keys); when the dispatcher starts it enqueues again whatever the database still needs judged\n", n)
	return nil
}
