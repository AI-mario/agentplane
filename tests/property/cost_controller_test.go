package property

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/agentplane/agentplane/internal/cost"
	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/store/sqlite"
	"pgregory.net/rapid"
)

// --- Test Helpers ---

// newCostStore creates a uniquely-named in-memory SQLite store for cost tests.
func newCostStore(t *testing.T, name string) *sqlite.SQLiteStore {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&_foreign_keys=on", name)
	s, err := sqlite.New(dsn)
	if err != nil {
		t.Fatalf("new store %s: %v", name, err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate %s: %v", name, err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// setupTeamBudget creates a budget for a team with given cap, accumulated cost, and period start.
func setupTeamBudget(t *testing.T, s *sqlite.SQLiteStore, teamID string, cap, accumulated float64, periodStart time.Time, blocked bool) {
	t.Helper()
	budget := &domain.Budget{
		ID:              "budget-" + teamID,
		TeamID:          teamID,
		CapAmount:       cap,
		PeriodType:      "monthly",
		PeriodStart:     periodStart,
		AccumulatedCost: accumulated,
		IsBlocked:       blocked,
	}
	if err := s.Budgets().Set(context.Background(), budget); err != nil {
		t.Fatalf("set budget for %s: %v", teamID, err)
	}
}

// drainEvents reads all available events from the channel and returns them.
func drainEvents(ch <-chan domain.SystemEvent) []domain.SystemEvent {
	var events []domain.SystemEvent
	for {
		select {
		case ev := <-ch:
			events = append(events, ev)
		default:
			return events
		}
	}
}

// --- Generators ---

// genCostEvent generates a valid CostEvent with random but realistic fields.
func genCostEvent(teamID, projectID string) *rapid.Generator[*domain.CostEvent] {
	return rapid.Custom(func(t *rapid.T) *domain.CostEvent {
		missionID := fmt.Sprintf("mission-%s", rapid.StringMatching(`[a-z0-9]{6}`).Draw(t, "missionID"))
		agentID := fmt.Sprintf("agent-%s", rapid.StringMatching(`[a-z0-9]{4}`).Draw(t, "agentID"))
		tokens := rapid.Int64Range(1, 100000).Draw(t, "tokens")
		computeMs := rapid.Int64Range(1, 60000).Draw(t, "computeMs")
		toolInvocations := rapid.IntRange(0, 50).Draw(t, "toolInvocations")
		totalCost := float64(rapid.IntRange(1, 10000).Draw(t, "costCents")) / 100.0

		return &domain.CostEvent{
			MissionID:       missionID,
			AgentID:         agentID,
			TeamID:          teamID,
			ProjectID:       projectID,
			TokenCount:      tokens,
			ComputeTimeMs:   computeMs,
			ToolInvocations: toolInvocations,
			TotalCost:       totalCost,
		}
	})
}

// --- Property Tests ---

// TestProperty28_CostEventCompleteness tests that recorded cost events contain all required fields
// and are correctly attributed to team/project/mission.
// **Validates: Requirements 7.1, 7.2**
func TestProperty28_CostEventCompleteness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()
		storeName := fmt.Sprintf("cost28_%s", rapid.StringMatching(`[a-z]{6}`).Draw(rt, "name"))
		s := newCostStore(t, storeName)

		emitter, _ := cost.NewChannelEmitter(100)
		ctrl := cost.New(s, emitter, cost.DefaultConfig())

		teamID := fmt.Sprintf("team-%s", rapid.StringMatching(`[a-z]{4}`).Draw(rt, "teamID"))
		projectID := fmt.Sprintf("proj-%s", rapid.StringMatching(`[a-z]{4}`).Draw(rt, "projectID"))

		// Set up budget so recording doesn't fail on aggregation.
		setupTeamBudget(t, s, teamID, 100000.0, 0.0, time.Now().AddDate(0, 0, -1), false)

		event := genCostEvent(teamID, projectID).Draw(rt, "event")

		err := ctrl.RecordUsage(ctx, event)
		if err != nil {
			t.Fatalf("RecordUsage: %v", err)
		}

		// Query the recorded cost.
		report, err := ctrl.QueryCosts(ctx, domain.CostFilter{TeamID: teamID})
		if err != nil {
			t.Fatalf("QueryCosts: %v", err)
		}
		if len(report.Records) != 1 {
			t.Fatalf("expected 1 record, got %d", len(report.Records))
		}

		r := report.Records[0]

		// Verify completeness: token count (integer).
		if r.TokenCount != event.TokenCount {
			t.Errorf("token count: got %d, want %d", r.TokenCount, event.TokenCount)
		}
		// Verify completeness: compute time (ms integer).
		if r.ComputeTimeMs != event.ComputeTimeMs {
			t.Errorf("compute time: got %d, want %d", r.ComputeTimeMs, event.ComputeTimeMs)
		}
		// Verify completeness: tool invocations (integer).
		if r.ToolInvocations != event.ToolInvocations {
			t.Errorf("tool invocations: got %d, want %d", r.ToolInvocations, event.ToolInvocations)
		}
		// Verify completeness: total cost (normalized monetary unit).
		if r.TotalCost != event.TotalCost {
			t.Errorf("total cost: got %f, want %f", r.TotalCost, event.TotalCost)
		}
		// Verify attribution: team.
		if r.TeamID != teamID {
			t.Errorf("team: got %q, want %q", r.TeamID, teamID)
		}
		// Verify attribution: project.
		if r.ProjectID != projectID {
			t.Errorf("project: got %q, want %q", r.ProjectID, projectID)
		}
		// Verify attribution: mission.
		if r.MissionID != event.MissionID {
			t.Errorf("mission: got %q, want %q", r.MissionID, event.MissionID)
		}
		// Verify non-empty ID.
		if r.ID == "" {
			t.Error("record ID is empty")
		}
		// Verify timestamp set.
		if r.RecordedAt.IsZero() {
			t.Error("recorded_at is zero")
		}
	})
}

