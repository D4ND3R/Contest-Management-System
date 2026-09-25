package backup

import (
	"context"
	"fmt"

	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// s3Remote copies backups to an S3-compatible bucket.
type s3Remote struct {
	c      *minio.Client
	bucket string
	prefix string
}

// NewS3Remote returns the off-site destination described by cfg, or nil
// when none is configured.
func NewS3Remote(cfg config.BackupS3) (Remote, error) {
	if !cfg.Enabled() {
		return nil, nil
	}
	c, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("backup s3 client: %w", err)
	}
	return &s3Remote{c: c, bucket: cfg.Bucket, prefix: cfg.Prefix}, nil
}

func (s *s3Remote) Upload(ctx context.Context, name, path string) error {
	_, err := s.c.FPutObject(ctx, s.bucket, s.prefix+name, path, minio.PutObjectOptions{ContentType: "application/octet-stream"})
	return err
}

func (s *s3Remote) Delete(ctx context.Context, name string) error {
	return s.c.RemoveObject(ctx, s.bucket, s.prefix+name, minio.RemoveObjectOptions{})
}
