package property

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/lifecycle"
	"github.com/agentplane/agentplane/internal/policy"
	"github.com/agentplane/agentplane/internal/reconciliation"
	"github.com/agentplane/agentplane/internal/registry"
	"github.com/agentplane/agentplane/internal/safety"
	"github.com/agentplane/agentplane/internal/scheduler"
	"github.com/agentplane/agentplane/internal/slo"
	"pgregory.net/rapid"
)

// --- Mocks for Reconciliation Property Tests ---

// recMockRegistry implements registry.AgentRegistryService for reconciliation tests.
type recMockRegistry struct {
	mu     sync.Mutex
	agents map[string]*domain.AgentEntry
	nextID int
}

func newRecMockRegistry() *recMockRegistry {
	return &recMockRegistry{agents: make(map[string]*domain.AgentEntry)}
}

func (m *recMockRegistry) Register(_ context.Context, manifest domain.AgentManifest) (*domain.AgentEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	entry := &domain.AgentEntry{
		ID:           fmt.Sprintf("agent-%d", m.nextID),
		Name:         manifest.Name,
		Namespace:    manifest.Namespace,
		Version:      manifest.Version,
		RuntimeType:  manifest.RuntimeType,
		Capabilities: manifest.Capabilities,
		Labels:       manifest.Labels,
		SLOs:         manifest.SLOs,
		Resources:    manifest.Resources,
		Deployment:   manifest.Deployment,
		Status:       domain.AgentStatusActive,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	m.agents[entry.ID] = entry
	return entry, nil
}

func (m *recMockRegistry) Get(_ context.Context, id string) (*domain.AgentEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := m.agents[id]; ok {
		return a, nil
	}
	return nil, fmt.Errorf("not found: %s", id)
}

func (m *recMockRegistry) List(_ context.Context, filter domain.AgentFilter) ([]*domain.AgentEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*domain.AgentEntry
	for _, a := range m.agents {
		if filter.Name != "" && a.Name != filter.Name {
			continue
		}
		result = append(result, a)
	}
	return result, nil
}

func (m *recMockRegistry) FindByCapability(_ context.Context, _ string) ([]*domain.AgentEntry, error) {
	return nil, nil
}

func (m *recMockRegistry) Deregister(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.agents, id)
	return nil
}

// snapshot returns a deep copy of all agents for state comparison.
func (m *recMockRegistry) snapshot() map[string]domain.AgentEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	snap := make(map[string]domain.AgentEntry, len(m.agents))
	for id, a := range m.agents {
		cp := *a
		if a.Capabilities != nil {
			cp.Capabilities = make([]domain.Capability, len(a.Capabilities))
			copy(cp.Capabilities, a.Capabilities)
		}
		if a.Labels != nil {
			cp.Labels = make(map[string]string, len(a.Labels))
			for k, v := range a.Labels {
				cp.Labels[k] = v
			}
		}
		snap[id] = cp
	}
	return snap
}

var _ registry.AgentRegistryService = (*recMockRegistry)(nil)

// recMockScheduler implements scheduler.SchedulerService.
type recMockScheduler struct{}

func (m *recMockScheduler) Schedule(_ context.Context, _ *domain.Mission) (*domain.Assignment, error) {
	return &domain.Assignment{}, nil
}

func (m *recMockScheduler) Reschedule(_ context.Context) error { return nil }

var _ scheduler.SchedulerService = (*recMockScheduler)(nil)

// recMockPolicy implements policy.PolicyEngineService.
// When deny is true, policy evaluation denies the request.
type recMockPolicy struct {
	deny    bool
	evalErr error
}

func (m *recMockPolicy) Evaluate(_ context.Context, _ domain.PolicyRequest) (*domain.PolicyDecision, error) {
	if m.evalErr != nil {
		return nil, m.evalErr
	}
	if m.deny {
		return &domain.PolicyDecision{Allowed: false, Reason: "denied by policy"}, nil
	}
	return &domain.PolicyDecision{Allowed: true}, nil
}

func (m *recMockPolicy) ApplyPolicy(_ context.Context, _ *domain.Policy) error { return nil }
func (m *recMockPolicy) GetPolicy(_ context.Context, _ string) (*domain.Policy, error) {
	return nil, nil
}

