package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/sandbox"
)

// TestVerifyHost runs scripts/verify-host.sh (SPEC_CLOSE A5) with the real
// sandbox: the environment checks, then the judge self-test twice through
// the cms binary. A host without isolate must be reported as a failure.
func TestVerifyHost(t *testing.T) {
	iso := sandbox.TestIsolate(t)
	if testing.Short() {
		t.Skip("slow: judges the security battery twice")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "cms")
	build := exec.Command("go", "build", "-o", bin, "./cmd/cms")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	script := filepath.Join("..", "..", "scripts", "verify-host.sh")
	run := func(args ...string) (int, string) {
		cmd := exec.Command("bash", append([]string{script}, args...)...)
		// One judging core and boxes no other test package uses (the
		// sandbox tests use 900-903).
		cmd.Env = append(os.Environ(), "CMS_CONFIG=", "CMS_LANGUAGES_DIR="+filepath.Join("..", "..", "config", "languages"),
			"CMS_WORKER_CORES="+strconv.Itoa(sandbox.DefaultCores()[0]), "CMS_ISOLATE_PATH="+iso.Path)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return code, out.String()
	}
	code, out := run("--cms", bin, "--isolate", iso.Path, "--languages", "c11", "--runs", "2", "--box", "998", "--box-offset", "950")
	if code != 0 || !strings.Contains(out, "RESULT: OK") || !strings.Contains(out, "run 2 of 2") ||
		!strings.Contains(out, "[ OK ] isolate --cg runs a program") || strings.Contains(out, "[FAIL]") {
		t.Fatalf("verify-host = %d:\n%s", code, out)
	}
	for _, want := range []string{"fork_bomb", "network", "privilege_escalation", "c11/tle"} {
		if !strings.Contains(out, want) {
			t.Errorf("self-test output lacks %s", want)
		}
	}
	code, out = run("--skip-judge", "--isolate", filepath.Join(dir, "missing-isolate"), "--box", "998")
	if code != 1 || !strings.Contains(out, "[FAIL] isolate is not installed") || !strings.Contains(out, "do NOT start the contest") ||
		!strings.Contains(out, "fix: sudo scripts/install-isolate.sh") {
		t.Fatalf("missing isolate = %d:\n%s", code, out)
	}
}