// TestProperty15_CostRecordingAndAttribution tests that costs are recorded as integers for
// tokens/compute/tools, normalized for total, and attributed to correct team/project/mission.
// **Validates: Requirements 7.1, 7.2**
func TestProperty15_CostRecordingAndAttribution(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()
		storeName := fmt.Sprintf("cost15_%s", rapid.StringMatching(`[a-z]{6}`).Draw(rt, "name"))
		s := newCostStore(t, storeName)

		emitter, _ := cost.NewChannelEmitter(100)
		ctrl := cost.New(s, emitter, cost.DefaultConfig())

		// Generate multiple teams/projects.
		numEvents := rapid.IntRange(1, 5).Draw(rt, "numEvents")

		type eventRecord struct {
			teamID    string
			projectID string
			missionID string
			event     *domain.CostEvent
		}
		var recorded []eventRecord

		for i := 0; i < numEvents; i++ {
			teamID := fmt.Sprintf("team-%d-%s", i, rapid.StringMatching(`[a-z]{3}`).Draw(rt, fmt.Sprintf("team%d", i)))
			projectID := fmt.Sprintf("proj-%d-%s", i, rapid.StringMatching(`[a-z]{3}`).Draw(rt, fmt.Sprintf("proj%d", i)))

			setupTeamBudget(t, s, teamID, 100000.0, 0.0, time.Now().AddDate(0, 0, -1), false)

			ev := genCostEvent(teamID, projectID).Draw(rt, fmt.Sprintf("event%d", i))
			err := ctrl.RecordUsage(ctx, ev)
			if err != nil {
				t.Fatalf("RecordUsage %d: %v", i, err)
			}
			recorded = append(recorded, eventRecord{teamID, projectID, ev.MissionID, ev})
		}

		// Verify each event attributed correctly.
		for _, rec := range recorded {
			report, err := ctrl.QueryCosts(ctx, domain.CostFilter{TeamID: rec.teamID, ProjectID: rec.projectID})
			if err != nil {
				t.Fatalf("QueryCosts for %s/%s: %v", rec.teamID, rec.projectID, err)
			}

			found := false
			for _, r := range report.Records {
				if r.MissionID == rec.missionID {
					found = true
					if r.TeamID != rec.teamID {
						t.Errorf("wrong team attribution: got %q, want %q", r.TeamID, rec.teamID)
					}
					if r.ProjectID != rec.projectID {
						t.Errorf("wrong project attribution: got %q, want %q", r.ProjectID, rec.projectID)
					}
					// Integer types preserved.
					if r.TokenCount != rec.event.TokenCount {
						t.Errorf("tokens not preserved: got %d, want %d", r.TokenCount, rec.event.TokenCount)
					}
					if r.ComputeTimeMs != rec.event.ComputeTimeMs {
						t.Errorf("compute not preserved: got %d, want %d", r.ComputeTimeMs, rec.event.ComputeTimeMs)
					}
					if r.ToolInvocations != rec.event.ToolInvocations {
						t.Errorf("tools not preserved: got %d, want %d", r.ToolInvocations, rec.event.ToolInvocations)
					}
				}
			}
			if !found {
				t.Errorf("record for mission %s not found under team %s / project %s", rec.missionID, rec.teamID, rec.projectID)
			}
		}
	})
}

