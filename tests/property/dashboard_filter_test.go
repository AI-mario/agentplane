package property

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/gateway"
	"pgregory.net/rapid"
)

// --- Property 56: Dashboard Filter Correctness ---
// For any filter combination (name, tag, capability, status), all returned agents
// match all specified filter criteria and the result count ≤ 100.
// **Validates: Requirements 13.5**

// dashboardMockRegistry implements a registry that fully filters agents in memory,
// simulating the behavior the dashboard expects from the REST API.
type dashboardMockRegistry struct {
	agents []*domain.AgentEntry
}

func (m *dashboardMockRegistry) Register(ctx context.Context, manifest domain.AgentManifest) (*domain.AgentEntry, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *dashboardMockRegistry) Get(ctx context.Context, id string) (*domain.AgentEntry, error) {
	for _, a := range m.agents {
		if a.ID == id {
			return a, nil
		}
	}
	return nil, fmt.Errorf("agent not found")
}

func (m *dashboardMockRegistry) List(ctx context.Context, filter domain.AgentFilter) ([]*domain.AgentEntry, error) {
	var result []*domain.AgentEntry
	for _, a := range m.agents {
		if !agentMatchesFilter(a, filter) {
			continue
		}
		result = append(result, a)
	}
	// Enforce dashboard max 100 results.
	if len(result) > 100 {
		result = result[:100]
	}
	return result, nil
}

func (m *dashboardMockRegistry) FindByCapability(ctx context.Context, capability string) ([]*domain.AgentEntry, error) {
	return nil, nil
}

func (m *dashboardMockRegistry) Deregister(ctx context.Context, id string) error {
	return fmt.Errorf("not implemented")
}

