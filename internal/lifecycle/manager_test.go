package lifecycle

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/store"
)

// --- Mock EventEmitter ---

type mockEmitter struct {
	mu     sync.Mutex
	events []domain.SystemEvent
}

func (e *mockEmitter) Emit(event domain.SystemEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, event)
}

func (e *mockEmitter) Events() []domain.SystemEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	cp := make([]domain.SystemEvent, len(e.events))
	copy(cp, e.events)
	return cp
}

// --- Mock Registry ---

type mockRegistry struct {
	mu     sync.Mutex
	agents map[string]*domain.AgentEntry
	regErr error
}

func newMockRegistry() *mockRegistry {
	return &mockRegistry{agents: make(map[string]*domain.AgentEntry)}
}

func (r *mockRegistry) Register(_ context.Context, manifest domain.AgentManifest) (*domain.AgentEntry, error) {
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

func (r *mockRegistry) Get(_ context.Context, id string) (*domain.AgentEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	agent, ok := r.agents[id]
	if !ok {
		return nil, nil
	}
	return agent, nil
}

func (r *mockRegistry) List(_ context.Context, _ domain.AgentFilter) ([]*domain.AgentEntry, error) {
	return nil, nil
}

func (r *mockRegistry) FindByCapability(_ context.Context, _ string) ([]*domain.AgentEntry, error) {
	return nil, nil
}

func (r *mockRegistry) Deregister(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.agents, id)
	return nil
}

// --- Mock Store ---

type mockStore struct {
	agentStore   *mockAgentStore
	missionStore *mockMissionStore
}

func newMockStore() *mockStore {
	return &mockStore{
		agentStore:   newMockAgentStore(),
		missionStore: &mockMissionStore{},
	}
}

func (s *mockStore) Agents() store.AgentStore     { return s.agentStore }
func (s *mockStore) Missions() store.MissionStore  { return s.missionStore }
func (s *mockStore) Policies() store.PolicyStore   { return nil }
func (s *mockStore) Costs() store.CostStore        { return nil }
func (s *mockStore) Traces() store.TraceStore      { return nil }
func (s *mockStore) Budgets() store.BudgetStore    { return nil }
func (s *mockStore) Messages() store.MessageStore  { return nil }
func (s *mockStore) Migrate(_ context.Context) error { return nil }
func (s *mockStore) Close() error                    { return nil }

// --- Mock AgentStore ---

type mockAgentStore struct {
	mu       sync.Mutex
	agents   map[string]*domain.AgentEntry
	versions map[string][]*domain.AgentVersion
}

func newMockAgentStore() *mockAgentStore {
	return &mockAgentStore{
		agents:   make(map[string]*domain.AgentEntry),
		versions: make(map[string][]*domain.AgentVersion),
	}
}

func (s *mockAgentStore) Create(_ context.Context, agent *domain.AgentEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents[agent.ID] = agent
	return nil
}

func (s *mockAgentStore) Get(_ context.Context, id string) (*domain.AgentEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agents[id], nil
}

func (s *mockAgentStore) GetByName(_ context.Context, name, ns string) (*domain.AgentEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.agents {
		if a.Name == name && a.Namespace == ns {
			return a, nil
		}
	}
	return nil, nil
}

func (s *mockAgentStore) List(_ context.Context, _ domain.AgentFilter) ([]*domain.AgentEntry, error) {
	return nil, nil
}

func (s *mockAgentStore) Update(_ context.Context, agent *domain.AgentEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents[agent.ID] = agent
	return nil
}

func (s *mockAgentStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.agents, id)
	return nil
}

func (s *mockAgentStore) QueryByCapability(_ context.Context, _ string) ([]*domain.AgentEntry, error) {
	return nil, nil
}

func (s *mockAgentStore) AddVersion(_ context.Context, v *domain.AgentVersion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.versions[v.AgentID] = append([]*domain.AgentVersion{v}, s.versions[v.AgentID]...)
	return nil
}

