package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/store"
	_ "github.com/mattn/go-sqlite3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// SQLiteStore implements store.Store using SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// New opens a SQLite database and returns a Store implementation.
func New(dsn string) (*SQLiteStore, error) {
	if dsn == "" {
		dsn = "file:agentplane.db?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on"
	}
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite open: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite supports one writer at a time
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("sqlite ping: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

// Agents returns the AgentStore implementation.
func (s *SQLiteStore) Agents() store.AgentStore { return &agentStore{db: s.db} }

// Missions returns the MissionStore implementation.
func (s *SQLiteStore) Missions() store.MissionStore { return &missionStore{db: s.db} }

// Policies returns the PolicyStore implementation.
func (s *SQLiteStore) Policies() store.PolicyStore { return &policyStore{db: s.db} }

// Costs returns the CostStore implementation.
func (s *SQLiteStore) Costs() store.CostStore { return &costStore{db: s.db} }

// Traces returns the TraceStore implementation.
func (s *SQLiteStore) Traces() store.TraceStore { return &traceStore{db: s.db} }

// Budgets returns the BudgetStore implementation.
func (s *SQLiteStore) Budgets() store.BudgetStore { return &budgetStore{db: s.db} }

// Messages returns the MessageStore implementation.
func (s *SQLiteStore) Messages() store.MessageStore { return &messageStore{db: s.db} }

// Migrate acquires a lock, reads embedded migrations, and applies them sequentially.
func (s *SQLiteStore) Migrate(ctx context.Context) error {
	// SQLite single-writer mode acts as implicit lock via MaxOpenConns(1).
	// We also use a transaction to ensure atomicity.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate begin tx: %w", err)
	}
	defer tx.Rollback()

	// Ensure schema_migrations table exists.
	_, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		return fmt.Errorf("migrate create tracking table: %w", err)
	}

	// Determine already applied versions.
	applied := make(map[int]bool)
	rows, err := tx.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return fmt.Errorf("migrate query applied: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return fmt.Errorf("migrate scan version: %w", err)
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("migrate rows err: %w", err)
	}

	// Read and apply migrations in order.
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("migrate read dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		// Parse version number from filename (e.g., "001_initial.sql" → 1).
		var version int
		fmt.Sscanf(entry.Name(), "%d_", &version)
		if version == 0 {
			continue
		}
		if applied[version] {
			continue
		}

		content, err := migrationsFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("migrate read file %s: %w", entry.Name(), err)
		}

		// Execute migration SQL (skip CREATE TABLE for schema_migrations since we already have it).
		stmts := splitStatements(string(content))
		for _, stmt := range stmts {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}
			// Skip the schema_migrations CREATE since we already created it above.
			if strings.Contains(stmt, "CREATE TABLE IF NOT EXISTS schema_migrations") {
				continue
			}
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("migrate exec %s: %w", entry.Name(), err)
			}
		}

		// Record migration.
		_, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)", version, time.Now().UTC())
		if err != nil {
			return fmt.Errorf("migrate record %d: %w", version, err)
		}
	}

	return tx.Commit()
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// DB exposes the underlying *sql.DB for testing purposes.
func (s *SQLiteStore) DB() *sql.DB {
	return s.db
}

// splitStatements splits SQL text on semicolons (simple approach for SQLite DDL).
func splitStatements(sql string) []string {
	var stmts []string
	for _, s := range strings.Split(sql, ";") {
		s = strings.TrimSpace(s)
		if s != "" {
			stmts = append(stmts, s)
		}
	}
	return stmts
}

// --- AgentStore ---

type agentStore struct {
	db *sql.DB
}

func (s *agentStore) Create(ctx context.Context, agent *domain.AgentEntry) error {
	capsJSON, _ := json.Marshal(agent.Capabilities)
	labelsJSON, _ := json.Marshal(agent.Labels)
	sloJSON, _ := json.Marshal(agent.SLOs)
	resourcesJSON, _ := json.Marshal(agent.Resources)
	deployJSON, _ := json.Marshal(agent.Deployment)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agents (id, name, namespace, version, runtime_type, status, labels_json, capabilities_json, slo_json, resources_json, deployment_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		agent.ID, agent.Name, agent.Namespace, agent.Version, string(agent.RuntimeType),
		string(agent.Status), string(labelsJSON), string(capsJSON), string(sloJSON),
		string(resourcesJSON), string(deployJSON), agent.CreatedAt.UTC(), agent.UpdatedAt.UTC())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return fmt.Errorf("agent already exists: %s/%s@%s", agent.Namespace, agent.Name, agent.Version)
		}
		return fmt.Errorf("agent create: %w", err)
	}
	return nil
}

