// Command seed prepares the load test contest: contest "load" (running
// now, 1 hour long), the documented batch example "suma" as its task, and
// N contestants load0001… with the same password. It reads the usual CMS
// configuration (CMS_CONFIG / CMS_* variables).
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/auth"
	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/deps"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/logging"
	"github.com/D4ND3R/Contest-Management-System/internal/problempkg"
	"github.com/jackc/pgx/v5"
)

func main() {
	users := flag.Int("users", 500, "contestants to create")
	password := flag.String("password", "load-pw", "their password")
	pkg := flag.String("package", "docs/examples/packages/batch-suma", "problem package folder")
	minutes := flag.Int("minutes", 60, "contest length from now")
	flag.Parse()
	ctx := context.Background()
	cfg, err := config.Load(os.Getenv("CMS_CONFIG"))
	if err != nil {
		log.Fatal(err)
	}
	d, err := deps.Open(ctx, cfg, logging.Discard(), deps.Need{DB: true, Blobs: true})
	if err != nil {
		log.Fatal(err)
	}
	defer d.Close()
	q := sqlc.New(d.DB)
	now := time.Now()
	cp := db.NewContestParams("load", now.Add(-time.Minute), now.Add(time.Duration(*minutes)*time.Minute))
	cp.Description = "Load test"
	c, err := q.CreateContest(ctx, cp)
	if err != nil {
		log.Fatal("contest: ", err)
	}
	reg, err := langs.Load(cfg.LanguagesDir)
	if err != nil {
		log.Fatal(err)
	}
	zr, err := zipFolder(*pkg)
	if err != nil {
		log.Fatal(err)
	}
	p := problempkg.Read(zr, problempkg.OptionsFor(reg))
	if !p.OK() {
		log.Fatal("package: ", p.Errors)
	}
	if _, err := problempkg.Import(ctx, d.DB, d.Blobs, p, problempkg.ImportOptions{ContestID: &c.ID}); err != nil {
		log.Fatal("import: ", err)
	}
	// One hash for everybody: seeding stays fast, every login still pays
	// a full verification.
	hash, err := auth.HashPassword(*password)
	if err != nil {
		log.Fatal(err)
	}
	rows := make([][]any, *users)
	for i := range rows {
		rows[i] = []any{fmt.Sprintf("load%04d", i+1), "Load", fmt.Sprintf("%04d", i+1), hash, []string{}}
	}
	if _, err := d.DB.CopyFrom(ctx, pgx.Identifier{"users"}, []string{"username", "first_name", "last_name", "password_hash", "preferred_languages"},
		pgx.CopyFromRows(rows)); err != nil {
		log.Fatal("users: ", err)
	}
	if _, err := d.DB.Exec(ctx, `INSERT INTO participations (contest_id, user_id)
SELECT $1, id FROM users WHERE username LIKE 'load%'`, c.ID); err != nil {
		log.Fatal("participations: ", err)
	}
	fmt.Printf("contest %d (load), task %s, %d contestants\n", c.ID, p.Config.Name, *users)
}

func zipFolder(dir string) (*zip.Reader, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err := filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		w, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
}
