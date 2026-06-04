package property

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/lifecycle"
	"github.com/agentplane/agentplane/internal/registry"
	"github.com/agentplane/agentplane/internal/store"
	"pgregory.net/rapid"
)

// --- Mocks for Lifecycle Property Tests ---

type lcMockEmitter struct {
	mu     sync.Mutex
	events []domain.SystemEvent
}

func (e *lcMockEmitter) Emit(event domain.SystemEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, event)
}

func (e *lcMockEmitter) Events() []domain.SystemEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	cp := make([]domain.SystemEvent, len(e.events))
	copy(cp, e.events)
	return cp
}

// lcMockRegistry implements registry.AgentRegistryService for lifecycle tests.
type lcMockRegistry struct {
	mu     sync.Mutex
	agents map[string]*domain.AgentEntry
	regErr error // If set, Register returns this error.
}

func newLCMockRegistry() *lcMockRegistry {
	return &lcMockRegistry{agents: make(map[string]*domain.AgentEntry)}
}

func (r *lcMockRegistry) Register(_ context.Context, manifest domain.AgentManifest) (*domain.AgentEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.regErr != nil {
		return nil, r.regErr
	}
	entry := &domain.AgentEntry{
		ID:           "agent-" + manifest.Name,
		Name:         manifest.Name,
		Namespace:    manifest.Namespace,
		Version:      manifest.Version,
		RuntimeType:  manifest.RuntimeType,
		Capabilities: manifest.Capabilities,
		Deployment:   manifest.Deployment,
		Status:       domain.AgentStatusActive,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	r.agents[entry.ID] = entry
	return entry, nil
}

func (r *lcMockRegistry) Get(_ context.Context, id string) (*domain.AgentEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	agent, ok := r.agents[id]
	if !ok {
		return nil, nil
	}
	return agent, nil
}

func (r *lcMockRegistry) List(_ context.Context, _ domain.AgentFilter) ([]*domain.AgentEntry, error) {
	return nil, nil
}

func (r *lcMockRegistry) FindByCapability(_ context.Context, _ string) ([]*domain.AgentEntry, error) {
	return nil, nil
}

func (r *lcMockRegistry) Deregister(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.agents, id)
	return nil
}

// Compile-time check.
var _ registry.AgentRegistryService = (*lcMockRegistry)(nil)

// lcMockAgentStore is a minimal in-memory agent store for lifecycle tests.
type lcMockAgentStore struct {
	mu       sync.Mutex
	agents   map[string]*domain.AgentEntry
	versions map[string][]*domain.AgentVersion
}

func newLCMockAgentStore() *lcMockAgentStore {
	return &lcMockAgentStore{
		agents:   make(map[string]*domain.AgentEntry),
		versions: make(map[string][]*domain.AgentVersion),
	}
}

func (s *lcMockAgentStore) Create(_ context.Context, agent *domain.AgentEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents[agent.ID] = agent
	return nil
}

func (s *lcMockAgentStore) Get(_ context.Context, id string) (*domain.AgentEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agents[id], nil
}

func (s *lcMockAgentStore) GetByName(_ context.Context, name, ns string) (*domain.AgentEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.agents {
		if a.Name == name && a.Namespace == ns {
			return a, nil
		}
	}
	return nil, nil
}

func (s *lcMockAgentStore) List(_ context.Context, _ domain.AgentFilter) ([]*domain.AgentEntry, error) {
	return nil, nil
}

func (s *lcMockAgentStore) Update(_ context.Context, agent *domain.AgentEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents[agent.ID] = agent
	return nil
}

func (s *lcMockAgentStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.agents, id)
	return nil
}

func (s *lcMockAgentStore) QueryByCapability(_ context.Context, _ string) ([]*domain.AgentEntry, error) {
	return nil, nil
}

func (s *lcMockAgentStore) AddVersion(_ context.Context, v *domain.AgentVersion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.versions[v.AgentID] = append([]*domain.AgentVersion{v}, s.versions[v.AgentID]...)
	return nil
}

func (s *lcMockAgentStore) ListVersions(_ context.Context, agentID string) ([]*domain.AgentVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.versions[agentID], nil
}

func (s *lcMockAgentStore) DeleteVersion(_ context.Context, _ string) error {
	return nil
}

// lcMockMissionStore is a minimal mission store for lifecycle tests.
type lcMockMissionStore struct {
	mu       sync.Mutex
	missions []*domain.Mission
}

func (s *lcMockMissionStore) Create(_ context.Context, m *domain.Mission) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.missions = append(s.missions, m)
	return nil
}