var _ policy.PolicyEngineService = (*recMockPolicy)(nil)

// recMockLifecycle implements lifecycle.LifecycleManagerService.
// deployErr controls whether Deploy fails.
type recMockLifecycle struct {
	mu             sync.Mutex
	deployErr      error
	rollbackCalled bool
	rollbackIDs    []string
}

func (m *recMockLifecycle) Deploy(_ context.Context, manifest domain.AgentManifest) (*domain.Deployment, error) {
	if m.deployErr != nil {
		return nil, m.deployErr
	}
	return &domain.Deployment{
		ID:      "deploy-1",
		AgentID: "agent-1",
		Version: manifest.Version,
		Status:  domain.DeploymentStatusCompleted,
	}, nil
}

func (m *recMockLifecycle) Rollback(_ context.Context, agentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rollbackCalled = true
	m.rollbackIDs = append(m.rollbackIDs, agentID)
	return nil
}

func (m *recMockLifecycle) Scale(_ context.Context, _ string, _ int) error { return nil }
func (m *recMockLifecycle) Deprecate(_ context.Context, _ string, _ time.Duration) error {
	return nil
}

func (m *recMockLifecycle) wasRollbackCalled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rollbackCalled
}

var _ lifecycle.LifecycleManagerService = (*recMockLifecycle)(nil)

// recMockSafety implements safety.SafetyMeshService.
type recMockSafety struct {
	cbState *domain.CircuitBreakerState
}

func (m *recMockSafety) ActivateKillSwitch(_ context.Context) error   { return nil }
func (m *recMockSafety) DeactivateKillSwitch(_ context.Context) error { return nil }
func (m *recMockSafety) GetCircuitBreaker(_ context.Context, _ string) (*domain.CircuitBreakerState, error) {
	return m.cbState, nil
}
func (m *recMockSafety) ProbeAgent(_ context.Context, _ string) (*domain.ProbeResult, error) {
	return nil, nil
}

var _ safety.SafetyMeshService = (*recMockSafety)(nil)

// recMockSLO implements slo.SLOManagerService.
type recMockSLO struct{}

func (m *recMockSLO) EvaluateSLO(_ context.Context, _ string) (*domain.SLOStatus, error) {
	return &domain.SLOStatus{}, nil
}

func (m *recMockSLO) GetCompliance(_ context.Context, _ string) (*domain.SLOCompliance, error) {
	return &domain.SLOCompliance{}, nil
}

var _ slo.SLOManagerService = (*recMockSLO)(nil)

// --- Generators ---

// genAgentStatus generates a random valid agent status.
func genAgentStatus() *rapid.Generator[domain.AgentStatus] {
	return rapid.SampledFrom([]domain.AgentStatus{
		domain.AgentStatusActive,
		domain.AgentStatusInactive,
		domain.AgentStatusDraining,
		domain.AgentStatusDeprecated,
		domain.AgentStatusUnhealthy,
		domain.AgentStatusIdleSafe,
	})
}

// genRuntimeType generates a random runtime type.
func genRuntimeType() *rapid.Generator[domain.RuntimeType] {
	return rapid.SampledFrom([]domain.RuntimeType{
		domain.RuntimeClaude,
		domain.RuntimeKiro,
		domain.RuntimeBedrock,
		domain.RuntimeGemini,
		domain.RuntimeCustom,
	})
}

// genCapability generates a random capability.
func genCapability(t *rapid.T, label string) domain.Capability {
	return domain.Capability{
		Name: rapid.StringMatching(`[a-z]{3,10}`).Draw(t, label+"-name"),
		Type: rapid.SampledFrom([]string{"mcp-tool", "a2a-message"}).Draw(t, label+"-type"),
	}
}

