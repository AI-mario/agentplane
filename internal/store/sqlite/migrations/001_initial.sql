-- 001_initial.sql: Initial schema for AgentPlane

CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS agents (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    namespace TEXT NOT NULL DEFAULT '',
    version TEXT NOT NULL,
    runtime_type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    labels_json TEXT NOT NULL DEFAULT '{}',
    capabilities_json TEXT NOT NULL DEFAULT '[]',
    slo_json TEXT NOT NULL DEFAULT '{}',
    resources_json TEXT NOT NULL DEFAULT '{}',
    deployment_json TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(name, namespace, version)
);

CREATE INDEX IF NOT EXISTS idx_agents_name ON agents(name);
CREATE INDEX IF NOT EXISTS idx_agents_namespace ON agents(namespace);
CREATE INDEX IF NOT EXISTS idx_agents_status ON agents(status);
CREATE INDEX IF NOT EXISTS idx_agents_runtime_type ON agents(runtime_type);

CREATE TABLE IF NOT EXISTS agent_versions (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL,
    version TEXT NOT NULL,
    registered_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_agent_versions_agent_id ON agent_versions(agent_id);

CREATE TABLE IF NOT EXISTS missions (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL DEFAULT '',
    team_id TEXT NOT NULL DEFAULT '',
    project_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    required_capabilities_json TEXT NOT NULL DEFAULT '[]',
    payload BLOB,
    assignment_rationale_json TEXT,
    priority INTEGER NOT NULL DEFAULT 0,
    timeout_ms INTEGER NOT NULL DEFAULT 3600000,
    submitted_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    assigned_at TIMESTAMP,
    completed_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_missions_agent_id ON missions(agent_id);
CREATE INDEX IF NOT EXISTS idx_missions_team_id ON missions(team_id);
CREATE INDEX IF NOT EXISTS idx_missions_status ON missions(status);

CREATE TABLE IF NOT EXISTS policies (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    scope TEXT NOT NULL,
    team_id TEXT NOT NULL DEFAULT '',
    rules_json TEXT NOT NULL DEFAULT '[]',
    version INTEGER NOT NULL DEFAULT 1,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_policies_scope ON policies(scope);
CREATE INDEX IF NOT EXISTS idx_policies_team_id ON policies(team_id);

CREATE TABLE IF NOT EXISTS cost_events (
    id TEXT PRIMARY KEY,
    mission_id TEXT NOT NULL DEFAULT '',
    agent_id TEXT NOT NULL DEFAULT '',
    team_id TEXT NOT NULL DEFAULT '',
    project_id TEXT NOT NULL DEFAULT '',
    token_count INTEGER NOT NULL DEFAULT 0,
    compute_time_ms INTEGER NOT NULL DEFAULT 0,
    tool_invocations INTEGER NOT NULL DEFAULT 0,
    total_cost REAL NOT NULL DEFAULT 0.0,
    recorded_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_cost_events_mission_id ON cost_events(mission_id);
CREATE INDEX IF NOT EXISTS idx_cost_events_team_id ON cost_events(team_id);
CREATE INDEX IF NOT EXISTS idx_cost_events_agent_id ON cost_events(agent_id);
CREATE INDEX IF NOT EXISTS idx_cost_events_recorded_at ON cost_events(recorded_at);

CREATE TABLE IF NOT EXISTS budget_caps (
    id TEXT PRIMARY KEY,
    team_id TEXT NOT NULL UNIQUE,
    cap_amount REAL NOT NULL DEFAULT 0.0,
    period_type TEXT NOT NULL DEFAULT 'monthly',
    period_start TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    accumulated_cost REAL NOT NULL DEFAULT 0.0,
    is_blocked INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_budget_caps_team_id ON budget_caps(team_id);

CREATE TABLE IF NOT EXISTS mission_traces (
    trace_id TEXT PRIMARY KEY,
    mission_id TEXT NOT NULL DEFAULT '',
    agent_id TEXT NOT NULL DEFAULT '',
    goal TEXT NOT NULL DEFAULT '',
    outcome TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    start_time TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    end_time TIMESTAMP,
    expires_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_mission_traces_mission_id ON mission_traces(mission_id);
CREATE INDEX IF NOT EXISTS idx_mission_traces_agent_id ON mission_traces(agent_id);
CREATE INDEX IF NOT EXISTS idx_mission_traces_expires_at ON mission_traces(expires_at);

CREATE TABLE IF NOT EXISTS trace_steps (
    span_id TEXT PRIMARY KEY,
    trace_id TEXT NOT NULL,
    parent_span_id TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'success',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    attributes_json TEXT NOT NULL DEFAULT '{}',
    start_time TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (trace_id) REFERENCES mission_traces(trace_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_trace_steps_trace_id ON trace_steps(trace_id);

CREATE TABLE IF NOT EXISTS messages (
    id TEXT PRIMARY KEY,
    from_agent TEXT NOT NULL DEFAULT '',
    to_agent TEXT NOT NULL DEFAULT '',
    message_type TEXT NOT NULL DEFAULT 'point_to_point',
    payload BLOB,
    retry_count INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'pending',
    sent_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    delivered_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_messages_from_agent ON messages(from_agent);
CREATE INDEX IF NOT EXISTS idx_messages_to_agent ON messages(to_agent);
CREATE INDEX IF NOT EXISTS idx_messages_status ON messages(status);

CREATE TABLE IF NOT EXISTS dead_letters (
    id TEXT PRIMARY KEY,
    message_id TEXT NOT NULL,
    failure_reason TEXT NOT NULL DEFAULT '',
    retry_attempts INTEGER NOT NULL DEFAULT 0,
    failed_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (message_id) REFERENCES messages(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_dead_letters_message_id ON dead_letters(message_id);

CREATE TABLE IF NOT EXISTS circuit_breakers (
    agent_id TEXT PRIMARY KEY,
    state TEXT NOT NULL DEFAULT 'closed',
    error_rate REAL NOT NULL DEFAULT 0.0,
    cooldown_secs INTEGER NOT NULL DEFAULT 60,
    opened_at TIMESTAMP,
    cooldown_end TIMESTAMP
);

CREATE TABLE IF NOT EXISTS slo_metrics (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL,
    metric_type TEXT NOT NULL,
    value REAL NOT NULL DEFAULT 0.0,
    recorded_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_slo_metrics_agent_id ON slo_metrics(agent_id);
CREATE INDEX IF NOT EXISTS idx_slo_metrics_recorded_at ON slo_metrics(recorded_at);