func (s *lcMockMissionStore) Get(_ context.Context, _ string) (*domain.Mission, error) {
	return nil, nil
}

func (s *lcMockMissionStore) List(_ context.Context, f domain.MissionFilter) ([]*domain.Mission, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []*domain.Mission
	for _, m := range s.missions {
		if f.AgentID != "" && m.AgentID != f.AgentID {
			continue
		}
		if f.Status != "" && m.Status != f.Status {
			continue
		}
		result = append(result, m)
	}
	return result, nil
}

func (s *lcMockMissionStore) Update(_ context.Context, _ *domain.Mission) error {
	return nil
}

func (s *lcMockMissionStore) GetPending(_ context.Context) ([]*domain.Mission, error) {
	return nil, nil
}

// lcMockStore aggregates the mock stores.
type lcMockStore struct {
	agentStore   *lcMockAgentStore
	missionStore *lcMockMissionStore
}

func newLCMockStore() *lcMockStore {
	return &lcMockStore{
		agentStore:   newLCMockAgentStore(),
		missionStore: &lcMockMissionStore{},
	}
}

func (s *lcMockStore) Agents() store.AgentStore    { return s.agentStore }
func (s *lcMockStore) Missions() store.MissionStore { return s.missionStore }
func (s *lcMockStore) Policies() store.PolicyStore  { return nil }
func (s *lcMockStore) Costs() store.CostStore       { return nil }
func (s *lcMockStore) Traces() store.TraceStore     { return nil }
func (s *lcMockStore) Budgets() store.BudgetStore   { return nil }
func (s *lcMockStore) Messages() store.MessageStore { return nil }
func (s *lcMockStore) Migrate(_ context.Context) error { return nil }
func (s *lcMockStore) Close() error                    { return nil }

// --- Property Tests ---

// TestProperty12_CanaryTrafficDistribution verifies that for a canary deployment with
// configured percentage P, approximately P% (±5%) of requests are routed to the new version
// over a large sample.
// **Validates: Requirements 5.3**
func TestProperty12_CanaryTrafficDistribution(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a canary percentage in valid range [1, 50].
		pct := rapid.IntRange(1, 50).Draw(rt, "canaryPercent")

		canary := &lifecycle.CanaryState{
			DeploymentID:   "dep-1",
			AgentID:        "agent-test",
			NewVersion:     "2.0.0",
			StableVersion:  "1.0.0",
			TrafficPercent: pct,
			Active:         true,
		}

		// Route a large sample of requests.
		const sampleSize = 10000
		newVersionCount := 0
		for i := 0; i < sampleSize; i++ {
			if canary.RouteToNew(i) {
				newVersionCount++
			}
		}

		// Actual percentage routed to new version.
		actualPct := float64(newVersionCount) / float64(sampleSize) * 100.0
		expectedPct := float64(pct)

		// Allow ±5% tolerance.
		if math.Abs(actualPct-expectedPct) > 5.0 {
			t.Errorf("canary traffic distribution: configured %d%%, actual %.1f%% (tolerance ±5%%)",
				pct, actualPct)
		}
	})
}

