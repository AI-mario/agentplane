package property

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/store"
	"github.com/agentplane/agentplane/internal/store/sqlite"
	"pgregory.net/rapid"
)

// --- Helpers ---

// newMigrationStore creates a uniquely-named in-memory SQLite store with migrations applied.
func newMigrationStore(t *testing.T, name string) *sqlite.SQLiteStore {
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

// --- Generators ---

// genAgentEntryForMigration generates a random valid AgentEntry with a given index for uniqueness.
func genAgentEntryForMigration(idx int) *rapid.Generator[*domain.AgentEntry] {
	return rapid.Custom(func(t *rapid.T) *domain.AgentEntry {
		suffix := rapid.StringMatching(`[a-z0-9]{4,6}`).Draw(t, "suffix")
		id := fmt.Sprintf("agent-%d-%s", idx, suffix)
		name := fmt.Sprintf("agt-%d-%s", idx, suffix)
		ns := rapid.SampledFrom([]string{"default", "engineering", "platform"}).Draw(t, "ns")
		version := fmt.Sprintf("%d.%d.%d",
			rapid.IntRange(0, 9).Draw(t, "major"),
			rapid.IntRange(0, 20).Draw(t, "minor"),
			rapid.IntRange(0, 50).Draw(t, "patch"))
		runtime := rapid.SampledFrom([]domain.RuntimeType{
			domain.RuntimeClaude, domain.RuntimeKiro, domain.RuntimeBedrock, domain.RuntimeGemini, domain.RuntimeCustom,
		}).Draw(t, "runtime")

		numCaps := rapid.IntRange(1, 4).Draw(t, "numCaps")
		caps := make([]domain.Capability, numCaps)
		for i := range caps {
			caps[i] = domain.Capability{
				Name: fmt.Sprintf("cap-%s-%d", id, i),
				Type: rapid.SampledFrom([]string{"mcp-tool", "a2a-message"}).Draw(t, fmt.Sprintf("capType%d", i)),
			}
		}

		numLabels := rapid.IntRange(0, 3).Draw(t, "numLabels")
		labels := make(map[string]string, numLabels)
		for i := 0; i < numLabels; i++ {
			labels[fmt.Sprintf("key%d", i)] = fmt.Sprintf("val%d", i)
		}

		now := time.Now().UTC().Truncate(time.Millisecond)

		return &domain.AgentEntry{
			ID:           id,
			Name:         name,
			Namespace:    ns,
			Version:      version,
			RuntimeType:  runtime,
			Status:       domain.AgentStatusActive,
			Capabilities: caps,
			Labels:       labels,
			SLOs:         domain.SLODefinition{Latency: &domain.LatencyBound{MaxMs: 5000}},
			Resources:    domain.ResourceLimits{MaxConcurrentMissions: 10, MaxMemoryMB: 512},
			Deployment:   domain.DeploymentStrategy{Type: "rolling", MaxInstances: 5},
			CreatedAt:    now,
			UpdatedAt:    now,
		}
	})
}

// genMissionForMigration generates a random valid Mission with a given index.
func genMissionForMigration(idx int) *rapid.Generator[*domain.Mission] {
	return rapid.Custom(func(t *rapid.T) *domain.Mission {
		suffix := rapid.StringMatching(`[a-z0-9]{4,6}`).Draw(t, "suffix")
		id := fmt.Sprintf("mission-%d-%s", idx, suffix)
		teamID := rapid.SampledFrom([]string{"team-a", "team-b", "team-c"}).Draw(t, "teamID")
		projectID := rapid.SampledFrom([]string{"proj-1", "proj-2", "proj-3"}).Draw(t, "projectID")
		numCaps := rapid.IntRange(1, 3).Draw(t, "numCaps")
		caps := make([]string, numCaps)
		for i := range caps {
			caps[i] = fmt.Sprintf("cap-%d", i)
		}
		priority := rapid.IntRange(1, 10).Draw(t, "priority")

		return &domain.Mission{
			ID:                   id,
			TeamID:               teamID,
			ProjectID:            projectID,
			Status:               domain.MissionStatusPending,
			RequiredCapabilities: caps,
			Payload:              []byte(fmt.Sprintf(`{"goal":"task-%s"}`, id)),
			Priority:             priority,
			Timeout:              time.Duration(rapid.IntRange(60, 3600).Draw(t, "timeout")) * time.Second,
			SubmittedAt:          time.Now().UTC().Truncate(time.Millisecond),
		}
	})
}

// genPolicyForMigration generates a random valid Policy with a given index.
func genPolicyForMigration(idx int) *rapid.Generator[*domain.Policy] {
	return rapid.Custom(func(t *rapid.T) *domain.Policy {
		suffix := rapid.StringMatching(`[a-z0-9]{4,6}`).Draw(t, "suffix")
		id := fmt.Sprintf("pol-%d-%s", idx, suffix)
		scope := rapid.SampledFrom([]domain.PolicyScope{
			domain.PolicyScopeOrganization, domain.PolicyScopeTeam,
		}).Draw(t, "scope")
		teamID := ""
		if scope == domain.PolicyScopeTeam {
			teamID = rapid.SampledFrom([]string{"team-a", "team-b"}).Draw(t, "teamID")
		}

		numRules := rapid.IntRange(1, 3).Draw(t, "numRules")
		rules := make([]domain.PolicyRule, numRules)
		for i := range rules {
			rules[i] = domain.PolicyRule{
				ID:        fmt.Sprintf("%s-r%d", id, i),
				Type:      domain.PolicyRuleTypeBudget,
				Condition: "true",
				Effect:    domain.PolicyEffectAllow,
			}
		}

		return &domain.Policy{
			ID:        id,
			Name:      fmt.Sprintf("policy-%s", id),
			Scope:     scope,
			TeamID:    teamID,
			Rules:     rules,
			Version:   1,
			UpdatedAt: time.Now().UTC().Truncate(time.Millisecond),
		}
	})
}

// genCostRecordForMigration generates a random valid CostRecord with a given index.
func genCostRecordForMigration(idx int) *rapid.Generator[*domain.CostRecord] {
	return rapid.Custom(func(t *rapid.T) *domain.CostRecord {
		suffix := rapid.StringMatching(`[a-z0-9]{4,6}`).Draw(t, "suffix")
		id := fmt.Sprintf("cost-%d-%s", idx, suffix)
		return &domain.CostRecord{
			ID:              id,
			MissionID:       fmt.Sprintf("m-%s", id),
			AgentID:         fmt.Sprintf("a-%s", id),
			TeamID:          rapid.SampledFrom([]string{"team-a", "team-b", "team-c"}).Draw(t, "teamID"),
			ProjectID:       rapid.SampledFrom([]string{"proj-1", "proj-2"}).Draw(t, "projectID"),
			TokenCount:      int64(rapid.IntRange(100, 10000).Draw(t, "tokens")),
			ComputeTimeMs:   int64(rapid.IntRange(100, 60000).Draw(t, "compute")),
			ToolInvocations: rapid.IntRange(1, 20).Draw(t, "tools"),
			TotalCost:       float64(rapid.IntRange(1, 10000).Draw(t, "cost")) / 100.0,
			RecordedAt:      time.Now().UTC().Truncate(time.Millisecond),
		}
	})
}

// genTraceForMigration generates a random valid MissionTrace with a given index.
func genTraceForMigration(idx int) *rapid.Generator[*domain.MissionTrace] {
	return rapid.Custom(func(t *rapid.T) *domain.MissionTrace {
		suffix := rapid.StringMatching(`[a-z0-9]{4,6}`).Draw(t, "suffix")
		id := fmt.Sprintf("trace-%d-%s", idx, suffix)
		outcome := rapid.SampledFrom([]domain.MissionOutcome{
			domain.OutcomeSuccess, domain.OutcomeFailure, domain.OutcomePartial, domain.OutcomeTimeout,
		}).Draw(t, "outcome")
		now := time.Now().UTC().Truncate(time.Millisecond)
		endTime := now.Add(5 * time.Second)

		return &domain.MissionTrace{
			TraceID:   id,
			MissionID: fmt.Sprintf("m-%s", id),
			AgentID:   fmt.Sprintf("a-%s", id),
			Goal:      fmt.Sprintf("goal for %s", id),
			Outcome:   outcome,
			StartTime: now,
			EndTime:   &endTime,
			Duration:  5 * time.Second,
			ExpiresAt: now.Add(30 * 24 * time.Hour),
		}
	})
}

// --- failingStore wraps a store to make specific table writes fail ---

type failingStore struct {
	store.Store
	failOnTable string
}

func (f *failingStore) Agents() store.AgentStore {
	if f.failOnTable == "agents" {
		return &failingAgentStore{}
	}
	return f.Store.Agents()
}

func (f *failingStore) Missions() store.MissionStore {
	if f.failOnTable == "missions" {
		return &failingMissionStore{}
	}
	return f.Store.Missions()
}

func (f *failingStore) Policies() store.PolicyStore {
	if f.failOnTable == "policies" {
		return &failingPolicyStore{}
	}
	return f.Store.Policies()
}

func (f *failingStore) Costs() store.CostStore {
	if f.failOnTable == "cost_events" {
		return &failingCostStore{}
	}
	return f.Store.Costs()
}

func (f *failingStore) Traces() store.TraceStore {
	if f.failOnTable == "mission_traces" {
		return &failingTraceStore{}
	}
	return f.Store.Traces()
}

type failingAgentStore struct{ store.AgentStore }

func (f *failingAgentStore) Create(_ context.Context, _ *domain.AgentEntry) error {
	return fmt.Errorf("simulated write failure")
}

type failingMissionStore struct{ store.MissionStore }

func (f *failingMissionStore) Create(_ context.Context, _ *domain.Mission) error {
	return fmt.Errorf("simulated write failure")
}

type failingPolicyStore struct{ store.PolicyStore }

func (f *failingPolicyStore) Create(_ context.Context, _ *domain.Policy) error {
	return fmt.Errorf("simulated write failure")
}

type failingCostStore struct{ store.CostStore }

func (f *failingCostStore) Record(_ context.Context, _ *domain.CostRecord) error {
	return fmt.Errorf("simulated write failure")
}

type failingTraceStore struct{ store.TraceStore }

func (f *failingTraceStore) Create(_ context.Context, _ *domain.MissionTrace) error {
	return fmt.Errorf("simulated write failure")
}

// --- Property Tests ---

// TestProperty54_DataMigrationCompleteness tests that after migration, record count per table
// is identical and all data is preserved.
// **Validates: Requirements 12.3**
func TestProperty54_DataMigrationCompleteness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()

		// Create unique store names using the test iteration.
		srcName := fmt.Sprintf("src54_%s", rapid.StringMatching(`[a-z]{6}`).Draw(rt, "srcSuffix"))
		tgtName := fmt.Sprintf("tgt54_%s", rapid.StringMatching(`[a-z]{6}`).Draw(rt, "tgtSuffix"))
		source := newMigrationStore(t, srcName)
		target := newMigrationStore(t, tgtName)

		// Generate and insert random agents.
		numAgents := rapid.IntRange(1, 5).Draw(rt, "numAgents")
		agents := make([]*domain.AgentEntry, numAgents)
		for i := 0; i < numAgents; i++ {
			agents[i] = genAgentEntryForMigration(i).Draw(rt, fmt.Sprintf("agent%d", i))
			if err := source.Agents().Create(ctx, agents[i]); err != nil {
				t.Fatalf("insert agent %d: %v", i, err)
			}
		}

		// Generate and insert random missions.
		numMissions := rapid.IntRange(0, 5).Draw(rt, "numMissions")
		missions := make([]*domain.Mission, numMissions)
		for i := 0; i < numMissions; i++ {
			missions[i] = genMissionForMigration(i).Draw(rt, fmt.Sprintf("mission%d", i))
			if err := source.Missions().Create(ctx, missions[i]); err != nil {
				t.Fatalf("insert mission %d: %v", i, err)
			}
		}

		// Generate and insert random policies.
		numPolicies := rapid.IntRange(0, 3).Draw(rt, "numPolicies")
		policies := make([]*domain.Policy, numPolicies)
		for i := 0; i < numPolicies; i++ {
			policies[i] = genPolicyForMigration(i).Draw(rt, fmt.Sprintf("policy%d", i))
			if err := source.Policies().Create(ctx, policies[i]); err != nil {
				t.Fatalf("insert policy %d: %v", i, err)
			}
		}

		// Generate and insert random cost records.
		numCosts := rapid.IntRange(0, 5).Draw(rt, "numCosts")
		costRecords := make([]*domain.CostRecord, numCosts)
		for i := 0; i < numCosts; i++ {
			costRecords[i] = genCostRecordForMigration(i).Draw(rt, fmt.Sprintf("cost%d", i))
			if err := source.Costs().Record(ctx, costRecords[i]); err != nil {
				t.Fatalf("insert cost %d: %v", i, err)
			}
		}

		// Generate and insert random traces.
		numTraces := rapid.IntRange(0, 3).Draw(rt, "numTraces")
		traces := make([]*domain.MissionTrace, numTraces)
		for i := 0; i < numTraces; i++ {
			traces[i] = genTraceForMigration(i).Draw(rt, fmt.Sprintf("trace%d", i))
			if err := source.Traces().Create(ctx, traces[i]); err != nil {
				t.Fatalf("insert trace %d: %v", i, err)
			}
		}

		// Run migration.
		migrator := store.NewMigrator(source, target)
		result, err := migrator.Run(ctx)
		if err != nil {
			t.Fatalf("migration failed: %v", err)
		}

		// Verify counts match.
		if result.TableCounts["agents"] != numAgents {
			t.Errorf("agents migrated: got %d, want %d", result.TableCounts["agents"], numAgents)
		}
		if result.TableCounts["missions"] != numMissions {
			t.Errorf("missions migrated: got %d, want %d", result.TableCounts["missions"], numMissions)
		}
		if result.TableCounts["policies"] != numPolicies {
			t.Errorf("policies migrated: got %d, want %d", result.TableCounts["policies"], numPolicies)
		}
		if result.TableCounts["cost_events"] != numCosts {
			t.Errorf("costs migrated: got %d, want %d", result.TableCounts["cost_events"], numCosts)
		}
		if result.TableCounts["mission_traces"] != numTraces {
			t.Errorf("traces migrated: got %d, want %d", result.TableCounts["mission_traces"], numTraces)
		}

		// Verify data preserved in target — agents.
		for _, a := range agents {
			got, err := target.Agents().Get(ctx, a.ID)
			if err != nil {
				t.Errorf("target agent get %s: %v", a.ID, err)
				continue
			}
			if got == nil {
				t.Errorf("target agent %s not found", a.ID)
				continue
			}
			if got.Name != a.Name {
				t.Errorf("agent %s name: got %q, want %q", a.ID, got.Name, a.Name)
			}
			if got.Version != a.Version {
				t.Errorf("agent %s version: got %q, want %q", a.ID, got.Version, a.Version)
			}
			if got.RuntimeType != a.RuntimeType {
				t.Errorf("agent %s runtime: got %q, want %q", a.ID, got.RuntimeType, a.RuntimeType)
			}
			if len(got.Capabilities) != len(a.Capabilities) {
				t.Errorf("agent %s caps: got %d, want %d", a.ID, len(got.Capabilities), len(a.Capabilities))
			}
		}

		// Verify missions preserved.
		for _, m := range missions {
			got, err := target.Missions().Get(ctx, m.ID)
			if err != nil {
				t.Errorf("target mission get %s: %v", m.ID, err)
				continue
			}
			if got == nil {
				t.Errorf("target mission %s not found", m.ID)
				continue
			}
			if got.TeamID != m.TeamID {
				t.Errorf("mission %s team: got %q, want %q", m.ID, got.TeamID, m.TeamID)
			}
			if got.Priority != m.Priority {
				t.Errorf("mission %s priority: got %d, want %d", m.ID, got.Priority, m.Priority)
			}
		}

		// Verify policies preserved.
		for _, p := range policies {
			got, err := target.Policies().Get(ctx, p.ID)
			if err != nil {
				t.Errorf("target policy get %s: %v", p.ID, err)
				continue
			}
			if got == nil {
				t.Errorf("target policy %s not found", p.ID)
				continue
			}
			if got.Name != p.Name {
				t.Errorf("policy %s name: got %q, want %q", p.ID, got.Name, p.Name)
			}
			if len(got.Rules) != len(p.Rules) {
				t.Errorf("policy %s rules: got %d, want %d", p.ID, len(got.Rules), len(p.Rules))
			}
		}

		// Verify cost records preserved.
		targetCosts, targetTotal, err := target.Costs().Query(ctx, domain.CostFilter{Limit: 100000})
		if err != nil {
			t.Fatalf("target costs query: %v", err)
		}
		if targetTotal != numCosts {
			t.Errorf("target cost total: got %d, want %d", targetTotal, numCosts)
		}
		costMap := make(map[string]*domain.CostRecord, len(costRecords))
		for _, c := range costRecords {
			costMap[c.ID] = c
		}
		for _, tc := range targetCosts {
			src, ok := costMap[tc.ID]
			if !ok {
				t.Errorf("unexpected cost record %s in target", tc.ID)
				continue
			}
			if tc.TokenCount != src.TokenCount {
				t.Errorf("cost %s tokens: got %d, want %d", tc.ID, tc.TokenCount, src.TokenCount)
			}
			if tc.TotalCost != src.TotalCost {
				t.Errorf("cost %s total: got %f, want %f", tc.ID, tc.TotalCost, src.TotalCost)
			}
		}

		// Verify traces preserved.
		for _, tr := range traces {
			got, err := target.Traces().Get(ctx, tr.TraceID)
			if err != nil {
				t.Errorf("target trace get %s: %v", tr.TraceID, err)
				continue
			}
			if got == nil {
				t.Errorf("target trace %s not found", tr.TraceID)
				continue
			}
			if got.Goal != tr.Goal {
				t.Errorf("trace %s goal: got %q, want %q", tr.TraceID, got.Goal, tr.Goal)
			}
			if got.Outcome != tr.Outcome {
				t.Errorf("trace %s outcome: got %q, want %q", tr.TraceID, got.Outcome, tr.Outcome)
			}
		}
	})
}