// genAgentEntry generates a random agent entry to populate fleet state.
func genAgentEntry(t *rapid.T, idx int) *domain.AgentEntry {
	numCaps := rapid.IntRange(1, 5).Draw(t, fmt.Sprintf("numCaps-%d", idx))
	caps := make([]domain.Capability, numCaps)
	for i := range caps {
		caps[i] = genCapability(t, fmt.Sprintf("cap-%d-%d", idx, i))
	}

	return &domain.AgentEntry{
		ID:          fmt.Sprintf("agent-%d", idx),
		Name:        rapid.StringMatching(`[a-z]{4,10}`).Draw(t, fmt.Sprintf("name-%d", idx)),
		Namespace:   rapid.SampledFrom([]string{"default", "prod", "staging"}).Draw(t, fmt.Sprintf("ns-%d", idx)),
		Version:     rapid.StringMatching(`[0-9]+\.[0-9]+\.[0-9]+`).Draw(t, fmt.Sprintf("ver-%d", idx)),
		RuntimeType: genRuntimeType().Draw(t, fmt.Sprintf("runtime-%d", idx)),
		Status:      genAgentStatus().Draw(t, fmt.Sprintf("status-%d", idx)),
		Capabilities: caps,
		Resources: domain.ResourceLimits{
			MaxConcurrentMissions: rapid.IntRange(1, 50).Draw(t, fmt.Sprintf("maxMissions-%d", idx)),
		},
		Deployment: domain.DeploymentStrategy{
			Type:         rapid.SampledFrom([]string{"rolling", "canary", "immediate"}).Draw(t, fmt.Sprintf("deployType-%d", idx)),
			MaxInstances: rapid.IntRange(1, 20).Draw(t, fmt.Sprintf("maxInst-%d", idx)),
		},
		CreatedAt: time.Now().Add(-time.Duration(rapid.IntRange(1, 720).Draw(t, fmt.Sprintf("createdHoursAgo-%d", idx))) * time.Hour),
		UpdatedAt: time.Now().Add(-time.Duration(rapid.IntRange(0, 60).Draw(t, fmt.Sprintf("updatedMinsAgo-%d", idx))) * time.Minute),
	}
}

// --- Property Test ---

