package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/D4ND3R/Contest-Management-System/internal/auditlog"
	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/deps"
	"github.com/D4ND3R/Contest-Management-System/internal/logging"
)

func init() {
	ctlCommands["audit-verify"] = ctlCommand{"check the audit log's hash chain (tampering, removed or reordered entries)", cmdAuditVerify}
}

func cmdAuditVerify(args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("audit-verify", stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	d, err := deps.Open(ctx, cfg, logging.Discard(), deps.Need{DB: true})
	if err != nil {
		return err
	}
	defer d.Close()
	rep, err := auditlog.Verify(ctx, sqlc.New(d.DB))
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%d entries; head: entry %d, hash %s\n", rep.Entries, rep.HeadSeq, rep.HeadHash)
	if rep.OK() {
		fmt.Fprintln(stdout, "OK: the chain is intact. Compare the head hash with one written down earlier to prove nothing before it changed.")
		return nil
	}
	for _, p := range rep.Problems {
		fmt.Fprintln(stdout, "  -", p)
	}
	if rep.More > 0 {
		fmt.Fprintf(stdout, "  ... and %d more\n", rep.More)
	}
	return fmt.Errorf("the audit log's chain is broken (%d problems)", len(rep.Problems)+rep.More)
}
