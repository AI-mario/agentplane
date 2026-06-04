package store

import (
	"context"

	"github.com/agentplane/agentplane/internal/domain"
)

// MissionStore defines persistence operations for missions.
type MissionStore interface {
	Create(ctx context.Context, mission *domain.Mission) error
	Get(ctx context.Context, id string) (*domain.Mission, error)
	List(ctx context.Context, filter domain.MissionFilter) ([]*domain.Mission, error)
	Update(ctx context.Context, mission *domain.Mission) error
	GetPending(ctx context.Context) ([]*domain.Mission, error)
}
