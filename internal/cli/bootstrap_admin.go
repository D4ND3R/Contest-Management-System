package cli

import (
	"context"
	"io"
)

// bootstrapAdmin creates the first administrator (implemented with the
// data model in F1).
func bootstrapAdmin(ctx context.Context, env *ctlEnv, username, password string, stdout io.Writer) error {
	return nil
}
