package slo

import (
	"context"

	"github.com/agentplane/agentplane/internal/domain"
)

// SLOManagerService monitors agent performance against declared SLO contracts.
type SLOManagerService interface {
	// EvaluateSLO checks current metrics against SLO definitions.
	EvaluateSLO(ctx context.Context, agentID string) (*domain.SLOStatus, error)
	// GetCompliance returns SLO compliance percentage over a window.
	GetCompliance(ctx context.Context, agentID string) (*domain.SLOCompliance, error)
}
