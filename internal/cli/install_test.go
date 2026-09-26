package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/config"
)

func runInstall(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("bash", append([]string{filepath.Join("..", "..", "scripts", "install.sh")}, args...)...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("install.sh %v: %v\n%s", args, err, out.String())
	}
	return out.String()
}

func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(p)
			rel, _ := filepath.Rel(dir, p)
			out[rel] = string(b)
		}
		return err
	})
	return out
}

// TestInstallScriptRender (SPEC_CLOSE A6) renders what scripts/install.sh
// writes for a 2-vCPU main server and checks it: a valid configuration
// with one judging core, matching secrets, CPU pinning for everything but
// the worker, the proxy, PostgreSQL and Valkey settings; a second run
// changes nothing (idempotent). nginx and worker-role variants too.
func TestInstallScriptRender(t *testing.T) {
	for _, script := range []string{"install.sh", "verify-host.sh", "install-isolate.sh", "test.sh", "infra.sh"} {
		if out, err := exec.Command("bash", "-n", filepath.Join("..", "..", "scripts", script)).CombinedOutput(); err != nil {
			t.Errorf("%s: %v\n%s", script, err, out)
		}
	}
	dir := t.TempDir()
	args := []string{"--render-only", dir, "--domain", "cms.example.org", "--email", "ops@example.org",
		"--admin-allow", "10.0.0.0/8", "--private-ip", "10.8.0.1", "--cpus", "2", "--ram-mb", "4096"}
	out := runInstall(t, args...)
	if !strings.Contains(out, "judging cores: [1]; web CPUs: 0") {
		t.Fatalf("output:\n%s", out)
	}
	first := readTree(t, dir)
	cfg, err := config.Load(filepath.Join(dir, "etc", "cms", "cms.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if k, err := hex.DecodeString(cfg.SecretKey); err != nil || len(k) != 32 {
		t.Errorf("secret key %q", cfg.SecretKey)
	}
	if len(cfg.Worker.Cores) != 1 || cfg.Worker.Cores[0] != 1 || !cfg.ContestWeb.CookieSecure || cfg.Blob.Backend != "local" ||
		cfg.RankingWeb.PublicURL != "https://ranking.cms.example.org" || cfg.AdminWeb.ContestURL != "https://cms.example.org" ||
		cfg.BlobServer.Listen != "10.8.0.1:8891" || len(cfg.BlobServer.Token) < 32 || cfg.Backup.ContestInterval.D().Minutes() != 15 {
		t.Errorf("config %+v", cfg)
	}
	secrets := first["etc/cms/secrets.env"]
	for _, want := range []string{"requirepass ", "appendonly yes", "maxmemory-policy noeviction", "bind 127.0.0.1 -::1 10.8.0.1"} {
		if !strings.Contains(first["etc/valkey/cms.conf"], want) {
			t.Errorf("valkey conf lacks %q", want)
		}
	}
	pass := strings.Fields(strings.SplitN(first["etc/valkey/cms.conf"], "requirepass ", 2)[1])[0]
	if !strings.Contains(cfg.Redis.URL, ":"+pass+"@") || !strings.Contains(secrets, "REDIS_PASSWORD="+pass) {
		t.Errorf("redis password differs between valkey (%s) and cms.yaml (%s)", pass, cfg.Redis.URL)
	}
	for _, want := range []string{"shared_buffers = 512MB", "effective_cache_size = 2048MB", "max_parallel_workers_per_gather = 0", "jit = off", "synchronous_commit = on"} {
		if !strings.Contains(first["etc/postgresql/cms.conf"], want) {
			t.Errorf("postgresql conf lacks %q", want)
		}
	}
	caddy := first["etc/caddy/Caddyfile"]
	for _, want := range []string{"email ops@example.org", "cms.example.org {", "ranking.cms.example.org {", "admin.cms.example.org {",
		"@outside not remote_ip 10.0.0.0/8", "reverse_proxy 127.0.0.1:8889"} {
		if !strings.Contains(caddy, want) {
			t.Errorf("Caddyfile lacks %q:\n%s", want, caddy)
		}
	}
	for _, s := range []string{"contest-web", "admin-web", "ranking-web", "dispatcher", "monitor", "worker", "printing", "blob-server"} {
		unit := first["etc/systemd/system/cms-"+s+".service"]
		if !strings.Contains(unit, "ExecStart=/usr/local/bin/cms "+s) || !strings.Contains(unit, "Restart=always") ||
			!strings.Contains(unit, "WantedBy=cms.target") {
			t.Errorf("unit cms-%s:\n%s", s, unit)
		}
		pin, pinned := first["etc/systemd/system/cms-"+s+".service.d/cpu.conf"]
		if s == "worker" {
			if pinned {
				t.Error("the worker must not be confined to the web CPU")
			}
		} else if !strings.Contains(pin, "CPUAffinity=0") {
			t.Errorf("cms-%s not pinned: %q", s, pin)
		}
	}
	for _, s := range []string{"postgresql@.service", "valkey-server.service", "caddy.service"} {
		if !strings.Contains(first["etc/systemd/system/"+s+".d/cms-cpu.conf"], "CPUAffinity=0") {
			t.Errorf("%s not pinned", s)
		}
	}
	for _, want := range []string{"would run: ufw allow 443/tcp", "would run: ufw allow to 10.8.0.1 port 6379", "systemctl enable cms-blob-server.service",
		"cmsctl bootstrap -admin-username admin"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan lacks %q", want)
		}
	}
	// Idempotent: a second run keeps every file (and every secret).
	out = runInstall(t, args...)
	if strings.Contains(out, "  wrote ") || !strings.Contains(out, "kept the existing /etc/cms/cms.yaml") {
		t.Errorf("second run rewrote files:\n%s", out)
	}
	second := readTree(t, dir)
	for f, b := range first {
		if second[f] != b {
			t.Errorf("%s changed on the second run", f)
		}
	}

	// nginx on a LAN: plain HTTP, server-sent events unbuffered.
	lan := t.TempDir()
	runInstall(t, "--render-only", lan, "--lan", "--web", "nginx", "--cpus", "8", "--ram-mb", "16384")
	ng := readTree(t, lan)
	if c := ng["etc/nginx/sites-available/cms"]; !strings.Contains(c, "proxy_buffering off") || !strings.Contains(c, "listen 8081;") {
		t.Errorf("nginx:\n%s", c)
	}
	lcfg, err := config.Load(filepath.Join(lan, "etc", "cms", "cms.yaml"))
	if err != nil || lcfg.ContestWeb.CookieSecure || len(lcfg.Worker.Cores) != 6 || lcfg.Worker.Cores[0] != 2 {
		t.Errorf("LAN config %+v %v", lcfg, err)
	}
	if !strings.Contains(ng["etc/systemd/system/cms-contest-web.service.d/cpu.conf"], "CPUAffinity=0 1") {
		t.Error("8 CPUs: the web should get CPUs 0 and 1")
	}

	// A worker on another machine.
	wdir := t.TempDir()
	out = runInstall(t, "--render-only", wdir, "--role", "worker", "--main", "10.8.0.1", "--redis-password", "rpass",
		"--blob-token", "0123456789abcdef0123", "--worker-name", "judge-2", "--cpus", "4")
	wcfg, err := config.Load(filepath.Join(wdir, "etc", "cms", "cms.yaml"))
	if err != nil || wcfg.Blob.Backend != "http" || wcfg.Blob.HTTP.URL != "http://10.8.0.1:8891" || wcfg.Redis.URL != "redis://:rpass@10.8.0.1:6379/0" ||
		wcfg.Worker.Name != "judge-2" || len(wcfg.Worker.Cores) != 3 {
		t.Errorf("worker config %+v %v", wcfg, err)
	}
	if strings.Contains(out, "enable cms-contest-web") || !strings.Contains(out, "enable cms-worker.service") {
		t.Errorf("worker plan:\n%s", out)
	}
	if _, ok := readTree(t, wdir)["etc/valkey/cms.conf"]; ok {
		t.Error("a worker must not configure Valkey")
	}
}

