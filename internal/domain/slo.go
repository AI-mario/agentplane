package domain

import "time"

// LatencyBound defines the maximum acceptable latency in milliseconds.
type LatencyBound struct {
	MaxMs int // 1-60000
}

// AccuracyBound defines the minimum acceptable accuracy percentage.
type AccuracyBound struct {
	MinPercent float64 // 0.0-100.0
}

// CostBound defines the maximum acceptable cost per invocation.
type CostBound struct {
	MaxCost float64 // non-negative decimal
}

// SLODefinition specifies latency, accuracy, and cost bounds for an agent.
type SLODefinition struct {
	Latency  *LatencyBound
	Accuracy *AccuracyBound
	Cost     *CostBound
}

// SLOStatus represents the current SLO evaluation state for an agent.
type SLOStatus struct {
	AgentID          string
	ViolationStart   *time.Time
	ViolationMinutes int
	CurrentMetrics   map[string]float64
	TrafficReduction float64 // 0.0 = full traffic, 0.5 = 50% reduction
}

// SLOCompliance reports SLO compliance over a time window.
type SLOCompliance struct {
	AgentID        string
	Window         time.Duration
	CompliancePct  float64
	LastCalculated time.Time
}
