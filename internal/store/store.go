package store

import "context"

// Store is the top-level storage interface. All business logic depends only on this.
type Store interface {
	Agents() AgentStore
	Missions() MissionStore
	Policies() PolicyStore
	Costs() CostStore
	Traces() TraceStore
	Budgets() BudgetStore
	Messages() MessageStore
	Migrate(ctx context.Context) error
	Close() error
}
