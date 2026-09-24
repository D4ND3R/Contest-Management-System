// Package blob implements the content-addressed file store. Every file the
// system handles (testcases, statements, submissions, executables, outputs)
// is identified by the lowercase hex SHA-256 of its content, so identical
// files are stored once and can be cached anywhere without invalidation.
//
// Backends: Local (filesystem) and S3 (any S3-compatible service). Cache adds
// a local, size-bounded LRU in front of any backend (used by workers).
// Tracked records every stored blob in the database for garbage collection.
package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// ErrNotFound is returned when a digest is not present in the store.
var ErrNotFound = errors.New("blob not found")

// ErrDigestMismatch is returned when fetched content does not hash to the
// requested digest (corruption or tampering).
var ErrDigestMismatch = errors.New("blob digest mismatch")

// Info describes a stored blob.
type Info struct {
	Digest string
	Size   int64
	// Created is false when the content was already present (deduplicated).
	Created bool
}

// Store is a content-addressed blob store.
type Store interface {
	// Put stores the content of r and returns its digest. Storing content
	// that already exists is cheap and returns Created=false.
	Put(ctx context.Context, r io.Reader) (Info, error)
	// PutBytes is Put for in-memory content (avoids spooling).
	PutBytes(ctx context.Context, b []byte) (Info, error)
	// Open returns a reader for the blob or ErrNotFound.
	Open(ctx context.Context, digest string) (io.ReadCloser, error)
	// Stat returns the size of the blob or ErrNotFound.
	Stat(ctx context.Context, digest string) (int64, error)
	// Delete removes the blob (no error if absent).
	Delete(ctx context.Context, digest string) error
}

// Sum returns the digest of b.
func Sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// ValidDigest reports whether d is a well-formed digest (64 lowercase hex chars).
// Digests are used to build file paths, so this check also prevents path traversal.
func ValidDigest(d string) bool {
	if len(d) != 64 {
		return false
	}
	for i := 0; i < len(d); i++ {
		c := d[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func checkDigest(d string) error {
	if !ValidDigest(d) {
		return fmt.Errorf("invalid digest %q", d)
	}
	return nil
}

// ReadAll reads a whole blob into memory.
func ReadAll(ctx context.Context, s Store, digest string) ([]byte, error) {
	rc, err := s.Open(ctx, digest)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// ReadLimited reads at most limit bytes of a blob and reports whether it was truncated.
func ReadLimited(ctx context.Context, s Store, digest string, limit int64) ([]byte, bool, error) {
	rc, err := s.Open(ctx, digest)
	if err != nil {
		return nil, false, err
	}
	defer rc.Close()
	var buf bytes.Buffer
	n, err := io.CopyN(&buf, rc, limit+1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, false, err
	}
	if n > limit {
		return buf.Bytes()[:limit], true, nil
	}
	return buf.Bytes(), false, nil
}
