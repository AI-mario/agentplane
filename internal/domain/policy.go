package domain

import "time"

// PolicyScope defines policy applicability level.
type PolicyScope string

const (
	PolicyScopeOrganization PolicyScope = "organization"
	PolicyScopeTeam         PolicyScope = "team"
)

// PolicyRuleType categorizes what a rule controls.
type PolicyRuleType string

const (
	PolicyRuleTypeBudget     PolicyRuleType = "budget"
	PolicyRuleTypeCapability PolicyRuleType = "capability"
	PolicyRuleTypeRuntime    PolicyRuleType = "runtime"
	PolicyRuleTypeDataAccess PolicyRuleType = "data_access"
)

// PolicyEffect is the outcome of a policy rule match.
type PolicyEffect string

const (
	PolicyEffectAllow PolicyEffect = "allow"
	PolicyEffectDeny  PolicyEffect = "deny"
)

// Policy represents a set of rules governing fleet operations.
type Policy struct {
	ID        string
	Name      string
	Scope     PolicyScope
	TeamID    string
	Rules     []PolicyRule
	Version   int
	UpdatedAt time.Time
}

// PolicyRule is a single rule within a policy.
type PolicyRule struct {
	ID        string
	Type      PolicyRuleType
	Condition string // CEL expression
	Effect    PolicyEffect
}

// PolicyRequest is the input to a policy evaluation.
type PolicyRequest struct {
	Action        string
	Subject       *Identity
	Resource      string
	Context       map[string]interface{}
	EstimatedCost float64
}

// PolicyDecision is the result of a policy evaluation.
type PolicyDecision struct {
	Allowed  bool
	Reason   string
	PolicyID string
	EvalTime time.Duration
}

// PolicyFilter defines criteria for listing policies.
type PolicyFilter struct {
	Scope  PolicyScope
	TeamID string
	Limit  int
	Offset int
}
