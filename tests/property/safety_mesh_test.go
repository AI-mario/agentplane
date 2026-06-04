package property

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/safety"
	"github.com/agentplane/agentplane/internal/store/sqlite"
	"pgregory.net/rapid"
)

// --- Test Helpers ---

// testSafetyEmitter collects events for assertion.
type testSafetyEmitter struct {
	events []domain.SystemEvent
}

func (e *testSafetyEmitter) Emit(event domain.SystemEvent) {
	e.events = append(e.events, event)
}

func (e *testSafetyEmitter) eventsByType(t string) []domain.SystemEvent {
	var result []domain.SystemEvent
	for _, ev := range e.events {
		if ev.Type == t {
			result = append(result, ev)
		}
	}
	return result
}

// newSafetyStore creates a uniquely-named in-memory SQLite store for safety tests.
func newSafetyStore(t *testing.T, name string) *sqlite.SQLiteStore {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&_foreign_keys=on", name)
	s, err := sqlite.New(dsn)
	if err != nil {
		t.Fatalf("new store %s: %v", name, err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate %s: %v", name, err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// createSafetyAgent creates an active agent in the store via the safety mesh's store.
func createSafetyAgent(t *testing.T, s *sqlite.SQLiteStore, id, name string) {
	t.Helper()
	agent := &domain.AgentEntry{
		ID:           id,
		Name:         name,
		Namespace:    "default",
		Version:      "1.0.0",
		RuntimeType:  domain.RuntimeClaude,
		Status:       domain.AgentStatusActive,
		Capabilities: []domain.Capability{{Name: "test", Type: "mcp-tool"}},
		Labels:       map[string]string{},
		Resources:    domain.ResourceLimits{MaxConcurrentMissions: 10},
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := s.Agents().Create(context.Background(), agent); err != nil {
		t.Fatalf("create agent %s: %v", id, err)
	}
}

// createSafetyMission creates a mission with the given status.
func createSafetyMission(t *testing.T, s *sqlite.SQLiteStore, id, agentID string, status domain.MissionStatus) {
	t.Helper()
	now := time.Now()
	m := &domain.Mission{
		ID:                   id,
		AgentID:              agentID,
		TeamID:               "team-1",
		Status:               status,
		RequiredCapabilities: []string{"test"},
		Priority:             1,
		Timeout:              time.Hour,
		SubmittedAt:          now,
	}
	if status == domain.MissionStatusAssigned || status == domain.MissionStatusRunning {
		m.AssignedAt = &now
	}
	if err := s.Missions().Create(context.Background(), m); err != nil {
		t.Fatalf("create mission %s: %v", id, err)
	}
}

// --- Property Tests ---

// TestProperty44_KillSwitchHaltsAllFleetActivity tests that activating the kill switch
// cancels all N active missions, prevents new assignments, and transitions agents to idle-safe.
// **Validates: Requirements 10.1**
func TestProperty44_KillSwitchHaltsAllFleetActivity(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()
		storeName := fmt.Sprintf("safety44_%s", rapid.StringMatching(`[a-z]{6}`).Draw(rt, "name"))
		s := newSafetyStore(t, storeName)
		emitter := &testSafetyEmitter{}
		sm := safety.New(s, emitter, safety.DefaultConfig())

		// Generate N agents (1-5).
		numAgents := rapid.IntRange(1, 5).Draw(rt, "numAgents")
		agentIDs := make([]string, numAgents)
		for i := 0; i < numAgents; i++ {
			id := fmt.Sprintf("agent-%d", i)
			createSafetyAgent(t, s, id, fmt.Sprintf("Agent %d", i))
			agentIDs[i] = id
		}

		// Generate M missions per agent (0-3), randomly assigned or running.
		var totalMissions int
		for _, agentID := range agentIDs {
			numMissions := rapid.IntRange(0, 3).Draw(rt, fmt.Sprintf("missions_%s", agentID))
			for j := 0; j < numMissions; j++ {
				missionID := fmt.Sprintf("m-%s-%d", agentID, j)
				status := domain.MissionStatusAssigned
				if rapid.Bool().Draw(rt, fmt.Sprintf("running_%s_%d", agentID, j)) {
					status = domain.MissionStatusRunning
				}
				createSafetyMission(t, s, missionID, agentID, status)
				totalMissions++
			}
		}

		// Activate kill switch.
		err := sm.ActivateKillSwitch(ctx)
		if err != nil {
			t.Fatalf("ActivateKillSwitch: %v", err)
		}

		// Verify: all missions cancelled.
		assignedMissions, _ := s.Missions().List(ctx, domain.MissionFilter{Status: domain.MissionStatusAssigned, Limit: 10000})
		runningMissions, _ := s.Missions().List(ctx, domain.MissionFilter{Status: domain.MissionStatusRunning, Limit: 10000})
		if len(assignedMissions)+len(runningMissions) != 0 {
			t.Errorf("expected 0 active missions after kill switch, got %d assigned + %d running",
				len(assignedMissions), len(runningMissions))
		}

		cancelledMissions, _ := s.Missions().List(ctx, domain.MissionFilter{Status: domain.MissionStatusCancelled, Limit: 10000})
		if len(cancelledMissions) != totalMissions {
			t.Errorf("expected %d cancelled missions, got %d", totalMissions, len(cancelledMissions))
		}

		// Verify: no new assignments possible (kill switch active).
		if !sm.IsKillSwitchActive() {
			t.Error("expected kill switch to be active")
		}

		// Verify: all agents idle-safe.
		for _, agentID := range agentIDs {
			agent, err := s.Agents().Get(ctx, agentID)
			if err != nil {
				t.Fatalf("get agent %s: %v", agentID, err)
			}
			if agent.Status != domain.AgentStatusIdleSafe {
				t.Errorf("agent %s: expected idle-safe, got %s", agentID, agent.Status)
			}
		}
	})
}

// TestProperty45_KillSwitchPreservesMissionState tests that in-progress missions have
// their last state preserved and are marked as cancelled (interrupted).
// **Validates: Requirements 10.2**
func TestProperty45_KillSwitchPreservesMissionState(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()
		storeName := fmt.Sprintf("safety45_%s", rapid.StringMatching(`[a-z]{6}`).Draw(rt, "name"))
		s := newSafetyStore(t, storeName)
		emitter := &testSafetyEmitter{}
		sm := safety.New(s, emitter, safety.DefaultConfig())

		agentID := "agent-preserve"
		createSafetyAgent(t, s, agentID, "Preserve Agent")

		// Create a running mission with payload (simulating in-progress state).
		missionID := fmt.Sprintf("m-%s", rapid.StringMatching(`[a-z0-9]{6}`).Draw(rt, "mID"))
		payloadContent := rapid.StringMatching(`[a-zA-Z0-9 ]{5,50}`).Draw(rt, "payload")
		payload := fmt.Sprintf(`{"goal": "%s"}`, payloadContent)

		now := time.Now()
		mission := &domain.Mission{
			ID:                   missionID,
			AgentID:              agentID,
			TeamID:               "team-1",
			Status:               domain.MissionStatusRunning,
			RequiredCapabilities: []string{"test"},
			Payload:              []byte(payload),
			Priority:             rapid.IntRange(1, 10).Draw(rt, "priority"),
			Timeout:              time.Hour,
			SubmittedAt:          now,
			AssignedAt:           &now,
		}
		if err := s.Missions().Create(ctx, mission); err != nil {
			t.Fatalf("create mission: %v", err)
		}

		// Activate kill switch.
		err := sm.ActivateKillSwitch(ctx)
		if err != nil {
			t.Fatalf("ActivateKillSwitch: %v", err)
		}

		// Verify: mission status is cancelled (marking it interrupted).
		m, err := s.Missions().Get(ctx, missionID)
		if err != nil {
			t.Fatalf("get mission: %v", err)
		}
		if m.Status != domain.MissionStatusCancelled {
			t.Errorf("expected cancelled status, got %s", m.Status)
		}

		// Verify: payload (last state) preserved.
		if string(m.Payload) != payload {
			t.Errorf("payload not preserved: got %q, want %q", string(m.Payload), payload)
		}

		// Verify: CompletedAt set (marks interruption time).
		if m.CompletedAt == nil {
			t.Error("expected CompletedAt to be set for interrupted mission")
		}

		// Verify: AssignedAt still preserved.
		if m.AssignedAt == nil {
			t.Error("expected AssignedAt to be preserved")
		}
	})
}

