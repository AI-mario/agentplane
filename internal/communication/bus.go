package communication

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/policy"
	"github.com/agentplane/agentplane/internal/registry"
	"github.com/agentplane/agentplane/internal/store"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	maxPayloadSize     = 1 << 20 // 1 MB
	maxRetries         = 3
	baseBackoff        = 1 * time.Second
	maxBackoff         = 5 * time.Second
	ackTimeout         = 5 * time.Second
	maxBufferSize      = 1000
	bufferExpiry       = 60 * time.Second
	meterName          = "agentplane.communication_bus"
)

// ErrPayloadTooLarge is returned when a message exceeds the 1 MB limit.
var ErrPayloadTooLarge = errors.New("message payload exceeds 1 MB limit")

// ErrPolicyDenied is returned when the policy engine denies delivery.
var ErrPolicyDenied = errors.New("policy-denied")

// ErrAgentNotFound is returned when target agent is not in registry.
var ErrAgentNotFound = errors.New("target agent not found in registry")

// DeliverFunc is the actual transport function that delivers a message to an agent address.
// Returns nil on success, error on failure. The address comes from registry lookup.
type DeliverFunc func(ctx context.Context, address string, msg *domain.AgentMessage) error

// ringBuffer holds messages for an unresponsive agent.
type ringBuffer struct {
	mu        sync.Mutex
	messages  []*domain.AgentMessage
	createdAt time.Time
}

func (rb *ringBuffer) add(msg *domain.AgentMessage) bool {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	if len(rb.messages) >= maxBufferSize {
		return false
	}
	rb.messages = append(rb.messages, msg)
	return true
}

func (rb *ringBuffer) drain() []*domain.AgentMessage {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	msgs := rb.messages
	rb.messages = nil
	return msgs
}

func (rb *ringBuffer) expired() bool {
	return time.Since(rb.createdAt) > bufferExpiry
}

// CommunicationBus implements CommunicationBusService.
type CommunicationBus struct {
	store    store.MessageStore
	registry registry.AgentRegistryService
	policy   policy.PolicyEngineService
	deliver  DeliverFunc

	// In-memory buffers keyed by agent ID
	buffers sync.Map // map[string]*ringBuffer

	// OTel metrics
	deliveryLatency metric.Float64Histogram
	messagesTotal   metric.Int64Counter
	dlqTotal        metric.Int64Counter
}

// NewCommunicationBus creates a new CommunicationBus.
func NewCommunicationBus(
	msgStore store.MessageStore,
	reg registry.AgentRegistryService,
	pol policy.PolicyEngineService,
	deliverFn DeliverFunc,
) *CommunicationBus {
	meter := otel.Meter(meterName)

	latencyHist, _ := meter.Float64Histogram(
		"communication_bus.delivery_latency_ms",
		metric.WithDescription("Per-message delivery latency in milliseconds"),
		metric.WithUnit("ms"),
	)
	msgCounter, _ := meter.Int64Counter(
		"communication_bus.messages_total",
		metric.WithDescription("Total messages processed"),
	)
	dlqCounter, _ := meter.Int64Counter(
		"communication_bus.dlq_total",
		metric.WithDescription("Total messages moved to DLQ"),
	)

	return &CommunicationBus{
		store:           msgStore,
		registry:        reg,
		policy:          pol,
		deliver:         deliverFn,
		deliveryLatency: latencyHist,
		messagesTotal:   msgCounter,
		dlqTotal:        dlqCounter,
	}
}

// Send delivers a point-to-point message to target agent.
func (b *CommunicationBus) Send(ctx context.Context, msg *domain.AgentMessage) error {
	if len(msg.Payload) > maxPayloadSize {
		return ErrPayloadTooLarge
	}

	msg.Type = domain.MessageTypePointToPoint
	msg.Status = domain.MessageStatusPending
	if msg.SentAt.IsZero() {
		msg.SentAt = time.Now()
	}

	// Persist message
	if err := b.store.Create(ctx, msg); err != nil {
		return fmt.Errorf("persist message: %w", err)
	}

	b.messagesTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("type", "point_to_point"),
		attribute.String("from", msg.From),
		attribute.String("to", msg.To),
	))

	// Policy check
	if err := b.checkPolicy(ctx, msg); err != nil {
		return err
	}

	// Resolve target address
	agent, err := b.registry.Get(ctx, msg.To)
	if err != nil || agent == nil {
		_ = b.store.UpdateStatus(ctx, msg.ID, domain.MessageStatusFailed)
		return ErrAgentNotFound
	}

	// Attempt delivery with retry
	return b.deliverWithRetry(ctx, msg, resolveAddress(agent))
}

