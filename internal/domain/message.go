package domain

import "time"

// MessageType classifies the delivery mode of a message.
type MessageType string

const (
	MessageTypePointToPoint MessageType = "point_to_point"
	MessageTypeBroadcast    MessageType = "broadcast"
)

// MessageStatus represents the delivery state of a message.
type MessageStatus string

const (
	MessageStatusPending   MessageStatus = "pending"
	MessageStatusDelivered MessageStatus = "delivered"
	MessageStatusFailed    MessageStatus = "failed"
	MessageStatusDLQ       MessageStatus = "dlq"
)

// AgentMessage represents an inter-agent communication.
type AgentMessage struct {
	ID         string
	From       string
	To         string
	Type       MessageType
	Payload    []byte
	Status     MessageStatus
	SentAt     time.Time
	DeliveredAt *time.Time
	RetryCount int
}

// DeadLetter wraps a failed message with failure context.
type DeadLetter struct {
	ID              string
	MessageID       string
	OriginalMessage *AgentMessage
	FailureReason   string
	FailedAt        time.Time
	RetryAttempts   int
}

// DLQFilter defines criteria for querying dead letter messages.
type DLQFilter struct {
	From   string
	To     string
	Limit  int
	Offset int
}
