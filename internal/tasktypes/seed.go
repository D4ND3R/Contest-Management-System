package tasktypes

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/sandbox"
)

// SeedCache builds and stores compile seeds (see langs.CompileSeed), one
// per language and worker. Seeds are built lazily by the first compilation
// that needs them.
type SeedCache struct {
	dir string
	mu  sync.Mutex
	// built maps language id -> seed directory ("" when building failed).
	built map[string]string
}

// NewSeedCache stores seeds under dir.
func NewSeedCache(dir string) (*SeedCache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &SeedCache{dir: dir, built: map[string]string{}}, nil
}

// get returns the seed directory for lang, building it on first use. A
// failed build is not retried and compilation proceeds without a seed.
func (s *SeedCache) get(ctx context.Context, env *Env, lang *langs.Language) string {
	if s == nil || lang.CompileSeed == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.built[lang.ID]; ok {
		return p
	}
	p, err := s.build(ctx, env, lang)
	if err != nil {
		env.Log.Warn("compile seed build failed; compiling without it", "language", lang.ID, "error", err)
		p = ""
	}
	s.built[lang.ID] = p
	return p
}

func (s *SeedCache) build(ctx context.Context, env *Env, lang *langs.Language) (string, error) {
	cs := lang.CompileSeed
	box, err := env.box(ctx, 2)
	if err != nil {
		return "", err
	}
	if err := box.WriteFile(cs.WarmupFile, []byte(cs.Warmup), 0o644); err != nil {
		return "", err
	}
	if err := box.Mkdir(cs.Dir); err != nil {
		return "", err
	}
	main := strings.TrimSuffix(cs.WarmupFile, filepath.Ext(cs.WarmupFile))
	vars := langs.Vars{Sources: []string{cs.WarmupFile}, MainSource: cs.WarmupFile, Main: main, Executable: lang.ExecutableName(main)}
	cl := lang.CompileLimits
	start := time.Now()
	for _, tmpl := range lang.Compile {
		res, err := box.Run(ctx, &sandbox.Spec{
			Args: langs.Expand(tmpl, vars), Env: lang.EnvList(), Dirs: env.dirs(lang),
			Limits: sandbox.Limits{CPUTime: 4 * cl.Time.D(), WallTime: 8*cl.Time.D() + time.Second,
				Memory: int64(cl.Memory), Processes: cl.Processes, FileSize: int64(cl.Output), OpenFiles: 512},
		})
		if err != nil {
			return "", err
		}
		if res.Status != sandbox.StatusOK {
			return "", fmt.Errorf("warm-up compilation: %s", res.Status)
		}
	}
	dst := filepath.Join(s.dir, lang.ID)
	os.RemoveAll(dst)
	if err := copyTree(box.Path(cs.Dir), dst); err != nil {
		return "", err
	}
	env.Log.Info("compile seed ready", "language", lang.ID, "took", time.Since(start))
	return dst, nil
}

// copyTree copies regular files and directories (never symlinks or special
// files) from src to dst.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type().IsRegular():
			return copyFile(p, target)
		default:
			return nil // symlinks, fifos, devices: skipped
		}
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}
