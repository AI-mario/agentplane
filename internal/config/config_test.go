package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, ":8080")
	}
	if cfg.StorageBackend != "sqlite" {
		t.Errorf("StorageBackend = %q, want %q", cfg.StorageBackend, "sqlite")
	}
	if cfg.SQLiteDSN == "" {
		t.Error("SQLiteDSN should have a default value")
	}
	if cfg.MCPAddr != ":8081" {
		t.Errorf("MCPAddr = %q, want %q", cfg.MCPAddr, ":8081")
	}
	if cfg.A2AAddr != ":8082" {
		t.Errorf("A2AAddr = %q, want %q", cfg.A2AAddr, ":8082")
	}
	if cfg.GRPCAddr != ":9090" {
		t.Errorf("GRPCAddr = %q, want %q", cfg.GRPCAddr, ":9090")
	}
	if cfg.SessionTimeout != 30*time.Minute {
		t.Errorf("SessionTimeout = %v, want %v", cfg.SessionTimeout, 30*time.Minute)
	}
	if cfg.MissionTimeout != 3600*time.Second {
		t.Errorf("MissionTimeout = %v, want %v", cfg.MissionTimeout, 3600*time.Second)
	}
	if cfg.RetentionDays != 30 {
		t.Errorf("RetentionDays = %d, want %d", cfg.RetentionDays, 30)
	}
	if cfg.BudgetPeriod != "monthly" {
		t.Errorf("BudgetPeriod = %q, want %q", cfg.BudgetPeriod, "monthly")
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "info")
	}
	if cfg.SchedulerCostWeight != 0.4 {
		t.Errorf("SchedulerCostWeight = %f, want %f", cfg.SchedulerCostWeight, 0.4)
	}
	if cfg.SchedulerLatencyWeight != 0.3 {
		t.Errorf("SchedulerLatencyWeight = %f, want %f", cfg.SchedulerLatencyWeight, 0.3)
	}
	if cfg.SchedulerLoadWeight != 0.3 {
		t.Errorf("SchedulerLoadWeight = %f, want %f", cfg.SchedulerLoadWeight, 0.3)
	}
}

func TestLoadYAML(t *testing.T) {
	yamlContent := `
httpAddr: ":9000"
grpcAddr: ":9999"
mcpAddr: ":9081"
a2aAddr: ":9082"
storageBackend: postgres
postgresDSN: "postgres://localhost/agentplane"
sessionTimeout: 1h
missionTimeout: 1800s
retentionDays: 60
budgetPeriod: weekly
logLevel: debug
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yamlContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, nil)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.HTTPAddr != ":9000" {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, ":9000")
	}
	if cfg.GRPCAddr != ":9999" {
		t.Errorf("GRPCAddr = %q, want %q", cfg.GRPCAddr, ":9999")
	}
	if cfg.MCPAddr != ":9081" {
		t.Errorf("MCPAddr = %q, want %q", cfg.MCPAddr, ":9081")
	}
	if cfg.A2AAddr != ":9082" {
		t.Errorf("A2AAddr = %q, want %q", cfg.A2AAddr, ":9082")
	}
	if cfg.StorageBackend != "postgres" {
		t.Errorf("StorageBackend = %q, want %q", cfg.StorageBackend, "postgres")
	}
	if cfg.PostgresDSN != "postgres://localhost/agentplane" {
		t.Errorf("PostgresDSN = %q, want %q", cfg.PostgresDSN, "postgres://localhost/agentplane")
	}
	if cfg.SessionTimeout != time.Hour {
		t.Errorf("SessionTimeout = %v, want %v", cfg.SessionTimeout, time.Hour)
	}
	if cfg.MissionTimeout != 1800*time.Second {
		t.Errorf("MissionTimeout = %v, want %v", cfg.MissionTimeout, 1800*time.Second)
	}
	if cfg.RetentionDays != 60 {
		t.Errorf("RetentionDays = %d, want %d", cfg.RetentionDays, 60)
	}
	if cfg.BudgetPeriod != "weekly" {
		t.Errorf("BudgetPeriod = %q, want %q", cfg.BudgetPeriod, "weekly")
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "debug")
	}
}

func TestEnvOverridesYAML(t *testing.T) {
	yamlContent := `
httpAddr: ":9000"
storageBackend: sqlite
logLevel: debug
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yamlContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Set env vars that should override YAML.
	t.Setenv("AGENTPLANE_HTTP_ADDR", ":7070")
	t.Setenv("AGENTPLANE_STORAGE_BACKEND", "postgres")
	t.Setenv("AGENTPLANE_LOG_LEVEL", "warn")

	cfg, err := Load(path, nil)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.HTTPAddr != ":7070" {
		t.Errorf("HTTPAddr = %q, want %q (env should override YAML)", cfg.HTTPAddr, ":7070")
	}
	if cfg.StorageBackend != "postgres" {
		t.Errorf("StorageBackend = %q, want %q (env should override YAML)", cfg.StorageBackend, "postgres")
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, want %q (env should override YAML)", cfg.LogLevel, "warn")
	}
}

