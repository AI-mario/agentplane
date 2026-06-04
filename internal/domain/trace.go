package domain

import "time"

// StepStatus represents the outcome of a trace step.
type StepStatus string

const (
	StepStatusSuccess StepStatus = "success"
	StepStatusFailure StepStatus = "failure"
	StepStatusSkipped StepStatus = "skipped"
)

// MissionTrace captures the full execution trace of a mission.
type MissionTrace struct {
	TraceID   string
	MissionID string
	AgentID   string
	Goal      string
	Steps     []TraceStep
	Outcome   MissionOutcome
	StartTime time.Time
	EndTime   *time.Time
	Duration  time.Duration
	ExpiresAt time.Time
}

// TraceStep represents a single span within a mission trace.
type TraceStep struct {
	SpanID       string
	TraceID      string
	ParentSpanID string
	Name         string
	StartTime    time.Time
	Duration     time.Duration
	Status       StepStatus
	Attributes   map[string]string
}

// TraceSpan is the input for adding a span to a trace.
type TraceSpan struct {
	SpanID       string
	TraceID      string
	ParentSpanID string
	Name         string
	StartTime    time.Time
	DurationMs   int64
	Status       StepStatus
	Attributes   map[string]string
}

// TraceFilter defines criteria for querying traces.
type TraceFilter struct {
	MissionID string
	AgentID   string
	Outcome   MissionOutcome
	StartTime time.Time
	EndTime   time.Time
	Limit     int
}

// DriftAlert represents a detected behavioral drift.
type DriftAlert struct {
	AgentID       string
	MetricName    string
	ObservedValue float64
	BaselineValue float64
	Deviation     float64
	DetectedAt    time.Time
}
