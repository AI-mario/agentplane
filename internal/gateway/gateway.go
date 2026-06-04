// Package gateway implements the REST API server with authentication and routing.
package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/crypto/bcrypt"

	"github.com/agentplane/agentplane/internal/cost"
	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/policy"
	"github.com/agentplane/agentplane/internal/reconciliation"
	"github.com/agentplane/agentplane/internal/registry"
	"github.com/agentplane/agentplane/internal/safety"
	"github.com/agentplane/agentplane/internal/scheduler"
)

// AuthProvider validates API keys and OAuth2 tokens.
type AuthProvider interface {
	// ValidateAPIKey validates an API key and returns the associated identity.
	ValidateAPIKey(ctx context.Context, key string) (*domain.Identity, error)
	// ValidateOAuth2Token validates an OAuth2 bearer token.
	ValidateOAuth2Token(ctx context.Context, token string) (*domain.Identity, error)
}

// APIKeyEntry stores a hashed API key with its associated identity.
type APIKeyEntry struct {
	HashedKey []byte
	Identity  domain.Identity
}

// DefaultAuthProvider implements AuthProvider with bcrypt-hashed API keys and OAuth2 token validation.
type DefaultAuthProvider struct {
	mu      sync.RWMutex
	apiKeys []APIKeyEntry

	// OAuth2TokenValidator is a pluggable function for validating OAuth2 tokens.
	// Returns the identity associated with the token, or an error if invalid.
	OAuth2TokenValidator func(ctx context.Context, token string) (*domain.Identity, error)
}

// NewDefaultAuthProvider creates a new DefaultAuthProvider.
func NewDefaultAuthProvider() *DefaultAuthProvider {
	return &DefaultAuthProvider{}
}

// AddAPIKey registers an API key (plaintext) with an associated identity.
// The key is stored as a bcrypt hash.
func (p *DefaultAuthProvider) AddAPIKey(key string, identity domain.Identity) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(key), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hashing api key: %w", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.apiKeys = append(p.apiKeys, APIKeyEntry{HashedKey: hash, Identity: identity})
	return nil
}

// ValidateAPIKey checks the provided key against stored bcrypt hashes.
func (p *DefaultAuthProvider) ValidateAPIKey(ctx context.Context, key string) (*domain.Identity, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, entry := range p.apiKeys {
		if bcrypt.CompareHashAndPassword(entry.HashedKey, []byte(key)) == nil {
			id := entry.Identity
			return &id, nil
		}
	}
	return nil, fmt.Errorf("invalid api key")
}

// ValidateOAuth2Token validates a bearer token using the configured validator.
func (p *DefaultAuthProvider) ValidateOAuth2Token(ctx context.Context, token string) (*domain.Identity, error) {
	if p.OAuth2TokenValidator == nil {
		return nil, fmt.Errorf("oauth2 validation not configured")
	}
	return p.OAuth2TokenValidator(ctx, token)
}

// Session represents a dashboard user session.
type Session struct {
	ID        string
	Identity  *domain.Identity
	CreatedAt time.Time
	LastSeen  time.Time
}

// SessionManager manages dashboard sessions with configurable idle timeout.
type SessionManager struct {
	mu          sync.RWMutex
	sessions    map[string]*Session
	idleTimeout time.Duration
}

// NewSessionManager creates a session manager with the given idle timeout.
func NewSessionManager(idleTimeout time.Duration) *SessionManager {
	if idleTimeout <= 0 {
		idleTimeout = 30 * time.Minute
	}
	return &SessionManager{
		sessions:    make(map[string]*Session),
		idleTimeout: idleTimeout,
	}
}

// Create creates a new session for the given identity and returns the session ID.
func (sm *SessionManager) Create(identity *domain.Identity) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b)
	now := time.Now()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.sessions[id] = &Session{
		ID:        id,
		Identity:  identity,
		CreatedAt: now,
		LastSeen:  now,
	}
	return id, nil
}

// Validate checks if a session is valid and not expired. Updates last seen on success.
func (sm *SessionManager) Validate(sessionID string) (*domain.Identity, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sess, ok := sm.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session not found")
	}
	if time.Since(sess.LastSeen) > sm.idleTimeout {
		delete(sm.sessions, sessionID)
		return nil, fmt.Errorf("session expired")
	}
	sess.LastSeen = time.Now()
	return sess.Identity, nil
}

