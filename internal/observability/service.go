package observability

import (
	"context"

	"github.com/agentplane/agentplane/internal/domain"
)

// ObservabilityService captures mission traces and detects drift.
type ObservabilityService interface {
	// StartTrace creates a new mission trace.
	StartTrace(ctx context.Context, mission *domain.Mission) (*domain.MissionTrace, error)
	// RecordStep adds a span to an existing trace.
	RecordStep(ctx context.Context, traceID string, step *domain.TraceStep) error
	// CompleteTrace closes a mission trace with outcome.
	CompleteTrace(ctx context.Context, traceID string, outcome domain.MissionOutcome) error
	// DetectDrift evaluates metrics against historical baselines.
	DetectDrift(ctx context.Context) ([]domain.DriftAlert, error)
	// GetTrace retrieves a complete mission trace.
	GetTrace(ctx context.Context, traceID string) (*domain.MissionTrace, error)
}
