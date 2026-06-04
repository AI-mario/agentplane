package domain

import "time"

// SystemEvent is a structured event emitted by control plane components.
type SystemEvent struct {
	Type      string                 // e.g., "budget_exceeded", "slo_breach", "drift_detected"
	Severity  string                 // "info" | "warning" | "error" | "critical"
	Source    string                 // component that emitted the event
	Timestamp time.Time
	Payload   map[string]interface{}
}
