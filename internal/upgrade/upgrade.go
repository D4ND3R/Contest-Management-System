// Package upgrade replaces the installed release by another one (cmsctl
// upgrade). Releases live side by side under Root/releases/<version> and
// Root/current points at the one in use (scripts/install.sh lays them out
// so; /usr/local/bin/cms and cmsctl point through current).
//
// The upgrade refuses to start during a contest (unless forced), downloads
// the release and verifies its SHA-256 checksum, takes a backup, stops the
// services, switches current, applies the migrations with the new binary,
// starts the services and waits for them to be healthy. If anything fails
// once the services were stopped, it switches back, restores the backup
// (migrations are forward-only: the old release cannot run on a newer
// schema) and starts the previous release again.
package upgrade

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Steps are the parts of an upgrade that touch the running installation.
// Running, Backup, Restore and Migrate are nil on a machine without the
// database (a remote worker): its upgrade only switches the release and
// restarts the services.
type Steps struct {
	// Running lists the contests in progress (one line each).
	Running func(ctx context.Context) ([]string, error)
	// Backup takes a backup and returns where it is.
	Backup func(ctx context.Context) (string, error)
	// Restore puts a backup taken by Backup back, replacing the database.
	Restore func(ctx context.Context, backup string) error
	Stop    func(ctx context.Context) error
	Start   func(ctx context.Context) error
	// Migrate applies the migrations with the given cms binary.
	Migrate func(ctx context.Context, binary string) error
	// Health returns nil once every service answers.
	Health func(ctx context.Context) error
}

// Options of an upgrade.
type Options struct {
	Root       string // installation root, /opt/cms
	ReleaseURL string // https://github.com/OWNER/REPO/releases (or a mirror)
	Version    string // "latest" or X.Y.Z
	Archive    string // a local release tarball instead of downloading (checksums.txt next to it)
	Arch       string // amd64 or arm64
	Current    string // version of the running installation
	Force      bool   // even during a contest
	Reinstall  bool   // even when Version is the current one
	Keep       int    // releases kept on disk (default 3)
	Client     *http.Client
	Out        io.Writer
}

// Run upgrades the installation; it returns nil when it is up to date.
func Run(ctx context.Context, o Options, s Steps) error {
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 10 * time.Minute}
	}
	if o.Out == nil {
		o.Out = io.Discard
	}
	if o.Keep < 2 {
		o.Keep = 3
	}
	say := func(format string, args ...any) { fmt.Fprintf(o.Out, format+"\n", args...) }

	prev, err := os.Readlink(filepath.Join(o.Root, "current"))
	if err != nil {
		return fmt.Errorf("no %s: this installation predates releases; run the installer once (see docs/en/deployment.md)", filepath.Join(o.Root, "current"))
	}
	target, name, err := o.target(ctx)
	if err != nil {
		return err
	}
	if target == o.Current && !o.Reinstall {
		say("CMS %s is already installed.", target)
		return nil
	}
	if newer(o.Current, target) {
		return fmt.Errorf("%s is older than the installed %s: migrations only go forward (to go back, restore a backup taken with %s)", target, o.Current, target)
	}
	database := s.Running != nil && s.Backup != nil && s.Restore != nil && s.Migrate != nil
	var running []string
	if database {
		if running, err = s.Running(ctx); err != nil {
			return fmt.Errorf("checking for contests in progress: %w", err)
		}
	}
	if len(running) > 0 {
		if !o.Force {
			return fmt.Errorf("a contest is in progress (%s): upgrade after it, or pass -force to stop the services anyway", strings.Join(running, "; "))
		}
		say("warning: contests in progress (%s): upgrading anyway (-force)", strings.Join(running, "; "))
	}

	// Everything that can fail without touching the services first.
	say("Upgrading CMS %s -> %s", o.Current, target)
	dir, err := o.fetch(ctx, target, name, say)
	if err != nil {
		return err
	}
	rel, _ := filepath.Rel(o.Root, dir)
	bk := ""
	if database {
		say("Backing up...")
		if bk, err = s.Backup(ctx); err != nil {
			return fmt.Errorf("backup before upgrading (nothing was changed): %w", err)
		}
		say("  %s", bk)
	} else {
		say("No database on this machine (a worker): no contest check, backup or migrations.")
	}

	say("Stopping the services...")
	if err := s.Stop(ctx); err != nil {
		// Nothing was switched: start whatever stopped again.
		_ = s.Start(ctx)
		return fmt.Errorf("stopping the services (nothing was changed): %w", err)
	}
	rollback := func(stage string, cause error) error {
		say("Upgrade failed while %s: %v", stage, cause)
		say("Rolling back to %s...", prev)
		var problems []string
		if err := s.Stop(ctx); err != nil {
			problems = append(problems, "stop: "+err.Error())
		}
		if err := switchTo(o.Root, prev); err != nil {
			problems = append(problems, "switching back: "+err.Error())
		}
		if bk != "" {
			if err := s.Restore(ctx, bk); err != nil {
				problems = append(problems, "restoring "+bk+": "+err.Error())
			}
		}
		if err := s.Start(ctx); err != nil {
			problems = append(problems, "start: "+err.Error())
		} else if err := s.Health(ctx); err != nil {
			problems = append(problems, "health: "+err.Error())
		}
		switch {
		case len(problems) > 0 && bk == "":
			return fmt.Errorf("upgrade to %s failed (%s: %v) and the rollback did not complete (%s)",
				target, stage, cause, strings.Join(problems, "; "))
		case len(problems) > 0:
			return fmt.Errorf("upgrade to %s failed (%s: %v) and the rollback did not complete (%s): restore %s by hand (docs/en/backups.md)",
				target, stage, cause, strings.Join(problems, "; "), bk)
		case bk == "":
			return fmt.Errorf("upgrade to %s failed (%s: %v); rolled back to %s", target, stage, cause, prev)
		}
		return fmt.Errorf("upgrade to %s failed (%s: %v); rolled back to %s, database restored from %s", target, stage, cause, prev, bk)
	}
	if err := switchTo(o.Root, rel); err != nil {
		return rollback("switching to "+rel, err)
	}
	if database {
		say("Applying the migrations...")
		if err := s.Migrate(ctx, filepath.Join(dir, "cms")); err != nil {
			return rollback("migrating", err)
		}
	}
	say("Starting the services...")
	if err := s.Start(ctx); err != nil {
		return rollback("starting", err)
	}
	if err := s.Health(ctx); err != nil {
		return rollback("waiting for the services", err)
	}
	prune(o.Root, o.Keep, say)
	if bk == "" {
		say("CMS %s is running (the previous release stays in %s).", target, filepath.Join(o.Root, prev))
	} else {
		say("CMS %s is running (the previous release stays in %s; the backup in %s).", target, filepath.Join(o.Root, prev), bk)
	}
	return nil
}