// fakeRelease builds a release tarball laid out as GoReleaser writes it
// (cms_VERSION_linux_ARCH/...) from this checkout, with a stand-in cms
// binary, and serves it the way GitHub does: /latest redirects to the tag,
// files are under /download/vVERSION/.
func fakeRelease(t *testing.T, version string, tamper bool) *httptest.Server {
	t.Helper()
	root := filepath.Join("..", "..")
	files := map[string]string{
		"cms":    "#!/bin/sh\necho \"cms " + version + " (test)\"\n",
		"cmsctl": "#!/bin/sh\nexit 0\n",
	}
	for _, glob := range []string{"deploy/systemd/*", "scripts/*.sh", "config/languages/*.yaml", "config/cms.example.yaml"} {
		ms, _ := filepath.Glob(filepath.Join(root, glob))
		for _, m := range ms {
			b, _ := os.ReadFile(m)
			rel, _ := filepath.Rel(root, m)
			files[filepath.ToSlash(rel)] = string(b)
		}
	}
	tarballs := map[string][]byte{}
	var sums strings.Builder
	for _, arch := range []string{"amd64", "arm64"} {
		name := fmt.Sprintf("cms_%s_linux_%s.tar.gz", version, arch)
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(zw)
		top := strings.TrimSuffix(name, ".tar.gz")
		for p, content := range files {
			tw.WriteHeader(&tar.Header{Name: top + "/" + p, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg})
			tw.Write([]byte(content))
		}
		tw.Close()
		zw.Close()
		tarballs[name] = buf.Bytes()
		sum := sha256.Sum256(buf.Bytes())
		if tamper {
			sum[0] ^= 1
		}
		fmt.Fprintf(&sums, "%x  %s\n", sum, name)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/tag/v"+version, http.StatusFound)
	})
	mux.HandleFunc("GET /tag/{tag}", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "release page") })
	mux.HandleFunc("GET /download/{tag}/{file}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("tag") != "v"+version {
			http.NotFound(w, r)
			return
		}
		if f := r.PathValue("file"); f == "checksums.txt" {
			fmt.Fprint(w, sums.String())
		} else if b, ok := tarballs[f]; ok {
			w.Write(b)
		} else {
			http.NotFound(w, r)
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// installEnv pretends the machine is a supported one (the checks read
// these instead of the real system).
func installEnv(t *testing.T, osRelease, arch, container, cgroupFS string) []string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "os-release")
	os.WriteFile(f, []byte(osRelease), 0o644)
	return append(os.Environ(), "CMS_INSTALL_OS_RELEASE="+f, "CMS_INSTALL_ARCH="+arch,
		"CMS_INSTALL_CONTAINER="+container, "CMS_INSTALL_CGROUP_FS="+cgroupFS)
}