// TestProperty16_BudgetThresholdEnforcement tests that 80% triggers warning event
// and 100% blocks new missions.
// **Validates: Requirements 7.3, 7.4**
func TestProperty16_BudgetThresholdEnforcement(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()
		storeName := fmt.Sprintf("cost16_%s", rapid.StringMatching(`[a-z]{6}`).Draw(rt, "name"))
		s := newCostStore(t, storeName)

		emitter, events := cost.NewChannelEmitter(100)
		ctrl := cost.New(s, emitter, cost.DefaultConfig())

		teamID := fmt.Sprintf("team-%s", rapid.StringMatching(`[a-z]{6}`).Draw(rt, "teamID"))
		budgetCap := float64(rapid.IntRange(100, 10000).Draw(rt, "budgetCap"))

		// Start just below 80%.
		warningThreshold := budgetCap * 0.80
		startAccumulated := warningThreshold - 1.0
		if startAccumulated < 0 {
			startAccumulated = 0
		}

		setupTeamBudget(t, s, teamID, budgetCap, startAccumulated, time.Now().AddDate(0, 0, -1), false)

		// Push past 80% threshold.
		event := &domain.CostEvent{
			MissionID:       "mission-threshold",
			AgentID:         "agent-threshold",
			TeamID:          teamID,
			ProjectID:       "project-threshold",
			TokenCount:      100,
			ComputeTimeMs:   50,
			ToolInvocations: 1,
			TotalCost:       2.0, // This pushes past 80%
		}

		err := ctrl.RecordUsage(ctx, event)
		if err != nil {
			t.Fatalf("RecordUsage: %v", err)
		}

		// Verify budget_warning emitted.
		evs := drainEvents(events)
		hasWarning := false
		for _, ev := range evs {
			if ev.Type == "budget_warning" {
				hasWarning = true
			}
		}
		if !hasWarning {
			t.Error("expected budget_warning event at 80% threshold")
		}

		// Now push to 100%.
		remaining := budgetCap - (startAccumulated + 2.0)
		if remaining > 0 {
			blockEvent := &domain.CostEvent{
				MissionID:       "mission-block",
				AgentID:         "agent-block",
				TeamID:          teamID,
				ProjectID:       "project-block",
				TokenCount:      200,
				ComputeTimeMs:   100,
				ToolInvocations: 2,
				TotalCost:       remaining + 1.0, // Exceed cap
			}
			err = ctrl.RecordUsage(ctx, blockEvent)
			if err != nil {
				t.Fatalf("RecordUsage block: %v", err)
			}
		}

		// CheckBudget should now deny new missions.
		allowed, err := ctrl.CheckBudget(ctx, teamID, 1.0)
		if err != nil {
			t.Fatalf("CheckBudget: %v", err)
		}
		if allowed {
			t.Error("expected new missions blocked at 100% budget")
		}
	})
}

