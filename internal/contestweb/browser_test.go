package contestweb

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestContestantUIInBrowser runs testdata/a11y.js in headless Chromium:
// the editor, the checks before sending, keyboard use and the right-to-left
// and high-contrast displays. It needs node with Playwright and a Chromium
// it can launch; elsewhere it is skipped.
func TestContestantUIInBrowser(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	env := os.Environ()
	if out, err := exec.Command("npm", "root", "-g").Output(); err == nil {
		env = append(env, "NODE_PATH="+strings.TrimSpace(string(out)))
	}
	probe := exec.Command(node, "-e", "require('playwright')")
	probe.Env = env
	if probe.Run() != nil {
		t.Skip("Playwright is not installed")
	}
	f := newFixture(t, fixtureOpts{})
	cmd := exec.Command(node, "testdata/a11y.js")
	cmd.Env = append(env, "CWS_URL="+f.url)
	out, err := cmd.CombinedOutput()
	if strings.Contains(string(out), "Executable doesn't exist") || strings.Contains(string(out), "Failed to launch") {
		t.Skip("Playwright cannot launch Chromium here")
	}
	if err != nil || strings.TrimSpace(string(out)) != "PASS" {
		t.Fatalf("%v\n%s", err, out)
	}
}
