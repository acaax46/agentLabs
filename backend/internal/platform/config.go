package platform

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Config holds every tunable. Nothing is read from the environment after startup, and no value
// here is a secret. Scaling is configuration: more API replicas, more worker processes, a higher
// WorkerConcurrency per node, a networked blob store, more sandbox nodes.
type Config struct {
	// Tenant is the tenant of internally created work in single-tenant deployments.
	Tenant string

	WorkerID          string
	WorkerConcurrency int
	PollInterval      time.Duration
	LeaseDuration     time.Duration
	DrainTimeout      time.Duration
	RecoverInterval   time.Duration
	MaxRunAttempts    int

	DefaultCurrency          string
	DefaultMaxConcurrentRuns int
	DefaultRetentionDays     int
	DefaultRatePerMinute     int
	MaxUploadBytes           int64
	MaxRunInputBytes         int64
	ModelRequestTimeout      time.Duration
	ModelSendRetries         int
	DownloadURLTTL           time.Duration

	// WorkspaceName is how this deployment's single workspace is shown to people.
	WorkspaceName string
	// StorageSampleInterval is how often a node looks for projects whose storage has not been
	// sampled today. Each project is recorded at most once per UTC day however often nodes look.
	StorageSampleInterval time.Duration

	// heartbeatEvery overrides the lease/3 heartbeat, and janitorEvery the one-minute cleanup cycle; tests shorten them.
	heartbeatEvery time.Duration
	janitorEvery   time.Duration
	storageEvery   time.Duration
}

// DefaultConfig is what a developer machine runs with.
func DefaultConfig(tenant string) Config {
	host, _ := os.Hostname()
	if host == "" {
		host = "worker"
	}
	return Config{
		Tenant:                   tenant,
		WorkerID:                 fmt.Sprintf("%s-%d", host, os.Getpid()),
		WorkerConcurrency:        4,
		PollInterval:             time.Second,
		LeaseDuration:            60 * time.Second,
		DrainTimeout:             90 * time.Second,
		RecoverInterval:          15 * time.Second,
		MaxRunAttempts:           3,
		DefaultCurrency:          "CNY",
		DefaultMaxConcurrentRuns: 10,
		DefaultRetentionDays:     30,
		DefaultRatePerMinute:     600,
		MaxUploadBytes:           25 << 20,
		MaxRunInputBytes:         100 << 20,
		ModelRequestTimeout:      120 * time.Second,
		ModelSendRetries:         2,
		DownloadURLTTL:           5 * time.Minute,
		WorkspaceName:            "AgentLab 工作区",
		StorageSampleInterval:    time.Hour,
	}
}

func envString(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	return value, nil
}

// ConfigFromEnv applies AGENTLAB_* overrides to the defaults and validates the result.
func ConfigFromEnv(tenant string) (Config, error) {
	cfg := DefaultConfig(tenant)
	cfg.WorkerID = envString("AGENTLAB_WORKER_ID", cfg.WorkerID)
	cfg.DefaultCurrency = strings.ToUpper(envString("AGENTLAB_DEFAULT_CURRENCY", cfg.DefaultCurrency))
	cfg.WorkspaceName = envString("AGENTLAB_WORKSPACE_NAME", cfg.WorkspaceName)
	type integer struct {
		name   string
		target *int
	}
	for _, field := range []integer{
		{"AGENTLAB_WORKER_CONCURRENCY", &cfg.WorkerConcurrency},
		{"AGENTLAB_RUN_MAX_ATTEMPTS", &cfg.MaxRunAttempts},
		{"AGENTLAB_PROJECT_MAX_CONCURRENT_RUNS", &cfg.DefaultMaxConcurrentRuns},
		{"AGENTLAB_ARTIFACT_RETENTION_DAYS", &cfg.DefaultRetentionDays},
		{"AGENTLAB_DEFAULT_RATE_PER_MINUTE", &cfg.DefaultRatePerMinute},
		{"AGENTLAB_MODEL_SEND_RETRIES", &cfg.ModelSendRetries},
	} {
		value, err := envInt(field.name, *field.target)
		if err != nil {
			return Config{}, err
		}
		*field.target = value
	}
	type duration struct {
		name   string
		target *time.Duration
		unit   time.Duration
	}
	for _, field := range []duration{
		{"AGENTLAB_WORKER_POLL_MS", &cfg.PollInterval, time.Millisecond},
		{"AGENTLAB_RUN_LEASE_SECONDS", &cfg.LeaseDuration, time.Second},
		{"AGENTLAB_DRAIN_SECONDS", &cfg.DrainTimeout, time.Second},
		{"AGENTLAB_RECOVER_SECONDS", &cfg.RecoverInterval, time.Second},
		{"AGENTLAB_MODEL_TIMEOUT_SECONDS", &cfg.ModelRequestTimeout, time.Second},
		{"AGENTLAB_STORAGE_SAMPLE_SECONDS", &cfg.StorageSampleInterval, time.Second},
	} {
		value, err := envInt(field.name, int(*field.target/field.unit))
		if err != nil {
			return Config{}, err
		}
		// Reject before multiplying: a huge number would wrap around and be judged as something else.
		if value < 0 || int64(value) > math.MaxInt64/int64(field.unit) {
			return Config{}, fmt.Errorf("%s is out of range", field.name)
		}
		*field.target = time.Duration(value) * field.unit
	}
	uploadMB, err := envInt("AGENTLAB_MAX_UPLOAD_MB", int(cfg.MaxUploadBytes>>20))
	if err != nil {
		return Config{}, err
	}
	inputMB, err := envInt("AGENTLAB_MAX_RUN_INPUT_MB", int(cfg.MaxRunInputBytes>>20))
	if err != nil {
		return Config{}, err
	}
	if uploadMB < 0 || uploadMB > 1<<20 || inputMB < 0 || inputMB > 1<<20 {
		return Config{}, fmt.Errorf("AGENTLAB_MAX_UPLOAD_MB and AGENTLAB_MAX_RUN_INPUT_MB are out of range")
	}
	cfg.MaxUploadBytes, cfg.MaxRunInputBytes = int64(uploadMB)<<20, int64(inputMB)<<20
	return cfg, cfg.Validate()
}

