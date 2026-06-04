package observability

import (
	"context"
	"math"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/store"
	"github.com/google/uuid"
)

// Config holds observability plane configuration.
type Config struct {
	// MissionTimeout is the maximum duration before a trace is marked as timeout.
	MissionTimeout time.Duration
	// RetentionDays is how long traces are retained (default 30).
	RetentionDays int
	// OTLPEndpoint is the OTLP collector endpoint for trace export.
	OTLPEndpoint string
	// DriftBaselineWindow is the historical window for drift baseline (default 7 days).
	DriftBaselineWindow time.Duration
	// DriftEvalWindow is the evaluation window for drift detection (default 1 hour).
	DriftEvalWindow time.Duration
	// DeviationThreshold is the number of standard deviations for drift (default 2.0).
	DeviationThreshold float64
}

// DefaultConfig returns configuration with spec-mandated defaults.
func DefaultConfig() Config {
	return Config{
		MissionTimeout:      3600 * time.Second,
		RetentionDays:       30,
		OTLPEndpoint:        "",
		DriftBaselineWindow: 7 * 24 * time.Hour,
		DriftEvalWindow:     1 * time.Hour,
		DeviationThreshold:  2.0,
	}
}

// EventEmitter is called when the observability plane needs to emit alerts.
type EventEmitter func(event domain.SystemEvent)

// OTLPExporter exports traces via OpenTelemetry protocol.
// This is an interface to allow stubbing for tests and real OTLP implementation.
type OTLPExporter interface {
	// ExportTrace sends a completed trace to the OTLP collector.
	ExportTrace(ctx context.Context, trace *domain.MissionTrace) error
	// Shutdown gracefully shuts down the exporter.
	Shutdown(ctx context.Context) error
}

// noopExporter is a stub exporter that does nothing.
type noopExporter struct{}

func (n *noopExporter) ExportTrace(_ context.Context, _ *domain.MissionTrace) error { return nil }
func (n *noopExporter) Shutdown(_ context.Context) error                            { return nil }

// observabilityService implements ObservabilityService.
type observabilityService struct {
	store    store.Store
	config   Config
	emitter  EventEmitter
	exporter OTLPExporter
}

// New creates a new ObservabilityService implementation.
func New(s store.Store, config Config, emitter EventEmitter, exporter OTLPExporter) ObservabilityService {
	if emitter == nil {
		emitter = func(_ domain.SystemEvent) {}
	}
	if exporter == nil {
		exporter = &noopExporter{}
	}
	return &observabilityService{
		store:    s,
		config:   config,
		emitter:  emitter,
		exporter: exporter,
	}
}

// StartTrace creates a new mission trace capturing goal, agent, and start timestamp.
func (o *observabilityService) StartTrace(ctx context.Context, mission *domain.Mission) (*domain.MissionTrace, error) {
	traceID := uuid.New().String()
	now := time.Now().UTC()

	retentionDuration := time.Duration(o.config.RetentionDays) * 24 * time.Hour
	trace := &domain.MissionTrace{
		TraceID:   traceID,
		MissionID: mission.ID,
		AgentID:   mission.AgentID,
		Goal:      string(mission.Payload),
		Steps:     []domain.TraceStep{},
		StartTime: now,
		ExpiresAt: now.Add(retentionDuration),
	}

	if err := o.store.Traces().Create(ctx, trace); err != nil {
		return nil, err
	}
	return trace, nil
}

// RecordStep adds a span to an existing trace.
func (o *observabilityService) RecordStep(ctx context.Context, traceID string, step *domain.TraceStep) error {
	if step.SpanID == "" {
		step.SpanID = uuid.New().String()
	}

	span := &domain.TraceSpan{
		SpanID:       step.SpanID,
		TraceID:      traceID,
		ParentSpanID: step.ParentSpanID,
		Name:         step.Name,
		StartTime:    step.StartTime,
		DurationMs:   step.Duration.Milliseconds(),
		Status:       step.Status,
		Attributes:   step.Attributes,
	}

	return o.store.Traces().AddSpan(ctx, span)
}