// TestProperty21_FailedDeploymentPreservesRunningVersion verifies that when deployment
// fails at any stage, the previous version remains active and unchanged.
// **Validates: Requirements 5.2**
func TestProperty21_FailedDeploymentPreservesRunningVersion(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()

		ms := newLCMockStore()
		reg := newLCMockRegistry()
		emitter := &lcMockEmitter{}

		// Set up existing agent with a running version.
		existingVersion := rapid.StringMatching(`[0-9]+\.[0-9]+\.[0-9]+`).Draw(rt, "existingVersion")
		agentName := rapid.StringMatching(`[a-z]{4,10}`).Draw(rt, "agentName")
		agentID := "agent-" + agentName

		existingAgent := &domain.AgentEntry{
			ID:          agentID,
			Name:        agentName,
			Namespace:   "default",
			Version:     existingVersion,
			RuntimeType: domain.RuntimeClaude,
			Status:      domain.AgentStatusActive,
			Capabilities: []domain.Capability{
				{Name: "test", Type: "mcp-tool"},
			},
			Deployment: domain.DeploymentStrategy{
				Type:         "immediate",
				MaxInstances: 10,
			},
			CreatedAt: time.Now().Add(-24 * time.Hour),
			UpdatedAt: time.Now().Add(-1 * time.Hour),
		}

		// Store existing agent in both mock store and mock registry.
		ms.agentStore.agents[agentID] = existingAgent
		reg.agents[agentID] = existingAgent

		// Force registration failure to simulate deployment failure.
		reg.regErr = errors.New("simulated deployment failure")

		mgr := lifecycle.NewManager(ms, reg, emitter, lifecycle.DefaultManagerConfig())

		// Attempt to deploy a new version — should fail.
		newVersion := rapid.StringMatching(`[0-9]+\.[0-9]+\.[0-9]+`).Draw(rt, "newVersion")
		manifest := domain.AgentManifest{
			Name:    agentName,
			Version: newVersion,
			Deployment: domain.DeploymentStrategy{
				Type:         "immediate",
				MaxInstances: 10,
			},
			RuntimeType:  domain.RuntimeClaude,
			Capabilities: []domain.Capability{{Name: "test", Type: "mcp-tool"}},
		}

		_, err := mgr.Deploy(ctx, manifest)
		if err == nil {
			t.Fatal("expected deployment to fail")
		}

		// Verify: existing agent version is unchanged.
		storedAgent := ms.agentStore.agents[agentID]
		if storedAgent == nil {
			t.Fatal("existing agent should still be in store")
		}
		if storedAgent.Version != existingVersion {
			t.Errorf("previous version changed: want %s, got %s", existingVersion, storedAgent.Version)
		}
		if storedAgent.Status != domain.AgentStatusActive {
			t.Errorf("previous agent status changed: want active, got %s", storedAgent.Status)
		}

		// Verify: deployment_failed event emitted.
		events := emitter.Events()
		foundDeployFailed := false
		for _, e := range events {
			if e.Type == "deployment_failed" {
				foundDeployFailed = true
			}
		}
		if !foundDeployFailed {
			t.Error("expected deployment_failed event")
		}
	})
}

// TestProperty22_CanaryTrafficPercentageBounds verifies that canary percentage P is
// within [1, 50] and that routing matches the configured percentage.
// **Validates: Requirements 5.3**
func TestProperty22_CanaryTrafficPercentageBounds(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()

		ms := newLCMockStore()
		reg := newLCMockRegistry()
		emitter := &lcMockEmitter{}
		mgr := lifecycle.NewManager(ms, reg, emitter, lifecycle.DefaultManagerConfig())

		// Test valid canary percentages [1, 50].
		pct := rapid.IntRange(1, 50).Draw(rt, "canaryPercent")
		agentName := rapid.StringMatching(`[a-z]{4,8}`).Draw(rt, "agentName")

		manifest := domain.AgentManifest{
			Name:    agentName,
			Version: "2.0.0",
			Deployment: domain.DeploymentStrategy{
				Type:          "canary",
				CanaryPercent: pct,
				MaxInstances:  10,
			},
			RuntimeType:  domain.RuntimeClaude,
			Capabilities: []domain.Capability{{Name: "deploy", Type: "mcp-tool"}},
		}

		dep, err := mgr.Deploy(ctx, manifest)
		if err != nil {
			t.Fatalf("deploy failed: %v", err)
		}

		// Verify: deployment accepted the canary percentage.
		if dep.CanaryPercent != pct {
			t.Errorf("deployment canary percent: want %d, got %d", pct, dep.CanaryPercent)
		}

		// Verify: canary state reflects correct traffic percentage.
		canary := mgr.GetCanaryState(dep.AgentID)
		if canary == nil {
			t.Fatal("expected canary state to exist")
		}
		if canary.TrafficPercent != pct {
			t.Errorf("canary traffic percent: want %d, got %d", pct, canary.TrafficPercent)
		}

		// Verify routing matches over 100 requests (deterministic counter-based routing).
		newCount := 0
		for i := 0; i < 100; i++ {
			if canary.RouteToNew(i) {
				newCount++
			}
		}
		if newCount != pct {
			t.Errorf("routing mismatch: configured %d%%, routed %d/100 to new", pct, newCount)
		}
	})

	// Also verify rejection of invalid percentages.
	t.Run("InvalidPercentRejected", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			ctx := context.Background()

			ms := newLCMockStore()
			reg := newLCMockRegistry()
			emitter := &lcMockEmitter{}
			mgr := lifecycle.NewManager(ms, reg, emitter, lifecycle.DefaultManagerConfig())

			// Generate invalid canary percent (outside [1, 50]).
			invalidPct := rapid.OneOf(
				rapid.IntRange(-100, 0),
				rapid.IntRange(51, 200),
			).Draw(rt, "invalidPct")

			manifest := domain.AgentManifest{
				Name:    "bad-canary",
				Version: "1.0.0",
				Deployment: domain.DeploymentStrategy{
					Type:          "canary",
					CanaryPercent: invalidPct,
					MaxInstances:  10,
				},
				RuntimeType:  domain.RuntimeClaude,
				Capabilities: []domain.Capability{{Name: "x", Type: "mcp-tool"}},
			}

			_, err := mgr.Deploy(ctx, manifest)
			if err == nil {
				t.Errorf("expected rejection for canary percent %d, got nil error", invalidPct)
			}
		})
	})
}