// target resolves the version to install and its tarball's name.
func (o *Options) target(ctx context.Context) (string, string, error) {
	if o.Archive != "" {
		name := filepath.Base(o.Archive)
		v, arch, ok := parseName(name)
		if !ok {
			return "", "", fmt.Errorf("%s is not a CMS release tarball (cms_VERSION_linux_ARCH.tar.gz)", name)
		}
		if arch != o.Arch {
			return "", "", fmt.Errorf("%s is for %s, this machine is %s", name, arch, o.Arch)
		}
		return v, name, nil
	}
	v := strings.TrimPrefix(o.Version, "v")
	if v == "" || v == "latest" {
		req, _ := http.NewRequestWithContext(ctx, "GET", o.ReleaseURL+"/latest", nil)
		resp, err := o.Client.Do(req)
		if err != nil {
			return "", "", fmt.Errorf("finding the latest release: %w", err)
		}
		resp.Body.Close()
		v = strings.TrimPrefix(path.Base(resp.Request.URL.Path), "v")
		if _, ok := semver(v); !ok || resp.StatusCode != http.StatusOK {
			return "", "", fmt.Errorf("no release found at %s (%s)", o.ReleaseURL, resp.Request.URL)
		}
	}
	if _, ok := semver(v); !ok {
		return "", "", fmt.Errorf("%q is not a version (X.Y.Z)", o.Version)
	}
	return v, fmt.Sprintf("cms_%s_linux_%s.tar.gz", v, o.Arch), nil
}

