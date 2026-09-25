package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func readYAML(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", path))
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := yaml.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return v
}

// get walks a decoded YAML document: get(v, "jobs", "images", "steps").
func get(v any, keys ...string) any {
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

func strs(v any) []string {
	var out []string
	if l, ok := v.([]any); ok {
		for _, e := range l {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// TestReleaseConfiguration checks what a tag vX.Y.Z publishes: GoReleaser
// builds both binaries for linux/amd64 and linux/arm64 with SHA-256
// checksums and a changelog from git, and every file the tarball lists
// exists (an empty glob would silently ship an incomplete release). The
// release workflow checks the tag first and publishes the cms and worker
// images for both architectures, versioned like the release plus latest.
// scripts/check-release.sh verifies an actual snapshot in CI.
func TestReleaseConfiguration(t *testing.T) {
	root := filepath.Join("..", "..")
	gr := readYAML(t, ".goreleaser.yaml")
	builds, _ := gr["builds"].([]any)
	var ids []string
	for _, b := range builds {
		ids = append(ids, get(b, "binary").(string))
		if goos, arch := strs(get(b, "goos")), strs(get(b, "goarch")); !slices.Equal(goos, []string{"linux"}) || !slices.Equal(arch, []string{"amd64", "arm64"}) {
			t.Errorf("build %v: goos %v goarch %v", get(b, "id"), goos, arch)
		}
		if !slices.Contains(strs(get(b, "env")), "CGO_ENABLED=0") {
			t.Errorf("build %v is not static", get(b, "id"))
		}
		if l := strings.Join(strs(get(b, "ldflags")), " "); !strings.Contains(l, "internal/version.Version={{ .Version }}") {
			t.Errorf("build %v does not stamp the version: %s", get(b, "id"), l)
		}
	}
	if !slices.Equal(ids, []string{"cms", "cmsctl"}) {
		t.Errorf("binaries %v", ids)
	}
	if get(gr, "checksum", "algorithm") != "sha256" || get(gr, "checksum", "name_template") != "checksums.txt" {
		t.Errorf("checksum %v", gr["checksum"])
	}
	if get(gr, "changelog", "use") != "git" {
		t.Errorf("changelog %v", gr["changelog"])
	}
	archives, _ := gr["archives"].([]any)
	if len(archives) != 1 || get(archives[0], "name_template") != "cms_{{ .Version }}_{{ .Os }}_{{ .Arch }}" {
		t.Fatalf("archives %v", archives)
	}
	// The name scripts/install.sh and internal/upgrade download.
	for _, f := range []string{"scripts/install.sh", "internal/upgrade/upgrade.go"} {
		if b, _ := os.ReadFile(filepath.Join(root, f)); !strings.Contains(string(b), "cms_") || !strings.Contains(string(b), "checksums.txt") {
			t.Errorf("%s does not download cms_<version>_linux_<arch>.tar.gz with checksums.txt", f)
		}
	}
	var listed []string
	for _, f := range get(archives[0], "files").([]any) {
		glob, ok := f.(string)
		if !ok {
			glob = get(f, "src").(string)
		}
		listed = append(listed, glob)
		matches, _ := filepath.Glob(filepath.Join(root, strings.ReplaceAll(glob, "**/*", "*/*")))
		if len(matches) == 0 {
			t.Errorf("the release tarball lists %s, which matches nothing", glob)
		}
	}
	for _, want := range []string{"LICENSE", "config/cms.example.yaml", "internal/db/migrations/*.sql", "web/templates/**/*", "deploy/systemd/*", "scripts/verify-host.sh"} {
		if !slices.Contains(listed, want) {
			t.Errorf("the release tarball does not ship %s", want)
		}
	}

	wf := readYAML(t, ".github/workflows/release.yml")
	// yaml.v3 decodes the key "on" as a string (YAML 1.2).
	if tags := strs(get(wf, "on", "push", "tags")); !slices.Equal(tags, []string{"v[0-9]+.[0-9]+.[0-9]+*"}) {
		t.Errorf("release trigger %v", tags)
	}
	for _, job := range []string{"binaries", "images"} {
		if !slices.Contains(strs(get(wf, "jobs", job, "needs")), "check") && get(wf, "jobs", job, "needs") != "check" {
			t.Errorf("job %s does not wait for the checks", job)
		}
	}
	if m := strs(get(wf, "jobs", "images", "strategy", "matrix", "image")); !slices.Equal(m, []string{"cms", "worker"}) {
		t.Errorf("images %v", m)
	}
	var push, meta, goreleaser map[string]any
	for _, job := range []string{"binaries", "images"} {
		for _, s := range get(wf, "jobs", job, "steps").([]any) {
			uses, _ := get(s, "uses").(string)
			w, _ := get(s, "with").(map[string]any)
			switch {
			case strings.HasPrefix(uses, "docker/build-push-action"):
				push = w
			case strings.HasPrefix(uses, "docker/metadata-action"):
				meta = w
			case strings.HasPrefix(uses, "goreleaser/goreleaser-action"):
				goreleaser = w
			}
		}
	}
	if push["platforms"] != "linux/amd64,linux/arm64" || push["push"] != true || push["target"] != "${{ matrix.image }}" {
		t.Errorf("image build %v", push)
	}
	if !strings.Contains(meta["tags"].(string), "type=semver,pattern={{version}}") || meta["flavor"] != "latest=auto" {
		t.Errorf("image tags %v", meta)
	}
	if goreleaser == nil || !strings.HasPrefix(goreleaser["args"].(string), "release") {
		t.Errorf("goreleaser step %v", goreleaser)
	}
	df, _ := os.ReadFile(filepath.Join(root, "Dockerfile"))
	for _, stage := range []string{" AS cms\n", " AS worker\n", "FROM --platform=$BUILDPLATFORM"} {
		if !strings.Contains(string(df), stage) {
			t.Errorf("Dockerfile lacks %q", strings.TrimSpace(stage))
		}
	}

	if _, err := exec.LookPath("goreleaser"); err == nil {
		cmd := exec.Command("goreleaser", "check")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("goreleaser check: %v\n%s", err, out)
		}
	}
}

// TestProductionCompose checks deploy/docker/compose.yml: the published
// images, a worker with only the capabilities isolate needs (never
// privileged), every piece of state in a named volume, secrets only from
// .env, the migrations before any service, and setup.sh generating the
// first administrator's password instead of a default one.
func TestProductionCompose(t *testing.T) {
	c := readYAML(t, "deploy/docker/compose.yml")
	services := c["services"].(map[string]any)
	volumes := c["volumes"].(map[string]any)
	for name, s := range services {
		if get(s, "privileged") == true {
			t.Errorf("%s is privileged", name)
		}
		img, _ := get(s, "image").(string)
		if strings.HasPrefix(img, "${CMS_IMAGE") {
			want := "/cms:${CMS_VERSION:-latest}"
			if name == "worker" {
				want = "/worker:${CMS_VERSION:-latest}"
			}
			if !strings.HasPrefix(img, "${CMS_IMAGE:-ghcr.io/d4nd3r/contest-management-system}") || !strings.HasSuffix(img, want) {
				t.Errorf("%s image %s", name, img)
			}
			if name != "init" && get(s, "depends_on", "init", "condition") != "service_completed_successfully" {
				t.Errorf("%s may start before the migrations", name)
			}
		}
		for _, v := range strs(get(s, "volumes")) {
			src, _, _ := strings.Cut(v, ":")
			if strings.HasPrefix(src, ".") {
				if !strings.HasSuffix(v, ":ro") {
					t.Errorf("%s mounts %s writable", name, v)
				}
				continue
			}
			if _, ok := volumes[src]; !ok {
				t.Errorf("%s uses the undeclared volume %s", name, src)
			}
		}
		for k, v := range get(s, "environment").(map[string]any) {
			if s, _ := v.(string); strings.Contains(k, "PASSWORD") && !strings.Contains(s, "${") {
				t.Errorf("%s: %s is not taken from .env", name, k)
			}
		}
	}
	w := services["worker"]
	if caps := strs(get(w, "cap_add")); !slices.Equal(caps, []string{"SYS_ADMIN", "NET_ADMIN"}) {
		t.Errorf("worker capabilities %v", caps)
	}
	if get(w, "cgroup") != "private" || !slices.Equal(strs(get(w, "security_opt")), []string{"apparmor:unconfined"}) {
		t.Errorf("worker isolation %v %v", get(w, "cgroup"), get(w, "security_opt"))
	}
	persistent := map[string]string{"postgres": "pgdata:/var/lib/postgresql/data", "valkey": "valkeydata:/data", "admin-web": "backups:/var/lib/cms/backups", "ranking-web": "ranking:/var/lib/cms/ranking", "caddy": "caddy:/data"}
	for svc, v := range persistent {
		if !slices.Contains(strs(get(services[svc], "volumes")), v) {
			t.Errorf("%s does not keep %s", svc, v)
		}
	}
	if !slices.Contains(strs(get(services["contest-web"], "volumes")), "blobs:/var/lib/cms/blobs") {
		t.Error("the stored files are not in the blobs volume")
	}
	setup, _ := os.ReadFile(filepath.Join("..", "..", "deploy", "docker", "setup.sh"))
	if !strings.Contains(string(setup), "ctl bootstrap -generate-password") {
		t.Error("setup.sh does not generate the administrator's password")
	}
	cfg, _ := os.ReadFile(filepath.Join("..", "..", "deploy", "docker", "cms.yaml"))
	for _, secret := range []string{"password", "secret_key", "push_token"} {
		for _, line := range strings.Split(string(cfg), "\n") {
			if l := strings.TrimSpace(line); !strings.HasPrefix(l, "#") && strings.HasPrefix(l, secret+":") && !strings.Contains(l, `""`) {
				t.Errorf("deploy/docker/cms.yaml holds a secret: %s", l)
			}
		}
	}
}
