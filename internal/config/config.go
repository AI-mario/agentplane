// Package config handles configuration loading with precedence: CLI flags > env vars > YAML file.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all AgentPlane configuration.
type Config struct {
	// Server settings
	HTTPAddr string `yaml:"httpAddr"`
	GRPCAddr string `yaml:"grpcAddr"`
	MCPAddr  string `yaml:"mcpAddr"`
	A2AAddr  string `yaml:"a2aAddr"`

	// Storage backend: "sqlite" or "postgres"
	StorageBackend string `yaml:"storageBackend"`
	// SQLite DSN (file path or in-memory)
	SQLiteDSN string `yaml:"sqliteDSN"`
	// PostgreSQL connection string
	PostgresDSN string `yaml:"postgresDSN"`

	// Session idle timeout for dashboard
	SessionTimeout time.Duration `yaml:"sessionTimeout"`

	// Scheduler weights
	SchedulerCostWeight    float64 `yaml:"schedulerCostWeight"`
	SchedulerLatencyWeight float64 `yaml:"schedulerLatencyWeight"`
	SchedulerLoadWeight    float64 `yaml:"schedulerLoadWeight"`

	// Observability
	OTLPEndpoint   string `yaml:"otlpEndpoint"`
	RetentionDays  int    `yaml:"retentionDays"`
	MissionTimeout time.Duration `yaml:"missionTimeout"`

	// Safety mesh
	CircuitBreakerCooldown int `yaml:"circuitBreakerCooldown"`

	// Cost controller
	BudgetPeriod string `yaml:"budgetPeriod"`

	// Lifecycle
	DeployTimeout   time.Duration `yaml:"deployTimeout"`
	MaxDrainTimeout time.Duration `yaml:"maxDrainTimeout"`

	// Log level
	LogLevel string `yaml:"logLevel"`

	// Gemini / Vertex AI adapter
	GeminiAPIKey             string `yaml:"geminiApiKey"`
	GeminiProjectID          string `yaml:"geminiProjectId"`
	GeminiLocation           string `yaml:"geminiLocation"`
	GeminiModel              string `yaml:"geminiModel"`
	GeminiServiceAccountJSON string `yaml:"geminiServiceAccountJson"`
}

// DefaultConfig returns a Config with spec-mandated defaults.
func DefaultConfig() Config {
	return Config{
		HTTPAddr:               ":8080",
		GRPCAddr:               ":9090",
		MCPAddr:                ":8081",
		A2AAddr:                ":8082",
		StorageBackend:         "sqlite",
		SQLiteDSN:              "file:agentplane.db?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on",
		PostgresDSN:            "",
		SessionTimeout:         30 * time.Minute,
		SchedulerCostWeight:    0.4,
		SchedulerLatencyWeight: 0.3,
		SchedulerLoadWeight:    0.3,
		OTLPEndpoint:           "",
		RetentionDays:          30,
		MissionTimeout:         3600 * time.Second,
		CircuitBreakerCooldown: 60,
		BudgetPeriod:           "monthly",
		DeployTimeout:          60 * time.Second,
		MaxDrainTimeout:        300 * time.Second,
		LogLevel:               "info",
	}
}

// Load reads configuration from the given YAML file path (if non-empty),
// overlays environment variables, then overlays CLI flag overrides.
// Precedence: flags > env > YAML > defaults.
func Load(yamlPath string, flags map[string]string) (*Config, error) {
	cfg := DefaultConfig()

	// Layer 1: YAML file (lowest precedence after defaults)
	if yamlPath != "" {
		if err := loadYAML(yamlPath, &cfg); err != nil {
			return nil, fmt.Errorf("config parse failure: %w", err)
		}
	}

	// Layer 2: Environment variables
	applyEnv(&cfg)

	// Layer 3: CLI flags (highest precedence)
	if flags != nil {
		applyFlags(&cfg, flags)
	}

	return &cfg, nil
}

// loadYAML reads and parses a YAML config file into cfg.
func loadYAML(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading config file %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("parsing config file %s: %w", path, err)
	}
	return nil
}

