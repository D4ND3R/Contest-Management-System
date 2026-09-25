package cli

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/auth"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/testutil"
)

// TestFirstAdminAndRecovery: the installer's "bootstrap -generate-password"
// creates the first administrator with a random password printed once (and
// never again), and "admin-password" replaces a lost one.
func TestFirstAdminAndRecovery(t *testing.T) {
	ctx := context.Background()
	pool, dbURL := testutil.DBWithURL(t)
	t.Setenv("CMS_CONFIG", "")
	t.Setenv("CMS_DATABASE_URL", dbURL)
	t.Setenv("CMS_ADMIN_PASSWORD", "")
	run := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := Main(append([]string{"ctl"}, args...), &out, &errb)
		return code, out.String(), errb.String()
	}
	pwRe := regexp.MustCompile(`\(shown only now\): (\S+)`)
	check := func(pw string) {
		t.Helper()
		a, err := sqlc.New(pool).GetAdminByUsername(ctx, "admin")
		if err != nil || auth.VerifyPassword(a.PasswordHash, pw) != nil || !a.Enabled || a.Role != "all" {
			t.Fatalf("admin %+v cannot log in with %q (%v)", a, pw, err)
		}
	}

	code, out, errs := run("bootstrap", "-generate-password")
	m := pwRe.FindStringSubmatch(out)
	if code != 0 || m == nil || len(m[1]) != 16 {
		t.Fatalf("bootstrap = %d\n%s%s", code, out, errs)
	}
	check(m[1])
	// A second run (the installer is idempotent) keeps it and shows nothing.
	if code, out, _ := run("bootstrap", "-generate-password"); code != 0 || !strings.Contains(out, "already exists") || pwRe.MatchString(out) {
		t.Fatalf("second bootstrap = %d\n%s", code, out)
	}
	check(m[1])

	// Lost: a new random one, printed once; the old one stops working.
	code, out, errs = run("admin-password", "-username", "admin")
	m2 := pwRe.FindStringSubmatch(out)
	if code != 0 || m2 == nil || m2[1] == m[1] {
		t.Fatalf("admin-password = %d\n%s%s", code, out, errs)
	}
	check(m2[1])
	// Chosen through the environment (not the process list), never a default.
	t.Setenv("CMS_ADMIN_PASSWORD", "admin")
	if code, _, errs := run("admin-password"); code == 0 || !strings.Contains(errs, "default") {
		t.Fatalf("default password accepted: %s", errs)
	}
	t.Setenv("CMS_ADMIN_PASSWORD", "Tr0ub4dor-horse")
	if code, out, _ := run("admin-password"); code != 0 || pwRe.MatchString(out) {
		t.Fatalf("admin-password from the environment = %d\n%s", code, out)
	}
	check("Tr0ub4dor-horse")
	if code, _, errs := run("admin-password", "-username", "nobody"); code == 0 || !strings.Contains(errs, "no administrator") {
		t.Fatalf("unknown admin: %s", errs)
	}
}
