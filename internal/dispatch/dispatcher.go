// Package dispatch routes mission execution to the appropriate runtime adapter
// based on the agent's declared runtime type.
package dispatch

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/agentplane/agentplane/internal/adapters/gemini"
	"github.com/agentplane/agentplane/internal/domain"
)

// RuntimeAdapter executes a mission against a specific runtime.
type RuntimeAdapter interface {
	// Execute dispatches a mission payload and returns the result.
	Execute(ctx context.Context, agent *domain.AgentEntry, mission *domain.Mission) (*ExecutionResult, error)
	// HealthCheck verifies the adapter is operational.
	HealthCheck(ctx context.Context) error
	// IsHealthy returns the last known health status.
	IsHealthy() bool
}

// ExecutionResult holds the outcome of a mission execution.
type ExecutionResult struct {
	Response    string
	TokensUsed  int64
	ComputeMs   int64
	ToolCalls   int
	Success     bool
	Error       string
}

// CostRecorder is called after execution to record usage.
type CostRecorder interface {
	RecordUsage(ctx context.Context, event *domain.CostEvent) error
}

// Config holds dispatcher configuration.
type Config struct {
	// Gemini adapter config (nil = gemini disabled)
	Gemini *gemini.Config
	// Default timeout for mission execution
	DefaultTimeout time.Duration
}

// Dispatcher routes missions to runtime-specific adapters.
type Dispatcher struct {
	mu           sync.RWMutex
	adapters     map[domain.RuntimeType]RuntimeAdapter
	costRecorder CostRecorder
	config       Config
}

// New creates a dispatcher with configured runtime adapters.
func New(cfg Config, costRecorder CostRecorder) *Dispatcher {
	d := &Dispatcher{
		adapters:     make(map[domain.RuntimeType]RuntimeAdapter),
		costRecorder: costRecorder,
		config:       cfg,
	}

	// Wire Gemini adapter if configured.
	if cfg.Gemini != nil {
		geminiAdapter := &geminiRuntimeAdapter{
			adapter: gemini.New(*cfg.Gemini),
		}
		d.adapters[domain.RuntimeGemini] = geminiAdapter
		log.Printf("dispatch: gemini adapter enabled (model=%s)", cfg.Gemini.Model)
	}

	// Custom/Claude/Kiro/Bedrock use A2A delivery (handled by communication bus).
	// They don't need a built-in adapter — missions are delivered via A2A protocol.

	return d
}

// Dispatch executes a mission on the appropriate runtime adapter.
// Returns nil if the runtime uses A2A delivery (handled externally).
func (d *Dispatcher) Dispatch(ctx context.Context, agent *domain.AgentEntry, mission *domain.Mission) (*ExecutionResult, error) {
	d.mu.RLock()
	adapter, hasAdapter := d.adapters[agent.RuntimeType]
	d.mu.RUnlock()

	if !hasAdapter {
		// No built-in adapter — mission delivered via A2A (communication bus).
		return nil, nil
	}

	// Execute via adapter.
	start := time.Now()
	result, err := adapter.Execute(ctx, agent, mission)
	elapsed := time.Since(start)

	if err != nil {
		return nil, fmt.Errorf("dispatch to %s agent %s failed: %w", agent.RuntimeType, agent.ID, err)
	}

	// Record cost.
	if d.costRecorder != nil && result != nil {
		costEvent := &domain.CostEvent{
			MissionID:       mission.ID,
			AgentID:         agent.ID,
			TeamID:          mission.TeamID,
			ProjectID:       mission.ProjectID,
			TokenCount:      result.TokensUsed,
			ComputeTimeMs:   elapsed.Milliseconds(),
			ToolInvocations: result.ToolCalls,
			TotalCost:       estimateCost(agent.RuntimeType, result.TokensUsed),
			Timestamp:       time.Now(),
		}
		if err := d.costRecorder.RecordUsage(ctx, costEvent); err != nil {
			log.Printf("dispatch: failed to record cost for mission %s: %v", mission.ID, err)
		}
	}

	return result, nil
}

