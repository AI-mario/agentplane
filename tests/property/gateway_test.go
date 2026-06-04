package property

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/gateway"
	"pgregory.net/rapid"
)

// --- Mock services for gateway property tests ---

type gwMockRegistry struct {
	agents map[string]*domain.AgentEntry
}

func newGwMockRegistry() *gwMockRegistry {
	return &gwMockRegistry{agents: make(map[string]*domain.AgentEntry)}
}

func (m *gwMockRegistry) Register(ctx context.Context, manifest domain.AgentManifest) (*domain.AgentEntry, error) {
	entry := &domain.AgentEntry{
		ID:           fmt.Sprintf("agent-%d", len(m.agents)+1),
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

func (m *gwMockRegistry) Get(ctx context.Context, id string) (*domain.AgentEntry, error) {
	agent, ok := m.agents[id]
	if !ok {
		return nil, fmt.Errorf("agent not found")
	}
	return agent, nil
}

func (m *gwMockRegistry) List(ctx context.Context, filter domain.AgentFilter) ([]*domain.AgentEntry, error) {
	var result []*domain.AgentEntry
	for _, a := range m.agents {
		result = append(result, a)
	}
	return result, nil
}

func (m *gwMockRegistry) FindByCapability(ctx context.Context, capability string) ([]*domain.AgentEntry, error) {
	return nil, nil
}

func (m *gwMockRegistry) Deregister(ctx context.Context, id string) error {
	if _, ok := m.agents[id]; !ok {
		return fmt.Errorf("agent not found")
	}
	delete(m.agents, id)
	return nil
}

type gwMockScheduler struct{}

func (m *gwMockScheduler) Schedule(ctx context.Context, mission *domain.Mission) (*domain.Assignment, error) {
	return &domain.Assignment{
		MissionID:  "mission-001",
		AgentID:    "agent-001",
		Score:      0.85,
		AssignedAt: time.Now(),
	}, nil
}

func (m *gwMockScheduler) Reschedule(ctx context.Context) error { return nil }

type gwMockPolicy struct {
	policies map[string]*domain.Policy
}

func newGwMockPolicy() *gwMockPolicy {
	return &gwMockPolicy{policies: make(map[string]*domain.Policy)}
}

func (m *gwMockPolicy) Evaluate(ctx context.Context, req domain.PolicyRequest) (*domain.PolicyDecision, error) {
	return &domain.PolicyDecision{Allowed: true}, nil
}

func (m *gwMockPolicy) ApplyPolicy(ctx context.Context, pol *domain.Policy) error {
	pol.ID = "policy-001"
	m.policies[pol.ID] = pol
	return nil
}

func (m *gwMockPolicy) GetPolicy(ctx context.Context, id string) (*domain.Policy, error) {
	pol, ok := m.policies[id]
	if !ok {
		return nil, fmt.Errorf("policy not found")
	}
	return pol, nil
}

type gwMockCost struct{}

func (m *gwMockCost) RecordUsage(ctx context.Context, event *domain.CostEvent) error { return nil }

func (m *gwMockCost) GetBudgetStatus(ctx context.Context, teamID string) (*domain.BudgetStatus, error) {
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

func (m *gwMockCost) QueryCosts(ctx context.Context, filter domain.CostFilter) (*domain.CostReport, error) {
	return &domain.CostReport{Records: []*domain.CostRecord{}, Total: 0}, nil
}

func (m *gwMockCost) CheckBudget(ctx context.Context, teamID string, estimatedCost float64) (bool, error) {
	return true, nil
}

type gwMockSafety struct{}

func (m *gwMockSafety) ActivateKillSwitch(ctx context.Context) error   { return nil }
func (m *gwMockSafety) DeactivateKillSwitch(ctx context.Context) error { return nil }
func (m *gwMockSafety) GetCircuitBreaker(ctx context.Context, agentID string) (*domain.CircuitBreakerState, error) {
	return &domain.CircuitBreakerState{AgentID: agentID, State: domain.CBClosed}, nil
}
func (m *gwMockSafety) ProbeAgent(ctx context.Context, agentID string) (*domain.ProbeResult, error) {
	return &domain.ProbeResult{AgentID: agentID, Success: true}, nil
}

type gwMockHealthChecker struct{}

func (m *gwMockHealthChecker) CheckHealth(ctx context.Context) error { return nil }

// --- Fast auth provider (avoids bcrypt overhead in property tests) ---

type fastAuthProvider struct {
	validAPIKey    string
	validOAuthToken string
	identity       *domain.Identity
}

func (f *fastAuthProvider) ValidateAPIKey(ctx context.Context, key string) (*domain.Identity, error) {
	if key == f.validAPIKey {
		return f.identity, nil
	}
	return nil, fmt.Errorf("invalid api key")
}

func (f *fastAuthProvider) ValidateOAuth2Token(ctx context.Context, token string) (*domain.Identity, error) {
	if token == f.validOAuthToken {
		return f.identity, nil
	}
	return nil, fmt.Errorf("invalid token")
}

// --- Test setup ---

const testAPIKey = "property-test-key-abc123"
const testOAuthToken = "valid-oauth-token"

func setupPropertyGateway() *gateway.Gateway {
	auth := &fastAuthProvider{
		validAPIKey:    testAPIKey,
		validOAuthToken: testOAuthToken,
		identity: &domain.Identity{
			Subject: "prop-test-user",
			Team:    "platform",
			Scopes:  []string{"admin"},
			Role:    domain.RoleAdmin,
		},
	}

	reg := newGwMockRegistry()
	reg.agents["agent-exist"] = &domain.AgentEntry{
		ID:      "agent-exist",
		Name:    "existing-agent",
		Version: "1.0.0",
		Status:  domain.AgentStatusActive,
	}

	return gateway.NewGateway(
		gateway.GatewayConfig{Addr: ":0", SessionTimeout: 30 * time.Minute},
		auth,
		reg,
		&gwMockScheduler{},
		newGwMockPolicy(),
		&gwMockCost{},
		&gwMockSafety{},
		nil,
		map[string]gateway.HealthChecker{
			"store": &gwMockHealthChecker{},
		},
	)
}

// --- Generators ---

// genInvalidAPIKey generates random strings that are NOT the valid API key.
func genInvalidAPIKey() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		key := rapid.StringMatching(`[a-zA-Z0-9\-]{1,64}`).Draw(t, "apiKey")
		if key == testAPIKey {
			key = key + "-invalid"
		}
		return key
	})
}

