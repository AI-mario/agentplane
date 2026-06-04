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

// --- MCP-specific mock registry that returns MCP tools ---

type mcpMockRegistry struct {
	agents []*domain.AgentEntry
}

func (m *mcpMockRegistry) Register(ctx context.Context, manifest domain.AgentManifest) (*domain.AgentEntry, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mcpMockRegistry) Get(ctx context.Context, id string) (*domain.AgentEntry, error) {
	for _, a := range m.agents {
		if a.ID == id {
			return a, nil
		}
	}
	return nil, fmt.Errorf("agent not found")
}

func (m *mcpMockRegistry) List(ctx context.Context, filter domain.AgentFilter) ([]*domain.AgentEntry, error) {
	var result []*domain.AgentEntry
	for _, a := range m.agents {
		if filter.Status != "" && a.Status != filter.Status {
			continue
		}
		result = append(result, a)
	}
	return result, nil
}

func (m *mcpMockRegistry) FindByCapability(ctx context.Context, capability string) ([]*domain.AgentEntry, error) {
	return nil, nil
}

func (m *mcpMockRegistry) Deregister(ctx context.Context, id string) error {
	return nil
}

// --- MCP-specific mock policy ---

type mcpMockPolicy struct {
	deny       bool
	denyReason string
	evalErr    bool
}

func (m *mcpMockPolicy) Evaluate(ctx context.Context, req domain.PolicyRequest) (*domain.PolicyDecision, error) {
	if m.evalErr {
		return nil, fmt.Errorf("internal policy error")
	}
	if m.deny {
		return &domain.PolicyDecision{Allowed: false, Reason: m.denyReason}, nil
	}
	return &domain.PolicyDecision{Allowed: true}, nil
}

func (m *mcpMockPolicy) ApplyPolicy(ctx context.Context, pol *domain.Policy) error {
	return nil
}

func (m *mcpMockPolicy) GetPolicy(ctx context.Context, id string) (*domain.Policy, error) {
	return nil, fmt.Errorf("not found")
}

// --- MCP-specific mock scheduler ---

type mcpMockScheduler struct {
	fail      bool
	failMsg   string
}

func (m *mcpMockScheduler) Schedule(ctx context.Context, mission *domain.Mission) (*domain.Assignment, error) {
	if m.fail {
		return nil, fmt.Errorf(m.failMsg)
	}
	return &domain.Assignment{
		MissionID:  "mission-mcp-001",
		AgentID:    "agent-code-review",
		Score:      0.92,
		AssignedAt: time.Now(),
	}, nil
}

func (m *mcpMockScheduler) Reschedule(ctx context.Context) error {
	return nil
}

// --- MCP test helper ---

func setupMCPServer() (*MCPServer, *mcpMockRegistry, *mcpMockPolicy, *mcpMockScheduler) {
	auth := NewDefaultAuthProvider()
	_ = auth.AddAPIKey("mcp-test-key", domain.Identity{
		Subject: "mcp-user",
		Team:    "platform",
		Scopes:  []string{"tools"},
		Role:    domain.RoleAdmin,
	})

	reg := &mcpMockRegistry{
		agents: []*domain.AgentEntry{
			{
				ID:      "agent-code-review",
				Name:    "code-reviewer",
				Version: "2.1.0",
				Status:  domain.AgentStatusActive,
				Capabilities: []domain.Capability{
					{Name: "code-review", Type: "mcp-tool"},
					{Name: "lint-check", Type: "mcp-tool"},
				},
			},
			{
				ID:      "agent-messenger",
				Name:    "message-handler",
				Version: "1.0.0",
				Status:  domain.AgentStatusActive,
				Capabilities: []domain.Capability{
					{Name: "send-notification", Type: "a2a-message"},
				},
			},
			{
				ID:     "agent-inactive",
				Name:   "offline-agent",
				Status: domain.AgentStatusInactive,
				Capabilities: []domain.Capability{
					{Name: "data-analysis", Type: "mcp-tool"},
				},
			},
		},
	}

	pol := &mcpMockPolicy{}
	sched := &mcpMockScheduler{}

	mcp := NewMCPServer(MCPServerConfig{Addr: ":8081"}, auth, reg, sched, pol)
	return mcp, reg, pol, sched
}

func mcpRequest(method, path string, body interface{}) *http.Request {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "mcp-test-key")
	return req
}

func serveMCP(mcp *MCPServer, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mcp.server.Handler.ServeHTTP(rec, req)
	return rec
}

// --- Tests ---

func TestMCPToolsList(t *testing.T) {
	mcp, _, _, _ := setupMCPServer()

	req := mcpRequest("POST", "/mcp/tools/list", nil)
	rec := serveMCP(mcp, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MCPToolsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid response JSON: %v", err)
	}

	// Only active agents with mcp-tool capabilities should appear.
	// agent-code-review has 2 mcp-tools, agent-messenger has a2a (excluded),
	// agent-inactive is inactive (excluded).
	if len(resp.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d: %+v", len(resp.Tools), resp.Tools)
	}

	// Check tool names.
	names := map[string]bool{}
	for _, tool := range resp.Tools {
		names[tool.Name] = true
		if tool.AgentID == "" {
			t.Error("tool missing agentId")
		}
	}
	if !names["code-review"] {
		t.Error("expected code-review tool")
	}
	if !names["lint-check"] {
		t.Error("expected lint-check tool")
	}
}

