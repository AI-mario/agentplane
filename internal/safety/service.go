package safety

import (
	"context"

	"github.com/agentplane/agentplane/internal/domain"
)

// SafetyMeshService provides fleet-wide circuit breakers, kill switch, and safety controls.
type SafetyMeshService interface {
	// ActivateKillSwitch stops all fleet activity.
	ActivateKillSwitch(ctx context.Context) error
	// DeactivateKillSwitch resumes normal operations.
	DeactivateKillSwitch(ctx context.Context) error
	// GetCircuitBreaker returns circuit breaker state for an agent.
	GetCircuitBreaker(ctx context.Context, agentID string) (*domain.CircuitBreakerState, error)
	// ProbeAgent sends a test mission to a circuit-broken agent.
	ProbeAgent(ctx context.Context, agentID string) (*domain.ProbeResult, error)
}