// genInvalidBearerToken generates random strings that are NOT a valid OAuth2 token.
func genInvalidBearerToken() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		token := rapid.StringMatching(`[a-zA-Z0-9\-_.]{1,128}`).Draw(t, "token")
		if token == "valid-oauth-token" {
			token = token + "-bad"
		}
		return token
	})
}

// authEndpoint pairs a path with the correct HTTP method.
type authEndpoint struct {
	Method string
	Path   string
}

// genAuthenticatedEndpoint generates endpoints (method+path) that require authentication.
func genAuthenticatedEndpoint() *rapid.Generator[authEndpoint] {
	return rapid.SampledFrom([]authEndpoint{
		{http.MethodGet, "/api/v1/agents"},
		{http.MethodGet, "/api/v1/agents/agent-exist"},
		{http.MethodDelete, "/api/v1/agents/agent-exist"},
		{http.MethodGet, "/api/v1/policies/some-id"},
		{http.MethodGet, "/api/v1/costs"},
		{http.MethodGet, "/api/v1/budgets/platform"},
		{http.MethodPost, "/api/v1/fleet/kill-switch"},
		{http.MethodDelete, "/api/v1/fleet/kill-switch"},
	})
}

// genNonExistentResourcePath generates paths to resources that don't exist.
func genNonExistentResourcePath() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		// Generate a random ID that won't match existing resources.
		id := rapid.StringMatching(`[a-z0-9\-]{8,32}`).Draw(t, "resourceId")
		// Ensure it's not one of our existing mock IDs.
		if id == "agent-exist" || id == "policy-001" {
			id = id + "-nope"
		}
		endpoint := rapid.SampledFrom([]string{
			"/api/v1/agents/" + id,
			"/api/v1/policies/" + id,
			"/api/v1/budgets/unknown-team",
			"/api/v1/missions/" + id,
		}).Draw(t, "endpoint")
		return endpoint
	})
}

// genMalformedPayload generates payloads that are invalid for agent registration.
func genMalformedPayload() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		variant := rapid.IntRange(0, 4).Draw(t, "variant")
		switch variant {
		case 0:
			// Completely invalid JSON.
			return rapid.StringMatching(`[^{}"\[\]]{5,50}`).Draw(t, "garbage")
		case 1:
			// Valid JSON but missing required fields.
			return `{"namespace": "test"}`
		case 2:
			// Has name but missing version/runtimeType/capabilities.
			name := rapid.StringMatching(`[a-z][a-z0-9\-]{0,20}`).Draw(t, "name")
			return fmt.Sprintf(`{"name": "%s"}`, name)
		case 3:
			// Has name and version but empty capabilities.
			name := rapid.StringMatching(`[a-z][a-z0-9\-]{0,20}`).Draw(t, "name")
			return fmt.Sprintf(`{"name": "%s", "version": "1.0.0", "runtimeType": "claude", "capabilities": []}`, name)
		case 4:
			// Unknown fields (disallowed).
			return `{"name": "x", "version": "1.0.0", "runtimeType": "claude", "capabilities": [{"name":"c","type":"t"}], "unknownField": true}`
		default:
			return `{`
		}
	})
}

