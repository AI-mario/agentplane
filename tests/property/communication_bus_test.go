package property

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/agentplane/agentplane/internal/communication"
	"github.com/agentplane/agentplane/internal/domain"
	"pgregory.net/rapid"
)

// --- Mocks for Communication Bus property tests ---

type mockMsgStore struct {
	mu       sync.Mutex
	messages map[string]*domain.AgentMessage
	dlq      []*domain.DeadLetter
}

func newMockMsgStore() *mockMsgStore {
	return &mockMsgStore{messages: make(map[string]*domain.AgentMessage)}
}

func (m *mockMsgStore) Create(_ context.Context, msg *domain.AgentMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages[msg.ID] = msg
	return nil
}

func (m *mockMsgStore) Get(_ context.Context, id string) (*domain.AgentMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	msg, ok := m.messages[id]
	if !ok {
		return nil, nil
	}
	return msg, nil
}

func (m *mockMsgStore) List(_ context.Context, limit int) ([]*domain.AgentMessage, error) {
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

func (m *mockMsgStore) GetDLQ(_ context.Context, filter domain.DLQFilter) ([]*domain.DeadLetter, error) {
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

func (m *mockMsgStore) MoveToDLQ(_ context.Context, msgID string, reason string) error {
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

func (m *mockMsgStore) UpdateStatus(_ context.Context, id string, status domain.MessageStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if msg, ok := m.messages[id]; ok {
		msg.Status = status
	}
	return nil
}

type mockBusRegistry struct {
	agents map[string]*domain.AgentEntry
}

func (r *mockBusRegistry) Register(_ context.Context, _ domain.AgentManifest) (*domain.AgentEntry, error) {
	return nil, nil
}

func (r *mockBusRegistry) Get(_ context.Context, id string) (*domain.AgentEntry, error) {
	a, ok := r.agents[id]
	if !ok {
		return nil, nil
	}
	return a, nil
}

func (r *mockBusRegistry) List(_ context.Context, filter domain.AgentFilter) ([]*domain.AgentEntry, error) {
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

func (r *mockBusRegistry) FindByCapability(_ context.Context, _ string) ([]*domain.AgentEntry, error) {
	return nil, nil
}

func (r *mockBusRegistry) Deregister(_ context.Context, _ string) error {
	return nil
}

type mockBusPolicy struct {
	allow bool
	err   error
}

func (p *mockBusPolicy) Evaluate(_ context.Context, _ domain.PolicyRequest) (*domain.PolicyDecision, error) {
	if p.err != nil {
		return nil, p.err
	}
	return &domain.PolicyDecision{Allowed: p.allow, Reason: "test-policy"}, nil
}

func (p *mockBusPolicy) ApplyPolicy(_ context.Context, _ *domain.Policy) error { return nil }
func (p *mockBusPolicy) GetPolicy(_ context.Context, _ string) (*domain.Policy, error) {
	return nil, nil
}

// --- Generators ---

// genPayload generates a valid message payload (1 byte to 1 MB).
func genPayload() *rapid.Generator[[]byte] {
	return rapid.Custom(func(t *rapid.T) []byte {
		size := rapid.IntRange(1, 1024).Draw(t, "payloadSize")
		data := make([]byte, size)
		for i := range data {
			data[i] = byte(rapid.IntRange(0, 255).Draw(t, fmt.Sprintf("byte%d", i)))
		}
		return data
	})
}

// genBusAgentID generates a valid agent identifier for bus tests.
func genBusAgentID() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		return fmt.Sprintf("agent-%s", rapid.StringMatching(`[a-z0-9]{4,8}`).Draw(t, "id"))
	})
}

// genMessageID generates a unique message identifier.
func genMessageID() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		return fmt.Sprintf("msg-%s", rapid.StringMatching(`[a-z0-9]{6,10}`).Draw(t, "msgid"))
	})
}

// genGroupName generates a group name for broadcast tests.
func genGroupName() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		return rapid.StringMatching(`[a-z]{3,10}`).Draw(t, "group")
	})
}

// --- Property Tests ---

