package property

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/slo"
	"github.com/agentplane/agentplane/internal/store"
	"pgregory.net/rapid"
)

// --- Mock implementations for SLO property tests ---

type sloMockEventEmitter struct {
	mu     sync.Mutex
	events []domain.SystemEvent
}

func (m *sloMockEventEmitter) Emit(event domain.SystemEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
}

func (m *sloMockEventEmitter) Events() []domain.SystemEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]domain.SystemEvent, len(m.events))
	copy(cp, m.events)
	return cp
}

type sloMockTrafficReducer struct {
	mu    sync.Mutex
	calls []sloTrafficCall
}

type sloTrafficCall struct {
	AgentID  string
	Fraction float64
}

func (m *sloMockTrafficReducer) ReduceTraffic(_ context.Context, agentID string, fraction float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, sloTrafficCall{AgentID: agentID, Fraction: fraction})
	return nil
}

func (m *sloMockTrafficReducer) Calls() []sloTrafficCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]sloTrafficCall, len(m.calls))
	copy(cp, m.calls)
	return cp
}

type sloMockRollbackTrigger struct {
	mu          sync.Mutex
	calls       []string
	errToReturn error
}

func (m *sloMockRollbackTrigger) Rollback(_ context.Context, agentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, agentID)
	return m.errToReturn
}

func (m *sloMockRollbackTrigger) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

type sloMockMetricsProvider struct {
	mu      sync.Mutex
	metrics map[string]float64
	err     error
}