// TestProperty23_CanaryAutoRollbackOnThresholdBreach verifies that when error rate >5%
// or p99 latency >3× baseline over 60s window, auto-rollback is triggered.
// **Validates: Requirements 5.4**
func TestProperty23_CanaryAutoRollbackOnThresholdBreach(t *testing.T) {
	t.Run("ErrorRateExceeded", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate an error rate above 5%.
			errorPct := rapid.IntRange(6, 50).Draw(rt, "errorPct")
			totalRequests := rapid.IntRange(20, 200).Draw(rt, "totalRequests")
			errorCount := (totalRequests * errorPct) / 100
			if errorCount <= totalRequests/20 {
				errorCount = totalRequests/20 + 1 // Ensure > 5%.
			}

			metrics := &lifecycle.CanaryMetrics{}
			// Set evaluation start to >60s ago so the window check passes.
			metrics.SetEvaluationStart(time.Now().Add(-61 * time.Second))

			// Record requests: errors first, then successes.
			for i := 0; i < errorCount; i++ {
				metrics.RecordRequest(10*time.Millisecond, true)
			}
			for i := 0; i < totalRequests-errorCount; i++ {
				metrics.RecordRequest(10*time.Millisecond, false)
			}

			// Verify rollback is triggered.
			if !metrics.ShouldRollback() {
				actualRate := metrics.ErrorRate()
				t.Errorf("expected rollback: error rate %.2f%% (>5%%) with %d total requests",
					actualRate*100, totalRequests)
			}
		})
	})

	t.Run("P99LatencyExceeded", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate a baseline and latency where p99 > 3× baseline.
			baselineMs := rapid.IntRange(50, 500).Draw(rt, "baselineMs")
			baseline := time.Duration(baselineMs) * time.Millisecond
			// Multiplier > 3.
			multiplier := rapid.Float64Range(3.1, 10.0).Draw(rt, "multiplier")
			highLatency := time.Duration(float64(baseline) * multiplier)

			metrics := &lifecycle.CanaryMetrics{}
			metrics.SetEvaluationStart(time.Now().Add(-61 * time.Second))
			metrics.SetBaselineP99(baseline)

			// Record requests all with high latency (no errors).
			numRequests := rapid.IntRange(20, 100).Draw(rt, "numRequests")
			for i := 0; i < numRequests; i++ {
				metrics.RecordRequest(highLatency, false)
			}

			// Verify rollback is triggered.
			if !metrics.ShouldRollback() {
				t.Errorf("expected rollback: p99=%v > 3×baseline=%v",
					metrics.P99Latency(), baseline*3)
			}
		})
	})

	t.Run("WithinThresholds_NoRollback", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate metrics well within thresholds.
			baselineMs := rapid.IntRange(50, 500).Draw(rt, "baselineMs")
			baseline := time.Duration(baselineMs) * time.Millisecond

			// Latency multiplier < 3.
			multiplier := rapid.Float64Range(0.5, 2.9).Draw(rt, "multiplier")
			normalLatency := time.Duration(float64(baseline) * multiplier)

			metrics := &lifecycle.CanaryMetrics{}
			metrics.SetEvaluationStart(time.Now().Add(-61 * time.Second))
			metrics.SetBaselineP99(baseline)

			// Record requests with low error rate (< 5%) and normal latency.
			totalRequests := rapid.IntRange(20, 200).Draw(rt, "totalRequests")
			errorCount := totalRequests / 25 // 4% errors (below 5% threshold).

			for i := 0; i < errorCount; i++ {
				metrics.RecordRequest(normalLatency, true)
			}
			for i := 0; i < totalRequests-errorCount; i++ {
				metrics.RecordRequest(normalLatency, false)
			}

			// Verify NO rollback.
			if metrics.ShouldRollback() {
				t.Errorf("unexpected rollback: error rate=%.2f%%, p99=%v, baseline=%v, 3×baseline=%v",
					metrics.ErrorRate()*100, metrics.P99Latency(), baseline, baseline*3)
			}
		})
	})
}

