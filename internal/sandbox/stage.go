package sandbox

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// StageMount is where a Stage appears inside the sandbox.
const StageMount = "/stage"

// Stage is a host directory bound read-only (and noexec) into the sandbox at
// /stage. Files are hard-linked from the root-owned, read-only blob cache
// when possible, which makes large inputs zero-copy: the sandboxed program
// can read them but, because the mount is read-only and outside the box
// directory (which isolate re-owns on every run), never modify them.
type Stage struct {
	dir string
}

// NewStage creates (or empties) a staging directory.
func NewStage(dir string) (*Stage, error) {
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Stage{dir: dir}, nil
}

// Clear removes every staged file.
func (s *Stage) Clear() error {
	ents, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if err := os.RemoveAll(filepath.Join(s.dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// Add makes src available as /stage/name (hard link, or copy across filesystems).
func (s *Stage) Add(src, name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	dst := filepath.Join(s.dir, name)
	os.Remove(dst)
	if err := os.Link(src, dst); err == nil {
		return os.Chmod(dst, 0o444) // no-op for cache files (already 0444)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o444)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("stage %s: %w", name, err)
	}
	return nil
}

// Dir returns the mount rule for the sandbox.
func (s *Stage) Dir() Dir { return Dir{Inside: StageMount, Outside: s.dir, NoExec: true} }

// Path returns the in-sandbox path of a staged file.
func (s *Stage) Path(name string) string { return StageMount + "/" + name }