// TestProperty29_BudgetWarningAt80Percent tests that accumulated cost reaching 80% of
// budget cap triggers a budget_warning event.
// **Validates: Requirements 7.3**
func TestProperty29_BudgetWarningAt80Percent(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()
		storeName := fmt.Sprintf("cost29_%s", rapid.StringMatching(`[a-z]{6}`).Draw(rt, "name"))
		s := newCostStore(t, storeName)

		emitter, events := cost.NewChannelEmitter(100)
		ctrl := cost.New(s, emitter, cost.DefaultConfig())

		teamID := fmt.Sprintf("team-%s", rapid.StringMatching(`[a-z]{6}`).Draw(rt, "teamID"))
		budgetCap := float64(rapid.IntRange(100, 5000).Draw(rt, "cap"))

		// Start with accumulated just below 80%.
		threshold80 := budgetCap * 0.80
		// Ensure we start below threshold.
		startPct := float64(rapid.IntRange(50, 79).Draw(rt, "startPct")) / 100.0
		startAccumulated := budgetCap * startPct

		setupTeamBudget(t, s, teamID, budgetCap, startAccumulated, time.Now().AddDate(0, 0, -1), false)

		// Add cost that pushes past 80% but not 100%.
		costToAdd := (threshold80 - startAccumulated) + 0.01
		if startAccumulated+costToAdd >= budgetCap {
			costToAdd = (threshold80 - startAccumulated) + 0.01
			if startAccumulated+costToAdd >= budgetCap {
				// Edge case: just set to slightly above 80% mark
				costToAdd = threshold80 - startAccumulated + 0.01
			}
		}

		event := &domain.CostEvent{
			MissionID:       "mission-warn",
			AgentID:         "agent-warn",
			TeamID:          teamID,
			ProjectID:       "proj-warn",
			TokenCount:      500,
			ComputeTimeMs:   200,
			ToolInvocations: 3,
			TotalCost:       costToAdd,
		}

		err := ctrl.RecordUsage(ctx, event)
		if err != nil {
			t.Fatalf("RecordUsage: %v", err)
		}

		// Verify budget_warning was emitted.
		evs := drainEvents(events)
		hasWarning := false
		for _, ev := range evs {
			if ev.Type == "budget_warning" {
				hasWarning = true
				// Verify payload contains team.
				if ev.Payload["team_id"] != teamID {
					t.Errorf("warning payload team_id: got %v, want %s", ev.Payload["team_id"], teamID)
				}
			}
		}
		if !hasWarning {
			t.Errorf("expected budget_warning when accumulated crosses 80%% (cap=%f, start=%f, added=%f)",
				budgetCap, startAccumulated, costToAdd)
		}
	})
}

