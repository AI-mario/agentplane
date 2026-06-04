package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/registry"
	"github.com/agentplane/agentplane/internal/store"
	"github.com/google/uuid"
)

// Errors returned by the lifecycle manager.
var (
	ErrDeploymentTimeout    = errors.New("deployment timed out")
	ErrInvalidReplicas      = errors.New("replicas must be between 1 and 100")
	ErrInvalidCanaryPercent = errors.New("canary percent must be between 1 and 50")
	ErrNoPreviousVersion    = errors.New("no previous version available for rollback")
	ErrAgentNotFound        = errors.New("agent not found")
)

// EventEmitter emits system events for lifecycle operations.
type EventEmitter interface {
	Emit(event domain.SystemEvent)
}

// CanaryMetrics holds monitoring data for a canary deployment.
type CanaryMetrics struct {
	mu              sync.RWMutex
	totalRequests   int
	errorCount      int
	latencies       []time.Duration
	baselineP99     time.Duration
	evaluationStart time.Time
}

// RecordRequest records a request result for canary monitoring.
func (cm *CanaryMetrics) RecordRequest(latency time.Duration, isError bool) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.totalRequests++
	if isError {
		cm.errorCount++
	}
	cm.latencies = append(cm.latencies, latency)
}

// ErrorRate returns the current error rate.
func (cm *CanaryMetrics) ErrorRate() float64 {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	if cm.totalRequests == 0 {
		return 0
	}
	return float64(cm.errorCount) / float64(cm.totalRequests)
}

// P99Latency returns the p99 latency from collected samples.
func (cm *CanaryMetrics) P99Latency() time.Duration {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	if len(cm.latencies) == 0 {
		return 0
	}
	// Sort a copy to compute p99.
	sorted := make([]time.Duration, len(cm.latencies))
	copy(sorted, cm.latencies)
	sortDurations(sorted)
	idx := int(float64(len(sorted)) * 0.99)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// ShouldRollback checks if canary thresholds are breached.
// Returns true if error > 5% or p99 > 3× baseline over the evaluation window.
func (cm *CanaryMetrics) ShouldRollback() bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// Need at least 60s of evaluation window data.
	if time.Since(cm.evaluationStart) < 60*time.Second {
		return false
	}
	if cm.totalRequests == 0 {
		return false
	}

	// Error rate > 5%.
	errorRate := float64(cm.errorCount) / float64(cm.totalRequests)
	if errorRate > 0.05 {
		return true
	}

	// P99 latency > 3× baseline.
	if cm.baselineP99 > 0 {
		p99 := cm.p99Locked()
		if p99 > cm.baselineP99*3 {
			return true
		}
	}

	return false
}

// Reset clears metrics for a new evaluation window.
func (cm *CanaryMetrics) Reset() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.totalRequests = 0
	cm.errorCount = 0
	cm.latencies = cm.latencies[:0]
	cm.evaluationStart = time.Now()
}

// SetEvaluationStart sets the evaluation window start time (for testing).
func (cm *CanaryMetrics) SetEvaluationStart(t time.Time) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.evaluationStart = t
}

// SetBaselineP99 sets the baseline p99 latency for threshold comparison (for testing).
func (cm *CanaryMetrics) SetBaselineP99(d time.Duration) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.baselineP99 = d
}

// p99Locked computes p99 without locking (caller must hold lock).
func (cm *CanaryMetrics) p99Locked() time.Duration {
	if len(cm.latencies) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(cm.latencies))
	copy(sorted, cm.latencies)
	sortDurations(sorted)
	idx := int(float64(len(sorted)) * 0.99)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// CanaryState tracks canary deployment routing state.
type CanaryState struct {
	mu             sync.RWMutex
	DeploymentID   string
	AgentID        string
	NewVersion     string
	StableVersion  string
	TrafficPercent int // 1-50, percent routed to new version
	Metrics        *CanaryMetrics
	Active         bool
}

