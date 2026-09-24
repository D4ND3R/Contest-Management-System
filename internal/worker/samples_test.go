package worker

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
)

// verdict maps a judged submission to the contest verdict categories.
func verdict(comp *jobs.Compilation, evs []jobs.Evaluation) string {
	if comp != nil && !comp.Success {
		return "CE"
	}
	if len(evs) == 0 {
		return "?"
	}
	e := evs[0]
	switch e.ExitStatus {
	case "ok":
		if e.Outcome >= 1 {
			return "AC"
		}
		return "WA"
	case "timeout", "timeout_wall":
		return "TLE"
	case "memory":
		return "MLE"
	case "nonzero", "signal":
		return "RE"
	case "output_limit":
		return "OLE"
	}
	return "SE"
}

var sampleKinds = []struct{ file, want string }{
	{"ac", "AC"}, {"wa", "WA"}, {"tle", "TLE"}, {"mle", "MLE"}, {"re", "RE"}, {"ce", "CE"},
}

// toolchainAvailable reports whether the language's compiler (or
// interpreter) exists on this host.
func toolchainAvailable(h *harness, id string) bool {
	l := h.lang(id)
	cmd := l.Run
	if len(l.Compile) > 0 {
		cmd = l.Compile[0]
	}
	bin := cmd[0]
	if bin == "/usr/bin/env" && len(cmd) > 1 {
		_, err := exec.LookPath(cmd[1])
		if err != nil {
			_, err = exec.LookPath("/usr/local/go/bin/" + cmd[1])
		}
		return err == nil
	}
	_, err := os.Stat(bin)
	return err == nil
}

type sampleResult struct {
	lang, kind, verdict, detail string
}

// runSamples judges every sample solution of every language.
func runSamples(t *testing.T, h *harness) map[string]string {
	dirs, err := os.ReadDir(filepath.Join("testdata", "samples"))
	if err != nil {
		t.Fatal(err)
	}
	lim := jobs.Limits{TimeMs: 2000, MemoryBytes: 256 << 20, OutputBytes: 16 << 20, Processes: 1}
	var mu sync.Mutex
	results := map[string]string{}
	var rows []sampleResult
	t.Run("languages", func(t *testing.T) {
		for _, d := range dirs {
			lang := d.Name()
			t.Run(lang, func(t *testing.T) {
				if !toolchainAvailable(h, lang) {
					if os.Getenv("CMS_REQUIRE_ALL_LANGUAGES") == "1" {
						t.Fatalf("toolchain for %s not installed", lang)
					}
					t.Skipf("toolchain for %s not installed", lang)
				}
				t.Parallel()
				files, _ := filepath.Glob(filepath.Join("testdata", "samples", lang, "*"))
				ext := filepath.Ext(files[0])
				for _, k := range sampleKinds {
					src, err := os.ReadFile(filepath.Join("testdata", "samples", lang, k.file+ext))
					if err != nil {
						t.Fatalf("missing sample %s/%s%s", lang, k.file, ext)
					}
					start := time.Now()
					tc := h.testcase(1, "20 22\n", "42\n")
					comp, evs, err := h.compileAndRun("Batch", batchJob{
						lang: lang, files: map[string][]byte{"sol" + ext: src}, limits: lim,
					}, []jobs.Testcase{tc})
					if err != nil {
						t.Errorf("%s/%s: infrastructure error: %v", lang, k.file, err)
						continue
					}
					got := verdict(comp, evs)
					detail := ""
					if comp != nil && !comp.Success {
						detail = firstLine(comp.Stderr + comp.Stdout)
					} else if len(evs) > 0 {
						detail = fmt.Sprintf("%s time=%.2fs mem=%dMiB", evs[0].ExitStatus, evs[0].Time, evs[0].Memory>>20)
					}
					if got != k.want {
						t.Errorf("%s/%s: verdict %s (%s), want %s; compile: %s", lang, k.file, got, detail, k.want, compileInfo(comp))
					}
					mu.Lock()
					results[lang+"/"+k.file] = got
					rows = append(rows, sampleResult{lang, k.file, got, fmt.Sprintf("%s (%.1fs)", detail, time.Since(start).Seconds())})
					mu.Unlock()
				}
			})
		}
	})
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].lang != rows[j].lang {
			return rows[i].lang < rows[j].lang
		}
		return rows[i].kind < rows[j].kind
	})
	for _, r := range rows {
		t.Logf("%-8s %-3s %-3s %s", r.lang, r.kind, r.verdict, r.detail)
	}
	return results
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 100 {
		s = s[:100]
	}
	return s
}

func compileInfo(c *jobs.Compilation) string {
	if c == nil {
		return "-"
	}
	return fmt.Sprintf("%s time=%.2fs %s", c.Text, c.Time, firstLine(c.Stderr+c.Stdout))
}

// TestSampleSolutions judges AC/WA/TLE/MLE/RE/CE solutions in every
// configured language, twice, and requires identical verdicts (SPEC §9: F3
// exit criterion).
func TestSampleSolutions(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: compiles ~72 programs twice")
	}
	h := newHarnessSlots(t, 3)
	first := runSamples(t, h)
	second := runSamples(t, h)
	var keys []string
	for k := range first {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var report strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&report, "| %s | %s | %s |\n", k, first[k], second[k])
		if first[k] != second[k] {
			t.Errorf("%s: verdict changed between runs: %s vs %s", k, first[k], second[k])
		}
	}
	t.Logf("sample suite (solution | run 1 | run 2):\n%s", report.String())
	if out := os.Getenv("CMS_SAMPLES_REPORT"); out != "" {
		os.WriteFile(out, []byte(report.String()), 0o644)
	}
}
