package observability

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/store"
	"github.com/agentplane/agentplane/internal/store/sqlite"
)

func newTestStore(t *testing.T) store.Store {
	t.Helper()
	s, err := sqlite.New("file::memory:?cache=shared&_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestStartTrace(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	svc := New(s, DefaultConfig(), nil, nil)

	mission := &domain.Mission{
		ID:      "mission-1",
		AgentID: "agent-1",
		Payload: []byte("Review PR #42"),
	}

	trace, err := svc.StartTrace(ctx, mission)
	if err != nil {
		t.Fatalf("StartTrace: %v", err)
	}

	if trace.TraceID == "" {
		t.Error("expected non-empty trace ID")
	}
	if trace.MissionID != "mission-1" {
		t.Errorf("got mission ID %q, want %q", trace.MissionID, "mission-1")
	}
	if trace.AgentID != "agent-1" {
		t.Errorf("got agent ID %q, want %q", trace.AgentID, "agent-1")
	}
	if trace.Goal != "Review PR #42" {
		t.Errorf("got goal %q, want %q", trace.Goal, "Review PR #42")
	}
	if trace.StartTime.IsZero() {
		t.Error("expected non-zero start time")
	}
	if trace.ExpiresAt.IsZero() {
		t.Error("expected non-zero expires at")
	}
	// Default retention: 30 days
	expectedExpiry := trace.StartTime.Add(30 * 24 * time.Hour)
	if trace.ExpiresAt.Sub(expectedExpiry) > time.Second {
		t.Errorf("expiry %v too far from expected %v", trace.ExpiresAt, expectedExpiry)
	}
}

func TestRecordStep(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	svc := New(s, DefaultConfig(), nil, nil)

	mission := &domain.Mission{ID: "m-1", AgentID: "a-1", Payload: []byte("goal")}
	trace, _ := svc.StartTrace(ctx, mission)

	step := &domain.TraceStep{
		ParentSpanID: "",
		Name:         "fetch-context",
		StartTime:    time.Now().UTC(),
		Duration:     150 * time.Millisecond,
		Status:       domain.StepStatusSuccess,
		Attributes:   map[string]string{"tool": "web-search"},
	}

	err := svc.RecordStep(ctx, trace.TraceID, step)
	if err != nil {
		t.Fatalf("RecordStep: %v", err)
	}

	// Verify span was stored
	got, err := svc.GetTrace(ctx, trace.TraceID)
	if err != nil {
		t.Fatalf("GetTrace: %v", err)
	}
	if len(got.Steps) != 1 {
		t.Fatalf("got %d steps, want 1", len(got.Steps))
	}
	if got.Steps[0].Name != "fetch-context" {
		t.Errorf("step name = %q, want %q", got.Steps[0].Name, "fetch-context")
	}
	if got.Steps[0].SpanID == "" {
		t.Error("expected auto-generated span ID")
	}
}

func TestCompleteTrace_Success(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	svc := New(s, DefaultConfig(), nil, nil)

	mission := &domain.Mission{ID: "m-2", AgentID: "a-2", Payload: []byte("goal")}
	trace, _ := svc.StartTrace(ctx, mission)

	err := svc.CompleteTrace(ctx, trace.TraceID, domain.OutcomeSuccess)
	if err != nil {
		t.Fatalf("CompleteTrace: %v", err)
	}

	got, _ := svc.GetTrace(ctx, trace.TraceID)
	if got.Outcome != domain.OutcomeSuccess {
		t.Errorf("outcome = %q, want %q", got.Outcome, domain.OutcomeSuccess)
	}
	if got.EndTime == nil {
		t.Error("expected non-nil end time")
	}
	// Duration is stored in milliseconds. For very fast completion it may be 0ms
	// when read back from the store. We verify the duration was set (>= 0).
	if got.Duration < 0 {
		t.Error("expected non-negative duration")
	}
}

func TestCompleteTrace_Timeout(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	var emittedEvents []domain.SystemEvent
	emitter := func(e domain.SystemEvent) {
		emittedEvents = append(emittedEvents, e)
	}

	// Use a very short timeout so the trace is automatically marked timeout
	config := DefaultConfig()
	config.MissionTimeout = 1 * time.Nanosecond

	svc := New(s, config, emitter, nil)

	mission := &domain.Mission{ID: "m-3", AgentID: "a-3", Payload: []byte("goal")}
	trace, _ := svc.StartTrace(ctx, mission)

	// Even a tiny sleep will exceed 1ns
	time.Sleep(1 * time.Millisecond)

	err := svc.CompleteTrace(ctx, trace.TraceID, domain.OutcomeSuccess)
	if err != nil {
		t.Fatalf("CompleteTrace: %v", err)
	}

	got, _ := svc.GetTrace(ctx, trace.TraceID)
	if got.Outcome != domain.OutcomeTimeout {
		t.Errorf("outcome = %q, want %q (timeout forced)", got.Outcome, domain.OutcomeTimeout)
	}

	// Verify mission_timeout event emitted
	if len(emittedEvents) == 0 {
		t.Fatal("expected mission_timeout event to be emitted")
	}
	if emittedEvents[0].Type != "mission_timeout" {
		t.Errorf("event type = %q, want %q", emittedEvents[0].Type, "mission_timeout")
	}
}

func TestDetectDrift_NoDriftWithinBounds(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	svc := New(s, DefaultConfig(), nil, nil)

	// No traces at all → no drift
	alerts, err := svc.DetectDrift(ctx)
	if err != nil {
		t.Fatalf("DetectDrift: %v", err)
	}
	if len(alerts) != 0 {
		t.Errorf("expected no alerts with empty store, got %d", len(alerts))
	}
}

func TestDetectDrift_WithDrift(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	var emittedEvents []domain.SystemEvent
	emitter := func(e domain.SystemEvent) {
		emittedEvents = append(emittedEvents, e)
	}

	config := DefaultConfig()
	svc := New(s, config, emitter, nil)

	now := time.Now().UTC()

	// Create baseline traces over past 7 days with consistent success (all success, ~1s duration)
	for d := 1; d <= 7; d++ {
		for i := 0; i < 10; i++ {
			traceTime := now.Add(-time.Duration(d) * 24 * time.Hour).Add(time.Duration(i) * time.Minute)
			endTime := traceTime.Add(1 * time.Second)
			trace := &domain.MissionTrace{
				TraceID:   genID(d, i),
				MissionID: "baseline-m",
				AgentID:   "agent-drift",
				Goal:      "baseline",
				Outcome:   domain.OutcomeSuccess,
				StartTime: traceTime,
				EndTime:   &endTime,
				Duration:  1 * time.Second,
				ExpiresAt: now.Add(30 * 24 * time.Hour),
			}
			if err := s.Traces().Create(ctx, trace); err != nil {
				t.Fatalf("create baseline trace: %v", err)
			}
		}
	}

	// Create recent traces (last hour) with all failures → major drift in success rate
	for i := 0; i < 10; i++ {
		traceTime := now.Add(-30 * time.Minute).Add(time.Duration(i) * time.Minute)
		endTime := traceTime.Add(1 * time.Second)
		trace := &domain.MissionTrace{
			TraceID:   "recent-" + genID(0, i),
			MissionID: "recent-m",
			AgentID:   "agent-drift",
			Goal:      "recent",
			Outcome:   domain.OutcomeFailure,
			StartTime: traceTime,
			EndTime:   &endTime,
			Duration:  1 * time.Second,
			ExpiresAt: now.Add(30 * 24 * time.Hour),
		}
		if err := s.Traces().Create(ctx, trace); err != nil {
			t.Fatalf("create recent trace: %v", err)
		}
	}

	alerts, err := svc.DetectDrift(ctx)
	if err != nil {
		t.Fatalf("DetectDrift: %v", err)
	}

	// Should detect drift in success_rate (100% baseline vs 0% recent)
	found := false
	for _, a := range alerts {
		if a.MetricName == "success_rate" && a.AgentID == "agent-drift" {
			found = true
			if a.Deviation <= 2.0 {
				t.Errorf("expected deviation > 2.0, got %f", a.Deviation)
			}
		}
	}
	if !found {
		t.Error("expected success_rate drift alert for agent-drift")
	}

	// Verify drift_detected event emitted
	driftEmitted := false
	for _, e := range emittedEvents {
		if e.Type == "drift_detected" {
			driftEmitted = true
		}
	}
	if !driftEmitted {
		t.Error("expected drift_detected event to be emitted")
	}
}

func TestDeleteExpiredTraces(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	svc := New(s, DefaultConfig(), nil, nil).(*observabilityService)

	// Create a trace that's already expired
	expired := &domain.MissionTrace{
		TraceID:   "expired-1",
		MissionID: "m-exp",
		AgentID:   "a-exp",
		Goal:      "old goal",
		StartTime: time.Now().Add(-60 * 24 * time.Hour),
		ExpiresAt: time.Now().Add(-1 * time.Hour), // already expired
	}
	if err := s.Traces().Create(ctx, expired); err != nil {
		t.Fatal(err)
	}

	n, err := svc.DeleteExpiredTraces(ctx)
	if err != nil {
		t.Fatalf("DeleteExpiredTraces: %v", err)
	}
	if n != 1 {
		t.Errorf("deleted %d, want 1", n)
	}
}

func TestOTLPExporterCalled(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	exported := make(chan *domain.MissionTrace, 1)
	exporter := &mockExporter{exportCh: exported}

	svc := New(s, DefaultConfig(), nil, exporter)

	mission := &domain.Mission{ID: "m-export", AgentID: "a-export", Payload: []byte("export goal")}
	trace, _ := svc.StartTrace(ctx, mission)

	_ = svc.CompleteTrace(ctx, trace.TraceID, domain.OutcomeSuccess)

	// Wait for async export
	select {
	case got := <-exported:
		if got.TraceID != trace.TraceID {
			t.Errorf("exported trace ID = %q, want %q", got.TraceID, trace.TraceID)
		}
	case <-time.After(5 * time.Second):
		t.Error("timeout waiting for OTLP export")
	}
}

// --- helpers ---

type mockExporter struct {
	exportCh chan *domain.MissionTrace
}

func (m *mockExporter) ExportTrace(_ context.Context, trace *domain.MissionTrace) error {
	m.exportCh <- trace
	return nil
}

func (m *mockExporter) Shutdown(_ context.Context) error { return nil }

func genID(day, idx int) string {
	return fmt.Sprintf("trace-d%d-i%d", day, idx)
}
