package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentplane/agentplane/internal/communication"
	"github.com/agentplane/agentplane/internal/domain"
)

// --- Mock communication bus for A2A tests ---

type mockCommunicationBus struct {
	sent      []*domain.AgentMessage
	sendErr   error
	policyErr bool
}

func (m *mockCommunicationBus) Send(ctx context.Context, msg *domain.AgentMessage) error {
	if m.policyErr {
		return communication.ErrPolicyDenied
	}
	if m.sendErr != nil {
		return m.sendErr
	}
	m.sent = append(m.sent, msg)
	return nil
}

func (m *mockCommunicationBus) Broadcast(ctx context.Context, group string, msg *domain.AgentMessage) error {
	return nil
}

func (m *mockCommunicationBus) GetDLQ(ctx context.Context, filter domain.DLQFilter) ([]*domain.DeadLetter, error) {
	return nil, nil
}

func (m *mockCommunicationBus) RetryDLQ(ctx context.Context, messageID string) error {
	return nil
}

// --- Test helpers ---

func setupA2AHandler() (*A2AHandler, *mockRegistry, *mockCommunicationBus) {
	reg := newMockRegistry()
	reg.agents["agent-target"] = &domain.AgentEntry{
		ID:      "agent-target",
		Name:    "target-agent",
		Version: "1.0.0",
		Status:  domain.AgentStatusActive,
		Labels:  map[string]string{"address": "http://localhost:9000"},
	}
	reg.agents["agent-sender"] = &domain.AgentEntry{
		ID:      "agent-sender",
		Name:    "sender-agent",
		Version: "1.0.0",
		Status:  domain.AgentStatusActive,
	}

	bus := &mockCommunicationBus{}

	handler := NewA2AHandler(A2AConfig{Addr: ":8082"}, reg, bus)
	return handler, reg, bus
}

// --- Tests ---

func TestA2AMessageSendSuccess(t *testing.T) {
	handler, _, bus := setupA2AHandler()

	body := `{"from": "agent-sender", "to": "agent-target", "payload": "hello agent"}`
	req := httptest.NewRequest(http.MethodPost, "/a2a/message/send", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp a2aMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json response: %v", err)
	}
	if resp.Status != "delivered" {
		t.Fatalf("expected status 'delivered', got %q", resp.Status)
	}
	if resp.MessageID == "" {
		t.Fatal("expected non-empty messageId")
	}

	// Verify message was sent via bus.
	if len(bus.sent) != 1 {
		t.Fatalf("expected 1 message sent via bus, got %d", len(bus.sent))
	}
	if bus.sent[0].From != "agent-sender" {
		t.Fatalf("expected from 'agent-sender', got %q", bus.sent[0].From)
	}
	if bus.sent[0].To != "agent-target" {
		t.Fatalf("expected to 'agent-target', got %q", bus.sent[0].To)
	}
}

func TestA2AMessageSendMissingFrom(t *testing.T) {
	handler, _, _ := setupA2AHandler()

	body := `{"to": "agent-target", "payload": "hello"}`
	req := httptest.NewRequest(http.MethodPost, "/a2a/message/send", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}

	var resp a2aMessageResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Status != "error" {
		t.Fatalf("expected status 'error', got %q", resp.Status)
	}
}

func TestA2AMessageSendMissingTo(t *testing.T) {
	handler, _, _ := setupA2AHandler()

	body := `{"from": "agent-sender", "payload": "hello"}`
	req := httptest.NewRequest(http.MethodPost, "/a2a/message/send", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestA2AMessageSendMissingPayload(t *testing.T) {
	handler, _, _ := setupA2AHandler()

	body := `{"from": "agent-sender", "to": "agent-target"}`
	req := httptest.NewRequest(http.MethodPost, "/a2a/message/send", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestA2AMessageSendTargetNotFound(t *testing.T) {
	handler, _, _ := setupA2AHandler()

	body := `{"from": "agent-sender", "to": "nonexistent-agent", "payload": "hello"}`
	req := httptest.NewRequest(http.MethodPost, "/a2a/message/send", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp a2aMessageResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Status != "error" {
		t.Fatalf("expected status 'error', got %q", resp.Status)
	}
}

func TestA2AMessageSendPolicyDenied(t *testing.T) {
	handler, _, bus := setupA2AHandler()
	bus.policyErr = true

	body := `{"from": "agent-sender", "to": "agent-target", "payload": "hello"}`
	req := httptest.NewRequest(http.MethodPost, "/a2a/message/send", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp a2aMessageResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Status != "policy-denied" {
		t.Fatalf("expected status 'policy-denied', got %q", resp.Status)
	}
}

func TestA2AMessageSendDeliveryFailure(t *testing.T) {
	handler, _, bus := setupA2AHandler()
	bus.sendErr = fmt.Errorf("message msg-1 moved to DLQ: delivery failed after 3 retries")

	body := `{"from": "agent-sender", "to": "agent-target", "payload": "hello"}`
	req := httptest.NewRequest(http.MethodPost, "/a2a/message/send", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp a2aMessageResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Status != "failed" {
		t.Fatalf("expected status 'failed', got %q", resp.Status)
	}
}

func TestA2AMessageSendInvalidJSON(t *testing.T) {
	handler, _, _ := setupA2AHandler()

	body := `{invalid`
	req := httptest.NewRequest(http.MethodPost, "/a2a/message/send", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestA2AMessageSendAgentNotFoundFromBus(t *testing.T) {
	handler, _, bus := setupA2AHandler()
	bus.sendErr = communication.ErrAgentNotFound

	body := `{"from": "agent-sender", "to": "agent-target", "payload": "hello"}`
	req := httptest.NewRequest(http.MethodPost, "/a2a/message/send", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp a2aMessageResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Status != "not-found" {
		t.Fatalf("expected status 'not-found', got %q", resp.Status)
	}
}

func TestA2ADefaultAddr(t *testing.T) {
	reg := newMockRegistry()
	bus := &mockCommunicationBus{}
	handler := NewA2AHandler(A2AConfig{}, reg, bus)

	if handler.config.Addr != ":8082" {
		t.Fatalf("expected default addr ':8082', got %q", handler.config.Addr)
	}
}

func TestA2AShutdown(t *testing.T) {
	reg := newMockRegistry()
	bus := &mockCommunicationBus{}
	handler := NewA2AHandler(A2AConfig{Addr: ":0"}, reg, bus)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// Should not error on shutdown before start.
	if err := handler.Shutdown(ctx); err != nil {
		t.Fatalf("unexpected shutdown error: %v", err)
	}
}