func (s *mockAgentStore) ListVersions(_ context.Context, agentID string) ([]*domain.AgentVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.versions[agentID], nil
}

func (s *mockAgentStore) DeleteVersion(_ context.Context, _ string) error {
	return nil
}

// --- Mock MissionStore ---

type mockMissionStore struct {
	mu       sync.Mutex
	missions []*domain.Mission
}

func (s *mockMissionStore) Create(_ context.Context, m *domain.Mission) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.missions = append(s.missions, m)
	return nil
}

func (s *mockMissionStore) Get(_ context.Context, _ string) (*domain.Mission, error) {
	return nil, nil
}

func (s *mockMissionStore) List(_ context.Context, f domain.MissionFilter) ([]*domain.Mission, error) {
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

func (s *mockMissionStore) Update(_ context.Context, _ *domain.Mission) error {
	return nil
}

func (s *mockMissionStore) GetPending(_ context.Context) ([]*domain.Mission, error) {
	return nil, nil
}

// --- Tests ---

func TestDeploy_Success_Immediate(t *testing.T) {
	reg := newMockRegistry()
	emitter := &mockEmitter{}
	ms := newMockStore()
	mgr := NewManager(ms, reg, emitter, DefaultManagerConfig())

	manifest := domain.AgentManifest{
		Name:    "test-agent",
		Version: "1.0.0",
		Deployment: domain.DeploymentStrategy{
			Type:         "immediate",
			MaxInstances: 10,
		},
		RuntimeType:  domain.RuntimeClaude,
		Capabilities: []domain.Capability{{Name: "test", Type: "mcp-tool"}},
	}

	dep, err := mgr.Deploy(context.Background(), manifest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dep == nil {
		t.Fatal("expected deployment, got nil")
	}
	if dep.Status != domain.DeploymentStatusCompleted {
		t.Errorf("expected completed, got %s", dep.Status)
	}
	if dep.AgentID == "" {
		t.Error("expected non-empty agent ID")
	}
}

func TestDeploy_Canary_Success(t *testing.T) {
	reg := newMockRegistry()
	emitter := &mockEmitter{}
	ms := newMockStore()
	mgr := NewManager(ms, reg, emitter, DefaultManagerConfig())

	manifest := domain.AgentManifest{
		Name:    "canary-agent",
		Version: "2.0.0",
		Deployment: domain.DeploymentStrategy{
			Type:          "canary",
			CanaryPercent: 10,
			MaxInstances:  5,
		},
		RuntimeType:  domain.RuntimeKiro,
		Capabilities: []domain.Capability{{Name: "deploy", Type: "a2a-message"}},
	}

	dep, err := mgr.Deploy(context.Background(), manifest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dep.Status != domain.DeploymentStatusRunning {
		t.Errorf("expected running (canary in progress), got %s", dep.Status)
	}
	if dep.CanaryPercent != 10 {
		t.Errorf("expected canary percent 10, got %d", dep.CanaryPercent)
	}

	canary := mgr.GetCanaryState(dep.AgentID)
	if canary == nil {
		t.Fatal("expected canary state")
	}
	if !canary.Active {
		t.Error("expected canary to be active")
	}
	if canary.TrafficPercent != 10 {
		t.Errorf("expected 10%% traffic, got %d%%", canary.TrafficPercent)
	}
}

func TestDeploy_InvalidCanaryPercent(t *testing.T) {
	reg := newMockRegistry()
	emitter := &mockEmitter{}
	ms := newMockStore()
	mgr := NewManager(ms, reg, emitter, DefaultManagerConfig())

	cases := []int{0, -1, 51, 100}
	for _, pct := range cases {
		manifest := domain.AgentManifest{
			Name:    "bad-canary",
			Version: "1.0.0",
			Deployment: domain.DeploymentStrategy{
				Type:          "canary",
				CanaryPercent: pct,
			},
			RuntimeType:  domain.RuntimeClaude,
			Capabilities: []domain.Capability{{Name: "x", Type: "mcp-tool"}},
		}

		_, err := mgr.Deploy(context.Background(), manifest)
		if !errors.Is(err, ErrInvalidCanaryPercent) {
			t.Errorf("canary percent %d: expected ErrInvalidCanaryPercent, got %v", pct, err)
		}
	}

	events := emitter.Events()
	deployFailedCount := 0
	for _, e := range events {
		if e.Type == "deployment_failed" {
			deployFailedCount++
		}
	}
	if deployFailedCount != len(cases) {
		t.Errorf("expected %d deployment_failed events, got %d", len(cases), deployFailedCount)
	}
}

func TestDeploy_RegistrationFailure(t *testing.T) {
	reg := newMockRegistry()
	reg.regErr = errors.New("registry unavailable")
	emitter := &mockEmitter{}
	ms := newMockStore()
	mgr := NewManager(ms, reg, emitter, DefaultManagerConfig())

	manifest := domain.AgentManifest{
		Name:    "fail-agent",
		Version: "1.0.0",
		Deployment: domain.DeploymentStrategy{
			Type:         "immediate",
			MaxInstances: 5,
		},
		RuntimeType:  domain.RuntimeClaude,
		Capabilities: []domain.Capability{{Name: "x", Type: "mcp-tool"}},
	}

	_, err := mgr.Deploy(context.Background(), manifest)
	if err == nil {
		t.Fatal("expected error on failed registration")
	}

	events := emitter.Events()
	found := false
	for _, e := range events {
		if e.Type == "deployment_failed" {
			found = true
		}
	}
	if !found {
		t.Error("expected deployment_failed event")
	}
}

func TestScale_ValidRange(t *testing.T) {
	reg := newMockRegistry()
	emitter := &mockEmitter{}
	ms := newMockStore()
	mgr := NewManager(ms, reg, emitter, DefaultManagerConfig())

	reg.mu.Lock()
	reg.agents["agent-1"] = &domain.AgentEntry{
		ID:         "agent-1",
		Deployment: domain.DeploymentStrategy{MaxInstances: 50},
	}
	reg.mu.Unlock()

	err := mgr.Scale(context.Background(), "agent-1", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mgr.GetInstances("agent-1") != 10 {
		t.Errorf("expected 10 instances, got %d", mgr.GetInstances("agent-1"))
	}
}

func TestScale_ExceedsMax_Clamped(t *testing.T) {
	reg := newMockRegistry()
	emitter := &mockEmitter{}
	ms := newMockStore()
	mgr := NewManager(ms, reg, emitter, DefaultManagerConfig())

	reg.mu.Lock()
	reg.agents["agent-1"] = &domain.AgentEntry{
		ID:         "agent-1",
		Deployment: domain.DeploymentStrategy{MaxInstances: 20},
	}
	reg.mu.Unlock()

	err := mgr.Scale(context.Background(), "agent-1", 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mgr.GetInstances("agent-1") != 20 {
		t.Errorf("expected clamped to 20, got %d", mgr.GetInstances("agent-1"))
	}
}

func TestScale_InvalidReplicas(t *testing.T) {
	reg := newMockRegistry()
	emitter := &mockEmitter{}
	ms := newMockStore()
	mgr := NewManager(ms, reg, emitter, DefaultManagerConfig())

	err := mgr.Scale(context.Background(), "agent-1", 0)
	if !errors.Is(err, ErrInvalidReplicas) {
		t.Errorf("expected ErrInvalidReplicas for 0, got %v", err)
	}

	err = mgr.Scale(context.Background(), "agent-1", 101)
	if !errors.Is(err, ErrInvalidReplicas) {
		t.Errorf("expected ErrInvalidReplicas for 101, got %v", err)
	}
}

func TestRollback_NoPreviousVersion(t *testing.T) {
	ms := newMockStore()
	reg := newMockRegistry()
	emitter := &mockEmitter{}

	ms.agentStore.agents["agent-1"] = &domain.AgentEntry{ID: "agent-1", Version: "1.0.0"}
	ms.agentStore.versions["agent-1"] = []*domain.AgentVersion{
		{ID: "v1", AgentID: "agent-1", Version: "1.0.0"},
	}
	reg.agents["agent-1"] = ms.agentStore.agents["agent-1"]

	mgr := NewManager(ms, reg, emitter, DefaultManagerConfig())

	err := mgr.Rollback(context.Background(), "agent-1")
	if !errors.Is(err, ErrNoPreviousVersion) {
		t.Errorf("expected ErrNoPreviousVersion, got %v", err)
	}
}

func TestRollback_Success(t *testing.T) {
	ms := newMockStore()
	reg := newMockRegistry()
	emitter := &mockEmitter{}

	ms.agentStore.agents["agent-1"] = &domain.AgentEntry{ID: "agent-1", Version: "2.0.0"}
	ms.agentStore.versions["agent-1"] = []*domain.AgentVersion{
		{ID: "v2", AgentID: "agent-1", Version: "2.0.0"},
		{ID: "v1", AgentID: "agent-1", Version: "1.0.0"},
	}
	reg.agents["agent-1"] = ms.agentStore.agents["agent-1"]

	mgr := NewManager(ms, reg, emitter, DefaultManagerConfig())

	err := mgr.Rollback(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	agent := ms.agentStore.agents["agent-1"]
	if agent.Version != "1.0.0" {
		t.Errorf("expected version 1.0.0 after rollback, got %s", agent.Version)
	}

	events := emitter.Events()
	found := false
	for _, e := range events {
		if e.Type == "rollback_completed" {
			found = true
			if e.Payload["previous_version"] != "1.0.0" {
				t.Errorf("expected previous_version 1.0.0, got %v", e.Payload["previous_version"])
			}
		}
	}
	if !found {
		t.Error("expected rollback_completed event")
	}
}

func TestCanaryMetrics_ErrorRateRollback(t *testing.T) {
	cm := &CanaryMetrics{
		evaluationStart: time.Now().Add(-61 * time.Second),
	}

	for i := 0; i < 94; i++ {
		cm.RecordRequest(10*time.Millisecond, false)
	}
	for i := 0; i < 6; i++ {
		cm.RecordRequest(10*time.Millisecond, true)
	}

	if !cm.ShouldRollback() {
		t.Error("expected rollback when error rate > 5%")
	}
}

func TestCanaryMetrics_P99RollbackOn3xBaseline(t *testing.T) {
	cm := &CanaryMetrics{
		evaluationStart: time.Now().Add(-61 * time.Second),
		baselineP99:     100 * time.Millisecond,
	}

	for i := 0; i < 100; i++ {
		cm.RecordRequest(350*time.Millisecond, false)
	}

	if !cm.ShouldRollback() {
		t.Error("expected rollback when p99 > 3× baseline")
	}
}

func TestCanaryMetrics_NoRollbackWithinThresholds(t *testing.T) {
	cm := &CanaryMetrics{
		evaluationStart: time.Now().Add(-61 * time.Second),
		baselineP99:     100 * time.Millisecond,
	}

	for i := 0; i < 100; i++ {
		cm.RecordRequest(50*time.Millisecond, false)
	}

	if cm.ShouldRollback() {
		t.Error("should not rollback when within thresholds")
	}
}

func TestCanaryRouting(t *testing.T) {
	cs := &CanaryState{
		TrafficPercent: 10,
		Active:         true,
	}

	newCount := 0
	for i := 0; i < 100; i++ {
		if cs.RouteToNew(i) {
			newCount++
		}
	}
	if newCount != 10 {
		t.Errorf("expected 10 requests to new version, got %d", newCount)
	}
}

func TestCanaryRouting_50Percent(t *testing.T) {
	cs := &CanaryState{
		TrafficPercent: 50,
		Active:         true,
	}

	newCount := 0
	for i := 0; i < 100; i++ {
		if cs.RouteToNew(i) {
			newCount++
		}
	}
	if newCount != 50 {
		t.Errorf("expected 50 requests to new version, got %d", newCount)
	}
}

func TestCanaryRouting_Inactive(t *testing.T) {
	cs := &CanaryState{
		TrafficPercent: 10,
		Active:         false,
	}

	for i := 0; i < 100; i++ {
		if cs.RouteToNew(i) {
			t.Error("inactive canary should not route to new version")
		}
	}
}

func TestDeprecate_AgentNotFound(t *testing.T) {
	reg := newMockRegistry()
	emitter := &mockEmitter{}
	ms := newMockStore()
	mgr := NewManager(ms, reg, emitter, DefaultManagerConfig())

	err := mgr.Deprecate(context.Background(), "nonexistent", 60*time.Second)
	if err == nil {
		t.Error("expected error for nonexistent agent")
	}
}

func TestDeprecate_NoActiveMissions_ImmediateRemoval(t *testing.T) {
	ms := newMockStore()
	reg := newMockRegistry()
	emitter := &mockEmitter{}

	ms.agentStore.agents["agent-1"] = &domain.AgentEntry{
		ID:     "agent-1",
		Status: domain.AgentStatusActive,
	}
	reg.agents["agent-1"] = ms.agentStore.agents["agent-1"]

	mgr := NewManager(ms, reg, emitter, DefaultManagerConfig())

	err := mgr.Deprecate(context.Background(), "agent-1", 60*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	agent := ms.agentStore.agents["agent-1"]
	if agent.Status != domain.AgentStatusDeprecated {
		t.Errorf("expected deprecated, got %s", agent.Status)
	}
}

func TestScaleOnQueueDepth_BelowThreshold(t *testing.T) {
	reg := newMockRegistry()
	emitter := &mockEmitter{}
	ms := newMockStore()
	mgr := NewManager(ms, reg, emitter, DefaultManagerConfig())

	reg.mu.Lock()
	reg.agents["agent-1"] = &domain.AgentEntry{
		ID:         "agent-1",
		Deployment: domain.DeploymentStrategy{MaxInstances: 10},
	}
	reg.mu.Unlock()

	mgr.mu.Lock()
	mgr.instances["agent-1"] = 2
	mgr.mu.Unlock()

	err := mgr.ScaleOnQueueDepth(context.Background(), "agent-1", 5, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mgr.GetInstances("agent-1") != 2 {
		t.Errorf("expected no scale change, got %d", mgr.GetInstances("agent-1"))
	}
}

func TestScaleOnQueueDepth_AboveThreshold(t *testing.T) {
	reg := newMockRegistry()
	emitter := &mockEmitter{}
	ms := newMockStore()
	mgr := NewManager(ms, reg, emitter, DefaultManagerConfig())

	reg.mu.Lock()
	reg.agents["agent-1"] = &domain.AgentEntry{
		ID:         "agent-1",
		Deployment: domain.DeploymentStrategy{MaxInstances: 10},
	}
	reg.mu.Unlock()

	mgr.mu.Lock()
	mgr.instances["agent-1"] = 2
	mgr.mu.Unlock()

	err := mgr.ScaleOnQueueDepth(context.Background(), "agent-1", 15, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mgr.GetInstances("agent-1") != 3 {
		t.Errorf("expected 3 instances after scale up, got %d", mgr.GetInstances("agent-1"))
	}
}

func TestScaleOnQueueDepth_AtMax(t *testing.T) {
	reg := newMockRegistry()
	emitter := &mockEmitter{}
	ms := newMockStore()
	mgr := NewManager(ms, reg, emitter, DefaultManagerConfig())

	reg.mu.Lock()
	reg.agents["agent-1"] = &domain.AgentEntry{
		ID:         "agent-1",
		Deployment: domain.DeploymentStrategy{MaxInstances: 5},
	}
	reg.mu.Unlock()

	mgr.mu.Lock()
	mgr.instances["agent-1"] = 5
	mgr.mu.Unlock()

	err := mgr.ScaleOnQueueDepth(context.Background(), "agent-1", 100, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mgr.GetInstances("agent-1") != 5 {
		t.Errorf("expected still 5 instances (at max), got %d", mgr.GetInstances("agent-1"))
	}
}