func (s *agentStore) Get(ctx context.Context, id string) (*domain.AgentEntry, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, namespace, version, runtime_type, status, labels_json, capabilities_json, slo_json, resources_json, deployment_json, created_at, updated_at
		 FROM agents WHERE id = ?`, id)
	return scanAgent(row)
}

func (s *agentStore) GetByName(ctx context.Context, name, namespace string) (*domain.AgentEntry, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, namespace, version, runtime_type, status, labels_json, capabilities_json, slo_json, resources_json, deployment_json, created_at, updated_at
		 FROM agents WHERE name = ? AND namespace = ? ORDER BY created_at DESC LIMIT 1`, name, namespace)
	return scanAgent(row)
}

func (s *agentStore) List(ctx context.Context, filter domain.AgentFilter) ([]*domain.AgentEntry, error) {
	query := `SELECT id, name, namespace, version, runtime_type, status, labels_json, capabilities_json, slo_json, resources_json, deployment_json, created_at, updated_at FROM agents WHERE 1=1`
	var args []interface{}

	if filter.Name != "" {
		query += " AND name = ?"
		args = append(args, filter.Name)
	}
	if filter.Status != "" {
		query += " AND status = ?"
		args = append(args, string(filter.Status))
	}
	if filter.Runtime != "" {
		query += " AND runtime_type = ?"
		args = append(args, string(filter.Runtime))
	}

	query += " ORDER BY created_at DESC"
	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filter.Limit)
	}
	if filter.Offset > 0 {
		query += fmt.Sprintf(" OFFSET %d", filter.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("agent list: %w", err)
	}
	defer rows.Close()

	var agents []*domain.AgentEntry
	for rows.Next() {
		a, err := scanAgentRow(rows)
		if err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

func (s *agentStore) Update(ctx context.Context, agent *domain.AgentEntry) error {
	capsJSON, _ := json.Marshal(agent.Capabilities)
	labelsJSON, _ := json.Marshal(agent.Labels)
	sloJSON, _ := json.Marshal(agent.SLOs)
	resourcesJSON, _ := json.Marshal(agent.Resources)
	deployJSON, _ := json.Marshal(agent.Deployment)

	_, err := s.db.ExecContext(ctx,
		`UPDATE agents SET name=?, namespace=?, version=?, runtime_type=?, status=?, labels_json=?, capabilities_json=?, slo_json=?, resources_json=?, deployment_json=?, updated_at=?
		 WHERE id=?`,
		agent.Name, agent.Namespace, agent.Version, string(agent.RuntimeType),
		string(agent.Status), string(labelsJSON), string(capsJSON), string(sloJSON),
		string(resourcesJSON), string(deployJSON), agent.UpdatedAt.UTC(), agent.ID)
	return err
}

func (s *agentStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM agents WHERE id = ?", id)
	return err
}

func (s *agentStore) QueryByCapability(ctx context.Context, capability string) ([]*domain.AgentEntry, error) {
	// Query all agents and filter by capability in Go (SQLite JSON support is limited).
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, namespace, version, runtime_type, status, labels_json, capabilities_json, slo_json, resources_json, deployment_json, created_at, updated_at
		 FROM agents WHERE status = 'active'`)
	if err != nil {
		return nil, fmt.Errorf("agent query by capability: %w", err)
	}
	defer rows.Close()

	var result []*domain.AgentEntry
	for rows.Next() {
		a, err := scanAgentRow(rows)
		if err != nil {
			return nil, err
		}
		for _, cap := range a.Capabilities {
			if cap.Name == capability {
				result = append(result, a)
				break
			}
		}
	}
	return result, rows.Err()
}

func (s *agentStore) AddVersion(ctx context.Context, version *domain.AgentVersion) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_versions (id, agent_id, version, registered_at) VALUES (?, ?, ?, ?)`,
		version.ID, version.AgentID, version.Version, version.RegisteredAt.UTC())
	return err
}

func (s *agentStore) ListVersions(ctx context.Context, agentID string) ([]*domain.AgentVersion, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, agent_id, version, registered_at FROM agent_versions WHERE agent_id = ? ORDER BY registered_at DESC`,
		agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var versions []*domain.AgentVersion
	for rows.Next() {
		v := &domain.AgentVersion{}
		var registeredAt string
		if err := rows.Scan(&v.ID, &v.AgentID, &v.Version, &registeredAt); err != nil {
			return nil, err
		}
		v.RegisteredAt, _ = time.Parse(time.RFC3339Nano, registeredAt)
		if v.RegisteredAt.IsZero() {
			v.RegisteredAt, _ = time.Parse("2006-01-02 15:04:05.999999999-07:00", registeredAt)
		}
		if v.RegisteredAt.IsZero() {
			v.RegisteredAt, _ = time.Parse("2006-01-02T15:04:05Z", registeredAt)
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

func (s *agentStore) DeleteVersion(ctx context.Context, versionID string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM agent_versions WHERE id = ?", versionID)
	return err
}

// --- MissionStore ---

type missionStore struct {
	db *sql.DB
}

func (s *missionStore) Create(ctx context.Context, mission *domain.Mission) error {
	capsJSON, _ := json.Marshal(mission.RequiredCapabilities)
	var rationaleJSON []byte
	if mission.AssignmentRationale != nil {
		rationaleJSON, _ = json.Marshal(mission.AssignmentRationale)
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO missions (id, agent_id, team_id, project_id, status, required_capabilities_json, payload, assignment_rationale_json, priority, timeout_ms, submitted_at, assigned_at, completed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		mission.ID, mission.AgentID, mission.TeamID, mission.ProjectID,
		string(mission.Status), string(capsJSON), mission.Payload,
		nullableString(rationaleJSON), mission.Priority, mission.Timeout.Milliseconds(),
		mission.SubmittedAt.UTC(), nullableTime(mission.AssignedAt), nullableTime(mission.CompletedAt))
	return err
}

func (s *missionStore) Get(ctx context.Context, id string) (*domain.Mission, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, agent_id, team_id, project_id, status, required_capabilities_json, payload, assignment_rationale_json, priority, timeout_ms, submitted_at, assigned_at, completed_at
		 FROM missions WHERE id = ?`, id)
	return scanMission(row)
}

