package blobserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/logging"
)

const token = "0123456789abcdef-token"

func start(t *testing.T, maxUpload int64) (*blob.Local, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := blob.NewLocal(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(store, token, maxUpload, logging.Discard())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan net.Addr, 1)
	done := make(chan struct{})
	go func() { srv.Run(ctx, "127.0.0.1:0", ready); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return store, "http://" + (<-ready).String()
}

// TestBlobServerRoundTrip: a worker on another machine stores and reads
// blobs through the server; missing, unauthenticated, oversized and
// corrupted content is refused.
func TestBlobServerRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, url := start(t, 1<<20)
	c := blob.NewHTTP(url, token)
	info, err := c.PutBytes(ctx, []byte("salida del programa"))
	if err != nil || !info.Created || info.Size != 19 {
		t.Fatalf("put %+v %v", info, err)
	}
	if again, err := c.Put(ctx, strings.NewReader("salida del programa")); err != nil || again.Created || again.Digest != info.Digest {
		t.Fatalf("second put %+v %v", again, err)
	}
	if got, err := blob.ReadAll(ctx, store, info.Digest); err != nil || string(got) != "salida del programa" {
		t.Fatalf("server store %q %v", got, err)
	}
	if got, err := blob.ReadAll(ctx, c, info.Digest); err != nil || string(got) != "salida del programa" {
		t.Fatalf("read back %q %v", got, err)
	}
	if n, err := c.Stat(ctx, info.Digest); err != nil || n != 19 {
		t.Fatalf("stat %d %v", n, err)
	}
	missing := blob.Sum([]byte("nope"))
	if _, err := c.Open(ctx, missing); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("missing = %v", err)
	}
	if _, err := c.Stat(ctx, missing); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("stat missing = %v", err)
	}
	if err := c.Delete(ctx, info.Digest); !errors.Is(err, blob.ErrReadOnly) {
		t.Fatalf("delete = %v", err)
	}
	if _, err := blob.NewHTTP(url, "wrong-token-wrong-token").Open(ctx, info.Digest); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("bad token = %v", err)
	}
	if _, err := c.Put(ctx, bytes.NewReader(make([]byte, 2<<20))); err == nil || !strings.Contains(err.Error(), "413") {
		t.Fatalf("oversized upload = %v", err)
	}
	// A corrupted file on the server is detected by the reader.
	os.WriteFile(store.Path(info.Digest), []byte("salida del programX"), 0o644)
	if _, err := io.ReadAll(must(c.Open(ctx, info.Digest))); !errors.Is(err, blob.ErrDigestMismatch) {
		t.Fatalf("corrupted = %v", err)
	}
	// The worker's local cache works on top of it.
	good, _ := c.PutBytes(ctx, []byte("caso de prueba"))
	cache, err := blob.NewCache(c, filepath.Join(t.TempDir(), "cache"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	p, err := cache.Fetch(ctx, good.Digest)
	if b, _ := os.ReadFile(p); err != nil || string(b) != "caso de prueba" {
		t.Fatalf("cache fetch %q %v", b, err)
	}
	if _, err := New(store, "short", 0, logging.Discard()); err == nil {
		t.Fatal("a short token was accepted")
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
