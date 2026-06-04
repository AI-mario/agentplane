package communication

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
)

// --- Mocks ---

type mockMessageStore struct {
	mu       sync.Mutex
	messages map[string]*domain.AgentMessage
	dlq      []*domain.DeadLetter
}

func newMockMessageStore() *mockMessageStore {
	return &mockMessageStore{messages: make(map[string]*domain.AgentMessage)}
}

func (m *mockMessageStore) Create(_ context.Context, msg *domain.AgentMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages[msg.ID] = msg
	return nil
}

func (m *mockMessageStore) Get(_ context.Context, id string) (*domain.AgentMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	msg, ok := m.messages[id]
	if !ok {
		return nil, nil
	}
	return msg, nil
}

func (m *mockMessageStore) List(_ context.Context, limit int) ([]*domain.AgentMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*domain.AgentMessage
	for _, msg := range m.messages {
		result = append(result, msg)
		if len(result) >= limit {
			break
		}
	}
	return result, nil
}

func (m *mockMessageStore) GetDLQ(_ context.Context, filter domain.DLQFilter) ([]*domain.DeadLetter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*domain.DeadLetter
	for _, dl := range m.dlq {
		if filter.From != "" && dl.OriginalMessage.From != filter.From {
			continue
		}
		if filter.To != "" && dl.OriginalMessage.To != filter.To {
			continue
		}
		result = append(result, dl)
	}
	if filter.Limit > 0 && len(result) > filter.Limit {
		result = result[:filter.Limit]
	}
	return result, nil
}

func (m *mockMessageStore) MoveToDLQ(_ context.Context, msgID string, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	msg, ok := m.messages[msgID]
	if !ok {
		return errors.New("message not found")
	}
	msg.Status = domain.MessageStatusDLQ
	m.dlq = append(m.dlq, &domain.DeadLetter{
		ID:              "dlq-" + msgID,
		MessageID:       msgID,
		OriginalMessage: msg,
		FailureReason:   reason,
		FailedAt:        time.Now(),
		RetryAttempts:   msg.RetryCount,
	})
	return nil
}

func (m *mockMessageStore) UpdateStatus(_ context.Context, id string, status domain.MessageStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if msg, ok := m.messages[id]; ok {
		msg.Status = status
	}
	return nil
}

type mockRegistry struct {
	agents map[string]*domain.AgentEntry
}

func (r *mockRegistry) Register(_ context.Context, _ domain.AgentManifest) (*domain.AgentEntry, error) {
	return nil, nil
}

func (r *mockRegistry) Get(_ context.Context, id string) (*domain.AgentEntry, error) {
	a, ok := r.agents[id]
	if !ok {
		return nil, nil
	}
	return a, nil
}