// Validate rejects settings that would break invariants the rest of the code relies on.
func (c Config) Validate() error {
	switch {
	case c.Tenant == "":
		return fmt.Errorf("platform: tenant is required")
	case c.WorkerID == "" || len(c.WorkerID) > 100:
		return fmt.Errorf("platform: worker id must be 1-100 characters")
	case c.WorkerConcurrency < 1 || c.WorkerConcurrency > 256:
		return fmt.Errorf("platform: worker concurrency must be 1-256")
	case c.PollInterval < 50*time.Millisecond || c.PollInterval > time.Minute:
		return fmt.Errorf("platform: poll interval must be 50ms-1m")
	case c.LeaseDuration < 10*time.Second || c.LeaseDuration > 30*time.Minute:
		return fmt.Errorf("platform: run lease must be 10s-30m")
	case c.DrainTimeout < 0 || c.DrainTimeout > time.Hour:
		return fmt.Errorf("platform: drain timeout must be at most 1h")
	case c.RecoverInterval < time.Second || c.RecoverInterval > 10*time.Minute:
		return fmt.Errorf("platform: recovery interval must be 1s-10m")
	case c.MaxRunAttempts < 1 || c.MaxRunAttempts > 10:
		return fmt.Errorf("platform: run attempts must be 1-10")
	case c.DefaultCurrency != "CNY" && c.DefaultCurrency != "USD":
		return fmt.Errorf("platform: default currency must be CNY or USD")
	case c.DefaultMaxConcurrentRuns < 1 || c.DefaultMaxConcurrentRuns > 1000:
		return fmt.Errorf("platform: project concurrency must be 1-1000")
	case c.DefaultRetentionDays < 1 || c.DefaultRetentionDays > 365:
		return fmt.Errorf("platform: retention must be 1-365 days")
	case c.DefaultRatePerMinute < 1 || c.DefaultRatePerMinute > 100000:
		return fmt.Errorf("platform: default rate limit must be 1-100000 per minute")
	case c.MaxUploadBytes < 1<<10 || c.MaxUploadBytes > 1<<30:
		return fmt.Errorf("platform: upload limit must be between 1KB and 1GB")
	case c.MaxRunInputBytes < c.MaxUploadBytes || c.MaxRunInputBytes > 4<<30:
		return fmt.Errorf("platform: run input limit must be at least the upload limit")
	case c.ModelRequestTimeout < 5*time.Second || c.ModelRequestTimeout > 15*time.Minute:
		return fmt.Errorf("platform: model request timeout must be 5s-15m")
	case c.ModelSendRetries < 0 || c.ModelSendRetries > 5:
		return fmt.Errorf("platform: model send retries must be 0-5")
	case c.DownloadURLTTL < time.Second || c.DownloadURLTTL > time.Hour:
		return fmt.Errorf("platform: download link lifetime must be 1s-1h")
	case utf8.RuneCountInString(c.WorkspaceName) < 1 || utf8.RuneCountInString(c.WorkspaceName) > 60 || strings.ContainsFunc(c.WorkspaceName, unicode.IsControl):
		return fmt.Errorf("platform: workspace name must be 1-60 characters without control characters")
	case c.StorageSampleInterval < time.Second || c.StorageSampleInterval > 24*time.Hour:
		return fmt.Errorf("platform: storage sample interval must be 1s-24h")
	}
	return nil
}

func (c Config) storageInterval() time.Duration {
	if c.storageEvery > 0 {
		return c.storageEvery
	}
	return c.StorageSampleInterval
}

func (c Config) janitorInterval() time.Duration {
	if c.janitorEvery > 0 {
		return c.janitorEvery
	}
	return time.Minute
}

func (c Config) heartbeat() time.Duration {
	if c.heartbeatEvery > 0 {
		return c.heartbeatEvery
	}
	return c.LeaseDuration / 3
}
