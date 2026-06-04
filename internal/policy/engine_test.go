package policy

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
)

// --- Test doubles ---

type mockPolicyStore struct {
	mu       sync.Mutex
	policies map[string]*domain.Policy
}

func newMockPolicyStore() *mockPolicyStore {
	return &mockPolicyStore{policies: make(map[string]*domain.Policy)}
}

func (m *mockPolicyStore) Create(_ context.Context, p *domain.Policy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.policies[p.ID]; exists {
		return fmt.Errorf("policy already exists: %s", p.ID)
	}
	m.policies[p.ID] = p
	return nil
}

func (m *mockPolicyStore) Get(_ context.Context, id string) (*domain.Policy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.policies[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return p, nil
}

func (m *mockPolicyStore) List(_ context.Context, filter domain.PolicyFilter) ([]*domain.Policy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*domain.Policy
	for _, p := range m.policies {
		if filter.Scope != "" && p.Scope != filter.Scope {
			continue
		}
		if filter.TeamID != "" && p.TeamID != filter.TeamID {
			continue
		}
		result = append(result, p)
	}
	return result, nil
}

func (m *mockPolicyStore) Update(_ context.Context, p *domain.Policy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.policies[p.ID] = p
	return nil
}

func (m *mockPolicyStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.policies, id)
	return nil
}

func (m *mockPolicyStore) GetEffective(_ context.Context, scope domain.PolicyScope, scopeID string) ([]*domain.Policy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*domain.Policy
	for _, p := range m.policies {
		if p.Scope == scope {
			if scope == domain.PolicyScopeOrganization || p.TeamID == scopeID {
				result = append(result, p)
			}
		}
	}
	return result, nil
}

type mockBudgetStore struct {
	mu      sync.Mutex
	budgets map[string]*domain.Budget
}

func newMockBudgetStore() *mockBudgetStore {
	return &mockBudgetStore{budgets: make(map[string]*domain.Budget)}
}

func (m *mockBudgetStore) Get(_ context.Context, teamID string) (*domain.Budget, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.budgets[teamID]
	if !ok {
		return nil, nil
	}
	return b, nil
}

func (m *mockBudgetStore) Set(_ context.Context, budget *domain.Budget) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.budgets[budget.TeamID] = budget
	return nil
}

func (m *mockBudgetStore) ListAll(_ context.Context) ([]*domain.Budget, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*domain.Budget
	for _, b := range m.budgets {
		result = append(result, b)
	}
	return result, nil
}

func (m *mockBudgetStore) IncrementAccumulated(_ context.Context, teamID string, amount float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.budgets[teamID]
	if !ok {
		return fmt.Errorf("budget not found")
	}
	b.AccumulatedCost += amount
	return nil
}

func (m *mockBudgetStore) ResetPeriod(_ context.Context, teamID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.budgets[teamID]
	if !ok {
		return fmt.Errorf("budget not found")
	}
	b.AccumulatedCost = 0
	b.IsBlocked = false
	return nil
}

type mockEmitter struct {
	mu     sync.Mutex
	events []domain.SystemEvent
}

func (m *mockEmitter) Emit(_ context.Context, event domain.SystemEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
}

func (m *mockEmitter) lastEvent() *domain.SystemEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.events) == 0 {
		return nil
	}
	return &m.events[len(m.events)-1]
}

func (m *mockEmitter) eventCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.events)
}

// --- Tests ---

func TestEvaluate_AllowOnValidBudgetRule(t *testing.T) {
	ps := newMockPolicyStore()
	bs := newMockBudgetStore()
	em := &mockEmitter{}
	engine := NewEngine(ps, bs, em)

	ctx := context.Background()

	// Create org policy with budget rule
	ps.Create(ctx, &domain.Policy{
		ID:    "org-1",
		Name:  "org-budget",
		Scope: domain.PolicyScopeOrganization,
		Rules: []domain.PolicyRule{
			{ID: "r1", Type: domain.PolicyRuleTypeBudget, Condition: "request.estimatedCost <= 1000.00", Effect: domain.PolicyEffectAllow},
		},
		Version: 1,
	})

	// Budget allows it
	bs.Set(ctx, &domain.Budget{
		ID:              "b1",
		TeamID:          "team-a",
		CapAmount:       5000,
		AccumulatedCost: 100,
	})

	req := domain.PolicyRequest{
		Action:        "mission.assign",
		Subject:       &domain.Identity{Subject: "user1", Team: "team-a"},
		EstimatedCost: 500,
	}

	decision, err := engine.Evaluate(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("expected allow, got deny: %s", decision.Reason)
	}
}