// TestProperty46_CircuitBreakerOpensOnErrorRate tests that >50% errors over a
// 5-minute window with at least 5 missions triggers the circuit breaker to open.
// **Validates: Requirements 10.3**
func TestProperty46_CircuitBreakerOpensOnErrorRate(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()
		storeName := fmt.Sprintf("safety46_%s", rapid.StringMatching(`[a-z]{6}`).Draw(rt, "name"))
		s := newSafetyStore(t, storeName)
		emitter := &testSafetyEmitter{}
		sm := safety.New(s, emitter, safety.DefaultConfig())

		agentID := fmt.Sprintf("agent-%s", rapid.StringMatching(`[a-z]{4}`).Draw(rt, "agentID"))
		createSafetyAgent(t, s, agentID, "CB Agent")

		// Generate total missions >= 5.
		totalMissions := rapid.IntRange(5, 20).Draw(rt, "totalMissions")

		// Generate failure count > 50% of total.
		minFailures := (totalMissions / 2) + 1
		failures := rapid.IntRange(minFailures, totalMissions).Draw(rt, "failures")
		successes := totalMissions - failures

		// Record successes first, then failures.
		for i := 0; i < successes; i++ {
			sm.RecordMissionOutcome(ctx, agentID, true)
		}
		for i := 0; i < failures; i++ {
			sm.RecordMissionOutcome(ctx, agentID, false)
		}

		// Verify circuit breaker opened.
		cb, err := sm.GetCircuitBreaker(ctx, agentID)
		if err != nil {
			t.Fatalf("GetCircuitBreaker: %v", err)
		}
		if cb.State != domain.CBOpen {
			t.Errorf("expected breaker open with %d/%d failures (%.1f%%), got state %s",
				failures, totalMissions, float64(failures)/float64(totalMissions)*100, cb.State)
		}

		// Verify event emitted.
		events := emitter.eventsByType("circuit_breaker_opened")
		if len(events) == 0 {
			t.Error("expected circuit_breaker_opened event")
		}
	})
}

