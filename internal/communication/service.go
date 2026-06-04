package communication

import (
	"context"

	"github.com/agentplane/agentplane/internal/domain"
)

// CommunicationBusService routes A2A messages with dead letter queue support.
type CommunicationBusService interface {
	// Send delivers a message to a target agent.
	Send(ctx context.Context, msg *domain.AgentMessage) error
	// Broadcast sends a message to all agents in a group.
	Broadcast(ctx context.Context, group string, msg *domain.AgentMessage) error
	// GetDLQ retrieves messages from the dead letter queue.
	GetDLQ(ctx context.Context, filter domain.DLQFilter) ([]*domain.DeadLetter, error)
	// RetryDLQ re-attempts delivery of a dead letter message.
	RetryDLQ(ctx context.Context, messageID string) error
}
