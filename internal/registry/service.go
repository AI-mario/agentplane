package registry

import (
	"context"

	"github.com/agentplane/agentplane/internal/domain"
)

// AgentRegistryService manages the catalog of registered agents.
type AgentRegistryService interface {
	// Register validates and stores a new agent entry.
	Register(ctx context.Context, manifest domain.AgentManifest) (*domain.AgentEntry, error)
	// Get retrieves an agent by ID.
	Get(ctx context.Context, id string) (*domain.AgentEntry, error)
	// List returns agents matching filter criteria.
	List(ctx context.Context, filter domain.AgentFilter) ([]*domain.AgentEntry, error)
	// FindByCapability returns agents declaring a specific capability.
	FindByCapability(ctx context.Context, capability string) ([]*domain.AgentEntry, error)
	// Deregister removes an agent from the registry.
	Deregister(ctx context.Context, id string) error
}