func TestEvaluate_DenyOnBudgetExceeded(t *testing.T) {
	ps := newMockPolicyStore()
	bs := newMockBudgetStore()
	em := &mockEmitter{}
	engine := NewEngine(ps, bs, em)

	ctx := context.Background()

	ps.Create(ctx, &domain.Policy{
		ID:    "org-1",
		Name:  "org-budget",
		Scope: domain.PolicyScopeOrganization,
		Rules: []domain.PolicyRule{
			{ID: "r1", Type: domain.PolicyRuleTypeBudget, Condition: "request.estimatedCost <= 1000.00", Effect: domain.PolicyEffectAllow},
		},
		Version: 1,
	})

	// Budget at capacity
	bs.Set(ctx, &domain.Budget{
		ID:              "b1",
		TeamID:          "team-a",
		CapAmount:       1000,
		AccumulatedCost: 900,
	})

	req := domain.PolicyRequest{
		Action:        "mission.assign",
		Subject:       &domain.Identity{Subject: "user1", Team: "team-a"},
		EstimatedCost: 200, // 900 + 200 = 1100 >= 1000
	}

	decision, err := engine.Evaluate(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.Allowed {
		t.Fatalf("expected deny, got allow")
	}

	// Check budget_exceeded event emitted
	evt := em.lastEvent()
	if evt == nil || evt.Type != "budget_exceeded" {
		t.Fatalf("expected budget_exceeded event, got %v", evt)
	}
}

func TestEvaluate_FailSafeDenyOnEmptyPolicy(t *testing.T) {
	ps := newMockPolicyStore()
	bs := newMockBudgetStore()
	em := &mockEmitter{}
	engine := NewEngine(ps, bs, em)

	ctx := context.Background()

	// No policies configured
	req := domain.PolicyRequest{
		Action:  "mission.assign",
		Subject: &domain.Identity{Subject: "user1", Team: "team-a"},
	}

	decision, err := engine.Evaluate(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.Allowed {
		t.Fatalf("expected deny on empty policy, got allow")
	}

	evt := em.lastEvent()
	if evt == nil || evt.Type != "policy_empty" {
		t.Fatalf("expected policy_empty event, got %v", evt)
	}
}

func TestEvaluate_FailSafeDenyOnInternalError(t *testing.T) {
	ps := &errorPolicyStore{}
	bs := newMockBudgetStore()
	em := &mockEmitter{}
	engine := NewEngine(ps, bs, em)

	ctx := context.Background()
	req := domain.PolicyRequest{
		Action:  "mission.assign",
		Subject: &domain.Identity{Subject: "user1", Team: "team-a"},
	}

	decision, err := engine.Evaluate(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.Allowed {
		t.Fatalf("expected deny on internal error, got allow")
	}

	evt := em.lastEvent()
	if evt == nil || evt.Type != "policy_error" {
		t.Fatalf("expected policy_error event, got %v", evt)
	}
}

func TestEvaluate_TeamOverridesOrgForSameRuleType(t *testing.T) {
	ps := newMockPolicyStore()
	bs := newMockBudgetStore()
	em := &mockEmitter{}
	engine := NewEngine(ps, bs, em)

	ctx := context.Background()

	// Org policy: budget <= 500
	ps.Create(ctx, &domain.Policy{
		ID:    "org-1",
		Name:  "org-budget",
		Scope: domain.PolicyScopeOrganization,
		Rules: []domain.PolicyRule{
			{ID: "r1", Type: domain.PolicyRuleTypeBudget, Condition: "request.estimatedCost <= 500.00", Effect: domain.PolicyEffectAllow},
		},
		Version: 1,
	})

	// Team policy: budget <= 2000 (overrides org)
	ps.Create(ctx, &domain.Policy{
		ID:     "team-1",
		Name:   "team-budget",
		Scope:  domain.PolicyScopeTeam,
		TeamID: "team-a",
		Rules: []domain.PolicyRule{
			{ID: "r2", Type: domain.PolicyRuleTypeBudget, Condition: "request.estimatedCost <= 2000.00", Effect: domain.PolicyEffectAllow},
		},
		Version: 1,
	})

	bs.Set(ctx, &domain.Budget{
		ID:              "b1",
		TeamID:          "team-a",
		CapAmount:       5000,
		AccumulatedCost: 0,
	})

	// Request with cost 800 — would fail org (<=500) but passes team (<=2000)
	req := domain.PolicyRequest{
		Action:        "mission.assign",
		Subject:       &domain.Identity{Subject: "user1", Team: "team-a"},
		EstimatedCost: 800,
	}

	decision, err := engine.Evaluate(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("expected allow (team override), got deny: %s", decision.Reason)
	}
}

func TestEvaluate_DenyRuleTakesPriority(t *testing.T) {
	ps := newMockPolicyStore()
	bs := newMockBudgetStore()
	em := &mockEmitter{}
	engine := NewEngine(ps, bs, em)

	ctx := context.Background()

	ps.Create(ctx, &domain.Policy{
		ID:    "org-1",
		Name:  "deny-all",
		Scope: domain.PolicyScopeOrganization,
		Rules: []domain.PolicyRule{
			{ID: "r1", Type: domain.PolicyRuleTypeBudget, Condition: "true", Effect: domain.PolicyEffectDeny},
		},
		Version: 1,
	})

	req := domain.PolicyRequest{
		Action:  "mission.assign",
		Subject: &domain.Identity{Subject: "user1", Team: "team-a"},
	}

	decision, err := engine.Evaluate(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.Allowed {
		t.Fatalf("expected deny, got allow")
	}
}

func TestApplyPolicy_RejectsOver50Rules(t *testing.T) {
	ps := newMockPolicyStore()
	bs := newMockBudgetStore()
	em := &mockEmitter{}
	engine := NewEngine(ps, bs, em)

	ctx := context.Background()

	rules := make([]domain.PolicyRule, 51)
	for i := range rules {
		rules[i] = domain.PolicyRule{
			ID:        fmt.Sprintf("r%d", i),
			Type:      domain.PolicyRuleTypeBudget,
			Condition: "true",
			Effect:    domain.PolicyEffectAllow,
		}
	}

	err := engine.ApplyPolicy(ctx, &domain.Policy{
		ID:    "big-policy",
		Name:  "too many rules",
		Scope: domain.PolicyScopeOrganization,
		Rules: rules,
	})

	if err == nil {
		t.Fatalf("expected error for >50 rules")
	}
}

func TestApplyPolicy_Accepts50Rules(t *testing.T) {
	ps := newMockPolicyStore()
	bs := newMockBudgetStore()
	em := &mockEmitter{}
	engine := NewEngine(ps, bs, em)

	ctx := context.Background()

	rules := make([]domain.PolicyRule, 50)
	for i := range rules {
		rules[i] = domain.PolicyRule{
			ID:        fmt.Sprintf("r%d", i),
			Type:      domain.PolicyRuleTypeBudget,
			Condition: "true",
			Effect:    domain.PolicyEffectAllow,
		}
	}

	err := engine.ApplyPolicy(ctx, &domain.Policy{
		ID:    "max-policy",
		Name:  "max rules",
		Scope: domain.PolicyScopeOrganization,
		Rules: rules,
	})

	if err != nil {
		t.Fatalf("unexpected error for 50 rules: %v", err)
	}
}

func TestApplyPolicy_VersionIncrementsOnUpdate(t *testing.T) {
	ps := newMockPolicyStore()
	bs := newMockBudgetStore()
	em := &mockEmitter{}
	engine := NewEngine(ps, bs, em)

	ctx := context.Background()

	policy := &domain.Policy{
		ID:    "p1",
		Name:  "test",
		Scope: domain.PolicyScopeOrganization,
		Rules: []domain.PolicyRule{
			{ID: "r1", Type: domain.PolicyRuleTypeBudget, Condition: "true", Effect: domain.PolicyEffectAllow},
		},
	}

	// First apply → version 1
	if err := engine.ApplyPolicy(ctx, policy); err != nil {
		t.Fatalf("first apply failed: %v", err)
	}

	got, _ := engine.GetPolicy(ctx, "p1")
	if got.Version != 1 {
		t.Fatalf("expected version 1, got %d", got.Version)
	}

	// Second apply → version 2
	policy.Rules = append(policy.Rules, domain.PolicyRule{
		ID: "r2", Type: domain.PolicyRuleTypeRuntime, Condition: "true", Effect: domain.PolicyEffectAllow,
	})
	if err := engine.ApplyPolicy(ctx, policy); err != nil {
		t.Fatalf("second apply failed: %v", err)
	}

	got, _ = engine.GetPolicy(ctx, "p1")
	if got.Version != 2 {
		t.Fatalf("expected version 2, got %d", got.Version)
	}
}

func TestApplyPolicy_AppliedWithin2Seconds(t *testing.T) {
	ps := newMockPolicyStore()
	bs := newMockBudgetStore()
	em := &mockEmitter{}
	engine := NewEngine(ps, bs, em)

	ctx := context.Background()

	start := time.Now()
	err := engine.ApplyPolicy(ctx, &domain.Policy{
		ID:    "p1",
		Name:  "quick",
		Scope: domain.PolicyScopeOrganization,
		Rules: []domain.PolicyRule{
			{ID: "r1", Type: domain.PolicyRuleTypeBudget, Condition: "true", Effect: domain.PolicyEffectAllow},
		},
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("apply took %v, exceeds 2 second limit", elapsed)
	}
}

func TestEvaluate_BudgetConditionParsing(t *testing.T) {
	ps := newMockPolicyStore()
	bs := newMockBudgetStore()
	em := &mockEmitter{}
	engine := NewEngine(ps, bs, em)

	ctx := context.Background()

	ps.Create(ctx, &domain.Policy{
		ID:    "org-1",
		Scope: domain.PolicyScopeOrganization,
		Rules: []domain.PolicyRule{
			{ID: "r1", Type: domain.PolicyRuleTypeBudget, Condition: "request.estimatedCost <= 100.00", Effect: domain.PolicyEffectAllow},
		},
	})

	bs.Set(ctx, &domain.Budget{TeamID: "t1", CapAmount: 10000})

	// Cost under limit → allow
	decision, _ := engine.Evaluate(ctx, domain.PolicyRequest{
		Action:        "mission.assign",
		Subject:       &domain.Identity{Team: "t1"},
		EstimatedCost: 50,
	})
	if !decision.Allowed {
		t.Fatalf("expected allow for cost 50, got deny: %s", decision.Reason)
	}

	// Cost over limit → deny
	decision, _ = engine.Evaluate(ctx, domain.PolicyRequest{
		Action:        "mission.assign",
		Subject:       &domain.Identity{Team: "t1"},
		EstimatedCost: 150,
	})
	if decision.Allowed {
		t.Fatalf("expected deny for cost 150, got allow")
	}
}

func TestGetPolicy_ReturnsPolicy(t *testing.T) {
	ps := newMockPolicyStore()
	bs := newMockBudgetStore()
	em := &mockEmitter{}
	engine := NewEngine(ps, bs, em)

	ctx := context.Background()

	ps.Create(ctx, &domain.Policy{
		ID:      "p1",
		Name:    "test-policy",
		Scope:   domain.PolicyScopeTeam,
		TeamID:  "team-x",
		Version: 3,
	})

	got, err := engine.GetPolicy(ctx, "p1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "test-policy" {
		t.Fatalf("expected name test-policy, got %s", got.Name)
	}
	if got.TeamID != "team-x" {
		t.Fatalf("expected team team-x, got %s", got.TeamID)
	}
}

// errorPolicyStore always returns errors (for fail-safe testing)
type errorPolicyStore struct{}

func (e *errorPolicyStore) Create(_ context.Context, _ *domain.Policy) error {
	return fmt.Errorf("store unavailable")
}
func (e *errorPolicyStore) Get(_ context.Context, _ string) (*domain.Policy, error) {
	return nil, fmt.Errorf("store unavailable")
}
func (e *errorPolicyStore) List(_ context.Context, _ domain.PolicyFilter) ([]*domain.Policy, error) {
	return nil, fmt.Errorf("store unavailable")
}
func (e *errorPolicyStore) Update(_ context.Context, _ *domain.Policy) error {
	return fmt.Errorf("store unavailable")
}
func (e *errorPolicyStore) Delete(_ context.Context, _ string) error {
	return fmt.Errorf("store unavailable")
}
func (e *errorPolicyStore) GetEffective(_ context.Context, _ domain.PolicyScope, _ string) ([]*domain.Policy, error) {
	return nil, fmt.Errorf("store unavailable")
}
