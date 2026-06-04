// Package gateway implements protocol adapters for the AgentPlane control plane.
// This file implements an MCP-compatible HTTP server on :8081.
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/policy"
	"github.com/agentplane/agentplane/internal/registry"
	"github.com/agentplane/agentplane/internal/scheduler"
)

// MCPTool represents a tool exposed via the MCP protocol.
type MCPTool struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	AgentID     string            `json:"agentId"`
	InputSchema map[string]interface{} `json:"inputSchema,omitempty"`
}

// MCPToolsListResponse is returned from POST /mcp/tools/list.
type MCPToolsListResponse struct {
	Tools []MCPTool `json:"tools"`
}

// MCPToolCallRequest is the payload for POST /mcp/tools/call.
type MCPToolCallRequest struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

// MCPToolCallResponse is the result of a tool call.
type MCPToolCallResponse struct {
	Content []MCPContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

// MCPContent represents a content block in an MCP response.
type MCPContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// MCPErrorResponse is returned on protocol errors.
type MCPErrorResponse struct {
	Error MCPErrorDetail `json:"error"`
}

// MCPErrorDetail contains error code and message.
type MCPErrorDetail struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// MCPServerConfig holds configuration for the MCP server.
type MCPServerConfig struct {
	Addr string // listen address, default ":8081"
}

// MCPServer is the MCP-compatible HTTP server.
type MCPServer struct {
	config    MCPServerConfig
	server    *http.Server
	registry  registry.AgentRegistryService
	scheduler scheduler.SchedulerService
	policy    policy.PolicyEngineService
	auth      AuthProvider
}

// NewMCPServer creates a new MCP server with all dependencies.
func NewMCPServer(
	config MCPServerConfig,
	auth AuthProvider,
	reg registry.AgentRegistryService,
	sched scheduler.SchedulerService,
	pol policy.PolicyEngineService,
) *MCPServer {
	if config.Addr == "" {
		config.Addr = ":8081"
	}

	mcp := &MCPServer{
		config:    config,
		registry:  reg,
		scheduler: sched,
		policy:    pol,
		auth:      auth,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp/tools/list", mcp.handleToolsList)
	mux.HandleFunc("POST /mcp/tools/call", mcp.handleToolsCall)

	mcp.server = &http.Server{
		Addr:         config.Addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return mcp
}

// Start begins serving MCP requests.
func (m *MCPServer) Start() error {
	return m.server.ListenAndServe()
}

// Shutdown gracefully stops the server.
func (m *MCPServer) Shutdown(ctx context.Context) error {
	return m.server.Shutdown(ctx)
}

// handleToolsList aggregates capabilities from all registered agents and returns
// them as MCP-compatible tools.
func (m *MCPServer) handleToolsList(w http.ResponseWriter, r *http.Request) {
	identity, err := m.authenticate(r)
	if err != nil {
		writeMCPError(w, http.StatusUnauthorized, -32001, "authentication failed")
		return
	}
	_ = identity

	// List all active agents from registry.
	agents, err := m.registry.List(r.Context(), domain.AgentFilter{
		Status: domain.AgentStatusActive,
	})
	if err != nil {
		writeMCPError(w, http.StatusInternalServerError, -32603, "failed to list agents")
		return
	}

	var tools []MCPTool
	for _, agent := range agents {
		for _, cap := range agent.Capabilities {
			if cap.Type == "mcp-tool" {
				tools = append(tools, MCPTool{
					Name:    cap.Name,
					AgentID: agent.ID,
					Description: fmt.Sprintf("Tool provided by agent %s (v%s)",
						agent.Name, agent.Version),
				})
			}
		}
	}

	if tools == nil {
		tools = []MCPTool{}
	}

	writeMCPJSON(w, http.StatusOK, MCPToolsListResponse{Tools: tools})
}

// handleToolsCall routes a tool invocation through policy → scheduler → dispatch.
func (m *MCPServer) handleToolsCall(w http.ResponseWriter, r *http.Request) {
	identity, err := m.authenticate(r)
	if err != nil {
		writeMCPError(w, http.StatusUnauthorized, -32001, "authentication failed")
		return
	}

	var req MCPToolCallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeMCPError(w, http.StatusBadRequest, -32700, "invalid JSON request")
		return
	}

	if req.Name == "" {
		writeMCPError(w, http.StatusBadRequest, -32602, "missing required field: name")
		return
	}

	// Policy check: validate tool call is allowed.
	policyReq := domain.PolicyRequest{
		Action:   "tools.call",
		Subject:  identity,
		Resource: req.Name,
		Context: map[string]interface{}{
			"tool":      req.Name,
			"arguments": req.Arguments,
		},
	}

	decision, err := m.policy.Evaluate(r.Context(), policyReq)
	if err != nil {
		log.Printf("MCP policy evaluation error: tool=%s err=%v", req.Name, err)
		writeMCPError(w, http.StatusForbidden, -32003, "policy evaluation failed: access denied")
		return
	}
	if !decision.Allowed {
		writeMCPError(w, http.StatusForbidden, -32003,
			fmt.Sprintf("policy denied: %s", decision.Reason))
		return
	}

	// Route to scheduler: create a mission for this tool call.
	payload, _ := json.Marshal(map[string]interface{}{
		"tool":      req.Name,
		"arguments": req.Arguments,
	})

	mission := &domain.Mission{
		RequiredCapabilities: []string{req.Name},
		Priority:             1,
		TeamID:               identity.Team,
		Payload:              payload,
		Timeout:              30 * time.Second,
		SubmittedAt:          time.Now(),
	}

	assignment, err := m.scheduler.Schedule(r.Context(), mission)
	if err != nil {
		writeMCPError(w, http.StatusServiceUnavailable, -32002,
			fmt.Sprintf("scheduling failed: %s", err.Error()))
		return
	}

	// Return successful dispatch result.
	resultText := fmt.Sprintf("Tool %q dispatched to agent %s (mission=%s, score=%.3f)",
		req.Name, assignment.AgentID, assignment.MissionID, assignment.Score)

	writeMCPJSON(w, http.StatusOK, MCPToolCallResponse{
		Content: []MCPContent{
			{Type: "text", Text: resultText},
		},
	})
}

// authenticate extracts and validates credentials from the request.
func (m *MCPServer) authenticate(r *http.Request) (*domain.Identity, error) {
	// Check Authorization header.
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
			return m.auth.ValidateOAuth2Token(r.Context(), authHeader[7:])
		}
		if len(authHeader) > 7 && authHeader[:7] == "ApiKey " {
			return m.auth.ValidateAPIKey(r.Context(), authHeader[7:])
		}
	}

	// Check X-API-Key header.
	if apiKey := r.Header.Get("X-API-Key"); apiKey != "" {
		return m.auth.ValidateAPIKey(r.Context(), apiKey)
	}

	return nil, fmt.Errorf("missing authentication credentials")
}

// writeMCPJSON writes a JSON response.
func writeMCPJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("MCP: failed to write response: %v", err)
	}
}

// writeMCPError writes an MCP error response.
func writeMCPError(w http.ResponseWriter, httpStatus int, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	if err := json.NewEncoder(w).Encode(MCPErrorResponse{
		Error: MCPErrorDetail{Code: code, Message: message},
	}); err != nil {
		log.Printf("MCP: failed to write error response: %v", err)
	}
}
