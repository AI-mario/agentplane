// Package postgres implements the store.Store interface backed by PostgreSQL.
package postgres

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/store"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store implements store.Store using PostgreSQL via pgx.
type Store struct {
	pool     *pgxpool.Pool
	agents   *AgentStore
	missions *MissionStore
	policies *PolicyStore
	costs    *CostStore
	traces   *TraceStore
	budgets  *BudgetStore
	messages *MessageStore
}

// New creates a new PostgreSQL Store from a connection string.
func New(ctx context.Context, connStr string) (*Store, error) {
	config, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse config: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("postgres: connect: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}

	s := &Store{pool: pool}
	s.agents = &AgentStore{pool: pool}
	s.missions = &MissionStore{pool: pool}
	s.policies = &PolicyStore{pool: pool}
	s.costs = &CostStore{pool: pool}
	s.traces = &TraceStore{pool: pool}
	s.budgets = &BudgetStore{pool: pool}
	s.messages = &MessageStore{pool: pool}
	return s, nil
}

func (s *Store) Agents() store.AgentStore     { return s.agents }
func (s *Store) Missions() store.MissionStore { return s.missions }
func (s *Store) Policies() store.PolicyStore  { return s.policies }
func (s *Store) Costs() store.CostStore       { return s.costs }
func (s *Store) Traces() store.TraceStore     { return s.traces }
func (s *Store) Budgets() store.BudgetStore   { return s.budgets }
func (s *Store) Messages() store.MessageStore { return s.messages }

func (s *Store) Close() error {
	s.pool.Close()
	return nil
}

// Migrate acquires an advisory lock, applies pending migrations sequentially, then releases.
func (s *Store) Migrate(ctx context.Context) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("postgres: acquire conn for migration: %w", err)
	}
	defer conn.Release()

	// Advisory lock key derived from a fixed string.
	lockKey := advisoryLockKey("agentplane_migrations")

	// Acquire advisory lock (blocks until available).
	_, err = conn.Exec(ctx, "SELECT pg_advisory_lock($1)", lockKey)
	if err != nil {
		return fmt.Errorf("postgres: acquire migration lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", lockKey)
	}()

	// Ensure schema_migrations table exists.
	_, err = conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		return fmt.Errorf("postgres: create schema_migrations: %w", err)
	}

	// Determine current version.
	var currentVersion int
	err = conn.QueryRow(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&currentVersion)
	if err != nil {
		return fmt.Errorf("postgres: get current version: %w", err)
	}

	// Read and sort migration files.
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("postgres: read migrations dir: %w", err)
	}

	type migration struct {
		version int
		name    string
	}
	var migrations []migration
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		var ver int
		if _, err := fmt.Sscanf(e.Name(), "%d_", &ver); err == nil {
			migrations = append(migrations, migration{version: ver, name: e.Name()})
		}
	}
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})

	// Apply pending migrations.
	for _, m := range migrations {
		if m.version <= currentVersion {
			continue
		}
		sql, err := migrationsFS.ReadFile("migrations/" + m.name)
		if err != nil {
			return fmt.Errorf("postgres: read migration %s: %w", m.name, err)
		}

		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("postgres: begin tx for migration %d: %w", m.version, err)
		}

		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("postgres: apply migration %d (%s): %w", m.version, m.name, err)
		}

		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", m.version); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("postgres: record migration %d: %w", m.version, err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("postgres: commit migration %d: %w", m.version, err)
		}
	}

	return nil
}

// advisoryLockKey produces a stable int64 hash for pg_advisory_lock.
func advisoryLockKey(name string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	return int64(h.Sum64())
}

// --- AgentStore ---

type AgentStore struct {
	pool *pgxpool.Pool
}

func (s *AgentStore) Create(ctx context.Context, agent *domain.AgentEntry) error {
	capJSON, _ := json.Marshal(agent.Capabilities)
	labelsJSON, _ := json.Marshal(agent.Labels)
	sloJSON, _ := json.Marshal(agent.SLOs)
	resJSON, _ := json.Marshal(agent.Resources)
	deplJSON, _ := json.Marshal(agent.Deployment)

	_, err := s.pool.Exec(ctx, `
		INSERT INTO agents (id, name, namespace, version, runtime_type, status, labels_json, capabilities_json, slo_json, resources_json, deployment_json, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`, agent.ID, agent.Name, agent.Namespace, agent.Version,
		string(agent.RuntimeType), string(agent.Status),
		capJSON, labelsJSON, sloJSON, resJSON, deplJSON,
		agent.CreatedAt, agent.UpdatedAt)
	if err != nil {
		return fmt.Errorf("postgres: create agent: %w", err)
	}
	return nil
}

func (s *AgentStore) Get(ctx context.Context, id string) (*domain.AgentEntry, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, name, namespace, version, runtime_type, status, labels_json, capabilities_json, slo_json, resources_json, deployment_json, created_at, updated_at
		FROM agents WHERE id = $1
	`, id)
	return scanAgent(row)
}

func (s *AgentStore) GetByName(ctx context.Context, name, namespace string) (*domain.AgentEntry, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, name, namespace, version, runtime_type, status, labels_json, capabilities_json, slo_json, resources_json, deployment_json, created_at, updated_at
		FROM agents WHERE name = $1 AND namespace = $2
	`, name, namespace)
	return scanAgent(row)
}

