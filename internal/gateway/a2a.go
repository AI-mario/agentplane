// Package gateway implements the A2A-compatible server for inter-agent communication.
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/agentplane/agentplane/internal/communication"
	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/registry"
)

// A2AConfig holds configuration for the A2A server.
type A2AConfig struct {
	Addr string // listen address, default ":8082"
}

// A2AHandler implements the A2A-compatible server.
type A2AHandler struct {
	config   A2AConfig
	router   chi.Router
	server   *http.Server
	registry registry.AgentRegistryService
	bus      communication.CommunicationBusService
}

// NewA2AHandler creates a new A2A server with registry and communication bus dependencies.
func NewA2AHandler(
	config A2AConfig,
	reg registry.AgentRegistryService,
	bus communication.CommunicationBusService,
) *A2AHandler {
	if config.Addr == "" {
		config.Addr = ":8082"
	}

	h := &A2AHandler{
		config:   config,
		registry: reg,
		bus:      bus,
	}

	h.router = h.buildRouter()
	h.server = &http.Server{
		Addr:         config.Addr,
		Handler:      h.router,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return h
}

// Router returns the chi router (useful for testing).
func (h *A2AHandler) Router() chi.Router {
	return h.router
}

// Start begins serving A2A requests.
func (h *A2AHandler) Start() error {
	return h.server.ListenAndServe()
}

// Shutdown gracefully stops the A2A server.
func (h *A2AHandler) Shutdown(ctx context.Context) error {
	return h.server.Shutdown(ctx)
}

func (h *A2AHandler) buildRouter() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(2 * time.Second))

	r.Post("/a2a/message/send", h.handleMessageSend)

	return r
}

// a2aMessageRequest is the A2A protocol message/send request body.
type a2aMessageRequest struct {
	From    string          `json:"from"`
	To      string          `json:"to"`
	Payload json.RawMessage `json:"payload"`
}

// a2aMessageResponse is the A2A protocol message/send response.
type a2aMessageResponse struct {
	Status    string `json:"status"`
	MessageID string `json:"messageId,omitempty"`
	Error     string `json:"error,omitempty"`
}

// handleMessageSend implements POST /a2a/message/send.
// Flow: validate request → resolve target from Registry → route via Communication Bus (which does policy check internally) → return delivery status.
func (h *A2AHandler) handleMessageSend(w http.ResponseWriter, r *http.Request) {
	var req a2aMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeA2AError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	// Validate required fields.
	if req.From == "" {
		writeA2AError(w, http.StatusBadRequest, "field 'from' is required")
		return
	}
	if req.To == "" {
		writeA2AError(w, http.StatusBadRequest, "field 'to' is required")
		return
	}
	if len(req.Payload) == 0 {
		writeA2AError(w, http.StatusBadRequest, "field 'payload' is required")
		return
	}

	// Resolve target agent from registry.
	agent, err := h.registry.Get(r.Context(), req.To)
	if err != nil || agent == nil {
		writeA2AError(w, http.StatusNotFound, fmt.Sprintf("target agent %q not found in registry", req.To))
		return
	}

	// Build the message for the Communication Bus.
	// The bus internally performs: policy check → delivery with retry → DLQ on failure.
	msg := &domain.AgentMessage{
		ID:      generateMessageID(),
		From:    req.From,
		To:      req.To,
		Type:    domain.MessageTypePointToPoint,
		Payload: req.Payload,
		SentAt:  time.Now(),
	}

	// Route via Communication Bus (includes policy check).
	if err := h.bus.Send(r.Context(), msg); err != nil {
		// Differentiate error types for the response.
		switch {
		case isPolicyDenied(err):
			writeA2AResponse(w, http.StatusForbidden, a2aMessageResponse{
				Status:    "policy-denied",
				MessageID: msg.ID,
				Error:     "message delivery denied by policy",
			})
		case isAgentNotFound(err):
			writeA2AResponse(w, http.StatusNotFound, a2aMessageResponse{
				Status:    "not-found",
				MessageID: msg.ID,
				Error:     "target agent not reachable",
			})
		default:
			writeA2AResponse(w, http.StatusInternalServerError, a2aMessageResponse{
				Status:    "failed",
				MessageID: msg.ID,
				Error:     "delivery failed: " + err.Error(),
			})
		}
		return
	}

	// Successful delivery.
	writeA2AResponse(w, http.StatusOK, a2aMessageResponse{
		Status:    "delivered",
		MessageID: msg.ID,
	})
}

// isPolicyDenied checks if the error is a policy denial.
func isPolicyDenied(err error) bool {
	if err == nil {
		return false
	}
	return err.Error() == "policy-denied" || err == communication.ErrPolicyDenied
}

// isAgentNotFound checks if the error indicates agent not found.
func isAgentNotFound(err error) bool {
	if err == nil {
		return false
	}
	return err == communication.ErrAgentNotFound
}

// generateMessageID creates a unique message identifier.
func generateMessageID() string {
	return fmt.Sprintf("a2a-msg-%d", time.Now().UnixNano())
}

func writeA2AResponse(w http.ResponseWriter, status int, resp a2aMessageResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("a2a: failed to write response: %v", err)
	}
}

func writeA2AError(w http.ResponseWriter, status int, message string) {
	writeA2AResponse(w, status, a2aMessageResponse{
		Status: "error",
		Error:  message,
	})
}
