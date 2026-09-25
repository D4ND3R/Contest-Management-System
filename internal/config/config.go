// Package config loads the CMS configuration from a YAML file with
// environment-variable overrides. Every service reads the same file; each
// one only looks at the sections it needs.
package config

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration that unmarshals from strings like "1.5s".
type Duration time.Duration

// D returns the value as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(strings.TrimSpace(n.Value))
	if err != nil {
		return fmt.Errorf("line %d: invalid duration %q: %w", n.Line, n.Value, err)
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

// ByteSize is a size in bytes that unmarshals from strings like "512MiB".
type ByteSize int64

func (b *ByteSize) UnmarshalYAML(n *yaml.Node) error {
	v, err := ParseByteSize(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: %w", n.Line, err)
	}
	*b = ByteSize(v)
	return nil
}

// ParseByteSize parses "123", "10KiB", "64MB", "4GiB" (binary and decimal units).
func ParseByteSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	units := []struct {
		suffix string
		mult   int64
	}{
		{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30}, {"TiB", 1 << 40},
		{"KB", 1000}, {"MB", 1000 * 1000}, {"GB", 1000 * 1000 * 1000},
		{"K", 1 << 10}, {"M", 1 << 20}, {"G", 1 << 30}, {"B", 1},
	}
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			num := strings.TrimSpace(strings.TrimSuffix(s, u.suffix))
			f, err := strconv.ParseFloat(num, 64)
			if err != nil || f < 0 {
				return 0, fmt.Errorf("invalid byte size %q", s)
			}
			return int64(f * float64(u.mult)), nil
		}
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("invalid byte size %q", s)
	}
	return v, nil
}

type Log struct {
	Level  string `yaml:"level"`  // debug, info, warn, error
	Format string `yaml:"format"` // json, text
}

type Database struct {
	URL      string `yaml:"url"`
	MaxConns int32  `yaml:"max_conns"`
}

type Redis struct {
	URL      string `yaml:"url"`
	PoolSize int    `yaml:"pool_size"`
	// Namespace prefixes every key (several installations can share a server).
	Namespace string `yaml:"namespace"`
}

