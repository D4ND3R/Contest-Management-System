package cli

import (
	"bytes"
	"encoding/hex"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
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
