package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/deps"
	"github.com/D4ND3R/Contest-Management-System/internal/logging"
)

func init() {
	ctlCommands["blobs-gc"] = ctlCommand{"delete blobs no longer referenced by any row", cmdBlobsGC}
}

func cmdBlobsGC(args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("blobs-gc", stderr)
	grace := fs.Duration("grace", 24*time.Hour, "only delete blobs registered longer ago than this")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	d, err := deps.Open(ctx, cfg, logging.Discard(), deps.Need{DB: true, Blobs: true})
	if err != nil {
		return err
	}
	defer d.Close()
	n, size, err := blob.GC(ctx, d.Blobs, sqlc.New(d.DB), time.Now().Add(-*grace), 500)
	fmt.Fprintf(stdout, "deleted %d blobs (%d bytes)\n", n, size)
	return err
}
