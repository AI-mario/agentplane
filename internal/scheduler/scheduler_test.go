package scheduler

import (
	"testing"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
)

func TestHasAllCapabilities(t *testing.T) {
	agent := &domain.AgentEntry{
		Capabilities: []domain.Capability{
			{Name: "code-review", Type: "mcp-tool"},
			{Name: "test-gen", Type: "mcp-tool"},
			{Name: "deploy", Type: "a2a-message"},
		},
	}

	tests := []struct {
		name     string
		required []string
		want     bool
	}{
		{"empty required", nil, true},
		{"subset match", []string{"code-review"}, true},
		{"exact match", []string{"code-review", "test-gen", "deploy"}, true},
		{"partial mismatch", []string{"code-review", "unknown"}, false},
		{"all missing", []string{"foo", "bar"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasAllCapabilities(agent, tt.required)
			if got != tt.want {
				t.Errorf("hasAllCapabilities(%v) = %v, want %v", tt.required, got, tt.want)
			}
		})
	}
}

func TestValidateWeights(t *testing.T) {
	tests := []struct {
		name    string
		weights domain.ScoringWeights
		wantErr bool
	}{
		{"valid default", domain.ScoringWeights{Cost: 0.4, Latency: 0.3, Load: 0.3}, false},
		{"valid custom", domain.ScoringWeights{Cost: 0.5, Latency: 0.25, Load: 0.25}, false},
		{"sum too low", domain.ScoringWeights{Cost: 0.3, Latency: 0.3, Load: 0.3}, true},
		{"sum too high", domain.ScoringWeights{Cost: 0.5, Latency: 0.5, Load: 0.5}, true},
		{"negative weight", domain.ScoringWeights{Cost: -0.1, Latency: 0.6, Load: 0.5}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWeights(tt.weights)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateWeights() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestAgentCost(t *testing.T) {
	agent := &domain.AgentEntry{
		SLOs: domain.SLODefinition{
			Cost: &domain.CostBound{MaxCost: 0.05},
		},
	}
	if got := agentCost(agent); got != 0.05 {
		t.Errorf("agentCost() = %v, want 0.05", got)
	}

	agentNoCost := &domain.AgentEntry{SLOs: domain.SLODefinition{}}
	if got := agentCost(agentNoCost); got != 0 {
		t.Errorf("agentCost() = %v, want 0", got)
	}
}

func TestAgentLatency(t *testing.T) {
	agent := &domain.AgentEntry{
		SLOs: domain.SLODefinition{
			Latency: &domain.LatencyBound{MaxMs: 5000},
		},
	}
	if got := agentLatency(agent); got != 5000 {
		t.Errorf("agentLatency() = %v, want 5000", got)
	}

	agentNoLatency := &domain.AgentEntry{SLOs: domain.SLODefinition{}}
	if got := agentLatency(agentNoLatency); got != 0 {
		t.Errorf("agentLatency() = %v, want 0", got)
	}
}

func TestChannelEmitter(t *testing.T) {
	emitter, ch := NewChannelEmitter(10)
	event := domain.SystemEvent{
		Type:      "test_event",
		Severity:  "info",
		Source:    "test",
		Timestamp: time.Now(),
	}
	emitter.Emit(event)

	select {
	case received := <-ch:
		if received.Type != "test_event" {
			t.Errorf("received event type = %v, want test_event", received.Type)
		}
	case <-time.After(time.Second):
		t.Error("timeout waiting for event")
	}
}

func TestScoringTieBreak(t *testing.T) {
	// Verify sort order: highest score > lowest load > earliest CreatedAt.
	now := time.Now()
	scored := []scoredCandidate{
		{
			agent:       &domain.AgentEntry{ID: "a1", CreatedAt: now.Add(-1 * time.Hour)},
			totalScore:  0.8,
			currentLoad: 5,
		},
		{
			agent:       &domain.AgentEntry{ID: "a2", CreatedAt: now.Add(-2 * time.Hour)},
			totalScore:  0.8,
			currentLoad: 3,
		},
		{
			agent:       &domain.AgentEntry{ID: "a3", CreatedAt: now.Add(-3 * time.Hour)},
			totalScore:  0.8,
			currentLoad: 3,
		},
		{
			agent:       &domain.AgentEntry{ID: "a4", CreatedAt: now},
			totalScore:  0.9,
			currentLoad: 10,
		},
	}

	// Sort using same logic as Schedule.
	sortCandidates(scored)

	// a4 has highest score, then a3 (lowest load + earliest), a2, a1.
	expected := []string{"a4", "a3", "a2", "a1"}
	for i, want := range expected {
		if scored[i].agent.ID != want {
			t.Errorf("position %d: got %s, want %s", i, scored[i].agent.ID, want)
		}
	}
}

// sortCandidates applies the scheduler's sort logic for testing.
func sortCandidates(scored []scoredCandidate) {
	for i := 0; i < len(scored)-1; i++ {
		for j := i + 1; j < len(scored); j++ {
			swap := false
			if scored[j].totalScore > scored[i].totalScore {
				swap = true
			} else if scored[j].totalScore == scored[i].totalScore {
				if scored[j].currentLoad < scored[i].currentLoad {
					swap = true
				} else if scored[j].currentLoad == scored[i].currentLoad {
					if scored[j].agent.CreatedAt.Before(scored[i].agent.CreatedAt) {
						swap = true
					}
				}
			}
			if swap {
				scored[i], scored[j] = scored[j], scored[i]
			}
		}
	}
}
