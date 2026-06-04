package reconciliation

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
)

// --- Mock implementations ---

type mockRegistry struct {
	mu      sync.Mutex
	agents  map[string]*domain.AgentEntry
	nextID  int
	listErr error
}

func newMockRegistry() *mockRegistry {
	return &mockRegistry{agents: make(map[string]*domain.AgentEntry)}
}

func (m *mockRegistry) Register(_ context.Context, manifest domain.AgentManifest) (*domain.AgentEntry, error) {
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

func (m *mockRegistry) Get(_ context.Context, id string) (*domain.AgentEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := m.agents[id]; ok {
		return a, nil
	}
	return nil, fmt.Errorf("not found")
}

func (m *mockRegistry) List(_ context.Context, filter domain.AgentFilter) ([]*domain.AgentEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listErr != nil {
		return nil, m.listErr
	}
	var result []*domain.AgentEntry
	for _, a := range m.agents {
		if filter.Name != "" && a.Name != filter.Name {
			continue
		}
		result = append(result, a)
	}
	return result, nil
}

func (m *mockRegistry) FindByCapability(_ context.Context, _ string) ([]*domain.AgentEntry, error) {
	return nil, nil
}

func (m *mockRegistry) Deregister(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.agents, id)
	return nil
}

type mockScheduler struct{}

func (m *mockScheduler) Schedule(_ context.Context, _ *domain.Mission) (*domain.Assignment, error) {
	return &domain.Assignment{}, nil
}

func (m *mockScheduler) Reschedule(_ context.Context) error {
	return nil
}

type mockPolicy struct {
	deny bool
}

func (m *mockPolicy) Evaluate(_ context.Context, _ domain.PolicyRequest) (*domain.PolicyDecision, error) {
	if m.deny {
		return &domain.PolicyDecision{Allowed: false, Reason: "denied by policy"}, nil
	}
	return &domain.PolicyDecision{Allowed: true}, nil
}

func (m *mockPolicy) ApplyPolicy(_ context.Context, _ *domain.Policy) error { return nil }
func (m *mockPolicy) GetPolicy(_ context.Context, _ string) (*domain.Policy, error) {
	return nil, nil
}

type mockLifecycle struct {
	deployErr      error
	rollbackCalled bool
	mu             sync.Mutex
	reg            *mockRegistry // optional: if set, deploy registers the agent
}

func (m *mockLifecycle) Deploy(_ context.Context, manifest domain.AgentManifest) (*domain.Deployment, error) {
	if m.deployErr != nil {
		return nil, m.deployErr
	}
	agentID := "agent-1"
	// If we have a registry, register the agent there so health checks can find it.
	if m.reg != nil {
		m.reg.mu.Lock()
		m.reg.agents[agentID] = &domain.AgentEntry{
			ID:           agentID,
			Name:         manifest.Name,
			Namespace:    manifest.Namespace,
			Version:      manifest.Version,
			RuntimeType:  manifest.RuntimeType,
			Capabilities: manifest.Capabilities,
			Status:       domain.AgentStatusActive,
		}
		m.reg.mu.Unlock()
	}
	return &domain.Deployment{
		ID:      "deploy-1",
		AgentID: agentID,
		Version: manifest.Version,
		Status:  domain.DeploymentStatusCompleted,
	}, nil
}

func (m *mockLifecycle) Rollback(_ context.Context, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rollbackCalled = true
	return nil
}

func (m *mockLifecycle) Scale(_ context.Context, _ string, _ int) error { return nil }
func (m *mockLifecycle) Deprecate(_ context.Context, _ string, _ time.Duration) error {
	return nil
}

func (m *mockLifecycle) wasRollbackCalled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rollbackCalled
}

type mockSafety struct {
	cbState *domain.CircuitBreakerState
}