// RouteToNew returns true if a request should be routed to the new version.
// Uses a simple counter-based approach for deterministic percentage routing.
func (cs *CanaryState) RouteToNew(requestNum int) bool {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	if !cs.Active {
		return false
	}
	// Route requestNum % 100 < TrafficPercent to new version.
	return (requestNum % 100) < cs.TrafficPercent
}

// Manager implements LifecycleManagerService.
type Manager struct {
	store    store.Store
	registry registry.AgentRegistryService
	emitter  EventEmitter

	mu          sync.RWMutex
	deployments map[string]*domain.Deployment // deploymentID -> Deployment
	canaries    map[string]*CanaryState       // agentID -> CanaryState
	instances   map[string]int                // agentID -> instance count

	// DeployTimeout is the maximum time for a deployment to complete.
	DeployTimeout time.Duration
	// DrainTimeout is the maximum default drain period.
	MaxDrainTimeout time.Duration
}

// ManagerConfig holds configuration for the lifecycle manager.
type ManagerConfig struct {
	DeployTimeout   time.Duration
	MaxDrainTimeout time.Duration
}

// DefaultManagerConfig returns sensible defaults.
func DefaultManagerConfig() ManagerConfig {
	return ManagerConfig{
		DeployTimeout:   60 * time.Second,
		MaxDrainTimeout: 300 * time.Second,
	}
}

// NewManager creates a new lifecycle Manager.
func NewManager(s store.Store, reg registry.AgentRegistryService, emitter EventEmitter, config ManagerConfig) *Manager {
	if config.DeployTimeout <= 0 {
		config.DeployTimeout = 60 * time.Second
	}
	if config.MaxDrainTimeout <= 0 {
		config.MaxDrainTimeout = 300 * time.Second
	}
	return &Manager{
		store:           s,
		registry:        reg,
		emitter:         emitter,
		deployments:     make(map[string]*domain.Deployment),
		canaries:        make(map[string]*CanaryState),
		instances:       make(map[string]int),
		DeployTimeout:   config.DeployTimeout,
		MaxDrainTimeout: config.MaxDrainTimeout,
	}
}

// Deploy initiates deployment of a new agent version.
// Creates instance, registers in registry within 60s, emits deployment_failed on failure.
func (m *Manager) Deploy(ctx context.Context, manifest domain.AgentManifest) (*domain.Deployment, error) {
	now := time.Now()

	deployment := &domain.Deployment{
		ID:            uuid.New().String(),
		AgentID:       "", // set after registration
		Version:       manifest.Version,
		Strategy:      manifest.Deployment,
		Status:        domain.DeploymentStatusPending,
		CanaryPercent: manifest.Deployment.CanaryPercent,
		StartedAt:     now,
	}

	// Validate canary percent if canary strategy.
	if manifest.Deployment.Type == "canary" {
		if manifest.Deployment.CanaryPercent < 1 || manifest.Deployment.CanaryPercent > 50 {
			m.emitDeploymentFailed(deployment, ErrInvalidCanaryPercent.Error())
			return nil, ErrInvalidCanaryPercent
		}
	}

	// Use context with deploy timeout.
	deployCtx, cancel := context.WithTimeout(ctx, m.DeployTimeout)
	defer cancel()

	// Register with registry (creates instance).
	entry, err := m.registry.Register(deployCtx, manifest)
	if err != nil {
		deployment.Status = domain.DeploymentStatusFailed
		m.emitDeploymentFailed(deployment, err.Error())
		return nil, fmt.Errorf("deployment failed during registration: %w", err)
	}

	deployment.AgentID = entry.ID
	deployment.Status = domain.DeploymentStatusRunning

	// Store deployment.
	m.mu.Lock()
	m.deployments[deployment.ID] = deployment
	if _, ok := m.instances[entry.ID]; !ok {
		m.instances[entry.ID] = 1
	}
	m.mu.Unlock()

	// If canary strategy, set up canary state.
	if manifest.Deployment.Type == "canary" {
		m.setupCanary(deployment, entry)
	} else {
		// For immediate/rolling, mark completed.
		completedAt := time.Now()
		deployment.Status = domain.DeploymentStatusCompleted
		deployment.CompletedAt = &completedAt
	}

	return deployment, nil
}

