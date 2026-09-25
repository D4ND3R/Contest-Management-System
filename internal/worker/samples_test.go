package worker

import (
	"os"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/jobs"
	"github.com/D4ND3R/Contest-Management-System/internal/selftest"
)

// verdict maps a judged submission to the contest verdict categories.
func verdict(comp *jobs.Compilation, evs []jobs.Evaluation) string {
	return selftest.Verdict(comp, evs)
}

// toolchainAvailable reports whether the language's compiler (or
// interpreter) exists on this host.
func toolchainAvailable(h *harness, id string) bool { return selftest.ToolchainAvailable(h.lang(id)) }

// selectedLanguage honours CMS_SAMPLE_LANGUAGES (comma-separated ids).
func selectedLanguage(id string) bool {
	sel := os.Getenv("CMS_SAMPLE_LANGUAGES")
	if sel == "" {
		return true
	}
	for _, s := range strings.Split(sel, ",") {
		if strings.TrimSpace(s) == id {
			return true
		}
	}
	return false
}

// TestSampleSolutions judges AC/WA/TLE/MLE/RE/CE solutions in every
// configured language, twice, and requires identical verdicts (SPEC §9: F3
// exit criterion).
func TestSampleSolutions(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: compiles ~72 programs twice")
	}
	h := newHarnessSlots(t, 3)
	var langs []string
	for _, l := range selftest.SampleLanguages() {
		if selectedLanguage(l) {
			langs = append(langs, l)
		}
	}
	j := h.judge(3)
	first, skipped, err := j.RunSamples(t.Context(), langs)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) > 0 {
		if os.Getenv("CMS_REQUIRE_ALL_LANGUAGES") == "1" {
			t.Fatalf("toolchains not installed: %v", skipped)
		}
		t.Logf("toolchains not installed (skipped): %v", skipped)
	}
	report(t, first)
	second, _, err := j.RunSamples(t.Context(), langs)
	if err != nil {
		t.Fatal(err)
	}
	report(t, second)
	compareRuns(t, first, second, "CMS_SAMPLES_REPORT")
}
