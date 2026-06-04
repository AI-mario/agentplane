package property

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/policy"
	"pgregory.net/rapid"
)

// --- Mock stores (mirroring internal/policy/engine_test.go) ---

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

// mockEmitter records emitted events.
type mockEmitter struct {
	mu     sync.Mutex
	events []domain.SystemEvent
}

func newMockEmitter() *mockEmitter {
	return &mockEmitter{}
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

func (m *mockEmitter) hasEventType(eventType string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.events {
		if e.Type == eventType {
			return true
		}
	}
	return false
}

func (m *mockEmitter) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = nil
}

// errorPolicyStore always returns errors for fail-safe testing.
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

// --- Generators ---

// genPositiveCost generates a positive cost value.
func genPositiveCost() *rapid.Generator[float64] {
	return rapid.Float64Range(0.01, 100000.0)
}

// genBudgetCap generates a positive budget cap.
func genBudgetCap() *rapid.Generator[float64] {
	return rapid.Float64Range(1.0, 100000.0)
}

// genTeamID generates a team identifier.
func genTeamID() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		return fmt.Sprintf("team-%d", rapid.IntRange(1, 1000).Draw(t, "teamNum"))
	})
}

// genPolicyRuleType picks a valid PolicyRuleType.
func genPolicyRuleType() *rapid.Generator[domain.PolicyRuleType] {
	return rapid.SampledFrom([]domain.PolicyRuleType{
		domain.PolicyRuleTypeBudget,
		domain.PolicyRuleTypeCapability,
		domain.PolicyRuleTypeRuntime,
		domain.PolicyRuleTypeDataAccess,
	})
}

// genRuleCount generates a rule count in [1, n].
func genRuleCount(maxN int) *rapid.Generator[int] {
	return rapid.IntRange(1, maxN)
}

// --- Property Tests ---

// TestProperty10_PolicyBudgetDenial tests that when estimated cost >= budget cap,
// the policy engine denies and emits budget_exceeded.
// **Validates: Requirements 4.2**
func TestProperty10_PolicyBudgetDenial(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		ps := newMockPolicyStore()
		bs := newMockBudgetStore()
		em := newMockEmitter()
		engine := policy.NewEngine(ps, bs, em)
		ctx := context.Background()

		teamID := genTeamID().Draw(t, "teamID")
		budgetCap := genBudgetCap().Draw(t, "budgetCap")

		// Set up budget so accumulated + estimated >= cap
		// Generate accumulated in [0, cap)
		accumulated := rapid.Float64Range(0, budgetCap-0.01).Draw(t, "accumulated")
		// Estimated cost must push us at or over the cap: estimated >= cap - accumulated
		minEstimated := budgetCap - accumulated
		estimatedCost := rapid.Float64Range(minEstimated, minEstimated+10000.0).Draw(t, "estimatedCost")

		bs.Set(ctx, &domain.Budget{
			ID:              "b-" + teamID,
			TeamID:          teamID,
			CapAmount:       budgetCap,
			AccumulatedCost: accumulated,
		})

		// Need at least one policy rule so we don't hit "empty policy" first
		ps.Create(ctx, &domain.Policy{
			ID:    "org-pol",
			Name:  "org-policy",
			Scope: domain.PolicyScopeOrganization,
			Rules: []domain.PolicyRule{
				{ID: "r1", Type: domain.PolicyRuleTypeBudget, Condition: "true", Effect: domain.PolicyEffectAllow},
			},
			Version: 1,
		})

		req := domain.PolicyRequest{
			Action:        "mission.assign",
			Subject:       &domain.Identity{Subject: "user1", Team: teamID},
			EstimatedCost: estimatedCost,
		}

		decision, err := engine.Evaluate(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if decision.Allowed {
			t.Fatalf("expected deny when accumulated(%.2f) + estimated(%.2f) >= cap(%.2f)",
				accumulated, estimatedCost, budgetCap)
		}
		if !em.hasEventType("budget_exceeded") {
			t.Fatal("expected budget_exceeded event to be emitted")
		}
	})
}