// Invalidate removes a session.
func (sm *SessionManager) Invalidate(sessionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.sessions, sessionID)
}

// CleanExpired removes all expired sessions.
func (sm *SessionManager) CleanExpired() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	now := time.Now()
	for id, sess := range sm.sessions {
		if now.Sub(sess.LastSeen) > sm.idleTimeout {
			delete(sm.sessions, id)
		}
	}
}

// HealthChecker reports subsystem health.
type HealthChecker interface {
	// CheckHealth returns nil if healthy, or an error describing the issue.
	CheckHealth(ctx context.Context) error
}

// GatewayConfig holds configuration for the API gateway.
type GatewayConfig struct {
	Addr           string        // listen address, default ":8080"
	SessionTimeout time.Duration // dashboard session idle timeout, default 30 min
}

// Gateway is the REST API server.
type Gateway struct {
	config         GatewayConfig
	router         chi.Router
	server         *http.Server
	auth           AuthProvider
	sessions       *SessionManager
	registry       registry.AgentRegistryService
	scheduler      scheduler.SchedulerService
	policy         policy.PolicyEngineService
	cost           cost.CostControllerService
	safety         safety.SafetyMeshService
	reconciler     *reconciliation.Reconciler
	healthCheckers map[string]HealthChecker
	dashboardFS    http.FileSystem // embedded dashboard assets (may be nil)
}

// NewGateway creates a new API gateway with all dependencies.
func NewGateway(
	config GatewayConfig,
	auth AuthProvider,
	reg registry.AgentRegistryService,
	sched scheduler.SchedulerService,
	pol policy.PolicyEngineService,
	costCtrl cost.CostControllerService,
	safetyMesh safety.SafetyMeshService,
	reconciler *reconciliation.Reconciler,
	healthCheckers map[string]HealthChecker,
) *Gateway {
	if config.Addr == "" {
		config.Addr = ":8080"
	}
	if config.SessionTimeout <= 0 {
		config.SessionTimeout = 30 * time.Minute
	}

	gw := &Gateway{
		config:         config,
		auth:           auth,
		sessions:       NewSessionManager(config.SessionTimeout),
		registry:       reg,
		scheduler:      sched,
		policy:         pol,
		cost:           costCtrl,
		safety:         safetyMesh,
		reconciler:     reconciler,
		healthCheckers: healthCheckers,
	}

	gw.router = gw.buildRouter()
	gw.server = &http.Server{
		Addr:         config.Addr,
		Handler:      gw.router,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return gw
}

// Router returns the chi router (useful for testing).
func (gw *Gateway) Router() chi.Router {
	return gw.router
}

// Start begins serving HTTP requests.
func (gw *Gateway) Start() error {
	return gw.server.ListenAndServe()
}

// Shutdown gracefully stops the server.
func (gw *Gateway) Shutdown(ctx context.Context) error {
	return gw.server.Shutdown(ctx)
}

// SetDashboardFS sets the embedded dashboard file system for serving static assets.
// Must be called before Start(). If dashFS is nil, no dashboard is served.
func (gw *Gateway) SetDashboardFS(dashFS fs.FS) {
	if dashFS != nil {
		gw.dashboardFS = http.FS(dashFS)
		// Rebuild router with dashboard routes.
		gw.router = gw.buildRouter()
		gw.server.Handler = gw.router
	}
}

func (gw *Gateway) buildRouter() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(2 * time.Second))
	r.Use(middleware.RealIP)

	// Health endpoint — no auth required.
	r.Get("/api/v1/health", gw.handleHealth)

	// Authenticated routes.
	r.Group(func(r chi.Router) {
		r.Use(gw.authMiddleware)

		// Agents
		r.Post("/api/v1/agents", gw.handleRegisterAgent)
		r.Get("/api/v1/agents", gw.handleListAgents)
		r.Get("/api/v1/agents/{id}", gw.handleGetAgent)
		r.Delete("/api/v1/agents/{id}", gw.handleDeregisterAgent)

		// Missions
		r.Post("/api/v1/missions", gw.handleSubmitMission)
		r.Get("/api/v1/missions/{id}", gw.handleGetMission)

		// Policies
		r.Post("/api/v1/policies", gw.handleApplyPolicy)
		r.Get("/api/v1/policies/{id}", gw.handleGetPolicy)

		// Costs
		r.Get("/api/v1/costs", gw.handleQueryCosts)

		// Budgets
		r.Get("/api/v1/budgets/{teamId}", gw.handleGetBudget)

		// Fleet safety
		r.Post("/api/v1/fleet/kill-switch", gw.handleActivateKillSwitch)
		r.Delete("/api/v1/fleet/kill-switch", gw.handleDeactivateKillSwitch)
	})

	// Serve embedded dashboard assets (SPA fallback).
	if gw.dashboardFS != nil {
		fileServer := http.FileServer(gw.dashboardFS)
		r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
			// Try to serve the file directly. If not found, serve index.html (SPA).
			path := r.URL.Path
			if path == "/" {
				path = "/index.html"
			}
			// Check if the file exists in the embedded FS.
			f, err := gw.dashboardFS.Open(strings.TrimPrefix(path, "/"))
			if err == nil {
				f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
			// SPA fallback: serve index.html for any unmatched route.
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
		})
	}

	return r
}

