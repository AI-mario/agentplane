package property

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/observability"
	"github.com/agentplane/agentplane/internal/store/sqlite"
	"pgregory.net/rapid"
)

// --- Helpers ---

// newObsStore creates an in-memory SQLite store for observability property tests.
func newObsStore(t *testing.T, name string) *sqlite.SQLiteStore {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&_foreign_keys=on", name)
	s, err := sqlite.New(dsn)
	if err != nil {
		t.Fatalf("new obs store %s: %v", name, err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate obs store %s: %v", name, err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// obsEventCollector collects emitted events for assertions.
type obsEventCollector struct {
	events []domain.SystemEvent
}

func (c *obsEventCollector) emit(e domain.SystemEvent) {
	c.events = append(c.events, e)
}

// --- Generators ---

func genGoal() *rapid.Generator[string] {
	return rapid.StringMatching(`[A-Za-z ]{5,50}`)
}

func genAgentID() *rapid.Generator[string] {
	return rapid.StringMatching(`agent-[a-z]{3,8}`)
}

func genMissionID() *rapid.Generator[string] {
	return rapid.StringMatching(`mission-[a-z0-9]{4,8}`)
}

func genStepName() *rapid.Generator[string] {
	return rapid.StringMatching(`[a-z]{3,10}-[a-z]{3,10}`)
}

func genStepStatus() *rapid.Generator[domain.StepStatus] {
	return rapid.SampledFrom([]domain.StepStatus{
		domain.StepStatusSuccess,
		domain.StepStatusFailure,
		domain.StepStatusSkipped,
	})
}

func genOutcome() *rapid.Generator[domain.MissionOutcome] {
	return rapid.SampledFrom([]domain.MissionOutcome{
		domain.OutcomeSuccess,
		domain.OutcomeFailure,
		domain.OutcomePartial,
		domain.OutcomeTimeout,
	})
}

// --- Property Tests ---

// TestProperty13_MissionTraceCompleteness verifies that an initiated trace has goal+agent+start;
// N steps have all span fields; completed trace has outcome+duration.
// **Validates: Requirements 6.1, 6.2, 6.3**
func TestProperty13_MissionTraceCompleteness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		storeName := fmt.Sprintf("obs13_%d", rapid.IntRange(0, 999999).Draw(rt, "storeID"))
		s := newObsStore(t, storeName)
		ctx := context.Background()
		collector := &obsEventCollector{}
		svc := observability.New(s, observability.DefaultConfig(), collector.emit, nil)

		// Generate mission inputs.
		goal := genGoal().Draw(rt, "goal")
		agentID := genAgentID().Draw(rt, "agentID")
		missionID := genMissionID().Draw(rt, "missionID")

		mission := &domain.Mission{
			ID:      missionID,
			AgentID: agentID,
			Payload: []byte(goal),
		}

		// Start trace: must have goal, agent, start.
		trace, err := svc.StartTrace(ctx, mission)
		if err != nil {
			t.Fatalf("StartTrace: %v", err)
		}

		if trace.Goal != goal {
			t.Errorf("trace goal = %q, want %q", trace.Goal, goal)
		}
		if trace.AgentID != agentID {
			t.Errorf("trace agent = %q, want %q", trace.AgentID, agentID)
		}
		if trace.StartTime.IsZero() {
			t.Error("trace start time is zero")
		}

		// Record N steps, each must have all span fields.
		numSteps := rapid.IntRange(1, 5).Draw(rt, "numSteps")
		parentSpanID := ""
		for i := 0; i < numSteps; i++ {
			stepName := genStepName().Draw(rt, fmt.Sprintf("stepName%d", i))
			durationMs := rapid.IntRange(1, 5000).Draw(rt, fmt.Sprintf("durationMs%d", i))
			status := genStepStatus().Draw(rt, fmt.Sprintf("status%d", i))

			step := &domain.TraceStep{
				ParentSpanID: parentSpanID,
				Name:         stepName,
				StartTime:    time.Now().UTC(),
				Duration:     time.Duration(durationMs) * time.Millisecond,
				Status:       status,
			}

			err := svc.RecordStep(ctx, trace.TraceID, step)
			if err != nil {
				t.Fatalf("RecordStep %d: %v", i, err)
			}
			// Use auto-generated span ID as parent for next step.
			parentSpanID = step.SpanID
		}

		// Complete trace with outcome.
		outcome := genOutcome().Draw(rt, "outcome")
		err = svc.CompleteTrace(ctx, trace.TraceID, outcome)
		if err != nil {
			t.Fatalf("CompleteTrace: %v", err)
		}

		// Retrieve and verify completed trace.
		got, err := svc.GetTrace(ctx, trace.TraceID)
		if err != nil {
			t.Fatalf("GetTrace: %v", err)
		}

		// Completed trace must have outcome and duration.
		if got.Outcome == "" {
			t.Error("completed trace has empty outcome")
		}
		if got.EndTime == nil {
			t.Error("completed trace has nil end time")
		}
		if got.Duration < 0 {
			t.Errorf("completed trace has negative duration: %v", got.Duration)
		}

		// Verify all N steps have required span fields.
		if len(got.Steps) != numSteps {
			t.Errorf("got %d steps, want %d", len(got.Steps), numSteps)
		}
		for i, step := range got.Steps {
			if step.SpanID == "" {
				t.Errorf("step %d: empty span ID", i)
			}
			if step.Name == "" {
				t.Errorf("step %d: empty name", i)
			}
			if step.StartTime.IsZero() {
				t.Errorf("step %d: zero start time", i)
			}
			if step.Duration < 0 {
				t.Errorf("step %d: negative duration", i)
			}
			if step.Status == "" {
				t.Errorf("step %d: empty status", i)
			}
		}
	})
}

