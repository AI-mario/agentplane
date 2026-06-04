package domain

import "time"

// RuntimeType represents the agent runtime.
type RuntimeType string

const (
	RuntimeClaude  RuntimeType = "claude"
	RuntimeKiro    RuntimeType = "kiro"
	RuntimeBedrock RuntimeType = "bedrock"
	RuntimeCustom  RuntimeType = "custom"
)

// AgentStatus represents agent lifecycle state.
type AgentStatus string

const (
	AgentStatusActive      AgentStatus = "active"
	AgentStatusInactive    AgentStatus = "inactive"
	AgentStatusDraining    AgentStatus = "draining"
	AgentStatusDeprecated  AgentStatus = "deprecated"
	AgentStatusUnhealthy   AgentStatus = "unhealthy"
	AgentStatusIdleSafe    AgentStatus = "idle-safe"
)

// Capability declares a tool or message type an agent supports.
type Capability struct {
	Name string
	Type string // "mcp-tool" | "a2a-message"
}

// ResourceLimits defines resource constraints for an agent.
type ResourceLimits struct {
	MaxConcurrentMissions int
	MaxMemoryMB           int
}

// DeploymentStrategy defines how an agent is deployed.
type DeploymentStrategy struct {
	Type          string // "rolling" | "canary" | "immediate"
	CanaryPercent int
	MaxInstances  int
	DrainTimeout  time.Duration
}

// AgentEntry is a registered agent in the registry.
type AgentEntry struct {
	ID           string
	Name         string
	Namespace    string
	Version      string
	RuntimeType  RuntimeType
	Capabilities []Capability
	Labels       map[string]string
	SLOs         SLODefinition
	Resources    ResourceLimits
	Deployment   DeploymentStrategy
	Status       AgentStatus
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// AgentManifest represents the YAML manifest submitted for registration.
type AgentManifest struct {
	Name         string
	Namespace    string
	Version      string
	Labels       map[string]string
	RuntimeType  RuntimeType
	Capabilities []Capability
	SLOs         SLODefinition
	Resources    ResourceLimits
	Deployment   DeploymentStrategy
}

// AgentFilter defines criteria for listing agents.
type AgentFilter struct {
	Name       string
	Capability string
	Label      map[string]string
	Status     AgentStatus
	Runtime    RuntimeType
	Limit      int
	Offset     int
}

// AgentVersion records a version registration event.
type AgentVersion struct {
	ID           string
	AgentID      string
	Version      string
	RegisteredAt time.Time
}

// DeploymentStatus represents the state of a deployment.
type DeploymentStatus string

const (
	DeploymentStatusPending   DeploymentStatus = "pending"
	DeploymentStatusRunning   DeploymentStatus = "running"
	DeploymentStatusCompleted DeploymentStatus = "completed"
	DeploymentStatusFailed    DeploymentStatus = "failed"
	DeploymentStatusRolledBack DeploymentStatus = "rolled_back"
)

// Deployment represents a deployment operation for an agent version.
type Deployment struct {
	ID            string
	AgentID       string
	Version       string
	Strategy      DeploymentStrategy
	Status        DeploymentStatus
	CanaryPercent int
	StartedAt     time.Time
	CompletedAt   *time.Time
}