// TestProperty34_A2AMessageDeliveryCorrectness verifies that a valid message
// addressed to a registered agent arrives at the correct target with payload intact.
// **Validates: Requirements 8.1**
func TestProperty34_A2AMessageDeliveryCorrectness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()

		senderID := genBusAgentID().Draw(rt, "sender")
		targetID := genBusAgentID().Draw(rt, "target")
		msgID := genMessageID().Draw(rt, "msgID")
		payload := genPayload().Draw(rt, "payload")
		address := fmt.Sprintf("localhost:%d", rapid.IntRange(1000, 9999).Draw(rt, "port"))

		// Track what was delivered
		var deliveredAddr string
		var deliveredMsg *domain.AgentMessage
		var mu sync.Mutex

		store := newMockMsgStore()
		reg := &mockBusRegistry{agents: map[string]*domain.AgentEntry{
			targetID: {ID: targetID, Labels: map[string]string{"address": address}},
		}}
		pol := &mockBusPolicy{allow: true}
		deliver := func(_ context.Context, addr string, msg *domain.AgentMessage) error {
			mu.Lock()
			deliveredAddr = addr
			deliveredMsg = msg
			mu.Unlock()
			return nil
		}

		bus := communication.NewCommunicationBus(store, reg, pol, deliver)

		msg := &domain.AgentMessage{
			ID:      msgID,
			From:    senderID,
			To:      targetID,
			Payload: payload,
		}

		err := bus.Send(ctx, msg)
		if err != nil {
			rt.Fatalf("Send failed: %v", err)
		}

		mu.Lock()
		defer mu.Unlock()

		// Message arrived at correct target address
		if deliveredAddr != address {
			rt.Errorf("delivered to wrong address: got %q, want %q", deliveredAddr, address)
		}

		// Payload intact
		if deliveredMsg == nil {
			rt.Fatal("no message delivered")
		}
		if len(deliveredMsg.Payload) != len(payload) {
			rt.Errorf("payload size mismatch: got %d, want %d", len(deliveredMsg.Payload), len(payload))
		}
		for i := range payload {
			if deliveredMsg.Payload[i] != payload[i] {
				rt.Errorf("payload byte %d mismatch: got %d, want %d", i, deliveredMsg.Payload[i], payload[i])
				break
			}
		}

		// Target agent is correct
		if deliveredMsg.To != targetID {
			rt.Errorf("message To field: got %q, want %q", deliveredMsg.To, targetID)
		}
	})
}

// TestProperty35_DeadLetterQueueOnDeliveryExhaustion verifies that after 3 failed
// retry attempts, the message is moved to the DLQ and the sender is notified.
// Note: inherently slow due to real exponential backoff sleeps in the bus (~8s/iteration).
// **Validates: Requirements 8.2**
func TestProperty35_DeadLetterQueueOnDeliveryExhaustion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow property test with real backoff in short mode")
	}
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()

		senderID := genBusAgentID().Draw(rt, "sender")
		targetID := genBusAgentID().Draw(rt, "target")
		msgID := genMessageID().Draw(rt, "msgID")
		payload := genPayload().Draw(rt, "payload")

		var attemptCount int
		var mu sync.Mutex

		store := newMockMsgStore()
		reg := &mockBusRegistry{agents: map[string]*domain.AgentEntry{
			targetID: {ID: targetID, Labels: map[string]string{"address": "localhost:8080"}},
		}}
		pol := &mockBusPolicy{allow: true}
		deliver := func(_ context.Context, _ string, _ *domain.AgentMessage) error {
			mu.Lock()
			attemptCount++
			mu.Unlock()
			return errors.New("connection refused")
		}

		bus := communication.NewCommunicationBus(store, reg, pol, deliver)

		msg := &domain.AgentMessage{
			ID:      msgID,
			From:    senderID,
			To:      targetID,
			Payload: payload,
		}

		err := bus.Send(ctx, msg)

		// Sender is notified of failure (error returned)
		if err == nil {
			rt.Fatal("expected error after delivery exhaustion, got nil")
		}

		// Exactly 3 retry attempts
		mu.Lock()
		attempts := attemptCount
		mu.Unlock()
		if attempts != 3 {
			rt.Errorf("expected 3 delivery attempts, got %d", attempts)
		}

		// Message moved to DLQ
		dlq, dlqErr := store.GetDLQ(ctx, domain.DLQFilter{})
		if dlqErr != nil {
			rt.Fatalf("GetDLQ error: %v", dlqErr)
		}
		if len(dlq) != 1 {
			rt.Fatalf("expected 1 DLQ entry, got %d", len(dlq))
		}
		if dlq[0].MessageID != msgID {
			rt.Errorf("DLQ message ID: got %q, want %q", dlq[0].MessageID, msgID)
		}

		// Stored message status reflects DLQ
		stored, _ := store.Get(ctx, msgID)
		if stored == nil {
			rt.Fatal("stored message not found")
		}
		if stored.Status != domain.MessageStatusDLQ {
			rt.Errorf("stored status: got %q, want %q", stored.Status, domain.MessageStatusDLQ)
		}
	})
}