// TestProperty14_DriftDetectionThreshold verifies that >2σ deviation produces an alert
// and within 2σ produces no alert.
// **Validates: Requirements 6.6**
func TestProperty14_DriftDetectionThreshold(t *testing.T) {
	t.Run("AboveThreshold_Alert", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			storeName := fmt.Sprintf("obs14a_%d", rapid.IntRange(0, 999999).Draw(rt, "storeID"))
			s := newObsStore(t, storeName)
			ctx := context.Background()
			collector := &obsEventCollector{}

			config := observability.DefaultConfig()
			svc := observability.New(s, config, collector.emit, nil)

			now := time.Now().UTC()
			agentID := genAgentID().Draw(rt, "agentID")

			// Create a consistent baseline: 100% success rate over 7 days.
			for d := 1; d <= 7; d++ {
				for i := 0; i < 10; i++ {
					traceTime := now.Add(-time.Duration(d) * 24 * time.Hour).Add(time.Duration(i) * time.Minute)
					endTime := traceTime.Add(1 * time.Second)
					trace := &domain.MissionTrace{
						TraceID:   fmt.Sprintf("bl-%s-d%d-i%d", agentID, d, i),
						MissionID: "bl-mission",
						AgentID:   agentID,
						Goal:      "baseline",
						Outcome:   domain.OutcomeSuccess,
						StartTime: traceTime,
						EndTime:   &endTime,
						Duration:  1 * time.Second,
						ExpiresAt: now.Add(30 * 24 * time.Hour),
					}
					if err := s.Traces().Create(ctx, trace); err != nil {
						t.Fatalf("create baseline: %v", err)
					}
				}
			}

			// Recent window: all failures (0% success vs 100% baseline → infinite σ deviation).
			for i := 0; i < 10; i++ {
				traceTime := now.Add(-30 * time.Minute).Add(time.Duration(i) * time.Minute)
				endTime := traceTime.Add(1 * time.Second)
				trace := &domain.MissionTrace{
					TraceID:   fmt.Sprintf("rc-%s-i%d", agentID, i),
					MissionID: "recent-m",
					AgentID:   agentID,
					Goal:      "recent",
					Outcome:   domain.OutcomeFailure,
					StartTime: traceTime,
					EndTime:   &endTime,
					Duration:  1 * time.Second,
					ExpiresAt: now.Add(30 * 24 * time.Hour),
				}
				if err := s.Traces().Create(ctx, trace); err != nil {
					t.Fatalf("create recent: %v", err)
				}
			}

			alerts, err := svc.DetectDrift(ctx)
			if err != nil {
				t.Fatalf("DetectDrift: %v", err)
			}

			// Must emit at least one drift alert for success_rate.
			found := false
			for _, a := range alerts {
				if a.AgentID == agentID && a.MetricName == "success_rate" {
					found = true
					if a.Deviation <= 2.0 {
						t.Errorf("expected deviation > 2.0, got %f", a.Deviation)
					}
				}
			}
			if !found {
				t.Errorf("expected drift alert for agent %s success_rate", agentID)
			}

			// Verify drift_detected event emitted.
			driftEmitted := false
			for _, e := range collector.events {
				if e.Type == "drift_detected" {
					driftEmitted = true
				}
			}
			if !driftEmitted {
				t.Error("expected drift_detected event")
			}
		})
	})

	t.Run("WithinThreshold_NoAlert", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			storeName := fmt.Sprintf("obs14b_%d", rapid.IntRange(0, 999999).Draw(rt, "storeID"))
			s := newObsStore(t, storeName)
			ctx := context.Background()
			collector := &obsEventCollector{}

			config := observability.DefaultConfig()
			svc := observability.New(s, config, collector.emit, nil)

			now := time.Now().UTC()
			agentID := genAgentID().Draw(rt, "agentID")

			// Create baseline: 100% success rate over 7 days.
			for d := 1; d <= 7; d++ {
				for i := 0; i < 10; i++ {
					traceTime := now.Add(-time.Duration(d) * 24 * time.Hour).Add(time.Duration(i) * time.Minute)
					endTime := traceTime.Add(1 * time.Second)
					trace := &domain.MissionTrace{
						TraceID:   fmt.Sprintf("bl-%s-d%d-i%d", agentID, d, i),
						MissionID: "bl-mission",
						AgentID:   agentID,
						Goal:      "baseline",
						Outcome:   domain.OutcomeSuccess,
						StartTime: traceTime,
						EndTime:   &endTime,
						Duration:  1 * time.Second,
						ExpiresAt: now.Add(30 * 24 * time.Hour),
					}
					if err := s.Traces().Create(ctx, trace); err != nil {
						t.Fatalf("create baseline: %v", err)
					}
				}
			}

			// Recent window: also 100% success → same as baseline → no drift.
			for i := 0; i < 10; i++ {
				traceTime := now.Add(-30 * time.Minute).Add(time.Duration(i) * time.Minute)
				endTime := traceTime.Add(1 * time.Second)
				trace := &domain.MissionTrace{
					TraceID:   fmt.Sprintf("rc-%s-i%d", agentID, i),
					MissionID: "recent-m",
					AgentID:   agentID,
					Goal:      "recent",
					Outcome:   domain.OutcomeSuccess,
					StartTime: traceTime,
					EndTime:   &endTime,
					Duration:  1 * time.Second,
					ExpiresAt: now.Add(30 * 24 * time.Hour),
				}
				if err := s.Traces().Create(ctx, trace); err != nil {
					t.Fatalf("create recent: %v", err)
				}
			}

			alerts, err := svc.DetectDrift(ctx)
			if err != nil {
				t.Fatalf("DetectDrift: %v", err)
			}

			// No drift expected for this agent.
			for _, a := range alerts {
				if a.AgentID == agentID {
					t.Errorf("unexpected drift alert for agent %s: metric=%s deviation=%f",
						agentID, a.MetricName, a.Deviation)
				}
			}
		})
	})
}