func (s *AgentStore) List(ctx context.Context, filter domain.AgentFilter) ([]*domain.AgentEntry, error) {
	query := "SELECT id, name, namespace, version, runtime_type, status, labels_json, capabilities_json, slo_json, resources_json, deployment_json, created_at, updated_at FROM agents WHERE 1=1"
	args := []interface{}{}
	argIdx := 1

	if filter.Name != "" {
		query += fmt.Sprintf(" AND name = $%d", argIdx)
		args = append(args, filter.Name)
		argIdx++
	}
	if filter.Status != "" {
		query += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, string(filter.Status))
		argIdx++
	}
	if filter.Runtime != "" {
		query += fmt.Sprintf(" AND runtime_type = $%d", argIdx)
		args = append(args, string(filter.Runtime))
		argIdx++
	}
	if filter.Capability != "" {
		query += fmt.Sprintf(" AND capabilities_json @> $%d::jsonb", argIdx)
		capMatch, _ := json.Marshal([]domain.Capability{{Name: filter.Capability}})
		args = append(args, string(capMatch))
		argIdx++
	}
	if len(filter.Label) > 0 {
		for k, v := range filter.Label {
			query += fmt.Sprintf(" AND labels_json->>$%d = $%d", argIdx, argIdx+1)
			args = append(args, k, v)
			argIdx += 2
		}
	}

	query += " ORDER BY created_at DESC"

	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, filter.Limit)
		argIdx++
	}
	if filter.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argIdx)
		args = append(args, filter.Offset)
		argIdx++
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: list agents: %w", err)
	}
	defer rows.Close()

	var agents []*domain.AgentEntry
	for rows.Next() {
		a, err := scanAgentRows(rows)
		if err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

func (s *AgentStore) Update(ctx context.Context, agent *domain.AgentEntry) error {
	capJSON, _ := json.Marshal(agent.Capabilities)
	labelsJSON, _ := json.Marshal(agent.Labels)
	sloJSON, _ := json.Marshal(agent.SLOs)
	resJSON, _ := json.Marshal(agent.Resources)
	deplJSON, _ := json.Marshal(agent.Deployment)

	_, err := s.pool.Exec(ctx, `
		UPDATE agents SET name=$2, namespace=$3, version=$4, runtime_type=$5, status=$6,
			labels_json=$7, capabilities_json=$8, slo_json=$9, resources_json=$10, deployment_json=$11, updated_at=$12
		WHERE id=$1
	`, agent.ID, agent.Name, agent.Namespace, agent.Version,
		string(agent.RuntimeType), string(agent.Status),
		capJSON, labelsJSON, sloJSON, resJSON, deplJSON, agent.UpdatedAt)
	if err != nil {
		return fmt.Errorf("postgres: update agent: %w", err)
	}
	return nil
}

func (s *AgentStore) Delete(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM agents WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("postgres: delete agent: %w", err)
	}
	return nil
}

func (s *AgentStore) QueryByCapability(ctx context.Context, capability string) ([]*domain.AgentEntry, error) {
	// Use JSONB containment to find agents with matching capability name.
	capMatch, _ := json.Marshal([]map[string]string{{"Name": capability}})
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, namespace, version, runtime_type, status, labels_json, capabilities_json, slo_json, resources_json, deployment_json, created_at, updated_at
		FROM agents WHERE capabilities_json @> $1::jsonb
	`, string(capMatch))
	if err != nil {
		return nil, fmt.Errorf("postgres: query by capability: %w", err)
	}
	defer rows.Close()

	var agents []*domain.AgentEntry
	for rows.Next() {
		a, err := scanAgentRows(rows)
		if err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

func (s *AgentStore) AddVersion(ctx context.Context, version *domain.AgentVersion) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO agent_versions (id, agent_id, version, registered_at)
		VALUES ($1, $2, $3, $4)
	`, version.ID, version.AgentID, version.Version, version.RegisteredAt)
	if err != nil {
		return fmt.Errorf("postgres: add version: %w", err)
	}
	return nil
}

func (s *AgentStore) ListVersions(ctx context.Context, agentID string) ([]*domain.AgentVersion, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, agent_id, version, registered_at
		FROM agent_versions WHERE agent_id = $1
		ORDER BY registered_at DESC
	`, agentID)
	if err != nil {
		return nil, fmt.Errorf("postgres: list versions: %w", err)
	}
	defer rows.Close()

	var versions []*domain.AgentVersion
	for rows.Next() {
		v := &domain.AgentVersion{}
		if err := rows.Scan(&v.ID, &v.AgentID, &v.Version, &v.RegisteredAt); err != nil {
			return nil, fmt.Errorf("postgres: scan version: %w", err)
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

func (s *AgentStore) DeleteVersion(ctx context.Context, versionID string) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM agent_versions WHERE id = $1", versionID)
	if err != nil {
		return fmt.Errorf("postgres: delete version: %w", err)
	}
	return nil
}

// --- MissionStore ---

type MissionStore struct {
	pool *pgxpool.Pool
}

func (s *MissionStore) Create(ctx context.Context, mission *domain.Mission) error {
	capsJSON, _ := json.Marshal(mission.RequiredCapabilities)
	var rationaleJSON []byte
	if mission.AssignmentRationale != nil {
		rationaleJSON, _ = json.Marshal(mission.AssignmentRationale)
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO missions (id, agent_id, team_id, project_id, status, required_capabilities_json, payload, assignment_rationale_json, priority, timeout_ms, submitted_at, assigned_at, completed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`, mission.ID, mission.AgentID, mission.TeamID, mission.ProjectID,
		string(mission.Status), capsJSON, mission.Payload, rationaleJSON,
		mission.Priority, mission.Timeout.Milliseconds(),
		mission.SubmittedAt, mission.AssignedAt, mission.CompletedAt)
	if err != nil {
		return fmt.Errorf("postgres: create mission: %w", err)
	}
	return nil
}

