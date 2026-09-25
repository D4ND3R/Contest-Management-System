package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

// TestIsolate returns an Isolate for integration tests, skipping the test
// when isolate is unavailable or the process is not root. Set
// CMS_SANDBOX_TESTS=1 to turn the skip into a failure (CI sandbox job).
// CMS_TEST_ISOLATE_CG=0 disables control groups (hosts without cgroups).
//
// Tests that judge hold a machine-wide lock until they end: test packages
// run in parallel, and judging on shared cores makes wall-clock limits
// expire and verdicts change (the very thing the judge must never do).
func TestIsolate(t testing.TB) *Isolate {
	t.Helper()
	path := os.Getenv("CMS_TEST_ISOLATE")
	if path == "" {
		path, _ = exec.LookPath("isolate")
	}
	required := os.Getenv("CMS_SANDBOX_TESTS") == "1"
	if path == "" || os.Geteuid() != 0 {
		if required {
			t.Fatal("CMS_SANDBOX_TESTS=1 but isolate is missing or not running as root")
		}
		t.Skip("isolate not available or not root; sandbox tests skipped")
	}
	judgeLock(t)
	root := os.Getenv("CMS_TEST_ISOLATE_BOX_ROOT")
	if root == "" {
		root = "/var/local/lib/isolate"
	}
	return &Isolate{Path: path, CG: os.Getenv("CMS_TEST_ISOLATE_CG") != "0", BoxRoot: root}
}

var (
	lockMu    sync.Mutex
	lockFile  *os.File
	lockCount int
	lockOwner = map[testing.TB]bool{}
)

// judgeLock takes the cross-process judging lock for t (reentrant within
// the process: a test may call TestIsolate several times).
func judgeLock(t testing.TB) {
	lockMu.Lock()
	defer lockMu.Unlock()
	if lockOwner[t] {
		return
	}
	if lockCount == 0 {
		f, err := os.OpenFile(filepath.Join(os.TempDir(), "cms-judging-tests.lock"), os.O_CREATE|os.O_RDWR, 0o666)
		if err != nil {
			t.Fatalf("judging lock: %v", err)
		}
		if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
			f.Close()
			t.Fatalf("judging lock: %v", err)
		}
		lockFile = f
	}
	lockCount++
	lockOwner[t] = true
	t.Cleanup(func() {
		lockMu.Lock()
		defer lockMu.Unlock()
		delete(lockOwner, t)
		if lockCount--; lockCount == 0 {
			unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
			lockFile.Close()
			lockFile = nil
		}
	})
}
