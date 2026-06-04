package property

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentplane/agentplane/internal/config"
	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/gateway"
	"pgregory.net/rapid"
)

// --- Property 60: Configuration Precedence ---
// For any param at multiple levels, highest-precedence source wins (flags > env > YAML).
// **Validates: Requirements 15.4**

// configParam represents a testable config parameter with its YAML key, env var, and flag name.
type configParam struct {
	Name    string
	YAMLKey string
	EnvVar  string
	FlagKey string
}

// stringConfigParams are config parameters that take string values.
var stringConfigParams = []configParam{
	{Name: "HTTPAddr", YAMLKey: "httpAddr", EnvVar: "AGENTPLANE_HTTP_ADDR", FlagKey: "http-addr"},
	{Name: "GRPCAddr", YAMLKey: "grpcAddr", EnvVar: "AGENTPLANE_GRPC_ADDR", FlagKey: "grpc-addr"},
	{Name: "MCPAddr", YAMLKey: "mcpAddr", EnvVar: "AGENTPLANE_MCP_ADDR", FlagKey: "mcp-addr"},
	{Name: "A2AAddr", YAMLKey: "a2aAddr", EnvVar: "AGENTPLANE_A2A_ADDR", FlagKey: "a2a-addr"},
	{Name: "StorageBackend", YAMLKey: "storageBackend", EnvVar: "AGENTPLANE_STORAGE_BACKEND", FlagKey: "storage-backend"},
	{Name: "LogLevel", YAMLKey: "logLevel", EnvVar: "AGENTPLANE_LOG_LEVEL", FlagKey: "log-level"},
	{Name: "BudgetPeriod", YAMLKey: "budgetPeriod", EnvVar: "AGENTPLANE_BUDGET_PERIOD", FlagKey: "budget-period"},
	{Name: "OTLPEndpoint", YAMLKey: "otlpEndpoint", EnvVar: "AGENTPLANE_OTLP_ENDPOINT", FlagKey: "otlp-endpoint"},
}

// getStringField extracts the string field value from a Config based on param name.
func getStringField(cfg *config.Config, paramName string) string {
	switch paramName {
	case "HTTPAddr":
		return cfg.HTTPAddr
	case "GRPCAddr":
		return cfg.GRPCAddr
	case "MCPAddr":
		return cfg.MCPAddr
	case "A2AAddr":
		return cfg.A2AAddr
	case "StorageBackend":
		return cfg.StorageBackend
	case "LogLevel":
		return cfg.LogLevel
	case "BudgetPeriod":
		return cfg.BudgetPeriod
	case "OTLPEndpoint":
		return cfg.OTLPEndpoint
	default:
		return ""
	}
}

// genDistinctStringValues generates 3 distinct non-empty string values for YAML, env, and flag.
func genDistinctStringValues() *rapid.Generator[[3]string] {
	return rapid.Custom(func(t *rapid.T) [3]string {
		// Use simple port-like or identifier-like strings.
		base := rapid.IntRange(1000, 9000).Draw(t, "base")
		return [3]string{
			fmt.Sprintf(":%d", base),
			fmt.Sprintf(":%d", base+1),
			fmt.Sprintf(":%d", base+2),
		}
	})
}

// clearConfigEnvVars unsets all AGENTPLANE_ environment variables.
func clearConfigEnvVars(t *testing.T) {
	envVars := []string{
		"AGENTPLANE_HTTP_ADDR", "AGENTPLANE_GRPC_ADDR", "AGENTPLANE_MCP_ADDR",
		"AGENTPLANE_A2A_ADDR", "AGENTPLANE_STORAGE_BACKEND", "AGENTPLANE_SQLITE_DSN",
		"AGENTPLANE_POSTGRES_DSN", "AGENTPLANE_SESSION_TIMEOUT", "AGENTPLANE_MISSION_TIMEOUT",
		"AGENTPLANE_RETENTION_DAYS", "AGENTPLANE_BUDGET_PERIOD", "AGENTPLANE_LOG_LEVEL",
		"AGENTPLANE_CB_COOLDOWN", "AGENTPLANE_OTLP_ENDPOINT", "AGENTPLANE_DEPLOY_TIMEOUT",
		"AGENTPLANE_DRAIN_TIMEOUT", "AGENTPLANE_SCHEDULER_COST_WEIGHT",
		"AGENTPLANE_SCHEDULER_LATENCY_WEIGHT", "AGENTPLANE_SCHEDULER_LOAD_WEIGHT",
	}
	for _, k := range envVars {
		os.Unsetenv(k)
	}
	_ = t
}

