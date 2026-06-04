-- 001_initial.sql: Initial schema for AgentPlane PostgreSQL backend

CREATE TABLE IF NOT EXISTS agents (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    namespace TEXT NOT NULL DEFAULT '',
    version TEXT NOT NULL,
    runtime_type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    labels_json JSONB NOT NULL DEFAULT '{}',
    capabilities_json JSONB NOT NULL DEFAULT '[]',
    slo_json JSONB NOT NULL DEFAULT '{}',
    resources_json JSONB NOT NULL DEFAULT '{}',
    deployment_json JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(name, namespace)
);

CREATE TABLE IF NOT EXISTS agent_versions (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    version TEXT NOT NULL,
    registered_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_agent_versions_agent_id ON agent_versions(agent_id);

CREATE TABLE IF NOT EXISTS missions (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL DEFAULT '',
    team_id TEXT NOT NULL DEFAULT '',
    project_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    required_capabilities_json JSONB NOT NULL DEFAULT '[]',
    payload BYTEA,
    assignment_rationale_json JSONB,
    priority INTEGER NOT NULL DEFAULT 0,
    timeout_ms BIGINT NOT NULL DEFAULT 3600000,
    submitted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    assigned_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_missions_status ON missions(status);
CREATE INDEX IF NOT EXISTS idx_missions_agent_id ON missions(agent_id);

CREATE TABLE IF NOT EXISTS policies (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    scope TEXT NOT NULL,
    team_id TEXT NOT NULL DEFAULT '',
    rules_json JSONB NOT NULL DEFAULT '[]',
    version INTEGER NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_policies_scope ON policies(scope, team_id);

CREATE TABLE IF NOT EXISTS cost_events (
    id TEXT PRIMARY KEY,
    mission_id TEXT NOT NULL DEFAULT '',
    agent_id TEXT NOT NULL DEFAULT '',
    team_id TEXT NOT NULL DEFAULT '',
    project_id TEXT NOT NULL DEFAULT '',
    token_count BIGINT NOT NULL DEFAULT 0,
    compute_time_ms BIGINT NOT NULL DEFAULT 0,
    tool_invocations INTEGER NOT NULL DEFAULT 0,
    total_cost DOUBLE PRECISION NOT NULL DEFAULT 0,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cost_events_team ON cost_events(team_id);
CREATE INDEX IF NOT EXISTS idx_cost_events_recorded ON cost_events(recorded_at);

CREATE TABLE IF NOT EXISTS budget_caps (
    id TEXT PRIMARY KEY,
    team_id TEXT NOT NULL UNIQUE,
    cap_amount DOUBLE PRECISION NOT NULL DEFAULT 0,
    period_type TEXT NOT NULL DEFAULT 'monthly',
    period_start TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    accumulated_cost DOUBLE PRECISION NOT NULL DEFAULT 0,
    is_blocked BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE TABLE IF NOT EXISTS mission_traces (
    trace_id TEXT PRIMARY KEY,
    mission_id TEXT NOT NULL DEFAULT '',
    agent_id TEXT NOT NULL DEFAULT '',
    goal TEXT NOT NULL DEFAULT '',
    outcome TEXT NOT NULL DEFAULT '',
    duration_ms BIGINT NOT NULL DEFAULT 0,
    start_time TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    end_time TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '30 days')
);

CREATE INDEX IF NOT EXISTS idx_traces_mission ON mission_traces(mission_id);
CREATE INDEX IF NOT EXISTS idx_traces_expires ON mission_traces(expires_at);

CREATE TABLE IF NOT EXISTS trace_steps (
    span_id TEXT PRIMARY KEY,
    trace_id TEXT NOT NULL REFERENCES mission_traces(trace_id) ON DELETE CASCADE,
    parent_span_id TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    duration_ms BIGINT NOT NULL DEFAULT 0,
    attributes_json JSONB NOT NULL DEFAULT '{}',
    start_time TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_trace_steps_trace ON trace_steps(trace_id);

CREATE TABLE IF NOT EXISTS messages (
    id TEXT PRIMARY KEY,
    from_agent TEXT NOT NULL DEFAULT '',
    to_agent TEXT NOT NULL DEFAULT '',
    message_type TEXT NOT NULL DEFAULT 'point_to_point',
    payload BYTEA,
    retry_count INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'pending',
    sent_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    delivered_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_messages_status ON messages(status);

CREATE TABLE IF NOT EXISTS dead_letters (
    id TEXT PRIMARY KEY,
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    failure_reason TEXT NOT NULL DEFAULT '',
    retry_attempts INTEGER NOT NULL DEFAULT 0,
    failed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