// Broadcast sends message to all agents in a group (matched by label "group"=<group>).
func (b *CommunicationBus) Broadcast(ctx context.Context, group string, msg *domain.AgentMessage) error {
	if len(msg.Payload) > maxPayloadSize {
		return ErrPayloadTooLarge
	}

	msg.Type = domain.MessageTypeBroadcast
	msg.Status = domain.MessageStatusPending
	if msg.SentAt.IsZero() {
		msg.SentAt = time.Now()
	}

	// Find group members via registry label filter
	agents, err := b.registry.List(ctx, domain.AgentFilter{
		Label: map[string]string{"group": group},
	})
	if err != nil {
		return fmt.Errorf("list group agents: %w", err)
	}

	b.messagesTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("type", "broadcast"),
		attribute.String("from", msg.From),
		attribute.String("group", group),
	))

	// Policy check once for broadcast
	if err := b.checkPolicy(ctx, msg); err != nil {
		return err
	}

	var lastErr error
	for _, agent := range agents {
		// Clone message per target
		clone := *msg
		clone.To = agent.ID

		if err := b.store.Create(ctx, &clone); err != nil {
			lastErr = err
			continue
		}

		if err := b.deliverWithRetry(ctx, &clone, resolveAddress(agent)); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// GetDLQ retrieves dead letter messages matching filter.
func (b *CommunicationBus) GetDLQ(ctx context.Context, filter domain.DLQFilter) ([]*domain.DeadLetter, error) {
	return b.store.GetDLQ(ctx, filter)
}

// RetryDLQ re-attempts delivery of a dead letter message.
func (b *CommunicationBus) RetryDLQ(ctx context.Context, messageID string) error {
	msg, err := b.store.Get(ctx, messageID)
	if err != nil {
		return fmt.Errorf("get message for retry: %w", err)
	}
	if msg == nil {
		return fmt.Errorf("message %s not found", messageID)
	}

	// Reset retry count and status
	msg.RetryCount = 0
	msg.Status = domain.MessageStatusPending
	if err := b.store.UpdateStatus(ctx, msg.ID, domain.MessageStatusPending); err != nil {
		return fmt.Errorf("reset message status: %w", err)
	}

	agent, err := b.registry.Get(ctx, msg.To)
	if err != nil || agent == nil {
		return ErrAgentNotFound
	}

	return b.deliverWithRetry(ctx, msg, resolveAddress(agent))
}

// checkPolicy evaluates message delivery against the policy engine.
func (b *CommunicationBus) checkPolicy(ctx context.Context, msg *domain.AgentMessage) error {
	decision, err := b.policy.Evaluate(ctx, domain.PolicyRequest{
		Action:   "agent.communicate",
		Resource: msg.To,
		Context: map[string]interface{}{
			"from":    msg.From,
			"to":      msg.To,
			"type":    string(msg.Type),
			"payload": len(msg.Payload),
		},
	})
	if err != nil {
		// Policy error → deny by fail-safe
		_ = b.store.UpdateStatus(ctx, msg.ID, domain.MessageStatusFailed)
		return fmt.Errorf("policy evaluation error: %w", err)
	}
	if !decision.Allowed {
		_ = b.store.UpdateStatus(ctx, msg.ID, domain.MessageStatusFailed)
		return ErrPolicyDenied
	}
	return nil
}

// deliverWithRetry attempts delivery with exponential backoff; on exhaustion → DLQ.
func (b *CommunicationBus) deliverWithRetry(ctx context.Context, msg *domain.AgentMessage, address string) error {
	start := time.Now()

	for attempt := 0; attempt < maxRetries; attempt++ {
		msg.RetryCount = attempt

		deliverCtx, cancel := context.WithTimeout(ctx, ackTimeout)
		err := b.deliver(deliverCtx, address, msg)
		cancel()

		if err == nil {
			// Successful delivery
			latency := float64(time.Since(start).Milliseconds())
			b.deliveryLatency.Record(ctx, latency, metric.WithAttributes(
				attribute.String("to", msg.To),
				attribute.String("status", "delivered"),
			))
			now := time.Now()
			msg.DeliveredAt = &now
			_ = b.store.UpdateStatus(ctx, msg.ID, domain.MessageStatusDelivered)
			return nil
		}

		// Check if context cancelled
		if ctx.Err() != nil {
			break
		}

		// Exponential backoff: min(baseBackoff * 2^attempt, maxBackoff)
		backoff := baseBackoff * (1 << uint(attempt))
		if backoff > maxBackoff {
			backoff = maxBackoff
		}

		// If delivery failed due to ack timeout, buffer the message
		if isAckTimeout(err) && attempt == 0 {
			if b.bufferMessage(ctx, msg) {
				// Message buffered, will flush later
				latency := float64(time.Since(start).Milliseconds())
				b.deliveryLatency.Record(ctx, latency, metric.WithAttributes(
					attribute.String("to", msg.To),
					attribute.String("status", "buffered"),
				))
				return nil
			}
			// Buffer full → fall through to retry/DLQ logic
		}

		select {
		case <-ctx.Done():
			break
		case <-time.After(backoff):
			continue
		}
	}

	// Exhausted retries → move to DLQ and notify sender
	return b.moveToDLQ(ctx, msg, start)
}

// bufferMessage adds a message to the agent's ring buffer.
// Returns true if buffered, false if buffer is full or expired.
func (b *CommunicationBus) bufferMessage(ctx context.Context, msg *domain.AgentMessage) bool {
	val, _ := b.buffers.LoadOrStore(msg.To, &ringBuffer{
		messages:  make([]*domain.AgentMessage, 0, maxBufferSize),
		createdAt: time.Now(),
	})
	rb := val.(*ringBuffer)

	// Check if buffer has expired
	if rb.expired() {
		// Flush expired buffer to DLQ
		b.flushBufferToDLQ(ctx, msg.To, rb)
		return false
	}

	return rb.add(msg)
}

// flushBufferToDLQ moves all buffered messages for an agent to DLQ.
func (b *CommunicationBus) flushBufferToDLQ(ctx context.Context, agentID string, rb *ringBuffer) {
	msgs := rb.drain()
	b.buffers.Delete(agentID)
	for _, m := range msgs {
		_ = b.store.MoveToDLQ(ctx, m.ID, "buffer_expired: agent did not acknowledge within 60s")
		b.dlqTotal.Add(ctx, 1, metric.WithAttributes(
			attribute.String("to", m.To),
			attribute.String("reason", "buffer_expired"),
		))
	}
}

// FlushExpiredBuffers should be called periodically to clean up expired buffers.
func (b *CommunicationBus) FlushExpiredBuffers(ctx context.Context) {
	b.buffers.Range(func(key, value interface{}) bool {
		agentID := key.(string)
		rb := value.(*ringBuffer)
		if rb.expired() {
			b.flushBufferToDLQ(ctx, agentID, rb)
		}
		return true
	})
}

// moveToDLQ moves a failed message to the dead letter queue and records metrics.
func (b *CommunicationBus) moveToDLQ(ctx context.Context, msg *domain.AgentMessage, start time.Time) error {
	reason := fmt.Sprintf("delivery failed after %d retries", maxRetries)
	if err := b.store.MoveToDLQ(ctx, msg.ID, reason); err != nil {
		return fmt.Errorf("move to DLQ: %w", err)
	}

	latency := float64(time.Since(start).Milliseconds())
	b.deliveryLatency.Record(ctx, latency, metric.WithAttributes(
		attribute.String("to", msg.To),
		attribute.String("status", "dlq"),
	))
	b.dlqTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("to", msg.To),
		attribute.String("reason", "retries_exhausted"),
	))

	// Notify sender of delivery failure (best-effort via store status)
	_ = b.store.UpdateStatus(ctx, msg.ID, domain.MessageStatusDLQ)

	return fmt.Errorf("message %s moved to DLQ: %s", msg.ID, reason)
}

// resolveAddress extracts the delivery address for an agent.
// Uses agent ID as a fallback address if no explicit endpoint is configured.
func resolveAddress(agent *domain.AgentEntry) string {
	// Convention: label "address" holds the agent endpoint
	if addr, ok := agent.Labels["address"]; ok {
		return addr
	}
	return agent.ID
}

// isAckTimeout checks if an error indicates an acknowledgment timeout.
func isAckTimeout(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.DeadlineExceeded)
}
