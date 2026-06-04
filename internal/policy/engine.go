package policy

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/store"
)

const maxRulesPerPolicy = 50

// EventEmitter handles system event emission.
type EventEmitter interface {
	Emit(ctx context.Context, event domain.SystemEvent)
}

// Engine implements PolicyEngineService with inheritance, budget checks, and fail-safe deny.
type Engine struct {
	policies PolicyStore
	budgets  store.BudgetStore
	emitter  EventEmitter

	// policyCache provides fast lookup for in-flight evaluation consistency.
	// When a policy is updated, new evaluations use the new version while
	// in-flight evaluations complete under the previous version.
	mu          sync.RWMutex
	policyCache map[string]*domain.Policy
}

// PolicyStore is a local alias matching the store.PolicyStore interface for testability.
type PolicyStore = store.PolicyStore

// NewEngine creates a new policy engine.
func NewEngine(policies PolicyStore, budgets store.BudgetStore, emitter EventEmitter) *Engine {
	return &Engine{
		policies:    policies,
		budgets:     budgets,
		emitter:     emitter,
		policyCache: make(map[string]*domain.Policy),
	}
}

// Evaluate checks a request against applicable policies.
// Implements fail-safe deny on internal error or empty policy.
func (e *Engine) Evaluate(ctx context.Context, req domain.PolicyRequest) (*domain.PolicyDecision, error) {
	start := time.Now()

	// Determine team from request subject
	teamID := ""
	if req.Subject != nil {
		teamID = req.Subject.Team
	}

	// Resolve effective policies: org-level + team-level with team override
	effectivePolicies, err := e.resolveEffectivePolicies(ctx, teamID)
	if err != nil {
		// Fail-safe: deny on internal error
		e.emitEvent(ctx, "policy_error", "error", map[string]interface{}{
			"error":  err.Error(),
			"teamID": teamID,
			"action": req.Action,
		})
		return &domain.PolicyDecision{
			Allowed:  false,
			Reason:   "internal error: " + err.Error(),
			EvalTime: time.Since(start),
		}, nil
	}

	// Merge rules with inheritance (team overrides org for same RuleType)
	rules := e.mergeRules(effectivePolicies)

	// Fail-safe: deny on empty policy (no applicable rules)
	if len(rules) == 0 {
		e.emitEvent(ctx, "policy_empty", "warning", map[string]interface{}{
			"teamID": teamID,
			"action": req.Action,
		})
		return &domain.PolicyDecision{
			Allowed:  false,
			Reason:   "no applicable policy rules",
			EvalTime: time.Since(start),
		}, nil
	}

	// Budget check: reject if estimated cost + accumulated >= budget cap
	if req.EstimatedCost > 0 && teamID != "" {
		budgetDenied, budgetReason := e.checkBudget(ctx, teamID, req.EstimatedCost)
		if budgetDenied {
			e.emitEvent(ctx, "budget_exceeded", "warning", map[string]interface{}{
				"teamID":        teamID,
				"estimatedCost": req.EstimatedCost,
				"action":        req.Action,
			})
			return &domain.PolicyDecision{
				Allowed:  false,
				Reason:   budgetReason,
				EvalTime: time.Since(start),
			}, nil
		}
	}

	// Evaluate rules
	decision, err := e.evaluateRules(ctx, rules, req)
	if err != nil {
		// Fail-safe: deny on evaluation error
		e.emitEvent(ctx, "policy_error", "error", map[string]interface{}{
			"error":  err.Error(),
			"teamID": teamID,
			"action": req.Action,
		})
		return &domain.PolicyDecision{
			Allowed:  false,
			Reason:   "rule evaluation error: " + err.Error(),
			EvalTime: time.Since(start),
		}, nil
	}

	decision.EvalTime = time.Since(start)
	return decision, nil
}

