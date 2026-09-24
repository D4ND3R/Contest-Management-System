package blob

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func randBytes(t testing.TB, n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// storeContract exercises the behaviour every backend must provide.
func storeContract(t *testing.T, s Store) {
	ctx := context.Background()
	data := []byte("hello, contestants\n")
	want := Sum(data)

	info, err := s.PutBytes(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	if info.Digest != want || info.Size != int64(len(data)) {
		t.Fatalf("PutBytes info = %+v", info)
	}
	// Same content again: deduplicated.
	info2, err := s.Put(ctx, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if info2.Digest != want || info2.Created {
		t.Fatalf("second Put must be a dedup hit: %+v", info2)
	}
	got, err := ReadAll(ctx, s, want)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("ReadAll = %q, %v", got, err)
	}
	if n, err := s.Stat(ctx, want); err != nil || n != int64(len(data)) {
		t.Fatalf("Stat = %d, %v", n, err)
	}
	missing := Sum([]byte("missing"))
	if _, err := s.Open(ctx, missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Open(missing) err = %v", err)
	}
	if _, err := s.Stat(ctx, missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Stat(missing) err = %v", err)
	}
	if _, err := s.Open(ctx, "../../etc/passwd"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid digest must be rejected, got %v", err)
	}
	// Large streamed content (exercises spooling).
	big := randBytes(t, 9<<20)
	info3, err := s.Put(ctx, bytes.NewReader(big))
	if err != nil {
		t.Fatal(err)
	}
	if info3.Digest != Sum(big) || info3.Size != int64(len(big)) || !info3.Created {
		t.Fatalf("big Put info = %+v", info3)
	}
	gotBig, err := ReadAll(ctx, s, info3.Digest)
	if err != nil || !bytes.Equal(gotBig, big) {
		t.Fatalf("big roundtrip failed: %v", err)
	}
	if err := s.Delete(ctx, info3.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Stat(ctx, info3.Digest); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if err := s.Delete(ctx, info3.Digest); err != nil {
		t.Fatalf("deleting a missing blob must succeed: %v", err)
	}
}

func TestLocalContract(t *testing.T) {
	s, err := NewLocal(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	storeContract(t, s)
}

func TestMemContract(t *testing.T) { storeContract(t, NewMem()) }

func TestLocalDeduplicationStoresOneFile(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewLocal(dir, true)
	ctx := context.Background()
	data := []byte("int main() { return 0; }")
	var wg sync.WaitGroup
	infos := make([]Info, 16)
	for i := range infos {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var err error
			if i%2 == 0 {
				infos[i], err = s.PutBytes(ctx, data)
			} else {
				infos[i], err = s.Put(ctx, bytes.NewReader(data))
			}
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var files int
	filepath.Walk(filepath.Join(dir, "objects"), func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			files++
		}
		return nil
	})
	if files != 1 {
		t.Fatalf("expected exactly one stored object, found %d", files)
	}
	tmp, _ := os.ReadDir(filepath.Join(dir, "tmp"))
	if len(tmp) != 0 {
		t.Fatalf("temporary files leaked: %d", len(tmp))
	}
	for _, in := range infos {
		if in.Digest != Sum(data) {
			t.Fatalf("digest mismatch: %+v", in)
		}
	}
}

func TestValidDigest(t *testing.T) {
	if !ValidDigest(Sum(nil)) {
		t.Fatal("sha256 of empty input must be valid")
	}
	for _, bad := range []string{"", "abc", strings.Repeat("G", 64), strings.Repeat("A", 64), strings.Repeat("a", 63) + "/"} {
		if ValidDigest(bad) {
			t.Errorf("%q considered valid", bad)
		}
	}
}

func TestReadLimited(t *testing.T) {
	s := NewMem()
	info, _ := s.PutBytes(context.Background(), []byte("0123456789"))
	b, trunc, err := ReadLimited(context.Background(), s, info.Digest, 4)
	if err != nil || string(b) != "0123" || !trunc {
		t.Fatalf("got %q %v %v", b, trunc, err)
	}
	b, trunc, _ = ReadLimited(context.Background(), s, info.Digest, 10)
	if string(b) != "0123456789" || trunc {
		t.Fatalf("got %q %v", b, trunc)
	}
}

// countingStore counts backend opens to observe cache hits.
type countingStore struct {
	Store
	mu    sync.Mutex
	opens int
}

func (c *countingStore) Open(ctx context.Context, d string) (io.ReadCloser, error) {
	c.mu.Lock()
	c.opens++
	c.mu.Unlock()
	return c.Store.Open(ctx, d)
}

func TestCacheReadThroughAndSingleflight(t *testing.T) {
	ctx := context.Background()
	backend := &countingStore{Store: NewMem()}
	data := randBytes(t, 1<<16)
	info, _ := backend.PutBytes(ctx, data)
	c, err := NewCache(backend, t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := ReadAll(ctx, c, info.Digest)
			if err != nil || !bytes.Equal(got, data) {
				t.Errorf("read: %v", err)
			}
		}()
	}
	wg.Wait()
	if backend.opens != 1 {
		t.Fatalf("backend opened %d times; want 1 (singleflight + cache)", backend.opens)
	}
	p, _ := c.Fetch(ctx, info.Digest)
	if st, _ := os.Stat(p); st.Mode().Perm()&0o222 != 0 {
		t.Fatalf("cached file must be read-only, mode %v", st.Mode())
	}
}

func TestCacheEvictsLRU(t *testing.T) {
	ctx := context.Background()
	backend := NewMem()
	var digests []string
	for i := 0; i < 5; i++ {
		info, _ := backend.PutBytes(ctx, randBytes(t, 1000))
		digests = append(digests, info.Digest)
	}
	c, _ := NewCache(backend, t.TempDir(), 3000)
	for _, d := range digests[:3] {
		if _, err := c.Fetch(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	c.Fetch(ctx, digests[0]) // touch 0: now 1 is least recently used
	c.Fetch(ctx, digests[3]) // evicts 1
	if c.Contains(digests[1]) || !c.Contains(digests[0]) || !c.Contains(digests[3]) {
		t.Fatalf("unexpected cache content after eviction")
	}
	if c.Size() > 3000 {
		t.Fatalf("cache size %d exceeds limit", c.Size())
	}
}

func TestCacheRejectsCorruptBackend(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	l, _ := NewLocal(dir, false)
	info, _ := l.PutBytes(ctx, []byte("expected output"))
	// Corrupt the stored object behind the store's back.
	os.WriteFile(l.Path(info.Digest), []byte("tampered"), 0o644)
	c, _ := NewCache(l, t.TempDir(), 1<<20)
	if _, err := c.Fetch(ctx, info.Digest); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("expected digest mismatch, got %v", err)
	}
	if c.Contains(info.Digest) {
		t.Fatal("corrupt content must not be cached")
	}
}

func TestCacheReindexesOnRestart(t *testing.T) {
	ctx := context.Background()
	backend := NewMem()
	dir := t.TempDir()
	c1, _ := NewCache(backend, dir, 1<<20)
	info, _ := c1.PutBytes(ctx, []byte("persisted"))
	c2, err := NewCache(&countingStore{Store: NewMem()}, dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !c2.Contains(info.Digest) {
		t.Fatal("cache must index files from a previous run")
	}
	got, err := ReadAll(ctx, c2, info.Digest)
	if err != nil || string(got) != "persisted" {
		t.Fatalf("got %q %v", got, err)
	}
}

func TestCacheContract(t *testing.T) {
	c, err := NewCache(NewMem(), t.TempDir(), 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	storeContract(t, c)
}

func TestS3Contract(t *testing.T) {
	endpoint := os.Getenv("CMS_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("CMS_TEST_S3_ENDPOINT not set (S3/MinIO not available)")
	}
	s, err := NewS3(context.Background(), S3Config{
		Endpoint: endpoint, Bucket: "cms-test", AccessKey: os.Getenv("CMS_TEST_S3_ACCESS_KEY"),
		SecretKey: os.Getenv("CMS_TEST_S3_SECRET_KEY"), Prefix: "t" + Sum(randBytes(t, 8))[:12] + "/",
	})
	if err != nil {
		t.Fatal(err)
	}
	storeContract(t, s)
}

func BenchmarkLocalPutBytesDedupHit(b *testing.B) {
	s, _ := NewLocal(b.TempDir(), false)
	data := randBytes(b, 4096)
	s.PutBytes(context.Background(), data)
	b.ResetTimer()
	for b.Loop() {
		s.PutBytes(context.Background(), data)
	}
}

func BenchmarkCacheFetchHit(b *testing.B) {
	backend := NewMem()
	info, _ := backend.PutBytes(context.Background(), randBytes(b, 1<<20))
	c, _ := NewCache(backend, b.TempDir(), 64<<20)
	c.Fetch(context.Background(), info.Digest)
	b.ResetTimer()
	for b.Loop() {
		if _, err := c.Fetch(context.Background(), info.Digest); err != nil {
			b.Fatal(err)
		}
	}
}