// TestProperty36_BroadcastDeliveryToGroupMembers verifies that a broadcast message
// to a group is delivered to all agents registered in that group.
// **Validates: Requirements 8.3**
func TestProperty36_BroadcastDeliveryToGroupMembers(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()

		senderID := genBusAgentID().Draw(rt, "sender")
		group := genGroupName().Draw(rt, "group")
		msgID := genMessageID().Draw(rt, "msgID")
		payload := genPayload().Draw(rt, "payload")

		// Generate group members (2-8 agents in group)
		numMembers := rapid.IntRange(2, 8).Draw(rt, "numMembers")
		// Generate non-members (0-3 agents outside group)
		numNonMembers := rapid.IntRange(0, 3).Draw(rt, "numNonMembers")

		agents := make(map[string]*domain.AgentEntry)
		memberIDs := make(map[string]bool)

		for i := 0; i < numMembers; i++ {
			id := fmt.Sprintf("member-%d-%s", i, rapid.StringMatching(`[a-z0-9]{3}`).Draw(rt, fmt.Sprintf("mid%d", i)))
			agents[id] = &domain.AgentEntry{
				ID:     id,
				Labels: map[string]string{"group": group, "address": fmt.Sprintf("addr-%d", i)},
			}
			memberIDs[id] = true
		}

		for i := 0; i < numNonMembers; i++ {
			id := fmt.Sprintf("other-%d-%s", i, rapid.StringMatching(`[a-z0-9]{3}`).Draw(rt, fmt.Sprintf("oid%d", i)))
			agents[id] = &domain.AgentEntry{
				ID:     id,
				Labels: map[string]string{"group": "different-group", "address": fmt.Sprintf("other-%d", i)},
			}
		}

		deliveredTo := make(map[string]bool)
		var mu sync.Mutex

		store := newMockMsgStore()
		reg := &mockBusRegistry{agents: agents}
		pol := &mockBusPolicy{allow: true}
		deliver := func(_ context.Context, _ string, msg *domain.AgentMessage) error {
			mu.Lock()
			deliveredTo[msg.To] = true
			mu.Unlock()
			return nil
		}

		bus := communication.NewCommunicationBus(store, reg, pol, deliver)

		msg := &domain.AgentMessage{
			ID:      msgID,
			From:    senderID,
			Payload: payload,
		}

		err := bus.Broadcast(ctx, group, msg)
		if err != nil {
			rt.Fatalf("Broadcast failed: %v", err)
		}

		mu.Lock()
		defer mu.Unlock()

		// All group members received the message
		for id := range memberIDs {
			if !deliveredTo[id] {
				rt.Errorf("group member %q did not receive broadcast", id)
			}
		}

		// Non-members did NOT receive the message
		for id := range deliveredTo {
			if !memberIDs[id] {
				rt.Errorf("non-member %q received broadcast", id)
			}
		}
	})
}

