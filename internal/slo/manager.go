package slo

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/store"
)

// Errors returned by the SLO manager.
var (
	ErrAgentNotFound = errors.New("agent not found")
	ErrNoSLODefined  = errors.New("no SLO definition for agent")
)

// EventEmitter emits system events for SLO operations.
type EventEmitter interface {
	Emit(event domain.SystemEvent)
}

// TrafficReducer controls traffic allocation for an agent.
type TrafficReducer interface {
	// ReduceTraffic sets the traffic fraction for an agent (0.0 = no traffic, 1.0 = full traffic).
	ReduceTraffic(ctx context.Context, agentID string, fraction float64) error
}

// RollbackTrigger initiates a rollback for an agent via the lifecycle manager.
type RollbackTrigger interface {
	// Rollback reverts agent to previous version. Returns ErrNoPreviousVersion if none exists.
	Rollback(ctx context.Context, agentID string) error
}

// MetricsProvider retrieves current rolling metrics for an agent.
type MetricsProvider interface {
	// GetRollingMetrics returns current metrics for an agent computed over recent traces.
	// Returns map with keys: "latency_ms", "accuracy_pct", "cost_per_invocation".
	GetRollingMetrics(ctx context.Context, agentID string, window time.Duration) (map[string]float64, error)
}

// Config holds SLO manager configuration.
type Config struct {
	// EvaluationInterval is how often metrics are checked (default 60s).
	EvaluationInterval time.Duration
	// TrafficReductionThreshold is consecutive violation minutes before traffic reduction (default 5).
	TrafficReductionThreshold int
	// RollbackThreshold is consecutive violation minutes before auto-rollback (default 15).
	RollbackThreshold int
	// ComplianceWindow is the rolling window for compliance calculation (default 1h).
	ComplianceWindow time.Duration
}

// DefaultConfig returns spec-mandated defaults.
func DefaultConfig() Config {
	return Config{
		EvaluationInterval:        60 * time.Second,
		TrafficReductionThreshold: 5,
		RollbackThreshold:         15,
		ComplianceWindow:          1 * time.Hour,
	}
}

// violationState tracks consecutive violation state per agent.
type violationState struct {
	Start            time.Time
	ConsecutiveCount int // number of consecutive 60s intervals in violation
}

// Manager implements SLOManagerService.
type Manager struct {
	store           store.Store
	emitter         EventEmitter
	trafficReducer  TrafficReducer
	rollbackTrigger RollbackTrigger
	metricsProvider MetricsProvider
	config          Config

	mu         sync.RWMutex
	violations map[string]*violationState // agentID -> violation state
}

// NewManager creates a new SLO Manager.
func NewManager(
	s store.Store,
	emitter EventEmitter,
	trafficReducer TrafficReducer,
	rollbackTrigger RollbackTrigger,
	metricsProvider MetricsProvider,
	config Config,
) *Manager {
	if config.EvaluationInterval <= 0 {
		config.EvaluationInterval = 60 * time.Second
	}
	if config.TrafficReductionThreshold <= 0 {
		config.TrafficReductionThreshold = 5
	}
	if config.RollbackThreshold <= 0 {
		config.RollbackThreshold = 15
	}
	if config.ComplianceWindow <= 0 {
		config.ComplianceWindow = 1 * time.Hour
	}
	return &Manager{
		store:           s,
		emitter:         emitter,
		trafficReducer:  trafficReducer,
		rollbackTrigger: rollbackTrigger,
		metricsProvider: metricsProvider,
		config:          config,
		violations:      make(map[string]*violationState),
	}
}

