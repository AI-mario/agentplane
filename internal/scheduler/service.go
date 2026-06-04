package scheduler

import (
	"context"

	"github.com/agentplane/agentplane/internal/domain"
)

// SchedulerService assigns missions to best-fit agents using weighted scoring.
type SchedulerService interface {
	// Schedule finds the best agent for a mission and assigns it.
	Schedule(ctx context.Context, mission *domain.Mission) (*domain.Assignment, error)
	// Reschedule re-evaluates queued missions.
	Reschedule(ctx context.Context) error
}