// Rollback reverts to a previous agent version.
// Must complete within 30s, redirect traffic, emit rollback_completed.
func (m *Manager) Rollback(ctx context.Context, agentID string) error {
	// Verify agent exists.
	agent, err := m.registry.Get(ctx, agentID)
	if err != nil {
		return fmt.Errorf("rollback: %w", err)
	}
	if agent == nil {
		return ErrAgentNotFound
	}

	// Get previous version.
	versions, err := m.store.Agents().ListVersions(ctx, agentID)
	if err != nil {
		return fmt.Errorf("rollback listing versions: %w", err)
	}
	if len(versions) < 2 {
		return ErrNoPreviousVersion
	}

	// versions[0] is current (newest), versions[1] is previous.
	previousVersion := versions[1]

	// Stop any active canary for this agent.
	m.mu.Lock()
	if canary, ok := m.canaries[agentID]; ok {
		canary.mu.Lock()
		canary.Active = false
		canary.mu.Unlock()
		delete(m.canaries, agentID)
	}
	m.mu.Unlock()

	// Revert agent version in registry.
	agent.Version = previousVersion.Version
	agent.UpdatedAt = time.Now()
	if err := m.store.Agents().Update(ctx, agent); err != nil {
		return fmt.Errorf("rollback update: %w", err)
	}

	// Update any running deployment to rolled_back status.
	m.mu.Lock()
	for _, dep := range m.deployments {
		if dep.AgentID == agentID && dep.Status == domain.DeploymentStatusRunning {
			dep.Status = domain.DeploymentStatusRolledBack
			completedAt := time.Now()
			dep.CompletedAt = &completedAt
		}
	}
	m.mu.Unlock()

	// Emit rollback_completed event.
	m.emitter.Emit(domain.SystemEvent{
		Type:      "rollback_completed",
		Severity:  "info",
		Source:    "lifecycle_manager",
		Timestamp: time.Now(),
		Payload: map[string]interface{}{
			"agent_id":         agentID,
			"previous_version": previousVersion.Version,
		},
	})

	return nil
}

// Scale adjusts instance count for an agent.
// Max instances: 1-100, must complete within 30s of threshold breach.
func (m *Manager) Scale(ctx context.Context, agentID string, replicas int) error {
	if replicas < 1 || replicas > 100 {
		return ErrInvalidReplicas
	}

	// Verify agent exists.
	agent, err := m.registry.Get(ctx, agentID)
	if err != nil {
		return fmt.Errorf("scale: %w", err)
	}
	if agent == nil {
		return ErrAgentNotFound
	}

	// Enforce max instances from deployment strategy.
	maxInstances := agent.Deployment.MaxInstances
	if maxInstances <= 0 {
		maxInstances = 100
	}
	if replicas > maxInstances {
		replicas = maxInstances
	}

	// Update instance count.
	m.mu.Lock()
	m.instances[agentID] = replicas
	m.mu.Unlock()

	return nil
}