func (r *mockRegistry) List(_ context.Context, filter domain.AgentFilter) ([]*domain.AgentEntry, error) {
	var result []*domain.AgentEntry
	for _, a := range r.agents {
		if filter.Label != nil {
			match := true
			for k, v := range filter.Label {
				if a.Labels[k] != v {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		}
		result = append(result, a)
	}
	return result, nil
}

func (r *mockRegistry) FindByCapability(_ context.Context, _ string) ([]*domain.AgentEntry, error) {
	return nil, nil
}

func (r *mockRegistry) Deregister(_ context.Context, _ string) error {
	return nil
}

type mockPolicy struct {
	allow bool
	err   error
}

func (p *mockPolicy) Evaluate(_ context.Context, _ domain.PolicyRequest) (*domain.PolicyDecision, error) {
	if p.err != nil {
		return nil, p.err
	}
	return &domain.PolicyDecision{Allowed: p.allow, Reason: "test"}, nil
}

func (p *mockPolicy) ApplyPolicy(_ context.Context, _ *domain.Policy) error { return nil }
func (p *mockPolicy) GetPolicy(_ context.Context, _ string) (*domain.Policy, error) {
	return nil, nil
}

// --- Tests ---

func TestSend_Success(t *testing.T) {
	ms := newMockMessageStore()
	reg := &mockRegistry{agents: map[string]*domain.AgentEntry{
		"agent-1": {ID: "agent-1", Labels: map[string]string{"address": "localhost:8080"}},
	}}
	pol := &mockPolicy{allow: true}
	deliver := func(_ context.Context, _ string, _ *domain.AgentMessage) error {
		return nil
	}

	bus := NewCommunicationBus(ms, reg, pol, deliver)
	msg := &domain.AgentMessage{
		ID:      "msg-1",
		From:    "agent-0",
		To:      "agent-1",
		Payload: []byte("hello"),
	}

	err := bus.Send(context.Background(), msg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	stored, _ := ms.Get(context.Background(), "msg-1")
	if stored.Status != domain.MessageStatusDelivered {
		t.Errorf("expected status delivered, got %s", stored.Status)
	}
}

func TestSend_PayloadTooLarge(t *testing.T) {
	ms := newMockMessageStore()
	reg := &mockRegistry{agents: map[string]*domain.AgentEntry{}}
	pol := &mockPolicy{allow: true}
	deliver := func(_ context.Context, _ string, _ *domain.AgentMessage) error { return nil }

	bus := NewCommunicationBus(ms, reg, pol, deliver)
	msg := &domain.AgentMessage{
		ID:      "msg-big",
		From:    "agent-0",
		To:      "agent-1",
		Payload: make([]byte, maxPayloadSize+1),
	}

	err := bus.Send(context.Background(), msg)
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("expected ErrPayloadTooLarge, got %v", err)
	}
}

func TestSend_PolicyDenied(t *testing.T) {
	ms := newMockMessageStore()
	reg := &mockRegistry{agents: map[string]*domain.AgentEntry{
		"agent-1": {ID: "agent-1", Labels: map[string]string{}},
	}}
	pol := &mockPolicy{allow: false}
	deliver := func(_ context.Context, _ string, _ *domain.AgentMessage) error { return nil }

	bus := NewCommunicationBus(ms, reg, pol, deliver)
	msg := &domain.AgentMessage{
		ID:      "msg-deny",
		From:    "agent-0",
		To:      "agent-1",
		Payload: []byte("secret"),
	}

	err := bus.Send(context.Background(), msg)
	if !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected ErrPolicyDenied, got %v", err)
	}
}

func TestSend_AgentNotFound(t *testing.T) {
	ms := newMockMessageStore()
	reg := &mockRegistry{agents: map[string]*domain.AgentEntry{}}
	pol := &mockPolicy{allow: true}
	deliver := func(_ context.Context, _ string, _ *domain.AgentMessage) error { return nil }

	bus := NewCommunicationBus(ms, reg, pol, deliver)
	msg := &domain.AgentMessage{
		ID:      "msg-nf",
		From:    "agent-0",
		To:      "agent-unknown",
		Payload: []byte("hello"),
	}

	err := bus.Send(context.Background(), msg)
	if !errors.Is(err, ErrAgentNotFound) {
		t.Fatalf("expected ErrAgentNotFound, got %v", err)
	}
}

func TestSend_RetriesExhausted_MovesToDLQ(t *testing.T) {
	ms := newMockMessageStore()
	reg := &mockRegistry{agents: map[string]*domain.AgentEntry{
		"agent-1": {ID: "agent-1", Labels: map[string]string{"address": "localhost:9090"}},
	}}
	pol := &mockPolicy{allow: true}
	attempts := 0
	deliver := func(_ context.Context, _ string, _ *domain.AgentMessage) error {
		attempts++
		return errors.New("connection refused")
	}

	bus := NewCommunicationBus(ms, reg, pol, deliver)
	msg := &domain.AgentMessage{
		ID:      "msg-retry",
		From:    "agent-0",
		To:      "agent-1",
		Payload: []byte("hello"),
	}

	err := bus.Send(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if attempts != maxRetries {
		t.Errorf("expected %d attempts, got %d", maxRetries, attempts)
	}

	dlq, _ := ms.GetDLQ(context.Background(), domain.DLQFilter{})
	if len(dlq) != 1 {
		t.Fatalf("expected 1 DLQ entry, got %d", len(dlq))
	}
	if dlq[0].MessageID != "msg-retry" {
		t.Errorf("expected DLQ message ID msg-retry, got %s", dlq[0].MessageID)
	}
}

func TestSend_AckTimeout_BuffersMessage(t *testing.T) {
	ms := newMockMessageStore()
	reg := &mockRegistry{agents: map[string]*domain.AgentEntry{
		"agent-1": {ID: "agent-1", Labels: map[string]string{"address": "localhost:9090"}},
	}}
	pol := &mockPolicy{allow: true}
	deliver := func(ctx context.Context, _ string, _ *domain.AgentMessage) error {
		// Simulate ack timeout by exceeding the context deadline
		return context.DeadlineExceeded
	}

	bus := NewCommunicationBus(ms, reg, pol, deliver)
	msg := &domain.AgentMessage{
		ID:      "msg-buf",
		From:    "agent-0",
		To:      "agent-1",
		Payload: []byte("hello"),
	}

	err := bus.Send(context.Background(), msg)
	if err != nil {
		t.Fatalf("expected nil (buffered), got %v", err)
	}

	// Verify message is in buffer
	val, ok := bus.buffers.Load("agent-1")
	if !ok {
		t.Fatal("expected buffer for agent-1")
	}
	rb := val.(*ringBuffer)
	rb.mu.Lock()
	count := len(rb.messages)
	rb.mu.Unlock()
	if count != 1 {
		t.Errorf("expected 1 buffered message, got %d", count)
	}
}

func TestBroadcast_SendsToGroup(t *testing.T) {
	ms := newMockMessageStore()
	reg := &mockRegistry{agents: map[string]*domain.AgentEntry{
		"agent-1": {ID: "agent-1", Labels: map[string]string{"group": "reviewers", "address": "a1"}},
		"agent-2": {ID: "agent-2", Labels: map[string]string{"group": "reviewers", "address": "a2"}},
		"agent-3": {ID: "agent-3", Labels: map[string]string{"group": "builders", "address": "a3"}},
	}}
	pol := &mockPolicy{allow: true}
	delivered := make(map[string]bool)
	var mu sync.Mutex
	deliver := func(_ context.Context, addr string, _ *domain.AgentMessage) error {
		mu.Lock()
		delivered[addr] = true
		mu.Unlock()
		return nil
	}

	bus := NewCommunicationBus(ms, reg, pol, deliver)
	msg := &domain.AgentMessage{
		ID:      "msg-bc",
		From:    "agent-0",
		Payload: []byte("broadcast"),
	}

	err := bus.Broadcast(context.Background(), "reviewers", msg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !delivered["a1"] || !delivered["a2"] {
		t.Errorf("expected delivery to a1 and a2, got %v", delivered)
	}
	if delivered["a3"] {
		t.Error("should not deliver to agent-3 (different group)")
	}
}

func TestRetryDLQ_Success(t *testing.T) {
	ms := newMockMessageStore()
	reg := &mockRegistry{agents: map[string]*domain.AgentEntry{
		"agent-1": {ID: "agent-1", Labels: map[string]string{"address": "localhost:8080"}},
	}}
	pol := &mockPolicy{allow: true}
	deliver := func(_ context.Context, _ string, _ *domain.AgentMessage) error {
		return nil
	}

	bus := NewCommunicationBus(ms, reg, pol, deliver)

	// Pre-populate a message in the store
	msg := &domain.AgentMessage{
		ID:      "msg-dlq-retry",
		From:    "agent-0",
		To:      "agent-1",
		Payload: []byte("hello"),
		Status:  domain.MessageStatusDLQ,
	}
	_ = ms.Create(context.Background(), msg)

	err := bus.RetryDLQ(context.Background(), "msg-dlq-retry")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	stored, _ := ms.Get(context.Background(), "msg-dlq-retry")
	if stored.Status != domain.MessageStatusDelivered {
		t.Errorf("expected delivered after retry, got %s", stored.Status)
	}
}

func TestBufferOverflow_MovesToDLQ(t *testing.T) {
	ms := newMockMessageStore()
	reg := &mockRegistry{agents: map[string]*domain.AgentEntry{
		"agent-1": {ID: "agent-1", Labels: map[string]string{"address": "localhost:9090"}},
	}}
	pol := &mockPolicy{allow: true}
	deliver := func(_ context.Context, _ string, _ *domain.AgentMessage) error {
		return context.DeadlineExceeded
	}

	bus := NewCommunicationBus(ms, reg, pol, deliver)

	// Fill buffer to capacity
	for i := 0; i < maxBufferSize; i++ {
		msg := &domain.AgentMessage{
			ID:      fmt.Sprintf("msg-%d", i),
			From:    "agent-0",
			To:      "agent-1",
			Payload: []byte("x"),
		}
		_ = bus.Send(context.Background(), msg)
	}

	// Next message should fail because buffer is full → retries exhaust → DLQ
	msg := &domain.AgentMessage{
		ID:      "msg-overflow",
		From:    "agent-0",
		To:      "agent-1",
		Payload: []byte("overflow"),
	}
	err := bus.Send(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error when buffer overflows")
	}

	dlq, _ := ms.GetDLQ(context.Background(), domain.DLQFilter{To: "agent-1"})
	if len(dlq) == 0 {
		t.Error("expected at least one DLQ entry after overflow")
	}
}

func TestFlushExpiredBuffers(t *testing.T) {
	ms := newMockMessageStore()
	reg := &mockRegistry{agents: map[string]*domain.AgentEntry{
		"agent-1": {ID: "agent-1", Labels: map[string]string{"address": "localhost:9090"}},
	}}
	pol := &mockPolicy{allow: true}
	deliver := func(_ context.Context, _ string, _ *domain.AgentMessage) error {
		return context.DeadlineExceeded
	}

	bus := NewCommunicationBus(ms, reg, pol, deliver)

	// Manually create an expired buffer
	rb := &ringBuffer{
		messages:  []*domain.AgentMessage{{ID: "old-msg", From: "a", To: "agent-1"}},
		createdAt: time.Now().Add(-2 * bufferExpiry),
	}
	bus.buffers.Store("agent-1", rb)

	// Create the message in store so MoveToDLQ works
	_ = ms.Create(context.Background(), &domain.AgentMessage{ID: "old-msg", From: "a", To: "agent-1"})

	bus.FlushExpiredBuffers(context.Background())

	dlq, _ := ms.GetDLQ(context.Background(), domain.DLQFilter{})
	if len(dlq) != 1 {
		t.Fatalf("expected 1 DLQ entry from expired buffer, got %d", len(dlq))
	}
}
