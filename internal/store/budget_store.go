package store

import (
	"context"

	"github.com/agentplane/agentplane/internal/domain"
)

// BudgetStore defines persistence operations for team budgets.
type BudgetStore interface {
	Get(ctx context.Context, teamID string) (*domain.Budget, error)
	Set(ctx context.Context, budget *domain.Budget) error
	ListAll(ctx context.Context) ([]*domain.Budget, error)
	IncrementAccumulated(ctx context.Context, teamID string, amount float64) error
	ResetPeriod(ctx context.Context, teamID string) error
}
