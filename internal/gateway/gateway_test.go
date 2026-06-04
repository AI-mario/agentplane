package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
)

// --- Mock services ---

type mockRegistry struct {
	agents map[string]*domain.AgentEntry
}

func newMockRegistry() *mockRegistry {
	return &mockRegistry{agents: make(map[string]*domain.AgentEntry)}
}

func (m *mockRegistry) Register(ctx context.Context, manifest domain.AgentManifest) (*domain.AgentEntry, error) {
	entry := &domain.AgentEntry{
		ID:           "agent-001",
		Name:         manifest.Name,
		Version:      manifest.Version,
		RuntimeType:  manifest.RuntimeType,
		Capabilities: manifest.Capabilities,
		Labels:       manifest.Labels,
		Status:       domain.AgentStatusActive,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	m.agents[entry.ID] = entry
	return entry, nil
}

func (m *mockRegistry) Get(ctx context.Context, id string) (*domain.AgentEntry, error) {
	agent, ok := m.agents[id]
	if !ok {
		return nil, fmt.Errorf("agent not found")
	}
	return agent, nil
}

func (m *mockRegistry) List(ctx context.Context, filter domain.AgentFilter) ([]*domain.AgentEntry, error) {
	var result []*domain.AgentEntry
	for _, a := range m.agents {
		result = append(result, a)
	}
	return result, nil
}

func (m *mockRegistry) FindByCapability(ctx context.Context, capability string) ([]*domain.AgentEntry, error) {
	return nil, nil
}

func (m *mockRegistry) Deregister(ctx context.Context, id string) error {
	if _, ok := m.agents[id]; !ok {
		return fmt.Errorf("agent not found")
	}
	delete(m.agents, id)
	return nil
}

type mockScheduler struct{}

func (m *mockScheduler) Schedule(ctx context.Context, mission *domain.Mission) (*domain.Assignment, error) {
	return &domain.Assignment{
		MissionID:  "mission-001",
		AgentID:    "agent-001",
		Score:      0.85,
		AssignedAt: time.Now(),
	}, nil
}

func (m *mockScheduler) Reschedule(ctx context.Context) error {
	return nil
}

type mockPolicy struct {
	policies map[string]*domain.Policy
}

func newMockPolicy() *mockPolicy {
	return &mockPolicy{policies: make(map[string]*domain.Policy)}
}

func (m *mockPolicy) Evaluate(ctx context.Context, req domain.PolicyRequest) (*domain.PolicyDecision, error) {
	return &domain.PolicyDecision{Allowed: true}, nil
}

func (m *mockPolicy) ApplyPolicy(ctx context.Context, pol *domain.Policy) error {
	pol.ID = "policy-001"
	m.policies[pol.ID] = pol
	return nil
}

func (m *mockPolicy) GetPolicy(ctx context.Context, id string) (*domain.Policy, error) {
	pol, ok := m.policies[id]
	if !ok {
		return nil, fmt.Errorf("policy not found")
	}
	return pol, nil
}

type mockCost struct{}

func (m *mockCost) RecordUsage(ctx context.Context, event *domain.CostEvent) error {
	return nil
}

func (m *mockCost) GetBudgetStatus(ctx context.Context, teamID string) (*domain.BudgetStatus, error) {
	if teamID == "unknown-team" {
		return nil, fmt.Errorf("budget not found")
	}
	return &domain.BudgetStatus{
		TeamID:          teamID,
		BudgetCap:       10000,
		AccumulatedCost: 5000,
		UtilizationPct:  50.0,
		PeriodStart:     time.Now().AddDate(0, 0, -15),
		PeriodEnd:       time.Now().AddDate(0, 0, 15),
		IsBlocked:       false,
	}, nil
}

func (m *mockCost) QueryCosts(ctx context.Context, filter domain.CostFilter) (*domain.CostReport, error) {
	return &domain.CostReport{Records: []*domain.CostRecord{}, Total: 0}, nil
}

func (m *mockCost) CheckBudget(ctx context.Context, teamID string, estimatedCost float64) (bool, error) {
	return true, nil
}

type mockSafety struct {
	killSwitchActive bool
}

func (m *mockSafety) ActivateKillSwitch(ctx context.Context) error {
	m.killSwitchActive = true
	return nil
}

func (m *mockSafety) DeactivateKillSwitch(ctx context.Context) error {
	m.killSwitchActive = false
	return nil
}

func (m *mockSafety) GetCircuitBreaker(ctx context.Context, agentID string) (*domain.CircuitBreakerState, error) {
	return &domain.CircuitBreakerState{AgentID: agentID, State: domain.CBClosed}, nil
}

func (m *mockSafety) ProbeAgent(ctx context.Context, agentID string) (*domain.ProbeResult, error) {
	return &domain.ProbeResult{AgentID: agentID, Success: true}, nil
}

type mockHealthChecker struct {
	healthy bool
}

func (m *mockHealthChecker) CheckHealth(ctx context.Context) error {
	if !m.healthy {
		return fmt.Errorf("unhealthy")
	}
	return nil
}

// --- Test helpers ---

func setupGateway() *Gateway {
	auth := NewDefaultAuthProvider()
	_ = auth.AddAPIKey("test-key-123", domain.Identity{
		Subject: "test-user",
		Team:    "platform",
		Scopes:  []string{"admin"},
		Role:    domain.RoleAdmin,
	})

	reg := newMockRegistry()
	reg.agents["agent-001"] = &domain.AgentEntry{
		ID:      "agent-001",
		Name:    "test-agent",
		Version: "1.0.0",
		Status:  domain.AgentStatusActive,
	}

	return NewGateway(
		GatewayConfig{Addr: ":8080", SessionTimeout: 30 * time.Minute},
		auth,
		reg,
		&mockScheduler{},
		newMockPolicy(),
		&mockCost{},
		&mockSafety{},
		nil,
		map[string]HealthChecker{
			"store":    &mockHealthChecker{healthy: true},
			"registry": &mockHealthChecker{healthy: true},
		},
	)
}

func authRequest(req *http.Request) *http.Request {
	req.Header.Set("X-API-Key", "test-key-123")
	return req
}

// --- Tests ---

func TestHealthEndpoint(t *testing.T) {
	gw := setupGateway()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp["status"] != "healthy" {
		t.Fatalf("expected healthy, got %v", resp["status"])
	}
}

func TestHealthEndpointUnhealthy(t *testing.T) {
	auth := NewDefaultAuthProvider()
	gw := NewGateway(
		GatewayConfig{},
		auth,
		newMockRegistry(),
		&mockScheduler{},
		newMockPolicy(),
		&mockCost{},
		&mockSafety{},
		nil,
		map[string]HealthChecker{
			"store": &mockHealthChecker{healthy: false},
		},
	)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestAuthRequired(t *testing.T) {
	gw := setupGateway()

	// No auth header → 401.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAuthWithInvalidKey(t *testing.T) {
	gw := setupGateway()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	req.Header.Set("X-API-Key", "wrong-key")
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAuthWithValidKey(t *testing.T) {
	gw := setupGateway()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	req = authRequest(req)
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestAuthWithBearerToken(t *testing.T) {
	auth := NewDefaultAuthProvider()
	auth.OAuth2TokenValidator = func(ctx context.Context, token string) (*domain.Identity, error) {
		if token == "valid-token" {
			return &domain.Identity{Subject: "oauth-user", Team: "eng"}, nil
		}
		return nil, fmt.Errorf("invalid token")
	}

	gw := NewGateway(
		GatewayConfig{},
		auth,
		newMockRegistry(),
		&mockScheduler{},
		newMockPolicy(),
		&mockCost{},
		&mockSafety{},
		nil,
		map[string]HealthChecker{},
	)

	// Valid token.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid Bearer, got %d", rec.Code)
	}

	// Invalid token.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer bad-token")
	rec = httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with bad Bearer, got %d", rec.Code)
	}
}

func TestRegisterAgent(t *testing.T) {
	gw := setupGateway()

	body := `{
		"name": "my-agent",
		"version": "1.0.0",
		"runtimeType": "claude",
		"capabilities": [{"name": "code-review", "type": "mcp-tool"}]
	}`

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = authRequest(req)
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRegisterAgentMissingFields(t *testing.T) {
	gw := setupGateway()

	body := `{"name": ""}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = authRequest(req)
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(resp.Fields) == 0 {
		t.Fatal("expected field-specific errors")
	}
}

func TestGetAgent(t *testing.T) {
	gw := setupGateway()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/agent-001", nil)
	req = authRequest(req)
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestGetAgentNotFound(t *testing.T) {
	gw := setupGateway()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/nonexistent", nil)
	req = authRequest(req)
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestDeregisterAgent(t *testing.T) {
	gw := setupGateway()

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/agent-001", nil)
	req = authRequest(req)
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestDeregisterAgentNotFound(t *testing.T) {
	gw := setupGateway()

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/nonexistent", nil)
	req = authRequest(req)
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestSubmitMission(t *testing.T) {
	gw := setupGateway()

	body := `{
		"requiredCapabilities": ["code-review"],
		"priority": 1,
		"teamId": "platform",
		"projectId": "backend",
		"timeout": "3600s"
	}`

	req := httptest.NewRequest(http.MethodPost, "/api/v1/missions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = authRequest(req)
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSubmitMissionMissingFields(t *testing.T) {
	gw := setupGateway()

	body := `{"priority": 1}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/missions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = authRequest(req)
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestApplyPolicy(t *testing.T) {
	gw := setupGateway()

	body := `{
		"name": "team-budget",
		"scope": "team",
		"teamId": "platform",
		"rules": [{"id": "r1", "type": "budget", "condition": "cost < 1000", "effect": "allow"}]
	}`

	req := httptest.NewRequest(http.MethodPost, "/api/v1/policies", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = authRequest(req)
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetPolicyNotFound(t *testing.T) {
	gw := setupGateway()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/policies/nonexistent", nil)
	req = authRequest(req)
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestQueryCosts(t *testing.T) {
	gw := setupGateway()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/costs?teamId=platform", nil)
	req = authRequest(req)
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestQueryCostsInvalidTime(t *testing.T) {
	gw := setupGateway()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/costs?startTime=not-a-date", nil)
	req = authRequest(req)
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestGetBudget(t *testing.T) {
	gw := setupGateway()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/budgets/platform", nil)
	req = authRequest(req)
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestGetBudgetNotFound(t *testing.T) {
	gw := setupGateway()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/budgets/unknown-team", nil)
	req = authRequest(req)
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestKillSwitch(t *testing.T) {
	gw := setupGateway()

	// Activate.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fleet/kill-switch", nil)
	req = authRequest(req)
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on activate, got %d", rec.Code)
	}

	// Deactivate.
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/fleet/kill-switch", nil)
	req = authRequest(req)
	rec = httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on deactivate, got %d", rec.Code)
	}
}

func TestSessionManagement(t *testing.T) {
	gw := setupGateway()

	// Create a session.
	identity := &domain.Identity{Subject: "dashboard-user", Team: "platform"}
	sessionID, err := gw.sessions.Create(identity)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// Use session to access protected endpoint.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	req.Header.Set("X-Session-ID", sessionID)
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid session, got %d", rec.Code)
	}

	// Invalid session → fallback to key check → 401.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	req.Header.Set("X-Session-ID", "bogus-session")
	rec = httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with invalid session, got %d", rec.Code)
	}
}

func TestSessionExpiry(t *testing.T) {
	// Use a very short timeout.
	auth := NewDefaultAuthProvider()
	gw := NewGateway(
		GatewayConfig{SessionTimeout: 1 * time.Millisecond},
		auth,
		newMockRegistry(),
		&mockScheduler{},
		newMockPolicy(),
		&mockCost{},
		&mockSafety{},
		nil,
		map[string]HealthChecker{},
	)

	identity := &domain.Identity{Subject: "test", Team: "t"}
	sessionID, _ := gw.sessions.Create(identity)

	// Wait for expiry.
	time.Sleep(5 * time.Millisecond)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	req.Header.Set("X-Session-ID", sessionID)
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for expired session, got %d", rec.Code)
	}
}

func TestMalformedJSON(t *testing.T) {
	gw := setupGateway()

	body := `{invalid json`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = authRequest(req)
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed JSON, got %d", rec.Code)
	}
}