func runInstallEnv(env []string, stdin string, args ...string) (string, error) {
	var cmd *exec.Cmd
	if stdin != "" { // curl ... | bash -s -- args
		cmd = exec.Command("bash", append([]string{"-s", "--"}, args...)...)
		cmd.Stdin = strings.NewReader(stdin)
	} else {
		cmd = exec.Command("bash", append([]string{filepath.Join("..", "..", "scripts", "install.sh")}, args...)...)
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestInstallFromRelease: the one-line installer resolves the latest
// release, downloads the tarball for this architecture, verifies its
// SHA-256 checksum and plans the installation (--dry-run changes nothing);
// a tampered download, an unsupported machine and the uninstaller.
func TestInstallFromRelease(t *testing.T) {
	ubuntu := "ID=ubuntu\nVERSION_ID=\"24.04\"\nPRETTY_NAME=\"Ubuntu 24.04 LTS\"\n"
	good := installEnv(t, ubuntu, "aarch64", "none", "cgroup2fs")
	ts := fakeRelease(t, "9.9.9", false)

	// Piped, as with curl ... | sudo bash: the script is not a file.
	script, _ := os.ReadFile(filepath.Join("..", "..", "scripts", "install.sh"))
	out, err := runInstallEnv(good, string(script), "--dry-run", "--release-url", ts.URL, "--domain", "cms.example.org")
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	for _, want := range []string{"system: Ubuntu 24.04 LTS", "architecture: arm64", "control groups: v2", "==> release 9.9.9",
		"downloading " + ts.URL + "/download/v9.9.9/cms_9.9.9_linux_arm64.tar.gz", "SHA-256 verified",
		"would run: apt-get install", "would install /opt/cms/releases/9.9.9", "would write /etc/cms/cms.yaml",
		"would write /etc/caddy/Caddyfile", "cmsctl bootstrap -admin-username admin -generate-password",
		"would run: /usr/local/sbin/cms-verify-host", "Dry run: nothing was changed"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan lacks %q", want)
		}
	}
	if strings.Contains(out, "BLOCKER") || t.Failed() {
		t.Fatalf("plan:\n%s", out)
	}
	// Ubuntu 22.04's archive has no caddy, no valkey and no Java 21: Caddy's
	// own repository, Redis and Java 17 instead.
	aptCache := filepath.Join(t.TempDir(), "apt-cache")
	os.WriteFile(aptCache, []byte("#!/bin/sh\ncase \"$2\" in caddy|valkey-server|openjdk-21-jdk-headless) exit 100 ;; esac\nexit 0\n"), 0o755)
	jammy := append(installEnv(t, "ID=ubuntu\nVERSION_ID=\"22.04\"\nPRETTY_NAME=\"Ubuntu 22.04 LTS\"\n", "x86_64", "none", "cgroup2fs"),
		"CMS_INSTALL_APT_CACHE="+aptCache)
	out, err = runInstallEnv(jammy, "", "--dry-run", "--release-url", ts.URL, "--domain", "cms.example.org")
	if err != nil || !strings.Contains(out, "adding Caddy's repository") || !strings.Contains(out, "caddy-stable.list") ||
		!regexp.MustCompile(`apt-get install .*openjdk-17-jdk-headless .*redis-server caddy `).MatchString(out) {
		t.Fatalf("Ubuntu 22.04 packages: %v\n%s", err, out)
	}
	// Where the archive has them, no extra repository.
	os.WriteFile(aptCache, []byte("#!/bin/sh\nexit 0\n"), 0o755)
	out, _ = runInstallEnv(jammy, "", "--dry-run", "--release-url", ts.URL, "--domain", "cms.example.org")
	if strings.Contains(out, "Caddy's repository") || !regexp.MustCompile(`apt-get install .*openjdk-21-jdk-headless .*valkey-server caddy `).MatchString(out) {
		t.Fatalf("archive packages:\n%s", out)
	}

	// A given version, from the checkout this time.
	if out, err := runInstallEnv(good, "", "--dry-run", "--release-url", ts.URL, "--version", "v9.9.9"); err != nil || !strings.Contains(out, "==> release 9.9.9") {
		t.Fatalf("--version: %v\n%s", err, out)
	}
	if out, err := runInstallEnv(good, "", "--dry-run", "--release-url", ts.URL, "--version", "1.0.0"); err == nil || !strings.Contains(out, "cannot download") {
		t.Fatalf("missing version: %v\n%s", err, out)
	}

	// A tampered download stops everything before any change.
	bad := fakeRelease(t, "9.9.9", true)
	out, err = runInstallEnv(good, "", "--dry-run", "--release-url", bad.URL)
	if err == nil || !strings.Contains(out, "checksum mismatch for cms_9.9.9_linux_arm64.tar.gz") || strings.Contains(out, "==> packages") {
		t.Fatalf("tampered release: %v\n%s", err, out)
	}

	// Machines that cannot judge: each reason is reported.
	unsupported := installEnv(t, "ID=centos\nVERSION_ID=\"9\"\nPRETTY_NAME=\"CentOS Stream 9\"\n", "i686", "lxc", "tmpfs")
	out, err = runInstallEnv(unsupported, "", "--dry-run", "--release-url", ts.URL)
	for _, want := range []string{"BLOCKER: unsupported system CentOS Stream 9", "BLOCKER: unsupported architecture i686",
		"BLOCKER: this is a container (lxc)", "BLOCKER: control groups v2 are not active", "4 problem(s) above would stop"} {
		if !strings.Contains(out, want) {
			t.Errorf("unsupported machine: no %q", want)
		}
	}
	if t.Failed() {
		t.Fatalf("%v\n%s", err, out)
	}

	// Installed already: the installer reconfigures that release, and
	// leaves version changes to cmsctl upgrade.
	root := t.TempDir()
	rel := filepath.Join(root, "releases", "1.0.0")
	os.MkdirAll(filepath.Join(rel, "deploy"), 0o755)
	os.WriteFile(filepath.Join(rel, "cms"), []byte("#!/bin/sh\necho 'cms 1.0.0 (x)'\n"), 0o755)
	exec.Command("cp", "-r", filepath.Join("..", "..", "deploy", "systemd"), filepath.Join(rel, "deploy")).Run()
	os.Symlink("releases/1.0.0", filepath.Join(root, "current"))
	installed := append(good, "CMS_INSTALL_ROOT="+root)
	if out, err := runInstallEnv(installed, "", "--dry-run", "--release-url", ts.URL); err != nil || !strings.Contains(out, "release 1.0.0 (installed") ||
		strings.Contains(out, "downloading") {
		t.Fatalf("reconfigure: %v\n%s", err, out)
	}
	if out, err := runInstallEnv(installed, "", "--dry-run", "--release-url", ts.URL, "--version", "9.9.9"); err == nil || !strings.Contains(out, "sudo cmsctl upgrade -version 9.9.9") {
		t.Fatalf("version change: %v\n%s", err, out)
	}

	// Uninstall: the plan keeps the data unless --purge.
	out, err = runInstallEnv(good, "", "--uninstall", "--dry-run")
	if err != nil || !strings.Contains(out, "would run: systemctl disable --now cms.target") || !strings.Contains(out, "rm -rf /usr/local/share/doc/cms /opt/cms") ||
		strings.Contains(out, "rm -rf /etc/cms /var/lib/cms") || !strings.Contains(out, "its data stays") {
		t.Fatalf("uninstall: %v\n%s", err, out)
	}
	out, err = runInstallEnv(good, "", "--uninstall", "--purge", "--dry-run")
	if err != nil || !strings.Contains(out, "would run: rm -rf /etc/cms /var/lib/cms /var/cache/cms") {
		t.Fatalf("purge: %v\n%s", err, out)
	}
}

// TestInstallPostgresCluster: the installer uses the main cluster of the
// newest installed PostgreSQL, on its own port (after a distribution upgrade
// the old version's cluster keeps 5432 and the new one gets 5433), and
// creates one, in C.UTF-8, when there is none.
func TestInstallPostgresCluster(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "scripts", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(script)
	funcs := filepath.Join(t.TempDir(), "functions.sh")
	os.WriteFile(funcs, []byte(src[:strings.LastIndex(src, "main \"$@\"")]), 0o644) // everything but the call to main
	run := func(clusters string, installed []string, mode string) (string, string, error) {
		dir := t.TempDir()
		lib, bin := filepath.Join(dir, "lib"), filepath.Join(dir, "bin")
		for _, v := range installed {
			os.MkdirAll(filepath.Join(lib, v, "bin"), 0o755)
			os.WriteFile(filepath.Join(lib, v, "bin", "postgres"), []byte("#!/bin/sh\n"), 0o755)
		}
		os.MkdirAll(bin, 0o755)
		os.WriteFile(filepath.Join(dir, "clusters"), []byte(clusters), 0o644)
		os.WriteFile(filepath.Join(bin, "pg_lsclusters"), []byte("#!/bin/sh\ncat "+dir+"/clusters\n"), 0o755)
		os.WriteFile(filepath.Join(bin, "pg_createcluster"), []byte("#!/bin/sh\necho \"$*\" > "+dir+"/created\n"+
			"echo \"$3 $4 5432 down postgres /var/lib/postgresql/$3/$4 log\" >> "+dir+"/clusters\n"), 0o755)
		cmd := exec.Command("bash", "-c", `source "$1"; pg_cluster $2; echo "cluster=$PGVER port=$PGPORT"`, "_", funcs, mode)
		cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "CMS_INSTALL_PG_LIB="+lib)
		out, err := cmd.CombinedOutput()
		created, _ := os.ReadFile(filepath.Join(dir, "created"))
		return string(out), string(created), err
	}
	upgraded := "16 main 5432 down,binaries_missing postgres /var/lib/postgresql/16/main log\n18 main 5433 down postgres /var/lib/postgresql/18/main log\n"
	for _, c := range []struct {
		name, clusters string
		installed      []string
		mode, want     string
	}{
		{"fresh", "18 main 5432 online postgres /var/lib/postgresql/18/main log\n", []string{"18"}, "create", "cluster=18 port=5432"},
		{"upgraded, old server removed", upgraded, []string{"18"}, "create", "cluster=18 port=5433"},
		{"upgraded, both installed", upgraded, []string{"16", "18"}, "create", "cluster=18 port=5433"},
		{"other clusters only", "18 reports 5432 online postgres /x log\n", []string{"18"}, "", "cluster= port=5432"},
	} {
		if out, _, err := run(c.clusters, c.installed, c.mode); err != nil || !strings.Contains(out, c.want) {
			t.Errorf("%s: %v\n%s", c.name, err, out)
		}
	}
	out, created, err := run("", []string{"16", "18"}, "create")
	if err != nil || !strings.Contains(out, "creating 18/main") || !strings.Contains(out, "cluster=18 port=5432") || strings.TrimSpace(created) != "--locale C.UTF-8 18 main" {
		t.Errorf("no cluster: %v created %q\n%s", err, created, out)
	}
	if out, _, err := run("", nil, "create"); err == nil || !strings.Contains(out, "PostgreSQL is not installed") {
		t.Errorf("nothing installed: %v\n%s", err, out)
	}
}