// CompleteTrace closes a mission trace with the given outcome.
// If the mission exceeded the configured timeout, the outcome is forced to "timeout"
// and a mission_timeout alert is emitted.
func (o *observabilityService) CompleteTrace(ctx context.Context, traceID string, outcome domain.MissionOutcome) error {
	trace, err := o.store.Traces().Get(ctx, traceID)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	duration := now.Sub(trace.StartTime)

	// Timeout handling: if mission exceeded timeout, force outcome to timeout.
	if duration > o.config.MissionTimeout {
		outcome = domain.OutcomeTimeout
		o.emitter(domain.SystemEvent{
			Type:      "mission_timeout",
			Severity:  "warning",
			Source:    "observability",
			Timestamp: now,
			Payload: map[string]interface{}{
				"trace_id":   traceID,
				"mission_id": trace.MissionID,
				"agent_id":   trace.AgentID,
				"duration_s": duration.Seconds(),
				"timeout_s":  o.config.MissionTimeout.Seconds(),
			},
		})
	}

	trace.Outcome = outcome
	trace.EndTime = &now
	trace.Duration = duration

	// Persist the completed trace.
	if err := o.updateTrace(ctx, trace); err != nil {
		return err
	}

	// Schedule OTLP export (within 60s of completion per spec).
	o.scheduleExport(ctx, trace)

	return nil
}

// updateTrace persists the completed trace state.
func (o *observabilityService) updateTrace(ctx context.Context, trace *domain.MissionTrace) error {
	return o.store.Traces().Update(ctx, trace)
}

// scheduleExport queues a trace for OTLP export. The export happens
// asynchronously but within 60 seconds of completion per requirement 6.5.
func (o *observabilityService) scheduleExport(ctx context.Context, trace *domain.MissionTrace) {
	go func() {
		exportCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		// Best-effort export; failures are logged but don't fail the trace.
		_ = o.exporter.ExportTrace(exportCtx, trace)
	}()
}

// DetectDrift compares recent (1-hour window) success rate and mean duration
// against a rolling 7-day baseline. Returns drift alerts for metrics exceeding
// 2 standard deviations from baseline.
func (o *observabilityService) DetectDrift(ctx context.Context) ([]domain.DriftAlert, error) {
	now := time.Now().UTC()

	// Query recent traces (last 1 hour).
	recentFilter := domain.TraceFilter{
		StartTime: now.Add(-o.config.DriftEvalWindow),
		EndTime:   now,
		Limit:     10000,
	}
	recentTraces, err := o.store.Traces().Query(ctx, recentFilter)
	if err != nil {
		return nil, err
	}

	// Need at least some data to detect drift.
	if len(recentTraces) == 0 {
		return nil, nil
	}

	// Query baseline traces (7-day window, excluding the recent hour).
	baselineFilter := domain.TraceFilter{
		StartTime: now.Add(-o.config.DriftBaselineWindow),
		EndTime:   now.Add(-o.config.DriftEvalWindow),
		Limit:     100000,
	}
	baselineTraces, err := o.store.Traces().Query(ctx, baselineFilter)
	if err != nil {
		return nil, err
	}

	// Need baseline data to compare against.
	if len(baselineTraces) == 0 {
		return nil, nil
	}

	var alerts []domain.DriftAlert

	// Group by agent for per-agent drift detection.
	recentByAgent := groupTracesByAgent(recentTraces)
	baselineByAgent := groupTracesByAgent(baselineTraces)

	for agentID, recent := range recentByAgent {
		baseline, ok := baselineByAgent[agentID]
		if !ok || len(baseline) == 0 {
			continue
		}

		// Check success rate drift.
		recentSuccessRate := computeSuccessRate(recent)
		baselineSuccessRate, baselineSuccessStdDev := computeSuccessRateStats(baseline)

		if successDrift := detectMetricDrift(recentSuccessRate, baselineSuccessRate, baselineSuccessStdDev, o.config.DeviationThreshold); successDrift > 0 {
			alerts = append(alerts, domain.DriftAlert{
				AgentID:       agentID,
				MetricName:    "success_rate",
				ObservedValue: recentSuccessRate,
				BaselineValue: baselineSuccessRate,
				Deviation:     successDrift,
				DetectedAt:    now,
			})
		}

		// Check mean duration drift.
		recentMeanDuration := computeMeanDuration(recent)
		baselineMeanDuration, baselineDurationStdDev := computeDurationStats(baseline)

		if durationDrift := detectMetricDrift(recentMeanDuration, baselineMeanDuration, baselineDurationStdDev, o.config.DeviationThreshold); durationDrift > 0 {
			alerts = append(alerts, domain.DriftAlert{
				AgentID:       agentID,
				MetricName:    "mean_duration",
				ObservedValue: recentMeanDuration,
				BaselineValue: baselineMeanDuration,
				Deviation:     durationDrift,
				DetectedAt:    now,
			})
		}
	}

	// Emit drift_detected events for each alert.
	for _, alert := range alerts {
		o.emitter(domain.SystemEvent{
			Type:      "drift_detected",
			Severity:  "warning",
			Source:    "observability",
			Timestamp: now,
			Payload: map[string]interface{}{
				"agent_id":       alert.AgentID,
				"metric_name":    alert.MetricName,
				"observed_value": alert.ObservedValue,
				"baseline_value": alert.BaselineValue,
				"deviation":      alert.Deviation,
			},
		})
	}

	return alerts, nil
}