func TestFlagsOverrideEnvAndYAML(t *testing.T) {
	yamlContent := `
httpAddr: ":9000"
logLevel: debug
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yamlContent), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("AGENTPLANE_HTTP_ADDR", ":7070")
	t.Setenv("AGENTPLANE_LOG_LEVEL", "warn")

	flags := map[string]string{
		"http-addr": ":5555",
		"log-level": "error",
	}

	cfg, err := Load(path, flags)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.HTTPAddr != ":5555" {
		t.Errorf("HTTPAddr = %q, want %q (flag should override env and YAML)", cfg.HTTPAddr, ":5555")
	}
	if cfg.LogLevel != "error" {
		t.Errorf("LogLevel = %q, want %q (flag should override env and YAML)", cfg.LogLevel, "error")
	}
}

func TestInvalidYAMLReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("{{{{not yaml"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path, nil)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestMissingConfigFileReturnsError(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml", nil)
	if err == nil {
		t.Fatal("expected error for missing config file, got nil")
	}
}

func TestNoConfigFileUsesDefaults(t *testing.T) {
	// Clear any env vars that might interfere.
	for _, key := range []string{
		"AGENTPLANE_HTTP_ADDR", "AGENTPLANE_GRPC_ADDR", "AGENTPLANE_MCP_ADDR",
		"AGENTPLANE_A2A_ADDR", "AGENTPLANE_STORAGE_BACKEND", "AGENTPLANE_SQLITE_DSN",
		"AGENTPLANE_POSTGRES_DSN", "AGENTPLANE_SESSION_TIMEOUT", "AGENTPLANE_MISSION_TIMEOUT",
		"AGENTPLANE_RETENTION_DAYS", "AGENTPLANE_BUDGET_PERIOD", "AGENTPLANE_LOG_LEVEL",
		"AGENTPLANE_CB_COOLDOWN", "AGENTPLANE_OTLP_ENDPOINT", "AGENTPLANE_DEPLOY_TIMEOUT",
		"AGENTPLANE_DRAIN_TIMEOUT", "AGENTPLANE_SCHEDULER_COST_WEIGHT",
		"AGENTPLANE_SCHEDULER_LATENCY_WEIGHT", "AGENTPLANE_SCHEDULER_LOAD_WEIGHT",
	} {
		t.Setenv(key, "")
	}

	cfg, err := Load("", nil)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	defaults := DefaultConfig()
	if cfg.HTTPAddr != defaults.HTTPAddr {
		t.Errorf("HTTPAddr = %q, want default %q", cfg.HTTPAddr, defaults.HTTPAddr)
	}
	if cfg.StorageBackend != defaults.StorageBackend {
		t.Errorf("StorageBackend = %q, want default %q", cfg.StorageBackend, defaults.StorageBackend)
	}
	if cfg.MissionTimeout != defaults.MissionTimeout {
		t.Errorf("MissionTimeout = %v, want default %v", cfg.MissionTimeout, defaults.MissionTimeout)
	}
}

func TestAutoInitSQLiteDefault(t *testing.T) {
	// When no external DB configured, SQLite DSN should have a value.
	cfg, err := Load("", nil)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.StorageBackend != "sqlite" {
		t.Errorf("StorageBackend = %q, want %q", cfg.StorageBackend, "sqlite")
	}
	if cfg.SQLiteDSN == "" {
		t.Error("SQLiteDSN should be auto-initialized when no external DB configured")
	}
}

func TestEnvDurationParsing(t *testing.T) {
	t.Setenv("AGENTPLANE_SESSION_TIMEOUT", "45m")
	t.Setenv("AGENTPLANE_MISSION_TIMEOUT", "7200s")

	cfg, err := Load("", nil)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.SessionTimeout != 45*time.Minute {
		t.Errorf("SessionTimeout = %v, want %v", cfg.SessionTimeout, 45*time.Minute)
	}
	if cfg.MissionTimeout != 7200*time.Second {
		t.Errorf("MissionTimeout = %v, want %v", cfg.MissionTimeout, 7200*time.Second)
	}
}

func TestEnvRetentionDays(t *testing.T) {
	t.Setenv("AGENTPLANE_RETENTION_DAYS", "90")

	cfg, err := Load("", nil)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.RetentionDays != 90 {
		t.Errorf("RetentionDays = %d, want %d", cfg.RetentionDays, 90)
	}
}

func TestFlagStorageBackend(t *testing.T) {
	t.Setenv("AGENTPLANE_STORAGE_BACKEND", "sqlite")

	flags := map[string]string{
		"storage-backend": "postgres",
		"postgres-dsn":    "postgres://localhost/test",
	}

	cfg, err := Load("", flags)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.StorageBackend != "postgres" {
		t.Errorf("StorageBackend = %q, want %q (flag should override env)", cfg.StorageBackend, "postgres")
	}
	if cfg.PostgresDSN != "postgres://localhost/test" {
		t.Errorf("PostgresDSN = %q, want %q", cfg.PostgresDSN, "postgres://localhost/test")
	}
}

func TestFlagSessionTimeout(t *testing.T) {
	t.Setenv("AGENTPLANE_SESSION_TIMEOUT", "45m")

	flags := map[string]string{
		"session-timeout": "10m",
	}

	cfg, err := Load("", flags)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.SessionTimeout != 10*time.Minute {
		t.Errorf("SessionTimeout = %v, want %v (flag should win)", cfg.SessionTimeout, 10*time.Minute)
	}
}
