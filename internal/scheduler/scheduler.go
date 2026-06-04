package scheduler

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/store"
)

// DefaultWeights are the default scoring weights for candidate ranking.
var DefaultWeights = domain.ScoringWeights{
	Cost:    0.4,
	Latency: 0.3,
	Load:    0.3,
}

// SchedulerConfig holds configurable scheduler parameters.
type SchedulerConfig struct {
	Weights        domain.ScoringWeights
	QueueTimeout   time.Duration // default 60s
}

// DefaultConfig returns default scheduler configuration.
func DefaultConfig() SchedulerConfig {
	return SchedulerConfig{
		Weights:      DefaultWeights,
		QueueTimeout: 60 * time.Second,
	}
}

// CircuitBreakerChecker checks circuit breaker state for an agent.
type CircuitBreakerChecker interface {
	GetCircuitBreaker(ctx context.Context, agentID string) (*domain.CircuitBreakerState, error)
}

// EventEmitter emits system events.
type EventEmitter interface {
	Emit(event domain.SystemEvent)
}

// channelEmitter is a simple EventEmitter that sends events to a channel.
type channelEmitter struct {
	ch chan domain.SystemEvent
}

func (e *channelEmitter) Emit(event domain.SystemEvent) {
	select {
	case e.ch <- event:
	default:
		// Drop event if channel full — non-blocking.
	}
}

// NewChannelEmitter creates an EventEmitter backed by a buffered channel.
func NewChannelEmitter(bufSize int) (EventEmitter, <-chan domain.SystemEvent) {
	ch := make(chan domain.SystemEvent, bufSize)
	return &channelEmitter{ch: ch}, ch
}

// Scheduler implements SchedulerService.
type Scheduler struct {
	store   store.Store
	cb      CircuitBreakerChecker
	emitter EventEmitter
	config  SchedulerConfig
}

// New creates a new Scheduler.
func New(s store.Store, cb CircuitBreakerChecker, emitter EventEmitter, config SchedulerConfig) *Scheduler {
	if err := validateWeights(config.Weights); err != nil {
		config.Weights = DefaultWeights
	}
	if config.QueueTimeout <= 0 {
		config.QueueTimeout = 60 * time.Second
	}
	return &Scheduler{
		store:   s,
		cb:      cb,
		emitter: emitter,
		config:  config,
	}
}

// Schedule finds the best agent for a mission and assigns it.
func (s *Scheduler) Schedule(ctx context.Context, mission *domain.Mission) (*domain.Assignment, error) {
	if mission == nil {
		return nil, fmt.Errorf("mission must not be nil")
	}

	// 1. Find candidate agents whose capabilities are a superset of required.
	candidates, err := s.findCandidates(ctx, mission)
	if err != nil {
		return nil, fmt.Errorf("finding candidates: %w", err)
	}

	// 2. If no candidates, queue mission and emit scheduling_failed.
	if len(candidates) == 0 {
		mission.Status = domain.MissionStatusQueued
		if err := s.store.Missions().Update(ctx, mission); err != nil {
			return nil, fmt.Errorf("queuing mission: %w", err)
		}
		s.emitter.Emit(domain.SystemEvent{
			Type:      "scheduling_failed",
			Severity:  "warning",
			Source:    "scheduler",
			Timestamp: time.Now(),
			Payload: map[string]interface{}{
				"mission_id":            mission.ID,
				"required_capabilities": mission.RequiredCapabilities,
			},
		})
		return nil, nil
	}

	// 3. Score and rank candidates.
	scored := s.scoreCandidates(ctx, candidates, mission)

	// 4. Sort: highest score first, tie-break by lowest load then earliest registration.
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].totalScore != scored[j].totalScore {
			return scored[i].totalScore > scored[j].totalScore
		}
		if scored[i].currentLoad != scored[j].currentLoad {
			return scored[i].currentLoad < scored[j].currentLoad
		}
		return scored[i].agent.CreatedAt.Before(scored[j].agent.CreatedAt)
	})

	// 5. Select the top candidate.
	winner := scored[0]
	now := time.Now()

	assignment := &domain.Assignment{
		MissionID:  mission.ID,
		AgentID:    winner.agent.ID,
		Score:      winner.totalScore,
		Rationale: domain.SelectionRationale{
			CostScore:    winner.costScore,
			LatencyScore: winner.latencyScore,
			LoadScore:    winner.loadScore,
			Weights:      s.config.Weights,
		},
		AssignedAt: now,
	}

	// 6. Update mission with assignment.
	mission.AgentID = winner.agent.ID
	mission.Status = domain.MissionStatusAssigned
	mission.AssignedAt = &now
	mission.AssignmentRationale = &assignment.Rationale

	if err := s.store.Missions().Update(ctx, mission); err != nil {
		return nil, fmt.Errorf("recording assignment: %w", err)
	}

	return assignment, nil
}