// fetch downloads (or reads) the release, verifies its checksum and unpacks
// it into Root/releases/<version>; it returns that directory.
func (o *Options) fetch(ctx context.Context, v, name string, say func(string, ...any)) (string, error) {
	var tgz, sums []byte
	var err error
	if o.Archive != "" {
		if tgz, err = os.ReadFile(o.Archive); err != nil {
			return "", err
		}
		if sums, err = os.ReadFile(filepath.Join(filepath.Dir(o.Archive), "checksums.txt")); err != nil {
			return "", fmt.Errorf("checksums.txt next to %s: %w", o.Archive, err)
		}
	} else {
		base := fmt.Sprintf("%s/download/v%s/", o.ReleaseURL, v)
		say("Downloading %s", base+name)
		if tgz, err = o.get(ctx, base+name); err != nil {
			return "", err
		}
		if sums, err = o.get(ctx, base+"checksums.txt"); err != nil {
			return "", err
		}
	}
	want := ""
	sc := bufio.NewScanner(strings.NewReader(string(sums)))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			want = f[0]
		}
	}
	got := sha256.Sum256(tgz)
	if want == "" {
		return "", fmt.Errorf("%s is not listed in checksums.txt", name)
	}
	if want != hex.EncodeToString(got[:]) {
		return "", fmt.Errorf("checksum mismatch for %s: expected %s, got %x (download corrupted or tampered with; nothing was changed)", name, want, got)
	}
	say("  SHA-256 verified: %x", got)
	dir := filepath.Join(o.Root, "releases", v)
	tmp := filepath.Join(o.Root, "releases", "."+v+".partial")
	os.RemoveAll(tmp)
	if err := unpack(tgz, tmp); err != nil {
		os.RemoveAll(tmp)
		return "", fmt.Errorf("unpacking %s: %w", name, err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "cms")); err != nil {
		os.RemoveAll(tmp)
		return "", fmt.Errorf("%s has no cms binary", name)
	}
	if cur, err := os.Readlink(filepath.Join(o.Root, "current")); err == nil && cur == filepath.Join("releases", v) {
		// Reinstalling the release in use: keep it, it is what runs.
		os.RemoveAll(tmp)
		return dir, nil
	}
	os.RemoveAll(dir)
	if err := os.Rename(tmp, dir); err != nil {
		return "", err
	}
	return dir, nil
}

func (o *Options) get(ctx context.Context, url string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := o.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<30))
}

// unpack extracts a release tarball into dir, dropping its top directory
// (cms_VERSION_linux_ARCH/); entries escaping dir are refused.
func unpack(tgz []byte, dir string) error {
	zr, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return err
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		rel := h.Name
		if i := strings.IndexByte(rel, '/'); i >= 0 {
			rel = rel[i+1:]
		} else {
			continue // the top directory itself
		}
		if rel == "" {
			continue
		}
		clean := filepath.Clean(rel)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe path %q", h.Name)
		}
		p := filepath.Join(dir, clean)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode&0o777))
			if err != nil {
				return err
			}
			_, err = io.Copy(f, io.LimitReader(tr, h.Size))
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("unexpected entry %q (type %c)", h.Name, h.Typeflag)
		}
	}
}

// switchTo points Root/current at rel (releases/<version>) atomically.
func switchTo(root, rel string) error {
	tmp := filepath.Join(root, ".current.new")
	os.Remove(tmp)
	if err := os.Symlink(rel, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(root, "current"))
}

// prune removes the oldest releases, keeping keep of them (current among).
func prune(root string, keep int, say func(string, ...any)) {
	cur, _ := os.Readlink(filepath.Join(root, "current"))
	ents, _ := os.ReadDir(filepath.Join(root, "releases"))
	type rel struct {
		name string
		at   time.Time
	}
	var rels []rel
	for _, e := range ents {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || filepath.Join("releases", e.Name()) == cur {
			continue
		}
		if info, err := e.Info(); err == nil {
			rels = append(rels, rel{e.Name(), info.ModTime()})
		}
	}
	sort.Slice(rels, func(i, j int) bool { return rels[i].at.After(rels[j].at) })
	for i, r := range rels {
		if i >= keep-1 {
			if os.RemoveAll(filepath.Join(root, "releases", r.name)) == nil {
				say("  removed the old release %s", r.name)
			}
		}
	}
}

func parseName(name string) (version, arch string, ok bool) {
	rest, found := strings.CutPrefix(name, "cms_")
	if !found {
		return "", "", false
	}
	rest, found = strings.CutSuffix(rest, ".tar.gz")
	if !found {
		return "", "", false
	}
	i := strings.LastIndex(rest, "_linux_")
	if i < 0 {
		return "", "", false
	}
	version, arch = rest[:i], rest[i+len("_linux_"):]
	_, ok = semver(version)
	return version, arch, ok && (arch == "amd64" || arch == "arm64")
}

// semver parses X.Y.Z (a -suffix, as in 1.2.0-rc.1, is allowed and
// ignored for ordering).
func semver(v string) ([3]int, bool) {
	var out [3]int
	core, _, _ := strings.Cut(v, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// newer reports whether a is a known version strictly newer than b.
func newer(a, b string) bool {
	x, ok1 := semver(a)
	y, ok2 := semver(b)
	if !ok1 || !ok2 {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}
