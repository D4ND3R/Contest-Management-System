package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// Continuous archiving (SPEC_IOI §12): PostgreSQL hands every finished WAL
// segment to `cms ctl wal-archive` (archive_command) and asks for them
// back with `cms ctl wal-restore` (restore_command) during a
// point-in-time recovery. Segments go to <backup dir>/wal and, when the
// backups have an S3 destination, to its wal/ prefix too.

// ErrRemoteNotFound: the remote does not have the object.
var ErrRemoteNotFound = errors.New("not in the remote")

// ErrWALNotFound: no copy of the segment exists (restore_command must then
// fail: PostgreSQL tries the next source or ends the recovery).
var ErrWALNotFound = errors.New("WAL segment not archived")

// Downloader is a remote that can give files back.
type Downloader interface {
	Download(ctx context.Context, name, path string) error
}

var walName = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._-]*$`)

// WALDir is where the segments are kept locally.
func WALDir(backupDir string) string { return filepath.Join(backupDir, "wal") }

// ArchiveWAL stores segment name (the file at src) durably. Archiving the
// same segment again is harmless; a different file under an existing name
// is refused (PostgreSQL then retries and the monitor raises an alert).
func ArchiveWAL(ctx context.Context, backupDir, src, name string, remote Remote) error {
	if !walName.MatchString(name) {
		return fmt.Errorf("invalid WAL file name %q", name)
	}
	dir := WALDir(backupDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	dst := filepath.Join(dir, name)
	if same, err := sameContent(src, dst); err == nil {
		if !same {
			return fmt.Errorf("%s is already archived with a different content", name)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else if err := copyDurably(src, dst); err != nil {
		return err
	}
	if remote != nil {
		if err := remote.Upload(ctx, "wal/"+name, dst); err != nil {
			return fmt.Errorf("upload %s: %w", name, err)
		}
	}
	return nil
}

// RestoreWAL copies segment name to dst, from the local archive or else
// from the remote.
func RestoreWAL(ctx context.Context, backupDir, name, dst string, remote Remote) error {
	if !walName.MatchString(name) {
		return fmt.Errorf("invalid WAL file name %q", name)
	}
	src := filepath.Join(WALDir(backupDir), name)
	if _, err := os.Stat(src); err == nil {
		return copyDurably(src, dst)
	}
	if d, ok := remote.(Downloader); ok && remote != nil {
		tmp := dst + ".part"
		err := d.Download(ctx, "wal/"+name, tmp)
		if errors.Is(err, ErrRemoteNotFound) {
			return ErrWALNotFound
		}
		if err != nil {
			return err
		}
		return os.Rename(tmp, dst)
	}
	return ErrWALNotFound
}

// copyDurably writes src to dst through a temporary file, synced before
// the rename (a crash never leaves a partial segment under its name).
func copyDurably(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	if d, err := os.Open(filepath.Dir(dst)); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

func sameContent(a, b string) (bool, error) {
	hb, err := fileHash(b)
	if err != nil {
		return false, err
	}
	ha, err := fileHash(a)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ha, hb), nil
}

func fileHash(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}