// contextKey is a type for context keys to avoid collisions.
type contextKey string

const identityContextKey contextKey = "identity"

// authMiddleware validates API key or OAuth2 bearer token.
func (gw *Gateway) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var identity *domain.Identity
		var err error

		authHeader := r.Header.Get("Authorization")

		// Try session cookie first (for dashboard).
		if sessionID := r.Header.Get("X-Session-ID"); sessionID != "" {
			identity, err = gw.sessions.Validate(sessionID)
			if err == nil {
				ctx := context.WithValue(r.Context(), identityContextKey, identity)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}

		if authHeader == "" {
			// Also check X-API-Key header.
			apiKey := r.Header.Get("X-API-Key")
			if apiKey == "" {
				gw.logAuthFailure(r)
				writeError(w, http.StatusUnauthorized, "missing authentication credentials")
				return
			}
			identity, err = gw.auth.ValidateAPIKey(r.Context(), apiKey)
		} else if strings.HasPrefix(authHeader, "Bearer ") {
			token := strings.TrimPrefix(authHeader, "Bearer ")
			identity, err = gw.auth.ValidateOAuth2Token(r.Context(), token)
		} else if strings.HasPrefix(authHeader, "ApiKey ") {
			key := strings.TrimPrefix(authHeader, "ApiKey ")
			identity, err = gw.auth.ValidateAPIKey(r.Context(), key)
		} else {
			gw.logAuthFailure(r)
			writeError(w, http.StatusUnauthorized, "unsupported authentication scheme")
			return
		}

		if err != nil {
			gw.logAuthFailure(r)
			writeError(w, http.StatusUnauthorized, "authentication failed")
			return
		}

		ctx := context.WithValue(r.Context(), identityContextKey, identity)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (gw *Gateway) logAuthFailure(r *http.Request) {
	source := r.RemoteAddr
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		source = forwarded
	}
	log.Printf("AUTH_FAILURE source=%s timestamp=%s path=%s method=%s",
		source, time.Now().UTC().Format(time.RFC3339), r.URL.Path, r.Method)
}

// --- Health ---

func (gw *Gateway) handleHealth(w http.ResponseWriter, r *http.Request) {
	type subsystemStatus struct {
		Name    string `json:"name"`
		Healthy bool   `json:"healthy"`
		Error   string `json:"error,omitempty"`
	}

	var statuses []subsystemStatus
	allHealthy := true

	for name, checker := range gw.healthCheckers {
		err := checker.CheckHealth(r.Context())
		s := subsystemStatus{Name: name, Healthy: err == nil}
		if err != nil {
			s.Error = err.Error()
			allHealthy = false
		}
		statuses = append(statuses, s)
	}

	status := http.StatusOK
	if !allHealthy {
		status = http.StatusServiceUnavailable
	}

	writeJSON(w, status, map[string]interface{}{
		"status":     statusString(allHealthy),
		"subsystems": statuses,
	})
}

func statusString(healthy bool) string {
	if healthy {
		return "healthy"
	}
	return "unhealthy"
}

// --- Agents ---

type registerAgentRequest struct {
	Name         string              `json:"name"`
	Namespace    string              `json:"namespace"`
	Version      string              `json:"version"`
	RuntimeType  string              `json:"runtimeType"`
	Capabilities []capabilityRequest `json:"capabilities"`
	Labels       map[string]string   `json:"labels"`
	SLOs         *sloRequest         `json:"slos"`
	Resources    *resourcesRequest   `json:"resources"`
	Deployment   *deploymentRequest  `json:"deployment"`
}

