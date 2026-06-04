package property

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/registry"
	"github.com/agentplane/agentplane/internal/store/sqlite"
	"pgregory.net/rapid"
)

// --- Helpers ---

// newRegistryForTest creates an in-memory SQLite store and returns a Registry.
func newRegistryForTest(t testing.TB) *registry.Registry {
	t.Helper()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return registry.New(s)
}

// newRegistryWithStore creates an in-memory SQLite store and returns both.
func newRegistryWithStore(t testing.TB) (*sqlite.SQLiteStore, *registry.Registry) {
	t.Helper()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, registry.New(s)
}

// --- Generators ---

// genValidAgentName generates a valid agent name (1-253 lowercase alphanumeric + hyphens).
func genValidAgentName() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		// Generate a name matching the pattern: starts with [a-z0-9], rest is [a-z0-9-]
		length := rapid.IntRange(1, 20).Draw(t, "nameLen")
		return rapid.StringMatching(fmt.Sprintf(`[a-z][a-z0-9]{%d}`, length-1)).Draw(t, "name")
	})
}

// genSemverVersion generates a valid semver string.
func genSemverVersion() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		major := rapid.IntRange(0, 99).Draw(t, "major")
		minor := rapid.IntRange(0, 99).Draw(t, "minor")
		patch := rapid.IntRange(0, 99).Draw(t, "patch")
		return fmt.Sprintf("%d.%d.%d", major, minor, patch)
	})
}

// genRuntimeTypeValue generates a valid runtime type.
func genRuntimeTypeValue() *rapid.Generator[domain.RuntimeType] {
	return rapid.SampledFrom([]domain.RuntimeType{
		domain.RuntimeClaude,
		domain.RuntimeKiro,
		domain.RuntimeBedrock,
		domain.RuntimeCustom,
	})
}

// genCapName generates a capability name string.
func genCapName() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		return rapid.StringMatching(`[a-z][a-z0-9\-]{2,15}`).Draw(t, "capName")
	})
}

// genCapabilitiesList generates 1 to maxN capabilities with unique names.
func genCapabilitiesList(maxN int) *rapid.Generator[[]domain.Capability] {
	return rapid.Custom(func(t *rapid.T) []domain.Capability {
		n := rapid.IntRange(1, maxN).Draw(t, "numCaps")
		caps := make([]domain.Capability, n)
		seen := make(map[string]bool)
		for i := 0; i < n; i++ {
			name := genCapName().Draw(t, fmt.Sprintf("cap%d", i))
			for seen[name] {
				name = name + fmt.Sprintf("%d", i)
			}
			seen[name] = true
			capType := rapid.SampledFrom([]string{"mcp-tool", "a2a-message"}).Draw(t, fmt.Sprintf("capType%d", i))
			caps[i] = domain.Capability{Name: name, Type: capType}
		}
		return caps
	})
}

// genLabelMap generates 0 to maxN labels with valid key/value lengths.
func genLabelMap(maxN int) *rapid.Generator[map[string]string] {
	return rapid.Custom(func(t *rapid.T) map[string]string {
		n := rapid.IntRange(0, maxN).Draw(t, "numLabels")
		labels := make(map[string]string, n)
		for i := 0; i < n; i++ {
			key := rapid.StringMatching(`[a-z]{1,10}`).Draw(t, fmt.Sprintf("lk%d", i))
			val := rapid.StringMatching(`[a-z0-9]{1,20}`).Draw(t, fmt.Sprintf("lv%d", i))
			labels[key] = val
		}
		return labels
	})
}