// Deprecate marks an agent version for drain and removal.
// Drains active missions within drainTimeout (max 300s).
// Force-removes + emits drain_timeout_exceeded if exceeded.
func (m *Manager) Deprecate(ctx context.Context, agentID string, drainTimeout time.Duration) error {
	// Enforce max drain timeout.
	if drainTimeout <= 0 || drainTimeout > m.MaxDrainTimeout {
		drainTimeout = m.MaxDrainTimeout
	}

	// Verify agent exists.
	agent, err := m.registry.Get(ctx, agentID)
	if err != nil {
		return fmt.Errorf("deprecate: %w", err)
	}
	if agent == nil {
		return ErrAgentNotFound
	}

	// Mark as draining.
	agent.Status = domain.AgentStatusDraining
	agent.UpdatedAt = time.Now()
	if err := m.store.Agents().Update(ctx, agent); err != nil {
		return fmt.Errorf("deprecate: updating status: %w", err)
	}

	// Check for active missions.
	activeMissions, err := m.getActiveMissions(ctx, agentID)
	if err != nil {
		return fmt.Errorf("deprecate: checking active missions: %w", err)
	}

	if len(activeMissions) == 0 {
		// No active missions, remove immediately.
		return m.completeDeprecation(ctx, agent)
	}

	// Wait for drain or timeout.
	go m.drainAndRemove(agentID, drainTimeout, activeMissions)

	return nil
}

// GetDeployment returns a deployment by ID.
func (m *Manager) GetDeployment(deploymentID string) *domain.Deployment {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.deployments[deploymentID]
}

// GetCanaryState returns canary state for an agent.
func (m *Manager) GetCanaryState(agentID string) *CanaryState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.canaries[agentID]
}

// GetInstances returns instance count for an agent.
func (m *Manager) GetInstances(agentID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count, ok := m.instances[agentID]
	if !ok {
		return 0
	}
	return count
}

// CheckCanaryHealth evaluates canary metrics and triggers rollback if thresholds breached.
// Called periodically by a monitoring loop.
func (m *Manager) CheckCanaryHealth(ctx context.Context) {
	m.mu.RLock()
	canaries := make(map[string]*CanaryState, len(m.canaries))
	for k, v := range m.canaries {
		canaries[k] = v
	}
	m.mu.RUnlock()

	for agentID, canary := range canaries {
		canary.mu.RLock()
		active := canary.Active
		canary.mu.RUnlock()

		if !active {
			continue
		}

		if canary.Metrics.ShouldRollback() {
			// Auto-rollback triggered.
			if err := m.Rollback(ctx, agentID); err != nil {
				m.emitter.Emit(domain.SystemEvent{
					Type:      "canary_rollback_failed",
					Severity:  "error",
					Source:    "lifecycle_manager",
					Timestamp: time.Now(),
					Payload: map[string]interface{}{
						"agent_id": agentID,
						"error":    err.Error(),
					},
				})
			}
		}
	}
}

// ScaleOnQueueDepth checks queue depth and scales agents as needed.
func (m *Manager) ScaleOnQueueDepth(ctx context.Context, agentID string, queueDepth int, threshold int) error {
	if queueDepth <= threshold {
		return nil
	}

	agent, err := m.registry.Get(ctx, agentID)
	if err != nil {
		return err
	}
	if agent == nil {
		return ErrAgentNotFound
	}

	maxInstances := agent.Deployment.MaxInstances
	if maxInstances <= 0 {
		maxInstances = 100
	}

	m.mu.RLock()
	currentInstances := m.instances[agentID]
	m.mu.RUnlock()

	if currentInstances >= maxInstances {
		return nil // Already at max.
	}

	// Scale up by 1 instance per call, never exceeding max.
	newCount := currentInstances + 1
	if newCount > maxInstances {
		newCount = maxInstances
	}

	return m.Scale(ctx, agentID, newCount)
}

// setupCanary initializes canary tracking for a deployment.
func (m *Manager) setupCanary(deployment *domain.Deployment, entry *domain.AgentEntry) {
	metrics := &CanaryMetrics{
		evaluationStart: time.Now(),
		baselineP99:     0, // Will be set from stable version metrics.
	}

	canary := &CanaryState{
		DeploymentID:   deployment.ID,
		AgentID:        entry.ID,
		NewVersion:     deployment.Version,
		StableVersion:  "", // Previous stable version.
		TrafficPercent: deployment.CanaryPercent,
		Metrics:        metrics,
		Active:         true,
	}

	m.mu.Lock()
	m.canaries[entry.ID] = canary
	m.mu.Unlock()
}