func (s *MissionStore) Get(ctx context.Context, id string) (*domain.Mission, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, agent_id, team_id, project_id, status, required_capabilities_json, payload, assignment_rationale_json, priority, timeout_ms, submitted_at, assigned_at, completed_at
		FROM missions WHERE id = $1
	`, id)
	return scanMission(row)
}

func (s *MissionStore) List(ctx context.Context, filter domain.MissionFilter) ([]*domain.Mission, error) {
	query := "SELECT id, agent_id, team_id, project_id, status, required_capabilities_json, payload, assignment_rationale_json, priority, timeout_ms, submitted_at, assigned_at, completed_at FROM missions WHERE 1=1"
	args := []interface{}{}
	argIdx := 1

	if filter.AgentID != "" {
		query += fmt.Sprintf(" AND agent_id = $%d", argIdx)
		args = append(args, filter.AgentID)
		argIdx++
	}
	if filter.TeamID != "" {
		query += fmt.Sprintf(" AND team_id = $%d", argIdx)
		args = append(args, filter.TeamID)
		argIdx++
	}
	if filter.Status != "" {
		query += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, string(filter.Status))
		argIdx++
	}

	query += " ORDER BY submitted_at DESC"

	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, filter.Limit)
		argIdx++
	}
	if filter.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argIdx)
		args = append(args, filter.Offset)
		argIdx++
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: list missions: %w", err)
	}
	defer rows.Close()

	var missions []*domain.Mission
	for rows.Next() {
		m, err := scanMissionRows(rows)
		if err != nil {
			return nil, err
		}
		missions = append(missions, m)
	}
	return missions, rows.Err()
}

func (s *MissionStore) Update(ctx context.Context, mission *domain.Mission) error {
	capsJSON, _ := json.Marshal(mission.RequiredCapabilities)
	var rationaleJSON []byte
	if mission.AssignmentRationale != nil {
		rationaleJSON, _ = json.Marshal(mission.AssignmentRationale)
	}

	_, err := s.pool.Exec(ctx, `
		UPDATE missions SET agent_id=$2, team_id=$3, project_id=$4, status=$5,
			required_capabilities_json=$6, payload=$7, assignment_rationale_json=$8,
			priority=$9, timeout_ms=$10, submitted_at=$11, assigned_at=$12, completed_at=$13
		WHERE id=$1
	`, mission.ID, mission.AgentID, mission.TeamID, mission.ProjectID,
		string(mission.Status), capsJSON, mission.Payload, rationaleJSON,
		mission.Priority, mission.Timeout.Milliseconds(),
		mission.SubmittedAt, mission.AssignedAt, mission.CompletedAt)
	if err != nil {
		return fmt.Errorf("postgres: update mission: %w", err)
	}
	return nil
}

func (s *MissionStore) GetPending(ctx context.Context) ([]*domain.Mission, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, agent_id, team_id, project_id, status, required_capabilities_json, payload, assignment_rationale_json, priority, timeout_ms, submitted_at, assigned_at, completed_at
		FROM missions WHERE status IN ('pending', 'queued')
		ORDER BY priority DESC, submitted_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("postgres: get pending missions: %w", err)
	}
	defer rows.Close()

	var missions []*domain.Mission
	for rows.Next() {
		m, err := scanMissionRows(rows)
		if err != nil {
			return nil, err
		}
		missions = append(missions, m)
	}
	return missions, rows.Err()
}

// --- PolicyStore ---

type PolicyStore struct {
	pool *pgxpool.Pool
}

func (s *PolicyStore) Create(ctx context.Context, policy *domain.Policy) error {
	rulesJSON, _ := json.Marshal(policy.Rules)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO policies (id, name, scope, team_id, rules_json, version, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, policy.ID, policy.Name, string(policy.Scope), policy.TeamID,
		rulesJSON, policy.Version, policy.UpdatedAt)
	if err != nil {
		return fmt.Errorf("postgres: create policy: %w", err)
	}
	return nil
}

func (s *PolicyStore) Get(ctx context.Context, id string) (*domain.Policy, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, name, scope, team_id, rules_json, version, updated_at
		FROM policies WHERE id = $1
	`, id)
	return scanPolicy(row)
}

func (s *PolicyStore) List(ctx context.Context, filter domain.PolicyFilter) ([]*domain.Policy, error) {
	query := "SELECT id, name, scope, team_id, rules_json, version, updated_at FROM policies WHERE 1=1"
	args := []interface{}{}
	argIdx := 1

	if filter.Scope != "" {
		query += fmt.Sprintf(" AND scope = $%d", argIdx)
		args = append(args, string(filter.Scope))
		argIdx++
	}
	if filter.TeamID != "" {
		query += fmt.Sprintf(" AND team_id = $%d", argIdx)
		args = append(args, filter.TeamID)
		argIdx++
	}

	query += " ORDER BY updated_at DESC"

	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, filter.Limit)
		argIdx++
	}
	if filter.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argIdx)
		args = append(args, filter.Offset)
		argIdx++
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: list policies: %w", err)
	}
	defer rows.Close()

	var policies []*domain.Policy
	for rows.Next() {
		p, err := scanPolicyRows(rows)
		if err != nil {
			return nil, err
		}
		policies = append(policies, p)
	}
	return policies, rows.Err()
}