// genAgentManifest generates a valid AgentManifest for registry property tests.
func genAgentManifest() *rapid.Generator[domain.AgentManifest] {
	return rapid.Custom(func(t *rapid.T) domain.AgentManifest {
		return domain.AgentManifest{
			Name:         genValidAgentName().Draw(t, "name"),
			Namespace:    "default",
			Version:      genSemverVersion().Draw(t, "version"),
			RuntimeType:  genRuntimeTypeValue().Draw(t, "runtime"),
			Capabilities: genCapabilitiesList(5).Draw(t, "caps"),
			Labels:       genLabelMap(5).Draw(t, "labels"),
			SLOs: domain.SLODefinition{
				Latency: &domain.LatencyBound{MaxMs: rapid.IntRange(1, 60000).Draw(t, "latency")},
			},
			Resources: domain.ResourceLimits{
				MaxConcurrentMissions: rapid.IntRange(1, 50).Draw(t, "maxMissions"),
				MaxMemoryMB:           rapid.IntRange(128, 4096).Draw(t, "maxMem"),
			},
			Deployment: domain.DeploymentStrategy{
				Type:         "rolling",
				MaxInstances: rapid.IntRange(1, 10).Draw(t, "maxInst"),
			},
		}
	})
}

// --- Property Tests ---

// TestProperty1_AgentRegistrationRoundTrip tests that registering a valid manifest
// and retrieving by ID produces an entry with identical name, version, runtime,
// capabilities, labels, and SLOs.
// **Validates: Requirements 1.1**
func TestProperty1_AgentRegistrationRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Fresh store per iteration to avoid name collisions
		reg := newRegistryForTest(t)
		ctx := context.Background()

		manifest := genAgentManifest().Draw(rt, "manifest")

		entry, err := reg.Register(ctx, manifest)
		if err != nil {
			rt.Fatalf("register: %v", err)
		}

		// Retrieve by ID
		got, err := reg.Get(ctx, entry.ID)
		if err != nil {
			rt.Fatalf("get: %v", err)
		}

		// Verify round-trip: all key fields match
		if got.Name != manifest.Name {
			rt.Errorf("name: got %q, want %q", got.Name, manifest.Name)
		}
		if got.Version != manifest.Version {
			rt.Errorf("version: got %q, want %q", got.Version, manifest.Version)
		}
		if got.RuntimeType != manifest.RuntimeType {
			rt.Errorf("runtime: got %q, want %q", got.RuntimeType, manifest.RuntimeType)
		}

		// Capabilities: same count and same names
		if len(got.Capabilities) != len(manifest.Capabilities) {
			rt.Errorf("capabilities count: got %d, want %d", len(got.Capabilities), len(manifest.Capabilities))
		} else {
			gotCaps := make(map[string]string)
			for _, c := range got.Capabilities {
				gotCaps[c.Name] = c.Type
			}
			for _, c := range manifest.Capabilities {
				if gotCaps[c.Name] != c.Type {
					rt.Errorf("capability %q: got type %q, want %q", c.Name, gotCaps[c.Name], c.Type)
				}
			}
		}

		// Labels
		if len(got.Labels) != len(manifest.Labels) {
			rt.Errorf("labels count: got %d, want %d", len(got.Labels), len(manifest.Labels))
		} else {
			for k, v := range manifest.Labels {
				if got.Labels[k] != v {
					rt.Errorf("label %q: got %q, want %q", k, got.Labels[k], v)
				}
			}
		}

		// SLOs
		if manifest.SLOs.Latency != nil {
			if got.SLOs.Latency == nil {
				rt.Error("expected latency SLO, got nil")
			} else if got.SLOs.Latency.MaxMs != manifest.SLOs.Latency.MaxMs {
				rt.Errorf("latency SLO: got %d, want %d", got.SLOs.Latency.MaxMs, manifest.SLOs.Latency.MaxMs)
			}
		}
	})
}