// TestProperty30_BudgetBlockAt100Percent tests that accumulated cost reaching 100% blocks
// new missions but allows in-progress ones to complete.
// **Validates: Requirements 7.4**
func TestProperty30_BudgetBlockAt100Percent(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()
		storeName := fmt.Sprintf("cost30_%s", rapid.StringMatching(`[a-z]{6}`).Draw(rt, "name"))
		s := newCostStore(t, storeName)

		emitter, events := cost.NewChannelEmitter(100)
		ctrl := cost.New(s, emitter, cost.DefaultConfig())

		teamID := fmt.Sprintf("team-%s", rapid.StringMatching(`[a-z]{6}`).Draw(rt, "teamID"))
		budgetCap := float64(rapid.IntRange(50, 5000).Draw(rt, "cap"))

		// Start just below 100%.
		startAccumulated := budgetCap - 1.0
		if startAccumulated < 0 {
			startAccumulated = 0
		}

		setupTeamBudget(t, s, teamID, budgetCap, startAccumulated, time.Now().AddDate(0, 0, -1), false)

		// Push past 100%.
		event := &domain.CostEvent{
			MissionID:       "mission-block",
			AgentID:         "agent-block",
			TeamID:          teamID,
			ProjectID:       "proj-block",
			TokenCount:      1000,
			ComputeTimeMs:   500,
			ToolInvocations: 5,
			TotalCost:       2.0, // Pushes past cap
		}

		err := ctrl.RecordUsage(ctx, event)
		if err != nil {
			t.Fatalf("RecordUsage: %v", err)
		}

		// Verify budget_exceeded event emitted.
		evs := drainEvents(events)
		hasExceeded := false
		for _, ev := range evs {
			if ev.Type == "budget_exceeded" {
				hasExceeded = true
			}
		}
		if !hasExceeded {
			t.Error("expected budget_exceeded event at 100%")
		}

		// New missions should be blocked.
		allowed, err := ctrl.CheckBudget(ctx, teamID, 1.0)
		if err != nil {
			t.Fatalf("CheckBudget: %v", err)
		}
		if allowed {
			t.Error("expected new missions blocked when budget at 100%")
		}

		// Verify budget status reflects blocked state.
		status, err := ctrl.GetBudgetStatus(ctx, teamID)
		if err != nil {
			t.Fatalf("GetBudgetStatus: %v", err)
		}
		if !status.IsBlocked {
			t.Error("expected budget status IsBlocked=true")
		}

		// In-progress missions can still record cost (RecordUsage succeeds).
		inProgressEvent := &domain.CostEvent{
			MissionID:       "mission-in-progress",
			AgentID:         "agent-block",
			TeamID:          teamID,
			ProjectID:       "proj-block",
			TokenCount:      10,
			ComputeTimeMs:   5,
			ToolInvocations: 1,
			TotalCost:       0.01,
		}
		err = ctrl.RecordUsage(ctx, inProgressEvent)
		if err != nil {
			t.Errorf("in-progress mission should still be able to record cost: %v", err)
		}

		_ = events
	})
}

