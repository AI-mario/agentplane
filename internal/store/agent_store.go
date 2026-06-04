package store

import (
	"context"

	"github.com/agentplane/agentplane/internal/domain"
)

// AgentStore defines persistence operations for agents and their versions.
type AgentStore interface {
	Create(ctx context.Context, agent *domain.AgentEntry) error
	Get(ctx context.Context, id string) (*domain.AgentEntry, error)
	GetByName(ctx context.Context, name, namespace string) (*domain.AgentEntry, error)
	List(ctx context.Context, filter domain.AgentFilter) ([]*domain.AgentEntry, error)
	Update(ctx context.Context, agent *domain.AgentEntry) error
	Delete(ctx context.Context, id string) error
	QueryByCapability(ctx context.Context, capability string) ([]*domain.AgentEntry, error)
	AddVersion(ctx context.Context, version *domain.AgentVersion) error
	ListVersions(ctx context.Context, agentID string) ([]*domain.AgentVersion, error)
	DeleteVersion(ctx context.Context, versionID string) error
}
