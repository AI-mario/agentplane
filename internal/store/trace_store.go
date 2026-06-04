package store

import (
	"context"

	"github.com/agentplane/agentplane/internal/domain"
)

// TraceStore defines persistence operations for mission traces and spans.
type TraceStore interface {
	Create(ctx context.Context, trace *domain.MissionTrace) error
	Update(ctx context.Context, trace *domain.MissionTrace) error
	AddSpan(ctx context.Context, span *domain.TraceSpan) error
	Get(ctx context.Context, id string) (*domain.MissionTrace, error)
	Query(ctx context.Context, filter domain.TraceFilter) ([]*domain.MissionTrace, error)
	DeleteExpired(ctx context.Context) (int, error)
}