// Reschedule re-evaluates queued missions.
func (s *Scheduler) Reschedule(ctx context.Context) error {
	queued, err := s.store.Missions().GetPending(ctx)
	if err != nil {
		return fmt.Errorf("fetching queued missions: %w", err)
	}

	now := time.Now()
	for _, mission := range queued {
		if mission.Status != domain.MissionStatusQueued {
			continue
		}

		// Check timeout: if queued > 60s, emit scheduling_timeout.
		if now.Sub(mission.SubmittedAt) > s.config.QueueTimeout {
			s.emitter.Emit(domain.SystemEvent{
				Type:      "scheduling_timeout",
				Severity:  "error",
				Source:    "scheduler",
				Timestamp: now,
				Payload: map[string]interface{}{
					"mission_id": mission.ID,
					"queued_for": now.Sub(mission.SubmittedAt).String(),
				},
			})
			continue
		}

		// Try scheduling again.
		_, _ = s.Schedule(ctx, mission)
	}
	return nil
}

// scoredCandidate holds an agent with its computed scores.
type scoredCandidate struct {
	agent        *domain.AgentEntry
	costScore    float64
	latencyScore float64
	loadScore    float64
	totalScore   float64
	currentLoad  int
}

// findCandidates returns agents that:
// - have capabilities that are a superset of mission's required capabilities
// - are not overloaded (current load < max concurrent missions)
// - have circuit breaker closed
func (s *Scheduler) findCandidates(ctx context.Context, mission *domain.Mission) ([]*domain.AgentEntry, error) {
	// Get all active agents.
	agents, err := s.store.Agents().List(ctx, domain.AgentFilter{
		Status: domain.AgentStatusActive,
		Limit:  1000,
	})
	if err != nil {
		return nil, err
	}

	var candidates []*domain.AgentEntry
	for _, agent := range agents {
		// Check capability superset.
		if !hasAllCapabilities(agent, mission.RequiredCapabilities) {
			continue
		}

		// Check load: agent not overloaded.
		load, err := s.agentLoad(ctx, agent.ID)
		if err != nil {
			continue
		}
		if load >= agent.Resources.MaxConcurrentMissions {
			continue
		}

		// Check circuit breaker closed.
		if s.cb != nil {
			cbState, err := s.cb.GetCircuitBreaker(ctx, agent.ID)
			if err == nil && cbState != nil && cbState.State != domain.CBClosed {
				continue
			}
		}

		candidates = append(candidates, agent)
	}

	return candidates, nil
}

