package blob

import (
	"bytes"
	"context"
	"io"
	"sync"
)

// Mem is an in-memory Store for tests.
type Mem struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// NewMem returns an empty in-memory store.
func NewMem() *Mem { return &Mem{data: map[string][]byte{}} }

func (m *Mem) Put(ctx context.Context, r io.Reader) (Info, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return Info{}, err
	}
	return m.PutBytes(ctx, b)
}

func (m *Mem) PutBytes(ctx context.Context, b []byte) (Info, error) {
	d := Sum(b)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.data[d]; ok {
		return Info{Digest: d, Size: int64(len(b))}, nil
	}
	m.data[d] = bytes.Clone(b)
	return Info{Digest: d, Size: int64(len(b)), Created: true}, nil
}

func (m *Mem) Open(ctx context.Context, digest string) (io.ReadCloser, error) {
	if err := checkDigest(digest); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.data[digest]
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (m *Mem) Stat(ctx context.Context, digest string) (int64, error) {
	if err := checkDigest(digest); err != nil {
		return 0, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.data[digest]
	if !ok {
		return 0, ErrNotFound
	}
	return int64(len(b)), nil
}

func (m *Mem) Delete(ctx context.Context, digest string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, digest)
	return nil
}

// Len returns the number of stored blobs.
func (m *Mem) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.data)
}