// TestInstallValkeyPort: Valkey keeps 6379 when it is free or already
// Valkey's, and moves to the next free port when another program has it (a
// Redis left by another installation, another application's store), leaving
// that program alone; an existing cms.yaml follows the ports.
func TestInstallValkeyPort(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "scripts", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(script)
	funcs := filepath.Join(t.TempDir(), "functions.sh")
	os.WriteFile(funcs, []byte(src[:strings.LastIndex(src, "main \"$@\"")]), 0o644)
	run := func(holders map[int]string, args, call string) (string, error) {
		dir := t.TempDir()
		bin := filepath.Join(dir, "bin")
		os.MkdirAll(filepath.Join(dir, "ports"), 0o755)
		os.MkdirAll(bin, 0o755)
		for port, name := range holders {
			os.WriteFile(filepath.Join(dir, "ports", fmt.Sprint(port)), []byte(name), 0o644)
		}
		// ss -ltnpH "sport = :PORT" prints the listener of PORT, if any.
		os.WriteFile(filepath.Join(bin, "ss"), []byte("#!/bin/sh\nport=${2##*:}\nf="+dir+"/ports/$port\n"+
			"[ -f \"$f\" ] && echo \"LISTEN 0 511 127.0.0.1:$port 0.0.0.0:* users:((\\\"$(cat \"$f\")\\\",pid=7,fd=8))\"\nexit 0\n"), 0o755)
		os.WriteFile(filepath.Join(bin, "valkey-server"), []byte("#!/bin/sh\n"), 0o755)
		// --dry-run: the functions under test do not need root, the argument check would.
		cmd := exec.Command("bash", "-c", `source "$1"; shift; parse_args --dry-run "$@" >/dev/null; `+call, "_", funcs)
		cmd.Args = append(cmd.Args, strings.Fields(args)...)
		cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	port := `kv_port; echo "port=$REDIS_PORT"`
	for _, c := range []struct {
		name    string
		holders map[int]string
		args    string
		want    string
	}{
		{"free", nil, "--lan", "port=6379"},
		{"already Valkey's", map[int]string{6379: "valkey-server"}, "--lan", "port=6379"},
		{"another Redis", map[int]string{6379: "redis-server"}, "--lan", "port 6379 is used by redis-server: CMS's valkey-server listens on 6380"},
		{"several taken", map[int]string{6379: "redis-server", 6380: "docker-proxy"}, "--lan", "port=6381"},
		{"moved before", map[int]string{6379: "redis-server", 6380: "valkey-server"}, "--lan", "port=6380"},
		{"given", map[int]string{6379: "redis-server"}, "--lan --redis-port 7000", "port=7000"},
	} {
		if out, err := run(c.holders, c.args, port); err != nil || !strings.Contains(out, c.want) {
			t.Errorf("%s: %v\n%s", c.name, err, out)
		}
	}
	if out, err := run(nil, "--lan --redis-port six", port); err == nil || !strings.Contains(out, "--redis-port must be a port number") {
		t.Errorf("bad --redis-port: %v\n%s", err, out)
	}

	// A cms.yaml written for 5432 and 6379 follows the ports found now.
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "etc", "cms"), 0o755)
	yaml := "database:\n  url: postgres://cms:pw@127.0.0.1:5432/cms?sslmode=disable\nredis:\n  url: redis://:pw@127.0.0.1:6379/0\n"
	os.WriteFile(filepath.Join(root, "etc", "cms", "cms.yaml"), []byte(yaml), 0o640)
	sync := `P=` + root + ` PGPORT=5433 REDIS_PORT=6380; sync_ports; sync_ports`
	out, err := run(nil, "--lan", sync)
	got, _ := os.ReadFile(filepath.Join(root, "etc", "cms", "cms.yaml"))
	if err != nil || strings.Count(out, "updated the PostgreSQL (5433) and Valkey (6380) ports") != 1 ||
		!strings.Contains(string(got), "@127.0.0.1:5433/cms") || !strings.Contains(string(got), "@127.0.0.1:6380/0") {
		t.Errorf("sync_ports: %v\n%s\n%s", err, out, got)
	}
}