func (s *missionStore) List(ctx context.Context, filter domain.MissionFilter) ([]*domain.Mission, error) {
	query := `SELECT id, agent_id, team_id, project_id, status, required_capabilities_json, payload, assignment_rationale_json, priority, timeout_ms, submitted_at, assigned_at, completed_at FROM missions WHERE 1=1`
	var args []interface{}

	if filter.AgentID != "" {
		query += " AND agent_id = ?"
		args = append(args, filter.AgentID)
	}
	if filter.TeamID != "" {
		query += " AND team_id = ?"
		args = append(args, filter.TeamID)
	}
	if filter.Status != "" {
		query += " AND status = ?"
		args = append(args, string(filter.Status))
	}

	query += " ORDER BY submitted_at DESC"
	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filter.Limit)
	}
	if filter.Offset > 0 {
		query += fmt.Sprintf(" OFFSET %d", filter.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var missions []*domain.Mission
	for rows.Next() {
		m, err := scanMissionRow(rows)
		if err != nil {
			return nil, err
		}
		missions = append(missions, m)
	}
	return missions, rows.Err()
}

func (s *missionStore) Update(ctx context.Context, mission *domain.Mission) error {
	capsJSON, _ := json.Marshal(mission.RequiredCapabilities)
	var rationaleJSON []byte
	if mission.AssignmentRationale != nil {
		rationaleJSON, _ = json.Marshal(mission.AssignmentRationale)
	}

	_, err := s.db.ExecContext(ctx,
		`UPDATE missions SET agent_id=?, team_id=?, project_id=?, status=?, required_capabilities_json=?, payload=?, assignment_rationale_json=?, priority=?, timeout_ms=?, assigned_at=?, completed_at=?
		 WHERE id=?`,
		mission.AgentID, mission.TeamID, mission.ProjectID, string(mission.Status),
		string(capsJSON), mission.Payload, nullableString(rationaleJSON),
		mission.Priority, mission.Timeout.Milliseconds(),
		nullableTime(mission.AssignedAt), nullableTime(mission.CompletedAt), mission.ID)
	return err
}

func (s *missionStore) GetPending(ctx context.Context) ([]*domain.Mission, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, agent_id, team_id, project_id, status, required_capabilities_json, payload, assignment_rationale_json, priority, timeout_ms, submitted_at, assigned_at, completed_at
		 FROM missions WHERE status IN ('pending', 'queued') ORDER BY priority DESC, submitted_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var missions []*domain.Mission
	for rows.Next() {
		m, err := scanMissionRow(rows)
		if err != nil {
			return nil, err
		}
		missions = append(missions, m)
	}
	return missions, rows.Err()
}

// --- PolicyStore ---

type policyStore struct {
	db *sql.DB
}

func (s *policyStore) Create(ctx context.Context, policy *domain.Policy) error {
	rulesJSON, _ := json.Marshal(policy.Rules)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO policies (id, name, scope, team_id, rules_json, version, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		policy.ID, policy.Name, string(policy.Scope), policy.TeamID,
		string(rulesJSON), policy.Version, policy.UpdatedAt.UTC())
	return err
}

func (s *policyStore) Get(ctx context.Context, id string) (*domain.Policy, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, scope, team_id, rules_json, version, updated_at FROM policies WHERE id = ?`, id)
	return scanPolicy(row)
}

func (s *policyStore) List(ctx context.Context, filter domain.PolicyFilter) ([]*domain.Policy, error) {
	query := `SELECT id, name, scope, team_id, rules_json, version, updated_at FROM policies WHERE 1=1`
	var args []interface{}

	if filter.Scope != "" {
		query += " AND scope = ?"
		args = append(args, string(filter.Scope))
	}
	if filter.TeamID != "" {
		query += " AND team_id = ?"
		args = append(args, filter.TeamID)
	}

	query += " ORDER BY updated_at DESC"
	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filter.Limit)
	}
	if filter.Offset > 0 {
		query += fmt.Sprintf(" OFFSET %d", filter.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var policies []*domain.Policy
	for rows.Next() {
		p, err := scanPolicyRow(rows)
		if err != nil {
			return nil, err
		}
		policies = append(policies, p)
	}
	return policies, rows.Err()
}

func (s *policyStore) Update(ctx context.Context, policy *domain.Policy) error {
	rulesJSON, _ := json.Marshal(policy.Rules)
	_, err := s.db.ExecContext(ctx,
		`UPDATE policies SET name=?, scope=?, team_id=?, rules_json=?, version=?, updated_at=? WHERE id=?`,
		policy.Name, string(policy.Scope), policy.TeamID, string(rulesJSON),
		policy.Version, policy.UpdatedAt.UTC(), policy.ID)
	return err
}

func (s *policyStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM policies WHERE id = ?", id)
	return err
}

func (s *policyStore) GetEffective(ctx context.Context, scope domain.PolicyScope, scopeID string) ([]*domain.Policy, error) {
	// Get org-level policies + team-level policies for given scope.
	query := `SELECT id, name, scope, team_id, rules_json, version, updated_at FROM policies WHERE scope = 'organization'`
	var args []interface{}

	if scope == domain.PolicyScopeTeam && scopeID != "" {
		query += " UNION ALL SELECT id, name, scope, team_id, rules_json, version, updated_at FROM policies WHERE scope = 'team' AND team_id = ?"
		args = append(args, scopeID)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var policies []*domain.Policy
	for rows.Next() {
		p, err := scanPolicyRow(rows)
		if err != nil {
			return nil, err
		}
		policies = append(policies, p)
	}
	return policies, rows.Err()
}

// --- CostStore ---

type costStore struct {
	db *sql.DB
}

func (s *costStore) Record(ctx context.Context, record *domain.CostRecord) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO cost_events (id, mission_id, agent_id, team_id, project_id, token_count, compute_time_ms, tool_invocations, total_cost, recorded_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.MissionID, record.AgentID, record.TeamID, record.ProjectID,
		record.TokenCount, record.ComputeTimeMs, record.ToolInvocations, record.TotalCost, record.RecordedAt.UTC())
	return err
}

func (s *costStore) Query(ctx context.Context, filter domain.CostFilter) ([]*domain.CostRecord, int, error) {
	query := `SELECT id, mission_id, agent_id, team_id, project_id, token_count, compute_time_ms, tool_invocations, total_cost, recorded_at FROM cost_events WHERE 1=1`
	countQuery := `SELECT COUNT(*) FROM cost_events WHERE 1=1`
	var args []interface{}
	var countArgs []interface{}

	if filter.TeamID != "" {
		query += " AND team_id = ?"
		countQuery += " AND team_id = ?"
		args = append(args, filter.TeamID)
		countArgs = append(countArgs, filter.TeamID)
	}
	if filter.AgentID != "" {
		query += " AND agent_id = ?"
		countQuery += " AND agent_id = ?"
		args = append(args, filter.AgentID)
		countArgs = append(countArgs, filter.AgentID)
	}
	if filter.ProjectID != "" {
		query += " AND project_id = ?"
		countQuery += " AND project_id = ?"
		args = append(args, filter.ProjectID)
		countArgs = append(countArgs, filter.ProjectID)
	}
	if !filter.StartTime.IsZero() {
		query += " AND recorded_at >= ?"
		countQuery += " AND recorded_at >= ?"
		args = append(args, filter.StartTime.UTC())
		countArgs = append(countArgs, filter.StartTime.UTC())
	}
	if !filter.EndTime.IsZero() {
		query += " AND recorded_at <= ?"
		countQuery += " AND recorded_at <= ?"
		args = append(args, filter.EndTime.UTC())
		countArgs = append(countArgs, filter.EndTime.UTC())
	}

	var total int
	s.db.QueryRowContext(ctx, countQuery, countArgs...).Scan(&total)

	query += " ORDER BY recorded_at DESC"
	limit := filter.Limit
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	query += fmt.Sprintf(" LIMIT %d", limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var records []*domain.CostRecord
	for rows.Next() {
		r := &domain.CostRecord{}
		var recordedAt string
		if err := rows.Scan(&r.ID, &r.MissionID, &r.AgentID, &r.TeamID, &r.ProjectID,
			&r.TokenCount, &r.ComputeTimeMs, &r.ToolInvocations, &r.TotalCost, &recordedAt); err != nil {
			return nil, 0, err
		}
		r.RecordedAt = parseTime(recordedAt)
		records = append(records, r)
	}
	return records, total, rows.Err()
}

func (s *costStore) Aggregate(ctx context.Context, filter domain.CostFilter) (*domain.CostAggregation, error) {
	query := `SELECT COALESCE(SUM(total_cost),0), COALESCE(SUM(token_count),0), COALESCE(SUM(compute_time_ms),0), COALESCE(SUM(tool_invocations),0), COUNT(*) FROM cost_events WHERE 1=1`
	var args []interface{}

	if filter.TeamID != "" {
		query += " AND team_id = ?"
		args = append(args, filter.TeamID)
	}
	if filter.AgentID != "" {
		query += " AND agent_id = ?"
		args = append(args, filter.AgentID)
	}
	if filter.ProjectID != "" {
		query += " AND project_id = ?"
		args = append(args, filter.ProjectID)
	}
	if !filter.StartTime.IsZero() {
		query += " AND recorded_at >= ?"
		args = append(args, filter.StartTime.UTC())
	}
	if !filter.EndTime.IsZero() {
		query += " AND recorded_at <= ?"
		args = append(args, filter.EndTime.UTC())
	}

	agg := &domain.CostAggregation{}
	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&agg.TotalCost, &agg.TotalTokens, &agg.TotalComputeMs, &agg.TotalToolCalls, &agg.RecordCount)
	return agg, err
}

// --- TraceStore ---

type traceStore struct {
	db *sql.DB
}

func (s *traceStore) Create(ctx context.Context, trace *domain.MissionTrace) error {
	var endTime *string
	if trace.EndTime != nil {
		t := trace.EndTime.UTC().Format(time.RFC3339Nano)
		endTime = &t
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO mission_traces (trace_id, mission_id, agent_id, goal, outcome, duration_ms, start_time, end_time, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		trace.TraceID, trace.MissionID, trace.AgentID, trace.Goal,
		string(trace.Outcome), trace.Duration.Milliseconds(),
		trace.StartTime.UTC(), endTime, trace.ExpiresAt.UTC())
	return err
}

func (s *traceStore) Update(ctx context.Context, trace *domain.MissionTrace) error {
	var endTime *string
	if trace.EndTime != nil {
		t := trace.EndTime.UTC().Format(time.RFC3339Nano)
		endTime = &t
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE mission_traces SET outcome=?, duration_ms=?, end_time=?, expires_at=? WHERE trace_id=?`,
		string(trace.Outcome), trace.Duration.Milliseconds(), endTime, trace.ExpiresAt.UTC(), trace.TraceID)
	return err
}