func TestMCPToolsListNoAuth(t *testing.T) {
	mcp, _, _, _ := setupMCPServer()

	req := httptest.NewRequest("POST", "/mcp/tools/list", nil)
	// No auth header.
	rec := serveMCP(mcp, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestMCPToolsListEmptyTools(t *testing.T) {
	auth := NewDefaultAuthProvider()
	_ = auth.AddAPIKey("key", domain.Identity{Subject: "u", Team: "t"})

	reg := &mcpMockRegistry{agents: []*domain.AgentEntry{}}
	mcp := NewMCPServer(MCPServerConfig{}, auth, reg, &mcpMockScheduler{}, &mcpMockPolicy{})

	req := httptest.NewRequest("POST", "/mcp/tools/list", nil)
	req.Header.Set("X-API-Key", "key")
	rec := serveMCP(mcp, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp MCPToolsListResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Tools) != 0 {
		t.Fatalf("expected 0 tools, got %d", len(resp.Tools))
	}
}

func TestMCPToolsCallSuccess(t *testing.T) {
	mcp, _, _, _ := setupMCPServer()

	req := mcpRequest("POST", "/mcp/tools/call", MCPToolCallRequest{
		Name:      "code-review",
		Arguments: map[string]interface{}{"file": "main.go"},
	})
	rec := serveMCP(mcp, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MCPToolCallResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid response JSON: %v", err)
	}

	if resp.IsError {
		t.Error("expected isError=false")
	}
	if len(resp.Content) == 0 {
		t.Fatal("expected at least one content block")
	}
	if resp.Content[0].Type != "text" {
		t.Errorf("expected content type text, got %s", resp.Content[0].Type)
	}
}

func TestMCPToolsCallNoAuth(t *testing.T) {
	mcp, _, _, _ := setupMCPServer()

	body, _ := json.Marshal(MCPToolCallRequest{Name: "code-review"})
	req := httptest.NewRequest("POST", "/mcp/tools/call", bytes.NewReader(body))
	rec := serveMCP(mcp, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestMCPToolsCallMissingName(t *testing.T) {
	mcp, _, _, _ := setupMCPServer()

	req := mcpRequest("POST", "/mcp/tools/call", MCPToolCallRequest{
		Name: "",
	})
	rec := serveMCP(mcp, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMCPToolsCallInvalidJSON(t *testing.T) {
	mcp, _, _, _ := setupMCPServer()

	req := httptest.NewRequest("POST", "/mcp/tools/call", bytes.NewBufferString("{bad json"))
	req.Header.Set("X-API-Key", "mcp-test-key")
	rec := serveMCP(mcp, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestMCPToolsCallPolicyDenied(t *testing.T) {
	mcp, _, pol, _ := setupMCPServer()
	pol.deny = true
	pol.denyReason = "budget exceeded"

	req := mcpRequest("POST", "/mcp/tools/call", MCPToolCallRequest{
		Name: "code-review",
	})
	rec := serveMCP(mcp, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MCPErrorResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Error.Code != -32003 {
		t.Errorf("expected error code -32003, got %d", resp.Error.Code)
	}
}

func TestMCPToolsCallPolicyError(t *testing.T) {
	mcp, _, pol, _ := setupMCPServer()
	pol.evalErr = true

	req := mcpRequest("POST", "/mcp/tools/call", MCPToolCallRequest{
		Name: "code-review",
	})
	rec := serveMCP(mcp, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on policy error, got %d", rec.Code)
	}
}

func TestMCPToolsCallSchedulerFailure(t *testing.T) {
	mcp, _, _, sched := setupMCPServer()
	sched.fail = true
	sched.failMsg = "no matching agent for capability"

	req := mcpRequest("POST", "/mcp/tools/call", MCPToolCallRequest{
		Name:      "unknown-tool",
		Arguments: map[string]interface{}{},
	})
	rec := serveMCP(mcp, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMCPToolsCallWithBearerAuth(t *testing.T) {
	auth := NewDefaultAuthProvider()
	auth.OAuth2TokenValidator = func(ctx context.Context, token string) (*domain.Identity, error) {
		if token == "mcp-bearer-token" {
			return &domain.Identity{Subject: "oauth-mcp", Team: "eng"}, nil
		}
		return nil, fmt.Errorf("invalid token")
	}

	reg := &mcpMockRegistry{agents: []*domain.AgentEntry{}}
	mcp := NewMCPServer(MCPServerConfig{}, auth, reg, &mcpMockScheduler{}, &mcpMockPolicy{})

	body, _ := json.Marshal(MCPToolCallRequest{Name: "test-tool"})
	req := httptest.NewRequest("POST", "/mcp/tools/call", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer mcp-bearer-token")
	rec := serveMCP(mcp, req)

	// Should authenticate successfully and proceed (scheduler mock returns success).
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with Bearer auth, got %d: %s", rec.Code, rec.Body.String())
	}
}