// TestProperty47_OpenCircuitBreakerBlocksAllMissions tests that an agent with an open
// circuit breaker has its state reported as open via GetCircuitBreaker.
// **Validates: Requirements 10.4**
func TestProperty47_OpenCircuitBreakerBlocksAllMissions(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()
		storeName := fmt.Sprintf("safety47_%s", rapid.StringMatching(`[a-z]{6}`).Draw(rt, "name"))
		s := newSafetyStore(t, storeName)
		emitter := &testSafetyEmitter{}
		sm := safety.New(s, emitter, safety.DefaultConfig())

		agentID := fmt.Sprintf("agent-%s", rapid.StringMatching(`[a-z]{4}`).Draw(rt, "agentID"))
		createSafetyAgent(t, s, agentID, "Blocked Agent")

		// Open the breaker by recording enough failures.
		numMissions := rapid.IntRange(5, 15).Draw(rt, "numMissions")
		// All failures ensures >50%.
		for i := 0; i < numMissions; i++ {
			sm.RecordMissionOutcome(ctx, agentID, false)
		}

		// Verify breaker is open (scheduler would check this before assigning).
		cb, err := sm.GetCircuitBreaker(ctx, agentID)
		if err != nil {
			t.Fatalf("GetCircuitBreaker: %v", err)
		}
		if cb.State != domain.CBOpen {
			t.Errorf("expected open breaker for agent with 100%% failures, got %s", cb.State)
		}

		// Verify the breaker reports the agent ID.
		if cb.AgentID != agentID {
			t.Errorf("breaker agent ID mismatch: got %q, want %q", cb.AgentID, agentID)
		}

		// Verify error rate > 50%.
		if cb.ErrorRate <= 0.5 {
			t.Errorf("expected error rate > 50%%, got %.2f", cb.ErrorRate)
		}
	})
}

