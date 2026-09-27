package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestDocsLinks checks that every relative link in the documentation (and
// the README) points at an existing file, and that every English page has
// a Spanish counterpart listed in the Spanish index.
func TestDocsLinks(t *testing.T) {
	root := filepath.Join("..", "..")
	link := regexp.MustCompile(`\]\(([^)\s]+)\)`)
	fence := regexp.MustCompile("(?s)```.*?```")
	span := regexp.MustCompile("`[^`\n]*`")
	var pages []string
	filepath.Walk(filepath.Join(root, "docs"), func(p string, info os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(p, ".md") {
			pages = append(pages, p)
		}
		return err
	})
	pages = append(pages, filepath.Join(root, "README.md"))
	for _, p := range pages {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		// Code blocks and spans show syntax, not links.
		text := fence.ReplaceAllString(string(b), "")
		text = span.ReplaceAllString(text, "")
		for _, m := range link.FindAllStringSubmatch(text, -1) {
			target := m[1]
			if strings.Contains(target, "://") || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			target, _, _ = strings.Cut(target, "#")
			if _, err := os.Stat(filepath.Join(filepath.Dir(p), target)); err != nil {
				t.Errorf("%s: broken link %s", p, m[1])
			}
		}
	}
	en, _ := filepath.Glob(filepath.Join(root, "docs", "en", "*.md"))
	es, _ := filepath.Glob(filepath.Join(root, "docs", "es", "*.md"))
	if len(en) != len(es) {
		t.Errorf("%d English pages but %d Spanish ones", len(en), len(es))
	}
	for _, dir := range []string{"en", "es"} {
		index, _ := os.ReadFile(filepath.Join(root, "docs", dir, "README.md"))
		pages, _ := filepath.Glob(filepath.Join(root, "docs", dir, "*.md"))
		for _, p := range pages {
			if name := filepath.Base(p); name != "README.md" && !strings.Contains(string(index), "("+name+")") {
				t.Errorf("docs/%s/README.md does not list %s", dir, name)
			}
		}
	}
}