func (s *traceStore) AddSpan(ctx context.Context, span *domain.TraceSpan) error {
	attrsJSON, _ := json.Marshal(span.Attributes)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO trace_steps (span_id, trace_id, parent_span_id, name, status, duration_ms, attributes_json, start_time)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		span.SpanID, span.TraceID, span.ParentSpanID, span.Name,
		string(span.Status), span.DurationMs, string(attrsJSON), span.StartTime.UTC())
	return err
}

func (s *traceStore) Get(ctx context.Context, id string) (*domain.MissionTrace, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT trace_id, mission_id, agent_id, goal, outcome, duration_ms, start_time, end_time, expires_at
		 FROM mission_traces WHERE trace_id = ?`, id)

	trace, err := scanTrace(row)
	if err != nil {
		return nil, err
	}

	// Load steps.
	rows, err := s.db.QueryContext(ctx,
		`SELECT span_id, trace_id, parent_span_id, name, status, duration_ms, attributes_json, start_time
		 FROM trace_steps WHERE trace_id = ? ORDER BY start_time ASC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		step, err := scanTraceStep(rows)
		if err != nil {
			return nil, err
		}
		trace.Steps = append(trace.Steps, *step)
	}
	return trace, rows.Err()
}

func (s *traceStore) Query(ctx context.Context, filter domain.TraceFilter) ([]*domain.MissionTrace, error) {
	query := `SELECT trace_id, mission_id, agent_id, goal, outcome, duration_ms, start_time, end_time, expires_at FROM mission_traces WHERE 1=1`
	var args []interface{}

	if filter.MissionID != "" {
		query += " AND mission_id = ?"
		args = append(args, filter.MissionID)
	}
	if filter.AgentID != "" {
		query += " AND agent_id = ?"
		args = append(args, filter.AgentID)
	}
	if filter.Outcome != "" {
		query += " AND outcome = ?"
		args = append(args, string(filter.Outcome))
	}
	if !filter.StartTime.IsZero() {
		query += " AND start_time >= ?"
		args = append(args, filter.StartTime.UTC())
	}
	if !filter.EndTime.IsZero() {
		query += " AND start_time <= ?"
		args = append(args, filter.EndTime.UTC())
	}

	query += " ORDER BY start_time DESC"
	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filter.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var traces []*domain.MissionTrace
	for rows.Next() {
		t, err := scanTraceRow(rows)
		if err != nil {
			return nil, err
		}
		traces = append(traces, t)
	}
	return traces, rows.Err()
}