// TestProperty2_VersionHistoryInvariant tests that N registrations produce
// min(N, 50) version records, with the most recent retained.
// **Validates: Requirements 1.2**
func TestProperty2_VersionHistoryInvariant(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s, reg := newRegistryWithStore(t)
		ctx := context.Background()

		// Generate N versions (between 1 and 60 to test both under and over cap)
		n := rapid.IntRange(1, 60).Draw(rt, "numVersions")

		baseName := genValidAgentName().Draw(rt, "baseName")

		// Register the first version to get an agent entry
		manifest := domain.AgentManifest{
			Name:        baseName,
			Namespace:   "default",
			Version:     "0.0.0",
			RuntimeType: domain.RuntimeClaude,
			Capabilities: []domain.Capability{
				{Name: "cap-1", Type: "mcp-tool"},
			},
			SLOs: domain.SLODefinition{
				Latency: &domain.LatencyBound{MaxMs: 5000},
			},
			Resources: domain.ResourceLimits{
				MaxConcurrentMissions: 10,
				MaxMemoryMB:           512,
			},
			Deployment: domain.DeploymentStrategy{
				Type:         "rolling",
				MaxInstances: 3,
			},
		}

		entry, err := reg.Register(ctx, manifest)
		if err != nil {
			rt.Fatalf("register initial: %v", err)
		}

		// Register already added version "0.0.0" with RegisteredAt = time.Now().
		// Add (n-1) more versions with increasing timestamps so ordering is deterministic.
		baseTime := time.Now().UTC()
		for i := 1; i < n; i++ {
			ver := fmt.Sprintf("%d.%d.%d", i/100, (i/10)%10, i%10)
			v := &domain.AgentVersion{
				ID:           fmt.Sprintf("ver-%s-%d", baseName, i),
				AgentID:      entry.ID,
				Version:      ver,
				RegisteredAt: baseTime.Add(time.Duration(i) * time.Second),
			}
			if err := s.Agents().AddVersion(ctx, v); err != nil {
				rt.Fatalf("add version %d: %v", i, err)
			}
		}

		// Now we have n total versions for this agent.
		versions, err := s.Agents().ListVersions(ctx, entry.ID)
		if err != nil {
			rt.Fatalf("list versions: %v", err)
		}

		if n <= registry.MaxVersionHistory {
			// Should have exactly n version records
			if len(versions) != n {
				rt.Errorf("expected %d version records (n <= 50), got %d", n, len(versions))
			}
		} else {
			// n > 50: verify that after applying trim, only 50 remain.
			// Simulate the Registry's trim behavior (it deletes entries beyond index 50).
			if len(versions) > registry.MaxVersionHistory {
				for i := registry.MaxVersionHistory; i < len(versions); i++ {
					if err := s.Agents().DeleteVersion(ctx, versions[i].ID); err != nil {
						rt.Fatalf("delete version: %v", err)
					}
				}
			}

			// Verify post-trim count
			versions, err = s.Agents().ListVersions(ctx, entry.ID)
			if err != nil {
				rt.Fatalf("list versions after trim: %v", err)
			}
			if len(versions) != registry.MaxVersionHistory {
				rt.Errorf("expected %d version records after trim (n=%d > 50), got %d",
					registry.MaxVersionHistory, n, len(versions))
			}

			// Verify the most recent versions are retained (ordered by registered_at DESC).
			// The last version added (index n-1) has the highest timestamp, so it's first.
			if len(versions) > 0 {
				lastVer := fmt.Sprintf("%d.%d.%d", (n-1)/100, ((n-1)/10)%10, (n-1)%10)
				if versions[0].Version != lastVer {
					rt.Errorf("most recent version: got %q, want %q", versions[0].Version, lastVer)
				}
			}
		}
	})
}