// fakeSS makes an ss that reports the given listeners ("80/tcp" ->
// program, "" for a kernel socket): with "sport = :PORT" only that TCP
// port, otherwise every listener of the protocol asked for (-ltnpH or
// -lunpH). wg, when given, is what `wg show all listen-port` prints.
func fakeSS(t *testing.T, listeners map[string]string, wg string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(filepath.Join(dir, "ports"), 0o755)
	os.MkdirAll(bin, 0o755)
	for port, name := range listeners {
		p, proto, _ := strings.Cut(port, "/")
		os.WriteFile(filepath.Join(dir, "ports", proto+"-"+p), []byte(name), 0o644)
	}
	os.WriteFile(filepath.Join(bin, "ss"), []byte(`#!/bin/sh
D="`+dir+`/ports"
line() { n=$(cat "$D/$2-$1"); u=""; [ -n "$n" ] && u="users:((\"$n\",pid=7,fd=8))"; echo "LISTEN 0 511 0.0.0.0:$1 0.0.0.0:* $u"; }
case "$1" in -lunpH) proto=udp ;; *) proto=tcp ;; esac
case "$2" in
  *:*) p=${2##*:}; [ -f "$D/$proto-$p" ] && line "$p" $proto ;;
  *) for f in "$D"/$proto-*; do [ -f "$f" ] && line "${f##*-}" $proto; done ;;
esac
exit 0
`), 0o755)
	if wg != "" {
		os.WriteFile(filepath.Join(bin, "wg"), []byte("#!/bin/sh\nprintf '"+wg+"\\n'\n"), 0o755)
	}
	return bin
}