// EvaluateSLO checks current rolling metrics at 60s granularity against declared SLO bounds.
// If violation persists >5 consecutive minutes → reduce traffic 50%.
// If violation persists >15 minutes → trigger rollback; if no previous version → emit rollback_failed + 0% traffic.
func (m *Manager) EvaluateSLO(ctx context.Context, agentID string) (*domain.SLOStatus, error) {
	// Get agent to retrieve SLO definition.
	agent, err := m.store.Agents().Get(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if agent == nil {
		return nil, ErrAgentNotFound
	}

	slo := agent.SLOs
	if slo.Latency == nil && slo.Accuracy == nil && slo.Cost == nil {
		return nil, ErrNoSLODefined
	}

	// Get current rolling metrics.
	metrics, err := m.metricsProvider.GetRollingMetrics(ctx, agentID, m.config.EvaluationInterval)
	if err != nil {
		return nil, err
	}

	// Evaluate each SLO bound.
	violated := false
	var violatedMetric string
	var threshold float64
	var observed float64

	if slo.Latency != nil {
		if latency, ok := metrics["latency_ms"]; ok {
			if latency > float64(slo.Latency.MaxMs) {
				violated = true
				violatedMetric = "latency_ms"
				threshold = float64(slo.Latency.MaxMs)
				observed = latency
			}
		}
	}

	if !violated && slo.Accuracy != nil {
		if accuracy, ok := metrics["accuracy_pct"]; ok {
			if accuracy < slo.Accuracy.MinPercent {
				violated = true
				violatedMetric = "accuracy_pct"
				threshold = slo.Accuracy.MinPercent
				observed = accuracy
			}
		}
	}

	if !violated && slo.Cost != nil {
		if cost, ok := metrics["cost_per_invocation"]; ok {
			if cost > slo.Cost.MaxCost {
				violated = true
				violatedMetric = "cost_per_invocation"
				threshold = slo.Cost.MaxCost
				observed = cost
			}
		}
	}

	// Update violation state.
	m.mu.Lock()
	vs := m.violations[agentID]

	if violated {
		// Emit slo_breach event.
		m.emitter.Emit(domain.SystemEvent{
			Type:      "slo_breach",
			Severity:  "warning",
			Source:    "slo_manager",
			Timestamp: time.Now(),
			Payload: map[string]interface{}{
				"agent_id":    agentID,
				"metric_name": violatedMetric,
				"threshold":   threshold,
				"observed":    observed,
			},
		})

		if vs == nil {
			vs = &violationState{
				Start:            time.Now(),
				ConsecutiveCount: 1,
			}
			m.violations[agentID] = vs
		} else {
			vs.ConsecutiveCount++
		}
	} else {
		// No violation — clear state.
		delete(m.violations, agentID)
		vs = nil
	}
	m.mu.Unlock()

	// Build status response.
	status := &domain.SLOStatus{
		AgentID:        agentID,
		CurrentMetrics: metrics,
	}

	if vs != nil {
		start := vs.Start
		status.ViolationStart = &start
		status.ViolationMinutes = vs.ConsecutiveCount

		// Check if rollback threshold breached (>15 minutes).
		if vs.ConsecutiveCount > m.config.RollbackThreshold {
			err := m.rollbackTrigger.Rollback(ctx, agentID)
			if err != nil {
				// No previous version available — emit rollback_failed, set traffic to 0%.
				m.emitter.Emit(domain.SystemEvent{
					Type:      "rollback_failed",
					Severity:  "error",
					Source:    "slo_manager",
					Timestamp: time.Now(),
					Payload: map[string]interface{}{
						"agent_id": agentID,
						"reason":   err.Error(),
					},
				})
				// Reduce traffic to 0%.
				_ = m.trafficReducer.ReduceTraffic(ctx, agentID, 0.0)
				status.TrafficReduction = 1.0 // 100% reduction = 0% traffic
			} else {
				// Rollback succeeded — clear violation state.
				m.mu.Lock()
				delete(m.violations, agentID)
				m.mu.Unlock()
				status.TrafficReduction = 0.0
			}
		} else if vs.ConsecutiveCount > m.config.TrafficReductionThreshold {
			// Traffic reduction threshold breached (>5 minutes) → reduce 50%.
			_ = m.trafficReducer.ReduceTraffic(ctx, agentID, 0.5)
			status.TrafficReduction = 0.5
		}
	}

	return status, nil
}

// GetCompliance calculates SLO compliance percentage on a rolling 1-hour window.
// Compliance = (compliant intervals / total intervals) × 100.
// Each interval is 60 seconds.
func (m *Manager) GetCompliance(ctx context.Context, agentID string) (*domain.SLOCompliance, error) {
	// Get agent to retrieve SLO definition.
	agent, err := m.store.Agents().Get(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if agent == nil {
		return nil, ErrAgentNotFound
	}

	slo := agent.SLOs
	if slo.Latency == nil && slo.Accuracy == nil && slo.Cost == nil {
		return nil, ErrNoSLODefined
	}

	// Calculate compliance over rolling window at 60s granularity.
	now := time.Now()
	window := m.config.ComplianceWindow
	intervalDuration := m.config.EvaluationInterval
	totalIntervals := int(window / intervalDuration)
	if totalIntervals <= 0 {
		totalIntervals = 60 // 1h / 60s = 60 intervals
	}

	compliantIntervals := 0

	// Check each 60s interval in the window.
	for i := 0; i < totalIntervals; i++ {
		intervalEnd := now.Add(-time.Duration(i) * intervalDuration)
		intervalStart := intervalEnd.Add(-intervalDuration)

		// Get metrics for this interval.
		metrics, err := m.metricsProvider.GetRollingMetrics(ctx, agentID, intervalDuration)
		if err != nil {
			// If we can't get metrics for an interval, skip it (don't count as violation).
			continue
		}

		if m.isCompliant(slo, metrics, intervalStart) {
			compliantIntervals++
		}
	}

	compliancePct := 0.0
	if totalIntervals > 0 {
		compliancePct = (float64(compliantIntervals) / float64(totalIntervals)) * 100.0
	}

	return &domain.SLOCompliance{
		AgentID:        agentID,
		Window:         window,
		CompliancePct:  compliancePct,
		LastCalculated: now,
	}, nil
}

// isCompliant checks if metrics satisfy all declared SLO bounds.
func (m *Manager) isCompliant(slo domain.SLODefinition, metrics map[string]float64, _ time.Time) bool {
	if slo.Latency != nil {
		if latency, ok := metrics["latency_ms"]; ok {
			if latency > float64(slo.Latency.MaxMs) {
				return false
			}
		}
	}

	if slo.Accuracy != nil {
		if accuracy, ok := metrics["accuracy_pct"]; ok {
			if accuracy < slo.Accuracy.MinPercent {
				return false
			}
		}
	}

	if slo.Cost != nil {
		if cost, ok := metrics["cost_per_invocation"]; ok {
			if cost > slo.Cost.MaxCost {
				return false
			}
		}
	}

	return true
}

// GetViolationState returns current violation state for an agent (for testing).
func (m *Manager) GetViolationState(agentID string) (start *time.Time, consecutiveMinutes int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	vs, ok := m.violations[agentID]
	if !ok || vs == nil {
		return nil, 0
	}
	return &vs.Start, vs.ConsecutiveCount
}

// ClearViolation clears violation state for an agent (for testing or recovery).
func (m *Manager) ClearViolation(agentID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.violations, agentID)
}