// TestProperty25_MissionTraceCompletenessSimple verifies that initiated trace has goal, agent, start;
// completed trace has outcome and duration.
// **Validates: Requirements 6.1, 6.3**
func TestProperty25_MissionTraceCompletenessSimple(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		storeName := fmt.Sprintf("obs25_%d", rapid.IntRange(0, 999999).Draw(rt, "storeID"))
		s := newObsStore(t, storeName)
		ctx := context.Background()
		svc := observability.New(s, observability.DefaultConfig(), nil, nil)

		goal := genGoal().Draw(rt, "goal")
		agentID := genAgentID().Draw(rt, "agentID")
		missionID := genMissionID().Draw(rt, "missionID")

		mission := &domain.Mission{
			ID:      missionID,
			AgentID: agentID,
			Payload: []byte(goal),
		}

		// Start: must have goal, agent, start.
		trace, err := svc.StartTrace(ctx, mission)
		if err != nil {
			t.Fatalf("StartTrace: %v", err)
		}
		if trace.Goal != goal {
			t.Errorf("goal = %q, want %q", trace.Goal, goal)
		}
		if trace.AgentID != agentID {
			t.Errorf("agentID = %q, want %q", trace.AgentID, agentID)
		}
		if trace.StartTime.IsZero() {
			t.Error("start time is zero")
		}

		// Complete: must have outcome and duration.
		outcome := genOutcome().Draw(rt, "outcome")
		err = svc.CompleteTrace(ctx, trace.TraceID, outcome)
		if err != nil {
			t.Fatalf("CompleteTrace: %v", err)
		}

		got, err := svc.GetTrace(ctx, trace.TraceID)
		if err != nil {
			t.Fatalf("GetTrace: %v", err)
		}
		if got.Outcome == "" {
			t.Error("completed trace has empty outcome")
		}
		if got.EndTime == nil {
			t.Error("completed trace has nil end time")
		}
		if got.Duration < 0 {
			t.Error("completed trace has negative duration")
		}
	})
}