// TestInstallWebPorts: on a machine that already serves something (another
// web server, a container), the web ports are checked before anything
// changes, --http-ports moves the LAN sites, and the firewall is left to the
// administrator instead of cutting the other services off; SSH stays open
// on whatever port it listens.
func TestInstallWebPorts(t *testing.T) {
	// The chosen ports reach Caddy, nginx and the firewall.
	for _, web := range []string{"caddy", "nginx"} {
		dir := t.TempDir()
		runInstall(t, "--render-only", dir, "--lan", "--web", web, "--http-ports", "8000,8001,8002", "--cpus", "2", "--ram-mb", "4096")
		files := readTree(t, dir)
		conf := files["etc/caddy/Caddyfile"]
		if web == "nginx" {
			conf = files["etc/nginx/sites-available/cms"]
		}
		for _, p := range []string{"8000", "8001", "8002"} {
			if !strings.Contains(conf, p) {
				t.Errorf("%s configuration lacks port %s:\n%s", web, p, conf)
			}
		}
		if strings.Contains(conf, ":80 ") || strings.Contains(conf, "listen 80;") {
			t.Errorf("%s still on port 80:\n%s", web, conf)
		}
	}
	for _, bad := range [][]string{{"--lan", "--http-ports", "8000,8000,8001"}, {"--lan", "--http-ports", "web"}, {"--lan", "--http-ports", "0,8001,8002"}, {"--lan", "--http-ports", "8000,8888,8002"}, {"--domain", "x.org", "--http-ports", "8000,8001,8002"}} {
		cmd := exec.Command("bash", append([]string{filepath.Join("..", "..", "scripts", "install.sh"), "--dry-run"}, bad...)...)
		if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "--http-ports") {
			t.Errorf("%v accepted: %s", bad, out)
		}
	}

	ubuntu := "ID=ubuntu\nVERSION_ID=\"24.04\"\nPRETTY_NAME=\"Ubuntu 24.04 LTS\"\n"
	ts := fakeRelease(t, "9.9.9", false)
	withSS := func(listeners map[string]string, wg string) []string {
		env := installEnv(t, ubuntu, "x86_64", "none", "cgroup2fs")
		return append(env, "PATH="+fakeSS(t, listeners, wg)+":"+os.Getenv("PATH"))
	}
	// Nextcloud-style containers on 80 and 8080: reported before any change,
	// with the way out.
	nextcloud := map[string]string{"80/tcp": "docker-proxy", "8080/tcp": "docker-proxy", "443/tcp": "docker-proxy", "2222/tcp": "sshd", "68/udp": "systemd-network"}
	out, err := runInstallEnv(withSS(nextcloud, ""), "", "--dry-run", "--release-url", ts.URL, "--lan")
	if err != nil || !strings.Contains(out, "BLOCKER: port 80 is used by docker-proxy") || !strings.Contains(out, "BLOCKER: port 8080 is used by docker-proxy") ||
		!strings.Contains(out, "--http-ports 8000,8001,8002") {
		t.Fatalf("taken web ports: %v\n%s", err, out)
	}
	if out, err := runInstallEnv(withSS(nextcloud, ""), "", "--dry-run", "--release-url", ts.URL, "--domain", "cms.example.org"); err != nil ||
		!strings.Contains(out, "BLOCKER: port 80 is used by docker-proxy: HTTPS for a domain needs ports 80 and 443") {
		t.Fatalf("taken HTTPS ports: %v\n%s", err, out)
	}
	// Free ports: no blocker; containers do not keep the firewall off, and
	// SSH on 2222 is allowed before it is enabled.
	out, err = runInstallEnv(withSS(nextcloud, ""), "", "--dry-run", "--release-url", ts.URL, "--lan", "--http-ports", "8000,8001,8002")
	if err != nil || strings.Contains(out, "BLOCKER") || !strings.Contains(out, "would run: ufw allow 2222/tcp") ||
		!strings.Contains(out, "would run: ufw allow 8000/tcp") || !strings.Contains(out, "would run: ufw --force enable") ||
		!strings.Contains(out, "contest:  http://<this machine>:8000/") {
		t.Fatalf("free ports: %v\n%s", err, out)
	}
	// Another service on the machine, TCP or UDP, or a kernel one: the
	// firewall is left off, with what CMS needs.
	for _, other := range []struct{ port, name, want string }{
		{"3000/tcp", "node", "node:3000/tcp"},
		{"41641/udp", "tailscaled", "tailscaled:41641/udp"},
		{"2049/tcp", "", "2049/tcp"},
		{"8765/tcp", "systemd", "systemd:8765/tcp"},
	} {
		busy := map[string]string{"80/tcp": "docker-proxy", "22/tcp": "sshd", other.port: other.name}
		out, err = runInstallEnv(withSS(busy, ""), "", "--dry-run", "--release-url", ts.URL, "--lan", "--http-ports", "8000,8001,8002")
		if err != nil || !strings.Contains(out, "left off: other programs listen on this machine ("+other.want+")") ||
			!strings.Contains(out, "CMS needs TCP 22 8000 8001 8002 open") || strings.Contains(out, "ufw --force enable") {
			t.Fatalf("other service %s: %v\n%s", other.want, err, out)
		}
	}
	// A WireGuard tunnel (a kernel UDP socket) and SSH through systemd's
	// socket are kept open instead.
	tunnel := map[string]string{"51820/udp": "", "22/tcp": "systemd"}
	out, err = runInstallEnv(withSS(tunnel, "wg0\t51820"), "", "--dry-run", "--release-url", ts.URL, "--lan", "--http-ports", "8000,8001,8002")
	if err != nil || strings.Contains(out, "left off") || !strings.Contains(out, "would run: ufw allow 51820/udp") ||
		!strings.Contains(out, "would run: ufw --force enable") {
		t.Fatalf("WireGuard: %v\n%s", err, out)
	}
}