func (s *traceStore) DeleteExpired(ctx context.Context) (int, error) {
	result, err := s.db.ExecContext(ctx, "DELETE FROM mission_traces WHERE expires_at <= ?", time.Now().UTC())
	if err != nil {
		return 0, err
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}

// --- BudgetStore ---

type budgetStore struct {
	db *sql.DB
}

func (s *budgetStore) ListAll(ctx context.Context) ([]*domain.Budget, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, team_id, cap_amount, period_type, period_start, accumulated_cost, is_blocked FROM budget_caps`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var budgets []*domain.Budget
	for rows.Next() {
		b := &domain.Budget{}
		var periodStart string
		var isBlocked int
		if err := rows.Scan(&b.ID, &b.TeamID, &b.CapAmount, &b.PeriodType, &periodStart, &b.AccumulatedCost, &isBlocked); err != nil {
			return nil, err
		}
		b.PeriodStart = parseTime(periodStart)
		b.IsBlocked = isBlocked != 0
		budgets = append(budgets, b)
	}
	return budgets, rows.Err()
}

func (s *budgetStore) Get(ctx context.Context, teamID string) (*domain.Budget, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, team_id, cap_amount, period_type, period_start, accumulated_cost, is_blocked
		 FROM budget_caps WHERE team_id = ?`, teamID)

	b := &domain.Budget{}
	var periodStart string
	var isBlocked int
	err := row.Scan(&b.ID, &b.TeamID, &b.CapAmount, &b.PeriodType, &periodStart, &b.AccumulatedCost, &isBlocked)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	b.PeriodStart = parseTime(periodStart)
	b.IsBlocked = isBlocked != 0
	return b, nil
}