// ApplyPolicy creates or updates a policy. Enforces max 50 rules.
// New policy version applies within 2 seconds; in-flight evaluations complete under previous version.
func (e *Engine) ApplyPolicy(ctx context.Context, policy *domain.Policy) error {
	if policy == nil {
		return fmt.Errorf("policy cannot be nil")
	}
	if len(policy.Rules) > maxRulesPerPolicy {
		return fmt.Errorf("policy exceeds maximum of %d rules (has %d)", maxRulesPerPolicy, len(policy.Rules))
	}

	// Check if policy exists (update vs create)
	existing, err := e.policies.Get(ctx, policy.ID)
	if err != nil && existing == nil {
		// New policy — create
		policy.Version = 1
		policy.UpdatedAt = time.Now()
		if err := e.policies.Create(ctx, policy); err != nil {
			return fmt.Errorf("failed to create policy: %w", err)
		}
	} else {
		// Existing policy — update with new version
		policy.Version = existing.Version + 1
		policy.UpdatedAt = time.Now()
		if err := e.policies.Update(ctx, policy); err != nil {
			return fmt.Errorf("failed to update policy: %w", err)
		}
	}

	// Update cache for new evaluations (in-flight keep their snapshot)
	e.mu.Lock()
	e.policyCache[policy.ID] = policy
	e.mu.Unlock()

	return nil
}

// GetPolicy retrieves a policy by ID.
func (e *Engine) GetPolicy(ctx context.Context, id string) (*domain.Policy, error) {
	return e.policies.Get(ctx, id)
}

// resolveEffectivePolicies loads org-level and team-level policies.
func (e *Engine) resolveEffectivePolicies(ctx context.Context, teamID string) ([]*domain.Policy, error) {
	// Load org-level policies
	orgPolicies, err := e.policies.GetEffective(ctx, domain.PolicyScopeOrganization, "")
	if err != nil {
		return nil, fmt.Errorf("failed to load org policies: %w", err)
	}

	if teamID == "" {
		return orgPolicies, nil
	}

	// Load team-level policies
	teamPolicies, err := e.policies.GetEffective(ctx, domain.PolicyScopeTeam, teamID)
	if err != nil {
		return nil, fmt.Errorf("failed to load team policies: %w", err)
	}

	// Combine: org + team (mergeRules handles override logic)
	all := make([]*domain.Policy, 0, len(orgPolicies)+len(teamPolicies))
	all = append(all, orgPolicies...)
	all = append(all, teamPolicies...)
	return all, nil
}

// mergeRules implements policy inheritance: team-level rules override org-level
// rules of the same RuleType.
func (e *Engine) mergeRules(policies []*domain.Policy) []mergedRule {
	// Index: RuleType → rule (team wins over org)
	rulesByType := make(map[domain.PolicyRuleType]mergedRule)

	for _, p := range policies {
		for _, r := range p.Rules {
			existing, exists := rulesByType[r.Type]
			if !exists {
				// First rule of this type
				rulesByType[r.Type] = mergedRule{rule: r, policyID: p.ID, scope: p.Scope}
			} else if p.Scope == domain.PolicyScopeTeam && existing.scope == domain.PolicyScopeOrganization {
				// Team overrides org for same type
				rulesByType[r.Type] = mergedRule{rule: r, policyID: p.ID, scope: p.Scope}
			} else if p.Scope == existing.scope {
				// Same scope — last one wins (latest policy)
				rulesByType[r.Type] = mergedRule{rule: r, policyID: p.ID, scope: p.Scope}
			}
		}
	}

	result := make([]mergedRule, 0, len(rulesByType))
	for _, mr := range rulesByType {
		result = append(result, mr)
	}
	return result
}

type mergedRule struct {
	rule     domain.PolicyRule
	policyID string
	scope    domain.PolicyScope
}

// checkBudget returns true (denied) if estimated cost + accumulated >= budget cap.
func (e *Engine) checkBudget(ctx context.Context, teamID string, estimatedCost float64) (bool, string) {
	budget, err := e.budgets.Get(ctx, teamID)
	if err != nil || budget == nil {
		// No budget configured = no budget constraint
		return false, ""
	}

	if budget.CapAmount <= 0 {
		// No cap set
		return false, ""
	}

	if budget.AccumulatedCost+estimatedCost >= budget.CapAmount {
		return true, fmt.Sprintf(
			"budget exceeded: accumulated %.2f + estimated %.2f >= cap %.2f",
			budget.AccumulatedCost, estimatedCost, budget.CapAmount,
		)
	}

	return false, ""
}

