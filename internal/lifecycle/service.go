package lifecycle

import (
	"context"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
)

// LifecycleManagerService handles deployments, scaling, canary, and rollback operations.
type LifecycleManagerService interface {
	// Deploy initiates deployment of a new agent version.
	Deploy(ctx context.Context, manifest domain.AgentManifest) (*domain.Deployment, error)
	// Rollback reverts to a previous agent version.
	Rollback(ctx context.Context, agentID string) error
	// Scale adjusts instance count for an agent.
	Scale(ctx context.Context, agentID string, replicas int) error
	// Deprecate marks an agent version for drain and removal.
	Deprecate(ctx context.Context, agentID string, drainTimeout time.Duration) error
}