func TestProperty60_ConfigPrecedence_FlagOverridesEnvAndYAML(t *testing.T) {
	// For any string config param with values at all three levels,
	// the flag value always wins.
	rapid.Check(t, func(rt *rapid.T) {
		clearConfigEnvVars(t)

		param := rapid.SampledFrom(stringConfigParams).Draw(rt, "param")
		values := genDistinctStringValues().Draw(rt, "values")
		yamlVal, envVal, flagVal := values[0], values[1], values[2]

		// Write YAML with the parameter.
		yamlContent := fmt.Sprintf("%s: %q\n", param.YAMLKey, yamlVal)
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(path, []byte(yamlContent), 0644); err != nil {
			t.Fatal(err)
		}

		// Set env var.
		os.Setenv(param.EnvVar, envVal)
		defer os.Unsetenv(param.EnvVar)

		// Set flag.
		flags := map[string]string{param.FlagKey: flagVal}

		cfg, err := config.Load(path, flags)
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}

		got := getStringField(cfg, param.Name)
		if got != flagVal {
			t.Fatalf("param %s: expected flag value %q, got %q (yaml=%q, env=%q)",
				param.Name, flagVal, got, yamlVal, envVal)
		}
	})
}

func TestProperty60_ConfigPrecedence_EnvOverridesYAML(t *testing.T) {
	// For any string config param with values at YAML and env levels (no flag),
	// the env value wins.
	rapid.Check(t, func(rt *rapid.T) {
		clearConfigEnvVars(t)

		param := rapid.SampledFrom(stringConfigParams).Draw(rt, "param")
		values := genDistinctStringValues().Draw(rt, "values")
		yamlVal, envVal := values[0], values[1]

		// Write YAML.
		yamlContent := fmt.Sprintf("%s: %q\n", param.YAMLKey, yamlVal)
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(path, []byte(yamlContent), 0644); err != nil {
			t.Fatal(err)
		}

		// Set env var.
		os.Setenv(param.EnvVar, envVal)
		defer os.Unsetenv(param.EnvVar)

		cfg, err := config.Load(path, nil)
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}

		got := getStringField(cfg, param.Name)
		if got != envVal {
			t.Fatalf("param %s: expected env value %q, got %q (yaml=%q)",
				param.Name, envVal, got, yamlVal)
		}
	})
}

func TestProperty60_ConfigPrecedence_YAMLOverridesDefault(t *testing.T) {
	// For any string config param with a YAML value (no env, no flag),
	// the YAML value wins over default.
	rapid.Check(t, func(rt *rapid.T) {
		clearConfigEnvVars(t)

		param := rapid.SampledFrom(stringConfigParams).Draw(rt, "param")
		values := genDistinctStringValues().Draw(rt, "values")
		yamlVal := values[0]

		yamlContent := fmt.Sprintf("%s: %q\n", param.YAMLKey, yamlVal)
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(path, []byte(yamlContent), 0644); err != nil {
			t.Fatal(err)
		}

		cfg, err := config.Load(path, nil)
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}

		got := getStringField(cfg, param.Name)
		if got != yamlVal {
			t.Fatalf("param %s: expected YAML value %q, got %q",
				param.Name, yamlVal, got)
		}
	})
}