// SetCanaryBaseline sets the baseline p99 for canary comparison.
func (m *Manager) SetCanaryBaseline(agentID string, baselineP99 time.Duration) {
	m.mu.RLock()
	canary, ok := m.canaries[agentID]
	m.mu.RUnlock()
	if !ok {
		return
	}
	canary.Metrics.mu.Lock()
	canary.Metrics.baselineP99 = baselineP99
	canary.Metrics.mu.Unlock()
}

// drainAndRemove waits for missions to complete or timeout, then removes agent.
func (m *Manager) drainAndRemove(agentID string, drainTimeout time.Duration, missions []*domain.Mission) {
	ctx := context.Background()
	deadline := time.Now().Add(drainTimeout)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if time.Now().After(deadline) {
				// Drain timeout exceeded — force-remove.
				interruptedIDs := make([]string, 0, len(missions))
				for _, mission := range missions {
					interruptedIDs = append(interruptedIDs, mission.ID)
				}

				m.emitter.Emit(domain.SystemEvent{
					Type:      "drain_timeout_exceeded",
					Severity:  "warning",
					Source:    "lifecycle_manager",
					Timestamp: time.Now(),
					Payload: map[string]interface{}{
						"agent_id":             agentID,
						"interrupted_missions": interruptedIDs,
					},
				})

				// Force-remove agent.
				agent, err := m.registry.Get(ctx, agentID)
				if err == nil && agent != nil {
					_ = m.completeDeprecation(ctx, agent)
				}
				return
			}

			// Check if all missions completed.
			active, err := m.getActiveMissions(ctx, agentID)
			if err != nil {
				continue
			}
			if len(active) == 0 {
				// All drained — remove agent.
				agent, err := m.registry.Get(ctx, agentID)
				if err == nil && agent != nil {
					_ = m.completeDeprecation(ctx, agent)
				}
				return
			}
		}
	}
}

// completeDeprecation finalizes agent removal.
func (m *Manager) completeDeprecation(ctx context.Context, agent *domain.AgentEntry) error {
	agent.Status = domain.AgentStatusDeprecated
	agent.UpdatedAt = time.Now()
	if err := m.store.Agents().Update(ctx, agent); err != nil {
		return err
	}

	// Clean up instance tracking.
	m.mu.Lock()
	delete(m.instances, agent.ID)
	delete(m.canaries, agent.ID)
	m.mu.Unlock()

	return nil
}

// getActiveMissions returns running/assigned missions for an agent.
func (m *Manager) getActiveMissions(ctx context.Context, agentID string) ([]*domain.Mission, error) {
	assigned, err := m.store.Missions().List(ctx, domain.MissionFilter{
		AgentID: agentID,
		Status:  domain.MissionStatusAssigned,
		Limit:   1000,
	})
	if err != nil {
		return nil, err
	}

	running, err := m.store.Missions().List(ctx, domain.MissionFilter{
		AgentID: agentID,
		Status:  domain.MissionStatusRunning,
		Limit:   1000,
	})
	if err != nil {
		return nil, err
	}

	return append(assigned, running...), nil
}

// emitDeploymentFailed emits a deployment_failed event.
func (m *Manager) emitDeploymentFailed(deployment *domain.Deployment, reason string) {
	m.emitter.Emit(domain.SystemEvent{
		Type:      "deployment_failed",
		Severity:  "error",
		Source:    "lifecycle_manager",
		Timestamp: time.Now(),
		Payload: map[string]interface{}{
			"deployment_id": deployment.ID,
			"agent_id":      deployment.AgentID,
			"version":       deployment.Version,
			"reason":        reason,
		},
	})
}

// sortDurations sorts a slice of durations in ascending order.
func sortDurations(d []time.Duration) {
	for i := 1; i < len(d); i++ {
		key := d[i]
		j := i - 1
		for j >= 0 && d[j] > key {
			d[j+1] = d[j]
			j--
		}
		d[j+1] = key
	}
}