// --- Property 51: Authentication Enforcement ---
// Invalid/missing API key or OAuth2 token → 401 + logged attempt
// **Validates: Requirements 11.5**

func TestProperty51_AuthenticationEnforcement_MissingCredentials(t *testing.T) {
	// For any authenticated endpoint, a request with no credentials → 401.
	rapid.Check(t, func(t *rapid.T) {
		gw := setupPropertyGateway()
		ep := genAuthenticatedEndpoint().Draw(t, "endpoint")

		req := httptest.NewRequest(ep.Method, ep.Path, nil)
		rec := httptest.NewRecorder()
		gw.Router().ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("missing credentials: expected 401, got %d for %s %s", rec.Code, ep.Method, ep.Path)
		}

		// Verify response contains error indication.
		var resp map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("response not valid JSON: %v", err)
		}
		if _, ok := resp["error"]; !ok {
			t.Fatal("response missing 'error' field")
		}
	})
}

func TestProperty51_AuthenticationEnforcement_InvalidAPIKey(t *testing.T) {
	// For any invalid API key, request → 401.
	rapid.Check(t, func(t *rapid.T) {
		gw := setupPropertyGateway()
		ep := genAuthenticatedEndpoint().Draw(t, "endpoint")
		badKey := genInvalidAPIKey().Draw(t, "badKey")

		req := httptest.NewRequest(ep.Method, ep.Path, nil)
		req.Header.Set("X-API-Key", badKey)
		rec := httptest.NewRecorder()
		gw.Router().ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("invalid API key %q: expected 401, got %d for %s %s", badKey, rec.Code, ep.Method, ep.Path)
		}
	})
}

func TestProperty51_AuthenticationEnforcement_InvalidOAuth2Token(t *testing.T) {
	// For any invalid OAuth2 bearer token, request → 401.
	rapid.Check(t, func(t *rapid.T) {
		gw := setupPropertyGateway()
		ep := genAuthenticatedEndpoint().Draw(t, "endpoint")
		badToken := genInvalidBearerToken().Draw(t, "badToken")

		req := httptest.NewRequest(ep.Method, ep.Path, nil)
		req.Header.Set("Authorization", "Bearer "+badToken)
		rec := httptest.NewRecorder()
		gw.Router().ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("invalid Bearer token %q: expected 401, got %d for %s %s", badToken, rec.Code, ep.Method, ep.Path)
		}
	})
}

func TestProperty51_AuthenticationEnforcement_UnsupportedScheme(t *testing.T) {
	// For any unsupported auth scheme, request → 401.
	rapid.Check(t, func(t *rapid.T) {
		gw := setupPropertyGateway()
		ep := genAuthenticatedEndpoint().Draw(t, "endpoint")
		scheme := rapid.SampledFrom([]string{"Basic", "Digest", "NTLM", "Custom"}).Draw(t, "scheme")
		value := rapid.StringMatching(`[a-zA-Z0-9]{10,30}`).Draw(t, "value")

		req := httptest.NewRequest(ep.Method, ep.Path, nil)
		req.Header.Set("Authorization", scheme+" "+value)
		rec := httptest.NewRecorder()
		gw.Router().ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("unsupported scheme %q: expected 401, got %d for %s %s", scheme, rec.Code, ep.Method, ep.Path)
		}
	})
}

// --- Property 52: Malformed Request Rejection Without State Change ---
// Invalid payload → 400 + field error, state unchanged
// **Validates: Requirements 11.7**

func TestProperty52_MalformedRequestRejection(t *testing.T) {
	// For any malformed payload to POST /agents, gateway returns 400 with field error
	// and state (agent count) remains unchanged.
	rapid.Check(t, func(t *rapid.T) {
		gw := setupPropertyGateway()
		payload := genMalformedPayload().Draw(t, "payload")

		// Count agents before.
		listReq := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
		listReq.Header.Set("X-API-Key", testAPIKey)
		listRec := httptest.NewRecorder()
		gw.Router().ServeHTTP(listRec, listReq)

		var beforeResp map[string]interface{}
		_ = json.Unmarshal(listRec.Body.Bytes(), &beforeResp)
		countBefore := beforeResp["count"]

		// Send malformed request.
		req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewBufferString(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", testAPIKey)
		rec := httptest.NewRecorder()
		gw.Router().ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("malformed payload: expected 400, got %d, body: %s", rec.Code, rec.Body.String())
		}

		// Verify error response has field-specific info or error message.
		var errResp map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
			t.Fatalf("response not valid JSON: %v", err)
		}
		if _, ok := errResp["error"]; !ok {
			t.Fatal("error response missing 'error' field")
		}

		// Verify state unchanged: agent count same.
		listReq2 := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
		listReq2.Header.Set("X-API-Key", testAPIKey)
		listRec2 := httptest.NewRecorder()
		gw.Router().ServeHTTP(listRec2, listReq2)

		var afterResp map[string]interface{}
		_ = json.Unmarshal(listRec2.Body.Bytes(), &afterResp)
		countAfter := afterResp["count"]

		if countBefore != countAfter {
			t.Fatalf("state changed: agent count before=%v after=%v", countBefore, countAfter)
		}
	})
}

