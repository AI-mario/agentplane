package domain

import "time"

// CBState represents the state of a circuit breaker.
type CBState string

const (
	CBClosed   CBState = "closed"
	CBOpen     CBState = "open"
	CBHalfOpen CBState = "half_open"
)

// CircuitBreakerState tracks the circuit breaker for an individual agent.
type CircuitBreakerState struct {
	AgentID      string
	State        CBState
	ErrorRate    float64
	OpenedAt     *time.Time
	CooldownEnd  *time.Time
	CooldownSecs int // 10-3600, default 60
}

// ProbeResult is the outcome of a probe mission sent to a circuit-broken agent.
type ProbeResult struct {
	AgentID   string
	Success   bool
	Error     string
	ProbedAt  time.Time
	Duration  time.Duration
}
