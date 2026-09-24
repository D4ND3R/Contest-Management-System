package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/dispatcher"
	"github.com/D4ND3R/Contest-Management-System/internal/monitor"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
)

func init() {
	ctlCommands["reevaluate"] = ctlCommand{"recompile, reevaluate or rescore submissions (by contest/task/dataset/user/submission)", cmdReevaluate}
	ctlCommands["set-live-dataset"] = ctlCommand{"make a dataset the live one of its task (re-aggregates scores)", cmdSetLiveDataset}
	ctlCommands["status"] = ctlCommand{"show queues and workers", cmdStatus}
}

func cmdReevaluate(args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("reevaluate", stderr)
	var sc dispatcher.Scope
	fs.Int64Var(&sc.ContestID, "contest", 0, "contest id")
	fs.Int64Var(&sc.TaskID, "task", 0, "task id")
	fs.Int64Var(&sc.DatasetID, "dataset", 0, "dataset id")
	fs.Int64Var(&sc.UserID, "user", 0, "user id")
	fs.Int64Var(&sc.ParticipationID, "participation", 0, "participation id")
	fs.Int64Var(&sc.SubmissionID, "submission", 0, "submission id")
	level := fs.String("level", "reevaluate", "recompile | reevaluate | rescore")
	if err := fs.Parse(args); err != nil {
		return err
	}
	lv, err := dispatcher.ParseLevel(*level)
	if err != nil {
		return err
	}
	ctx := context.Background()
	env, err := openCtl(ctx, *cfgPath, true, stderr)
	if err != nil {
		return err
	}
	defer env.Close()
	n, err := dispatcher.Invalidate(ctx, env.deps.DB, queue.New(env.deps.Redis, env.cfg.Redis.Namespace), sc, lv)
	fmt.Fprintf(stdout, "%d results invalidated (%s)\n", n, lv)
	return err
}

func cmdSetLiveDataset(args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("set-live-dataset", stderr)
	id := fs.Int64("dataset", 0, "dataset id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == 0 {
		return errors.New("-dataset is required")
	}
	ctx := context.Background()
	env, err := openCtl(ctx, *cfgPath, true, stderr)
	if err != nil {
		return err
	}
	defer env.Close()
	return dispatcher.ChangeLiveDataset(ctx, env.deps.DB, queue.New(env.deps.Redis, env.cfg.Redis.Namespace), *id)
}

func cmdStatus(args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("status", stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	env, err := openCtl(ctx, *cfgPath, true, stderr)
	if err != nil {
		return err
	}
	defer env.Close()
	q := queue.New(env.deps.Redis, env.cfg.Redis.Namespace)
	st, err := q.Stats(ctx)
	if err != nil {
		return err
	}
	ws, err := q.Workers(ctx, time.Hour)
	if err != nil {
		return err
	}
	out := monitor.Stats{Time: time.Now().UTC(), Queues: st, Workers: ws}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