// applyEnv overlays environment variables with AGENTPLANE_ prefix.
func applyEnv(cfg *Config) {
	if v := os.Getenv("AGENTPLANE_HTTP_ADDR"); v != "" {
		cfg.HTTPAddr = v
	}
	if v := os.Getenv("AGENTPLANE_GRPC_ADDR"); v != "" {
		cfg.GRPCAddr = v
	}
	if v := os.Getenv("AGENTPLANE_MCP_ADDR"); v != "" {
		cfg.MCPAddr = v
	}
	if v := os.Getenv("AGENTPLANE_A2A_ADDR"); v != "" {
		cfg.A2AAddr = v
	}
	if v := os.Getenv("AGENTPLANE_STORAGE_BACKEND"); v != "" {
		cfg.StorageBackend = v
	}
	if v := os.Getenv("AGENTPLANE_SQLITE_DSN"); v != "" {
		cfg.SQLiteDSN = v
	}
	if v := os.Getenv("AGENTPLANE_POSTGRES_DSN"); v != "" {
		cfg.PostgresDSN = v
	}
	if v := os.Getenv("AGENTPLANE_SESSION_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.SessionTimeout = d
		}
	}
	if v := os.Getenv("AGENTPLANE_OTLP_ENDPOINT"); v != "" {
		cfg.OTLPEndpoint = v
	}
	if v := os.Getenv("AGENTPLANE_RETENTION_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.RetentionDays = n
		}
	}
	if v := os.Getenv("AGENTPLANE_MISSION_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.MissionTimeout = d
		}
	}
	if v := os.Getenv("AGENTPLANE_CB_COOLDOWN"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.CircuitBreakerCooldown = n
		}
	}
	if v := os.Getenv("AGENTPLANE_BUDGET_PERIOD"); v != "" {
		cfg.BudgetPeriod = v
	}
	if v := os.Getenv("AGENTPLANE_DEPLOY_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.DeployTimeout = d
		}
	}
	if v := os.Getenv("AGENTPLANE_DRAIN_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.MaxDrainTimeout = d
		}
	}
	if v := os.Getenv("AGENTPLANE_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if v := os.Getenv("AGENTPLANE_SCHEDULER_COST_WEIGHT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.SchedulerCostWeight = f
		}
	}
	if v := os.Getenv("AGENTPLANE_SCHEDULER_LATENCY_WEIGHT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.SchedulerLatencyWeight = f
		}
	}
	if v := os.Getenv("AGENTPLANE_SCHEDULER_LOAD_WEIGHT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.SchedulerLoadWeight = f
		}
	}

	// Gemini / Vertex AI
	if v := os.Getenv("AGENTPLANE_GEMINI_API_KEY"); v != "" {
		cfg.GeminiAPIKey = v
	}
	if v := os.Getenv("AGENTPLANE_GEMINI_PROJECT_ID"); v != "" {
		cfg.GeminiProjectID = v
	}
	if v := os.Getenv("AGENTPLANE_GEMINI_LOCATION"); v != "" {
		cfg.GeminiLocation = v
	}
	if v := os.Getenv("AGENTPLANE_GEMINI_MODEL"); v != "" {
		cfg.GeminiModel = v
	}
	if v := os.Getenv("AGENTPLANE_GEMINI_SERVICE_ACCOUNT_JSON"); v != "" {
		cfg.GeminiServiceAccountJSON = v
	}
}

// applyFlags overlays CLI flag values onto cfg.
func applyFlags(cfg *Config, flags map[string]string) {
	if v, ok := flags["http-addr"]; ok {
		cfg.HTTPAddr = v
	}
	if v, ok := flags["grpc-addr"]; ok {
		cfg.GRPCAddr = v
	}
	if v, ok := flags["mcp-addr"]; ok {
		cfg.MCPAddr = v
	}
	if v, ok := flags["a2a-addr"]; ok {
		cfg.A2AAddr = v
	}
	if v, ok := flags["storage-backend"]; ok {
		cfg.StorageBackend = v
	}
	if v, ok := flags["sqlite-dsn"]; ok {
		cfg.SQLiteDSN = v
	}
	if v, ok := flags["postgres-dsn"]; ok {
		cfg.PostgresDSN = v
	}
	if v, ok := flags["session-timeout"]; ok {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.SessionTimeout = d
		}
	}
	if v, ok := flags["mission-timeout"]; ok {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.MissionTimeout = d
		}
	}
	if v, ok := flags["otlp-endpoint"]; ok {
		cfg.OTLPEndpoint = v
	}
	if v, ok := flags["retention-days"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.RetentionDays = n
		}
	}
	if v, ok := flags["budget-period"]; ok {
		cfg.BudgetPeriod = v
	}
	if v, ok := flags["log-level"]; ok {
		cfg.LogLevel = v
	}
	if v, ok := flags["cb-cooldown"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.CircuitBreakerCooldown = n
		}
	}
}
