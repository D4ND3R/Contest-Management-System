package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/D4ND3R/Contest-Management-System/internal/auth"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

// bootstrapAdmin creates the first administrator (role "all") when the
// username does not exist yet. It is a no-op without a password.
func bootstrapAdmin(ctx context.Context, env *ctlEnv, username, password string, stdout io.Writer) error {
	if password == "" {
		return nil
	}
	q := sqlc.New(env.deps.DB)
	if _, err := q.GetAdminByUsername(ctx, username); err == nil {
		fmt.Fprintf(stdout, "admin %q already exists\n", username)
		return nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	if _, err := q.CreateAdmin(ctx, sqlc.CreateAdminParams{Name: username, Username: username, PasswordHash: hash, Enabled: true, Role: "all"}); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "created admin %q\n", username)
	return nil
}