func (s *budgetStore) Set(ctx context.Context, budget *domain.Budget) error {
	var isBlocked int
	if budget.IsBlocked {
		isBlocked = 1
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO budget_caps (id, team_id, cap_amount, period_type, period_start, accumulated_cost, is_blocked)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		budget.ID, budget.TeamID, budget.CapAmount, budget.PeriodType,
		budget.PeriodStart.UTC(), budget.AccumulatedCost, isBlocked)
	return err
}

func (s *budgetStore) IncrementAccumulated(ctx context.Context, teamID string, amount float64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE budget_caps SET accumulated_cost = accumulated_cost + ? WHERE team_id = ?`,
		amount, teamID)
	return err
}

func (s *budgetStore) ResetPeriod(ctx context.Context, teamID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE budget_caps SET accumulated_cost = 0, is_blocked = 0, period_start = ? WHERE team_id = ?`,
		time.Now().UTC(), teamID)
	return err
}

// --- MessageStore ---

type messageStore struct {
	db *sql.DB
}

func (s *messageStore) Create(ctx context.Context, msg *domain.AgentMessage) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (id, from_agent, to_agent, message_type, payload, retry_count, status, sent_at, delivered_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		msg.ID, msg.From, msg.To, string(msg.Type), msg.Payload,
		msg.RetryCount, string(msg.Status), msg.SentAt.UTC(), nullableTime(msg.DeliveredAt))
	return err
}

func (s *messageStore) List(ctx context.Context, limit int) ([]*domain.AgentMessage, error) {
	query := `SELECT id, from_agent, to_agent, message_type, payload, retry_count, status, sent_at, delivered_at FROM messages ORDER BY sent_at DESC`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []*domain.AgentMessage
	for rows.Next() {
		msg := &domain.AgentMessage{}
		var msgType, status, sentAt string
		var deliveredAt *string
		if err := rows.Scan(&msg.ID, &msg.From, &msg.To, &msgType, &msg.Payload, &msg.RetryCount, &status, &sentAt, &deliveredAt); err != nil {
			return nil, err
		}
		msg.Type = domain.MessageType(msgType)
		msg.Status = domain.MessageStatus(status)
		msg.SentAt = parseTime(sentAt)
		if deliveredAt != nil {
			t := parseTime(*deliveredAt)
			msg.DeliveredAt = &t
		}
		msgs = append(msgs, msg)
	}
	return msgs, rows.Err()
}