type capabilityRequest struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type sloRequest struct {
	Latency  *int     `json:"latency"`
	Accuracy *float64 `json:"accuracy"`
	Cost     *float64 `json:"cost"`
}

type resourcesRequest struct {
	MaxConcurrentMissions int `json:"maxConcurrentMissions"`
	MaxMemoryMB           int `json:"maxMemoryMB"`
}

type deploymentRequest struct {
	Strategy      string `json:"strategy"`
	CanaryPercent int    `json:"canaryPercent"`
	MaxInstances  int    `json:"maxInstances"`
	DrainTimeout  string `json:"drainTimeout"`
}

func (gw *Gateway) handleRegisterAgent(w http.ResponseWriter, r *http.Request) {
	var req registerAgentRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
		return
	}

	// Validate required fields.
	var fieldErrors []fieldError
	if req.Name == "" {
		fieldErrors = append(fieldErrors, fieldError{Field: "name", Message: "required"})
	}
	if req.Version == "" {
		fieldErrors = append(fieldErrors, fieldError{Field: "version", Message: "required"})
	}
	if req.RuntimeType == "" {
		fieldErrors = append(fieldErrors, fieldError{Field: "runtimeType", Message: "required"})
	}
	if len(req.Capabilities) == 0 {
		fieldErrors = append(fieldErrors, fieldError{Field: "capabilities", Message: "at least one capability required"})
	}
	if len(fieldErrors) > 0 {
		writeValidationError(w, fieldErrors)
		return
	}

	// Build manifest.
	caps := make([]domain.Capability, len(req.Capabilities))
	for i, c := range req.Capabilities {
		caps[i] = domain.Capability{Name: c.Name, Type: c.Type}
	}

	manifest := domain.AgentManifest{
		Name:         req.Name,
		Namespace:    req.Namespace,
		Version:      req.Version,
		RuntimeType:  domain.RuntimeType(req.RuntimeType),
		Capabilities: caps,
		Labels:       req.Labels,
	}

	if req.SLOs != nil {
		if req.SLOs.Latency != nil {
			manifest.SLOs.Latency = &domain.LatencyBound{MaxMs: *req.SLOs.Latency}
		}
		if req.SLOs.Accuracy != nil {
			manifest.SLOs.Accuracy = &domain.AccuracyBound{MinPercent: *req.SLOs.Accuracy}
		}
		if req.SLOs.Cost != nil {
			manifest.SLOs.Cost = &domain.CostBound{MaxCost: *req.SLOs.Cost}
		}
	}

	if req.Resources != nil {
		manifest.Resources = domain.ResourceLimits{
			MaxConcurrentMissions: req.Resources.MaxConcurrentMissions,
			MaxMemoryMB:           req.Resources.MaxMemoryMB,
		}
	}

	if req.Deployment != nil {
		drainTimeout, _ := time.ParseDuration(req.Deployment.DrainTimeout)
		manifest.Deployment = domain.DeploymentStrategy{
			Type:          req.Deployment.Strategy,
			CanaryPercent: req.Deployment.CanaryPercent,
			MaxInstances:  req.Deployment.MaxInstances,
			DrainTimeout:  drainTimeout,
		}
	}

	// Use reconciliation loop if available (manifest apply flow).
	// Acknowledge within 5s, converge or fail within 60s, preserve previous state on failure.
	if gw.reconciler != nil {
		ackResult, resultCh, err := gw.reconciler.Apply(r.Context(), manifest)
		if err != nil {
			// Pre-flight check failed (e.g., circuit breaker open, kill switch).
			writeError(w, http.StatusConflict, err.Error())
			return
		}

		// Return acknowledge immediately with 202 Accepted.
		// The reconciliation converges asynchronously.
		response := map[string]interface{}{
			"status":    string(ackResult.Status),
			"version":   ackResult.Version,
			"startedAt": ackResult.StartedAt,
		}

		// If client wants synchronous result, wait for convergence.
		if r.URL.Query().Get("wait") == "true" {
			select {
			case result := <-resultCh:
				response["status"] = string(result.Status)
				response["agentId"] = result.AgentID
				response["duration"] = result.Duration.String()
				if result.Error != "" {
					response["error"] = result.Error
				}
				if result.Status == reconciliation.StatusConverged {
					writeJSON(w, http.StatusCreated, response)
				} else {
					writeJSON(w, http.StatusConflict, response)
				}
			case <-r.Context().Done():
				writeError(w, http.StatusGatewayTimeout, "reconciliation timeout")
			}
			return
		}

		writeJSON(w, http.StatusAccepted, response)
		return
	}

	// Fallback: direct registration without reconciliation loop.
	entry, err := gw.registry.Register(r.Context(), manifest)
	if err != nil {
		// Check if it's a conflict or validation error.
		if strings.Contains(err.Error(), "conflict") || strings.Contains(err.Error(), "duplicate") {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if strings.Contains(err.Error(), "validation") || strings.Contains(err.Error(), "invalid") {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusCreated, entry)
}

func (gw *Gateway) handleListAgents(w http.ResponseWriter, r *http.Request) {
	filter := domain.AgentFilter{
		Name:       r.URL.Query().Get("name"),
		Capability: r.URL.Query().Get("capability"),
		Runtime:    domain.RuntimeType(r.URL.Query().Get("runtime")),
	}
	if statusParam := r.URL.Query().Get("status"); statusParam != "" {
		filter.Status = domain.AgentStatus(statusParam)
	}

	// Parse labels from query params (format: label.key=value).
	labels := make(map[string]string)
	for key, values := range r.URL.Query() {
		if strings.HasPrefix(key, "label.") && len(values) > 0 {
			labels[strings.TrimPrefix(key, "label.")] = values[0]
		}
	}
	if len(labels) > 0 {
		filter.Label = labels
	}

	agents, err := gw.registry.List(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"agents": agents,
		"count":  len(agents),
	})
}

