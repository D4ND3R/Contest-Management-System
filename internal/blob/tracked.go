package blob

import (
	"context"
	"io"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
)

// Tracked records every stored blob in the blobs table (size, first upload
// time) so unreferenced content can be garbage-collected later.
type Tracked struct {
	Store
	q *sqlc.Queries
}

// NewTracked wraps s; q must point at the CMS database.
func NewTracked(s Store, q *sqlc.Queries) *Tracked { return &Tracked{Store: s, q: q} }

func (t *Tracked) register(ctx context.Context, info Info) error {
	return t.q.RegisterBlob(ctx, sqlc.RegisterBlobParams{Digest: info.Digest, Size: info.Size})
}

func (t *Tracked) Put(ctx context.Context, r io.Reader) (Info, error) {
	info, err := t.Store.Put(ctx, r)
	if err != nil {
		return info, err
	}
	return info, t.register(ctx, info)
}

func (t *Tracked) PutBytes(ctx context.Context, b []byte) (Info, error) {
	info, err := t.Store.PutBytes(ctx, b)
	if err != nil {
		return info, err
	}
	return info, t.register(ctx, info)
}

// Unwrap returns the underlying store.
func (t *Tracked) Unwrap() Store { return t.Store }

// GC deletes blobs that no table references and that were registered before
// olderThan (the grace period protects uploads whose rows are not committed
// yet). It returns the number of blobs and bytes removed.
func GC(ctx context.Context, s Store, q *sqlc.Queries, olderThan time.Time, batch int32) (int, int64, error) {
	var n int
	var bytes int64
	for {
		rows, err := q.ListUnreferencedBlobs(ctx, sqlc.ListUnreferencedBlobsParams{OlderThan: olderThan, Limit: batch})
		if err != nil {
			return n, bytes, err
		}
		for _, r := range rows {
			if err := s.Delete(ctx, r.Digest); err != nil {
				return n, bytes, err
			}
			if err := q.DeleteBlobRecord(ctx, r.Digest); err != nil {
				return n, bytes, err
			}
			n++
			bytes += r.Size
		}
		if len(rows) < int(batch) {
			return n, bytes, nil
		}
	}
}
