package policy

import (
	"context"

	"github.com/agentplane/agentplane/internal/domain"
)

// PolicyEngineService evaluates allow/deny decisions for missions and agent actions.
type PolicyEngineService interface {
	// Evaluate checks a request against applicable policies.
	Evaluate(ctx context.Context, req domain.PolicyRequest) (*domain.PolicyDecision, error)
	// ApplyPolicy creates or updates a policy.
	ApplyPolicy(ctx context.Context, policy *domain.Policy) error
	// GetPolicy retrieves a policy by ID.
	GetPolicy(ctx context.Context, id string) (*domain.Policy, error)
}
