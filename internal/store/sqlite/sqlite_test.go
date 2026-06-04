package sqlite

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := New("file::memory:?_foreign_keys=on")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrate(t *testing.T) {
	s := newTestStore(t)
	// Running migrate again should be idempotent.
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestAgentStore_CreateAndGet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	agent := &domain.AgentEntry{
		ID:          "agent-1",
		Name:        "test-agent",
		Namespace:   "default",
		Version:     "1.0.0",
		RuntimeType: domain.RuntimeClaude,
		Status:      domain.AgentStatusActive,
		Capabilities: []domain.Capability{
			{Name: "code-review", Type: "mcp-tool"},
		},
		Labels:    map[string]string{"team": "platform"},
		SLOs:      domain.SLODefinition{Latency: &domain.LatencyBound{MaxMs: 5000}},
		Resources: domain.ResourceLimits{MaxConcurrentMissions: 10, MaxMemoryMB: 512},
		Deployment: domain.DeploymentStrategy{Type: "rolling", MaxInstances: 5},
		CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
		UpdatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}

	if err := s.Agents().Create(ctx, agent); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.Agents().Get(ctx, "agent-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected agent, got nil")
	}
	if got.Name != "test-agent" {
		t.Errorf("name = %q, want %q", got.Name, "test-agent")
	}
	if got.RuntimeType != domain.RuntimeClaude {
		t.Errorf("runtime = %q, want %q", got.RuntimeType, domain.RuntimeClaude)
	}
	if len(got.Capabilities) != 1 || got.Capabilities[0].Name != "code-review" {
		t.Errorf("capabilities = %v, want [{code-review mcp-tool}]", got.Capabilities)
	}
	if got.Labels["team"] != "platform" {
		t.Errorf("labels = %v, want {team:platform}", got.Labels)
	}
}

func TestAgentStore_QueryByCapability(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for i, cap := range []string{"code-review", "test-gen"} {
		agent := &domain.AgentEntry{
			ID: fmt.Sprintf("agent-%d", i+1), Name: fmt.Sprintf("agent-%d", i+1),
			Namespace: "ns", Version: "1.0.0", RuntimeType: domain.RuntimeClaude,
			Status: domain.AgentStatusActive,
			Capabilities: []domain.Capability{{Name: cap, Type: "mcp-tool"}},
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}
		if err := s.Agents().Create(ctx, agent); err != nil {
			t.Fatalf("create agent-%d: %v", i+1, err)
		}
	}

	results, err := s.Agents().QueryByCapability(ctx, "code-review")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].ID != "agent-1" {
		t.Errorf("got agent %s, want agent-1", results[0].ID)
	}
}