// TestProperty11_PolicyInheritanceOverride tests that team-level rules override
// org-level rules for the same scope (RuleType).
// **Validates: Requirements 4.5**
func TestProperty11_PolicyInheritanceOverride(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		ps := newMockPolicyStore()
		bs := newMockBudgetStore()
		em := newMockEmitter()
		engine := policy.NewEngine(ps, bs, em)
		ctx := context.Background()

		teamID := genTeamID().Draw(t, "teamID")
		// Org rule: budget limit low (deny anything > orgLimit)
		orgLimit := rapid.Float64Range(10.0, 500.0).Draw(t, "orgLimit")
		// Team rule: budget limit higher (allow anything <= teamLimit)
		// Use integer-valued limits to avoid floating-point formatting precision issues
		// since budget conditions are formatted with %f (6 decimal places)
		teamLimit := float64(int(orgLimit) + rapid.IntRange(10, 5000).Draw(t, "teamDelta"))

		// Request cost between org limit and team limit — should be ALLOWED (team overrides org)
		// Keep request cost well under team limit to avoid precision boundary
		requestCost := rapid.Float64Range(orgLimit+1.0, teamLimit-1.0).Draw(t, "requestCost")

		ps.Create(ctx, &domain.Policy{
			ID:    "org-pol",
			Name:  "org-budget",
			Scope: domain.PolicyScopeOrganization,
			Rules: []domain.PolicyRule{
				{
					ID:        "org-r1",
					Type:      domain.PolicyRuleTypeBudget,
					Condition: fmt.Sprintf("request.estimatedCost <= %.2f", orgLimit),
					Effect:    domain.PolicyEffectAllow,
				},
			},
			Version: 1,
		})

		ps.Create(ctx, &domain.Policy{
			ID:     "team-pol",
			Name:   "team-budget",
			Scope:  domain.PolicyScopeTeam,
			TeamID: teamID,
			Rules: []domain.PolicyRule{
				{
					ID:        "team-r1",
					Type:      domain.PolicyRuleTypeBudget,
					Condition: fmt.Sprintf("request.estimatedCost <= %.2f", teamLimit),
					Effect:    domain.PolicyEffectAllow,
				},
			},
			Version: 1,
		})

		// Set generous budget cap so budget check doesn't interfere
		bs.Set(ctx, &domain.Budget{
			ID:              "b-" + teamID,
			TeamID:          teamID,
			CapAmount:       1000000,
			AccumulatedCost: 0,
		})

		req := domain.PolicyRequest{
			Action:        "mission.assign",
			Subject:       &domain.Identity{Subject: "user1", Team: teamID},
			EstimatedCost: requestCost,
		}

		decision, err := engine.Evaluate(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !decision.Allowed {
			t.Fatalf("expected allow (team override): cost %.2f <= teamLimit %.2f, but org limit was %.2f. Reason: %s",
				requestCost, teamLimit, orgLimit, decision.Reason)
		}
	})
}

// TestProperty16_PolicyEvaluationBeforeAssignment tests that policy evaluation
// happens before assignment finalization — a deny prevents assignment.
// **Validates: Requirements 4.1**
func TestProperty16_PolicyEvaluationBeforeAssignment(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		ps := newMockPolicyStore()
		bs := newMockBudgetStore()
		em := newMockEmitter()
		engine := policy.NewEngine(ps, bs, em)
		ctx := context.Background()

		teamID := genTeamID().Draw(t, "teamID")

		// Create a deny policy — should block all assignment attempts
		ps.Create(ctx, &domain.Policy{
			ID:    "deny-all",
			Name:  "deny-all-policy",
			Scope: domain.PolicyScopeOrganization,
			Rules: []domain.PolicyRule{
				{
					ID:        "deny-r1",
					Type:      domain.PolicyRuleTypeBudget,
					Condition: "true",
					Effect:    domain.PolicyEffectDeny,
				},
			},
			Version: 1,
		})

		// Even with generous budget, the deny rule must prevent assignment
		bs.Set(ctx, &domain.Budget{
			ID:              "b-" + teamID,
			TeamID:          teamID,
			CapAmount:       1000000,
			AccumulatedCost: 0,
		})

		estimatedCost := genPositiveCost().Draw(t, "estimatedCost")

		req := domain.PolicyRequest{
			Action:        "mission.assign",
			Subject:       &domain.Identity{Subject: "user1", Team: teamID},
			EstimatedCost: estimatedCost,
		}

		decision, err := engine.Evaluate(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Policy deny must prevent assignment finalization
		if decision.Allowed {
			t.Fatal("expected deny from policy evaluation to prevent assignment, got allow")
		}
	})
}