// agentMatchesFilter checks if an agent satisfies all filter criteria.
func agentMatchesFilter(a *domain.AgentEntry, f domain.AgentFilter) bool {
	if f.Name != "" && a.Name != f.Name {
		return false
	}
	if f.Status != "" && a.Status != f.Status {
		return false
	}
	if f.Runtime != "" && a.RuntimeType != f.Runtime {
		return false
	}
	if f.Capability != "" {
		found := false
		for _, cap := range a.Capabilities {
			if cap.Name == f.Capability {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if len(f.Label) > 0 {
		for k, v := range f.Label {
			if a.Labels == nil {
				return false
			}
			if a.Labels[k] != v {
				return false
			}
		}
	}
	return true
}

// --- Generators for dashboard filter property tests ---

var dashboardCapNames = []string{
	"code-review", "test-generation", "deployment", "monitoring",
	"documentation", "security-scan", "data-analysis", "chat",
}

var dashboardStatuses = []domain.AgentStatus{
	domain.AgentStatusActive,
	domain.AgentStatusInactive,
	domain.AgentStatusDraining,
	domain.AgentStatusDeprecated,
	domain.AgentStatusUnhealthy,
	domain.AgentStatusIdleSafe,
}

var dashboardRuntimes = []domain.RuntimeType{
	domain.RuntimeClaude,
	domain.RuntimeKiro,
	domain.RuntimeBedrock,
	domain.RuntimeGemini,
	domain.RuntimeCustom,
}

var dashboardLabelKeys = []string{"team", "env", "region", "tier", "owner"}
var dashboardLabelValues = []string{"platform", "backend", "production", "staging", "us-east-1", "eu-west-1", "critical", "standard"}

// genDashboardAgent generates a random agent entry with varied attributes.
func genDashboardAgent(idx int) *rapid.Generator[*domain.AgentEntry] {
	return rapid.Custom(func(t *rapid.T) *domain.AgentEntry {
		name := rapid.SampledFrom([]string{
			"code-reviewer", "test-runner", "deployer", "monitor-bot",
			"doc-writer", "security-agent", "analyst", "chat-agent",
			"helper", "orchestrator", "planner", "executor",
		}).Draw(t, fmt.Sprintf("name-%d", idx))

		runtime := rapid.SampledFrom(dashboardRuntimes).Draw(t, fmt.Sprintf("runtime-%d", idx))
		status := rapid.SampledFrom(dashboardStatuses).Draw(t, fmt.Sprintf("status-%d", idx))

		// Generate 1-4 capabilities.
		numCaps := rapid.IntRange(1, 4).Draw(t, fmt.Sprintf("numCaps-%d", idx))
		caps := make([]domain.Capability, numCaps)
		for i := 0; i < numCaps; i++ {
			caps[i] = domain.Capability{
				Name: rapid.SampledFrom(dashboardCapNames).Draw(t, fmt.Sprintf("cap-%d-%d", idx, i)),
				Type: rapid.SampledFrom([]string{"mcp-tool", "a2a-message"}).Draw(t, fmt.Sprintf("capType-%d-%d", idx, i)),
			}
		}

		// Generate 0-3 labels.
		numLabels := rapid.IntRange(0, 3).Draw(t, fmt.Sprintf("numLabels-%d", idx))
		labels := make(map[string]string)
		for i := 0; i < numLabels; i++ {
			key := rapid.SampledFrom(dashboardLabelKeys).Draw(t, fmt.Sprintf("labelKey-%d-%d", idx, i))
			val := rapid.SampledFrom(dashboardLabelValues).Draw(t, fmt.Sprintf("labelVal-%d-%d", idx, i))
			labels[key] = val
		}

		return &domain.AgentEntry{
			ID:           fmt.Sprintf("agent-%d", idx),
			Name:         name,
			Namespace:    "default",
			Version:      "1.0.0",
			RuntimeType:  runtime,
			Capabilities: caps,
			Labels:       labels,
			Status:       status,
			CreatedAt:    time.Now().Add(-time.Duration(idx) * time.Hour),
			UpdatedAt:    time.Now(),
		}
	})
}

// filterParams holds the query parameters for a dashboard filter request.
type filterParams struct {
	Name       string
	Capability string
	Runtime    string
	Status     string
	Labels     map[string]string
}

// genFilterParams generates a random combination of filter parameters.
// Any filter field can be empty (meaning "don't filter on this criterion").
func genFilterParams() *rapid.Generator[filterParams] {
	return rapid.Custom(func(t *rapid.T) filterParams {
		var fp filterParams

		// Each filter is optionally applied.
		if rapid.Bool().Draw(t, "filterName") {
			fp.Name = rapid.SampledFrom([]string{
				"code-reviewer", "test-runner", "deployer", "monitor-bot",
				"doc-writer", "security-agent", "analyst", "chat-agent",
				"helper", "orchestrator", "planner", "executor",
				"nonexistent-agent", // to test empty results
			}).Draw(t, "name")
		}

		if rapid.Bool().Draw(t, "filterCapability") {
			fp.Capability = rapid.SampledFrom(append(dashboardCapNames, "nonexistent-cap")).Draw(t, "capability")
		}

		if rapid.Bool().Draw(t, "filterRuntime") {
			fp.Runtime = rapid.SampledFrom([]string{
				"claude", "kiro", "bedrock", "custom",
			}).Draw(t, "runtime")
		}

		if rapid.Bool().Draw(t, "filterStatus") {
			fp.Status = rapid.SampledFrom([]string{
				"active", "inactive", "draining", "deprecated", "unhealthy", "idle-safe",
			}).Draw(t, "status")
		}

		if rapid.Bool().Draw(t, "filterLabels") {
			numLabels := rapid.IntRange(1, 2).Draw(t, "numFilterLabels")
			fp.Labels = make(map[string]string)
			for i := 0; i < numLabels; i++ {
				key := rapid.SampledFrom(dashboardLabelKeys).Draw(t, fmt.Sprintf("fLabelKey-%d", i))
				val := rapid.SampledFrom(append(dashboardLabelValues, "nonexistent-val")).Draw(t, fmt.Sprintf("fLabelVal-%d", i))
				fp.Labels[key] = val
			}
		}

		return fp
	})
}

// buildFilterURL builds a query URL from filter params.
func buildFilterURL(fp filterParams) string {
	parts := []string{}
	if fp.Name != "" {
		parts = append(parts, "name="+fp.Name)
	}
	if fp.Capability != "" {
		parts = append(parts, "capability="+fp.Capability)
	}
	if fp.Runtime != "" {
		parts = append(parts, "runtime="+fp.Runtime)
	}
	if fp.Status != "" {
		parts = append(parts, "status="+fp.Status)
	}
	for k, v := range fp.Labels {
		parts = append(parts, "label."+k+"="+v)
	}

	url := "/api/v1/agents"
	if len(parts) > 0 {
		url += "?" + strings.Join(parts, "&")
	}
	return url
}

// setupDashboardGateway creates a gateway with a populated mock registry.
func setupDashboardGateway(agents []*domain.AgentEntry) *gateway.Gateway {
	auth := &fastAuthProvider{
		validAPIKey:     testAPIKey,
		validOAuthToken: testOAuthToken,
		identity: &domain.Identity{
			Subject: "dashboard-user",
			Team:    "platform",
			Scopes:  []string{"admin"},
			Role:    domain.RoleAdmin,
		},
	}

	reg := &dashboardMockRegistry{agents: agents}

	return gateway.NewGateway(
		gateway.GatewayConfig{Addr: ":0", SessionTimeout: 30 * time.Minute},
		auth,
		reg,
		&gwMockScheduler{},
		newGwMockPolicy(),
		&gwMockCost{},
		&gwMockSafety{},
		nil,
		map[string]gateway.HealthChecker{
			"store": &gwMockHealthChecker{},
		},
	)
}

// --- Test ---

func TestProperty56_DashboardFilterCorrectness(t *testing.T) {
	// For any set of agents and any filter combination, all returned agents
	// match all criteria and count ≤ 100.
	rapid.Check(t, func(t *rapid.T) {
		// Generate 5-150 random agents (can exceed 100 to test capping).
		numAgents := rapid.IntRange(5, 150).Draw(t, "numAgents")
		agents := make([]*domain.AgentEntry, numAgents)
		for i := 0; i < numAgents; i++ {
			agents[i] = genDashboardAgent(i).Draw(t, fmt.Sprintf("agent-%d", i))
		}

		gw := setupDashboardGateway(agents)
		fp := genFilterParams().Draw(t, "filter")
		url := buildFilterURL(fp)

		req := httptest.NewRequest(http.MethodGet, url, nil)
		req.Header.Set("X-API-Key", testAPIKey)
		rec := httptest.NewRecorder()
		gw.Router().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d for URL %s, body: %s", rec.Code, url, rec.Body.String())
		}

		// Parse response.
		var resp struct {
			Agents []json.RawMessage `json:"agents"`
			Count  int               `json:"count"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}

		// Property: count ≤ 100.
		if resp.Count > 100 {
			t.Fatalf("result count %d exceeds max 100", resp.Count)
		}
		if resp.Count != len(resp.Agents) {
			t.Fatalf("count field %d != actual agents length %d", resp.Count, len(resp.Agents))
		}

		// Parse each agent and verify all match filter criteria.
		for i, raw := range resp.Agents {
			var agent struct {
				ID           string            `json:"ID"`
				Name         string            `json:"Name"`
				RuntimeType  string            `json:"RuntimeType"`
				Status       string            `json:"Status"`
				Capabilities []struct {
					Name string `json:"Name"`
					Type string `json:"Type"`
				} `json:"Capabilities"`
				Labels map[string]string `json:"Labels"`
			}
			if err := json.Unmarshal(raw, &agent); err != nil {
				t.Fatalf("failed to parse agent[%d]: %v", i, err)
			}

			// Check name filter.
			if fp.Name != "" && agent.Name != fp.Name {
				t.Fatalf("agent[%d] name=%q does not match filter name=%q", i, agent.Name, fp.Name)
			}

			// Check status filter.
			if fp.Status != "" && agent.Status != fp.Status {
				t.Fatalf("agent[%d] status=%q does not match filter status=%q", i, agent.Status, fp.Status)
			}

			// Check runtime filter.
			if fp.Runtime != "" && agent.RuntimeType != fp.Runtime {
				t.Fatalf("agent[%d] runtime=%q does not match filter runtime=%q", i, agent.RuntimeType, fp.Runtime)
			}

			// Check capability filter.
			if fp.Capability != "" {
				found := false
				for _, cap := range agent.Capabilities {
					if cap.Name == fp.Capability {
						found = true
						break
					}
				}
				if !found {
					capNames := make([]string, len(agent.Capabilities))
					for j, c := range agent.Capabilities {
						capNames[j] = c.Name
					}
					t.Fatalf("agent[%d] capabilities=%v does not contain filter capability=%q",
						i, capNames, fp.Capability)
				}
			}

			// Check label filters.
			for k, v := range fp.Labels {
				if agent.Labels[k] != v {
					t.Fatalf("agent[%d] label[%s]=%q does not match filter label[%s]=%q",
						i, k, agent.Labels[k], k, v)
				}
			}
		}
	})
}

func TestProperty56_DashboardFilterCountCap(t *testing.T) {
	// Specifically test that even with all agents matching, the count never exceeds 100.
	rapid.Check(t, func(t *rapid.T) {
		// Generate 101-200 agents, all with the same name/status/runtime so they all match.
		numAgents := rapid.IntRange(101, 200).Draw(t, "numAgents")
		agents := make([]*domain.AgentEntry, numAgents)
		for i := 0; i < numAgents; i++ {
			agents[i] = &domain.AgentEntry{
				ID:          fmt.Sprintf("agent-%d", i),
				Name:        "all-match-agent",
				Namespace:   "default",
				Version:     "1.0.0",
				RuntimeType: domain.RuntimeClaude,
				Capabilities: []domain.Capability{
					{Name: "code-review", Type: "mcp-tool"},
				},
				Labels:    map[string]string{"team": "platform"},
				Status:    domain.AgentStatusActive,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}
		}

		gw := setupDashboardGateway(agents)

		// Filter that matches all agents.
		req := httptest.NewRequest(http.MethodGet, "/api/v1/agents?name=all-match-agent&status=active", nil)
		req.Header.Set("X-API-Key", testAPIKey)
		rec := httptest.NewRecorder()
		gw.Router().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}

		var resp struct {
			Count int `json:"count"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}

		if resp.Count > 100 {
			t.Fatalf("count %d exceeds dashboard max 100 (total matching agents: %d)", resp.Count, numAgents)
		}
	})
}
