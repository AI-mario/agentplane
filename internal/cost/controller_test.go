package cost

import (
	"context"
	"testing"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/store/sqlite"
)

func setupTestStore(t *testing.T) *sqlite.SQLiteStore {
	t.Helper()
	s, err := sqlite.New("file::memory:?cache=shared&_foreign_keys=on")
	if err != nil {
		t.Fatalf("failed to create sqlite store: %v", err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func setupBudget(t *testing.T, s *sqlite.SQLiteStore, teamID string, cap float64, accumulated float64) {
	t.Helper()
	budget := &domain.Budget{
		ID:              "budget-" + teamID,
		TeamID:          teamID,
		CapAmount:       cap,
		PeriodType:      "monthly",
		PeriodStart:     time.Now().AddDate(0, 0, -1), // started yesterday
		AccumulatedCost: accumulated,
		IsBlocked:       false,
	}
	if err := s.Budgets().Set(context.Background(), budget); err != nil {
		t.Fatalf("failed to set budget: %v", err)
	}
}

func TestRecordUsage_Basic(t *testing.T) {
	s := setupTestStore(t)
	emitter, events := NewChannelEmitter(10)
	ctrl := New(s, emitter, DefaultConfig())

	event := &domain.CostEvent{
		MissionID:       "mission-1",
		AgentID:         "agent-1",
		TeamID:          "team-1",
		ProjectID:       "project-1",
		TokenCount:      1000,
		ComputeTimeMs:   500,
		ToolInvocations: 3,
		TotalCost:       0.05,
	}

	setupBudget(t, s, "team-1", 100.0, 0.0)

	err := ctrl.RecordUsage(context.Background(), event)
	if err != nil {
		t.Fatalf("RecordUsage failed: %v", err)
	}

	// Verify record persisted.
	report, err := ctrl.QueryCosts(context.Background(), domain.CostFilter{TeamID: "team-1"})
	if err != nil {
		t.Fatalf("QueryCosts failed: %v", err)
	}
	if len(report.Records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(report.Records))
	}
	r := report.Records[0]
	if r.TokenCount != 1000 || r.ComputeTimeMs != 500 || r.ToolInvocations != 3 {
		t.Errorf("record fields mismatch: tokens=%d compute=%d tools=%d", r.TokenCount, r.ComputeTimeMs, r.ToolInvocations)
	}
	if r.TotalCost != 0.05 {
		t.Errorf("expected cost 0.05, got %f", r.TotalCost)
	}

	// No events should have been emitted (well below threshold).
	select {
	case ev := <-events:
		t.Errorf("unexpected event: %s", ev.Type)
	default:
	}
}

func TestRecordUsage_AttributionFailure(t *testing.T) {
	s := setupTestStore(t)
	emitter, events := NewChannelEmitter(10)
	ctrl := New(s, emitter, DefaultConfig())

	// No team or project → unattributed.
	event := &domain.CostEvent{
		MissionID:       "mission-1",
		AgentID:         "agent-1",
		TeamID:          "",
		ProjectID:       "",
		TokenCount:      100,
		ComputeTimeMs:   10,
		ToolInvocations: 1,
		TotalCost:       0.01,
	}

	err := ctrl.RecordUsage(context.Background(), event)
	if err != nil {
		t.Fatalf("RecordUsage failed: %v", err)
	}

	// Should emit cost_attribution_failure.
	select {
	case ev := <-events:
		if ev.Type != "cost_attribution_failure" {
			t.Errorf("expected cost_attribution_failure, got %s", ev.Type)
		}
	default:
		t.Error("expected cost_attribution_failure event, got none")
	}

	// Verify stored as unattributed.
	report, err := ctrl.QueryCosts(context.Background(), domain.CostFilter{TeamID: "unattributed"})
	if err != nil {
		t.Fatalf("QueryCosts failed: %v", err)
	}
	if len(report.Records) != 1 {
		t.Fatalf("expected 1 unattributed record, got %d", len(report.Records))
	}
}

func TestRecordUsage_BudgetWarningAt80Percent(t *testing.T) {
	s := setupTestStore(t)
	emitter, events := NewChannelEmitter(10)
	ctrl := New(s, emitter, DefaultConfig())

	setupBudget(t, s, "team-warn", 100.0, 79.0)

	// This pushes accumulated to 80 → warning.
	event := &domain.CostEvent{
		MissionID:       "mission-1",
		AgentID:         "agent-1",
		TeamID:          "team-warn",
		ProjectID:       "project-1",
		TokenCount:      100,
		ComputeTimeMs:   10,
		ToolInvocations: 1,
		TotalCost:       1.0,
	}

	err := ctrl.RecordUsage(context.Background(), event)
	if err != nil {
		t.Fatalf("RecordUsage failed: %v", err)
	}

	// Should emit budget_warning.
	select {
	case ev := <-events:
		if ev.Type != "budget_warning" {
			t.Errorf("expected budget_warning, got %s", ev.Type)
		}
	default:
		t.Error("expected budget_warning event, got none")
	}
}

func TestRecordUsage_BudgetBlockAt100Percent(t *testing.T) {
	s := setupTestStore(t)
	emitter, events := NewChannelEmitter(10)
	ctrl := New(s, emitter, DefaultConfig())

	setupBudget(t, s, "team-block", 100.0, 99.0)

	// This pushes accumulated to 100 → block.
	event := &domain.CostEvent{
		MissionID:       "mission-1",
		AgentID:         "agent-1",
		TeamID:          "team-block",
		ProjectID:       "project-1",
		TokenCount:      100,
		ComputeTimeMs:   10,
		ToolInvocations: 1,
		TotalCost:       1.0,
	}

	err := ctrl.RecordUsage(context.Background(), event)
	if err != nil {
		t.Fatalf("RecordUsage failed: %v", err)
	}

	// Should emit budget_exceeded.
	select {
	case ev := <-events:
		if ev.Type != "budget_exceeded" {
			t.Errorf("expected budget_exceeded, got %s", ev.Type)
		}
	default:
		t.Error("expected budget_exceeded event, got none")
	}

	// CheckBudget should now deny.
	allowed, err := ctrl.CheckBudget(context.Background(), "team-block", 1.0)
	if err != nil {
		t.Fatalf("CheckBudget failed: %v", err)
	}
	if allowed {
		t.Error("expected budget blocked, but got allowed")
	}
}

func TestCheckBudget_NoBudgetConfigured(t *testing.T) {
	s := setupTestStore(t)
	emitter, _ := NewChannelEmitter(10)
	ctrl := New(s, emitter, DefaultConfig())

	// No budget set → allow by default.
	allowed, err := ctrl.CheckBudget(context.Background(), "team-no-budget", 10.0)
	if err != nil {
		t.Fatalf("CheckBudget failed: %v", err)
	}
	if !allowed {
		t.Error("expected allowed when no budget configured")
	}
}

func TestCheckBudget_WithinBudget(t *testing.T) {
	s := setupTestStore(t)
	emitter, _ := NewChannelEmitter(10)
	ctrl := New(s, emitter, DefaultConfig())

	setupBudget(t, s, "team-ok", 100.0, 50.0)

	allowed, err := ctrl.CheckBudget(context.Background(), "team-ok", 10.0)
	if err != nil {
		t.Fatalf("CheckBudget failed: %v", err)
	}
	if !allowed {
		t.Error("expected allowed, team is within budget")
	}
}

func TestCheckBudget_WouldExceed(t *testing.T) {
	s := setupTestStore(t)
	emitter, _ := NewChannelEmitter(10)
	ctrl := New(s, emitter, DefaultConfig())

	setupBudget(t, s, "team-full", 100.0, 95.0)

	// 95 + 10 = 105 >= 100 → deny.
	allowed, err := ctrl.CheckBudget(context.Background(), "team-full", 10.0)
	if err != nil {
		t.Fatalf("CheckBudget failed: %v", err)
	}
	if allowed {
		t.Error("expected denied, cost would exceed budget")
	}
}

func TestQueryCosts_FilterAndLimit(t *testing.T) {
	s := setupTestStore(t)
	emitter, _ := NewChannelEmitter(10)
	ctrl := New(s, emitter, DefaultConfig())

	setupBudget(t, s, "team-q", 10000.0, 0.0)

	// Insert multiple records.
	for i := 0; i < 5; i++ {
		event := &domain.CostEvent{
			MissionID:       "mission-q",
			AgentID:         "agent-q",
			TeamID:          "team-q",
			ProjectID:       "project-q",
			TokenCount:      int64(100 * (i + 1)),
			ComputeTimeMs:   int64(10 * (i + 1)),
			ToolInvocations: i + 1,
			TotalCost:       float64(i+1) * 0.01,
		}
		if err := ctrl.RecordUsage(context.Background(), event); err != nil {
			t.Fatalf("RecordUsage %d failed: %v", i, err)
		}
	}

	// Query with limit.
	report, err := ctrl.QueryCosts(context.Background(), domain.CostFilter{
		TeamID: "team-q",
		Limit:  3,
	})
	if err != nil {
		t.Fatalf("QueryCosts failed: %v", err)
	}
	if len(report.Records) != 3 {
		t.Errorf("expected 3 records, got %d", len(report.Records))
	}
	if report.Total != 5 {
		t.Errorf("expected total 5, got %d", report.Total)
	}
}

func TestQueryCosts_MaxLimit1000(t *testing.T) {
	s := setupTestStore(t)
	emitter, _ := NewChannelEmitter(10)
	ctrl := New(s, emitter, DefaultConfig())

	// Requesting limit > 1000 gets capped to 1000.
	report, err := ctrl.QueryCosts(context.Background(), domain.CostFilter{
		Limit: 5000,
	})
	if err != nil {
		t.Fatalf("QueryCosts failed: %v", err)
	}
	// No records, but limit was silently capped (not an error).
	if report == nil {
		t.Fatal("expected non-nil report")
	}
}

func TestGetBudgetStatus(t *testing.T) {
	s := setupTestStore(t)
	emitter, _ := NewChannelEmitter(10)
	ctrl := New(s, emitter, DefaultConfig())

	setupBudget(t, s, "team-status", 200.0, 80.0)

	status, err := ctrl.GetBudgetStatus(context.Background(), "team-status")
	if err != nil {
		t.Fatalf("GetBudgetStatus failed: %v", err)
	}
	if status.TeamID != "team-status" {
		t.Errorf("expected team-status, got %s", status.TeamID)
	}
	if status.BudgetCap != 200.0 {
		t.Errorf("expected cap 200, got %f", status.BudgetCap)
	}
	if status.AccumulatedCost != 80.0 {
		t.Errorf("expected accumulated 80, got %f", status.AccumulatedCost)
	}
	if status.UtilizationPct != 0.4 {
		t.Errorf("expected utilization 0.4, got %f", status.UtilizationPct)
	}
	if status.IsBlocked {
		t.Error("expected not blocked")
	}
}

func TestBudgetPeriodReset(t *testing.T) {
	s := setupTestStore(t)
	emitter, _ := NewChannelEmitter(10)
	ctrl := New(s, emitter, DefaultConfig())

	// Set budget with period_start in the past (more than a month ago).
	budget := &domain.Budget{
		ID:              "budget-reset",
		TeamID:          "team-reset",
		CapAmount:       100.0,
		PeriodType:      "monthly",
		PeriodStart:     time.Now().AddDate(0, -2, 0), // 2 months ago
		AccumulatedCost: 95.0,
		IsBlocked:       true,
	}
	if err := s.Budgets().Set(context.Background(), budget); err != nil {
		t.Fatalf("failed to set budget: %v", err)
	}

	// GetBudgetStatus should trigger reset.
	status, err := ctrl.GetBudgetStatus(context.Background(), "team-reset")
	if err != nil {
		t.Fatalf("GetBudgetStatus failed: %v", err)
	}
	if status.AccumulatedCost != 0.0 {
		t.Errorf("expected accumulated 0 after reset, got %f", status.AccumulatedCost)
	}
	if status.IsBlocked {
		t.Error("expected unblocked after period reset")
	}
}

func TestRecordUsage_NilEvent(t *testing.T) {
	s := setupTestStore(t)
	emitter, _ := NewChannelEmitter(10)
	ctrl := New(s, emitter, DefaultConfig())

	err := ctrl.RecordUsage(context.Background(), nil)
	if err == nil {
		t.Error("expected error for nil event")
	}
}

func TestGetBudgetStatus_EmptyTeamID(t *testing.T) {
	s := setupTestStore(t)
	emitter, _ := NewChannelEmitter(10)
	ctrl := New(s, emitter, DefaultConfig())

	_, err := ctrl.GetBudgetStatus(context.Background(), "")
	if err == nil {
		t.Error("expected error for empty team ID")
	}
}

func TestCheckBudget_EmptyTeamID(t *testing.T) {
	s := setupTestStore(t)
	emitter, _ := NewChannelEmitter(10)
	ctrl := New(s, emitter, DefaultConfig())

	_, err := ctrl.CheckBudget(context.Background(), "", 10.0)
	if err == nil {
		t.Error("expected error for empty team ID")
	}
}