func (m *sloMockMetricsProvider) GetRollingMetrics(_ context.Context, _ string, _ time.Duration) (map[string]float64, error) {
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

func (m *sloMockMetricsProvider) SetMetrics(metrics map[string]float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.metrics = metrics
}

// --- Mock store for SLO tests ---

type sloMockAgentStore struct {
	agents map[string]*domain.AgentEntry
}

func (m *sloMockAgentStore) Create(_ context.Context, agent *domain.AgentEntry) error {
	m.agents[agent.ID] = agent
	return nil
}
func (m *sloMockAgentStore) Get(_ context.Context, id string) (*domain.AgentEntry, error) {
	a, ok := m.agents[id]
	if !ok {
		return nil, nil
	}
	return a, nil
}
func (m *sloMockAgentStore) GetByName(_ context.Context, _, _ string) (*domain.AgentEntry, error) {
	return nil, nil
}
func (m *sloMockAgentStore) List(_ context.Context, _ domain.AgentFilter) ([]*domain.AgentEntry, error) {
	return nil, nil
}
func (m *sloMockAgentStore) Update(_ context.Context, _ *domain.AgentEntry) error { return nil }
func (m *sloMockAgentStore) Delete(_ context.Context, _ string) error             { return nil }
func (m *sloMockAgentStore) QueryByCapability(_ context.Context, _ string) ([]*domain.AgentEntry, error) {
	return nil, nil
}
func (m *sloMockAgentStore) AddVersion(_ context.Context, _ *domain.AgentVersion) error { return nil }
func (m *sloMockAgentStore) ListVersions(_ context.Context, _ string) ([]*domain.AgentVersion, error) {
	return nil, nil
}
func (m *sloMockAgentStore) DeleteVersion(_ context.Context, _ string) error { return nil }

type sloMockStore struct {
	agentStore *sloMockAgentStore
}

func (m *sloMockStore) Agents() store.AgentStore    { return m.agentStore }
func (m *sloMockStore) Missions() store.MissionStore { return nil }
func (m *sloMockStore) Policies() store.PolicyStore  { return nil }
func (m *sloMockStore) Costs() store.CostStore       { return nil }
func (m *sloMockStore) Traces() store.TraceStore     { return nil }
func (m *sloMockStore) Budgets() store.BudgetStore   { return nil }
func (m *sloMockStore) Messages() store.MessageStore { return nil }
func (m *sloMockStore) Migrate(_ context.Context) error { return nil }
func (m *sloMockStore) Close() error                    { return nil }

// --- Helpers ---

func newSLOTestManager(agent *domain.AgentEntry, metrics map[string]float64, rollbackErr error) (
	*slo.Manager, *sloMockEventEmitter, *sloMockTrafficReducer, *sloMockRollbackTrigger, *sloMockMetricsProvider,
) {
	agentStore := &sloMockAgentStore{agents: map[string]*domain.AgentEntry{}}
	if agent != nil {
		agentStore.agents[agent.ID] = agent
	}
	s := &sloMockStore{agentStore: agentStore}

	emitter := &sloMockEventEmitter{}
	trafficReducer := &sloMockTrafficReducer{}
	rollbackTrigger := &sloMockRollbackTrigger{errToReturn: rollbackErr}
	metricsProvider := &sloMockMetricsProvider{metrics: metrics}

	mgr := slo.NewManager(s, emitter, trafficReducer, rollbackTrigger, metricsProvider, slo.DefaultConfig())
	return mgr, emitter, trafficReducer, rollbackTrigger, metricsProvider
}

func sloTestAgent(id string, slos domain.SLODefinition) *domain.AgentEntry {
	return &domain.AgentEntry{
		ID:      id,
		Name:    "test-agent",
		Version: "1.0.0",
		Status:  domain.AgentStatusActive,
		SLOs:    slos,
	}
}

// --- Property Tests ---

// TestProperty40_TrafficReductionOnSustainedSLOViolation tests that when an agent's rolling
// metrics violate its declared SLO for more than 5 consecutive minutes (60s granularity),
// traffic is reduced by 50%.
// **Validates: Requirements 9.2**
func TestProperty40_TrafficReductionOnSustainedSLOViolation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()

		// Generate SLO bounds.
		maxLatencyMs := rapid.IntRange(100, 50000).Draw(rt, "maxLatencyMs")
		slos := domain.SLODefinition{
			Latency: &domain.LatencyBound{MaxMs: maxLatencyMs},
		}

		agentID := "agent-traffic-" + rapid.StringMatching(`[a-z0-9]{6}`).Draw(rt, "agentID")
		agent := sloTestAgent(agentID, slos)

		// Generate a violating metric value (above threshold).
		violatingLatency := float64(maxLatencyMs) + float64(rapid.IntRange(1, 10000).Draw(rt, "excess"))
		metrics := map[string]float64{"latency_ms": violatingLatency}

		mgr, _, trafficReducer, _, _ := newSLOTestManager(agent, metrics, nil)

		// Simulate exactly 6 consecutive violations (>5 threshold in default config).
		for i := 0; i < 6; i++ {
			_, err := mgr.EvaluateSLO(ctx, agentID)
			if err != nil {
				t.Fatalf("EvaluateSLO iteration %d: %v", i, err)
			}
		}

		// Verify traffic was reduced to 50%.
		calls := trafficReducer.Calls()
		if len(calls) == 0 {
			t.Fatal("expected traffic reducer to be called after >5 min violation")
		}

		// The last call should reduce to 0.5 fraction (50% of original traffic).
		lastCall := calls[len(calls)-1]
		if lastCall.AgentID != agentID {
			t.Errorf("traffic reduction for wrong agent: got %q, want %q", lastCall.AgentID, agentID)
		}
		if lastCall.Fraction != 0.5 {
			t.Errorf("expected 50%% traffic reduction (fraction=0.5), got %f", lastCall.Fraction)
		}
	})
}