// TestProperty17_BudgetCapEnforcement tests that accumulated + estimated >= cap → reject.
// **Validates: Requirements 4.2**
func TestProperty17_BudgetCapEnforcement(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		ps := newMockPolicyStore()
		bs := newMockBudgetStore()
		em := newMockEmitter()
		engine := policy.NewEngine(ps, bs, em)
		ctx := context.Background()

		teamID := genTeamID().Draw(t, "teamID")
		budgetCap := genBudgetCap().Draw(t, "budgetCap")

		// Generate values where accumulated + estimated >= cap
		accumulated := rapid.Float64Range(0, budgetCap).Draw(t, "accumulated")
		// Ensure overflow: estimated >= cap - accumulated
		remaining := budgetCap - accumulated
		estimatedCost := rapid.Float64Range(remaining, remaining+10000.0).Draw(t, "estimatedCost")

		bs.Set(ctx, &domain.Budget{
			ID:              "b-" + teamID,
			TeamID:          teamID,
			CapAmount:       budgetCap,
			AccumulatedCost: accumulated,
		})

		// Need a policy with at least one rule
		ps.Create(ctx, &domain.Policy{
			ID:    "org-pol",
			Name:  "org-policy",
			Scope: domain.PolicyScopeOrganization,
			Rules: []domain.PolicyRule{
				{ID: "r1", Type: domain.PolicyRuleTypeBudget, Condition: "true", Effect: domain.PolicyEffectAllow},
			},
			Version: 1,
		})

		req := domain.PolicyRequest{
			Action:        "mission.assign",
			Subject:       &domain.Identity{Subject: "user1", Team: teamID},
			EstimatedCost: estimatedCost,
		}

		decision, err := engine.Evaluate(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if decision.Allowed {
			t.Fatalf("expected reject: accumulated(%.2f) + estimated(%.2f) = %.2f >= cap(%.2f)",
				accumulated, estimatedCost, accumulated+estimatedCost, budgetCap)
		}
	})
}

// TestProperty18_PolicyRuleCountEnforcement tests that >50 rules → reject,
// <=50 → stored and evaluable.
// **Validates: Requirements 4.3**
func TestProperty18_PolicyRuleCountEnforcement(t *testing.T) {
	t.Run("RejectOver50Rules", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			ps := newMockPolicyStore()
			bs := newMockBudgetStore()
			em := newMockEmitter()
			engine := policy.NewEngine(ps, bs, em)
			ctx := context.Background()

			ruleCount := rapid.IntRange(51, 200).Draw(t, "ruleCount")
			rules := make([]domain.PolicyRule, ruleCount)
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
				Name:  "too-many-rules",
				Scope: domain.PolicyScopeOrganization,
				Rules: rules,
			})

			if err == nil {
				t.Fatalf("expected rejection for %d rules (> 50)", ruleCount)
			}
		})
	})

	t.Run("AcceptUpTo50Rules", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			ps := newMockPolicyStore()
			bs := newMockBudgetStore()
			em := newMockEmitter()
			engine := policy.NewEngine(ps, bs, em)
			ctx := context.Background()

			ruleCount := rapid.IntRange(1, 50).Draw(t, "ruleCount")
			rules := make([]domain.PolicyRule, ruleCount)
			for i := range rules {
				rules[i] = domain.PolicyRule{
					ID:        fmt.Sprintf("r%d", i),
					Type:      domain.PolicyRuleTypeBudget,
					Condition: "true",
					Effect:    domain.PolicyEffectAllow,
				}
			}

			pol := &domain.Policy{
				ID:    "valid-policy",
				Name:  "valid-rules",
				Scope: domain.PolicyScopeOrganization,
				Rules: rules,
			}

			err := engine.ApplyPolicy(ctx, pol)
			if err != nil {
				t.Fatalf("expected acceptance for %d rules (<= 50), got: %v", ruleCount, err)
			}

			// Verify it can be retrieved and evaluated
			got, err := engine.GetPolicy(ctx, "valid-policy")
			if err != nil {
				t.Fatalf("failed to retrieve stored policy: %v", err)
			}
			if len(got.Rules) != ruleCount {
				t.Fatalf("expected %d rules stored, got %d", ruleCount, len(got.Rules))
			}

			// Verify evaluable: should allow since all rules are "true" with Allow effect
			req := domain.PolicyRequest{
				Action:  "mission.assign",
				Subject: &domain.Identity{Subject: "user1", Team: "any-team"},
			}
			decision, err := engine.Evaluate(ctx, req)
			if err != nil {
				t.Fatalf("evaluate failed: %v", err)
			}
			if !decision.Allowed {
				t.Fatalf("expected allow for all-true-allow rules, got deny: %s", decision.Reason)
			}
		})
	})
}

