// Command gen writes internal/i18n/contestant.txt: every message the
// contestant and ranking sites can show, one per line (Go-quoted), which
// is what a new UI language must translate. Run with `go generate
// ./internal/i18n` (make generate); TestContestantMessagesUpToDate fails
// when the file is stale.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/D4ND3R/Contest-Management-System/internal/i18n/extract"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	keys, err := extract.Collect(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(root, "internal/i18n/contestant.txt"), extract.File(keys), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