// HasBuiltInAdapter returns true if the runtime has a built-in adapter
// (vs. A2A delivery).
func (d *Dispatcher) HasBuiltInAdapter(rt domain.RuntimeType) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	_, ok := d.adapters[rt]
	return ok
}

// HealthCheck checks all registered adapters.
func (d *Dispatcher) HealthCheck(ctx context.Context) map[domain.RuntimeType]error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	results := make(map[domain.RuntimeType]error)
	for rt, adapter := range d.adapters {
		results[rt] = adapter.HealthCheck(ctx)
	}
	return results
}

// --- Gemini Runtime Adapter ---

type geminiRuntimeAdapter struct {
	adapter *gemini.Adapter
}

func (g *geminiRuntimeAdapter) Execute(ctx context.Context, agent *domain.AgentEntry, mission *domain.Mission) (*ExecutionResult, error) {
	// Build mission payload for Gemini.
	payload := gemini.MissionPayload{
		Goal:      extractGoal(mission),
		Context:   extractContext(mission, agent),
		MaxTokens: 4096,
	}

	// Map agent capabilities to Gemini function declarations.
	for _, cap := range agent.Capabilities {
		if cap.Type == "mcp-tool" {
			payload.Tools = append(payload.Tools, gemini.FunctionDeclaration{
				Name:        cap.Name,
				Description: fmt.Sprintf("Tool: %s", cap.Name),
			})
		}
	}

	result, err := g.adapter.Execute(ctx, payload)
	if err != nil {
		return &ExecutionResult{
			Success: false,
			Error:   err.Error(),
		}, err
	}

	return &ExecutionResult{
		Response:   result.Response,
		TokensUsed: int64(result.TokensUsed.TotalTokenCount),
		ComputeMs:  result.LatencyMs,
		ToolCalls:  len(result.FunctionCalls),
		Success:    result.FinishReason == "STOP" || result.FinishReason == "MAX_TOKENS",
	}, nil
}

func (g *geminiRuntimeAdapter) HealthCheck(ctx context.Context) error {
	return g.adapter.HealthCheck(ctx)
}

func (g *geminiRuntimeAdapter) IsHealthy() bool {
	return g.adapter.IsHealthy()
}

// --- Helpers ---

// extractGoal pulls the goal from mission payload.
func extractGoal(mission *domain.Mission) string {
	if mission.Payload == nil {
		return fmt.Sprintf("Mission %s", mission.ID)
	}
	// Payload is []byte JSON — try to extract "goal" field.
	// Simple approach: use as string if not JSON.
	return string(mission.Payload)
}

// extractContext builds context map for the Gemini call.
func extractContext(mission *domain.Mission, agent *domain.AgentEntry) map[string]string {
	ctx := map[string]string{
		"mission_id": mission.ID,
		"agent_id":   agent.ID,
		"agent_name": agent.Name,
		"team":       mission.TeamID,
	}
	if mission.ProjectID != "" {
		ctx["project"] = mission.ProjectID
	}
	return ctx
}

// estimateCost calculates approximate cost based on runtime and token usage.
func estimateCost(rt domain.RuntimeType, tokens int64) float64 {
	// Approximate pricing per 1K tokens (input+output averaged)
	pricePerKToken := map[domain.RuntimeType]float64{
		domain.RuntimeGemini:  0.00125, // Gemini 2.5 Pro average
		domain.RuntimeClaude:  0.008,   // Claude Sonnet average
		domain.RuntimeBedrock: 0.005,   // Bedrock average
		domain.RuntimeKiro:    0.006,   // Kiro average
		domain.RuntimeCustom:  0.001,   // Custom (low estimate)
	}

	price, ok := pricePerKToken[rt]
	if !ok {
		price = 0.001
	}
	return float64(tokens) / 1000.0 * price
}