func (s *PolicyStore) Update(ctx context.Context, policy *domain.Policy) error {
	rulesJSON, _ := json.Marshal(policy.Rules)
	_, err := s.pool.Exec(ctx, `
		UPDATE policies SET name=$2, scope=$3, team_id=$4, rules_json=$5, version=$6, updated_at=$7
		WHERE id=$1
	`, policy.ID, policy.Name, string(policy.Scope), policy.TeamID,
		rulesJSON, policy.Version, policy.UpdatedAt)
	if err != nil {
		return fmt.Errorf("postgres: update policy: %w", err)
	}
	return nil
}

func (s *PolicyStore) Delete(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM policies WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("postgres: delete policy: %w", err)
	}
	return nil
}

func (s *PolicyStore) GetEffective(ctx context.Context, scope domain.PolicyScope, scopeID string) ([]*domain.Policy, error) {
	// Get policies for the given scope. For team scope, also include org-level policies.
	var rows pgx.Rows
	var err error

	if scope == domain.PolicyScopeTeam {
		rows, err = s.pool.Query(ctx, `
			SELECT id, name, scope, team_id, rules_json, version, updated_at
			FROM policies WHERE (scope = 'organization') OR (scope = 'team' AND team_id = $1)
			ORDER BY scope ASC, updated_at DESC
		`, scopeID)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT id, name, scope, team_id, rules_json, version, updated_at
			FROM policies WHERE scope = 'organization'
			ORDER BY updated_at DESC
		`)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: get effective policies: %w", err)
	}
	defer rows.Close()

	var policies []*domain.Policy
	for rows.Next() {
		p, err := scanPolicyRows(rows)
		if err != nil {
			return nil, err
		}
		policies = append(policies, p)
	}
	return policies, rows.Err()
}

// --- CostStore ---

type CostStore struct {
	pool *pgxpool.Pool
}

func (s *CostStore) Record(ctx context.Context, record *domain.CostRecord) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO cost_events (id, mission_id, agent_id, team_id, project_id, token_count, compute_time_ms, tool_invocations, total_cost, recorded_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, record.ID, record.MissionID, record.AgentID, record.TeamID, record.ProjectID,
		record.TokenCount, record.ComputeTimeMs, record.ToolInvocations, record.TotalCost, record.RecordedAt)
	if err != nil {
		return fmt.Errorf("postgres: record cost: %w", err)
	}
	return nil
}

func (s *CostStore) Query(ctx context.Context, filter domain.CostFilter) ([]*domain.CostRecord, int, error) {
	query := "SELECT id, mission_id, agent_id, team_id, project_id, token_count, compute_time_ms, tool_invocations, total_cost, recorded_at FROM cost_events WHERE 1=1"
	countQuery := "SELECT COUNT(*) FROM cost_events WHERE 1=1"
	args := []interface{}{}
	countArgs := []interface{}{}
	argIdx := 1
	countIdx := 1

	addFilter := func(clause string, val interface{}) {
		query += fmt.Sprintf(" AND "+clause, argIdx)
		args = append(args, val)
		argIdx++
		countQuery += fmt.Sprintf(" AND "+clause, countIdx)
		countArgs = append(countArgs, val)
		countIdx++
	}

	if filter.TeamID != "" {
		addFilter("team_id = $%d", filter.TeamID)
	}
	if filter.AgentID != "" {
		addFilter("agent_id = $%d", filter.AgentID)
	}
	if filter.ProjectID != "" {
		addFilter("project_id = $%d", filter.ProjectID)
	}
	if !filter.StartTime.IsZero() {
		addFilter("recorded_at >= $%d", filter.StartTime)
	}
	if !filter.EndTime.IsZero() {
		addFilter("recorded_at <= $%d", filter.EndTime)
	}

	// Get total count.
	var total int
	err := s.pool.QueryRow(ctx, countQuery, countArgs...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("postgres: count costs: %w", err)
	}

	query += " ORDER BY recorded_at DESC"
	limit := filter.Limit
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	query += fmt.Sprintf(" LIMIT $%d", argIdx)
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("postgres: query costs: %w", err)
	}
	defer rows.Close()

	var records []*domain.CostRecord
	for rows.Next() {
		r := &domain.CostRecord{}
		if err := rows.Scan(&r.ID, &r.MissionID, &r.AgentID, &r.TeamID, &r.ProjectID,
			&r.TokenCount, &r.ComputeTimeMs, &r.ToolInvocations, &r.TotalCost, &r.RecordedAt); err != nil {
			return nil, 0, fmt.Errorf("postgres: scan cost: %w", err)
		}
		records = append(records, r)
	}
	return records, total, rows.Err()
}

func (s *CostStore) Aggregate(ctx context.Context, filter domain.CostFilter) (*domain.CostAggregation, error) {
	query := `SELECT COALESCE(SUM(total_cost), 0), COALESCE(SUM(token_count), 0), COALESCE(SUM(compute_time_ms), 0), COALESCE(SUM(tool_invocations), 0), COUNT(*) FROM cost_events WHERE 1=1`
	args := []interface{}{}
	argIdx := 1

	if filter.TeamID != "" {
		query += fmt.Sprintf(" AND team_id = $%d", argIdx)
		args = append(args, filter.TeamID)
		argIdx++
	}
	if filter.AgentID != "" {
		query += fmt.Sprintf(" AND agent_id = $%d", argIdx)
		args = append(args, filter.AgentID)
		argIdx++
	}
	if filter.ProjectID != "" {
		query += fmt.Sprintf(" AND project_id = $%d", argIdx)
		args = append(args, filter.ProjectID)
		argIdx++
	}
	if !filter.StartTime.IsZero() {
		query += fmt.Sprintf(" AND recorded_at >= $%d", argIdx)
		args = append(args, filter.StartTime)
		argIdx++
	}
	if !filter.EndTime.IsZero() {
		query += fmt.Sprintf(" AND recorded_at <= $%d", argIdx)
		args = append(args, filter.EndTime)
		argIdx++
	}

	agg := &domain.CostAggregation{}
	err := s.pool.QueryRow(ctx, query, args...).Scan(
		&agg.TotalCost, &agg.TotalTokens, &agg.TotalComputeMs, &agg.TotalToolCalls, &agg.RecordCount)
	if err != nil {
		return nil, fmt.Errorf("postgres: aggregate costs: %w", err)
	}
	return agg, nil
}

// --- TraceStore ---

type TraceStore struct {
	pool *pgxpool.Pool
}

func (s *TraceStore) Create(ctx context.Context, trace *domain.MissionTrace) error {
	var durationMs int64
	if trace.Duration > 0 {
		durationMs = trace.Duration.Milliseconds()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mission_traces (trace_id, mission_id, agent_id, goal, outcome, duration_ms, start_time, end_time, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, trace.TraceID, trace.MissionID, trace.AgentID, trace.Goal,
		string(trace.Outcome), durationMs, trace.StartTime, trace.EndTime, trace.ExpiresAt)
	if err != nil {
		return fmt.Errorf("postgres: create trace: %w", err)
	}
	return nil
}

func (s *TraceStore) Update(ctx context.Context, trace *domain.MissionTrace) error {
	var durationMs int64
	if trace.Duration > 0 {
		durationMs = trace.Duration.Milliseconds()
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE mission_traces SET outcome=$2, duration_ms=$3, end_time=$4, expires_at=$5 WHERE trace_id=$1
	`, trace.TraceID, string(trace.Outcome), durationMs, trace.EndTime, trace.ExpiresAt)
	if err != nil {
		return fmt.Errorf("postgres: update trace: %w", err)
	}
	return nil
}

