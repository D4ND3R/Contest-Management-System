package blob

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/D4ND3R/Contest-Management-System/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

var cacheEvents = metrics.NewCounterVec(prometheus.CounterOpts{
	Name: "cms_blob_cache_events_total",
	Help: "Blob cache lookups by result (hit, miss) and evictions.",
}, []string{"event"})

// Cache is a read-through, size-bounded LRU cache of blobs on the local
// filesystem (ideally tmpfs) in front of another Store. Content fetched from
// the backend is verified against its digest before being cached, so a
// corrupted backend object can never poison a worker.
type Cache struct {
	backend Store
	dir     string
	max     int64

	mu    sync.Mutex
	lru   *list.List // front = most recently used; values are *cacheEntry
	items map[string]*list.Element
	size  int64
	calls map[string]*fetchCall
}

type cacheEntry struct {
	digest string
	size   int64
}

type fetchCall struct {
	done chan struct{}
	err  error
}

// NewCache opens a cache in dir holding at most maxBytes, indexing files
// left by a previous run (least recently modified evicted first).
func NewCache(backend Store, dir string, maxBytes int64) (*Cache, error) {
	c := &Cache{backend: backend, dir: dir, max: maxBytes, lru: list.New(),
		items: map[string]*list.Element{}, calls: map[string]*fetchCall{}}
	for _, d := range []string{filepath.Join(dir, "objects"), filepath.Join(dir, "tmp")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	// Leftover temporaries from a crash are garbage.
	if ents, err := os.ReadDir(filepath.Join(dir, "tmp")); err == nil {
		for _, e := range ents {
			os.Remove(filepath.Join(dir, "tmp", e.Name()))
		}
	}
	type found struct {
		digest string
		size   int64
		mtime  int64
	}
	var all []found
	err := filepath.WalkDir(filepath.Join(dir, "objects"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !ValidDigest(d.Name()) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		all = append(all, found{d.Name(), info.Size(), info.ModTime().UnixNano()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(all, func(i, j int) bool { return all[i].mtime > all[j].mtime })
	for _, f := range all {
		c.items[f.digest] = c.lru.PushBack(&cacheEntry{f.digest, f.size})
		c.size += f.size
	}
	c.mu.Lock()
	c.evictLocked()
	c.mu.Unlock()
	return c, nil
}

func (c *Cache) path(digest string) string {
	return filepath.Join(c.dir, "objects", digest[:2], digest)
}

// Size returns the bytes currently cached.
func (c *Cache) Size() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.size
}

// Contains reports whether digest is cached locally.
func (c *Cache) Contains(digest string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.items[digest]
	return ok
}

func (c *Cache) evictLocked() {
	for c.size > c.max && c.lru.Len() > 1 {
		el := c.lru.Back()
		e := el.Value.(*cacheEntry)
		c.lru.Remove(el)
		delete(c.items, e.digest)
		c.size -= e.size
		// Open file descriptors keep working after unlink.
		os.Remove(c.path(e.digest))
		cacheEvents.WithLabelValues("evict").Inc()
	}
}

func (c *Cache) insert(tmpPath, digest string, size int64) error {
	final := c.path(digest)
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return err
	}
	// Cached files are immutable: read-only for everyone.
	_ = os.Chmod(tmpPath, 0o444)
	if err := os.Rename(tmpPath, final); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[digest]; ok {
		c.lru.MoveToFront(el)
		return nil
	}
	c.items[digest] = c.lru.PushFront(&cacheEntry{digest, size})
	c.size += size
	c.evictLocked()
	return nil
}

// Fetch makes digest available locally and returns its path. Concurrent
// fetches of the same digest share a single download. The returned file can
// be evicted at any later time; use OpenFile when holding it open matters.
func (c *Cache) Fetch(ctx context.Context, digest string) (string, error) {
	if err := checkDigest(digest); err != nil {
		return "", err
	}
	for {
		c.mu.Lock()
		if el, ok := c.items[digest]; ok {
			c.lru.MoveToFront(el)
			c.mu.Unlock()
			cacheEvents.WithLabelValues("hit").Inc()
			return c.path(digest), nil
		}
		if call, ok := c.calls[digest]; ok {
			c.mu.Unlock()
			select {
			case <-call.done:
			case <-ctx.Done():
				return "", ctx.Err()
			}
			if call.err != nil {
				return "", call.err
			}
			continue
		}
		call := &fetchCall{done: make(chan struct{})}
		c.calls[digest] = call
		c.mu.Unlock()

		cacheEvents.WithLabelValues("miss").Inc()
		call.err = c.download(ctx, digest)
		c.mu.Lock()
		delete(c.calls, digest)
		c.mu.Unlock()
		close(call.done)
		if call.err != nil {
			return "", call.err
		}
	}
}

func (c *Cache) download(ctx context.Context, digest string) error {
	rc, err := c.backend.Open(ctx, digest)
	if err != nil {
		return err
	}
	defer rc.Close()
	tmp, err := os.CreateTemp(filepath.Join(c.dir, "tmp"), "fetch-*")
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), rc)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != digest {
		err = ErrDigestMismatch
	}
	if err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := c.insert(tmp.Name(), digest, n); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// OpenFile fetches digest and returns an open handle, which stays valid even
// if the entry is evicted meanwhile.
func (c *Cache) OpenFile(ctx context.Context, digest string) (*os.File, error) {
	for attempt := 0; attempt < 3; attempt++ {
		p, err := c.Fetch(ctx, digest)
		if err != nil {
			return nil, err
		}
		f, err := os.Open(p)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		// Evicted between Fetch and Open: forget it and retry.
		c.forget(digest)
	}
	return nil, errors.New("blob cache: entry repeatedly evicted")
}

func (c *Cache) forget(digest string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[digest]; ok {
		c.size -= el.Value.(*cacheEntry).size
		c.lru.Remove(el)
		delete(c.items, digest)
	}
}

// Put writes through to the backend and keeps a local copy.
func (c *Cache) Put(ctx context.Context, r io.Reader) (Info, error) {
	tmp, err := os.CreateTemp(filepath.Join(c.dir, "tmp"), "put-*")
	if err != nil {
		return Info{}, err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful insert (renamed)
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), r)
	if err != nil {
		tmp.Close()
		return Info{}, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		tmp.Close()
		return Info{}, err
	}
	info, err := c.backend.Put(ctx, tmp)
	tmp.Close()
	if err != nil {
		return Info{}, err
	}
	if info.Digest != hex.EncodeToString(h.Sum(nil)) || info.Size != n {
		return Info{}, ErrDigestMismatch
	}
	if !c.Contains(info.Digest) {
		_ = c.insert(tmp.Name(), info.Digest, n)
	}
	return info, nil
}

// PutBytes writes through to the backend and keeps a local copy.
func (c *Cache) PutBytes(ctx context.Context, b []byte) (Info, error) {
	info, err := c.backend.PutBytes(ctx, b)
	if err != nil {
		return Info{}, err
	}
	if c.Contains(info.Digest) {
		return info, nil
	}
	tmp, err := os.CreateTemp(filepath.Join(c.dir, "tmp"), "put-*")
	if err != nil {
		return info, nil // caching is best effort
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || c.insert(tmp.Name(), info.Digest, int64(len(b))) != nil {
		os.Remove(tmp.Name())
	}
	return info, nil
}

func (c *Cache) Open(ctx context.Context, digest string) (io.ReadCloser, error) {
	return c.OpenFile(ctx, digest)
}

func (c *Cache) Stat(ctx context.Context, digest string) (int64, error) {
	if err := checkDigest(digest); err != nil {
		return 0, err
	}
	c.mu.Lock()
	if el, ok := c.items[digest]; ok {
		n := el.Value.(*cacheEntry).size
		c.mu.Unlock()
		return n, nil
	}
	c.mu.Unlock()
	return c.backend.Stat(ctx, digest)
}

func (c *Cache) Delete(ctx context.Context, digest string) error {
	if err := checkDigest(digest); err != nil {
		return err
	}
	c.forget(digest)
	os.Remove(c.path(digest))
	return c.backend.Delete(ctx, digest)
}