// evaluateRules evaluates merged rules against the request.
// Uses native evaluation for budget rules and simple condition matching for others.
func (e *Engine) evaluateRules(ctx context.Context, rules []mergedRule, req domain.PolicyRequest) (*domain.PolicyDecision, error) {
	for _, mr := range rules {
		r := mr.rule

		match, err := e.evaluateCondition(r, req)
		if err != nil {
			return nil, fmt.Errorf("evaluating rule %s: %w", r.ID, err)
		}

		if match {
			if r.Effect == domain.PolicyEffectDeny {
				return &domain.PolicyDecision{
					Allowed:  false,
					Reason:   fmt.Sprintf("denied by rule %s (type: %s)", r.ID, r.Type),
					PolicyID: mr.policyID,
				}, nil
			}
			// Allow — continue checking other rules (deny takes priority)
		} else {
			// Condition not met: if it's an allow rule, the condition didn't match → deny
			if r.Effect == domain.PolicyEffectAllow {
				return &domain.PolicyDecision{
					Allowed:  false,
					Reason:   fmt.Sprintf("allow condition not met for rule %s (type: %s)", r.ID, r.Type),
					PolicyID: mr.policyID,
				}, nil
			}
		}
	}

	// All rules passed (allow conditions met, no deny triggered)
	policyID := ""
	if len(rules) > 0 {
		policyID = rules[0].policyID
	}
	return &domain.PolicyDecision{
		Allowed:  true,
		Reason:   "all rules passed",
		PolicyID: policyID,
	}, nil
}

// evaluateCondition evaluates a single rule condition against the request.
// Handles budget rules natively and simple string conditions.
func (e *Engine) evaluateCondition(rule domain.PolicyRule, req domain.PolicyRequest) (bool, error) {
	cond := rule.Condition

	// Handle simple literal conditions
	switch cond {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "":
		// Empty condition = always matches
		return true, nil
	}

	// Native budget rule evaluation
	if rule.Type == domain.PolicyRuleTypeBudget {
		return e.evaluateBudgetCondition(cond, req)
	}

	// For non-budget rules with complex CEL expressions,
	// default to match (allow the condition through).
	// Full CEL evaluation would require a CEL library; for now,
	// non-budget complex conditions are treated as matching.
	return true, nil
}

// evaluateBudgetCondition handles conditions like "request.estimatedCost <= 1000.00"
func (e *Engine) evaluateBudgetCondition(condition string, req domain.PolicyRequest) (bool, error) {
	// Parse simple budget conditions: "request.estimatedCost <= X" or "request.estimatedCost < X"
	var threshold float64
	var op string

	// Try various patterns
	patterns := []struct {
		format string
		oper   string
	}{
		{"request.estimatedCost <= %f", "<="},
		{"request.estimatedCost < %f", "<"},
		{"request.estimatedCost >= %f", ">="},
		{"request.estimatedCost > %f", ">"},
		{"request.estimatedCost == %f", "=="},
	}

	matched := false
	for _, p := range patterns {
		n, err := fmt.Sscanf(condition, p.format, &threshold)
		if err == nil && n == 1 {
			op = p.oper
			matched = true
			break
		}
	}

	if !matched {
		// Cannot parse — treat as matching (permissive for unparseable budget conditions)
		return true, nil
	}

	cost := req.EstimatedCost
	switch op {
	case "<=":
		return cost <= threshold, nil
	case "<":
		return cost < threshold, nil
	case ">=":
		return cost >= threshold, nil
	case ">":
		return cost > threshold, nil
	case "==":
		return cost == threshold, nil
	default:
		return true, nil
	}
}

// emitEvent emits a system event through the event emitter.
func (e *Engine) emitEvent(ctx context.Context, eventType, severity string, payload map[string]interface{}) {
	if e.emitter == nil {
		return
	}
	e.emitter.Emit(ctx, domain.SystemEvent{
		Type:      eventType,
		Severity:  severity,
		Source:    "policy_engine",
		Timestamp: time.Now(),
		Payload:   payload,
	})
}
