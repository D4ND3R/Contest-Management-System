package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var ctx = context.Background()

// tarball builds a release tarball whose top directory is dropped when
// unpacked, as GoReleaser writes them.
func tarball(t *testing.T, top string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, content := range files {
		tw.WriteHeader(&tar.Header{Name: top + "/" + name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg})
		tw.Write([]byte(content))
	}
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

// server publishes releases the way GitHub does.
func server(t *testing.T, latest string, tgz map[string][]byte, tamper bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /latest", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/tag/v"+latest, http.StatusFound) })
	mux.HandleFunc("GET /tag/{tag}", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("GET /download/{tag}/{file}", func(w http.ResponseWriter, r *http.Request) {
		f := r.PathValue("file")
		if f == "checksums.txt" {
			for name, b := range tgz {
				sum := sha256.Sum256(b)
				if tamper {
					sum[0] ^= 1
				}
				fmt.Fprintf(w, "%x  %s\n", sum, name)
			}
			return
		}
		if b, ok := tgz[f]; ok && strings.Contains(f, strings.TrimPrefix(r.PathValue("tag"), "v")) {
			w.Write(b)
			return
		}
		http.NotFound(w, r)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// install lays out an installation of version 1.0.0 under a new root.
func install(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "releases", "1.0.0"), 0o755)
	os.WriteFile(filepath.Join(root, "releases", "1.0.0", "cms"), []byte("old"), 0o755)
	if err := os.Symlink("releases/1.0.0", filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	return root
}

// recorder fakes the steps and records their order.
type recorder struct {
	calls                       []string
	running                     []string
	failMigrate, failHealthOnce bool
	restored                    string
}

func (r *recorder) steps() Steps {
	return Steps{
		Running: func(context.Context) ([]string, error) { return r.running, nil },
		Backup: func(context.Context) (string, error) {
			r.calls = append(r.calls, "backup")
			return "/backups/b1.tar.zst", nil
		},
		Restore: func(_ context.Context, b string) error {
			r.calls = append(r.calls, "restore")
			r.restored = b
			return nil
		},
		Stop:  func(context.Context) error { r.calls = append(r.calls, "stop"); return nil },
		Start: func(context.Context) error { r.calls = append(r.calls, "start"); return nil },
		Migrate: func(_ context.Context, bin string) error {
			b, _ := os.ReadFile(bin)
			r.calls = append(r.calls, "migrate:"+string(b))
			if r.failMigrate {
				return errors.New("migration 0017 failed")
			}
			return nil
		},
		Health: func(context.Context) error {
			r.calls = append(r.calls, "health")
			if r.failHealthOnce {
				r.failHealthOnce = false
				return errors.New("contest-web: connection refused")
			}
			return nil
		},
	}
}

func current(t *testing.T, root string) string {
	t.Helper()
	l, err := os.Readlink(filepath.Join(root, "current"))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestUpgrade(t *testing.T) {
	rel := map[string][]byte{
		"cms_2.0.0_linux_amd64.tar.gz": tarball(t, "cms_2.0.0_linux_amd64", map[string]string{"cms": "new", "cmsctl": "new", "deploy/systemd/cms.target": "[Unit]"}),
		"cms_2.0.0_linux_arm64.tar.gz": tarball(t, "cms_2.0.0_linux_arm64", map[string]string{"cms": "new-arm"}),
	}
	ts := server(t, "2.0.0", rel, false)
	opts := func(root string) Options {
		return Options{Root: root, ReleaseURL: ts.URL, Version: "latest", Arch: "amd64", Current: "1.0.0"}
	}

	t.Run("success", func(t *testing.T) {
		root := install(t)
		var out bytes.Buffer
		o := opts(root)
		o.Out = &out
		r := &recorder{}
		if err := Run(ctx, o, r.steps()); err != nil {
			t.Fatalf("%v\n%s", err, out.String())
		}
		if got := strings.Join(r.calls, ","); got != "backup,stop,migrate:new,start,health" {
			t.Fatalf("steps %s", got)
		}
		if current(t, root) != "releases/2.0.0" {
			t.Fatalf("current -> %s", current(t, root))
		}
		if b, _ := os.ReadFile(filepath.Join(root, "current", "deploy", "systemd", "cms.target")); string(b) != "[Unit]" {
			t.Fatal("release not unpacked")
		}
		if _, err := os.Stat(filepath.Join(root, "releases", "1.0.0", "cms")); err != nil {
			t.Fatal("the previous release was removed")
		}
		if !strings.Contains(out.String(), "SHA-256 verified") || !strings.Contains(out.String(), "CMS 2.0.0 is running") {
			t.Fatalf("output:\n%s", out.String())
		}
		// Again: nothing to do.
		o.Current = "2.0.0"
		r = &recorder{}
		if err := Run(ctx, o, r.steps()); err != nil || len(r.calls) != 0 {
			t.Fatalf("second run: %v %v", err, r.calls)
		}
	})

	t.Run("contest in progress", func(t *testing.T) {
		root := install(t)
		r := &recorder{running: []string{"omi2030 (until 2030-01-01 15:00 UTC)"}}
		err := Run(ctx, opts(root), r.steps())
		if err == nil || !strings.Contains(err.Error(), "omi2030") || !strings.Contains(err.Error(), "-force") || len(r.calls) != 0 {
			t.Fatalf("%v %v", err, r.calls)
		}
		o := opts(root)
		o.Force = true
		if err := Run(ctx, o, r.steps()); err != nil || current(t, root) != "releases/2.0.0" {
			t.Fatalf("forced: %v", err)
		}
	})

	t.Run("tampered download", func(t *testing.T) {
		root := install(t)
		o := opts(root)
		o.ReleaseURL = server(t, "2.0.0", rel, true).URL
		r := &recorder{}
		err := Run(ctx, o, r.steps())
		if err == nil || !strings.Contains(err.Error(), "checksum mismatch") || len(r.calls) != 0 || current(t, root) != "releases/1.0.0" {
			t.Fatalf("%v %v", err, r.calls)
		}
		if _, err := os.Stat(filepath.Join(root, "releases", "2.0.0")); err == nil {
			t.Fatal("a tampered release was unpacked")
		}
	})

	t.Run("failed migration rolls back", func(t *testing.T) {
		root := install(t)
		r := &recorder{failMigrate: true}
		err := Run(ctx, opts(root), r.steps())
		if err == nil || !strings.Contains(err.Error(), "rolled back to releases/1.0.0") {
			t.Fatalf("%v", err)
		}
		if got := strings.Join(r.calls, ","); got != "backup,stop,migrate:new,stop,restore,start,health" {
			t.Fatalf("steps %s", got)
		}
		if current(t, root) != "releases/1.0.0" || r.restored != "/backups/b1.tar.zst" {
			t.Fatalf("current %s restored %q", current(t, root), r.restored)
		}
	})

	t.Run("unhealthy services roll back", func(t *testing.T) {
		root := install(t)
		r := &recorder{failHealthOnce: true}
		err := Run(ctx, opts(root), r.steps())
		if err == nil || !strings.Contains(err.Error(), "waiting for the services") || current(t, root) != "releases/1.0.0" {
			t.Fatalf("%v, current %s", err, current(t, root))
		}
		if got := strings.Join(r.calls, ","); got != "backup,stop,migrate:new,start,health,stop,restore,start,health" {
			t.Fatalf("steps %s", got)
		}
	})

	t.Run("worker without database", func(t *testing.T) {
		root := install(t)
		var out bytes.Buffer
		o := opts(root)
		o.Out = &out
		r := &recorder{running: []string{"never asked"}}
		s := r.steps()
		s.Running, s.Backup, s.Restore, s.Migrate = nil, nil, nil, nil
		if err := Run(ctx, o, s); err != nil {
			t.Fatalf("%v\n%s", err, out.String())
		}
		if got := strings.Join(r.calls, ","); got != "stop,start,health" || current(t, root) != "releases/2.0.0" {
			t.Fatalf("steps %s, current %s", got, current(t, root))
		}
		if !strings.Contains(out.String(), "no contest check, backup or migrations") {
			t.Fatalf("output:\n%s", out.String())
		}
		// Unhealthy after the switch: back to the previous release, nothing to restore.
		root = install(t)
		o = opts(root)
		r = &recorder{failHealthOnce: true}
		s = r.steps()
		s.Running, s.Backup, s.Restore, s.Migrate = nil, nil, nil, nil
		err := Run(ctx, o, s)
		if err == nil || !strings.Contains(err.Error(), "rolled back to releases/1.0.0") || strings.Contains(err.Error(), "restored") {
			t.Fatalf("%v", err)
		}
		if got := strings.Join(r.calls, ","); got != "stop,start,health,stop,start,health" || current(t, root) != "releases/1.0.0" {
			t.Fatalf("steps %s, current %s", got, current(t, root))
		}
	})

	t.Run("refused", func(t *testing.T) {
		root := install(t)
		for _, c := range []struct {
			o    func(*Options)
			want string
		}{
			{func(o *Options) { o.Current = "3.0.0" }, "older than the installed 3.0.0"},
			{func(o *Options) { o.Version = "9.9.9" }, "404"},
			{func(o *Options) { o.Version = "nonsense" }, "not a version"},
			{func(o *Options) { o.Root = t.TempDir() }, "predates releases"},
		} {
			o := opts(root)
			c.o(&o)
			r := &recorder{}
			if err := Run(ctx, o, r.steps()); err == nil || !strings.Contains(err.Error(), c.want) || len(r.calls) != 0 {
				t.Errorf("want %q: %v %v", c.want, err, r.calls)
			}
		}
	})

	t.Run("local archive for arm64", func(t *testing.T) {
		root := install(t)
		dir := t.TempDir()
		name := "cms_2.0.0_linux_arm64.tar.gz"
		os.WriteFile(filepath.Join(dir, name), rel[name], 0o644)
		sum := sha256.Sum256(rel[name])
		os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(fmt.Sprintf("%x  %s\n", sum, name)), 0o644)
		o := Options{Root: root, Archive: filepath.Join(dir, name), Arch: "arm64", Current: "1.0.0"}
		r := &recorder{}
		if err := Run(ctx, o, r.steps()); err != nil || current(t, root) != "releases/2.0.0" || !strings.Contains(strings.Join(r.calls, ","), "migrate:new-arm") {
			t.Fatalf("%v %v", err, r.calls)
		}
		o.Arch = "amd64"
		if err := Run(ctx, o, r.steps()); err == nil || !strings.Contains(err.Error(), "is for arm64") {
			t.Fatalf("wrong architecture: %v", err)
		}
	})
}

func TestUnpackRefusesEscapes(t *testing.T) {
	for _, name := range []string{"top/../../etc/passwd", "top/../x"} {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(zw)
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
		tw.Write([]byte("x"))
		tw.Close()
		zw.Close()
		if err := unpack(buf.Bytes(), t.TempDir()); err == nil || !strings.Contains(err.Error(), "unsafe") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestPruneKeepsRecentReleases(t *testing.T) {
	root := install(t)
	for _, v := range []string{"0.7.0", "0.8.0", "0.9.0"} {
		os.MkdirAll(filepath.Join(root, "releases", v), 0o755)
	}
	prune(root, 3, func(string, ...any) {})
	ents, _ := os.ReadDir(filepath.Join(root, "releases"))
	if len(ents) != 3 {
		t.Fatalf("%d releases kept", len(ents))
	}
	if _, err := os.Stat(filepath.Join(root, "releases", "1.0.0")); err != nil {
		t.Fatal("the current release was removed")
	}
}