// TestProperty19_PolicyInheritanceWithTeamOverride tests that when both org and team
// define rules on the same scope (RuleType), the team rule wins.
// **Validates: Requirements 4.5**
func TestProperty19_PolicyInheritanceWithTeamOverride(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		ps := newMockPolicyStore()
		bs := newMockBudgetStore()
		em := newMockEmitter()
		engine := policy.NewEngine(ps, bs, em)
		ctx := context.Background()

		teamID := genTeamID().Draw(t, "teamID")

		// Org policy: DENY all with a specific rule type
		ruleType := genPolicyRuleType().Draw(t, "ruleType")

		ps.Create(ctx, &domain.Policy{
			ID:    "org-pol",
			Name:  "org-deny",
			Scope: domain.PolicyScopeOrganization,
			Rules: []domain.PolicyRule{
				{
					ID:        "org-r1",
					Type:      ruleType,
					Condition: "true",
					Effect:    domain.PolicyEffectDeny,
				},
			},
			Version: 1,
		})

		// Team policy: ALLOW with same rule type (overrides org deny)
		ps.Create(ctx, &domain.Policy{
			ID:     "team-pol",
			Name:   "team-allow",
			Scope:  domain.PolicyScopeTeam,
			TeamID: teamID,
			Rules: []domain.PolicyRule{
				{
					ID:        "team-r1",
					Type:      ruleType,
					Condition: "true",
					Effect:    domain.PolicyEffectAllow,
				},
			},
			Version: 1,
		})

		req := domain.PolicyRequest{
			Action:  "mission.assign",
			Subject: &domain.Identity{Subject: "user1", Team: teamID},
		}

		decision, err := engine.Evaluate(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Team allow should override org deny for same rule type
		if !decision.Allowed {
			t.Fatalf("expected allow (team override of org deny for ruleType %s), got deny: %s",
				ruleType, decision.Reason)
		}
	})
}

// TestProperty20_PolicyFailSafeDeny tests that on error or empty policy,
// the engine denies and emits the appropriate event.
// **Validates: Requirements 4.6, 4.7**
func TestProperty20_PolicyFailSafeDeny(t *testing.T) {
	t.Run("DenyOnInternalError", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			ps := &errorPolicyStore{}
			bs := newMockBudgetStore()
			em := newMockEmitter()
			engine := policy.NewEngine(ps, bs, em)
			ctx := context.Background()

			teamID := genTeamID().Draw(t, "teamID")
			action := rapid.SampledFrom([]string{
				"mission.assign",
				"agent.communicate",
				"agent.deploy",
				"mission.submit",
			}).Draw(t, "action")

			req := domain.PolicyRequest{
				Action:        action,
				Subject:       &domain.Identity{Subject: "user1", Team: teamID},
				EstimatedCost: rapid.Float64Range(0, 10000).Draw(t, "cost"),
			}

			decision, err := engine.Evaluate(ctx, req)
			if err != nil {
				t.Fatalf("unexpected error (should fail safe, not propagate): %v", err)
			}
			if decision.Allowed {
				t.Fatal("expected deny on internal error, got allow")
			}
			if !em.hasEventType("policy_error") {
				t.Fatal("expected policy_error event to be emitted")
			}
		})
	})

	t.Run("DenyOnEmptyPolicy", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			ps := newMockPolicyStore()
			bs := newMockBudgetStore()
			em := newMockEmitter()
			engine := policy.NewEngine(ps, bs, em)
			ctx := context.Background()

			// No policies configured at all
			teamID := genTeamID().Draw(t, "teamID")
			action := rapid.SampledFrom([]string{
				"mission.assign",
				"agent.communicate",
				"agent.deploy",
			}).Draw(t, "action")

			req := domain.PolicyRequest{
				Action:  action,
				Subject: &domain.Identity{Subject: "user1", Team: teamID},
			}

			decision, err := engine.Evaluate(ctx, req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if decision.Allowed {
				t.Fatal("expected deny on empty policy, got allow")
			}
			if !em.hasEventType("policy_empty") {
				t.Fatal("expected policy_empty event to be emitted")
			}
		})
	})
}
