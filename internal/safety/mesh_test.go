package safety

import (
	"context"
	"testing"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/store/sqlite"
)

// testEmitter collects events for assertion.
type testEmitter struct {
	events []domain.SystemEvent
}

func (e *testEmitter) Emit(event domain.SystemEvent) {
	e.events = append(e.events, event)
}

func (e *testEmitter) lastEvent() *domain.SystemEvent {
	if len(e.events) == 0 {
		return nil
	}
	return &e.events[len(e.events)-1]
}

func (e *testEmitter) eventsByType(t string) []domain.SystemEvent {
	var result []domain.SystemEvent
	for _, ev := range e.events {
		if ev.Type == t {
			result = append(result, ev)
		}
	}
	return result
}

func setupTestSafety(t *testing.T) (*SafetyMesh, *testEmitter) {
	t.Helper()
	s, err := sqlite.New(":memory:?_foreign_keys=on")
	if err != nil {
		t.Fatalf("sqlite new: %v", err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	emitter := &testEmitter{}
	sm := New(s, emitter, DefaultConfig())
	return sm, emitter
}

func createTestAgent(t *testing.T, sm *SafetyMesh, id, name string) *domain.AgentEntry {
	t.Helper()
	agent := &domain.AgentEntry{
		ID:          id,
		Name:        name,
		Namespace:   "default",
		Version:     "1.0.0",
		RuntimeType: domain.RuntimeClaude,
		Status:      domain.AgentStatusActive,
		Capabilities: []domain.Capability{{Name: "test", Type: "mcp-tool"}},
		Labels:      map[string]string{},
		Resources:   domain.ResourceLimits{MaxConcurrentMissions: 10},
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := sm.store.Agents().Create(context.Background(), agent); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return agent
}

func createTestMission(t *testing.T, sm *SafetyMesh, id, agentID string, status domain.MissionStatus) *domain.Mission {
	t.Helper()
	m := &domain.Mission{
		ID:                   id,
		AgentID:              agentID,
		TeamID:               "team-1",
		Status:               status,
		RequiredCapabilities: []string{"test"},
		Priority:             1,
		Timeout:              time.Hour,
		SubmittedAt:          time.Now(),
	}
	if status == domain.MissionStatusAssigned || status == domain.MissionStatusRunning {
		now := time.Now()
		m.AssignedAt = &now
	}
	if err := sm.store.Missions().Create(context.Background(), m); err != nil {
		t.Fatalf("create mission: %v", err)
	}
	return m
}

func TestKillSwitch_CancelsActiveMissions(t *testing.T) {
	sm, emitter := setupTestSafety(t)
	ctx := context.Background()

	agent := createTestAgent(t, sm, "agent-1", "test-agent")
	createTestMission(t, sm, "m-1", agent.ID, domain.MissionStatusRunning)
	createTestMission(t, sm, "m-2", agent.ID, domain.MissionStatusAssigned)

	err := sm.ActivateKillSwitch(ctx)
	if err != nil {
		t.Fatalf("activate kill switch: %v", err)
	}

	// Verify missions cancelled.
	m1, _ := sm.store.Missions().Get(ctx, "m-1")
	m2, _ := sm.store.Missions().Get(ctx, "m-2")
	if m1.Status != domain.MissionStatusCancelled {
		t.Errorf("expected m-1 cancelled, got %s", m1.Status)
	}
	if m2.Status != domain.MissionStatusCancelled {
		t.Errorf("expected m-2 cancelled, got %s", m2.Status)
	}

	// Verify agent transitioned to idle-safe.
	a, _ := sm.store.Agents().Get(ctx, agent.ID)
	if a.Status != domain.AgentStatusIdleSafe {
		t.Errorf("expected agent idle-safe, got %s", a.Status)
	}

	// Verify event emitted.
	events := emitter.eventsByType("kill_switch_activated")
	if len(events) == 0 {
		t.Error("expected kill_switch_activated event")
	}
}

func TestKillSwitch_PreservesInterruptedMissionState(t *testing.T) {
	sm, _ := setupTestSafety(t)
	ctx := context.Background()

	agent := createTestAgent(t, sm, "agent-1", "test-agent")
	mission := createTestMission(t, sm, "m-1", agent.ID, domain.MissionStatusRunning)

	// Mission has payload and assignment info.
	mission.Payload = []byte(`{"goal": "review code"}`)
	sm.store.Missions().Update(ctx, mission)

	err := sm.ActivateKillSwitch(ctx)
	if err != nil {
		t.Fatalf("activate kill switch: %v", err)
	}

	// Verify mission state preserved (payload still exists, just status changed).
	m, _ := sm.store.Missions().Get(ctx, "m-1")
	if m.Status != domain.MissionStatusCancelled {
		t.Errorf("expected cancelled, got %s", m.Status)
	}
	if string(m.Payload) != `{"goal": "review code"}` {
		t.Error("mission payload not preserved")
	}
	if m.CompletedAt == nil {
		t.Error("expected CompletedAt to be set")
	}
}

func TestKillSwitch_PreventsNewAssignments(t *testing.T) {
	sm, _ := setupTestSafety(t)
	ctx := context.Background()

	createTestAgent(t, sm, "agent-1", "test-agent")

	err := sm.ActivateKillSwitch(ctx)
	if err != nil {
		t.Fatalf("activate kill switch: %v", err)
	}

	if !sm.IsKillSwitchActive() {
		t.Error("expected kill switch to be active")
	}
}

func TestDeactivateKillSwitch_RestoresAgents(t *testing.T) {
	sm, emitter := setupTestSafety(t)
	ctx := context.Background()

	createTestAgent(t, sm, "agent-1", "test-agent")

	sm.ActivateKillSwitch(ctx)
	sm.DeactivateKillSwitch(ctx)

	a, _ := sm.store.Agents().Get(ctx, "agent-1")
	if a.Status != domain.AgentStatusActive {
		t.Errorf("expected agent active, got %s", a.Status)
	}

	events := emitter.eventsByType("kill_switch_deactivated")
	if len(events) == 0 {
		t.Error("expected kill_switch_deactivated event")
	}
}

func TestCircuitBreaker_OpensOnHighErrorRate(t *testing.T) {
	sm, emitter := setupTestSafety(t)
	ctx := context.Background()

	createTestAgent(t, sm, "agent-1", "test-agent")

	// Record 3 successes and 3 failures (>50% error rate with 6 >= 5 min missions).
	sm.RecordMissionOutcome(ctx, "agent-1", true)
	sm.RecordMissionOutcome(ctx, "agent-1", true)
	sm.RecordMissionOutcome(ctx, "agent-1", false)
	sm.RecordMissionOutcome(ctx, "agent-1", false)
	sm.RecordMissionOutcome(ctx, "agent-1", false)
	sm.RecordMissionOutcome(ctx, "agent-1", false) // 4/6 = 66% error rate

	cb, err := sm.GetCircuitBreaker(ctx, "agent-1")
	if err != nil {
		t.Fatalf("get cb: %v", err)
	}
	if cb.State != domain.CBOpen {
		t.Errorf("expected breaker open, got %s", cb.State)
	}

	events := emitter.eventsByType("circuit_breaker_opened")
	if len(events) == 0 {
		t.Error("expected circuit_breaker_opened event")
	}
}

func TestCircuitBreaker_DoesNotOpenBelowMinMissions(t *testing.T) {
	sm, _ := setupTestSafety(t)
	ctx := context.Background()

	createTestAgent(t, sm, "agent-1", "test-agent")

	// Only 4 missions (below minimum of 5).
	sm.RecordMissionOutcome(ctx, "agent-1", false)
	sm.RecordMissionOutcome(ctx, "agent-1", false)
	sm.RecordMissionOutcome(ctx, "agent-1", false)
	sm.RecordMissionOutcome(ctx, "agent-1", false)

	cb, _ := sm.GetCircuitBreaker(ctx, "agent-1")
	if cb.State != domain.CBClosed {
		t.Errorf("expected breaker closed with <5 missions, got %s", cb.State)
	}
}

func TestCircuitBreaker_DoesNotOpenAt50Percent(t *testing.T) {
	sm, _ := setupTestSafety(t)
	ctx := context.Background()

	createTestAgent(t, sm, "agent-1", "test-agent")

	// Exactly 50% (not exceeding threshold).
	sm.RecordMissionOutcome(ctx, "agent-1", true)
	sm.RecordMissionOutcome(ctx, "agent-1", true)
	sm.RecordMissionOutcome(ctx, "agent-1", true)
	sm.RecordMissionOutcome(ctx, "agent-1", false)
	sm.RecordMissionOutcome(ctx, "agent-1", false)
	sm.RecordMissionOutcome(ctx, "agent-1", false) // 3/6 = 50% exactly

	cb, _ := sm.GetCircuitBreaker(ctx, "agent-1")
	if cb.State != domain.CBClosed {
		t.Errorf("expected breaker closed at exactly 50%%, got %s", cb.State)
	}
}

func TestCircuitBreaker_ProbeSuccess_ClosesBreaker(t *testing.T) {
	sm, emitter := setupTestSafety(t)
	ctx := context.Background()

	createTestAgent(t, sm, "agent-1", "test-agent")

	// Open breaker.
	for i := 0; i < 6; i++ {
		sm.RecordMissionOutcome(ctx, "agent-1", false)
	}

	// Set cooldown to already be elapsed.
	sm.mu.Lock()
	cb := sm.breakers["agent-1"]
	past := time.Now().Add(-2 * time.Minute)
	cb.CooldownEnd = &past
	sm.mu.Unlock()

	// Record a heartbeat so probe considers agent reachable.
	sm.RecordHeartbeat("agent-1")

	result, err := sm.ProbeAgent(ctx, "agent-1")
	if err != nil {
		t.Fatalf("probe agent: %v", err)
	}
	if !result.Success {
		t.Errorf("expected probe success, got failure: %s", result.Error)
	}

	cbState, _ := sm.GetCircuitBreaker(ctx, "agent-1")
	if cbState.State != domain.CBClosed {
		t.Errorf("expected breaker closed after successful probe, got %s", cbState.State)
	}

	events := emitter.eventsByType("circuit_breaker_closed")
	if len(events) == 0 {
		t.Error("expected circuit_breaker_closed event")
	}
}

func TestCircuitBreaker_ProbeFailure_ReOpens(t *testing.T) {
	sm, emitter := setupTestSafety(t)
	ctx := context.Background()

	// Create agent but mark as idle-safe so probe fails.
	agent := createTestAgent(t, sm, "agent-1", "test-agent")
	agent.Status = domain.AgentStatusIdleSafe
	sm.store.Agents().Update(ctx, agent)

	// Open breaker manually.
	sm.mu.Lock()
	now := time.Now()
	past := now.Add(-2 * time.Minute)
	sm.breakers["agent-1"] = &domain.CircuitBreakerState{
		AgentID:      "agent-1",
		State:        domain.CBOpen,
		OpenedAt:     &past,
		CooldownEnd:  &past,
		CooldownSecs: 60,
	}
	sm.mu.Unlock()

	result, err := sm.ProbeAgent(ctx, "agent-1")
	if err != nil {
		t.Fatalf("probe agent: %v", err)
	}
	if result.Success {
		t.Error("expected probe failure for idle-safe agent")
	}

	cbState, _ := sm.GetCircuitBreaker(ctx, "agent-1")
	if cbState.State != domain.CBOpen {
		t.Errorf("expected breaker re-opened after failed probe, got %s", cbState.State)
	}

	events := emitter.eventsByType("circuit_breaker_reopened")
	if len(events) == 0 {
		t.Error("expected circuit_breaker_reopened event")
	}
}

func TestCircuitBreaker_CooldownNotElapsed_ProbeReturnsError(t *testing.T) {
	sm, _ := setupTestSafety(t)
	ctx := context.Background()

	createTestAgent(t, sm, "agent-1", "test-agent")

	// Open breaker with future cooldown.
	sm.mu.Lock()
	now := time.Now()
	future := now.Add(5 * time.Minute)
	sm.breakers["agent-1"] = &domain.CircuitBreakerState{
		AgentID:      "agent-1",
		State:        domain.CBOpen,
		OpenedAt:     &now,
		CooldownEnd:  &future,
		CooldownSecs: 60,
	}
	sm.mu.Unlock()

	_, err := sm.ProbeAgent(ctx, "agent-1")
	if err == nil {
		t.Error("expected error when cooldown not elapsed")
	}
}

func TestPolicyInheritance_AgentOverridesFleet(t *testing.T) {
	sm, _ := setupTestSafety(t)
	ctx := context.Background()

	createTestAgent(t, sm, "agent-1", "test-agent")

	// Set agent-level policy with different cooldown.
	agentPolicy := SafetyPolicy{
		CooldownSecs:       120,
		ErrorRateThreshold: 0.7,
		WindowDuration:     5 * time.Minute,
		MinMissions:        3,
		ConnectivityTimeout: 30 * time.Second,
	}
	sm.SetAgentPolicy("agent-1", agentPolicy)

	// Record missions: 3 failures out of 4 = 75% > 70% threshold.
	sm.RecordMissionOutcome(ctx, "agent-1", true)
	sm.RecordMissionOutcome(ctx, "agent-1", false)
	sm.RecordMissionOutcome(ctx, "agent-1", false)
	sm.RecordMissionOutcome(ctx, "agent-1", false)

	cb, _ := sm.GetCircuitBreaker(ctx, "agent-1")
	if cb.State != domain.CBOpen {
		t.Errorf("expected breaker open with agent policy threshold, got %s", cb.State)
	}
	if cb.CooldownSecs != 120 {
		t.Errorf("expected cooldown 120s from agent policy, got %d", cb.CooldownSecs)
	}
}

func TestPolicyInheritance_FleetLevelDefault(t *testing.T) {
	sm, _ := setupTestSafety(t)
	ctx := context.Background()

	createTestAgent(t, sm, "agent-1", "test-agent")

	// No agent-level override: use fleet default (50% threshold, 5 min missions).
	cb, _ := sm.GetCircuitBreaker(ctx, "agent-1")
	if cb.CooldownSecs != 60 {
		t.Errorf("expected fleet default cooldown 60s, got %d", cb.CooldownSecs)
	}
}

func TestConnectivity_MarksUnhealthyAndOpensBreaker(t *testing.T) {
	sm, emitter := setupTestSafety(t)
	ctx := context.Background()

	createTestAgent(t, sm, "agent-1", "test-agent")

	// Set stale heartbeat (>30s ago).
	sm.mu.Lock()
	sm.lastHeartbeat["agent-1"] = time.Now().Add(-45 * time.Second)
	sm.mu.Unlock()

	sm.CheckConnectivity(ctx)

	// Agent should be unhealthy.
	agent, _ := sm.store.Agents().Get(ctx, "agent-1")
	if agent.Status != domain.AgentStatusUnhealthy {
		t.Errorf("expected agent unhealthy, got %s", agent.Status)
	}

	// Breaker should be open.
	cb, _ := sm.GetCircuitBreaker(ctx, "agent-1")
	if cb.State != domain.CBOpen {
		t.Errorf("expected breaker open, got %s", cb.State)
	}

	events := emitter.eventsByType("agent_unhealthy")
	if len(events) == 0 {
		t.Error("expected agent_unhealthy event")
	}
}

func TestConnectivity_RecentHeartbeat_NoAction(t *testing.T) {
	sm, _ := setupTestSafety(t)
	ctx := context.Background()

	createTestAgent(t, sm, "agent-1", "test-agent")

	// Recent heartbeat.
	sm.RecordHeartbeat("agent-1")

	sm.CheckConnectivity(ctx)

	agent, _ := sm.store.Agents().Get(ctx, "agent-1")
	if agent.Status != domain.AgentStatusActive {
		t.Errorf("expected agent active with recent heartbeat, got %s", agent.Status)
	}
}

func TestGetCircuitBreaker_DefaultClosed(t *testing.T) {
	sm, _ := setupTestSafety(t)
	ctx := context.Background()

	cb, err := sm.GetCircuitBreaker(ctx, "unknown-agent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cb.State != domain.CBClosed {
		t.Errorf("expected default closed, got %s", cb.State)
	}
}