func (s *TraceStore) AddSpan(ctx context.Context, span *domain.TraceSpan) error {
	attrsJSON, _ := json.Marshal(span.Attributes)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO trace_steps (span_id, trace_id, parent_span_id, name, status, duration_ms, attributes_json, start_time)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, span.SpanID, span.TraceID, span.ParentSpanID, span.Name,
		string(span.Status), span.DurationMs, attrsJSON, span.StartTime)
	if err != nil {
		return fmt.Errorf("postgres: add span: %w", err)
	}
	return nil
}

func (s *TraceStore) Get(ctx context.Context, id string) (*domain.MissionTrace, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT trace_id, mission_id, agent_id, goal, outcome, duration_ms, start_time, end_time, expires_at
		FROM mission_traces WHERE trace_id = $1
	`, id)

	t := &domain.MissionTrace{}
	var durationMs int64
	var outcome string
	if err := row.Scan(&t.TraceID, &t.MissionID, &t.AgentID, &t.Goal, &outcome,
		&durationMs, &t.StartTime, &t.EndTime, &t.ExpiresAt); err != nil {
		return nil, fmt.Errorf("postgres: get trace: %w", err)
	}
	t.Outcome = domain.MissionOutcome(outcome)
	t.Duration = time.Duration(durationMs) * time.Millisecond

	// Load steps.
	rows, err := s.pool.Query(ctx, `
		SELECT span_id, trace_id, parent_span_id, name, status, duration_ms, attributes_json, start_time
		FROM trace_steps WHERE trace_id = $1 ORDER BY start_time ASC
	`, id)
	if err != nil {
		return nil, fmt.Errorf("postgres: get trace steps: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		step := domain.TraceStep{}
		var stepDurMs int64
		var status string
		var attrsJSON []byte
		if err := rows.Scan(&step.SpanID, &step.TraceID, &step.ParentSpanID, &step.Name,
			&status, &stepDurMs, &attrsJSON, &step.StartTime); err != nil {
			return nil, fmt.Errorf("postgres: scan step: %w", err)
		}
		step.Status = domain.StepStatus(status)
		step.Duration = time.Duration(stepDurMs) * time.Millisecond
		if len(attrsJSON) > 0 {
			_ = json.Unmarshal(attrsJSON, &step.Attributes)
		}
		t.Steps = append(t.Steps, step)
	}
	return t, rows.Err()
}

func (s *TraceStore) Query(ctx context.Context, filter domain.TraceFilter) ([]*domain.MissionTrace, error) {
	query := "SELECT trace_id, mission_id, agent_id, goal, outcome, duration_ms, start_time, end_time, expires_at FROM mission_traces WHERE 1=1"
	args := []interface{}{}
	argIdx := 1

	if filter.MissionID != "" {
		query += fmt.Sprintf(" AND mission_id = $%d", argIdx)
		args = append(args, filter.MissionID)
		argIdx++
	}
	if filter.AgentID != "" {
		query += fmt.Sprintf(" AND agent_id = $%d", argIdx)
		args = append(args, filter.AgentID)
		argIdx++
	}
	if filter.Outcome != "" {
		query += fmt.Sprintf(" AND outcome = $%d", argIdx)
		args = append(args, string(filter.Outcome))
		argIdx++
	}
	if !filter.StartTime.IsZero() {
		query += fmt.Sprintf(" AND start_time >= $%d", argIdx)
		args = append(args, filter.StartTime)
		argIdx++
	}
	if !filter.EndTime.IsZero() {
		query += fmt.Sprintf(" AND start_time <= $%d", argIdx)
		args = append(args, filter.EndTime)
		argIdx++
	}

	query += " ORDER BY start_time DESC"
	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, filter.Limit)
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: query traces: %w", err)
	}
	defer rows.Close()

	var traces []*domain.MissionTrace
	for rows.Next() {
		t := &domain.MissionTrace{}
		var durationMs int64
		var outcome string
		if err := rows.Scan(&t.TraceID, &t.MissionID, &t.AgentID, &t.Goal, &outcome,
			&durationMs, &t.StartTime, &t.EndTime, &t.ExpiresAt); err != nil {
			return nil, fmt.Errorf("postgres: scan trace: %w", err)
		}
		t.Outcome = domain.MissionOutcome(outcome)
		t.Duration = time.Duration(durationMs) * time.Millisecond
		traces = append(traces, t)
	}
	return traces, rows.Err()
}

func (s *TraceStore) DeleteExpired(ctx context.Context) (int, error) {
	tag, err := s.pool.Exec(ctx, "DELETE FROM mission_traces WHERE expires_at <= NOW()")
	if err != nil {
		return 0, fmt.Errorf("postgres: delete expired traces: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// --- BudgetStore ---

type BudgetStore struct {
	pool *pgxpool.Pool
}

func (s *BudgetStore) ListAll(ctx context.Context) ([]*domain.Budget, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, team_id, cap_amount, period_type, period_start, accumulated_cost, is_blocked FROM budget_caps`)
	if err != nil {
		return nil, fmt.Errorf("postgres: list budgets: %w", err)
	}
	defer rows.Close()

	var budgets []*domain.Budget
	for rows.Next() {
		b := &domain.Budget{}
		if err := rows.Scan(&b.ID, &b.TeamID, &b.CapAmount, &b.PeriodType, &b.PeriodStart, &b.AccumulatedCost, &b.IsBlocked); err != nil {
			return nil, fmt.Errorf("postgres: scan budget: %w", err)
		}
		budgets = append(budgets, b)
	}
	return budgets, rows.Err()
}