// TestProperty9_FailedReconciliationPreservesFleetState verifies that for any fleet state S,
// if reconciliation of a newly applied manifest fails, the fleet state after failure is
// identical to S.
// **Validates: Requirements 2.6**
func TestProperty9_FailedReconciliationPreservesFleetState(t *testing.T) {
	t.Run("DeployFailure", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			ctx := context.Background()

			// Generate random fleet state with 1-10 agents.
			fleetSize := rapid.IntRange(1, 10).Draw(rt, "fleetSize")
			reg := newRecMockRegistry()
			for i := 0; i < fleetSize; i++ {
				agent := genAgentEntry(rt, i)
				reg.mu.Lock()
				reg.agents[agent.ID] = agent
				reg.mu.Unlock()
			}

			// Snapshot fleet state before reconciliation.
			preState := reg.snapshot()

			// Create reconciler with deploy failure.
			lcm := &recMockLifecycle{deployErr: errors.New("simulated deploy failure")}
			rec := reconciliation.New(
				reg,
				&recMockScheduler{},
				&recMockPolicy{},
				lcm,
				&recMockSafety{},
				&recMockSLO{},
				reconciliation.Config{
					AcknowledgeTimeout:  5 * time.Second,
					ConvergenceTimeout:  5 * time.Second,
					HealthCheckInterval: 50 * time.Millisecond,
					HealthCheckRetries:  2,
				},
			)

			// Pick a random existing agent to "update" via manifest.
			targetIdx := rapid.IntRange(0, fleetSize-1).Draw(rt, "targetIdx")
			targetID := fmt.Sprintf("agent-%d", targetIdx)
			targetAgent := preState[targetID]

			manifest := domain.AgentManifest{
				Name:      targetAgent.Name,
				Namespace: targetAgent.Namespace,
				Version:   rapid.StringMatching(`[0-9]+\.[0-9]+\.[0-9]+`).Draw(rt, "newVersion"),
				RuntimeType: targetAgent.RuntimeType,
				Capabilities: []domain.Capability{
					{Name: "updated-cap", Type: "mcp-tool"},
				},
				Resources: domain.ResourceLimits{MaxConcurrentMissions: 20},
				Deployment: domain.DeploymentStrategy{
					Type:         "immediate",
					MaxInstances: 5,
				},
			}

			// Attempt reconciliation — should fail.
			result, _ := rec.ApplySync(ctx, manifest)
			if result == nil {
				t.Fatal("expected non-nil result")
			}
			if result.Status != reconciliation.StatusRolledBack {
				t.Fatalf("expected rolled_back status, got %s (error: %s)", result.Status, result.Error)
			}

			// Verify fleet state is identical to pre-reconciliation state.
			postState := reg.snapshot()
			assertFleetStatesEqual(t, preState, postState)
		})
	})

	t.Run("PolicyDenial", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			ctx := context.Background()

			// Generate random fleet state.
			fleetSize := rapid.IntRange(1, 10).Draw(rt, "fleetSize")
			reg := newRecMockRegistry()
			for i := 0; i < fleetSize; i++ {
				agent := genAgentEntry(rt, i)
				reg.mu.Lock()
				reg.agents[agent.ID] = agent
				reg.mu.Unlock()
			}

			// Snapshot fleet state before reconciliation.
			preState := reg.snapshot()

			// Create reconciler with policy denial.
			rec := reconciliation.New(
				reg,
				&recMockScheduler{},
				&recMockPolicy{deny: true},
				&recMockLifecycle{},
				&recMockSafety{},
				&recMockSLO{},
				reconciliation.Config{
					AcknowledgeTimeout:  5 * time.Second,
					ConvergenceTimeout:  5 * time.Second,
					HealthCheckInterval: 50 * time.Millisecond,
					HealthCheckRetries:  2,
				},
			)

			// Pick a random existing agent to target.
			targetIdx := rapid.IntRange(0, fleetSize-1).Draw(rt, "targetIdx")
			targetID := fmt.Sprintf("agent-%d", targetIdx)
			targetAgent := preState[targetID]

			manifest := domain.AgentManifest{
				Name:      targetAgent.Name,
				Namespace: targetAgent.Namespace,
				Version:   rapid.StringMatching(`[0-9]+\.[0-9]+\.[0-9]+`).Draw(rt, "newVersion"),
				RuntimeType: targetAgent.RuntimeType,
				Capabilities: []domain.Capability{
					{Name: "new-cap", Type: "mcp-tool"},
				},
				Resources:  domain.ResourceLimits{MaxConcurrentMissions: 10},
				Deployment: domain.DeploymentStrategy{Type: "immediate", MaxInstances: 3},
			}

			// Attempt reconciliation — should fail due to policy denial.
			result, _ := rec.ApplySync(ctx, manifest)
			if result == nil {
				t.Fatal("expected non-nil result")
			}
			if result.Status != reconciliation.StatusRolledBack {
				t.Fatalf("expected rolled_back status, got %s (error: %s)", result.Status, result.Error)
			}

			// Verify fleet state preserved.
			postState := reg.snapshot()
			assertFleetStatesEqual(t, preState, postState)
		})
	})

	t.Run("PolicyEvalError", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			ctx := context.Background()

			// Generate random fleet state.
			fleetSize := rapid.IntRange(1, 8).Draw(rt, "fleetSize")
			reg := newRecMockRegistry()
			for i := 0; i < fleetSize; i++ {
				agent := genAgentEntry(rt, i)
				reg.mu.Lock()
				reg.agents[agent.ID] = agent
				reg.mu.Unlock()
			}

			preState := reg.snapshot()

			// Policy evaluation returns internal error (fail-safe deny).
			rec := reconciliation.New(
				reg,
				&recMockScheduler{},
				&recMockPolicy{evalErr: errors.New("policy engine unavailable")},
				&recMockLifecycle{},
				&recMockSafety{},
				&recMockSLO{},
				reconciliation.Config{
					AcknowledgeTimeout:  5 * time.Second,
					ConvergenceTimeout:  5 * time.Second,
					HealthCheckInterval: 50 * time.Millisecond,
					HealthCheckRetries:  2,
				},
			)

			targetIdx := rapid.IntRange(0, fleetSize-1).Draw(rt, "targetIdx")
			targetID := fmt.Sprintf("agent-%d", targetIdx)
			targetAgent := preState[targetID]

			manifest := domain.AgentManifest{
				Name:        targetAgent.Name,
				Namespace:   targetAgent.Namespace,
				Version:     rapid.StringMatching(`[0-9]+\.[0-9]+\.[0-9]+`).Draw(rt, "newVersion"),
				RuntimeType: targetAgent.RuntimeType,
				Capabilities: []domain.Capability{
					{Name: "cap", Type: "mcp-tool"},
				},
				Resources:  domain.ResourceLimits{MaxConcurrentMissions: 5},
				Deployment: domain.DeploymentStrategy{Type: "immediate", MaxInstances: 2},
			}

			result, _ := rec.ApplySync(ctx, manifest)
			if result == nil {
				t.Fatal("expected non-nil result")
			}
			// Either rolled_back or failed (pre-flight can fail before async phase).
			if result.Status != reconciliation.StatusRolledBack && result.Status != reconciliation.StatusFailed {
				t.Fatalf("expected failure status, got %s", result.Status)
			}

			postState := reg.snapshot()
			assertFleetStatesEqual(t, preState, postState)
		})
	})
}