func (s *messageStore) Get(ctx context.Context, id string) (*domain.AgentMessage, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, from_agent, to_agent, message_type, payload, retry_count, status, sent_at, delivered_at
		 FROM messages WHERE id = ?`, id)
	return scanMessage(row)
}

func (s *messageStore) GetDLQ(ctx context.Context, filter domain.DLQFilter) ([]*domain.DeadLetter, error) {
	query := `SELECT dl.id, dl.message_id, dl.failure_reason, dl.retry_attempts, dl.failed_at,
		m.id, m.from_agent, m.to_agent, m.message_type, m.payload, m.retry_count, m.status, m.sent_at, m.delivered_at
		FROM dead_letters dl JOIN messages m ON dl.message_id = m.id WHERE 1=1`
	var args []interface{}

	if filter.From != "" {
		query += " AND m.from_agent = ?"
		args = append(args, filter.From)
	}
	if filter.To != "" {
		query += " AND m.to_agent = ?"
		args = append(args, filter.To)
	}

	query += " ORDER BY dl.failed_at DESC"
	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filter.Limit)
	}
	if filter.Offset > 0 {
		query += fmt.Sprintf(" OFFSET %d", filter.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var letters []*domain.DeadLetter
	for rows.Next() {
		dl := &domain.DeadLetter{OriginalMessage: &domain.AgentMessage{}}
		var failedAt, sentAt string
		var deliveredAt *string
		if err := rows.Scan(
			&dl.ID, &dl.MessageID, &dl.FailureReason, &dl.RetryAttempts, &failedAt,
			&dl.OriginalMessage.ID, &dl.OriginalMessage.From, &dl.OriginalMessage.To,
			&dl.OriginalMessage.Type, &dl.OriginalMessage.Payload, &dl.OriginalMessage.RetryCount,
			&dl.OriginalMessage.Status, &sentAt, &deliveredAt,
		); err != nil {
			return nil, err
		}
		dl.FailedAt = parseTime(failedAt)
		dl.OriginalMessage.SentAt = parseTime(sentAt)
		if deliveredAt != nil {
			t := parseTime(*deliveredAt)
			dl.OriginalMessage.DeliveredAt = &t
		}
		letters = append(letters, dl)
	}
	return letters, rows.Err()
}

func (s *messageStore) MoveToDLQ(ctx context.Context, msgID string, reason string) error {
	// Get current retry count.
	var retryCount int
	s.db.QueryRowContext(ctx, "SELECT retry_count FROM messages WHERE id = ?", msgID).Scan(&retryCount)

	// Update message status.
	_, err := s.db.ExecContext(ctx, "UPDATE messages SET status = ? WHERE id = ?", string(domain.MessageStatusDLQ), msgID)
	if err != nil {
		return err
	}

	// Insert dead letter.
	dlID := fmt.Sprintf("dl-%s", msgID)
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO dead_letters (id, message_id, failure_reason, retry_attempts, failed_at) VALUES (?, ?, ?, ?, ?)`,
		dlID, msgID, reason, retryCount, time.Now().UTC())
	return err
}

