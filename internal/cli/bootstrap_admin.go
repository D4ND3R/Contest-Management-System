package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/D4ND3R/Contest-Management-System/internal/auth"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

func init() {
	ctlCommands["admin-password"] = ctlCommand{"set a new password for an administrator (random unless $CMS_ADMIN_PASSWORD), shown once", cmdAdminPassword}
}

// adminPassword is the password given for an administrator: the flag, or
// $CMS_ADMIN_PASSWORD (which, unlike a flag, other users of the machine
// cannot read in the process list), or a random one when generate is set.
func adminPassword(flagValue string, generate bool) (pw string, generated bool) {
	switch {
	case flagValue != "":
		return flagValue, false
	case os.Getenv("CMS_ADMIN_PASSWORD") != "":
		return os.Getenv("CMS_ADMIN_PASSWORD"), false
	case generate:
		return auth.RandomPassword(16), true
	}
	return "", false
}

// bootstrapAdmin creates the first administrator (role "all") when the
// username does not exist yet. It is a no-op without a password. A
// generated password is printed once, here, and stored nowhere else.
func bootstrapAdmin(ctx context.Context, env *ctlEnv, username, password string, generated bool, stdout io.Writer) error {
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
	if generated {
		fmt.Fprintf(stdout, "admin password (shown only now): %s\n", password)
	}
	return nil
}

func cmdAdminPassword(args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("admin-password", stderr)
	username := fs.String("username", "admin", "administrator whose password is replaced")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	env, err := openCtl(ctx, *cfgPath, false, stderr)
	if err != nil {
		return err
	}
	defer env.Close()
	q := sqlc.New(env.deps.DB)
	a, err := q.GetAdminByUsername(ctx, *username)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("no administrator %q", *username)
	} else if err != nil {
		return err
	}
	pw, generated := adminPassword("", true)
	if len(pw) < 8 || auth.IsDefaultPassword(a.Username, pw) {
		return errors.New("the password must have at least 8 characters and not be a well-known default nor the username")
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	// Enabled again too: this is how a locked-out installation recovers.
	if err := q.SetAdminPassword(ctx, sqlc.SetAdminPasswordParams{ID: a.ID, PasswordHash: hash}); err != nil {
		return err
	}
	if _, err := q.UpdateAdmin(ctx, sqlc.UpdateAdminParams{ID: a.ID, Name: a.Name, Username: a.Username, Enabled: true, Role: a.Role}); err != nil {
		return err
	}
	if err := q.InsertAuditLog(ctx, sqlc.InsertAuditLogParams{Action: "admin.password_cli", TargetType: "admin", TargetID: &a.ID,
		Details: []byte(fmt.Sprintf(`{"username":%q}`, a.Username))}); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "new password for %q set; its sessions were closed\n", a.Username)
	if generated {
		fmt.Fprintf(stdout, "password (shown only now): %s\n", pw)
	}
	return nil
}
