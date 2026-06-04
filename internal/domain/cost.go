package domain

import "time"

// CostEvent records resource consumption for a mission.
type CostEvent struct {
	ID              string
	MissionID       string
	AgentID         string
	TeamID          string
	ProjectID       string
	TokenCount      int64
	ComputeTimeMs   int64
	ToolInvocations int
	TotalCost       float64
	Timestamp       time.Time
}

// CostRecord is the persisted form of a cost event.
type CostRecord struct {
	ID              string
	MissionID       string
	AgentID         string
	TeamID          string
	ProjectID       string
	TokenCount      int64
	ComputeTimeMs   int64
	ToolInvocations int
	TotalCost       float64
	RecordedAt      time.Time
}

// CostFilter defines criteria for querying cost records.
type CostFilter struct {
	TeamID    string
	AgentID   string
	ProjectID string
	StartTime time.Time
	EndTime   time.Time
	Limit     int
}

// CostAggregation summarizes costs over a filter window.
type CostAggregation struct {
	TotalCost       float64
	TotalTokens     int64
	TotalComputeMs  int64
	TotalToolCalls  int
	RecordCount     int
}

// CostReport wraps query results with pagination info.
type CostReport struct {
	Records []*CostRecord
	Total   int
}

// BudgetStatus represents current budget utilization for a team.
type BudgetStatus struct {
	TeamID          string
	BudgetCap       float64
	AccumulatedCost float64
	UtilizationPct  float64
	PeriodStart     time.Time
	PeriodEnd       time.Time
	IsBlocked       bool
}

// Budget represents a team's budget configuration.
type Budget struct {
	ID              string
	TeamID          string
	CapAmount       float64
	PeriodType      string // "monthly" | "weekly"
	PeriodStart     time.Time
	AccumulatedCost float64
	IsBlocked       bool
}