// TestProperty26_TraceStepSpanCompleteness verifies that every step span has:
// name, start, duration ms, status, parent ID.
// **Validates: Requirements 6.2**
func TestProperty26_TraceStepSpanCompleteness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		storeName := fmt.Sprintf("obs26_%d", rapid.IntRange(0, 999999).Draw(rt, "storeID"))
		s := newObsStore(t, storeName)
		ctx := context.Background()
		svc := observability.New(s, observability.DefaultConfig(), nil, nil)

		missionID := genMissionID().Draw(rt, "missionID")
		agentID := genAgentID().Draw(rt, "agentID")
		mission := &domain.Mission{
			ID:      missionID,
			AgentID: agentID,
			Payload: []byte("test goal"),
		}

		trace, err := svc.StartTrace(ctx, mission)
		if err != nil {
			t.Fatalf("StartTrace: %v", err)
		}

		// Generate steps with parent chaining.
		numSteps := rapid.IntRange(1, 8).Draw(rt, "numSteps")
		parentSpanID := "root-span"
		for i := 0; i < numSteps; i++ {
			stepName := genStepName().Draw(rt, fmt.Sprintf("name%d", i))
			durationMs := rapid.IntRange(1, 10000).Draw(rt, fmt.Sprintf("dur%d", i))
			status := genStepStatus().Draw(rt, fmt.Sprintf("status%d", i))

			step := &domain.TraceStep{
				ParentSpanID: parentSpanID,
				Name:         stepName,
				StartTime:    time.Now().UTC(),
				Duration:     time.Duration(durationMs) * time.Millisecond,
				Status:       status,
			}

			err := svc.RecordStep(ctx, trace.TraceID, step)
			if err != nil {
				t.Fatalf("RecordStep %d: %v", i, err)
			}
			parentSpanID = step.SpanID
		}

		// Retrieve trace and validate each step's span fields.
		got, err := svc.GetTrace(ctx, trace.TraceID)
		if err != nil {
			t.Fatalf("GetTrace: %v", err)
		}

		if len(got.Steps) != numSteps {
			t.Fatalf("got %d steps, want %d", len(got.Steps), numSteps)
		}

		for i, step := range got.Steps {
			// Name must be non-empty.
			if step.Name == "" {
				t.Errorf("step %d: missing name", i)
			}
			// Start time must be set.
			if step.StartTime.IsZero() {
				t.Errorf("step %d: zero start time", i)
			}
			// Duration must be non-negative (stored as ms).
			if step.Duration < 0 {
				t.Errorf("step %d: negative duration %v", i, step.Duration)
			}
			// Status must be one of the valid values.
			if step.Status != domain.StepStatusSuccess &&
				step.Status != domain.StepStatusFailure &&
				step.Status != domain.StepStatusSkipped {
				t.Errorf("step %d: invalid status %q", i, step.Status)
			}
			// Parent span ID must be set (we always provide one).
			if step.ParentSpanID == "" {
				t.Errorf("step %d: missing parent span ID", i)
			}
		}
	})
}