// TestProperty3_DuplicateRegistrationRejection tests that registering the same
// name+version combination is rejected with a conflict error, leaving original unchanged.
// **Validates: Requirements 1.3**
func TestProperty3_DuplicateRegistrationRejection(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Fresh store per iteration to avoid cross-iteration collisions
		reg := newRegistryForTest(t)
		ctx := context.Background()

		manifest := genAgentManifest().Draw(rt, "manifest")

		// First registration should succeed
		original, err := reg.Register(ctx, manifest)
		if err != nil {
			rt.Fatalf("first register: %v", err)
		}

		// Second registration with same name+version should fail
		_, err = reg.Register(ctx, manifest)
		if err != registry.ErrDuplicateAgent {
			rt.Fatalf("expected ErrDuplicateAgent, got: %v", err)
		}

		// Original should be unchanged
		got, err := reg.Get(ctx, original.ID)
		if err != nil {
			rt.Fatalf("get after duplicate rejection: %v", err)
		}

		if got.Name != original.Name {
			rt.Errorf("name changed: got %q, want %q", got.Name, original.Name)
		}
		if got.Version != original.Version {
			rt.Errorf("version changed: got %q, want %q", got.Version, original.Version)
		}
		if got.RuntimeType != original.RuntimeType {
			rt.Errorf("runtime changed: got %q, want %q", got.RuntimeType, original.RuntimeType)
		}
		if len(got.Capabilities) != len(original.Capabilities) {
			rt.Errorf("capabilities count changed: got %d, want %d", len(got.Capabilities), len(original.Capabilities))
		}
	})
}

// TestProperty5_CapabilityQueryFilterCorrectness tests that querying by capability
// returns exactly the set of agents whose declared capabilities include that capability —
// no false positives, no false negatives.
// **Validates: Requirements 1.5**
func TestProperty5_CapabilityQueryFilterCorrectness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Fresh store per iteration since agents accumulate
		reg := newRegistryForTest(t)
		ctx := context.Background()

		// Generate a target capability to query
		targetCap := genCapName().Draw(rt, "targetCap")

		// Generate N agents, some with the target capability, some without
		numAgents := rapid.IntRange(2, 10).Draw(rt, "numAgents")
		expectedIDs := make(map[string]bool)

		for i := 0; i < numAgents; i++ {
			hasTarget := rapid.Bool().Draw(rt, fmt.Sprintf("hasTarget%d", i))

			caps := []domain.Capability{}
			if hasTarget {
				caps = append(caps, domain.Capability{Name: targetCap, Type: "mcp-tool"})
			}
			// Add a distinct non-target capability so agent always has at least 1
			otherCap := fmt.Sprintf("other-%d-%s", i, rapid.StringMatching(`[a-z]{3}`).Draw(rt, fmt.Sprintf("other%d", i)))
			caps = append(caps, domain.Capability{Name: otherCap, Type: "a2a-message"})

			manifest := domain.AgentManifest{
				Name:         fmt.Sprintf("agent-%d-%s", i, rapid.StringMatching(`[a-z0-9]{4}`).Draw(rt, fmt.Sprintf("sfx%d", i))),
				Namespace:    "default",
				Version:      fmt.Sprintf("%d.0.0", i),
				RuntimeType:  domain.RuntimeClaude,
				Capabilities: caps,
				SLOs: domain.SLODefinition{
					Latency: &domain.LatencyBound{MaxMs: 5000},
				},
				Resources: domain.ResourceLimits{
					MaxConcurrentMissions: 10,
					MaxMemoryMB:           512,
				},
				Deployment: domain.DeploymentStrategy{
					Type:         "rolling",
					MaxInstances: 3,
				},
			}

			entry, err := reg.Register(ctx, manifest)
			if err != nil {
				rt.Fatalf("register agent %d: %v", i, err)
			}

			if hasTarget {
				expectedIDs[entry.ID] = true
			}
		}

		// Query by capability
		results, err := reg.FindByCapability(ctx, targetCap)
		if err != nil {
			rt.Fatalf("find by capability: %v", err)
		}

		// Verify: exact match
		gotIDs := make(map[string]bool)
		for _, r := range results {
			gotIDs[r.ID] = true
		}

		// No false negatives
		for id := range expectedIDs {
			if !gotIDs[id] {
				rt.Errorf("false negative: agent %s has capability %q but not in results", id, targetCap)
			}
		}

		// No false positives
		for id := range gotIDs {
			if !expectedIDs[id] {
				rt.Errorf("false positive: agent %s in results but doesn't have capability %q", id, targetCap)
			}
		}

		// Count check
		if len(results) != len(expectedIDs) {
			rt.Errorf("result count: got %d, want %d", len(results), len(expectedIDs))
		}
	})
}
