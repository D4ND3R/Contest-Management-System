package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Local stores blobs as files under dir/objects/ab/<digest>. Writes go to
// dir/tmp first and are renamed into place atomically, so readers never see
// partial content and concurrent writers of the same content are harmless.
type Local struct {
	dir  string
	sync bool
}

// NewLocal creates (if needed) and opens a local store rooted at dir. When
// sync is true every new blob is fsync'ed before being published.
func NewLocal(dir string, sync bool) (*Local, error) {
	for _, d := range []string{filepath.Join(dir, "objects"), filepath.Join(dir, "tmp")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("blob store: %w", err)
		}
	}
	return &Local{dir: dir, sync: sync}, nil
}

// Path returns the file path of a digest (the file may not exist).
func (l *Local) Path(digest string) string {
	return filepath.Join(l.dir, "objects", digest[:2], digest)
}

func (l *Local) exists(digest string) (int64, bool) {
	st, err := os.Stat(l.Path(digest))
	if err != nil {
		return 0, false
	}
	return st.Size(), true
}

func (l *Local) publish(tmp *os.File, digest string) (bool, error) {
	final := l.Path(digest)
	if _, err := os.Stat(final); err == nil {
		os.Remove(tmp.Name())
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		os.Remove(tmp.Name())
		return false, err
	}
	if err := os.Rename(tmp.Name(), final); err != nil {
		os.Remove(tmp.Name())
		return false, err
	}
	return true, nil
}

func (l *Local) Put(ctx context.Context, r io.Reader) (Info, error) {
	tmp, err := os.CreateTemp(filepath.Join(l.dir, "tmp"), "put-*")
	if err != nil {
		return Info{}, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), r)
	if err == nil && l.sync {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return Info{}, err
	}
	digest := hex.EncodeToString(h.Sum(nil))
	created, err := l.publish(tmp, digest)
	if err != nil {
		return Info{}, err
	}
	return Info{Digest: digest, Size: n, Created: created}, nil
}

func (l *Local) PutBytes(ctx context.Context, b []byte) (Info, error) {
	digest := Sum(b)
	if _, ok := l.exists(digest); ok {
		return Info{Digest: digest, Size: int64(len(b))}, nil
	}
	tmp, err := os.CreateTemp(filepath.Join(l.dir, "tmp"), "put-*")
	if err != nil {
		return Info{}, err
	}
	_, err = io.Copy(tmp, bytes.NewReader(b))
	if err == nil && l.sync {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return Info{}, err
	}
	created, err := l.publish(tmp, digest)
	if err != nil {
		return Info{}, err
	}
	return Info{Digest: digest, Size: int64(len(b)), Created: created}, nil
}

func (l *Local) Open(ctx context.Context, digest string) (io.ReadCloser, error) {
	if err := checkDigest(digest); err != nil {
		return nil, err
	}
	f, err := os.Open(l.Path(digest))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	return f, err
}

func (l *Local) Stat(ctx context.Context, digest string) (int64, error) {
	if err := checkDigest(digest); err != nil {
		return 0, err
	}
	if n, ok := l.exists(digest); ok {
		return n, nil
	}
	return 0, ErrNotFound
}

func (l *Local) Delete(ctx context.Context, digest string) error {
	if err := checkDigest(digest); err != nil {
		return err
	}
	err := os.Remove(l.Path(digest))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
