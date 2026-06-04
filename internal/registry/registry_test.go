package registry

import (
	"context"
	"fmt"
	"testing"

	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/store/sqlite"
)

func setupTestRegistry(t *testing.T) *Registry {
	t.Helper()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return New(s)
}

func validManifest(name, version string) domain.AgentManifest {
	return domain.AgentManifest{
		Name:        name,
		Namespace:   "default",
		Version:     version,
		RuntimeType: domain.RuntimeClaude,
		Capabilities: []domain.Capability{
			{Name: "code-review", Type: "mcp-tool"},
		},
		Labels: map[string]string{"env": "test"},
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
}

func TestRegister_Success(t *testing.T) {
	reg := setupTestRegistry(t)
	ctx := context.Background()

	entry, err := reg.Register(ctx, validManifest("test-agent", "1.0.0"))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if entry.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if entry.Name != "test-agent" {
		t.Errorf("name = %q, want %q", entry.Name, "test-agent")
	}
	if entry.Version != "1.0.0" {
		t.Errorf("version = %q, want %q", entry.Version, "1.0.0")
	}
	if entry.Status != domain.AgentStatusActive {
		t.Errorf("status = %q, want %q", entry.Status, domain.AgentStatusActive)
	}
}

func TestRegister_DuplicateRejected(t *testing.T) {
	reg := setupTestRegistry(t)
	ctx := context.Background()

	_, err := reg.Register(ctx, validManifest("dup-agent", "1.0.0"))
	if err != nil {
		t.Fatalf("first register: %v", err)
	}

	_, err = reg.Register(ctx, validManifest("dup-agent", "1.0.0"))
	if err != ErrDuplicateAgent {
		t.Errorf("expected ErrDuplicateAgent, got %v", err)
	}
}

func TestRegister_InvalidManifest(t *testing.T) {
	reg := setupTestRegistry(t)
	ctx := context.Background()

	// Missing name
	m := validManifest("", "1.0.0")
	_, err := reg.Register(ctx, m)
	if err == nil {
		t.Fatal("expected validation error for empty name")
	}
	valErr, ok := err.(*domain.ValidationError)
	if !ok {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
	if len(valErr.Violations) == 0 {
		t.Fatal("expected at least one violation")
	}
}

func TestGet_Found(t *testing.T) {
	reg := setupTestRegistry(t)
	ctx := context.Background()

	entry, _ := reg.Register(ctx, validManifest("get-agent", "1.0.0"))

	got, err := reg.Get(ctx, entry.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "get-agent" {
		t.Errorf("name = %q, want %q", got.Name, "get-agent")
	}
}

func TestGet_NotFound(t *testing.T) {
	reg := setupTestRegistry(t)
	ctx := context.Background()

	_, err := reg.Get(ctx, "nonexistent-id")
	if err != ErrAgentNotFound {
		t.Errorf("expected ErrAgentNotFound, got %v", err)
	}
}

func TestDeregister(t *testing.T) {
	reg := setupTestRegistry(t)
	ctx := context.Background()

	entry, _ := reg.Register(ctx, validManifest("del-agent", "1.0.0"))
	if err := reg.Deregister(ctx, entry.ID); err != nil {
		t.Fatalf("deregister: %v", err)
	}

	_, err := reg.Get(ctx, entry.ID)
	if err != ErrAgentNotFound {
		t.Errorf("expected ErrAgentNotFound after deregister, got %v", err)
	}
}

func TestDeregister_NotFound(t *testing.T) {
	reg := setupTestRegistry(t)
	ctx := context.Background()

	err := reg.Deregister(ctx, "no-such-id")
	if err != ErrAgentNotFound {
		t.Errorf("expected ErrAgentNotFound, got %v", err)
	}
}

func TestFindByCapability(t *testing.T) {
	reg := setupTestRegistry(t)
	ctx := context.Background()

	m1 := validManifest("cap-agent-1", "1.0.0")
	m1.Capabilities = []domain.Capability{{Name: "code-review", Type: "mcp-tool"}}
	reg.Register(ctx, m1)

	m2 := validManifest("cap-agent-2", "1.0.0")
	m2.Capabilities = []domain.Capability{{Name: "test-gen", Type: "a2a-message"}}
	reg.Register(ctx, m2)

	results, err := reg.FindByCapability(ctx, "code-review")
	if err != nil {
		t.Fatalf("find by capability: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Name != "cap-agent-1" {
		t.Errorf("expected cap-agent-1, got %s", results[0].Name)
	}
}

func TestFindByCapability_Empty(t *testing.T) {
	reg := setupTestRegistry(t)
	ctx := context.Background()

	_, err := reg.FindByCapability(ctx, "")
	if err == nil {
		t.Fatal("expected error for empty capability")
	}
}

func TestList_NoFilter(t *testing.T) {
	reg := setupTestRegistry(t)
	ctx := context.Background()

	reg.Register(ctx, validManifest("list-a", "1.0.0"))
	reg.Register(ctx, validManifest("list-b", "1.0.0"))

	results, err := reg.List(ctx, domain.AgentFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

func TestList_FilterByName(t *testing.T) {
	reg := setupTestRegistry(t)
	ctx := context.Background()

	reg.Register(ctx, validManifest("filter-target", "1.0.0"))
	reg.Register(ctx, validManifest("filter-other", "1.0.0"))

	results, err := reg.List(ctx, domain.AgentFilter{Name: "filter-target"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
	if results[0].Name != "filter-target" {
		t.Errorf("expected filter-target, got %s", results[0].Name)
	}
}

func TestList_FilterByRuntime(t *testing.T) {
	reg := setupTestRegistry(t)
	ctx := context.Background()

	m1 := validManifest("rt-claude", "1.0.0")
	m1.RuntimeType = domain.RuntimeClaude
	reg.Register(ctx, m1)

	m2 := validManifest("rt-bedrock", "1.0.0")
	m2.RuntimeType = domain.RuntimeBedrock
	reg.Register(ctx, m2)

	results, err := reg.List(ctx, domain.AgentFilter{Runtime: domain.RuntimeBedrock})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestList_LimitOffset(t *testing.T) {
	reg := setupTestRegistry(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		reg.Register(ctx, validManifest("page-agent-"+string(rune('a'+i)), "1.0.0"))
	}

	results, err := reg.List(ctx, domain.AgentFilter{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results with limit, got %d", len(results))
	}
}

func TestVersionHistory_Cap(t *testing.T) {
	reg := setupTestRegistry(t)
	ctx := context.Background()

	// Register 52 different versions of same agent (different name per version since same name+version is duplicate)
	// Actually, per the logic, duplicate = same name + same version.
	// Different versions are OK.
	for i := 0; i < 52; i++ {
		m := validManifest("versioned-agent", "1.0."+itoa(i))
		_, err := reg.Register(ctx, m)
		if err != nil {
			t.Fatalf("register version %d: %v", i, err)
		}
	}

	// Each registration creates a new agent entry (different version).
	// The version history is per-agent-ID, so each has exactly 1 version.
	// To properly test cap, we need to add versions to the same agent ID.
	// Let's test via the store directly for the version cap logic.

	// Create one agent and manually add 52 versions.
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer s.Close()

	reg2 := New(s)
	entry, err := reg2.Register(ctx, validManifest("cap-test", "1.0.0"))
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	// Add 51 more versions manually to hit the cap.
	for i := 1; i <= 51; i++ {
		v := &domain.AgentVersion{
			ID:      "ver-" + itoa(i),
			AgentID: entry.ID,
			Version: "1.0." + itoa(i),
		}
		if err := s.Agents().AddVersion(ctx, v); err != nil {
			t.Fatalf("add version %d: %v", i, err)
		}
	}

	// Now trigger trim.
	if err := reg2.trimVersionHistory(ctx, entry.ID); err != nil {
		t.Fatalf("trim: %v", err)
	}

	versions, err := s.Agents().ListVersions(ctx, entry.ID)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != MaxVersionHistory {
		t.Errorf("expected %d versions after trim, got %d", MaxVersionHistory, len(versions))
	}
}

func itoa(i int) string {
	return fmt.Sprintf("%d", i)
}