// TestInstallCPULayout: on a machine with hyperthreading, judging takes one
// CPU per physical core and leaves the siblings idle; the web keeps whole
// cores; --judge-all-threads judges on the siblings too; an existing
// cms.yaml still on the old layout (every CPU after the web ones) follows,
// and cores chosen by hand are kept.
func TestInstallCPULayout(t *testing.T) {
	sysfs := func(n int, list func(i int) string) string {
		dir := t.TempDir()
		for i := 0; i < n; i++ {
			d := filepath.Join(dir, fmt.Sprintf("cpu%d", i), "topology")
			os.MkdirAll(d, 0o755)
			os.WriteFile(filepath.Join(d, "thread_siblings_list"), []byte(list(i)+"\n"), 0o644)
		}
		return dir
	}
	intel8 := sysfs(8, func(i int) string { return fmt.Sprintf("%d,%d", i%4, i%4+4) }) // 4 cores × 2
	amd16 := sysfs(16, func(i int) string { return fmt.Sprintf("%d-%d", i/2*2, i/2*2+1) })
	plain8 := sysfs(8, func(i int) string { return fmt.Sprint(i) }) // a VM: no siblings
	for _, c := range []struct {
		name, sys, args, want, affinity string
	}{
		{"intel SMT", intel8, "--lan", "judging cores: [1, 2, 3]; web CPUs: 0 4; idle hyperthreads: 5 6 7", "CPUAffinity=0 4"},
		{"all threads", intel8, "--lan --judge-all-threads", "judging cores: [1, 2, 3, 5, 6, 7]; web CPUs: 0 4", "CPUAffinity=0 4"},
		{"amd SMT", amd16, "--lan", "judging cores: [4, 6, 8, 10, 12, 14]; web CPUs: 0 1 2 3; idle hyperthreads: 5 7 9 11 13 15", "CPUAffinity=0 1 2 3"},
		{"no SMT", plain8, "--lan", "judging cores: [2, 3, 4, 5, 6, 7]; web CPUs: 0 1", "CPUAffinity=0 1"},
		{"worker", intel8, "--role worker --main 10.0.0.1 --redis-password p --blob-token b", "judging cores: [1, 2, 3]; idle hyperthreads: 5 6 7", ""},
	} {
		dir := t.TempDir()
		cmd := exec.Command("bash", append([]string{filepath.Join("..", "..", "scripts", "install.sh"), "--render-only", dir, "--ram-mb", "8192"}, strings.Fields(c.args)...)...)
		cmd.Env = append(os.Environ(), "CMS_INSTALL_SYSFS="+c.sys)
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), c.want) {
			t.Errorf("%s: %v\n%s", c.name, err, out)
			continue
		}
		files := readTree(t, dir)
		cores := c.want[strings.Index(c.want, "[") : strings.Index(c.want, "]")+1]
		if !strings.Contains(files["etc/cms/cms.yaml"], "cores: "+cores) {
			t.Errorf("%s: cms.yaml lacks cores %s:\n%s", c.name, cores, files["etc/cms/cms.yaml"])
		}
		if c.affinity != "" && !strings.Contains(files["etc/systemd/system/cms-contest-web.service.d/cpu.conf"], c.affinity) {
			t.Errorf("%s: web pinning: %q", c.name, files["etc/systemd/system/cms-contest-web.service.d/cpu.conf"])
		}
	}

	// An existing cms.yaml: the old default moves, a hand-picked list stays.
	script, _ := os.ReadFile(filepath.Join("..", "..", "scripts", "install.sh"))
	src := string(script)
	funcs := filepath.Join(t.TempDir(), "functions.sh")
	os.WriteFile(funcs, []byte(src[:strings.LastIndex(src, "main \"$@\"")]), 0o644)
	sync := func(cores string) (string, string) {
		root := t.TempDir()
		os.MkdirAll(filepath.Join(root, "etc", "cms"), 0o755)
		f := filepath.Join(root, "etc", "cms", "cms.yaml")
		os.WriteFile(f, []byte("worker:\n  cores: ["+cores+"]\n"), 0o640)
		cmd := exec.Command("bash", "-c", `source "$1"; parse_args --dry-run --lan >/dev/null; DRY=0 P=`+root+` NCPU=8; cpu_layout; sync_cores; sync_cores`, "_", funcs)
		cmd.Env = append(os.Environ(), "CMS_INSTALL_SYSFS="+intel8)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("sync_cores: %v\n%s", err, out)
		}
		got, _ := os.ReadFile(f)
		return string(out), string(got)
	}
	if out, got := sync("2, 3, 4, 5, 6, 7"); !strings.Contains(got, "cores: [1, 2, 3]") || strings.Count(out, "-> [1, 2, 3]") != 1 {
		t.Errorf("old default not moved:\n%s\n%s", out, got)
	}
	if out, got := sync("3, 4"); !strings.Contains(got, "cores: [3, 4]") || !strings.Contains(out, "the recommended layout for this machine is [1, 2, 3]") {
		t.Errorf("hand-picked cores:\n%s\n%s", out, got)
	}
}
