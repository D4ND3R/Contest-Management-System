package cli

import (
	"fmt"
	"io"
	"sort"
)

// ctlCommand is one "cmsctl" subcommand.
type ctlCommand struct {
	summary string
	run     func(args []string, stdout, stderr io.Writer) error
}

var ctlCommands = map[string]ctlCommand{}

func ctlUsage(w io.Writer) {
	names := make([]string, 0, len(ctlCommands))
	for n := range ctlCommands {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Fprintln(w, "usage: cmsctl <command> [flags]\n\ncommands:")
	for _, n := range names {
		fmt.Fprintf(w, "  %-22s %s\n", n, ctlCommands[n].summary)
	}
}

func ctlMain(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		ctlUsage(stdout)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	c, ok := ctlCommands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		ctlUsage(stderr)
		return 2
	}
	if err := c.run(args[1:], stdout, stderr); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}