// TestProperty48_CircuitBreakerProbeAndRecovery tests that after cooldown: a successful
// probe closes the breaker, while a failed probe re-opens it and restarts cooldown.
// **Validates: Requirements 10.5, 10.6, 10.7**
func TestProperty48_CircuitBreakerProbeAndRecovery(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()
		storeName := fmt.Sprintf("safety48_%s", rapid.StringMatching(`[a-z]{6}`).Draw(rt, "name"))
		s := newSafetyStore(t, storeName)
		emitter := &testSafetyEmitter{}

		cooldownSecs := rapid.IntRange(10, 120).Draw(rt, "cooldownSecs")
		config := safety.DefaultConfig()
		config.FleetPolicy.CooldownSecs = cooldownSecs
		sm := safety.New(s, emitter, config)

		agentID := fmt.Sprintf("agent-%s", rapid.StringMatching(`[a-z]{4}`).Draw(rt, "agentID"))
		createSafetyAgent(t, s, agentID, "Probe Agent")

		// Open the circuit breaker.
		for i := 0; i < 6; i++ {
			sm.RecordMissionOutcome(ctx, agentID, false)
		}

		// Verify it opened.
		cb, _ := sm.GetCircuitBreaker(ctx, agentID)
		if cb.State != domain.CBOpen {
			t.Fatalf("expected open breaker, got %s", cb.State)
		}

		// Test successful probe: set cooldown in the past and provide heartbeat.
		probeSuccess := rapid.Bool().Draw(rt, "probeSuccess")

		// Manipulate cooldown end to be in the past so probe is allowed.
		sm.SetCooldownEndForTest(agentID, time.Now().Add(-1*time.Minute))

		if probeSuccess {
			// Record heartbeat so agent is reachable.
			sm.RecordHeartbeat(agentID)
		} else {
			// Make agent idle-safe so probe fails.
			agent, _ := s.Agents().Get(ctx, agentID)
			agent.Status = domain.AgentStatusIdleSafe
			s.Agents().Update(ctx, agent)
		}

		result, err := sm.ProbeAgent(ctx, agentID)
		if err != nil {
			t.Fatalf("ProbeAgent: %v", err)
		}

		if probeSuccess {
			// Probe succeeds → breaker closes.
			if !result.Success {
				t.Errorf("expected probe success, got failure: %s", result.Error)
			}
			cb, _ = sm.GetCircuitBreaker(ctx, agentID)
			if cb.State != domain.CBClosed {
				t.Errorf("expected breaker closed after successful probe, got %s", cb.State)
			}
		} else {
			// Probe fails → breaker re-opens, cooldown restarts.
			if result.Success {
				t.Error("expected probe failure for idle-safe agent")
			}
			cb, _ = sm.GetCircuitBreaker(ctx, agentID)
			if cb.State != domain.CBOpen {
				t.Errorf("expected breaker re-opened after failed probe, got %s", cb.State)
			}
			// Cooldown should be set in the future.
			if cb.CooldownEnd == nil || cb.CooldownEnd.Before(time.Now()) {
				t.Error("expected cooldown end to be in the future after re-open")
			}
		}
	})
}

