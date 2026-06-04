package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/store"
	"github.com/agentplane/agentplane/internal/store/sqlite"
)

func setupTestStore(t *testing.T) *sqlite.SQLiteStore {
	t.Helper()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("new sqlite: %v", err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

func TestMigrator_EmptySource(t *testing.T) {
	source := setupTestStore(t)
	target := setupTestStore(t)
	defer source.Close()
	defer target.Close()

	m := store.NewMigrator(source, target)
	result, err := m.Run(context.Background())
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	if result.TotalCount != 0 {
		t.Errorf("expected 0 total records, got %d", result.TotalCount)
	}
	for table, count := range result.TableCounts {
		if count != 0 {
			t.Errorf("table %s: expected 0, got %d", table, count)
		}
	}
}

func TestMigrator_MigratesAllTables(t *testing.T) {
	ctx := context.Background()
	source := setupTestStore(t)
	target := setupTestStore(t)
	defer source.Close()
	defer target.Close()

	now := time.Now().UTC().Truncate(time.Second)

	// Seed agents.
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
		Labels:    map[string]string{"env": "test"},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := source.Agents().Create(ctx, agent); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	// Seed agent version.
	version := &domain.AgentVersion{
		ID:           "ver-1",
		AgentID:      "agent-1",
		Version:      "1.0.0",
		RegisteredAt: now,
	}
	if err := source.Agents().AddVersion(ctx, version); err != nil {
		t.Fatalf("add version: %v", err)
	}

	// Seed mission.
	mission := &domain.Mission{
		ID:                   "mission-1",
		AgentID:              "agent-1",
		TeamID:               "team-1",
		ProjectID:            "proj-1",
		Status:               domain.MissionStatusCompleted,
		RequiredCapabilities: []string{"code-review"},
		Payload:              []byte(`{"goal":"test"}`),
		Priority:             1,
		Timeout:              30 * time.Second,
		SubmittedAt:          now,
	}
	if err := source.Missions().Create(ctx, mission); err != nil {
		t.Fatalf("create mission: %v", err)
	}

	// Seed policy.
	policy := &domain.Policy{
		ID:      "policy-1",
		Name:    "test-policy",
		Scope:   domain.PolicyScopeTeam,
		TeamID:  "team-1",
		Rules:   []domain.PolicyRule{{ID: "r1", Type: domain.PolicyRuleTypeBudget, Condition: "true", Effect: domain.PolicyEffectAllow}},
		Version: 1,
		UpdatedAt: now,
	}
	if err := source.Policies().Create(ctx, policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}

	// Seed cost event.
	costRecord := &domain.CostRecord{
		ID:              "cost-1",
		MissionID:       "mission-1",
		AgentID:         "agent-1",
		TeamID:          "team-1",
		ProjectID:       "proj-1",
		TokenCount:      100,
		ComputeTimeMs:   500,
		ToolInvocations: 3,
		TotalCost:       0.05,
		RecordedAt:      now,
	}
	if err := source.Costs().Record(ctx, costRecord); err != nil {
		t.Fatalf("record cost: %v", err)
	}

	// Seed budget.
	budget := &domain.Budget{
		ID:              "budget-1",
		TeamID:          "team-1",
		CapAmount:       1000.0,
		PeriodType:      "monthly",
		PeriodStart:     now,
		AccumulatedCost: 50.0,
		IsBlocked:       false,
	}
	if err := source.Budgets().Set(ctx, budget); err != nil {
		t.Fatalf("set budget: %v", err)
	}

	// Seed trace.
	trace := &domain.MissionTrace{
		TraceID:   "trace-1",
		MissionID: "mission-1",
		AgentID:   "agent-1",
		Goal:      "test goal",
		Outcome:   domain.OutcomeSuccess,
		StartTime: now,
		Duration:  5 * time.Second,
		ExpiresAt: now.Add(30 * 24 * time.Hour),
	}
	if err := source.Traces().Create(ctx, trace); err != nil {
		t.Fatalf("create trace: %v", err)
	}

	// Seed trace step.
	span := &domain.TraceSpan{
		SpanID:       "span-1",
		TraceID:      "trace-1",
		ParentSpanID: "",
		Name:         "step-1",
		StartTime:    now,
		DurationMs:   1000,
		Status:       domain.StepStatusSuccess,
		Attributes:   map[string]string{"key": "val"},
	}
	if err := source.Traces().AddSpan(ctx, span); err != nil {
		t.Fatalf("add span: %v", err)
	}

	// Seed message.
	msg := &domain.AgentMessage{
		ID:      "msg-1",
		From:    "agent-1",
		To:      "agent-2",
		Type:    domain.MessageTypePointToPoint,
		Payload: []byte("hello"),
		Status:  domain.MessageStatusDelivered,
		SentAt:  now,
	}
	if err := source.Messages().Create(ctx, msg); err != nil {
		t.Fatalf("create message: %v", err)
	}

	// Seed dead letter (need a failed message first).
	dlMsg := &domain.AgentMessage{
		ID:         "msg-dl-1",
		From:       "agent-1",
		To:         "agent-3",
		Type:       domain.MessageTypePointToPoint,
		Payload:    []byte("failed"),
		Status:     domain.MessageStatusPending,
		SentAt:     now,
		RetryCount: 3,
	}
	if err := source.Messages().Create(ctx, dlMsg); err != nil {
		t.Fatalf("create dl message: %v", err)
	}
	if err := source.Messages().MoveToDLQ(ctx, "msg-dl-1", "timeout"); err != nil {
		t.Fatalf("move to dlq: %v", err)
	}

	// Run migration.
	m := store.NewMigrator(source, target)
	result, err := m.Run(ctx)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	// Verify counts.
	expectedCounts := map[string]int{
		"agents":         1,
		"agent_versions": 1,
		"missions":       1,
		"policies":       1,
		"cost_events":    1,
		"budget_caps":    1,
		"mission_traces": 1,
		"trace_steps":    1,
		"messages":       2, // msg-1 + msg-dl-1
		"dead_letters":   1,
	}

	for table, expected := range expectedCounts {
		got := result.TableCounts[table]
		if got != expected {
			t.Errorf("table %s: expected %d, got %d", table, expected, got)
		}
	}

	// Verify data in target.
	gotAgent, err := target.Agents().Get(ctx, "agent-1")
	if err != nil {
		t.Fatalf("get agent from target: %v", err)
	}
	if gotAgent == nil {
		t.Fatal("agent not found in target")
	}
	if gotAgent.Name != "test-agent" {
		t.Errorf("agent name: expected test-agent, got %s", gotAgent.Name)
	}
	if gotAgent.RuntimeType != domain.RuntimeClaude {
		t.Errorf("agent runtime: expected claude, got %s", gotAgent.RuntimeType)
	}

	gotVersions, err := target.Agents().ListVersions(ctx, "agent-1")
	if err != nil {
		t.Fatalf("list versions from target: %v", err)
	}
	if len(gotVersions) != 1 {
		t.Errorf("versions: expected 1, got %d", len(gotVersions))
	}

	gotMission, err := target.Missions().Get(ctx, "mission-1")
	if err != nil {
		t.Fatalf("get mission from target: %v", err)
	}
	if gotMission == nil {
		t.Fatal("mission not found in target")
	}
	if gotMission.TeamID != "team-1" {
		t.Errorf("mission team: expected team-1, got %s", gotMission.TeamID)
	}

	gotPolicy, err := target.Policies().Get(ctx, "policy-1")
	if err != nil {
		t.Fatalf("get policy from target: %v", err)
	}
	if gotPolicy == nil {
		t.Fatal("policy not found in target")
	}
	if gotPolicy.Name != "test-policy" {
		t.Errorf("policy name: expected test-policy, got %s", gotPolicy.Name)
	}

	gotTrace, err := target.Traces().Get(ctx, "trace-1")
	if err != nil {
		t.Fatalf("get trace from target: %v", err)
	}
	if gotTrace == nil {
		t.Fatal("trace not found in target")
	}
	if len(gotTrace.Steps) != 1 {
		t.Errorf("trace steps: expected 1, got %d", len(gotTrace.Steps))
	}

	gotBudget, err := target.Budgets().Get(ctx, "team-1")
	if err != nil {
		t.Fatalf("get budget from target: %v", err)
	}
	if gotBudget == nil {
		t.Fatal("budget not found in target")
	}
	if gotBudget.CapAmount != 1000.0 {
		t.Errorf("budget cap: expected 1000.0, got %f", gotBudget.CapAmount)
	}

	gotMsg, err := target.Messages().Get(ctx, "msg-1")
	if err != nil {
		t.Fatalf("get message from target: %v", err)
	}
	if gotMsg == nil {
		t.Fatal("message not found in target")
	}
	if string(gotMsg.Payload) != "hello" {
		t.Errorf("message payload: expected hello, got %s", string(gotMsg.Payload))
	}

	gotDLQ, err := target.Messages().GetDLQ(ctx, domain.DLQFilter{Limit: 100})
	if err != nil {
		t.Fatalf("get dlq from target: %v", err)
	}
	if len(gotDLQ) != 1 {
		t.Errorf("dlq: expected 1, got %d", len(gotDLQ))
	}
}

func TestMigrator_SourceUnmodified(t *testing.T) {
	ctx := context.Background()
	source := setupTestStore(t)
	target := setupTestStore(t)
	defer source.Close()
	defer target.Close()

	now := time.Now().UTC().Truncate(time.Second)

	// Seed source.
	agent := &domain.AgentEntry{
		ID:           "agent-src",
		Name:         "source-agent",
		Namespace:    "default",
		Version:      "1.0.0",
		RuntimeType:  domain.RuntimeKiro,
		Status:       domain.AgentStatusActive,
		Capabilities: []domain.Capability{{Name: "test", Type: "mcp-tool"}},
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := source.Agents().Create(ctx, agent); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	// Run migration.
	m := store.NewMigrator(source, target)
	_, err := m.Run(ctx)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	// Verify source is untouched.
	srcAgent, err := source.Agents().Get(ctx, "agent-src")
	if err != nil {
		t.Fatalf("get source agent: %v", err)
	}
	if srcAgent == nil {
		t.Fatal("source agent was deleted")
	}
	if srcAgent.Name != "source-agent" {
		t.Errorf("source agent modified: got name %s", srcAgent.Name)
	}
}