// TestProperty31_CostQueryFilterCorrectness tests that all returned records match all
// specified filters and result count never exceeds 1000.
// **Validates: Requirements 7.6**
func TestProperty31_CostQueryFilterCorrectness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()
		storeName := fmt.Sprintf("cost31_%s", rapid.StringMatching(`[a-z]{6}`).Draw(rt, "name"))
		s := newCostStore(t, storeName)

		emitter, _ := cost.NewChannelEmitter(100)
		ctrl := cost.New(s, emitter, cost.DefaultConfig())

		// Create records across multiple teams/projects/agents.
		teams := []string{"team-alpha", "team-beta", "team-gamma"}
		projects := []string{"proj-x", "proj-y", "proj-z"}
		agents := []string{"agent-1", "agent-2", "agent-3"}

		// Set up budgets for all teams.
		for _, team := range teams {
			setupTeamBudget(t, s, team, 100000.0, 0.0, time.Now().AddDate(0, 0, -1), false)
		}

		numRecords := rapid.IntRange(5, 20).Draw(rt, "numRecords")
		for i := 0; i < numRecords; i++ {
			teamIdx := rapid.IntRange(0, len(teams)-1).Draw(rt, fmt.Sprintf("teamIdx%d", i))
			projIdx := rapid.IntRange(0, len(projects)-1).Draw(rt, fmt.Sprintf("projIdx%d", i))
			agentIdx := rapid.IntRange(0, len(agents)-1).Draw(rt, fmt.Sprintf("agentIdx%d", i))

			event := &domain.CostEvent{
				MissionID:       fmt.Sprintf("mission-%d", i),
				AgentID:         agents[agentIdx],
				TeamID:          teams[teamIdx],
				ProjectID:       projects[projIdx],
				TokenCount:      int64(rapid.IntRange(10, 1000).Draw(rt, fmt.Sprintf("tok%d", i))),
				ComputeTimeMs:   int64(rapid.IntRange(10, 5000).Draw(rt, fmt.Sprintf("comp%d", i))),
				ToolInvocations: rapid.IntRange(0, 10).Draw(rt, fmt.Sprintf("tool%d", i)),
				TotalCost:       float64(rapid.IntRange(1, 100).Draw(rt, fmt.Sprintf("cost%d", i))) / 100.0,
			}
			if err := ctrl.RecordUsage(ctx, event); err != nil {
				t.Fatalf("RecordUsage %d: %v", i, err)
			}
		}

		// Query with a random filter combination.
		filterTeamIdx := rapid.IntRange(0, len(teams)-1).Draw(rt, "filterTeam")
		filterTeam := teams[filterTeamIdx]

		useProjectFilter := rapid.Bool().Draw(rt, "useProjectFilter")
		useAgentFilter := rapid.Bool().Draw(rt, "useAgentFilter")

		filter := domain.CostFilter{
			TeamID: filterTeam,
		}
		if useProjectFilter {
			projIdx := rapid.IntRange(0, len(projects)-1).Draw(rt, "filterProj")
			filter.ProjectID = projects[projIdx]
		}
		if useAgentFilter {
			agentIdx := rapid.IntRange(0, len(agents)-1).Draw(rt, "filterAgent")
			filter.AgentID = agents[agentIdx]
		}

		report, err := ctrl.QueryCosts(ctx, filter)
		if err != nil {
			t.Fatalf("QueryCosts: %v", err)
		}

		// Verify: all returned records match all filter criteria.
		for _, r := range report.Records {
			if r.TeamID != filter.TeamID {
				t.Errorf("record team %q doesn't match filter %q", r.TeamID, filter.TeamID)
			}
			if filter.ProjectID != "" && r.ProjectID != filter.ProjectID {
				t.Errorf("record project %q doesn't match filter %q", r.ProjectID, filter.ProjectID)
			}
			if filter.AgentID != "" && r.AgentID != filter.AgentID {
				t.Errorf("record agent %q doesn't match filter %q", r.AgentID, filter.AgentID)
			}
		}

		// Verify: count never exceeds 1000.
		if len(report.Records) > 1000 {
			t.Errorf("returned %d records, max allowed is 1000", len(report.Records))
		}
	})
}

