package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/contestconfig"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/deps"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/logging"
	"github.com/D4ND3R/Contest-Management-System/internal/problempkg"
	"github.com/D4ND3R/Contest-Management-System/internal/queue"
)

func init() {
	ctlCommands["contest-config"] = ctlCommand{"keep a contest's configuration in Git: export DIR / apply DIR", cmdContestConfig}
}

const contestConfigUsage = `usage:
  cms ctl contest-config export -contest NAME DIR   write contest.yaml and tasks/<name>/ to DIR
  cms ctl contest-config apply [-dry-run] [-activate] DIR
      make the database match DIR (idempotent: nothing changes the second time)`

func cmdContestConfig(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || (args[0] != "export" && args[0] != "apply") {
		fmt.Fprintln(stderr, contestConfigUsage)
		return errors.New("contest-config needs export or apply")
	}
	sub := args[0]
	fs, cfgPath := newFlags("contest-config "+sub, stderr)
	contest := fs.String("contest", "", "export: the contest")
	dry := fs.Bool("dry-run", false, "apply: only show what would change")
	activate := fs.Bool("activate", false, "apply: make changed tasks' new datasets live even though the contest has started")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 1 || (sub == "export" && *contest == "") {
		fmt.Fprintln(stderr, contestConfigUsage)
		return errors.New("wrong arguments")
	}
	dir := fs.Arg(0)
	ctx := context.Background()
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	d, err := deps.Open(ctx, cfg, logging.Discard(), deps.Need{DB: true, Blobs: true, Redis: sub == "apply" && !*dry})
	if err != nil {
		return err
	}
	defer d.Close()
	if sub == "export" {
		if err := contestconfig.Export(ctx, sqlc.New(d.DB), d.Blobs, *contest, dir); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "exported %s to %s\n", *contest, dir)
		return nil
	}
	reg, err := langs.Load(cfg.LanguagesDir)
	if err != nil {
		return err
	}
	rep, err := contestconfig.Apply(ctx, d.DB, d.Blobs, dir, contestconfig.Options{DryRun: *dry, Activate: *activate,
		Packages: problempkg.OptionsFor(reg)})
	if err != nil {
		return err
	}
	for _, w := range rep.Warnings {
		fmt.Fprintln(stderr, "warning:", w)
	}
	verb := ""
	if *dry {
		verb = "would be "
	}
	switch {
	case rep.Created:
		fmt.Fprintf(stdout, "contest %s: %screated\n", rep.Contest, verb)
	case len(rep.Settings) > 0:
		fmt.Fprintf(stdout, "contest %s: %schanged %s\n", rep.Contest, verb, strings.Join(rep.Settings, ", "))
	default:
		fmt.Fprintf(stdout, "contest %s: settings unchanged\n", rep.Contest)
	}
	for _, t := range rep.Tasks {
		switch t.Action {
		case "unchanged":
			fmt.Fprintf(stdout, "task %s: unchanged\n", t.Name)
		case "pending":
			fmt.Fprintf(stdout, "task %s: dataset %q holds this content but is not live because the contest has started: "+
				"review it and set it live from the admin panel, or apply with -activate\n", t.Name, t.Dataset)
		default:
			line := fmt.Sprintf("task %s: %s%s", t.Name, verb, t.Action)
			if t.Dataset != "" {
				line += fmt.Sprintf(" (dataset %q)", t.Dataset)
			}
			if t.Action == "updated" && !t.Live {
				line += "; the new dataset is not live because the contest has started: review it and set it live from the admin panel, or apply with -activate"
			}
			fmt.Fprintln(stdout, line)
		}
	}
	if rep.Reordered {
		fmt.Fprintf(stdout, "task order: %supdated\n", verb)
	}
	for _, t := range rep.Unlisted {
		fmt.Fprintf(stdout, "task %s: in the contest but not in %s (left alone)\n", t, contestconfig.FileName)
	}
	if !rep.Changed() {
		fmt.Fprintln(stdout, "nothing to do")
	}
	q := queue.New(d.Redis, cfg.Redis.Namespace)
	for _, id := range rep.LiveChanged {
		if err := q.Notify(ctx, queue.Event{Kind: queue.EventDatasetChanged, TaskID: id}); err != nil {
			return err
		}
	}
	return nil
}