// TestProperty55_FailedMigrationSourcePreservation tests that source DB remains
// unmodified on failure.
// **Validates: Requirements 12.4**
func TestProperty55_FailedMigrationSourcePreservation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()

		srcName := fmt.Sprintf("src55_%s", rapid.StringMatching(`[a-z]{6}`).Draw(rt, "srcSuffix"))
		tgtName := fmt.Sprintf("tgt55_%s", rapid.StringMatching(`[a-z]{6}`).Draw(rt, "tgtSuffix"))
		source := newMigrationStore(t, srcName)
		targetBase := newMigrationStore(t, tgtName)

		// Insert data into source.
		numAgents := rapid.IntRange(1, 4).Draw(rt, "numAgents")
		agents := make([]*domain.AgentEntry, numAgents)
		for i := 0; i < numAgents; i++ {
			agents[i] = genAgentEntryForMigration(i).Draw(rt, fmt.Sprintf("agent%d", i))
			if err := source.Agents().Create(ctx, agents[i]); err != nil {
				t.Fatalf("insert agent: %v", err)
			}
		}

		numMissions := rapid.IntRange(1, 3).Draw(rt, "numMissions")
		originalMissions := make([]*domain.Mission, numMissions)
		for i := 0; i < numMissions; i++ {
			originalMissions[i] = genMissionForMigration(i).Draw(rt, fmt.Sprintf("mission%d", i))
			if err := source.Missions().Create(ctx, originalMissions[i]); err != nil {
				t.Fatalf("insert mission: %v", err)
			}
		}

		numPolicies := rapid.IntRange(1, 3).Draw(rt, "numPolicies")
		originalPolicies := make([]*domain.Policy, numPolicies)
		for i := 0; i < numPolicies; i++ {
			originalPolicies[i] = genPolicyForMigration(i).Draw(rt, fmt.Sprintf("policy%d", i))
			if err := source.Policies().Create(ctx, originalPolicies[i]); err != nil {
				t.Fatalf("insert policy: %v", err)
			}
		}

		// Insert cost records so cost_events table has data.
		numCosts := rapid.IntRange(1, 3).Draw(rt, "numCosts")
		for i := 0; i < numCosts; i++ {
			cr := genCostRecordForMigration(i).Draw(rt, fmt.Sprintf("cost%d", i))
			if err := source.Costs().Record(ctx, cr); err != nil {
				t.Fatalf("insert cost: %v", err)
			}
		}

		// Insert traces so mission_traces table has data.
		numTraces := rapid.IntRange(1, 3).Draw(rt, "numTraces")
		for i := 0; i < numTraces; i++ {
			tr := genTraceForMigration(i).Draw(rt, fmt.Sprintf("trace%d", i))
			if err := source.Traces().Create(ctx, tr); err != nil {
				t.Fatalf("insert trace: %v", err)
			}
		}

		// Choose which table to fail on. We pick tables that come AFTER agents
		// so the migration starts and then fails partway through.
		failTable := rapid.SampledFrom([]string{
			"missions", "policies", "cost_events", "mission_traces",
		}).Draw(rt, "failTable")

		// Create failing target.
		failTarget := &failingStore{Store: targetBase, failOnTable: failTable}

		// Run migration — should fail.
		migrator := store.NewMigrator(source, failTarget)
		_, err := migrator.Run(ctx)
		if err == nil {
			t.Fatal("expected migration to fail")
		}

		// Verify error mentions the failed table.
		if !strings.Contains(err.Error(), failTable) {
			t.Errorf("error should mention table %q, got: %v", failTable, err)
		}

		// Verify source data is unchanged — agents.
		sourceAgents, err := source.Agents().List(ctx, domain.AgentFilter{Limit: 100000})
		if err != nil {
			t.Fatalf("source agents list: %v", err)
		}
		if len(sourceAgents) != numAgents {
			t.Errorf("source agents count after failed migration: got %d, want %d", len(sourceAgents), numAgents)
		}
		for _, a := range agents {
			got, err := source.Agents().Get(ctx, a.ID)
			if err != nil {
				t.Errorf("source agent get %s: %v", a.ID, err)
				continue
			}
			if got == nil {
				t.Errorf("source agent %s missing after failed migration", a.ID)
				continue
			}
			if got.Name != a.Name {
				t.Errorf("source agent %s name changed: got %q, want %q", a.ID, got.Name, a.Name)
			}
			if got.Version != a.Version {
				t.Errorf("source agent %s version changed: got %q, want %q", a.ID, got.Version, a.Version)
			}
			if got.RuntimeType != a.RuntimeType {
				t.Errorf("source agent %s runtime changed: got %q, want %q", a.ID, got.RuntimeType, a.RuntimeType)
			}
		}

		// Verify source missions unchanged.
		sourceMissions, err := source.Missions().List(ctx, domain.MissionFilter{Limit: 100000})
		if err != nil {
			t.Fatalf("source missions list: %v", err)
		}
		if len(sourceMissions) != numMissions {
			t.Errorf("source missions count after failed migration: got %d, want %d", len(sourceMissions), numMissions)
		}
		for _, m := range originalMissions {
			got, err := source.Missions().Get(ctx, m.ID)
			if err != nil {
				t.Errorf("source mission get %s: %v", m.ID, err)
				continue
			}
			if got == nil {
				t.Errorf("source mission %s missing", m.ID)
				continue
			}
			if got.TeamID != m.TeamID {
				t.Errorf("source mission %s team changed: got %q, want %q", m.ID, got.TeamID, m.TeamID)
			}
		}

		// Verify source policies unchanged.
		sourcePolicies, err := source.Policies().List(ctx, domain.PolicyFilter{Limit: 100000})
		if err != nil {
			t.Fatalf("source policies list: %v", err)
		}
		if len(sourcePolicies) != numPolicies {
			t.Errorf("source policies count after failed migration: got %d, want %d", len(sourcePolicies), numPolicies)
		}
		for _, p := range originalPolicies {
			got, err := source.Policies().Get(ctx, p.ID)
			if err != nil {
				t.Errorf("source policy get %s: %v", p.ID, err)
				continue
			}
			if got == nil {
				t.Errorf("source policy %s missing", p.ID)
				continue
			}
			if got.Name != p.Name {
				t.Errorf("source policy %s name changed: got %q, want %q", p.ID, got.Name, p.Name)
			}
		}
	})
}