// scoreCandidates computes normalized scores for each candidate.
// Lower cost = higher score, lower latency = higher score, lower load = higher score.
func (s *Scheduler) scoreCandidates(ctx context.Context, candidates []*domain.AgentEntry, mission *domain.Mission) []scoredCandidate {
	if len(candidates) == 0 {
		return nil
	}

	// Gather raw metrics for normalization.
	type metrics struct {
		cost    float64
		latency float64
		load    int
	}

	agentMetrics := make([]metrics, len(candidates))
	var maxCost, maxLatency float64
	var maxLoad int

	for i, agent := range candidates {
		cost := agentCost(agent)
		latency := agentLatency(agent)
		load, _ := s.agentLoad(ctx, agent.ID)

		agentMetrics[i] = metrics{cost: cost, latency: latency, load: load}

		if cost > maxCost {
			maxCost = cost
		}
		if latency > maxLatency {
			maxLatency = latency
		}
		if load > maxLoad {
			maxLoad = load
		}
	}

	// Normalize: score = 1 - (value / max) when max > 0; else 1.0 (all equal).
	scored := make([]scoredCandidate, len(candidates))
	for i, agent := range candidates {
		m := agentMetrics[i]

		var costScore, latencyScore, loadScore float64

		if maxCost > 0 {
			costScore = 1.0 - (m.cost / maxCost)
		} else {
			costScore = 1.0
		}

		if maxLatency > 0 {
			latencyScore = 1.0 - (m.latency / maxLatency)
		} else {
			latencyScore = 1.0
		}

		if maxLoad > 0 {
			loadScore = 1.0 - (float64(m.load) / float64(maxLoad))
		} else {
			loadScore = 1.0
		}

		total := costScore*s.config.Weights.Cost +
			latencyScore*s.config.Weights.Latency +
			loadScore*s.config.Weights.Load

		scored[i] = scoredCandidate{
			agent:        agent,
			costScore:    costScore,
			latencyScore: latencyScore,
			loadScore:    loadScore,
			totalScore:   total,
			currentLoad:  m.load,
		}
	}

	return scored
}

// agentLoad returns current count of assigned/running missions for an agent.
func (s *Scheduler) agentLoad(ctx context.Context, agentID string) (int, error) {
	assigned, err := s.store.Missions().List(ctx, domain.MissionFilter{
		AgentID: agentID,
		Status:  domain.MissionStatusAssigned,
		Limit:   1000,
	})
	if err != nil {
		return 0, err
	}

	running, err := s.store.Missions().List(ctx, domain.MissionFilter{
		AgentID: agentID,
		Status:  domain.MissionStatusRunning,
		Limit:   1000,
	})
	if err != nil {
		return 0, err
	}

	return len(assigned) + len(running), nil
}

// hasAllCapabilities checks agent capabilities are a superset of required.
func hasAllCapabilities(agent *domain.AgentEntry, required []string) bool {
	if len(required) == 0 {
		return true
	}
	capSet := make(map[string]struct{}, len(agent.Capabilities))
	for _, c := range agent.Capabilities {
		capSet[c.Name] = struct{}{}
	}
	for _, req := range required {
		if _, ok := capSet[req]; !ok {
			return false
		}
	}
	return true
}

// agentCost derives a cost metric from the agent's SLO cost bound.
// If unset, returns 0 (cheapest possible).
func agentCost(agent *domain.AgentEntry) float64 {
	if agent.SLOs.Cost != nil {
		return agent.SLOs.Cost.MaxCost
	}
	return 0
}

// agentLatency derives a latency metric from the agent's SLO latency bound.
// If unset, returns 0 (fastest possible).
func agentLatency(agent *domain.AgentEntry) float64 {
	if agent.SLOs.Latency != nil {
		return float64(agent.SLOs.Latency.MaxMs)
	}
	return 0
}

// validateWeights checks weights sum to 1.0 (with floating point tolerance).
func validateWeights(w domain.ScoringWeights) error {
	sum := w.Cost + w.Latency + w.Load
	if math.Abs(sum-1.0) > 0.001 {
		return fmt.Errorf("weights must sum to 1.0, got %f", sum)
	}
	if w.Cost < 0 || w.Latency < 0 || w.Load < 0 {
		return fmt.Errorf("weights must be non-negative")
	}
	return nil
}
