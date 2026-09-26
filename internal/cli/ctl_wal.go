package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/backup"
	"github.com/D4ND3R/Contest-Management-System/internal/config"
)

func init() {
	ctlCommands["wal-archive"] = ctlCommand{"archive_command for PostgreSQL: store a WAL segment (args: %p %f)", cmdWALArchive}
	ctlCommands["wal-restore"] = ctlCommand{"restore_command for PostgreSQL: give a WAL segment back (args: %f %p)", cmdWALRestore}
	ctlCommands["basebackup"] = ctlCommand{"take a physical base backup (pg_basebackup) for point-in-time recovery", cmdBaseBackup}
}

func walSetup(name string, args []string, stderr io.Writer) (*config.Config, backup.Remote, []string, error) {
	fs, cfgPath := newFlags(name, stderr)
	if err := fs.Parse(args); err != nil {
		return nil, nil, nil, err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return nil, nil, nil, err
	}
	remote, err := backup.NewS3Remote(cfg.Backup.S3)
	if err != nil {
		return nil, nil, nil, err
	}
	return cfg, remote, fs.Args(), nil
}

func cmdWALArchive(args []string, stdout, stderr io.Writer) error {
	cfg, remote, rest, err := walSetup("wal-archive", args, stderr)
	if err != nil {
		return err
	}
	if len(rest) != 2 {
		return errors.New("usage: cms ctl wal-archive [-config file] <path %p> <name %f>")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return backup.ArchiveWAL(ctx, cfg.Backup.Dir, rest[0], rest[1], remote)
}

func cmdWALRestore(args []string, stdout, stderr io.Writer) error {
	cfg, remote, rest, err := walSetup("wal-restore", args, stderr)
	if err != nil {
		return err
	}
	if len(rest) != 2 {
		return errors.New("usage: cms ctl wal-restore [-config file] <name %f> <path %p>")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return backup.RestoreWAL(ctx, cfg.Backup.Dir, rest[0], rest[1], remote)
}

func cmdBaseBackup(args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("basebackup", stderr)
	dir := fs.String("dir", "", "where to write it (default: <backup dir>/base/<time>)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	bin, err := exec.LookPath("pg_basebackup")
	if err != nil {
		return errors.New("pg_basebackup is not installed (it comes with the PostgreSQL client tools)")
	}
	if *dir == "" {
		*dir = filepath.Join(cfg.Backup.Dir, "base", time.Now().UTC().Format("20060102-150405"))
	}
	if err := os.MkdirAll(filepath.Dir(*dir), 0o750); err != nil {
		return err
	}
	// Tar format, compressed; the WAL needed to make it consistent comes
	// from the archive (-X none keeps the base small).
	cmd := exec.Command(bin, "--dbname="+cfg.Database.URL, "-D", *dir, "-Ft", "-z", "-X", "none", "--checkpoint=fast", "-P")
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_basebackup: %w", err)
	}
	fmt.Fprintf(stdout, "base backup in %s; with the WAL archive it restores to any later moment\n", *dir)
	return nil
}
