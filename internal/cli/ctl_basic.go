package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/deps"
	"github.com/D4ND3R/Contest-Management-System/internal/logging"
)

func init() {
	ctlCommands["migrate"] = ctlCommand{"apply pending database migrations", cmdMigrate}
	ctlCommands["healthcheck"] = ctlCommand{"GET a /healthz URL; exit 0 when healthy (container probes)", cmdHealthcheck}
	ctlCommands["bootstrap"] = ctlCommand{"migrate the database and create the first admin", cmdBootstrap}
}

// ctlEnv holds what most ctl commands need: parsed config and open deps.
type ctlEnv struct {
	cfg  *config.Config
	deps *deps.Deps
}

func (e *ctlEnv) Close() { e.deps.Close() }

// openCtl parses the common -config flag (already registered on fs by the
// caller) and opens the database (and Redis when needRedis).
func openCtl(ctx context.Context, cfgPath string, needRedis bool, stderr io.Writer) (*ctlEnv, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	log := logging.New(stderr, "cmsctl", "warn", "text")
	d, err := deps.Open(ctx, cfg, log, deps.Need{DB: true, Redis: needRedis})
	if err != nil {
		return nil, err
	}
	return &ctlEnv{cfg: cfg, deps: d}, nil
}

func newFlags(name string, stderr io.Writer) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs, fs.String("config", "", "configuration file (default $CMS_CONFIG)")
}

func cmdMigrate(args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("migrate", stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	env, err := openCtl(ctx, *cfgPath, false, stderr)
	if err != nil {
		return err
	}
	defer env.Close()
	applied, err := db.Migrate(ctx, env.deps.DB)
	for _, name := range applied {
		fmt.Fprintln(stdout, "applied", name)
	}
	if err == nil && len(applied) == 0 {
		fmt.Fprintln(stdout, "database is up to date")
	}
	return err
}

func cmdHealthcheck(args []string, stdout, stderr io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: cmsctl healthcheck <url>")
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(args[0])
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("unhealthy: %s %s", resp.Status, body)
	}
	return nil
}

func cmdBootstrap(args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("bootstrap", stderr)
	adminUser := fs.String("admin-username", "admin", "username of the first administrator")
	adminPass := fs.String("admin-password", "", "password of the first administrator (skipped when empty)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	env, err := openCtl(ctx, *cfgPath, true, stderr)
	if err != nil {
		return err
	}
	defer env.Close()
	applied, err := db.Migrate(ctx, env.deps.DB)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "migrations applied: %d\n", len(applied))
	return bootstrapAdmin(ctx, env, *adminUser, *adminPass, stdout)
}
