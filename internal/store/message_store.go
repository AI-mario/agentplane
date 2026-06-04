package store

import (
	"context"

	"github.com/agentplane/agentplane/internal/domain"
)

// MessageStore defines persistence operations for inter-agent messages and dead letters.
type MessageStore interface {
	Create(ctx context.Context, msg *domain.AgentMessage) error
	Get(ctx context.Context, id string) (*domain.AgentMessage, error)
	List(ctx context.Context, limit int) ([]*domain.AgentMessage, error)
	GetDLQ(ctx context.Context, filter domain.DLQFilter) ([]*domain.DeadLetter, error)
	MoveToDLQ(ctx context.Context, msgID string, reason string) error
	UpdateStatus(ctx context.Context, id string, status domain.MessageStatus) error
}
