// Command cms is the single binary for every CMS service. The first
// argument selects the service:
//
//	cms contest-web   contestant portal (CWS)
//	cms admin-web     administration (AWS)
//	cms ranking-web   public live scoreboard (RWS)
//	cms dispatcher    evaluation + scoring orchestration
//	cms worker        sandboxed compilation/evaluation
//	cms monitor       worker heartbeats and stuck-job recovery
//	cms printing      print queue
//	cms ctl ...       administrative CLI (same as the cmsctl binary)
//	cms migrate       apply database migrations
//	cms version
package main

import (
	"os"

	"github.com/D4ND3R/Contest-Management-System/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr))
}
