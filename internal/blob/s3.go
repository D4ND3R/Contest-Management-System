package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Config configures an S3-compatible backend (AWS S3, MinIO, Ceph, ...).
type S3Config struct {
	Endpoint  string
	Bucket    string
	AccessKey string
	SecretKey string
	Region    string
	UseSSL    bool
	// Prefix is prepended to object keys (lets tests share a bucket).
	Prefix string
}

// S3 stores blobs as objects named <prefix><digest>.
type S3 struct {
	c      *minio.Client
	bucket string
	prefix string
	// spoolDir holds temporary files while hashing streamed uploads.
	spoolDir string
}

// memSpoolLimit: streamed uploads up to this size are hashed in memory.
const memSpoolLimit = 8 << 20

// NewS3 connects to the service and creates the bucket if missing.
func NewS3(ctx context.Context, cfg S3Config) (*S3, error) {
	c, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("s3 client: %w", err)
	}
	ok, err := c.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("s3 bucket check: %w", err)
	}
	if !ok {
		if err := c.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{Region: cfg.Region}); err != nil {
			// Another process may have created it concurrently.
			if exists, e2 := c.BucketExists(ctx, cfg.Bucket); e2 != nil || !exists {
				return nil, fmt.Errorf("s3 make bucket: %w", err)
			}
		}
	}
	return &S3{c: c, bucket: cfg.Bucket, prefix: cfg.Prefix, spoolDir: os.TempDir()}, nil
}

func (s *S3) key(d string) string { return s.prefix + d }

func (s *S3) Put(ctx context.Context, r io.Reader) (Info, error) {
	// The object name is the digest, so the content is hashed before upload:
	// small inputs in memory, large ones spooled to a temporary file.
	var buf bytes.Buffer
	n, err := io.CopyN(&buf, r, memSpoolLimit+1)
	if err != nil && err != io.EOF {
		return Info{}, err
	}
	if n <= memSpoolLimit {
		return s.PutBytes(ctx, buf.Bytes())
	}
	tmp, err := os.CreateTemp(s.spoolDir, "cms-s3-*")
	if err != nil {
		return Info{}, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, h), io.MultiReader(&buf, r))
	if err != nil {
		return Info{}, err
	}
	digest := hex.EncodeToString(h.Sum(nil))
	if _, err := s.Stat(ctx, digest); err == nil {
		return Info{Digest: digest, Size: size}, nil
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return Info{}, err
	}
	if _, err := s.c.PutObject(ctx, s.bucket, s.key(digest), tmp, size, minio.PutObjectOptions{ContentType: "application/octet-stream"}); err != nil {
		return Info{}, fmt.Errorf("s3 put: %w", err)
	}
	return Info{Digest: digest, Size: size, Created: true}, nil
}

func (s *S3) PutBytes(ctx context.Context, b []byte) (Info, error) {
	digest := Sum(b)
	if _, err := s.Stat(ctx, digest); err == nil {
		return Info{Digest: digest, Size: int64(len(b))}, nil
	}
	if _, err := s.c.PutObject(ctx, s.bucket, s.key(digest), bytes.NewReader(b), int64(len(b)), minio.PutObjectOptions{ContentType: "application/octet-stream"}); err != nil {
		return Info{}, fmt.Errorf("s3 put: %w", err)
	}
	return Info{Digest: digest, Size: int64(len(b)), Created: true}, nil
}

func isNotFound(err error) bool {
	resp := minio.ToErrorResponse(err)
	return resp.Code == "NoSuchKey" || resp.StatusCode == 404
}

func (s *S3) Open(ctx context.Context, digest string) (io.ReadCloser, error) {
	if err := checkDigest(digest); err != nil {
		return nil, err
	}
	obj, err := s.c.GetObject(ctx, s.bucket, s.key(digest), minio.GetObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	// GetObject is lazy; Stat surfaces a missing object now.
	if _, err := obj.Stat(); err != nil {
		obj.Close()
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return obj, nil
}

func (s *S3) Stat(ctx context.Context, digest string) (int64, error) {
	if err := checkDigest(digest); err != nil {
		return 0, err
	}
	st, err := s.c.StatObject(ctx, s.bucket, s.key(digest), minio.StatObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return st.Size, nil
}

func (s *S3) Delete(ctx context.Context, digest string) error {
	if err := checkDigest(digest); err != nil {
		return err
	}
	return s.c.RemoveObject(ctx, s.bucket, s.key(digest), minio.RemoveObjectOptions{})
}