func TestMissionStore_CreateAndGetPending(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mission := &domain.Mission{
		ID:                   "mission-1",
		TeamID:               "team-a",
		ProjectID:            "proj-1",
		Status:               domain.MissionStatusPending,
		RequiredCapabilities: []string{"code-review"},
		Priority:             5,
		Timeout:              time.Hour,
		SubmittedAt:          time.Now().UTC(),
	}

	if err := s.Missions().Create(ctx, mission); err != nil {
		t.Fatalf("create: %v", err)
	}

	pending, err := s.Missions().GetPending(ctx)
	if err != nil {
		t.Fatalf("get pending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(pending))
	}
	if pending[0].ID != "mission-1" {
		t.Errorf("got mission %s, want mission-1", pending[0].ID)
	}
}

func TestPolicyStore_CreateAndGetEffective(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	orgPolicy := &domain.Policy{
		ID: "pol-org", Name: "org-policy", Scope: domain.PolicyScopeOrganization,
		Rules: []domain.PolicyRule{{ID: "r1", Type: domain.PolicyRuleTypeBudget, Condition: "true", Effect: domain.PolicyEffectAllow}},
		Version: 1, UpdatedAt: time.Now().UTC(),
	}
	teamPolicy := &domain.Policy{
		ID: "pol-team", Name: "team-policy", Scope: domain.PolicyScopeTeam, TeamID: "team-a",
		Rules: []domain.PolicyRule{{ID: "r2", Type: domain.PolicyRuleTypeRuntime, Condition: "true", Effect: domain.PolicyEffectDeny}},
		Version: 1, UpdatedAt: time.Now().UTC(),
	}

	if err := s.Policies().Create(ctx, orgPolicy); err != nil {
		t.Fatalf("create org: %v", err)
	}
	if err := s.Policies().Create(ctx, teamPolicy); err != nil {
		t.Fatalf("create team: %v", err)
	}

	effective, err := s.Policies().GetEffective(ctx, domain.PolicyScopeTeam, "team-a")
	if err != nil {
		t.Fatalf("get effective: %v", err)
	}
	if len(effective) != 2 {
		t.Fatalf("expected 2 effective policies, got %d", len(effective))
	}
}

func TestBudgetStore_SetAndIncrement(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	budget := &domain.Budget{
		ID: "b-1", TeamID: "team-a", CapAmount: 1000.0,
		PeriodType: "monthly", PeriodStart: time.Now().UTC(),
		AccumulatedCost: 0.0, IsBlocked: false,
	}

	if err := s.Budgets().Set(ctx, budget); err != nil {
		t.Fatalf("set: %v", err)
	}

	if err := s.Budgets().IncrementAccumulated(ctx, "team-a", 250.0); err != nil {
		t.Fatalf("increment: %v", err)
	}

	got, err := s.Budgets().Get(ctx, "team-a")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.AccumulatedCost != 250.0 {
		t.Errorf("accumulated = %f, want 250.0", got.AccumulatedCost)
	}
}

func TestTraceStore_CreateAndGet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	trace := &domain.MissionTrace{
		TraceID: "trace-1", MissionID: "m-1", AgentID: "a-1",
		Goal: "review PR", Outcome: domain.OutcomeSuccess,
		StartTime: time.Now().UTC(), ExpiresAt: time.Now().Add(30 * 24 * time.Hour).UTC(),
		Duration: 5 * time.Second,
	}
	endTime := time.Now().UTC()
	trace.EndTime = &endTime

	if err := s.Traces().Create(ctx, trace); err != nil {
		t.Fatalf("create trace: %v", err)
	}

	span := &domain.TraceSpan{
		SpanID: "span-1", TraceID: "trace-1", ParentSpanID: "",
		Name: "analyze", StartTime: time.Now().UTC(), DurationMs: 2000,
		Status: domain.StepStatusSuccess, Attributes: map[string]string{"file": "main.go"},
	}
	if err := s.Traces().AddSpan(ctx, span); err != nil {
		t.Fatalf("add span: %v", err)
	}

	got, err := s.Traces().Get(ctx, "trace-1")
	if err != nil {
		t.Fatalf("get trace: %v", err)
	}
	if got == nil {
		t.Fatal("expected trace, got nil")
	}
	if got.Goal != "review PR" {
		t.Errorf("goal = %q, want %q", got.Goal, "review PR")
	}
	if len(got.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(got.Steps))
	}
	if got.Steps[0].Name != "analyze" {
		t.Errorf("step name = %q, want %q", got.Steps[0].Name, "analyze")
	}
}

func TestMessageStore_CreateAndMoveToDLQ(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	msg := &domain.AgentMessage{
		ID: "msg-1", From: "agent-a", To: "agent-b",
		Type: domain.MessageTypePointToPoint, Payload: []byte("hello"),
		Status: domain.MessageStatusPending, SentAt: time.Now().UTC(), RetryCount: 3,
	}

	if err := s.Messages().Create(ctx, msg); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := s.Messages().MoveToDLQ(ctx, "msg-1", "connection refused"); err != nil {
		t.Fatalf("move to dlq: %v", err)
	}

	dlq, err := s.Messages().GetDLQ(ctx, domain.DLQFilter{Limit: 10})
	if err != nil {
		t.Fatalf("get dlq: %v", err)
	}
	if len(dlq) != 1 {
		t.Fatalf("expected 1 dlq entry, got %d", len(dlq))
	}
	if dlq[0].FailureReason != "connection refused" {
		t.Errorf("reason = %q, want %q", dlq[0].FailureReason, "connection refused")
	}
}

func TestCostStore_RecordAndQuery(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	record := &domain.CostRecord{
		ID: "cost-1", MissionID: "m-1", AgentID: "a-1",
		TeamID: "team-a", ProjectID: "proj-1",
		TokenCount: 1000, ComputeTimeMs: 5000, ToolInvocations: 3,
		TotalCost: 0.05, RecordedAt: time.Now().UTC(),
	}

	if err := s.Costs().Record(ctx, record); err != nil {
		t.Fatalf("record: %v", err)
	}

	results, total, err := s.Costs().Query(ctx, domain.CostFilter{TeamID: "team-a", Limit: 10})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].TotalCost != 0.05 {
		t.Errorf("cost = %f, want 0.05", results[0].TotalCost)
	}

	agg, err := s.Costs().Aggregate(ctx, domain.CostFilter{TeamID: "team-a"})
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if agg.TotalCost != 0.05 {
		t.Errorf("agg cost = %f, want 0.05", agg.TotalCost)
	}
	if agg.RecordCount != 1 {
		t.Errorf("agg count = %d, want 1", agg.RecordCount)
	}
}