// TestProperty27_DriftDetectionOnStatisticalDeviation verifies that >2σ over 1h
// produces drift_detected with metric name, observed value, and baseline value.
// **Validates: Requirements 6.6**
func TestProperty27_DriftDetectionOnStatisticalDeviation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		storeName := fmt.Sprintf("obs27_%d", rapid.IntRange(0, 999999).Draw(rt, "storeID"))
		s := newObsStore(t, storeName)
		ctx := context.Background()
		collector := &obsEventCollector{}

		config := observability.DefaultConfig()
		svc := observability.New(s, config, collector.emit, nil)

		now := time.Now().UTC()
		agentID := genAgentID().Draw(rt, "agentID")

		// Baseline: consistent 100% success, ~1s duration over 7 days.
		for d := 1; d <= 7; d++ {
			for i := 0; i < 10; i++ {
				traceTime := now.Add(-time.Duration(d) * 24 * time.Hour).Add(time.Duration(i) * time.Minute)
				endTime := traceTime.Add(1 * time.Second)
				trace := &domain.MissionTrace{
					TraceID:   fmt.Sprintf("bl27-%s-d%d-i%d", agentID, d, i),
					MissionID: "bl-m",
					AgentID:   agentID,
					Goal:      "baseline",
					Outcome:   domain.OutcomeSuccess,
					StartTime: traceTime,
					EndTime:   &endTime,
					Duration:  1 * time.Second,
					ExpiresAt: now.Add(30 * 24 * time.Hour),
				}
				if err := s.Traces().Create(ctx, trace); err != nil {
					t.Fatalf("create baseline: %v", err)
				}
			}
		}

		// Recent: generate a dramatically different duration (>2σ deviation).
		// Use 30s duration when baseline is ~1s → large deviation.
		driftDuration := time.Duration(rapid.IntRange(20, 60).Draw(rt, "driftDurationSec")) * time.Second
		for i := 0; i < 10; i++ {
			traceTime := now.Add(-30 * time.Minute).Add(time.Duration(i) * time.Minute)
			endTime := traceTime.Add(driftDuration)
			trace := &domain.MissionTrace{
				TraceID:   fmt.Sprintf("rc27-%s-i%d", agentID, i),
				MissionID: "recent-m",
				AgentID:   agentID,
				Goal:      "recent",
				Outcome:   domain.OutcomeSuccess, // Same success rate as baseline.
				StartTime: traceTime,
				EndTime:   &endTime,
				Duration:  driftDuration,
				ExpiresAt: now.Add(30 * 24 * time.Hour),
			}
			if err := s.Traces().Create(ctx, trace); err != nil {
				t.Fatalf("create recent: %v", err)
			}
		}

		alerts, err := svc.DetectDrift(ctx)
		if err != nil {
			t.Fatalf("DetectDrift: %v", err)
		}

		// Must produce a drift alert for mean_duration.
		found := false
		for _, a := range alerts {
			if a.AgentID == agentID && a.MetricName == "mean_duration" {
				found = true
				// Alert must contain metric name, observed, and baseline.
				if a.MetricName == "" {
					t.Error("alert missing metric name")
				}
				if a.ObservedValue == 0 {
					t.Error("alert missing observed value")
				}
				if a.BaselineValue == 0 {
					t.Error("alert missing baseline value")
				}
				if a.Deviation <= 2.0 {
					t.Errorf("deviation %f should be > 2.0", a.Deviation)
				}
			}
		}
		if !found {
			t.Errorf("expected mean_duration drift alert for agent %s (drift duration=%v)", agentID, driftDuration)
		}

		// Verify drift_detected event was emitted with payload fields.
		driftEventFound := false
		for _, e := range collector.events {
			if e.Type == "drift_detected" {
				payload := e.Payload
				if payload["metric_name"] == "mean_duration" && payload["agent_id"] == agentID {
					driftEventFound = true
					if payload["observed_value"] == nil {
						t.Error("drift event missing observed_value")
					}
					if payload["baseline_value"] == nil {
						t.Error("drift event missing baseline_value")
					}
				}
			}
		}
		if !driftEventFound {
			t.Error("expected drift_detected event with metric_name=mean_duration")
		}
	})
}
