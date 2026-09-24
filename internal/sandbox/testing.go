package sandbox

import (
	"os"
	"os/exec"
	"testing"
)

// TestIsolate returns an Isolate for integration tests, skipping the test
// when isolate is unavailable or the process is not root. Set
// CMS_SANDBOX_TESTS=1 to turn the skip into a failure (CI sandbox job).
// CMS_TEST_ISOLATE_CG=0 disables control groups (hosts without cgroups).
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
	root := os.Getenv("CMS_TEST_ISOLATE_BOX_ROOT")
	if root == "" {
		root = "/var/local/lib/isolate"
	}
	return &Isolate{Path: path, CG: os.Getenv("CMS_TEST_ISOLATE_CG") != "0", BoxRoot: root}
}
