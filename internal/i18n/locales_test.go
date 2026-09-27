package i18n_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/i18n"
	"github.com/D4ND3R/Contest-Management-System/internal/i18n/extract"
)

// TestContestantMessagesUpToDate: contestant.txt lists exactly the
// messages the contestant and ranking sites use (make generate).
func TestContestantMessagesUpToDate(t *testing.T) {
	keys, err := extract.Collect(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("contestant.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, extract.File(keys)) {
		t.Fatal("internal/i18n/contestant.txt is stale: run make generate")
	}
	for _, k := range keys {
		if !i18n.Has("es", k) {
			t.Errorf("%q has no Spanish translation", k)
		}
	}
}

// TestShippedLocales: every shipped language translates every message,
// with the same placeholders, and nothing else.
func TestShippedLocales(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("locales", "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatal("no locales", err)
	}
	known := map[string]bool{}
	for _, m := range i18n.ContestantMessages() {
		known[m] = true
	}
	for _, f := range files {
		code := strings.TrimSuffix(filepath.Base(f), ".yaml")
		data, _ := os.ReadFile(f)
		l, warnings, err := i18n.ParseLocale(data)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		for _, w := range warnings {
			t.Errorf("%s: %s", f, w)
		}
		var missing []string
		for _, m := range i18n.ContestantMessages() {
			if l.Messages[m] == "" {
				missing = append(missing, m)
			}
		}
		if len(missing) > 0 {
			t.Errorf("%s: %d messages untranslated, e.g. %q", f, len(missing), missing[0])
		}
		for k := range l.Messages {
			if !known[k] {
				t.Errorf("%s: %q is not a message", f, k)
			}
		}
		wantDir := map[bool]string{true: "rtl", false: "ltr"}[code == "ar" || code == "fa" || code == "he"]
		if l.Dir != wantDir || i18n.Dir(code) != wantDir {
			t.Errorf("%s: dir %s", f, l.Dir)
		}
	}
}
