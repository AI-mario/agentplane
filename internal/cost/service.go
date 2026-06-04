package cost

import (
	"context"

	"github.com/agentplane/agentplane/internal/domain"
)

// CostControllerService meters usage, attributes costs, and enforces budgets.
type CostControllerService interface {
	// RecordUsage records a cost event for a mission.
	RecordUsage(ctx context.Context, event *domain.CostEvent) error
	// GetBudgetStatus returns current budget utilization for a team.
	GetBudgetStatus(ctx context.Context, teamID string) (*domain.BudgetStatus, error)
	// QueryCosts returns cost records matching filters.
	QueryCosts(ctx context.Context, filter domain.CostFilter) (*domain.CostReport, error)
	// CheckBudget returns whether a team can accept new missions.
	CheckBudget(ctx context.Context, teamID string, estimatedCost float64) (bool, error)
}