// TestProperty41_AutomaticRollbackOnProlongedViolation tests that when SLO violation
// persists for more than 15 minutes, a rollback to previous version is triggered.
// **Validates: Requirements 9.3**
func TestProperty41_AutomaticRollbackOnProlongedViolation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()

		// Generate random SLO type to violate.
		sloType := rapid.IntRange(0, 2).Draw(rt, "sloType")

		var slos domain.SLODefinition
		var metrics map[string]float64

		switch sloType {
		case 0: // Latency violation
			maxMs := rapid.IntRange(100, 50000).Draw(rt, "maxMs")
			slos = domain.SLODefinition{Latency: &domain.LatencyBound{MaxMs: maxMs}}
			violating := float64(maxMs) + float64(rapid.IntRange(1, 5000).Draw(rt, "latExcess"))
			metrics = map[string]float64{"latency_ms": violating}
		case 1: // Accuracy violation
			minPct := float64(rapid.IntRange(50, 99).Draw(rt, "minPct"))
			slos = domain.SLODefinition{Accuracy: &domain.AccuracyBound{MinPercent: minPct}}
			violating := minPct - float64(rapid.IntRange(1, 30).Draw(rt, "accDrop"))
			if violating < 0 {
				violating = 0
			}
			metrics = map[string]float64{"accuracy_pct": violating}
		case 2: // Cost violation
			maxCost := float64(rapid.IntRange(1, 100).Draw(rt, "maxCostCents")) / 100.0
			slos = domain.SLODefinition{Cost: &domain.CostBound{MaxCost: maxCost}}
			violating := maxCost + float64(rapid.IntRange(1, 50).Draw(rt, "costExcess"))/100.0
			metrics = map[string]float64{"cost_per_invocation": violating}
		}

		agentID := "agent-rollback-" + rapid.StringMatching(`[a-z0-9]{6}`).Draw(rt, "agentID")
		agent := sloTestAgent(agentID, slos)

		mgr, _, _, rollbackTrigger, _ := newSLOTestManager(agent, metrics, nil)

		// Simulate 16 consecutive violations (>15 threshold).
		for i := 0; i < 16; i++ {
			_, err := mgr.EvaluateSLO(ctx, agentID)
			if err != nil {
				t.Fatalf("EvaluateSLO iteration %d: %v", i, err)
			}
		}

		// Verify rollback was triggered.
		if rollbackTrigger.CallCount() == 0 {
			t.Fatal("expected rollback to be triggered after >15 min violation")
		}
	})
}

// TestProperty42_SLOComplianceCalculation tests that compliance % equals compliant intervals
// divided by total intervals in a 1-hour rolling window.
// **Validates: Requirements 9.4**
func TestProperty42_SLOComplianceCalculation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()

		// Generate SLO bounds.
		maxLatencyMs := rapid.IntRange(100, 50000).Draw(rt, "maxLatencyMs")
		slos := domain.SLODefinition{
			Latency: &domain.LatencyBound{MaxMs: maxLatencyMs},
		}

		agentID := "agent-compliance-" + rapid.StringMatching(`[a-z0-9]{6}`).Draw(rt, "agentID")
		agent := sloTestAgent(agentID, slos)

		// Decide whether metrics are compliant or violating.
		isCompliant := rapid.Bool().Draw(rt, "isCompliant")

		var latencyValue float64
		if isCompliant {
			// Compliant: value at or below threshold.
			latencyValue = float64(rapid.IntRange(1, maxLatencyMs).Draw(rt, "compliantLatency"))
		} else {
			// Violating: value above threshold.
			latencyValue = float64(maxLatencyMs) + float64(rapid.IntRange(1, 5000).Draw(rt, "violatingLatency"))
		}

		metrics := map[string]float64{"latency_ms": latencyValue}
		mgr, _, _, _, _ := newSLOTestManager(agent, metrics, nil)

		compliance, err := mgr.GetCompliance(ctx, agentID)
		if err != nil {
			t.Fatalf("GetCompliance: %v", err)
		}

		// Verify compliance calculation.
		// With a fixed metrics provider, all intervals return same result.
		// Total intervals in 1h at 60s granularity = 60.
		if isCompliant {
			// All 60 intervals compliant → 100%.
			if compliance.CompliancePct != 100.0 {
				t.Errorf("expected 100%% compliance for compliant metrics, got %f", compliance.CompliancePct)
			}
		} else {
			// All 60 intervals violating → 0%.
			if compliance.CompliancePct != 0.0 {
				t.Errorf("expected 0%% compliance for violating metrics, got %f", compliance.CompliancePct)
			}
		}

		// Verify window is 1 hour.
		if compliance.Window != 1*time.Hour {
			t.Errorf("expected 1h window, got %v", compliance.Window)
		}

		// Verify agent ID matches.
		if compliance.AgentID != agentID {
			t.Errorf("expected agent ID %q, got %q", agentID, compliance.AgentID)
		}

		// Compliance is bounded [0, 100].
		if compliance.CompliancePct < 0 || compliance.CompliancePct > 100.0 {
			t.Errorf("compliance %% out of range: %f", compliance.CompliancePct)
		}
	})
}

