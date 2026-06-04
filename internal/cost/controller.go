package cost

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/store"
)

// EventEmitter emits system events.
type EventEmitter interface {
	Emit(event domain.SystemEvent)
}

// channelEmitter is a simple EventEmitter that sends events to a channel.
type channelEmitter struct {
	ch chan domain.SystemEvent
}

func (e *channelEmitter) Emit(event domain.SystemEvent) {
	select {
	case e.ch <- event:
	default:
		// Drop event if channel full — non-blocking.
	}
}

// NewChannelEmitter creates an EventEmitter backed by a buffered channel.
func NewChannelEmitter(bufSize int) (EventEmitter, <-chan domain.SystemEvent) {
	ch := make(chan domain.SystemEvent, bufSize)
	return &channelEmitter{ch: ch}, ch
}

// Config holds configurable parameters for the CostController.
type Config struct {
	// BudgetPeriod defines the budget period duration (default: calendar month).
	BudgetPeriod string // "monthly" | "weekly"
	// WarningThreshold is the percentage of budget at which a warning fires (default 0.80).
	WarningThreshold float64
	// BlockThreshold is the percentage of budget at which new missions are blocked (default 1.0).
	BlockThreshold float64
}

// DefaultConfig returns default CostController configuration.
func DefaultConfig() Config {
	return Config{
		BudgetPeriod:     "monthly",
		WarningThreshold: 0.80,
		BlockThreshold:   1.0,
	}
}

// Controller implements CostControllerService.
type Controller struct {
	store   store.Store
	emitter EventEmitter
	config  Config
}

// New creates a new Controller.
func New(s store.Store, emitter EventEmitter, config Config) *Controller {
	if config.WarningThreshold <= 0 {
		config.WarningThreshold = 0.80
	}
	if config.BlockThreshold <= 0 {
		config.BlockThreshold = 1.0
	}
	if config.BudgetPeriod == "" {
		config.BudgetPeriod = "monthly"
	}
	return &Controller{
		store:   s,
		emitter: emitter,
		config:  config,
	}
}

// RecordUsage records a cost event for a mission.
// It persists the cost record, attributes to team/project/mission,
// updates budget aggregations, and emits budget_warning or blocks at threshold.
func (c *Controller) RecordUsage(ctx context.Context, event *domain.CostEvent) error {
	if event == nil {
		return fmt.Errorf("cost event must not be nil")
	}

	// Generate UUID if not set.
	if event.ID == "" {
		event.ID = uuid.New().String()
	}

	// Set timestamp if not provided.
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	// Check attribution — emit cost_attribution_failure if team/project unknown.
	if event.TeamID == "" || event.ProjectID == "" {
		if event.TeamID == "" {
			event.TeamID = "unattributed"
		}
		if event.ProjectID == "" {
			event.ProjectID = "unattributed"
		}
		c.emitter.Emit(domain.SystemEvent{
			Type:      "cost_attribution_failure",
			Severity:  "warning",
			Source:    "cost_controller",
			Timestamp: time.Now(),
			Payload: map[string]interface{}{
				"mission_id": event.MissionID,
				"agent_id":   event.AgentID,
				"team_id":    event.TeamID,
				"project_id": event.ProjectID,
			},
		})
	}

	// Persist cost record.
	record := &domain.CostRecord{
		ID:              event.ID,
		MissionID:       event.MissionID,
		AgentID:         event.AgentID,
		TeamID:          event.TeamID,
		ProjectID:       event.ProjectID,
		TokenCount:      event.TokenCount,
		ComputeTimeMs:   event.ComputeTimeMs,
		ToolInvocations: event.ToolInvocations,
		TotalCost:       event.TotalCost,
		RecordedAt:      event.Timestamp,
	}

	if err := c.store.Costs().Record(ctx, record); err != nil {
		return fmt.Errorf("persisting cost record: %w", err)
	}

	// Update budget aggregations (within 5s requirement — done synchronously).
	if event.TeamID != "unattributed" {
		if err := c.updateBudgetAggregation(ctx, event.TeamID, event.TotalCost); err != nil {
			return fmt.Errorf("updating budget aggregation: %w", err)
		}
	}

	return nil
}

// GetBudgetStatus returns current budget utilization for a team.
func (c *Controller) GetBudgetStatus(ctx context.Context, teamID string) (*domain.BudgetStatus, error) {
	if teamID == "" {
		return nil, fmt.Errorf("team ID must not be empty")
	}

	budget, err := c.store.Budgets().Get(ctx, teamID)
	if err != nil {
		return nil, fmt.Errorf("fetching budget for team %s: %w", teamID, err)
	}
	if budget == nil {
		return nil, fmt.Errorf("no budget configured for team %s", teamID)
	}

	// Check if period has rolled over and reset if needed.
	if err := c.maybeResetPeriod(ctx, budget); err != nil {
		return nil, fmt.Errorf("checking budget period: %w", err)
	}

	// Re-fetch after potential reset.
	budget, err = c.store.Budgets().Get(ctx, teamID)
	if err != nil {
		return nil, fmt.Errorf("re-fetching budget for team %s: %w", teamID, err)
	}

	utilization := 0.0
	if budget.CapAmount > 0 {
		utilization = budget.AccumulatedCost / budget.CapAmount
	}

	periodEnd := c.computePeriodEnd(budget.PeriodStart, budget.PeriodType)

	return &domain.BudgetStatus{
		TeamID:          budget.TeamID,
		BudgetCap:       budget.CapAmount,
		AccumulatedCost: budget.AccumulatedCost,
		UtilizationPct:  utilization,
		PeriodStart:     budget.PeriodStart,
		PeriodEnd:       periodEnd,
		IsBlocked:       budget.IsBlocked,
	}, nil
}

