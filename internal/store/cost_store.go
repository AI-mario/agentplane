package store

import (
	"context"

	"github.com/agentplane/agentplane/internal/domain"
)

// CostStore defines persistence operations for cost records.
type CostStore interface {
	Record(ctx context.Context, record *domain.CostRecord) error
	Query(ctx context.Context, filter domain.CostFilter) ([]*domain.CostRecord, int, error)
	Aggregate(ctx context.Context, filter domain.CostFilter) (*domain.CostAggregation, error)
}