func TestProperty60_ConfigPrecedence_IntegerRetentionDays(t *testing.T) {
	// Test precedence with an integer param (retentionDays): flag > env > YAML.
	rapid.Check(t, func(rt *rapid.T) {
		clearConfigEnvVars(t)

		yamlDays := rapid.IntRange(1, 30).Draw(rt, "yamlDays")
		envDays := rapid.IntRange(31, 60).Draw(rt, "envDays")
		flagDays := rapid.IntRange(61, 90).Draw(rt, "flagDays")

		yamlContent := fmt.Sprintf("retentionDays: %d\n", yamlDays)
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(path, []byte(yamlContent), 0644); err != nil {
			t.Fatal(err)
		}

		os.Setenv("AGENTPLANE_RETENTION_DAYS", fmt.Sprintf("%d", envDays))
		defer os.Unsetenv("AGENTPLANE_RETENTION_DAYS")

		flags := map[string]string{"retention-days": fmt.Sprintf("%d", flagDays)}

		cfg, err := config.Load(path, flags)
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}

		if cfg.RetentionDays != flagDays {
			t.Fatalf("retentionDays: expected flag value %d, got %d (yaml=%d, env=%d)",
				flagDays, cfg.RetentionDays, yamlDays, envDays)
		}
	})
}

// --- Property 61: Health Endpoint Correctness ---
// All healthy → 200; any unhealthy → non-200 + unhealthy subsystem indicated.
// **Validates: Requirements 15.5, 15.6**

// mockHealthChecker implements gateway.HealthChecker with configurable health status.
type mockHealthChecker struct {
	healthy bool
	errMsg  string
}

func (m *mockHealthChecker) CheckHealth(ctx context.Context) error {
	if m.healthy {
		return nil
	}
	return fmt.Errorf("%s", m.errMsg)
}

// subsystemState represents a random subsystem with a health/unhealthy state.
type subsystemState struct {
	Name    string
	Healthy bool
}

// genSubsystems generates 1-6 subsystems with random health states.
func genSubsystems(allHealthy bool) *rapid.Generator[[]subsystemState] {
	return rapid.Custom(func(t *rapid.T) []subsystemState {
		count := rapid.IntRange(1, 6).Draw(t, "subsystemCount")
		names := []string{"store", "scheduler", "registry", "policy", "cost", "safety"}
		states := make([]subsystemState, count)
		for i := 0; i < count; i++ {
			healthy := true
			if !allHealthy {
				healthy = rapid.Bool().Draw(t, fmt.Sprintf("healthy_%d", i))
			}
			states[i] = subsystemState{
				Name:    names[i],
				Healthy: healthy,
			}
		}
		// If not allHealthy, ensure at least one is unhealthy.
		if !allHealthy {
			hasUnhealthy := false
			for _, s := range states {
				if !s.Healthy {
					hasUnhealthy = true
					break
				}
			}
			if !hasUnhealthy {
				idx := rapid.IntRange(0, count-1).Draw(t, "forceUnhealthyIdx")
				states[idx].Healthy = false
			}
		}
		return states
	})
}

func buildHealthGateway(subsystems []subsystemState) *gateway.Gateway {
	auth := &fastAuthProvider{
		validAPIKey:    testAPIKey,
		validOAuthToken: testOAuthToken,
		identity: &domain.Identity{
			Subject: "health-test-user",
			Team:    "platform",
			Scopes:  []string{"admin"},
			Role:    domain.RoleAdmin,
		},
	}

	checkers := make(map[string]gateway.HealthChecker, len(subsystems))
	for _, s := range subsystems {
		errMsg := ""
		if !s.Healthy {
			errMsg = s.Name + " is unhealthy"
		}
		checkers[s.Name] = &mockHealthChecker{healthy: s.Healthy, errMsg: errMsg}
	}

	return gateway.NewGateway(
		gateway.GatewayConfig{Addr: ":0", SessionTimeout: 30 * time.Minute},
		auth,
		newGwMockRegistry(),
		&gwMockScheduler{},
		newGwMockPolicy(),
		&gwMockCost{},
		&gwMockSafety{},
		nil,
		checkers,
	)
}

