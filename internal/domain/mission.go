package domain

import "time"

// MissionStatus represents mission lifecycle state.
type MissionStatus string

const (
	MissionStatusPending   MissionStatus = "pending"
	MissionStatusAssigned  MissionStatus = "assigned"
	MissionStatusRunning   MissionStatus = "running"
	MissionStatusCompleted MissionStatus = "completed"
	MissionStatusFailed    MissionStatus = "failed"
	MissionStatusCancelled MissionStatus = "cancelled"
	MissionStatusQueued    MissionStatus = "queued"
)

// MissionOutcome represents how a mission ended.
type MissionOutcome string

const (
	OutcomeSuccess MissionOutcome = "success"
	OutcomeFailure MissionOutcome = "failure"
	OutcomePartial MissionOutcome = "partial"
	OutcomeTimeout MissionOutcome = "timeout"
)

// Mission represents a goal-oriented unit of work.
type Mission struct {
	ID                   string
	AgentID              string
	TeamID               string
	ProjectID            string
	Status               MissionStatus
	RequiredCapabilities []string
	Payload              []byte
	AssignmentRationale  *SelectionRationale
	Priority             int
	Timeout              time.Duration
	SubmittedAt          time.Time
	AssignedAt           *time.Time
	CompletedAt          *time.Time
}

// MissionFilter defines criteria for listing missions.
type MissionFilter struct {
	AgentID   string
	TeamID    string
	Status    MissionStatus
	Limit     int
	Offset    int
}

// Assignment represents a mission-to-agent binding.
type Assignment struct {
	MissionID  string
	AgentID    string
	Score      float64
	Rationale  SelectionRationale
	AssignedAt time.Time
}

// SelectionRationale explains why an agent was selected.
type SelectionRationale struct {
	CostScore    float64
	LatencyScore float64
	LoadScore    float64
	Weights      ScoringWeights
}

// ScoringWeights defines the relative importance of scoring dimensions.
type ScoringWeights struct {
	Cost    float64
	Latency float64
	Load    float64
}