type S3 struct {
	Endpoint  string `yaml:"endpoint"`
	Bucket    string `yaml:"bucket"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	Region    string `yaml:"region"`
	UseSSL    bool   `yaml:"use_ssl"`
}

type Blob struct {
	Backend       string   `yaml:"backend"` // local | s3
	LocalDir      string   `yaml:"local_dir"`
	S3            S3       `yaml:"s3"`
	CacheDir      string   `yaml:"cache_dir"`       // local LRU cache (workers, s3 backend)
	CacheMaxBytes ByteSize `yaml:"cache_max_bytes"` // 0 disables the cache
}

type ContestWeb struct {
	Listen string `yaml:"listen"`
	// ContestID pins the server to one contest; 0 lets the user pick.
	ContestID      int64    `yaml:"contest_id"`
	TrustedProxies []string `yaml:"trusted_proxies"`
	CookieSecure   bool     `yaml:"cookie_secure"`
	// MaxSubmissionBytes bounds the whole multipart request.
	MaxSubmissionBytes ByteSize `yaml:"max_submission_bytes"`
	MaxUserTestBytes   ByteSize `yaml:"max_user_test_bytes"`
	MaxPrintBytes      ByteSize `yaml:"max_print_bytes"`
	// Rate limiting (per session or IP) for mutating requests.
	RateLimitPerMinute int  `yaml:"rate_limit_per_minute"`
	LoginRateLimit     int  `yaml:"login_rate_limit_per_minute"`
	Pprof              bool `yaml:"pprof"`
}

type AdminWeb struct {
	Listen         string   `yaml:"listen"`
	CookieSecure   bool     `yaml:"cookie_secure"`
	TrustedProxies []string `yaml:"trusted_proxies"`
	// MaxUploadBytes bounds uploads (testcase archives, statements).
	MaxUploadBytes ByteSize `yaml:"max_upload_bytes"`
	LoginRateLimit int      `yaml:"login_rate_limit_per_minute"`
	Pprof          bool     `yaml:"pprof"`
}

type RankingWeb struct {
	Listen    string `yaml:"listen"`
	DataDir   string `yaml:"data_dir"`
	PushToken string `yaml:"push_token"` // shared secret for the dispatcher pushes
	// Title shown on the scoreboard.
	Title string `yaml:"title"`
	// MaxClients bounds the number of concurrent SSE spectators.
	MaxClients int  `yaml:"max_clients"`
	Pprof      bool `yaml:"pprof"`
}

type Dispatcher struct {
	MetricsListen string `yaml:"metrics_listen"`
	// RankingURLs are the RWS instances that receive score pushes,
	// e.g. http://rws.local:8890. The push token is taken from ranking_web.push_token.
	RankingURLs []string `yaml:"ranking_urls"`
	// SweepInterval controls how often the DB is scanned for submissions
	// whose jobs were lost (belt and braces over the durable queues).
	SweepInterval Duration `yaml:"sweep_interval"`
	MaxAttempts   int      `yaml:"max_attempts"`
	// TestcasesPerJob groups this many testcases per evaluation job (1 = max parallelism).
	TestcasesPerJob int `yaml:"testcases_per_job"`
}

type Worker struct {
	Name           string   `yaml:"name"`
	MetricsListen  string   `yaml:"metrics_listen"`
	IsolatePath    string   `yaml:"isolate_path"`
	IsolateCG      bool     `yaml:"isolate_cg"`       // use --cg (control groups)
	IsolateBoxRoot string   `yaml:"isolate_box_root"` // must match box_root in the isolate config
	Cores          []int    `yaml:"cores"`            // empty = one box per physical core
	BoxIDOffset    int      `yaml:"box_id_offset"`
	WorkDir        string   `yaml:"work_dir"`
	CacheDir       string   `yaml:"cache_dir"`
	CacheMaxBytes  ByteSize `yaml:"cache_max_bytes"`
	// Queues this worker consumes, in priority order. Empty = all.
	Queues []string `yaml:"queues"`
	// Extra read-only directories bound inside every sandbox (e.g. /usr/lib/jvm).
	SandboxDirs       []string `yaml:"sandbox_dirs"`
	HeartbeatInterval Duration `yaml:"heartbeat_interval"`
}

type Monitor struct {
	MetricsListen    string   `yaml:"metrics_listen"`
	HeartbeatTimeout Duration `yaml:"heartbeat_timeout"`
	// JobTimeout: jobs pending longer than this are reclaimed even if the
	// worker still heartbeats (it is probably stuck).
	JobTimeout    Duration `yaml:"job_timeout"`
	CheckInterval Duration `yaml:"check_interval"`
}

type Printing struct {
	MetricsListen string `yaml:"metrics_listen"`
	// Printer is the CUPS destination; empty disables printing (jobs are
	// marked done after the PDF is rendered, useful for testing).
	Printer  string `yaml:"printer"`
	LPPath   string `yaml:"lp_path"`
	MaxPages int    `yaml:"max_pages"`
	// PaperSize for text rendering (A4 or Letter).
	PaperSize string `yaml:"paper_size"`
}

// Config is the root configuration.
type Config struct {
	Log          Log        `yaml:"log"`
	Database     Database   `yaml:"database"`
	Redis        Redis      `yaml:"redis"`
	Blob         Blob       `yaml:"blob"`
	SecretKey    string     `yaml:"secret_key"` // hex, >= 32 bytes; signs cookies and CSRF tokens
	LanguagesDir string     `yaml:"languages_dir"`
	ContestWeb   ContestWeb `yaml:"contest_web"`
	AdminWeb     AdminWeb   `yaml:"admin_web"`
	RankingWeb   RankingWeb `yaml:"ranking_web"`
	Dispatcher   Dispatcher `yaml:"dispatcher"`
	Worker       Worker     `yaml:"worker"`
	Monitor      Monitor    `yaml:"monitor"`
	Printing     Printing   `yaml:"printing"`

	// Path the config was loaded from ("" when defaults only).
	Path string `yaml:"-"`
}

// Default returns a configuration suitable for local development.
func Default() *Config {
	return &Config{
		Log:          Log{Level: "info", Format: "json"},
		Database:     Database{URL: "postgres://cms:cms@localhost:5432/cms?sslmode=disable", MaxConns: 32},
		Redis:        Redis{URL: "redis://localhost:6379/0", PoolSize: 64, Namespace: "cms:"},
		Blob:         Blob{Backend: "local", LocalDir: "./data/blobs", CacheDir: "", CacheMaxBytes: 0},
		LanguagesDir: "./config/languages",
		ContestWeb: ContestWeb{
			Listen: ":8888", CookieSecure: false,
			MaxSubmissionBytes: 1 << 20, MaxUserTestBytes: 8 << 20, MaxPrintBytes: 2 << 20,
			RateLimitPerMinute: 120, LoginRateLimit: 20,
		},
		AdminWeb:   AdminWeb{Listen: ":8889", MaxUploadBytes: 1 << 30, LoginRateLimit: 20},
		RankingWeb: RankingWeb{Listen: ":8890", DataDir: "./data/ranking", Title: "Ranking", MaxClients: 20000},
		Dispatcher: Dispatcher{
			MetricsListen: ":9101", SweepInterval: Duration(30 * time.Second),
			MaxAttempts: 3, TestcasesPerJob: 1,
		},
		Worker: Worker{
			MetricsListen: ":9102", IsolatePath: "isolate", IsolateCG: true,
			IsolateBoxRoot: "/var/local/lib/isolate", WorkDir: "./data/worker",
			CacheDir: "./data/worker-cache", CacheMaxBytes: 2 << 30,
			HeartbeatInterval: Duration(2 * time.Second),
		},
		Monitor: Monitor{
			MetricsListen: ":9103", HeartbeatTimeout: Duration(10 * time.Second),
			JobTimeout: Duration(10 * time.Minute), CheckInterval: Duration(2 * time.Second),
		},
		Printing: Printing{MetricsListen: ":9104", LPPath: "lp", MaxPages: 10, PaperSize: "A4"},
	}
}

// Load reads the configuration file at path (if non-empty or if CMS_CONFIG
// is set) on top of the defaults, then applies environment overrides.
func Load(path string) (*Config, error) {
	cfg := Default()
	if path == "" {
		path = os.Getenv("CMS_CONFIG")
	}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read config: %w", err)
		}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("parse config %s: %w", path, err)
		}
		cfg.Path = path
	}
	if err := cfg.applyEnv(os.LookupEnv); err != nil {
		return nil, err
	}
	return cfg, cfg.Validate()
}

// applyEnv overrides selected fields from CMS_* environment variables. Only
// the settings that commonly differ between deployments are exposed.
func (c *Config) applyEnv(lookup func(string) (string, bool)) error {
	str := map[string]*string{
		"CMS_LOG_LEVEL":          &c.Log.Level,
		"CMS_LOG_FORMAT":         &c.Log.Format,
		"CMS_DATABASE_URL":       &c.Database.URL,
		"CMS_REDIS_URL":          &c.Redis.URL,
		"CMS_BLOB_BACKEND":       &c.Blob.Backend,
		"CMS_BLOB_DIR":           &c.Blob.LocalDir,
		"CMS_BLOB_CACHE_DIR":     &c.Blob.CacheDir,
		"CMS_S3_ENDPOINT":        &c.Blob.S3.Endpoint,
		"CMS_S3_BUCKET":          &c.Blob.S3.Bucket,
		"CMS_S3_ACCESS_KEY":      &c.Blob.S3.AccessKey,
		"CMS_S3_SECRET_KEY":      &c.Blob.S3.SecretKey,
		"CMS_SECRET_KEY":         &c.SecretKey,
		"CMS_LANGUAGES_DIR":      &c.LanguagesDir,
		"CMS_CONTEST_WEB_LISTEN": &c.ContestWeb.Listen,
		"CMS_ADMIN_WEB_LISTEN":   &c.AdminWeb.Listen,
		"CMS_RANKING_WEB_LISTEN": &c.RankingWeb.Listen,
		"CMS_RANKING_DATA_DIR":   &c.RankingWeb.DataDir,
		"CMS_RANKING_PUSH_TOKEN": &c.RankingWeb.PushToken,
		"CMS_WORKER_NAME":        &c.Worker.Name,
		"CMS_WORKER_WORK_DIR":    &c.Worker.WorkDir,
		"CMS_WORKER_CACHE_DIR":   &c.Worker.CacheDir,
		"CMS_ISOLATE_PATH":       &c.Worker.IsolatePath,
		"CMS_ISOLATE_BOX_ROOT":   &c.Worker.IsolateBoxRoot,
		"CMS_DISPATCHER_METRICS": &c.Dispatcher.MetricsListen,
		"CMS_WORKER_METRICS":     &c.Worker.MetricsListen,
		"CMS_MONITOR_METRICS":    &c.Monitor.MetricsListen,
		"CMS_PRINTING_METRICS":   &c.Printing.MetricsListen,
		"CMS_PRINTER":            &c.Printing.Printer,
	}
	for k, p := range str {
		if v, ok := lookup(k); ok {
			*p = v
		}
	}
	if v, ok := lookup("CMS_S3_USE_SSL"); ok {
		c.Blob.S3.UseSSL = v == "1" || strings.EqualFold(v, "true")
	}
	if v, ok := lookup("CMS_ISOLATE_CG"); ok {
		c.Worker.IsolateCG = v == "1" || strings.EqualFold(v, "true")
	}
	if v, ok := lookup("CMS_COOKIE_SECURE"); ok {
		b := v == "1" || strings.EqualFold(v, "true")
		c.ContestWeb.CookieSecure, c.AdminWeb.CookieSecure = b, b
	}
	if v, ok := lookup("CMS_CONTEST_ID"); ok {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("CMS_CONTEST_ID: %w", err)
		}
		c.ContestWeb.ContestID = id
	}
	if v, ok := lookup("CMS_RANKING_URLS"); ok {
		c.Dispatcher.RankingURLs = splitList(v)
	}
	if v, ok := lookup("CMS_WORKER_CORES"); ok {
		cores, err := ParseIntList(v)
		if err != nil {
			return fmt.Errorf("CMS_WORKER_CORES: %w", err)
		}
		c.Worker.Cores = cores
	}
	if v, ok := lookup("CMS_WORKER_BOX_OFFSET"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("CMS_WORKER_BOX_OFFSET: %w", err)
		}
		c.Worker.BoxIDOffset = n
	}
	if v, ok := lookup("CMS_WORKER_QUEUES"); ok {
		c.Worker.Queues = splitList(v)
	}
	return nil
}

func splitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ParseIntList parses "0,1,2" or ranges like "0-3,6".
func ParseIntList(v string) ([]int, error) {
	var out []int
	for _, part := range splitList(v) {
		if lo, hi, ok := strings.Cut(part, "-"); ok {
			a, err1 := strconv.Atoi(lo)
			b, err2 := strconv.Atoi(hi)
			if err1 != nil || err2 != nil || a > b || a < 0 {
				return nil, fmt.Errorf("invalid range %q", part)
			}
			for i := a; i <= b; i++ {
				out = append(out, i)
			}
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("invalid integer %q", part)
		}
		out = append(out, n)
	}
	return out, nil
}

// Validate checks invariants that would otherwise surface as confusing
// runtime errors.
func (c *Config) Validate() error {
	var errs []error
	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("log.level: unknown level %q", c.Log.Level))
	}
	switch c.Log.Format {
	case "json", "text":
	default:
		errs = append(errs, fmt.Errorf("log.format: unknown format %q", c.Log.Format))
	}
	switch c.Blob.Backend {
	case "local":
		if c.Blob.LocalDir == "" {
			errs = append(errs, errors.New("blob.local_dir is required for the local backend"))
		}
	case "s3":
		if c.Blob.S3.Endpoint == "" || c.Blob.S3.Bucket == "" {
			errs = append(errs, errors.New("blob.s3.endpoint and blob.s3.bucket are required for the s3 backend"))
		}
	default:
		errs = append(errs, fmt.Errorf("blob.backend: unknown backend %q", c.Blob.Backend))
	}
	if c.SecretKey != "" {
		k, err := hex.DecodeString(c.SecretKey)
		if err != nil || len(k) < 32 {
			errs = append(errs, errors.New("secret_key must be at least 32 bytes, hex encoded"))
		}
	}
	if c.Dispatcher.MaxAttempts < 1 {
		errs = append(errs, errors.New("dispatcher.max_attempts must be >= 1"))
	}
	if c.Dispatcher.TestcasesPerJob < 1 {
		errs = append(errs, errors.New("dispatcher.testcases_per_job must be >= 1"))
	}
	return errors.Join(errs...)
}

// Secret returns the decoded secret key. When none is configured a random
// key is generated, which is fine for a single dev process but invalidates
// sessions on restart; production configs must set secret_key.
func (c *Config) Secret() []byte {
	if c.SecretKey != "" {
		k, _ := hex.DecodeString(c.SecretKey)
		return k
	}
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	c.SecretKey = hex.EncodeToString(k)
	return k
}
