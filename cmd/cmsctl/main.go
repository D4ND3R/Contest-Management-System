// Command cmsctl is the administrative CLI: contests, users, imports,
// exports, reevaluations, dump/restore and bootstrap. It is equivalent to
// running "cms ctl".
package main

import (
	"os"

	"github.com/D4ND3R/Contest-Management-System/internal/cli"
)

func main() {
	os.Exit(cli.Main(append([]string{"ctl"}, os.Args[1:]...), os.Stdout, os.Stderr))
}
