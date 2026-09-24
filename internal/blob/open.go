package blob

import (
	"context"
	"fmt"

	"github.com/D4ND3R/Contest-Management-System/internal/config"
)

// Open builds the store described by cfg: the backend, wrapped in a local
// LRU cache when cache_dir and cache_max_bytes are set.
func Open(ctx context.Context, cfg config.Blob) (Store, error) {
	var s Store
	switch cfg.Backend {
	case "local":
		l, err := NewLocal(cfg.LocalDir, true)
		if err != nil {
			return nil, err
		}
		s = l
	case "s3":
		b, err := NewS3(ctx, S3Config{
			Endpoint: cfg.S3.Endpoint, Bucket: cfg.S3.Bucket, AccessKey: cfg.S3.AccessKey,
			SecretKey: cfg.S3.SecretKey, Region: cfg.S3.Region, UseSSL: cfg.S3.UseSSL,
		})
		if err != nil {
			return nil, err
		}
		s = b
	default:
		return nil, fmt.Errorf("unknown blob backend %q", cfg.Backend)
	}
	if cfg.CacheDir != "" && cfg.CacheMaxBytes > 0 {
		c, err := NewCache(s, cfg.CacheDir, int64(cfg.CacheMaxBytes))
		if err != nil {
			return nil, err
		}
		s = c
	}
	return s, nil
}