func (s *messageStore) UpdateStatus(ctx context.Context, id string, status domain.MessageStatus) error {
	var deliveredAt *string
	if status == domain.MessageStatusDelivered {
		t := time.Now().UTC().Format(time.RFC3339Nano)
		deliveredAt = &t
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE messages SET status = ?, delivered_at = COALESCE(?, delivered_at) WHERE id = ?`,
		string(status), deliveredAt, id)
	return err
}

// --- Scan helpers ---

type scannable interface {
	Scan(dest ...interface{}) error
}

func scanAgent(row scannable) (*domain.AgentEntry, error) {
	a := &domain.AgentEntry{}
	var runtimeType, status, labelsJSON, capsJSON, sloJSON, resourcesJSON, deployJSON string
	var createdAt, updatedAt string

	err := row.Scan(&a.ID, &a.Name, &a.Namespace, &a.Version, &runtimeType, &status,
		&labelsJSON, &capsJSON, &sloJSON, &resourcesJSON, &deployJSON, &createdAt, &updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	a.RuntimeType = domain.RuntimeType(runtimeType)
	a.Status = domain.AgentStatus(status)
	a.CreatedAt = parseTime(createdAt)
	a.UpdatedAt = parseTime(updatedAt)
	json.Unmarshal([]byte(labelsJSON), &a.Labels)
	json.Unmarshal([]byte(capsJSON), &a.Capabilities)
	json.Unmarshal([]byte(sloJSON), &a.SLOs)
	json.Unmarshal([]byte(resourcesJSON), &a.Resources)
	json.Unmarshal([]byte(deployJSON), &a.Deployment)

	return a, nil
}

func scanAgentRow(rows *sql.Rows) (*domain.AgentEntry, error) {
	return scanAgent(rows)
}

func scanMission(row scannable) (*domain.Mission, error) {
	m := &domain.Mission{}
	var status, capsJSON string
	var rationaleJSON *string
	var timeoutMs int64
	var submittedAt string
	var assignedAt, completedAt *string

	err := row.Scan(&m.ID, &m.AgentID, &m.TeamID, &m.ProjectID, &status, &capsJSON,
		&m.Payload, &rationaleJSON, &m.Priority, &timeoutMs, &submittedAt, &assignedAt, &completedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	m.Status = domain.MissionStatus(status)
	m.Timeout = time.Duration(timeoutMs) * time.Millisecond
	m.SubmittedAt = parseTime(submittedAt)
	json.Unmarshal([]byte(capsJSON), &m.RequiredCapabilities)

	if rationaleJSON != nil && *rationaleJSON != "" {
		r := &domain.SelectionRationale{}
		json.Unmarshal([]byte(*rationaleJSON), r)
		m.AssignmentRationale = r
	}
	if assignedAt != nil {
		t := parseTime(*assignedAt)
		m.AssignedAt = &t
	}
	if completedAt != nil {
		t := parseTime(*completedAt)
		m.CompletedAt = &t
	}

	return m, nil
}

func scanMissionRow(rows *sql.Rows) (*domain.Mission, error) {
	return scanMission(rows)
}

func scanPolicy(row scannable) (*domain.Policy, error) {
	p := &domain.Policy{}
	var scope, rulesJSON, updatedAt string

	err := row.Scan(&p.ID, &p.Name, &scope, &p.TeamID, &rulesJSON, &p.Version, &updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	p.Scope = domain.PolicyScope(scope)
	p.UpdatedAt = parseTime(updatedAt)
	json.Unmarshal([]byte(rulesJSON), &p.Rules)

	return p, nil
}

func scanPolicyRow(rows *sql.Rows) (*domain.Policy, error) {
	return scanPolicy(rows)
}

func scanTrace(row scannable) (*domain.MissionTrace, error) {
	t := &domain.MissionTrace{}
	var outcome string
	var durationMs int64
	var startTime string
	var endTime *string
	var expiresAt string

	err := row.Scan(&t.TraceID, &t.MissionID, &t.AgentID, &t.Goal, &outcome, &durationMs, &startTime, &endTime, &expiresAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	t.Outcome = domain.MissionOutcome(outcome)
	t.Duration = time.Duration(durationMs) * time.Millisecond
	t.StartTime = parseTime(startTime)
	t.ExpiresAt = parseTime(expiresAt)
	if endTime != nil {
		et := parseTime(*endTime)
		t.EndTime = &et
	}

	return t, nil
}

func scanTraceRow(rows *sql.Rows) (*domain.MissionTrace, error) {
	return scanTrace(rows)
}

func scanTraceStep(rows *sql.Rows) (*domain.TraceStep, error) {
	step := &domain.TraceStep{}
	var status string
	var durationMs int64
	var attrsJSON, startTime string

	err := rows.Scan(&step.SpanID, &step.TraceID, &step.ParentSpanID, &step.Name, &status, &durationMs, &attrsJSON, &startTime)
	if err != nil {
		return nil, err
	}

	step.Status = domain.StepStatus(status)
	step.Duration = time.Duration(durationMs) * time.Millisecond
	step.StartTime = parseTime(startTime)
	json.Unmarshal([]byte(attrsJSON), &step.Attributes)

	return step, nil
}

func scanMessage(row scannable) (*domain.AgentMessage, error) {
	msg := &domain.AgentMessage{}
	var msgType, status, sentAt string
	var deliveredAt *string

	err := row.Scan(&msg.ID, &msg.From, &msg.To, &msgType, &msg.Payload, &msg.RetryCount, &status, &sentAt, &deliveredAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	msg.Type = domain.MessageType(msgType)
	msg.Status = domain.MessageStatus(status)
	msg.SentAt = parseTime(sentAt)
	if deliveredAt != nil {
		t := parseTime(*deliveredAt)
		msg.DeliveredAt = &t
	}

	return msg, nil
}

// --- Utility helpers ---

func nullableTime(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func nullableString(b []byte) interface{} {
	if b == nil {
		return nil
	}
	return string(b)
}

func parseTime(s string) time.Time {
	// Try multiple formats that SQLite might produce.
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999+00:00",
		"2006-01-02 15:04:05",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