// TestProperty49_SafetyPolicyInheritance tests that agent-level policy overrides
// fleet-level policy for the same parameter.
// **Validates: Requirements 10.8**
func TestProperty49_SafetyPolicyInheritance(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()
		storeName := fmt.Sprintf("safety49_%s", rapid.StringMatching(`[a-z]{6}`).Draw(rt, "name"))
		s := newSafetyStore(t, storeName)
		emitter := &testSafetyEmitter{}

		// Fleet-level: 50% threshold, min 5 missions.
		config := safety.DefaultConfig()
		sm := safety.New(s, emitter, config)

		agentID := fmt.Sprintf("agent-%s", rapid.StringMatching(`[a-z]{4}`).Draw(rt, "agentID"))
		createSafetyAgent(t, s, agentID, "Policy Agent")

		// Generate agent-level override with different threshold and min missions.
		agentThreshold := float64(rapid.IntRange(60, 90).Draw(rt, "threshold")) / 100.0
		agentMinMissions := rapid.IntRange(2, 4).Draw(rt, "minMissions")
		agentCooldown := rapid.IntRange(30, 300).Draw(rt, "cooldown")

		agentPolicy := safety.SafetyPolicy{
			CooldownSecs:        agentCooldown,
			ErrorRateThreshold:  agentThreshold,
			WindowDuration:      5 * time.Minute,
			MinMissions:         agentMinMissions,
			ConnectivityTimeout: 30 * time.Second,
		}
		sm.SetAgentPolicy(agentID, agentPolicy)

		// Record exactly agentMinMissions failures — this should trigger breaker
		// because error rate = 100% > agentThreshold, and count >= agentMinMissions.
		for i := 0; i < agentMinMissions; i++ {
			sm.RecordMissionOutcome(ctx, agentID, false)
		}

		// Verify breaker opened using agent-level policy.
		cb, err := sm.GetCircuitBreaker(ctx, agentID)
		if err != nil {
			t.Fatalf("GetCircuitBreaker: %v", err)
		}
		if cb.State != domain.CBOpen {
			t.Errorf("expected breaker open with agent policy (threshold=%.2f, minMissions=%d), got %s",
				agentThreshold, agentMinMissions, cb.State)
		}

		// Verify cooldown uses agent-level value.
		if cb.CooldownSecs != agentCooldown {
			t.Errorf("expected cooldown %d from agent policy, got %d", agentCooldown, cb.CooldownSecs)
		}

		// Now test a second agent WITHOUT override — uses fleet defaults.
		agentID2 := fmt.Sprintf("agent2-%s", rapid.StringMatching(`[a-z]{4}`).Draw(rt, "agentID2"))
		createSafetyAgent(t, s, agentID2, "Fleet Agent")

		// Record 4 failures (below fleet min of 5) — breaker should NOT open.
		for i := 0; i < 4; i++ {
			sm.RecordMissionOutcome(ctx, agentID2, false)
		}

		cb2, _ := sm.GetCircuitBreaker(ctx, agentID2)
		if cb2.State != domain.CBClosed {
			t.Errorf("expected breaker closed for fleet-default agent with <5 missions, got %s", cb2.State)
		}
	})
}

// TestProperty50_ConnectivityLossMarksUnhealthy tests that an agent with no response
// for >30s is marked unhealthy with its circuit breaker opened.
// **Validates: Requirements 10.9**
func TestProperty50_ConnectivityLossMarksUnhealthy(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()
		storeName := fmt.Sprintf("safety50_%s", rapid.StringMatching(`[a-z]{6}`).Draw(rt, "name"))
		s := newSafetyStore(t, storeName)
		emitter := &testSafetyEmitter{}

		// Use a custom connectivity timeout (random 30-60s).
		timeoutSecs := rapid.IntRange(30, 60).Draw(rt, "timeoutSecs")
		config := safety.DefaultConfig()
		config.FleetPolicy.ConnectivityTimeout = time.Duration(timeoutSecs) * time.Second
		sm := safety.New(s, emitter, config)

		agentID := fmt.Sprintf("agent-%s", rapid.StringMatching(`[a-z]{4}`).Draw(rt, "agentID"))
		createSafetyAgent(t, s, agentID, "Disconnect Agent")

		// Record an initial heartbeat in the past beyond the timeout.
		staleSecs := rapid.IntRange(timeoutSecs+1, timeoutSecs+60).Draw(rt, "staleSecs")
		sm.SetHeartbeatForTest(agentID, time.Now().Add(-time.Duration(staleSecs)*time.Second))

		// Run connectivity check.
		sm.CheckConnectivity(ctx)

		// Verify agent marked unhealthy.
		agent, err := s.Agents().Get(ctx, agentID)
		if err != nil {
			t.Fatalf("get agent: %v", err)
		}
		if agent.Status != domain.AgentStatusUnhealthy {
			t.Errorf("expected agent unhealthy after %ds without heartbeat (timeout=%ds), got %s",
				staleSecs, timeoutSecs, agent.Status)
		}

		// Verify circuit breaker opened.
		cb, err := sm.GetCircuitBreaker(ctx, agentID)
		if err != nil {
			t.Fatalf("GetCircuitBreaker: %v", err)
		}
		if cb.State != domain.CBOpen {
			t.Errorf("expected breaker open for unresponsive agent, got %s", cb.State)
		}

		// Verify event emitted.
		events := emitter.eventsByType("agent_unhealthy")
		if len(events) == 0 {
			t.Error("expected agent_unhealthy event")
		}
	})
}