func TestProperty52_MalformedMissionRejection(t *testing.T) {
	// For any malformed mission payload, gateway returns 400 with error.
	rapid.Check(t, func(t *rapid.T) {
		gw := setupPropertyGateway()

		variant := rapid.IntRange(0, 2).Draw(t, "variant")
		var payload string
		switch variant {
		case 0:
			// Invalid JSON.
			payload = `{not valid json`
		case 1:
			// Missing required fields.
			payload = `{"priority": 1}`
		case 2:
			// Empty capabilities.
			payload = `{"requiredCapabilities": [], "teamId": "t"}`
		}

		req := httptest.NewRequest(http.MethodPost, "/api/v1/missions", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", testAPIKey)
		rec := httptest.NewRecorder()
		gw.Router().ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("malformed mission payload: expected 400, got %d, body: %s", rec.Code, rec.Body.String())
		}

		var errResp map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
			t.Fatalf("response not valid JSON: %v", err)
		}
		if _, ok := errResp["error"]; !ok {
			t.Fatal("error response missing 'error' field")
		}
	})
}

func TestProperty52_MalformedPolicyRejection(t *testing.T) {
	// For any malformed policy payload, gateway returns 400 with error.
	rapid.Check(t, func(t *rapid.T) {
		gw := setupPropertyGateway()

		variant := rapid.IntRange(0, 2).Draw(t, "variant")
		var payload string
		switch variant {
		case 0:
			payload = `{broken`
		case 1:
			// Missing name.
			payload = `{"scope": "team", "rules": [{"id":"r1","type":"budget","condition":"x","effect":"allow"}]}`
		case 2:
			// Missing rules.
			payload = `{"name": "test-policy", "scope": "org"}`
		}

		req := httptest.NewRequest(http.MethodPost, "/api/v1/policies", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", testAPIKey)
		rec := httptest.NewRecorder()
		gw.Router().ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("malformed policy payload: expected 400, got %d, body: %s", rec.Code, rec.Body.String())
		}

		var errResp map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
			t.Fatalf("response not valid JSON: %v", err)
		}
		if _, ok := errResp["error"]; !ok {
			t.Fatal("error response missing 'error' field")
		}
	})
}

// --- Property 53: Not-Found Resource Handling ---
// Valid auth + non-existent resource → 404
// **Validates: Requirements 11.9**

func TestProperty53_NotFoundResourceHandling(t *testing.T) {
	// For any valid auth + non-existent resource ID, gateway returns 404.
	rapid.Check(t, func(t *rapid.T) {
		gw := setupPropertyGateway()
		path := genNonExistentResourcePath().Draw(t, "path")

		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-API-Key", testAPIKey)
		rec := httptest.NewRecorder()
		gw.Router().ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("non-existent resource: expected 404, got %d for path %s, body: %s",
				rec.Code, path, rec.Body.String())
		}

		// Verify response contains error indication.
		var resp map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("response not valid JSON: %v", err)
		}
		if _, ok := resp["error"]; !ok {
			t.Fatal("404 response missing 'error' field")
		}
		errMsg, _ := resp["error"].(string)
		if !strings.Contains(strings.ToLower(errMsg), "not found") {
			t.Fatalf("404 error message should indicate 'not found', got: %s", errMsg)
		}
	})
}

func TestProperty53_NotFoundWithDelete(t *testing.T) {
	// DELETE on non-existent agent → 404.
	rapid.Check(t, func(t *rapid.T) {
		gw := setupPropertyGateway()
		id := rapid.StringMatching(`[a-z0-9\-]{8,32}`).Draw(t, "id")
		if id == "agent-exist" {
			id = id + "-gone"
		}

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/"+id, nil)
		req.Header.Set("X-API-Key", testAPIKey)
		rec := httptest.NewRecorder()
		gw.Router().ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("DELETE non-existent agent: expected 404, got %d for id %s", rec.Code, id)
		}
	})
}