// TestProperty32_UnattributedCostHandling tests that cost events with unknown team/project
// are recorded as "unattributed" and a cost_attribution_failure event is emitted.
// **Validates: Requirements 7.7**
func TestProperty32_UnattributedCostHandling(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()
		storeName := fmt.Sprintf("cost32_%s", rapid.StringMatching(`[a-z]{6}`).Draw(rt, "name"))
		s := newCostStore(t, storeName)

		emitter, events := cost.NewChannelEmitter(100)
		ctrl := cost.New(s, emitter, cost.DefaultConfig())

		// Randomly omit team, project, or both.
		omitTeam := rapid.Bool().Draw(rt, "omitTeam")
		omitProject := rapid.Bool().Draw(rt, "omitProject")

		// Ensure at least one is omitted.
		if !omitTeam && !omitProject {
			omitTeam = true
		}

		teamID := ""
		projectID := ""
		if !omitTeam {
			teamID = fmt.Sprintf("team-%s", rapid.StringMatching(`[a-z]{4}`).Draw(rt, "teamVal"))
		}
		if !omitProject {
			projectID = fmt.Sprintf("proj-%s", rapid.StringMatching(`[a-z]{4}`).Draw(rt, "projVal"))
		}

		event := &domain.CostEvent{
			MissionID:       fmt.Sprintf("mission-%s", rapid.StringMatching(`[a-z0-9]{6}`).Draw(rt, "mID")),
			AgentID:         fmt.Sprintf("agent-%s", rapid.StringMatching(`[a-z]{4}`).Draw(rt, "aID")),
			TeamID:          teamID,
			ProjectID:       projectID,
			TokenCount:      rapid.Int64Range(1, 5000).Draw(rt, "tokens"),
			ComputeTimeMs:   rapid.Int64Range(1, 10000).Draw(rt, "compute"),
			ToolInvocations: rapid.IntRange(0, 20).Draw(rt, "tools"),
			TotalCost:       float64(rapid.IntRange(1, 500).Draw(rt, "cost")) / 100.0,
		}

		err := ctrl.RecordUsage(ctx, event)
		if err != nil {
			t.Fatalf("RecordUsage: %v", err)
		}

		// Verify cost_attribution_failure event emitted.
		evs := drainEvents(events)
		hasAttrFailure := false
		for _, ev := range evs {
			if ev.Type == "cost_attribution_failure" {
				hasAttrFailure = true
			}
		}
		if !hasAttrFailure {
			t.Error("expected cost_attribution_failure event for unattributed cost")
		}

		// Verify record stored with "unattributed" for missing fields.
		report, err := ctrl.QueryCosts(ctx, domain.CostFilter{TeamID: "unattributed"})
		if err != nil {
			t.Fatalf("QueryCosts: %v", err)
		}

		found := false
		for _, r := range report.Records {
			if r.MissionID == event.MissionID {
				found = true
				if omitTeam && r.TeamID != "unattributed" {
					t.Errorf("expected team 'unattributed', got %q", r.TeamID)
				}
				if omitProject && r.ProjectID != "unattributed" {
					t.Errorf("expected project 'unattributed', got %q", r.ProjectID)
				}
			}
		}
		if !found {
			// If team wasn't omitted, it won't be in "unattributed" filter.
			// Check under actual team.
			if !omitTeam {
				report2, _ := ctrl.QueryCosts(ctx, domain.CostFilter{TeamID: teamID})
				for _, r := range report2.Records {
					if r.MissionID == event.MissionID {
						found = true
						if omitProject && r.ProjectID != "unattributed" {
							t.Errorf("expected project 'unattributed', got %q", r.ProjectID)
						}
					}
				}
			}
			if !found {
				t.Error("unattributed record not found in store")
			}
		}
	})
}

// TestProperty33_BudgetPeriodReset tests that at the start of a new budget period,
// accumulated cost resets to zero and team is unblocked.
// **Validates: Requirements 7.8**
func TestProperty33_BudgetPeriodReset(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()
		storeName := fmt.Sprintf("cost33_%s", rapid.StringMatching(`[a-z]{6}`).Draw(rt, "name"))
		s := newCostStore(t, storeName)

		emitter, _ := cost.NewChannelEmitter(100)
		ctrl := cost.New(s, emitter, cost.DefaultConfig())

		teamID := fmt.Sprintf("team-%s", rapid.StringMatching(`[a-z]{6}`).Draw(rt, "teamID"))
		budgetCap := float64(rapid.IntRange(100, 5000).Draw(rt, "cap"))
		accumulatedCost := float64(rapid.IntRange(50, int(budgetCap)).Draw(rt, "accumulated"))

		// Set period_start far in the past (beyond one period).
		monthsAgo := rapid.IntRange(2, 12).Draw(rt, "monthsAgo")
		periodStart := time.Now().AddDate(0, -monthsAgo, 0)

		// Team was blocked from previous period.
		setupTeamBudget(t, s, teamID, budgetCap, accumulatedCost, periodStart, true)

		// Access budget status → triggers period reset.
		status, err := ctrl.GetBudgetStatus(ctx, teamID)
		if err != nil {
			t.Fatalf("GetBudgetStatus: %v", err)
		}

		// After reset: accumulated should be zero.
		if status.AccumulatedCost != 0.0 {
			t.Errorf("expected accumulated 0 after period reset, got %f", status.AccumulatedCost)
		}

		// After reset: team should be unblocked.
		if status.IsBlocked {
			t.Error("expected team unblocked after period reset")
		}

		// CheckBudget should now allow.
		allowed, err := ctrl.CheckBudget(ctx, teamID, 1.0)
		if err != nil {
			t.Fatalf("CheckBudget after reset: %v", err)
		}
		if !allowed {
			t.Error("expected missions allowed after budget period reset")
		}
	})
}
