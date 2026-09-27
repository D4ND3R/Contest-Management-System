package dispatcher

import (
	"context"
	"errors"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
	"github.com/jackc/pgx/v5"
)

// cacheWorker is the worker name recorded for a compilation taken from the
// cache (SPEC_IOI H4, D87).
const cacheWorker = "cache"

// cachedCompilation returns the remembered successful compilation of the
// same inputs as job, or nil.
func cachedCompilation(ctx context.Context, q *sqlc.Queries, job *jobs.Job) (*jobs.Compilation, error) {
	key := jobs.CompileKey(job)
	if key == "" {
		return nil, nil
	}
	row, err := q.GetCompilationCache(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	files, err := q.ListCompilationCacheFiles(ctx, key)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}
	if err := q.UseCompilationCache(ctx, key); err != nil {
		return nil, err
	}
	c := &jobs.Compilation{Success: true, Text: row.Text, Stdout: row.Stdout, Stderr: row.Stderr,
		Time: row.Time, WallTime: row.WallTime, Memory: row.Memory}
	for _, f := range files {
		c.Executables = append(c.Executables, jobs.File{Name: f.Filename, Digest: f.Digest, Size: f.Size})
	}
	return c, nil
}

// rememberCompilation stores a successful compilation under the hash of
// its inputs (the first one stays; they are interchangeable).
func rememberCompilation(ctx context.Context, q *sqlc.Queries, job *jobs.Job, c *jobs.Compilation) error {
	key := jobs.CompileKey(job)
	if key == "" || !c.Success || len(c.Executables) == 0 {
		return nil
	}
	n, err := q.InsertCompilationCache(ctx, sqlc.InsertCompilationCacheParams{Key: key, Text: c.Text, Stdout: c.Stdout,
		Stderr: c.Stderr, Time: c.Time, WallTime: c.WallTime, Memory: c.Memory})
	if err != nil || n == 0 {
		return err
	}
	for _, e := range c.Executables {
		if err := q.InsertCompilationCacheFile(ctx, sqlc.InsertCompilationCacheFileParams{Key: key, Filename: e.Name,
			Digest: e.Digest, Size: e.Size}); err != nil {
			return err
		}
	}
	return nil
}