func (m *mockSafety) ActivateKillSwitch(_ context.Context) error   { return nil }
func (m *mockSafety) DeactivateKillSwitch(_ context.Context) error { return nil }
func (m *mockSafety) GetCircuitBreaker(_ context.Context, _ string) (*domain.CircuitBreakerState, error) {
	return m.cbState, nil
}
func (m *mockSafety) ProbeAgent(_ context.Context, _ string) (*domain.ProbeResult, error) {
	return nil, nil
}

type mockSLO struct{}

func (m *mockSLO) EvaluateSLO(_ context.Context, _ string) (*domain.SLOStatus, error) {
	return &domain.SLOStatus{}, nil
}

func (m *mockSLO) GetCompliance(_ context.Context, _ string) (*domain.SLOCompliance, error) {
	return &domain.SLOCompliance{}, nil
}

// --- Tests ---

func validManifest() domain.AgentManifest {
	return domain.AgentManifest{
		Name:        "test-agent",
		Namespace:   "default",
		Version:     "1.0.0",
		RuntimeType: domain.RuntimeClaude,
		Capabilities: []domain.Capability{
			{Name: "code-review", Type: "mcp-tool"},
		},
		Resources: domain.ResourceLimits{
			MaxConcurrentMissions: 10,
		},
		Deployment: domain.DeploymentStrategy{
			Type:         "immediate",
			MaxInstances: 5,
		},
	}
}

func TestReconciler_ApplySync_Success(t *testing.T) {
	reg := newMockRegistry()
	lcm := &mockLifecycle{reg: reg}
	rec := New(
		reg,
		&mockScheduler{},
		&mockPolicy{},
		lcm,
		&mockSafety{},
		&mockSLO{},
		DefaultConfig(),
	)

	result, err := rec.ApplySync(context.Background(), validManifest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusConverged {
		t.Errorf("expected status %q, got %q (error: %s)", StatusConverged, result.Status, result.Error)
	}
	if result.AgentID == "" {
		t.Error("expected non-empty agent ID")
	}
	if result.Version != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %s", result.Version)
	}
}

