package slo

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/store"
)

// --- Test doubles ---

type mockEventEmitter struct {
	mu     sync.Mutex
	events []domain.SystemEvent
}

func (m *mockEventEmitter) Emit(event domain.SystemEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
}

func (m *mockEventEmitter) Events() []domain.SystemEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]domain.SystemEvent, len(m.events))
	copy(cp, m.events)
	return cp
}

type mockTrafficReducer struct {
	mu    sync.Mutex
	calls []trafficCall
}

type trafficCall struct {
	AgentID  string
	Fraction float64
}

func (m *mockTrafficReducer) ReduceTraffic(_ context.Context, agentID string, fraction float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, trafficCall{AgentID: agentID, Fraction: fraction})
	return nil
}

func (m *mockTrafficReducer) LastCall() *trafficCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.calls) == 0 {
		return nil
	}
	return &m.calls[len(m.calls)-1]
}

type mockRollbackTrigger struct {
	mu            sync.Mutex
	calls         []string
	errToReturn   error
}

func (m *mockRollbackTrigger) Rollback(_ context.Context, agentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, agentID)
	return m.errToReturn
}

func (m *mockRollbackTrigger) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

type mockMetricsProvider struct {
	mu      sync.Mutex
	metrics map[string]float64
	err     error
}

func (m *mockMetricsProvider) GetRollingMetrics(_ context.Context, _ string, _ time.Duration) (map[string]float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	cp := make(map[string]float64, len(m.metrics))
	for k, v := range m.metrics {
		cp[k] = v
	}
	return cp, nil
}

func (m *mockMetricsProvider) SetMetrics(metrics map[string]float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.metrics = metrics
}

// mockAgentStore implements store.AgentStore for testing.
type mockAgentStore struct {
	agents map[string]*domain.AgentEntry
}

func (m *mockAgentStore) Create(_ context.Context, agent *domain.AgentEntry) error {
	m.agents[agent.ID] = agent
	return nil
}
func (m *mockAgentStore) Get(_ context.Context, id string) (*domain.AgentEntry, error) {
	a, ok := m.agents[id]
	if !ok {
		return nil, nil
	}
	return a, nil
}
func (m *mockAgentStore) GetByName(_ context.Context, _, _ string) (*domain.AgentEntry, error) {
	return nil, nil
}
func (m *mockAgentStore) List(_ context.Context, _ domain.AgentFilter) ([]*domain.AgentEntry, error) {
	return nil, nil
}
func (m *mockAgentStore) Update(_ context.Context, _ *domain.AgentEntry) error { return nil }
func (m *mockAgentStore) Delete(_ context.Context, _ string) error             { return nil }
func (m *mockAgentStore) QueryByCapability(_ context.Context, _ string) ([]*domain.AgentEntry, error) {
	return nil, nil
}
func (m *mockAgentStore) AddVersion(_ context.Context, _ *domain.AgentVersion) error { return nil }
func (m *mockAgentStore) ListVersions(_ context.Context, _ string) ([]*domain.AgentVersion, error) {
	return nil, nil
}
func (m *mockAgentStore) DeleteVersion(_ context.Context, _ string) error { return nil }

// mockStore wraps mockAgentStore to satisfy store.Store.
type mockStore struct {
	agentStore *mockAgentStore
}

func (m *mockStore) Agents() store.AgentStore      { return m.agentStore }
func (m *mockStore) Missions() store.MissionStore   { return nil }
func (m *mockStore) Policies() store.PolicyStore    { return nil }
func (m *mockStore) Costs() store.CostStore         { return nil }
func (m *mockStore) Traces() store.TraceStore       { return nil }
func (m *mockStore) Budgets() store.BudgetStore     { return nil }
func (m *mockStore) Messages() store.MessageStore   { return nil }
func (m *mockStore) Migrate(_ context.Context) error { return nil }
func (m *mockStore) Close() error                    { return nil }

// --- Test helpers ---

