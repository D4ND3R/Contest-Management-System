package cli

import (
	"archive/zip"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/deps"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/logging"
	"github.com/D4ND3R/Contest-Management-System/internal/problempkg"
)

func init() {
	ctlCommands["task-import"] = ctlCommand{"import a problem package (zip) as a task or a new dataset", cmdTaskImport}
	ctlCommands["task-export"] = ctlCommand{"export a task as a problem package (zip)", cmdTaskExport}
}

func openPackageDeps(ctx context.Context, cfgPath string) (*config.Config, *deps.Deps, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, nil, err
	}
	d, err := deps.Open(ctx, cfg, logging.Discard(), deps.Need{DB: true, Blobs: true})
	return cfg, d, err
}

func cmdTaskImport(args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("task-import", stderr)
	contest := fs.String("contest", "", "append the new task to this contest")
	task := fs.String("task", "", "add the package as a new (not live) dataset of this task")
	dry := fs.Bool("dry-run", false, "only check the package")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: cmsctl task-import [flags] package.zip")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return flag.ErrHelp
	}
	ctx := context.Background()
	cfg, d, err := openPackageDeps(ctx, *cfgPath)
	if err != nil {
		return err
	}
	defer d.Close()
	reg, err := langs.Load(cfg.LanguagesDir)
	if err != nil {
		return err
	}
	zr, err := zip.OpenReader(fs.Arg(0))
	if err != nil {
		return err
	}
	defer zr.Close()
	p := problempkg.Read(&zr.Reader, problempkg.OptionsFor(reg))
	for _, w := range p.Warnings {
		fmt.Fprintln(stderr, "warning:", w)
	}
	for _, e := range p.Errors {
		fmt.Fprintln(stderr, "error:", e)
	}
	if !p.OK() {
		return fmt.Errorf("%d problems: nothing was imported", len(p.Errors))
	}
	c := p.Config
	fmt.Fprintf(stdout, "%s (%s, %s): %d testcases, maximum score %g, %d statements, %d managers, %d solutions\n",
		c.Name, c.TaskType(), c.ScoreType(), len(p.Tests), p.MaxScore, len(p.Statements), len(p.Managers), len(p.Solutions))
	if *dry {
		return nil
	}
	q := sqlc.New(d.DB)
	var o problempkg.ImportOptions
	if *task != "" {
		t, err := q.GetTaskByName(ctx, *task)
		if err != nil {
			return fmt.Errorf("task %q: %w", *task, err)
		}
		o.TaskID = t.ID
	}
	if *contest != "" {
		ct, err := q.GetContestByName(ctx, *contest)
		if err != nil {
			return fmt.Errorf("contest %q: %w", *contest, err)
		}
		o.ContestID = &ct.ID
	}
	res, err := problempkg.Import(ctx, d.DB, d.Blobs, p, o)
	if errors.Is(err, problempkg.ErrNameTaken) {
		return fmt.Errorf("a task named %q exists: use -task %s to add a dataset", c.Name, c.Name)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "imported: task %d, dataset %d (%s)\n", res.TaskID, res.DatasetID, res.Dataset)
	if len(p.Solutions) > 0 {
		fmt.Fprintln(stdout, "the reference solutions are judged when the package is imported from the admin panel")
	}
	return nil
}

func cmdTaskExport(args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("task-export", stderr)
	dataset := fs.Int64("dataset", 0, "dataset id (default: the live one)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: cmsctl task-export [flags] TASK out.zip")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return flag.ErrHelp
	}
	ctx := context.Background()
	_, d, err := openPackageDeps(ctx, *cfgPath)
	if err != nil {
		return err
	}
	defer d.Close()
	q := sqlc.New(d.DB)
	t, err := q.GetTaskByName(ctx, fs.Arg(0))
	if err != nil {
		return fmt.Errorf("task %q: %w", fs.Arg(0), err)
	}
	out, err := os.Create(fs.Arg(1))
	if err != nil {
		return err
	}
	if err := problempkg.Export(ctx, q, d.Blobs, t.ID, *dataset, out); err != nil {
		out.Close()
		os.Remove(fs.Arg(1))
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "exported %s to %s\n", t.Name, fs.Arg(1))
	return nil
}