func TestReconciler_Apply_AcknowledgesImmediately(t *testing.T) {
	reg := newMockRegistry()
	rec := New(
		reg,
		&mockScheduler{},
		&mockPolicy{},
		&mockLifecycle{reg: reg},
		&mockSafety{},
		&mockSLO{},
		DefaultConfig(),
	)

	start := time.Now()
	ackResult, resultCh, err := rec.Apply(context.Background(), validManifest())
	ackDuration := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ackResult.Status != StatusAcknowledged {
		t.Errorf("expected acknowledged, got %s", ackResult.Status)
	}
	// Must acknowledge well within 5s (practically immediate).
	if ackDuration > 1*time.Second {
		t.Errorf("acknowledge took too long: %v", ackDuration)
	}

	// Wait for convergence.
	select {
	case result := <-resultCh:
		if result.Status != StatusConverged {
			t.Errorf("expected converged, got %s: %s", result.Status, result.Error)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("convergence timed out")
	}
}

func TestReconciler_PolicyDenied_FailsReconciliation(t *testing.T) {
	reg := newMockRegistry()
	rec := New(
		reg,
		&mockScheduler{},
		&mockPolicy{deny: true},
		&mockLifecycle{reg: reg},
		&mockSafety{},
		&mockSLO{},
		DefaultConfig(),
	)

	result, err := rec.ApplySync(context.Background(), validManifest())
	if err != nil {
		// err may be nil since ApplySync doesn't return error for convergence failures
		_ = err
	}
	if result == nil {
		t.Fatal("expected result")
	}
	if result.Status != StatusRolledBack {
		t.Errorf("expected rolled_back, got %s", result.Status)
	}
	if result.Error == "" {
		t.Error("expected error message")
	}
}

func TestReconciler_DeployFailure_RollsBack(t *testing.T) {
	lcm := &mockLifecycle{deployErr: errors.New("deploy failed")}
	reg := newMockRegistry()

	// Pre-populate a previous agent for rollback.
	reg.agents["existing-1"] = &domain.AgentEntry{
		ID:        "existing-1",
		Name:      "test-agent",
		Namespace: "default",
		Version:   "0.9.0",
		Status:    domain.AgentStatusActive,
	}

	rec := New(
		reg,
		&mockScheduler{},
		&mockPolicy{},
		lcm,
		&mockSafety{},
		&mockSLO{},
		DefaultConfig(),
	)

	result, _ := rec.ApplySync(context.Background(), validManifest())
	if result.Status != StatusRolledBack {
		t.Errorf("expected rolled_back, got %s", result.Status)
	}
	if !lcm.wasRollbackCalled() {
		t.Error("expected rollback to be called")
	}
}

func TestReconciler_CircuitBreakerOpen_Fails(t *testing.T) {
	reg := newMockRegistry()
	reg.agents["existing-1"] = &domain.AgentEntry{
		ID:        "existing-1",
		Name:      "test-agent",
		Namespace: "default",
		Version:   "0.9.0",
		Status:    domain.AgentStatusActive,
	}

	safetyMesh := &mockSafety{
		cbState: &domain.CircuitBreakerState{
			AgentID: "existing-1",
			State:   domain.CBOpen,
		},
	}

	rec := New(
		reg,
		&mockScheduler{},
		&mockPolicy{},
		&mockLifecycle{},
		safetyMesh,
		&mockSLO{},
		DefaultConfig(),
	)

	result, _, err := rec.Apply(context.Background(), validManifest())
	if err == nil {
		t.Fatal("expected error for open circuit breaker")
	}
	if !errors.Is(err, ErrCircuitBreakerOpen) {
		t.Errorf("expected ErrCircuitBreakerOpen, got: %v", err)
	}
	if result.Status != StatusFailed {
		t.Errorf("expected failed, got %s", result.Status)
	}
}

func TestReconciler_ConvergenceWithin60s(t *testing.T) {
	reg := newMockRegistry()
	rec := New(
		reg,
		&mockScheduler{},
		&mockPolicy{},
		&mockLifecycle{reg: reg},
		&mockSafety{},
		&mockSLO{},
		Config{
			AcknowledgeTimeout:  5 * time.Second,
			ConvergenceTimeout:  60 * time.Second,
			HealthCheckInterval: 100 * time.Millisecond,
			HealthCheckRetries:  3,
		},
	)

	start := time.Now()
	result, err := rec.ApplySync(context.Background(), validManifest())
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusConverged {
		t.Errorf("expected converged, got %s: %s", result.Status, result.Error)
	}
	if elapsed > 60*time.Second {
		t.Errorf("convergence took too long: %v", elapsed)
	}
}

func TestReconciler_PreservesStateOnFailure(t *testing.T) {
	reg := newMockRegistry()
	originalAgent := &domain.AgentEntry{
		ID:        "existing-1",
		Name:      "test-agent",
		Namespace: "default",
		Version:   "0.9.0",
		Status:    domain.AgentStatusActive,
		RuntimeType: domain.RuntimeClaude,
	}
	reg.agents["existing-1"] = originalAgent

	lcm := &mockLifecycle{deployErr: errors.New("infra failure")}

	rec := New(
		reg,
		&mockScheduler{},
		&mockPolicy{},
		lcm,
		&mockSafety{},
		&mockSLO{},
		DefaultConfig(),
	)

	result, _ := rec.ApplySync(context.Background(), validManifest())
	if result.Status != StatusRolledBack {
		t.Errorf("expected rolled_back, got %s", result.Status)
	}

	// Verify previous agent still exists in registry (not deleted).
	agent, err := reg.Get(context.Background(), "existing-1")
	if err != nil {
		t.Fatalf("previous agent should still exist: %v", err)
	}
	if agent.Version != "0.9.0" {
		t.Errorf("previous agent version should be preserved, got %s", agent.Version)
	}
	if agent.Status != domain.AgentStatusActive {
		t.Errorf("previous agent status should be preserved, got %s", agent.Status)
	}
}
