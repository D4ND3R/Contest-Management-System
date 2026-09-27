package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type memRemote struct {
	mu    sync.Mutex
	files map[string][]byte
}

func (m *memRemote) Upload(_ context.Context, name, path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[name] = b
	return nil
}

func (m *memRemote) Delete(_ context.Context, name string) error { delete(m.files, name); return nil }

func (m *memRemote) Download(_ context.Context, name, path string) error {
	m.mu.Lock()
	b, ok := m.files[name]
	m.mu.Unlock()
	if !ok {
		return ErrRemoteNotFound
	}
	return os.WriteFile(path, b, 0o600)
}

// TestWALArchive (SPEC_IOI §12): segments are stored locally and remotely,
// archiving twice is harmless, a different file under the same name is
// refused, names cannot escape the archive, and segments come back from
// the local copy or else from the remote.
func TestWALArchive(t *testing.T) {
	ctx := context.Background()
	dir, pg := t.TempDir(), t.TempDir()
	seg := filepath.Join(pg, "000000010000000000000001")
	os.WriteFile(seg, []byte("wal bytes"), 0o600)
	remote := &memRemote{files: map[string][]byte{}}
	if err := ArchiveWAL(ctx, dir, seg, "000000010000000000000001", remote); err != nil {
		t.Fatal(err)
	}
	if err := ArchiveWAL(ctx, dir, seg, "000000010000000000000001", remote); err != nil {
		t.Fatalf("second archive: %v", err)
	}
	if string(remote.files["wal/000000010000000000000001"]) != "wal bytes" {
		t.Fatalf("remote %v", remote.files)
	}
	other := filepath.Join(pg, "other")
	os.WriteFile(other, []byte("different"), 0o600)
	if err := ArchiveWAL(ctx, dir, other, "000000010000000000000001", remote); err == nil {
		t.Fatal("a different segment replaced an archived one")
	}
	for _, bad := range []string{"../x", "a/b", "", ".hidden"} {
		if err := ArchiveWAL(ctx, dir, seg, bad, nil); err == nil {
			t.Errorf("name %q accepted", bad)
		}
	}
	out := filepath.Join(pg, "RECOVERYXLOG")
	if err := RestoreWAL(ctx, dir, "000000010000000000000001", out, remote); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); string(b) != "wal bytes" {
		t.Fatalf("restored %q", b)
	}
	// Only in the remote (the local disk was lost).
	remote.files["wal/00000002.history"] = []byte("history")
	if err := RestoreWAL(ctx, dir, "00000002.history", out, remote); err != nil {
		t.Fatal(err)
	}
	if err := RestoreWAL(ctx, dir, "000000010000000000000009", out, remote); !errors.Is(err, ErrWALNotFound) {
		t.Fatalf("missing segment: %v", err)
	}
	if err := RestoreWAL(ctx, dir, "000000010000000000000009", out, nil); !errors.Is(err, ErrWALNotFound) {
		t.Fatalf("missing segment without remote: %v", err)
	}
}