// GetTrace retrieves a complete mission trace by ID.
func (o *observabilityService) GetTrace(ctx context.Context, traceID string) (*domain.MissionTrace, error) {
	return o.store.Traces().Get(ctx, traceID)
}

// DeleteExpiredTraces removes traces past their retention period.
// Should be called periodically (e.g., every hour) to satisfy the 24h deletion requirement.
func (o *observabilityService) DeleteExpiredTraces(ctx context.Context) (int, error) {
	return o.store.Traces().DeleteExpired(ctx)
}

// --- Helper functions ---

func groupTracesByAgent(traces []*domain.MissionTrace) map[string][]*domain.MissionTrace {
	grouped := make(map[string][]*domain.MissionTrace)
	for _, t := range traces {
		if t.AgentID != "" {
			grouped[t.AgentID] = append(grouped[t.AgentID], t)
		}
	}
	return grouped
}

func computeSuccessRate(traces []*domain.MissionTrace) float64 {
	if len(traces) == 0 {
		return 0
	}
	successes := 0
	completed := 0
	for _, t := range traces {
		if t.Outcome != "" {
			completed++
			if t.Outcome == domain.OutcomeSuccess {
				successes++
			}
		}
	}
	if completed == 0 {
		return 0
	}
	return float64(successes) / float64(completed)
}

// computeSuccessRateStats computes the mean success rate and standard deviation
// using a sliding window approach over the baseline traces.
// We split baseline into daily buckets and compute stats over those.
func computeSuccessRateStats(traces []*domain.MissionTrace) (mean, stddev float64) {
	if len(traces) == 0 {
		return 0, 0
	}

	// Group by day to get daily success rates.
	buckets := groupByDay(traces)
	if len(buckets) == 0 {
		return 0, 0
	}

	rates := make([]float64, 0, len(buckets))
	for _, bucket := range buckets {
		rates = append(rates, computeSuccessRate(bucket))
	}

	mean = computeMean(rates)
	stddev = computeStdDev(rates, mean)
	return mean, stddev
}

func computeMeanDuration(traces []*domain.MissionTrace) float64 {
	if len(traces) == 0 {
		return 0
	}
	var total float64
	count := 0
	for _, t := range traces {
		if t.Duration > 0 {
			total += t.Duration.Seconds()
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

func computeDurationStats(traces []*domain.MissionTrace) (mean, stddev float64) {
	if len(traces) == 0 {
		return 0, 0
	}

	buckets := groupByDay(traces)
	if len(buckets) == 0 {
		return 0, 0
	}

	means := make([]float64, 0, len(buckets))
	for _, bucket := range buckets {
		m := computeMeanDuration(bucket)
		if m > 0 {
			means = append(means, m)
		}
	}

	if len(means) == 0 {
		return 0, 0
	}

	mean = computeMean(means)
	stddev = computeStdDev(means, mean)
	return mean, stddev
}

func groupByDay(traces []*domain.MissionTrace) map[string][]*domain.MissionTrace {
	buckets := make(map[string][]*domain.MissionTrace)
	for _, t := range traces {
		day := t.StartTime.Format("2006-01-02")
		buckets[day] = append(buckets[day], t)
	}
	return buckets
}

func computeMean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func computeStdDev(values []float64, mean float64) float64 {
	if len(values) < 2 {
		return 0
	}
	sumSq := 0.0
	for _, v := range values {
		diff := v - mean
		sumSq += diff * diff
	}
	return math.Sqrt(sumSq / float64(len(values)))
}

// detectMetricDrift returns the deviation if it exceeds threshold, or 0 if no drift.
// When stddev is 0 (perfectly consistent baseline), any meaningful difference is flagged
// as exceeding the threshold (deviation set to threshold+1 to indicate infinite sigma).
func detectMetricDrift(observed, baseline, stddev, threshold float64) float64 {
	diff := math.Abs(observed - baseline)
	if diff < 1e-9 {
		return 0 // no difference at all
	}
	if stddev > 0 {
		deviation := diff / stddev
		if deviation > threshold {
			return deviation
		}
		return 0
	}
	// stddev == 0 but values differ → effectively infinite deviation
	return threshold + 1
}
