package store

import (
	"context"

	"github.com/agentplane/agentplane/internal/domain"
)

// PolicyStore defines persistence operations for policies.
type PolicyStore interface {
	Create(ctx context.Context, policy *domain.Policy) error
	Get(ctx context.Context, id string) (*domain.Policy, error)
	List(ctx context.Context, filter domain.PolicyFilter) ([]*domain.Policy, error)
	Update(ctx context.Context, policy *domain.Policy) error
	Delete(ctx context.Context, id string) error
	GetEffective(ctx context.Context, scope domain.PolicyScope, scopeID string) ([]*domain.Policy, error)
}