func (gw *Gateway) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	agent, err := gw.registry.Get(r.Context(), id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", id))
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, agent)
}

func (gw *Gateway) handleDeregisterAgent(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	err := gw.registry.Deregister(r.Context(), id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", id))
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deregistered", "id": id})
}

// --- Missions ---

type submitMissionRequest struct {
	RequiredCapabilities []string        `json:"requiredCapabilities"`
	Priority             int             `json:"priority"`
	TeamID               string          `json:"teamId"`
	ProjectID            string          `json:"projectId"`
	Payload              json.RawMessage `json:"payload"`
	Timeout              string          `json:"timeout"`
}

func (gw *Gateway) handleSubmitMission(w http.ResponseWriter, r *http.Request) {
	var req submitMissionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
		return
	}

	var fieldErrors []fieldError
	if len(req.RequiredCapabilities) == 0 {
		fieldErrors = append(fieldErrors, fieldError{Field: "requiredCapabilities", Message: "at least one capability required"})
	}
	if req.TeamID == "" {
		fieldErrors = append(fieldErrors, fieldError{Field: "teamId", Message: "required"})
	}
	if len(fieldErrors) > 0 {
		writeValidationError(w, fieldErrors)
		return
	}

	timeout, _ := time.ParseDuration(req.Timeout)
	if timeout == 0 {
		timeout = 3600 * time.Second
	}

	mission := &domain.Mission{
		RequiredCapabilities: req.RequiredCapabilities,
		Priority:             req.Priority,
		TeamID:               req.TeamID,
		ProjectID:            req.ProjectID,
		Payload:              req.Payload,
		Timeout:              timeout,
		SubmittedAt:          time.Now(),
	}

	assignment, err := gw.scheduler.Schedule(r.Context(), mission)
	if err != nil {
		if strings.Contains(err.Error(), "no matching agent") || strings.Contains(err.Error(), "queued") {
			writeJSON(w, http.StatusAccepted, map[string]interface{}{
				"status":    "queued",
				"missionId": mission.ID,
				"message":   err.Error(),
			})
			return
		}
		if strings.Contains(err.Error(), "budget") || strings.Contains(err.Error(), "denied") {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"missionId":  assignment.MissionID,
		"agentId":    assignment.AgentID,
		"score":      assignment.Score,
		"assignedAt": assignment.AssignedAt,
	})
}

func (gw *Gateway) handleGetMission(w http.ResponseWriter, r *http.Request) {
	// Mission retrieval is done through the scheduler or a mission store.
	// Since SchedulerService doesn't expose Get, we treat this as a not-implemented
	// pass-through that returns 404 if the mission doesn't exist.
	// In a full implementation this would query a MissionStore.
	id := chi.URLParam(r, "id")
	_ = id
	writeError(w, http.StatusNotFound, fmt.Sprintf("mission %q not found", id))
}

// --- Policies ---