// TestProperty43_SLOBreachEventCompleteness tests that every SLO breach event contains
// agent ID, metric name, threshold, and observed value.
// **Validates: Requirements 9.5**
func TestProperty43_SLOBreachEventCompleteness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()

		// Generate random SLO type.
		sloType := rapid.IntRange(0, 2).Draw(rt, "sloType")

		var slos domain.SLODefinition
		var metrics map[string]float64
		var expectedMetricName string
		var expectedThreshold float64
		var expectedObserved float64

		switch sloType {
		case 0: // Latency violation
			maxMs := rapid.IntRange(100, 50000).Draw(rt, "maxMs")
			slos = domain.SLODefinition{Latency: &domain.LatencyBound{MaxMs: maxMs}}
			observed := float64(maxMs) + float64(rapid.IntRange(1, 10000).Draw(rt, "excess"))
			metrics = map[string]float64{"latency_ms": observed}
			expectedMetricName = "latency_ms"
			expectedThreshold = float64(maxMs)
			expectedObserved = observed
		case 1: // Accuracy violation
			minPct := float64(rapid.IntRange(10, 99).Draw(rt, "minPct"))
			slos = domain.SLODefinition{Accuracy: &domain.AccuracyBound{MinPercent: minPct}}
			drop := float64(rapid.IntRange(1, int(minPct)-1).Draw(rt, "drop"))
			observed := minPct - drop
			metrics = map[string]float64{"accuracy_pct": observed}
			expectedMetricName = "accuracy_pct"
			expectedThreshold = minPct
			expectedObserved = observed
		case 2: // Cost violation
			maxCostCents := rapid.IntRange(1, 500).Draw(rt, "maxCostCents")
			maxCost := float64(maxCostCents) / 100.0
			slos = domain.SLODefinition{Cost: &domain.CostBound{MaxCost: maxCost}}
			excessCents := rapid.IntRange(1, 200).Draw(rt, "excessCents")
			observed := maxCost + float64(excessCents)/100.0
			metrics = map[string]float64{"cost_per_invocation": observed}
			expectedMetricName = "cost_per_invocation"
			expectedThreshold = maxCost
			expectedObserved = observed
		}

		agentID := "agent-breach-" + rapid.StringMatching(`[a-z0-9]{6}`).Draw(rt, "agentID")
		agent := sloTestAgent(agentID, slos)

		mgr, emitter, _, _, _ := newSLOTestManager(agent, metrics, nil)

		// Trigger one evaluation to produce a breach event.
		_, err := mgr.EvaluateSLO(ctx, agentID)
		if err != nil {
			t.Fatalf("EvaluateSLO: %v", err)
		}

		// Verify breach event completeness.
		events := emitter.Events()
		if len(events) == 0 {
			t.Fatal("expected slo_breach event to be emitted")
		}

		breachEvent := events[0]
		if breachEvent.Type != "slo_breach" {
			t.Fatalf("expected event type 'slo_breach', got %q", breachEvent.Type)
		}

		// Verify agent_id present and correct.
		if breachEvent.Payload["agent_id"] != agentID {
			t.Errorf("event agent_id: got %v, want %q", breachEvent.Payload["agent_id"], agentID)
		}

		// Verify metric_name present and correct.
		if breachEvent.Payload["metric_name"] != expectedMetricName {
			t.Errorf("event metric_name: got %v, want %q", breachEvent.Payload["metric_name"], expectedMetricName)
		}

		// Verify threshold present and correct.
		threshold, ok := breachEvent.Payload["threshold"].(float64)
		if !ok {
			t.Fatalf("event threshold not a float64: %T", breachEvent.Payload["threshold"])
		}
		if threshold != expectedThreshold {
			t.Errorf("event threshold: got %f, want %f", threshold, expectedThreshold)
		}

		// Verify observed value present and correct.
		observed, ok := breachEvent.Payload["observed"].(float64)
		if !ok {
			t.Fatalf("event observed not a float64: %T", breachEvent.Payload["observed"])
		}
		if observed != expectedObserved {
			t.Errorf("event observed: got %f, want %f", observed, expectedObserved)
		}

		// Verify timestamp is set.
		if breachEvent.Timestamp.IsZero() {
			t.Error("event timestamp is zero")
		}

		// Verify source is set.
		if breachEvent.Source == "" {
			t.Error("event source is empty")
		}
	})
}