func newTestManager(agent *domain.AgentEntry, metrics map[string]float64) (*Manager, *mockEventEmitter, *mockTrafficReducer, *mockRollbackTrigger, *mockMetricsProvider) {
	agentStore := &mockAgentStore{agents: map[string]*domain.AgentEntry{}}
	if agent != nil {
		agentStore.agents[agent.ID] = agent
	}
	s := &mockStore{agentStore: agentStore}

	emitter := &mockEventEmitter{}
	trafficReducer := &mockTrafficReducer{}
	rollbackTrigger := &mockRollbackTrigger{}
	metricsProvider := &mockMetricsProvider{metrics: metrics}

	mgr := NewManager(s, emitter, trafficReducer, rollbackTrigger, metricsProvider, DefaultConfig())
	return mgr, emitter, trafficReducer, rollbackTrigger, metricsProvider
}

func testAgent(id string, slos domain.SLODefinition) *domain.AgentEntry {
	return &domain.AgentEntry{
		ID:      id,
		Name:    "test-agent",
		Version: "1.0.0",
		Status:  domain.AgentStatusActive,
		SLOs:    slos,
	}
}

// --- Tests ---

func TestEvaluateSLO_NoViolation(t *testing.T) {
	slos := domain.SLODefinition{
		Latency:  &domain.LatencyBound{MaxMs: 5000},
		Accuracy: &domain.AccuracyBound{MinPercent: 95.0},
	}
	agent := testAgent("agent-1", slos)
	metrics := map[string]float64{
		"latency_ms":   3000,
		"accuracy_pct": 98.0,
	}

	mgr, emitter, trafficReducer, _, _ := newTestManager(agent, metrics)

	status, err := mgr.EvaluateSLO(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if status.ViolationStart != nil {
		t.Error("expected no violation start")
	}
	if status.ViolationMinutes != 0 {
		t.Errorf("expected 0 violation minutes, got %d", status.ViolationMinutes)
	}
	if status.TrafficReduction != 0.0 {
		t.Errorf("expected 0.0 traffic reduction, got %f", status.TrafficReduction)
	}

	// No slo_breach event should be emitted.
	events := emitter.Events()
	if len(events) != 0 {
		t.Errorf("expected no events, got %d", len(events))
	}

	// No traffic reduction calls.
	if trafficReducer.LastCall() != nil {
		t.Error("expected no traffic reducer calls")
	}
}

func TestEvaluateSLO_LatencyViolation_EmitsBreachEvent(t *testing.T) {
	slos := domain.SLODefinition{
		Latency: &domain.LatencyBound{MaxMs: 5000},
	}
	agent := testAgent("agent-1", slos)
	metrics := map[string]float64{
		"latency_ms": 6000, // exceeds 5000ms
	}

	mgr, emitter, _, _, _ := newTestManager(agent, metrics)

	status, err := mgr.EvaluateSLO(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if status.ViolationStart == nil {
		t.Error("expected violation start to be set")
	}
	if status.ViolationMinutes != 1 {
		t.Errorf("expected 1 violation minute, got %d", status.ViolationMinutes)
	}

	// slo_breach event emitted.
	events := emitter.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "slo_breach" {
		t.Errorf("expected slo_breach event, got %s", events[0].Type)
	}
	payload := events[0].Payload
	if payload["agent_id"] != "agent-1" {
		t.Errorf("expected agent_id=agent-1, got %v", payload["agent_id"])
	}
	if payload["metric_name"] != "latency_ms" {
		t.Errorf("expected metric_name=latency_ms, got %v", payload["metric_name"])
	}
	if payload["threshold"] != float64(5000) {
		t.Errorf("expected threshold=5000, got %v", payload["threshold"])
	}
	if payload["observed"] != float64(6000) {
		t.Errorf("expected observed=6000, got %v", payload["observed"])
	}
}

func TestEvaluateSLO_AccuracyViolation(t *testing.T) {
	slos := domain.SLODefinition{
		Accuracy: &domain.AccuracyBound{MinPercent: 95.0},
	}
	agent := testAgent("agent-1", slos)
	metrics := map[string]float64{
		"accuracy_pct": 90.0, // below 95%
	}

	mgr, emitter, _, _, _ := newTestManager(agent, metrics)

	status, err := mgr.EvaluateSLO(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if status.ViolationMinutes != 1 {
		t.Errorf("expected 1 violation minute, got %d", status.ViolationMinutes)
	}

	events := emitter.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Payload["metric_name"] != "accuracy_pct" {
		t.Errorf("expected metric_name=accuracy_pct, got %v", events[0].Payload["metric_name"])
	}
}

func TestEvaluateSLO_CostViolation(t *testing.T) {
	slos := domain.SLODefinition{
		Cost: &domain.CostBound{MaxCost: 0.05},
	}
	agent := testAgent("agent-1", slos)
	metrics := map[string]float64{
		"cost_per_invocation": 0.10, // exceeds 0.05
	}

	mgr, emitter, _, _, _ := newTestManager(agent, metrics)

	status, err := mgr.EvaluateSLO(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if status.ViolationMinutes != 1 {
		t.Errorf("expected 1 violation minute, got %d", status.ViolationMinutes)
	}

	events := emitter.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Payload["metric_name"] != "cost_per_invocation" {
		t.Errorf("expected metric_name=cost_per_invocation, got %v", events[0].Payload["metric_name"])
	}
}

func TestEvaluateSLO_TrafficReduction_After5Minutes(t *testing.T) {
	slos := domain.SLODefinition{
		Latency: &domain.LatencyBound{MaxMs: 5000},
	}
	agent := testAgent("agent-1", slos)
	metrics := map[string]float64{
		"latency_ms": 6000,
	}

	mgr, _, trafficReducer, _, _ := newTestManager(agent, metrics)

	// Simulate 6 consecutive violations (>5 threshold).
	for i := 0; i < 6; i++ {
		_, err := mgr.EvaluateSLO(context.Background(), "agent-1")
		if err != nil {
			t.Fatalf("unexpected error on iteration %d: %v", i, err)
		}
	}

	// After 6th evaluation, traffic should be reduced.
	lastCall := trafficReducer.LastCall()
	if lastCall == nil {
		t.Fatal("expected traffic reducer to be called")
	}
	if lastCall.Fraction != 0.5 {
		t.Errorf("expected 50%% traffic (fraction=0.5), got %f", lastCall.Fraction)
	}
}

func TestEvaluateSLO_AutoRollback_After15Minutes(t *testing.T) {
	slos := domain.SLODefinition{
		Latency: &domain.LatencyBound{MaxMs: 5000},
	}
	agent := testAgent("agent-1", slos)
	metrics := map[string]float64{
		"latency_ms": 6000,
	}

	mgr, _, _, rollbackTrigger, _ := newTestManager(agent, metrics)

	// Simulate 16 consecutive violations (>15 threshold).
	for i := 0; i < 16; i++ {
		_, err := mgr.EvaluateSLO(context.Background(), "agent-1")
		if err != nil {
			t.Fatalf("unexpected error on iteration %d: %v", i, err)
		}
	}

	// Rollback should have been triggered.
	if rollbackTrigger.CallCount() == 0 {
		t.Error("expected rollback to be triggered")
	}
}

func TestEvaluateSLO_RollbackFailed_NoVersion_ZeroTraffic(t *testing.T) {
	slos := domain.SLODefinition{
		Latency: &domain.LatencyBound{MaxMs: 5000},
	}
	agent := testAgent("agent-1", slos)
	metrics := map[string]float64{
		"latency_ms": 6000,
	}

	mgr, emitter, trafficReducer, rollbackTrigger, _ := newTestManager(agent, metrics)
	rollbackTrigger.errToReturn = errors.New("no previous version available for rollback")

	// Simulate 16 consecutive violations.
	var lastStatus *domain.SLOStatus
	for i := 0; i < 16; i++ {
		s, err := mgr.EvaluateSLO(context.Background(), "agent-1")
		if err != nil {
			t.Fatalf("unexpected error on iteration %d: %v", i, err)
		}
		lastStatus = s
	}

	// Rollback should have been attempted.
	if rollbackTrigger.CallCount() == 0 {
		t.Error("expected rollback to be attempted")
	}

	// Should emit rollback_failed event.
	events := emitter.Events()
	hasRollbackFailed := false
	for _, e := range events {
		if e.Type == "rollback_failed" {
			hasRollbackFailed = true
			if e.Payload["agent_id"] != "agent-1" {
				t.Errorf("expected agent_id=agent-1 in rollback_failed, got %v", e.Payload["agent_id"])
			}
			break
		}
	}
	if !hasRollbackFailed {
		t.Error("expected rollback_failed event")
	}

	// Traffic should be reduced to 0%.
	lastTrafficCall := trafficReducer.LastCall()
	if lastTrafficCall == nil {
		t.Fatal("expected traffic reducer to be called")
	}
	if lastTrafficCall.Fraction != 0.0 {
		t.Errorf("expected 0%% traffic (fraction=0.0), got %f", lastTrafficCall.Fraction)
	}

	// Status should show 100% traffic reduction.
	if lastStatus.TrafficReduction != 1.0 {
		t.Errorf("expected TrafficReduction=1.0, got %f", lastStatus.TrafficReduction)
	}
}

func TestEvaluateSLO_ViolationClears_OnCompliance(t *testing.T) {
	slos := domain.SLODefinition{
		Latency: &domain.LatencyBound{MaxMs: 5000},
	}
	agent := testAgent("agent-1", slos)

	// Start with violation.
	metricsData := map[string]float64{"latency_ms": 6000}
	mgr, _, _, _, metricsProvider := newTestManager(agent, metricsData)

	// Violate for 3 intervals.
	for i := 0; i < 3; i++ {
		_, _ = mgr.EvaluateSLO(context.Background(), "agent-1")
	}

	// Now become compliant.
	metricsProvider.SetMetrics(map[string]float64{"latency_ms": 3000})

	status, err := mgr.EvaluateSLO(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Violation should be cleared.
	if status.ViolationStart != nil {
		t.Error("expected violation to be cleared")
	}
	if status.ViolationMinutes != 0 {
		t.Errorf("expected 0 violation minutes, got %d", status.ViolationMinutes)
	}
}

func TestEvaluateSLO_AgentNotFound(t *testing.T) {
	mgr, _, _, _, _ := newTestManager(nil, nil)

	_, err := mgr.EvaluateSLO(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
	if !errors.Is(err, ErrAgentNotFound) {
		t.Errorf("expected ErrAgentNotFound, got %v", err)
	}
}

func TestEvaluateSLO_NoSLODefined(t *testing.T) {
	agent := testAgent("agent-1", domain.SLODefinition{})
	mgr, _, _, _, _ := newTestManager(agent, nil)

	_, err := mgr.EvaluateSLO(context.Background(), "agent-1")
	if err == nil {
		t.Fatal("expected error for no SLO")
	}
	if !errors.Is(err, ErrNoSLODefined) {
		t.Errorf("expected ErrNoSLODefined, got %v", err)
	}
}

func TestGetCompliance_AllCompliant(t *testing.T) {
	slos := domain.SLODefinition{
		Latency: &domain.LatencyBound{MaxMs: 5000},
	}
	agent := testAgent("agent-1", slos)
	metrics := map[string]float64{
		"latency_ms": 3000, // compliant
	}

	mgr, _, _, _, _ := newTestManager(agent, metrics)

	compliance, err := mgr.GetCompliance(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if compliance.AgentID != "agent-1" {
		t.Errorf("expected agent-1, got %s", compliance.AgentID)
	}
	if compliance.CompliancePct != 100.0 {
		t.Errorf("expected 100%% compliance, got %f", compliance.CompliancePct)
	}
	if compliance.Window != 1*time.Hour {
		t.Errorf("expected 1h window, got %v", compliance.Window)
	}
}

func TestGetCompliance_AllViolating(t *testing.T) {
	slos := domain.SLODefinition{
		Latency: &domain.LatencyBound{MaxMs: 5000},
	}
	agent := testAgent("agent-1", slos)
	metrics := map[string]float64{
		"latency_ms": 6000, // violating
	}

	mgr, _, _, _, _ := newTestManager(agent, metrics)

	compliance, err := mgr.GetCompliance(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if compliance.CompliancePct != 0.0 {
		t.Errorf("expected 0%% compliance, got %f", compliance.CompliancePct)
	}
}

func TestGetCompliance_AgentNotFound(t *testing.T) {
	mgr, _, _, _, _ := newTestManager(nil, nil)

	_, err := mgr.GetCompliance(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
	if !errors.Is(err, ErrAgentNotFound) {
		t.Errorf("expected ErrAgentNotFound, got %v", err)
	}
}

func TestGetCompliance_NoSLODefined(t *testing.T) {
	agent := testAgent("agent-1", domain.SLODefinition{})
	mgr, _, _, _, _ := newTestManager(agent, nil)

	_, err := mgr.GetCompliance(context.Background(), "agent-1")
	if err == nil {
		t.Fatal("expected error for no SLO")
	}
	if !errors.Is(err, ErrNoSLODefined) {
		t.Errorf("expected ErrNoSLODefined, got %v", err)
	}
}