type applyPolicyRequest struct {
	Name   string              `json:"name"`
	Scope  string              `json:"scope"`
	TeamID string              `json:"teamId"`
	Rules  []policyRuleRequest `json:"rules"`
}

type policyRuleRequest struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Condition string `json:"condition"`
	Effect    string `json:"effect"`
}

func (gw *Gateway) handleApplyPolicy(w http.ResponseWriter, r *http.Request) {
	var req applyPolicyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
		return
	}

	var fieldErrors []fieldError
	if req.Name == "" {
		fieldErrors = append(fieldErrors, fieldError{Field: "name", Message: "required"})
	}
	if req.Scope == "" {
		fieldErrors = append(fieldErrors, fieldError{Field: "scope", Message: "required"})
	}
	if len(req.Rules) == 0 {
		fieldErrors = append(fieldErrors, fieldError{Field: "rules", Message: "at least one rule required"})
	}
	if len(fieldErrors) > 0 {
		writeValidationError(w, fieldErrors)
		return
	}

	rules := make([]domain.PolicyRule, len(req.Rules))
	for i, r := range req.Rules {
		rules[i] = domain.PolicyRule{
			ID:        r.ID,
			Type:      domain.PolicyRuleType(r.Type),
			Condition: r.Condition,
			Effect:    domain.PolicyEffect(r.Effect),
		}
	}

	pol := &domain.Policy{
		Name:      req.Name,
		Scope:     domain.PolicyScope(req.Scope),
		TeamID:    req.TeamID,
		Rules:     rules,
		Version:   1,
		UpdatedAt: time.Now(),
	}

	if err := gw.policy.ApplyPolicy(r.Context(), pol); err != nil {
		if strings.Contains(err.Error(), "exceeds maximum") || strings.Contains(err.Error(), "too many rules") {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusCreated, pol)
}

func (gw *Gateway) handleGetPolicy(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	pol, err := gw.policy.GetPolicy(r.Context(), id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, fmt.Sprintf("policy %q not found", id))
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, pol)
}

// --- Costs ---

func (gw *Gateway) handleQueryCosts(w http.ResponseWriter, r *http.Request) {
	filter := domain.CostFilter{
		TeamID:    r.URL.Query().Get("teamId"),
		AgentID:   r.URL.Query().Get("agentId"),
		ProjectID: r.URL.Query().Get("projectId"),
		Limit:     1000,
	}

	if startTime := r.URL.Query().Get("startTime"); startTime != "" {
		if t, err := time.Parse(time.RFC3339, startTime); err == nil {
			filter.StartTime = t
		} else {
			writeError(w, http.StatusBadRequest, "invalid startTime: must be RFC3339 format")
			return
		}
	}
	if endTime := r.URL.Query().Get("endTime"); endTime != "" {
		if t, err := time.Parse(time.RFC3339, endTime); err == nil {
			filter.EndTime = t
		} else {
			writeError(w, http.StatusBadRequest, "invalid endTime: must be RFC3339 format")
			return
		}
	}

	report, err := gw.cost.QueryCosts(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// --- Budgets ---

func (gw *Gateway) handleGetBudget(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamId")
	status, err := gw.cost.GetBudgetStatus(r.Context(), teamID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, fmt.Sprintf("budget for team %q not found", teamID))
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// --- Fleet Kill Switch ---

func (gw *Gateway) handleActivateKillSwitch(w http.ResponseWriter, r *http.Request) {
	if err := gw.safety.ActivateKillSwitch(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to activate kill switch")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":      "activated",
		"activatedAt": time.Now().UTC().Format(time.RFC3339),
	})
}

func (gw *Gateway) handleDeactivateKillSwitch(w http.ResponseWriter, r *http.Request) {
	if err := gw.safety.DeactivateKillSwitch(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to deactivate kill switch")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":        "deactivated",
		"deactivatedAt": time.Now().UTC().Format(time.RFC3339),
	})
}

// --- Helpers ---

type fieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type errorResponse struct {
	Error  string       `json:"error"`
	Fields []fieldError `json:"fields,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("failed to write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}

func writeValidationError(w http.ResponseWriter, fields []fieldError) {
	writeJSON(w, http.StatusBadRequest, errorResponse{
		Error:  "validation failed",
		Fields: fields,
	})
}

func decodeJSON(r *http.Request, v interface{}) error {
	if r.Body == nil {
		return fmt.Errorf("request body is empty")
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(v)
}