// QueryCosts returns cost records matching filters, max 1000 records.
func (c *Controller) QueryCosts(ctx context.Context, filter domain.CostFilter) (*domain.CostReport, error) {
	// Enforce max 1000 records.
	if filter.Limit <= 0 || filter.Limit > 1000 {
		filter.Limit = 1000
	}

	records, total, err := c.store.Costs().Query(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("querying costs: %w", err)
	}

	return &domain.CostReport{
		Records: records,
		Total:   total,
	}, nil
}

// CheckBudget returns whether a team can accept new missions given estimated cost.
// Returns true if the team is within budget, false if blocked.
func (c *Controller) CheckBudget(ctx context.Context, teamID string, estimatedCost float64) (bool, error) {
	if teamID == "" {
		return false, fmt.Errorf("team ID must not be empty")
	}

	budget, err := c.store.Budgets().Get(ctx, teamID)
	if err != nil {
		return false, fmt.Errorf("fetching budget for team %s: %w", teamID, err)
	}

	// No budget configured — allow by default.
	if budget == nil {
		return true, nil
	}

	// Check if period has rolled over.
	if err := c.maybeResetPeriod(ctx, budget); err != nil {
		return false, fmt.Errorf("checking budget period: %w", err)
	}

	// Re-fetch after potential reset.
	budget, err = c.store.Budgets().Get(ctx, teamID)
	if err != nil {
		return false, fmt.Errorf("re-fetching budget: %w", err)
	}

	// If already blocked at 100%, deny new missions.
	if budget.IsBlocked {
		return false, nil
	}

	// Check if adding estimated cost would exceed cap.
	if budget.CapAmount > 0 && (budget.AccumulatedCost+estimatedCost) >= budget.CapAmount {
		return false, nil
	}

	return true, nil
}

// updateBudgetAggregation increments accumulated cost and checks thresholds.
func (c *Controller) updateBudgetAggregation(ctx context.Context, teamID string, amount float64) error {
	budget, err := c.store.Budgets().Get(ctx, teamID)
	if err != nil {
		return err
	}

	// No budget configured for team — nothing to enforce.
	if budget == nil {
		return nil
	}

	// Check period reset before incrementing.
	if err := c.maybeResetPeriod(ctx, budget); err != nil {
		return err
	}

	// Increment accumulated cost.
	if err := c.store.Budgets().IncrementAccumulated(ctx, teamID, amount); err != nil {
		return err
	}

	// Re-fetch to check new accumulated value.
	budget, err = c.store.Budgets().Get(ctx, teamID)
	if err != nil {
		return err
	}

	if budget.CapAmount <= 0 {
		return nil
	}

	utilization := budget.AccumulatedCost / budget.CapAmount

	// Emit budget_warning at 80% threshold.
	if utilization >= c.config.WarningThreshold && utilization < c.config.BlockThreshold {
		c.emitter.Emit(domain.SystemEvent{
			Type:      "budget_warning",
			Severity:  "warning",
			Source:    "cost_controller",
			Timestamp: time.Now(),
			Payload: map[string]interface{}{
				"team_id":        teamID,
				"utilization_pct": utilization,
				"accumulated":    budget.AccumulatedCost,
				"cap":            budget.CapAmount,
			},
		})
	}

	// Block new missions at 100% threshold.
	if utilization >= c.config.BlockThreshold && !budget.IsBlocked {
		budget.IsBlocked = true
		if err := c.store.Budgets().Set(ctx, budget); err != nil {
			return err
		}
		c.emitter.Emit(domain.SystemEvent{
			Type:      "budget_exceeded",
			Severity:  "critical",
			Source:    "cost_controller",
			Timestamp: time.Now(),
			Payload: map[string]interface{}{
				"team_id":     teamID,
				"accumulated": budget.AccumulatedCost,
				"cap":         budget.CapAmount,
			},
		})
	}

	return nil
}

// maybeResetPeriod checks if the budget period has elapsed and resets if so.
func (c *Controller) maybeResetPeriod(ctx context.Context, budget *domain.Budget) error {
	periodEnd := c.computePeriodEnd(budget.PeriodStart, budget.PeriodType)
	if time.Now().Before(periodEnd) {
		return nil
	}

	// Period has elapsed — reset.
	return c.store.Budgets().ResetPeriod(ctx, budget.TeamID)
}

// computePeriodEnd calculates when the current budget period ends.
func (c *Controller) computePeriodEnd(periodStart time.Time, periodType string) time.Time {
	switch periodType {
	case "weekly":
		return periodStart.AddDate(0, 0, 7)
	case "monthly":
		return periodStart.AddDate(0, 1, 0)
	default:
		// Default to calendar month.
		return periodStart.AddDate(0, 1, 0)
	}
}