func TestProperty61_HealthEndpoint_AllHealthy(t *testing.T) {
	// For any set of subsystems all reporting healthy, health endpoint returns 200.
	rapid.Check(t, func(rt *rapid.T) {
		subsystems := genSubsystems(true).Draw(rt, "subsystems")
		gw := buildHealthGateway(subsystems)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		rec := httptest.NewRecorder()
		gw.Router().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("all healthy: expected 200, got %d, body: %s", rec.Code, rec.Body.String())
		}

		var resp map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("response not valid JSON: %v", err)
		}
		if resp["status"] != "healthy" {
			t.Fatalf("expected status 'healthy', got %v", resp["status"])
		}
	})
}

func TestProperty61_HealthEndpoint_AnyUnhealthy(t *testing.T) {
	// For any set of subsystems where at least one is unhealthy,
	// health endpoint returns non-200 and indicates unhealthy subsystem(s).
	rapid.Check(t, func(rt *rapid.T) {
		subsystems := genSubsystems(false).Draw(rt, "subsystems")
		gw := buildHealthGateway(subsystems)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		rec := httptest.NewRecorder()
		gw.Router().ServeHTTP(rec, req)

		if rec.Code == http.StatusOK {
			t.Fatalf("unhealthy subsystems present: expected non-200, got 200")
		}

		var resp map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("response not valid JSON: %v", err)
		}

		// Status should indicate unhealthy.
		if resp["status"] != "unhealthy" {
			t.Fatalf("expected status 'unhealthy', got %v", resp["status"])
		}

		// Verify unhealthy subsystems are indicated in the response.
		rawSubsystems, ok := resp["subsystems"]
		if !ok {
			t.Fatal("response missing 'subsystems' field")
		}

		subsystemsList, ok := rawSubsystems.([]interface{})
		if !ok {
			t.Fatalf("subsystems field not an array: %T", rawSubsystems)
		}

		// Collect unhealthy names from response.
		unhealthyInResponse := make(map[string]bool)
		for _, item := range subsystemsList {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			healthy, _ := m["healthy"].(bool)
			name, _ := m["name"].(string)
			if !healthy {
				unhealthyInResponse[name] = true
			}
		}

		// Verify each expected-unhealthy subsystem is reported.
		for _, s := range subsystems {
			if !s.Healthy {
				if !unhealthyInResponse[s.Name] {
					t.Fatalf("unhealthy subsystem %q not indicated in response", s.Name)
				}
			}
		}
	})
}

func TestProperty61_HealthEndpoint_NoAuth(t *testing.T) {
	// Health endpoint does not require authentication — always accessible.
	rapid.Check(t, func(rt *rapid.T) {
		allHealthy := rapid.Bool().Draw(rt, "allHealthy")
		var subsystems []subsystemState
		if allHealthy {
			subsystems = genSubsystems(true).Draw(rt, "subsystems")
		} else {
			subsystems = genSubsystems(false).Draw(rt, "subsystems")
		}
		gw := buildHealthGateway(subsystems)

		// No auth headers at all.
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		rec := httptest.NewRecorder()
		gw.Router().ServeHTTP(rec, req)

		// Should never get 401.
		if rec.Code == http.StatusUnauthorized {
			t.Fatal("health endpoint should not require authentication")
		}

		// Should be 200 if all healthy, non-200 otherwise.
		if allHealthy && rec.Code != http.StatusOK {
			t.Fatalf("all healthy + no auth: expected 200, got %d", rec.Code)
		}
		if !allHealthy && rec.Code == http.StatusOK {
			t.Fatal("unhealthy + no auth: expected non-200, got 200")
		}
	})
}