func (s *BudgetStore) Get(ctx context.Context, teamID string) (*domain.Budget, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, team_id, cap_amount, period_type, period_start, accumulated_cost, is_blocked
		FROM budget_caps WHERE team_id = $1
	`, teamID)

	b := &domain.Budget{}
	if err := row.Scan(&b.ID, &b.TeamID, &b.CapAmount, &b.PeriodType, &b.PeriodStart, &b.AccumulatedCost, &b.IsBlocked); err != nil {
		return nil, fmt.Errorf("postgres: get budget: %w", err)
	}
	return b, nil
}

func (s *BudgetStore) Set(ctx context.Context, budget *domain.Budget) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO budget_caps (id, team_id, cap_amount, period_type, period_start, accumulated_cost, is_blocked)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (team_id) DO UPDATE SET
			cap_amount = EXCLUDED.cap_amount,
			period_type = EXCLUDED.period_type,
			period_start = EXCLUDED.period_start,
			accumulated_cost = EXCLUDED.accumulated_cost,
			is_blocked = EXCLUDED.is_blocked
	`, budget.ID, budget.TeamID, budget.CapAmount, budget.PeriodType,
		budget.PeriodStart, budget.AccumulatedCost, budget.IsBlocked)
	if err != nil {
		return fmt.Errorf("postgres: set budget: %w", err)
	}
	return nil
}

func (s *BudgetStore) IncrementAccumulated(ctx context.Context, teamID string, amount float64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE budget_caps SET accumulated_cost = accumulated_cost + $2
		WHERE team_id = $1
	`, teamID, amount)
	if err != nil {
		return fmt.Errorf("postgres: increment budget: %w", err)
	}
	return nil
}

func (s *BudgetStore) ResetPeriod(ctx context.Context, teamID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE budget_caps SET accumulated_cost = 0, is_blocked = FALSE, period_start = NOW()
		WHERE team_id = $1
	`, teamID)
	if err != nil {
		return fmt.Errorf("postgres: reset period: %w", err)
	}
	return nil
}

// --- MessageStore ---

type MessageStore struct {
	pool *pgxpool.Pool
}

func (s *MessageStore) Create(ctx context.Context, msg *domain.AgentMessage) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO messages (id, from_agent, to_agent, message_type, payload, retry_count, status, sent_at, delivered_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, msg.ID, msg.From, msg.To, string(msg.Type), msg.Payload,
		msg.RetryCount, string(msg.Status), msg.SentAt, msg.DeliveredAt)
	if err != nil {
		return fmt.Errorf("postgres: create message: %w", err)
	}
	return nil
}

func (s *MessageStore) List(ctx context.Context, limit int) ([]*domain.AgentMessage, error) {
	query := `SELECT id, from_agent, to_agent, message_type, payload, retry_count, status, sent_at, delivered_at FROM messages ORDER BY sent_at DESC`
	args := []interface{}{}
	if limit > 0 {
		query += " LIMIT $1"
		args = append(args, limit)
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: list messages: %w", err)
	}
	defer rows.Close()

	var msgs []*domain.AgentMessage
	for rows.Next() {
		msg := &domain.AgentMessage{}
		var msgType, status string
		if err := rows.Scan(&msg.ID, &msg.From, &msg.To, &msgType, &msg.Payload,
			&msg.RetryCount, &status, &msg.SentAt, &msg.DeliveredAt); err != nil {
			return nil, fmt.Errorf("postgres: scan message: %w", err)
		}
		msg.Type = domain.MessageType(msgType)
		msg.Status = domain.MessageStatus(status)
		msgs = append(msgs, msg)
	}
	return msgs, rows.Err()
}