// assertFleetStatesEqual verifies two fleet snapshots are identical.
func assertFleetStatesEqual(t *testing.T, pre, post map[string]domain.AgentEntry) {
	t.Helper()

	if len(pre) != len(post) {
		t.Errorf("fleet size changed: before=%d, after=%d", len(pre), len(post))
		return
	}

	for id, preAgent := range pre {
		postAgent, exists := post[id]
		if !exists {
			t.Errorf("agent %s disappeared after failed reconciliation", id)
			continue
		}

		if preAgent.ID != postAgent.ID {
			t.Errorf("agent %s: ID changed %q -> %q", id, preAgent.ID, postAgent.ID)
		}
		if preAgent.Name != postAgent.Name {
			t.Errorf("agent %s: Name changed %q -> %q", id, preAgent.Name, postAgent.Name)
		}
		if preAgent.Namespace != postAgent.Namespace {
			t.Errorf("agent %s: Namespace changed %q -> %q", id, preAgent.Namespace, postAgent.Namespace)
		}
		if preAgent.Version != postAgent.Version {
			t.Errorf("agent %s: Version changed %q -> %q", id, preAgent.Version, postAgent.Version)
		}
		if preAgent.RuntimeType != postAgent.RuntimeType {
			t.Errorf("agent %s: RuntimeType changed %q -> %q", id, preAgent.RuntimeType, postAgent.RuntimeType)
		}
		if preAgent.Status != postAgent.Status {
			t.Errorf("agent %s: Status changed %q -> %q", id, preAgent.Status, postAgent.Status)
		}
		if len(preAgent.Capabilities) != len(postAgent.Capabilities) {
			t.Errorf("agent %s: Capabilities count changed %d -> %d", id, len(preAgent.Capabilities), len(postAgent.Capabilities))
		} else {
			for i, cap := range preAgent.Capabilities {
				if cap.Name != postAgent.Capabilities[i].Name || cap.Type != postAgent.Capabilities[i].Type {
					t.Errorf("agent %s: Capability[%d] changed %v -> %v", id, i, cap, postAgent.Capabilities[i])
				}
			}
		}
		if preAgent.Resources.MaxConcurrentMissions != postAgent.Resources.MaxConcurrentMissions {
			t.Errorf("agent %s: Resources.MaxConcurrentMissions changed %d -> %d",
				id, preAgent.Resources.MaxConcurrentMissions, postAgent.Resources.MaxConcurrentMissions)
		}
		if preAgent.Deployment.Type != postAgent.Deployment.Type {
			t.Errorf("agent %s: Deployment.Type changed %q -> %q", id, preAgent.Deployment.Type, postAgent.Deployment.Type)
		}
		if preAgent.Deployment.MaxInstances != postAgent.Deployment.MaxInstances {
			t.Errorf("agent %s: Deployment.MaxInstances changed %d -> %d",
				id, preAgent.Deployment.MaxInstances, postAgent.Deployment.MaxInstances)
		}
	}

	// Check no new agents were added.
	for id := range post {
		if _, exists := pre[id]; !exists {
			t.Errorf("new agent %s appeared after failed reconciliation", id)
		}
	}
}