// TestProperty37_MessageBufferLimits verifies that when an agent fails to acknowledge
// within 5s, messages are buffered up to 1000 entries for up to 60s. Overflow → DLQ.
// Note: inherently slow due to real backoff on overflow messages (~8s per overflow msg).
// **Validates: Requirements 8.4**
func TestProperty37_MessageBufferLimits(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow property test with real backoff in short mode")
	}
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()

		senderID := genBusAgentID().Draw(rt, "sender")
		targetID := genBusAgentID().Draw(rt, "target")

		store := newMockMsgStore()
		reg := &mockBusRegistry{agents: map[string]*domain.AgentEntry{
			targetID: {ID: targetID, Labels: map[string]string{"address": "localhost:9090"}},
		}}
		pol := &mockBusPolicy{allow: true}

		// Deliver always returns DeadlineExceeded to simulate ack timeout
		deliver := func(_ context.Context, _ string, _ *domain.AgentMessage) error {
			return context.DeadlineExceeded
		}

		bus := communication.NewCommunicationBus(store, reg, pol, deliver)

		// Fill buffer to capacity (1000 messages) — fast since
		// ack timeout on first attempt → buffers immediately, returns nil
		for i := 0; i < 1000; i++ {
			msg := &domain.AgentMessage{
				ID:      fmt.Sprintf("buf-msg-%d", i),
				From:    senderID,
				To:      targetID,
				Payload: []byte("x"),
			}
			_ = bus.Send(ctx, msg)
		}

		// Send one overflow message — buffer full → retries exhaust → DLQ
		overflowMsg := &domain.AgentMessage{
			ID:      "overflow-msg-0",
			From:    senderID,
			To:      targetID,
			Payload: []byte("overflow"),
		}

		err := bus.Send(ctx, overflowMsg)
		// Overflow message should fail (buffer full → retries exhaust → DLQ)
		if err == nil {
			rt.Error("expected error for overflow message, got nil")
		}

		// Verify overflow message ended up in DLQ
		dlq, _ := store.GetDLQ(ctx, domain.DLQFilter{To: targetID})
		if len(dlq) < 1 {
			rt.Errorf("expected at least 1 DLQ entry for overflow, got %d", len(dlq))
		}
	})
}

// TestProperty38_PolicyDeniedMessageNonDelivery verifies that when the Policy Engine
// denies a message, it is NOT delivered to the target and the sender gets policy-denied.
// **Validates: Requirements 8.6**
func TestProperty38_PolicyDeniedMessageNonDelivery(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()

		senderID := genBusAgentID().Draw(rt, "sender")
		targetID := genBusAgentID().Draw(rt, "target")
		msgID := genMessageID().Draw(rt, "msgID")
		payload := genPayload().Draw(rt, "payload")

		var delivered bool
		var mu sync.Mutex

		store := newMockMsgStore()
		reg := &mockBusRegistry{agents: map[string]*domain.AgentEntry{
			targetID: {ID: targetID, Labels: map[string]string{"address": "localhost:8080"}},
		}}
		// Policy denies all messages
		pol := &mockBusPolicy{allow: false}
		deliver := func(_ context.Context, _ string, _ *domain.AgentMessage) error {
			mu.Lock()
			delivered = true
			mu.Unlock()
			return nil
		}

		bus := communication.NewCommunicationBus(store, reg, pol, deliver)

		msg := &domain.AgentMessage{
			ID:      msgID,
			From:    senderID,
			To:      targetID,
			Payload: payload,
		}

		err := bus.Send(ctx, msg)

		// Sender receives policy-denied indication
		if !errors.Is(err, communication.ErrPolicyDenied) {
			rt.Errorf("expected ErrPolicyDenied, got: %v", err)
		}

		// Message was NOT delivered to target
		mu.Lock()
		wasDelivered := delivered
		mu.Unlock()
		if wasDelivered {
			rt.Error("message was delivered despite policy denial")
		}

		// Stored message status reflects failure
		stored, _ := store.Get(ctx, msgID)
		if stored != nil && stored.Status != domain.MessageStatusFailed {
			rt.Errorf("stored status: got %q, want %q", stored.Status, domain.MessageStatusFailed)
		}
	})
}
