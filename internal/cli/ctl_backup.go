package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/D4ND3R/Contest-Management-System/internal/backup"
	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/deps"
	"github.com/D4ND3R/Contest-Management-System/internal/logging"
)

func init() {
	ctlCommands["dump"] = ctlCommand{"back up the database and every blob into one verifiable file", cmdDump}
	ctlCommands["restore"] = ctlCommand{"restore a backup into an empty database (stop every service first)", cmdRestore}
	ctlCommands["backup-verify"] = ctlCommand{"check every hash of a backup file without restoring it", cmdBackupVerify}
	ctlCommands["backups"] = ctlCommand{"list the backups in backup.dir", cmdBackups}
}

func openBackupDeps(ctx context.Context, cfgPath string, stderr io.Writer) (*config.Config, *deps.Deps, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, nil, err
	}
	d, err := deps.Open(ctx, cfg, logging.New(stderr, "cmsctl", "warn", "text"), deps.Need{DB: true, Blobs: true})
	return cfg, d, err
}

func cmdDump(args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("dump", stderr)
	out := fs.String("o", "", "write the backup to this file (\"-\": standard output) instead of backup.dir")
	rate := fs.String("max-rate", "", "read throttle per second (default backup.max_rate; 0 = unlimited)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	cfg, d, err := openBackupDeps(ctx, *cfgPath, stderr)
	if err != nil {
		return err
	}
	defer d.Close()
	if *rate != "" {
		v, err := config.ParseByteSize(*rate)
		if err != nil {
			return err
		}
		cfg.Backup.MaxRate = config.ByteSize(v)
	}
	if *out == "" {
		// Into backup.dir, with its metadata, like the admin's backups.
		r := backup.NewRunner(d.DB, d.Blobs, cfg.Backup, nil, nil, logging.Discard())
		e, err := r.Backup(ctx, backup.KindCLI, os.Getenv("USER"))
		if err != nil {
			return err
		}
		p, _ := r.Path(e.Name)
		fmt.Fprintf(stdout, "backup written to %s\n%d tables, %d rows, %d blobs (%d bytes), %d bytes compressed\nsha256 %s\n",
			p, e.Tables, e.Rows, e.Blobs, e.BlobBytes, e.Size, e.SHA256)
		if e.Missing > 0 {
			fmt.Fprintf(stderr, "warning: %d registered blobs are missing from the store\n", e.Missing)
		}
		return nil
	}
	var w io.Writer = stdout
	var f *os.File
	if *out != "-" {
		if f, err = os.OpenFile(*out+".partial", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600); err != nil {
			return err
		}
		defer os.Remove(*out + ".partial")
		w = f
	}
	bw := bufio.NewWriterSize(w, 1<<20)
	st, err := backup.Dump(ctx, d.DB, d.Blobs, bw, backup.Options{MaxRate: int64(cfg.Backup.MaxRate)})
	if err == nil {
		err = bw.Flush()
	}
	if f != nil {
		if err == nil {
			err = f.Sync()
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			err = os.Rename(*out+".partial", *out)
		}
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stderr, "%d tables, %d rows, %d blobs (%d bytes)\n", st.Tables, st.Rows, st.Blobs, st.BlobBytes)
	if len(st.Missing) > 0 {
		fmt.Fprintf(stderr, "warning: %d registered blobs are missing from the store\n", len(st.Missing))
	}
	return nil
}

func cmdRestore(args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("restore", stderr)
	force := fs.Bool("force", false, "replace a database that already holds data (everything in it is dropped)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: cmsctl restore [-force] <backup file>")
	}
	f, err := os.Open(fs.Arg(0))
	if err != nil {
		return err
	}
	defer f.Close()
	ctx := context.Background()
	_, d, err := openBackupDeps(ctx, *cfgPath, stderr)
	if err != nil {
		return err
	}
	defer d.Close()
	st, err := backup.Restore(ctx, d.DB, d.Blobs, bufio.NewReaderSize(f, 1<<20), backup.RestoreOptions{Force: *force})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "restored the backup of %s (CMS %s): %d tables, %d rows, %d blobs (%d bytes)\n",
		st.Header.CreatedAt.Format("2006-01-02 15:04:05 MST"), st.Header.Version, st.Tables, st.Rows, st.Blobs, st.BlobBytes)
	if len(st.Missing) > 0 {
		fmt.Fprintf(stderr, "warning: %d blobs were already missing when the backup was taken: %s\n", len(st.Missing), strings.Join(st.Missing, ", "))
	}
	return nil
}

func cmdBackupVerify(args []string, stdout, stderr io.Writer) error {
	fs, _ := newFlags("backup-verify", stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: cmsctl backup-verify <backup file>")
	}
	path := fs.Arg(0)
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	// The whole-file digest recorded next to the archive, when present.
	var meta backup.Entry
	metaFile := strings.TrimSuffix(path, ".tar.zst") + ".json"
	hasMeta := false
	if b, err := os.ReadFile(metaFile); err == nil && json.Unmarshal(b, &meta) == nil && meta.SHA256 != "" {
		hasMeta = true
	}
	hw := backup.NewFileHasher()
	// Everything read from the file is hashed once: what the buffer took,
	// then whatever the decompressor left.
	st, err := backup.Verify(context.Background(), bufio.NewReaderSize(io.TeeReader(f, hw), 1<<20))
	if err != nil {
		return err
	}
	if _, err := io.Copy(hw, f); err != nil {
		return err
	}
	if hasMeta && hw.Sum() != meta.SHA256 {
		return fmt.Errorf("the file digest %s does not match %s recorded in %s", hw.Sum(), meta.SHA256, metaFile)
	}
	fmt.Fprintf(stdout, "OK: backup of %s (CMS %s), %d tables, %d rows, %d blobs (%d bytes)\n",
		st.Header.CreatedAt.Format("2006-01-02 15:04:05 MST"), st.Header.Version, st.Tables, st.Rows, st.Blobs, st.BlobBytes)
	if len(st.Missing) > 0 {
		fmt.Fprintf(stdout, "note: %d blobs were missing from the store when it was taken\n", len(st.Missing))
	}
	return nil
}

func cmdBackups(args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("backups", stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	list, err := backup.NewRunner(nil, nil, cfg.Backup, nil, nil, logging.Discard()).List()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tKIND\tSTATUS\tSIZE\tBLOBS\tS3")
	for _, e := range list {
		s3 := ""
		if e.Remote {
			s3 = "yes"
		} else if e.RemoteError != "" {
			s3 = "failed"
		}
		status := e.Status
		if e.Error != "" {
			status += ": " + e.Error
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%s\n", e.Name, e.Kind, status, e.Size, e.Blobs, s3)
	}
	return tw.Flush()
}