func (s *MessageStore) Get(ctx context.Context, id string) (*domain.AgentMessage, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, from_agent, to_agent, message_type, payload, retry_count, status, sent_at, delivered_at
		FROM messages WHERE id = $1
	`, id)

	msg := &domain.AgentMessage{}
	var msgType, status string
	if err := row.Scan(&msg.ID, &msg.From, &msg.To, &msgType, &msg.Payload,
		&msg.RetryCount, &status, &msg.SentAt, &msg.DeliveredAt); err != nil {
		return nil, fmt.Errorf("postgres: get message: %w", err)
	}
	msg.Type = domain.MessageType(msgType)
	msg.Status = domain.MessageStatus(status)
	return msg, nil
}

func (s *MessageStore) GetDLQ(ctx context.Context, filter domain.DLQFilter) ([]*domain.DeadLetter, error) {
	query := `
		SELECT d.id, d.message_id, d.failure_reason, d.retry_attempts, d.failed_at,
			m.id, m.from_agent, m.to_agent, m.message_type, m.payload, m.retry_count, m.status, m.sent_at, m.delivered_at
		FROM dead_letters d JOIN messages m ON d.message_id = m.id WHERE 1=1`
	args := []interface{}{}
	argIdx := 1

	if filter.From != "" {
		query += fmt.Sprintf(" AND m.from_agent = $%d", argIdx)
		args = append(args, filter.From)
		argIdx++
	}
	if filter.To != "" {
		query += fmt.Sprintf(" AND m.to_agent = $%d", argIdx)
		args = append(args, filter.To)
		argIdx++
	}

	query += " ORDER BY d.failed_at DESC"
	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, filter.Limit)
		argIdx++
	}
	if filter.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argIdx)
		args = append(args, filter.Offset)
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: get dlq: %w", err)
	}
	defer rows.Close()

	var letters []*domain.DeadLetter
	for rows.Next() {
		dl := &domain.DeadLetter{OriginalMessage: &domain.AgentMessage{}}
		var msgType, status string
		if err := rows.Scan(
			&dl.ID, &dl.MessageID, &dl.FailureReason, &dl.RetryAttempts, &dl.FailedAt,
			&dl.OriginalMessage.ID, &dl.OriginalMessage.From, &dl.OriginalMessage.To,
			&msgType, &dl.OriginalMessage.Payload, &dl.OriginalMessage.RetryCount,
			&status, &dl.OriginalMessage.SentAt, &dl.OriginalMessage.DeliveredAt,
		); err != nil {
			return nil, fmt.Errorf("postgres: scan dlq: %w", err)
		}
		dl.OriginalMessage.Type = domain.MessageType(msgType)
		dl.OriginalMessage.Status = domain.MessageStatus(status)
		letters = append(letters, dl)
	}
	return letters, rows.Err()
}

func (s *MessageStore) MoveToDLQ(ctx context.Context, msgID string, reason string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx for dlq: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Update message status.
	_, err = tx.Exec(ctx, "UPDATE messages SET status = $2 WHERE id = $1", msgID, string(domain.MessageStatusDLQ))
	if err != nil {
		return fmt.Errorf("postgres: update msg status for dlq: %w", err)
	}

	// Get retry count for the dead letter entry.
	var retryCount int
	err = tx.QueryRow(ctx, "SELECT retry_count FROM messages WHERE id = $1", msgID).Scan(&retryCount)
	if err != nil {
		return fmt.Errorf("postgres: get retry count: %w", err)
	}

	// Insert dead letter.
	dlID := msgID + "-dl"
	_, err = tx.Exec(ctx, `
		INSERT INTO dead_letters (id, message_id, failure_reason, retry_attempts, failed_at)
		VALUES ($1, $2, $3, $4, NOW())
	`, dlID, msgID, reason, retryCount)
	if err != nil {
		return fmt.Errorf("postgres: insert dead letter: %w", err)
	}

	return tx.Commit(ctx)
}

func (s *MessageStore) UpdateStatus(ctx context.Context, id string, status domain.MessageStatus) error {
	_, err := s.pool.Exec(ctx, "UPDATE messages SET status = $2 WHERE id = $1", id, string(status))
	if err != nil {
		return fmt.Errorf("postgres: update message status: %w", err)
	}
	return nil
}

// --- Scan helpers ---

type scannable interface {
	Scan(dest ...interface{}) error
}

func scanAgent(row scannable) (*domain.AgentEntry, error) {
	a := &domain.AgentEntry{}
	var runtimeType, status string
	var labelsJSON, capsJSON, sloJSON, resJSON, deplJSON []byte

	if err := row.Scan(&a.ID, &a.Name, &a.Namespace, &a.Version, &runtimeType, &status,
		&labelsJSON, &capsJSON, &sloJSON, &resJSON, &deplJSON, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, fmt.Errorf("postgres: scan agent: %w", err)
	}

	a.RuntimeType = domain.RuntimeType(runtimeType)
	a.Status = domain.AgentStatus(status)

	if len(labelsJSON) > 0 {
		_ = json.Unmarshal(labelsJSON, &a.Labels)
	}
	if len(capsJSON) > 0 {
		_ = json.Unmarshal(capsJSON, &a.Capabilities)
	}
	if len(sloJSON) > 0 {
		_ = json.Unmarshal(sloJSON, &a.SLOs)
	}
	if len(resJSON) > 0 {
		_ = json.Unmarshal(resJSON, &a.Resources)
	}
	if len(deplJSON) > 0 {
		_ = json.Unmarshal(deplJSON, &a.Deployment)
	}

	return a, nil
}

func scanAgentRows(rows pgx.Rows) (*domain.AgentEntry, error) {
	a := &domain.AgentEntry{}
	var runtimeType, status string
	var labelsJSON, capsJSON, sloJSON, resJSON, deplJSON []byte

	if err := rows.Scan(&a.ID, &a.Name, &a.Namespace, &a.Version, &runtimeType, &status,
		&labelsJSON, &capsJSON, &sloJSON, &resJSON, &deplJSON, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, fmt.Errorf("postgres: scan agent row: %w", err)
	}

	a.RuntimeType = domain.RuntimeType(runtimeType)
	a.Status = domain.AgentStatus(status)

	if len(labelsJSON) > 0 {
		_ = json.Unmarshal(labelsJSON, &a.Labels)
	}
	if len(capsJSON) > 0 {
		_ = json.Unmarshal(capsJSON, &a.Capabilities)
	}
	if len(sloJSON) > 0 {
		_ = json.Unmarshal(sloJSON, &a.SLOs)
	}
	if len(resJSON) > 0 {
		_ = json.Unmarshal(resJSON, &a.Resources)
	}
	if len(deplJSON) > 0 {
		_ = json.Unmarshal(deplJSON, &a.Deployment)
	}

	return a, nil
}

func scanMission(row scannable) (*domain.Mission, error) {
	m := &domain.Mission{}
	var status string
	var capsJSON, rationaleJSON []byte
	var timeoutMs int64

	if err := row.Scan(&m.ID, &m.AgentID, &m.TeamID, &m.ProjectID, &status,
		&capsJSON, &m.Payload, &rationaleJSON, &m.Priority, &timeoutMs,
		&m.SubmittedAt, &m.AssignedAt, &m.CompletedAt); err != nil {
		return nil, fmt.Errorf("postgres: scan mission: %w", err)
	}

	m.Status = domain.MissionStatus(status)
	m.Timeout = time.Duration(timeoutMs) * time.Millisecond

	if len(capsJSON) > 0 {
		_ = json.Unmarshal(capsJSON, &m.RequiredCapabilities)
	}
	if len(rationaleJSON) > 0 {
		r := &domain.SelectionRationale{}
		_ = json.Unmarshal(rationaleJSON, r)
		m.AssignmentRationale = r
	}

	return m, nil
}

func scanMissionRows(rows pgx.Rows) (*domain.Mission, error) {
	m := &domain.Mission{}
	var status string
	var capsJSON, rationaleJSON []byte
	var timeoutMs int64

	if err := rows.Scan(&m.ID, &m.AgentID, &m.TeamID, &m.ProjectID, &status,
		&capsJSON, &m.Payload, &rationaleJSON, &m.Priority, &timeoutMs,
		&m.SubmittedAt, &m.AssignedAt, &m.CompletedAt); err != nil {
		return nil, fmt.Errorf("postgres: scan mission row: %w", err)
	}

	m.Status = domain.MissionStatus(status)
	m.Timeout = time.Duration(timeoutMs) * time.Millisecond

	if len(capsJSON) > 0 {
		_ = json.Unmarshal(capsJSON, &m.RequiredCapabilities)
	}
	if len(rationaleJSON) > 0 {
		r := &domain.SelectionRationale{}
		_ = json.Unmarshal(rationaleJSON, r)
		m.AssignmentRationale = r
	}

	return m, nil
}

func scanPolicy(row scannable) (*domain.Policy, error) {
	p := &domain.Policy{}
	var scope string
	var rulesJSON []byte

	if err := row.Scan(&p.ID, &p.Name, &scope, &p.TeamID, &rulesJSON, &p.Version, &p.UpdatedAt); err != nil {
		return nil, fmt.Errorf("postgres: scan policy: %w", err)
	}

	p.Scope = domain.PolicyScope(scope)
	if len(rulesJSON) > 0 {
		_ = json.Unmarshal(rulesJSON, &p.Rules)
	}

	return p, nil
}

func scanPolicyRows(rows pgx.Rows) (*domain.Policy, error) {
	p := &domain.Policy{}
	var scope string
	var rulesJSON []byte

	if err := rows.Scan(&p.ID, &p.Name, &scope, &p.TeamID, &rulesJSON, &p.Version, &p.UpdatedAt); err != nil {
		return nil, fmt.Errorf("postgres: scan policy row: %w", err)
	}

	p.Scope = domain.PolicyScope(scope)
	if len(rulesJSON) > 0 {
		_ = json.Unmarshal(rulesJSON, &p.Rules)
	}

	return p, nil
}

// Verify interface compliance at compile time.
var _ store.Store = (*Store)(nil)
var _ store.AgentStore = (*AgentStore)(nil)
var _ store.MissionStore = (*MissionStore)(nil)
var _ store.PolicyStore = (*PolicyStore)(nil)
var _ store.CostStore = (*CostStore)(nil)
var _ store.TraceStore = (*TraceStore)(nil)
var _ store.BudgetStore = (*BudgetStore)(nil)
var _ store.MessageStore = (*MessageStore)(nil)