// TestProperty24_AutoScalingOnQueueDepth verifies that when queue depth exceeds threshold,
// instances scale up but never exceed the configured maximum.
// **Validates: Requirements 5.6**
func TestProperty24_AutoScalingOnQueueDepth(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()

		ms := newLCMockStore()
		reg := newLCMockRegistry()
		emitter := &lcMockEmitter{}
		mgr := lifecycle.NewManager(ms, reg, emitter, lifecycle.DefaultManagerConfig())

		// Generate agent parameters.
		maxInstances := rapid.IntRange(2, 100).Draw(rt, "maxInstances")
		startInstances := rapid.IntRange(1, maxInstances).Draw(rt, "startInstances")
		threshold := rapid.IntRange(1, 50).Draw(rt, "threshold")
		queueDepth := rapid.IntRange(threshold+1, threshold+100).Draw(rt, "queueDepth")

		agentName := rapid.StringMatching(`[a-z]{4,8}`).Draw(rt, "agentName")
		agentID := "agent-" + agentName

		// Set up agent in registry.
		agent := &domain.AgentEntry{
			ID:          agentID,
			Name:        agentName,
			Namespace:   "default",
			Version:     "1.0.0",
			RuntimeType: domain.RuntimeClaude,
			Status:      domain.AgentStatusActive,
			Deployment: domain.DeploymentStrategy{
				Type:         "rolling",
				MaxInstances: maxInstances,
			},
			Capabilities: []domain.Capability{{Name: "work", Type: "mcp-tool"}},
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}
		reg.mu.Lock()
		reg.agents[agentID] = agent
		reg.mu.Unlock()

		// Set initial instance count via Scale.
		if err := mgr.Scale(ctx, agentID, startInstances); err != nil {
			t.Fatalf("initial scale: %v", err)
		}

		// Call ScaleOnQueueDepth — queue exceeds threshold → should scale up.
		err := mgr.ScaleOnQueueDepth(ctx, agentID, queueDepth, threshold)
		if err != nil {
			t.Fatalf("ScaleOnQueueDepth: %v", err)
		}

		// Get resulting instance count.
		instances := mgr.GetInstances(agentID)

		// Property 1: Queue exceeded threshold → scale up (instances > startInstances),
		//   unless already at max.
		if startInstances < maxInstances {
			if instances <= startInstances {
				t.Errorf("expected scale up from %d, got %d (queue=%d > threshold=%d)",
					startInstances, instances, queueDepth, threshold)
			}
		}

		// Property 2: Never exceed max instances.
		if instances > maxInstances {
			t.Errorf("instances %d exceeds max %d", instances, maxInstances)
		}
	})

	t.Run("BelowThreshold_NoScale", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			ctx := context.Background()

			ms := newLCMockStore()
			reg := newLCMockRegistry()
			emitter := &lcMockEmitter{}
			mgr := lifecycle.NewManager(ms, reg, emitter, lifecycle.DefaultManagerConfig())

			maxInstances := rapid.IntRange(5, 50).Draw(rt, "maxInstances")
			startInstances := rapid.IntRange(1, maxInstances).Draw(rt, "startInstances")
			threshold := rapid.IntRange(10, 100).Draw(rt, "threshold")
			// Queue depth at or below threshold.
			queueDepth := rapid.IntRange(0, threshold).Draw(rt, "queueDepth")

			agentName := rapid.StringMatching(`[a-z]{4,8}`).Draw(rt, "agentName")
			agentID := "agent-" + agentName

			agent := &domain.AgentEntry{
				ID:          agentID,
				Name:        agentName,
				Namespace:   "default",
				Version:     "1.0.0",
				RuntimeType: domain.RuntimeClaude,
				Status:      domain.AgentStatusActive,
				Deployment: domain.DeploymentStrategy{
					Type:         "rolling",
					MaxInstances: maxInstances,
				},
				Capabilities: []domain.Capability{{Name: "work", Type: "mcp-tool"}},
				CreatedAt:    time.Now(),
				UpdatedAt:    time.Now(),
			}
			reg.mu.Lock()
			reg.agents[agentID] = agent
			reg.mu.Unlock()

			if err := mgr.Scale(ctx, agentID, startInstances); err != nil {
				t.Fatalf("initial scale: %v", err)
			}

			err := mgr.ScaleOnQueueDepth(ctx, agentID, queueDepth, threshold)
			if err != nil {
				t.Fatalf("ScaleOnQueueDepth: %v", err)
			}

			// Should NOT scale when below threshold.
			instances := mgr.GetInstances(agentID)
			if instances != startInstances {
				t.Errorf("expected no scaling (queue %d <= threshold %d): instances changed from %d to %d",
					queueDepth, threshold, startInstances, instances)
			}
		})
	})
}
