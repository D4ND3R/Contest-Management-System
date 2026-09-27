package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/langs"
)

// TestProbeToolchains: the first non-empty line of each language's
// version_command, from standard output or error; "" when it is missing.
func TestProbeToolchains(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"out.yaml":     `{id: out, name: Out, version_command: ["sh", "-c", "echo; echo 'tool 1.2 (build 7)'; echo more"], source_extensions: [".o"], run: ["/bin/true"], executable: "x"}`,
		"err.yaml":     `{id: err, name: Err, version_command: ["sh", "-c", "echo 'jdk 21' >&2"], source_extensions: [".e"], run: ["/bin/true"], executable: "x"}`,
		"missing.yaml": `{id: missing, name: Missing, version_command: ["/nonexistent/cc", "--version"], source_extensions: [".m"], run: ["/bin/true"], executable: "x"}`,
		"none.yaml":    `{id: none, name: None, source_extensions: [".n"], run: ["/bin/true"], executable: "x"}`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg, err := langs.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := ProbeToolchains(context.Background(), reg)
	want := map[string]string{"out": "tool 1.2 (build 7)", "err": "jdk 21", "missing": ""}
	if len(got) != len(want) {
		t.Fatalf("toolchains %q", got)
	}
	for id, v := range want {
		if got[id] != v {
			t.Errorf("%s = %q, want %q", id, got[id], v)
		}
	}
}
