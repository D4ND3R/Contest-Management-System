package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultsValidate(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}
}

func TestLoadFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cms.yaml")
	body := `
log: {level: debug, format: text}
database: {url: "postgres://x/y", max_conns: 7}
blob: {backend: local, local_dir: /tmp/blobs, cache_max_bytes: 512MiB}
dispatcher: {sweep_interval: 1500ms, max_attempts: 5, testcases_per_job: 2}
worker: {cores: [0, 2]}
`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CMS_REDIS_URL", "redis://example:1/2")
	t.Setenv("CMS_WORKER_CORES", "4-6,9")
	t.Setenv("CMS_CONTEST_ID", "12")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Log.Level != "debug" || cfg.Database.MaxConns != 7 || cfg.Database.URL != "postgres://x/y" {
		t.Errorf("file values not applied: %+v", cfg)
	}
	if cfg.Blob.CacheMaxBytes != 512<<20 {
		t.Errorf("cache_max_bytes = %d", cfg.Blob.CacheMaxBytes)
	}
	if cfg.Dispatcher.SweepInterval.D() != 1500*time.Millisecond || cfg.Dispatcher.MaxAttempts != 5 {
		t.Errorf("dispatcher = %+v", cfg.Dispatcher)
	}
	if cfg.Redis.URL != "redis://example:1/2" {
		t.Errorf("env override not applied: %q", cfg.Redis.URL)
	}
	if got := cfg.Worker.Cores; len(got) != 4 || got[0] != 4 || got[3] != 9 {
		t.Errorf("cores = %v", got)
	}
	if cfg.ContestWeb.ContestID != 12 {
		t.Errorf("contest id = %d", cfg.ContestWeb.ContestID)
	}
	// Untouched sections keep their defaults.
	if cfg.AdminWeb.Listen != ":8889" {
		t.Errorf("admin listen = %q", cfg.AdminWeb.Listen)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cms.yaml")
	os.WriteFile(p, []byte("databse: {url: x}\n"), 0o644)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for misspelled section")
	}
}

func TestLoadEmptyFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cms.yaml")
	os.WriteFile(p, nil, 0o644)
	if _, err := Load(p); err != nil {
		t.Fatalf("empty config must load: %v", err)
	}
}

func TestValidate(t *testing.T) {
	c := Default()
	c.Log.Level = "loud"
	c.Blob.Backend = "tape"
	c.SecretKey = "abcd"
	c.Dispatcher.MaxAttempts = 0
	err := c.Validate()
	if err == nil {
		t.Fatal("expected validation errors")
	}
	for _, want := range []string{"log.level", "blob.backend", "secret_key", "max_attempts"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

func TestParseByteSize(t *testing.T) {
	cases := map[string]int64{"0": 0, "123": 123, "1KiB": 1024, "1.5MiB": 1572864, "2GiB": 2 << 30, "10MB": 10_000_000, "64M": 64 << 20}
	for in, want := range cases {
		got, err := ParseByteSize(in)
		if err != nil || got != want {
			t.Errorf("ParseByteSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "x", "-1", "1XB", "-2MiB"} {
		if _, err := ParseByteSize(bad); err == nil {
			t.Errorf("ParseByteSize(%q) should fail", bad)
		}
	}
}

func TestParseIntList(t *testing.T) {
	got, err := ParseIntList("0-2, 5")
	if err != nil || len(got) != 4 || got[3] != 5 {
		t.Fatalf("got %v %v", got, err)
	}
	if _, err := ParseIntList("3-1"); err == nil {
		t.Error("descending range must fail")
	}
}

func TestSecretGeneratedWhenMissing(t *testing.T) {
	c := Default()
	k := c.Secret()
	if len(k) != 32 || c.SecretKey == "" {
		t.Fatalf("secret = %x", k)
	}
	if string(c.Secret()) != string(k) {
		t.Fatal("secret must be stable once generated")
	}
}
