package cli

import (
	"archive/zip"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/D4ND3R/Contest-Management-System/internal/contestarchive"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
)

func init() {
	ctlCommands["contest-export"] = ctlCommand{"write the archive of a contest (zip: settings, tasks, participants, submissions)", cmdContestExport}
	ctlCommands["contest-import"] = ctlCommand{"create a contest from an archive written by contest-export", cmdContestImport}
}

func cmdContestExport(args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("contest-export", stderr)
	subs := fs.Bool("submissions", true, "include the submissions with their results")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: cmsctl contest-export [flags] CONTEST out.zip")
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
	c, err := sqlc.New(d.DB).GetContestByName(ctx, fs.Arg(0))
	if err != nil {
		return fmt.Errorf("contest %q: %w", fs.Arg(0), err)
	}
	out, err := os.Create(fs.Arg(1))
	if err != nil {
		return err
	}
	h, err := contestarchive.Export(ctx, d.DB, d.Blobs, c.ID, out, contestarchive.Options{Submissions: *subs})
	if err == nil {
		err = out.Close()
	} else {
		out.Close()
	}
	if err != nil {
		os.Remove(fs.Arg(1))
		return err
	}
	var rows int64
	for _, t := range h.Tables {
		rows += t.Rows
	}
	fmt.Fprintf(stdout, "exported %s to %s: %d rows (%d submissions), %d files (%d bytes)\n",
		c.Name, fs.Arg(1), rows, h.Rows("submissions"), h.Blobs, h.BlobBytes)
	if len(h.Missing) > 0 {
		fmt.Fprintf(stderr, "warning: %d files were missing from the blob store (first: %s)\n", len(h.Missing), h.Missing[0])
	}
	return nil
}

func cmdContestImport(args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("contest-import", stderr)
	var o contestarchive.ImportOptions
	fs.StringVar(&o.Name, "name", "", "name of the new contest (default: the archived one)")
	fs.StringVar(&o.TaskSuffix, "task-suffix", "", "suffix appended to every task name")
	fs.StringVar(&o.Status, "status", "", "archived, draft or published (default: archived with submissions, else draft)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: cmsctl contest-import [flags] archive.zip")
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
	_, d, err := openPackageDeps(ctx, *cfgPath)
	if err != nil {
		return err
	}
	defer d.Close()
	zr, err := zip.OpenReader(fs.Arg(0))
	if err != nil {
		return err
	}
	defer zr.Close()
	res, err := contestarchive.Import(ctx, d.DB, d.Blobs, &zr.Reader, o)
	var ce *contestarchive.ConflictError
	if errors.As(err, &ce) {
		return fmt.Errorf("%v: use -name and -task-suffix; nothing was imported", ce)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "imported %s as contest %d: %d rows, %d files, %d existing users and %d teams reused\n",
		res.Header.Contest, res.ContestID, res.Rows, res.Blobs, res.ReusedUsers, res.ReusedTeams)
	return nil
}
