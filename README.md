# AgentPlane

<p align="center">
  <img src="docs/agentplane-fleet.gif" alt="AgentPlane Fleet Animation" width="640"/>
</p>

**A lightweight control plane for managing fleets of AI agents in production.**

AgentPlane is a single Go binary that provides registry, scheduling, policy enforcement, lifecycle management, observability, cost control, and inter-agent communication for AI agent fleets. Think "Nomad for agents" — GitOps-friendly YAML manifests, protocol-native MCP/A2A integration, and a storage layer that scales from SQLite on a laptop to PostgreSQL in production without architecture changes.

---

## Key Features

| Capability | Description |
|------------|-------------|
| **Agent Registry** | Catalog of registered agents with capabilities, versions, labels, and SLOs |
| **Mission Scheduling** | Weighted scoring (cost/latency/load) with tie-breaking and queue management |
| **Policy Engine** | CEL-based rules with budget enforcement, inheritance, and fail-safe deny |
| **Lifecycle Manager** | Deploy, canary (1–50%), auto-rollback, scaling (1–100 instances), deprecation |
| **Observability Plane** | Mission-level traces (OpenTelemetry), drift detection (>2σ over 7-day baseline) |
| **Cost Controller** | Token/compute/tool metering, team budgets, 80% warning / 100% block |
| **Communication Bus** | A2A message routing, retry with exponential backoff, dead letter queue |
| **SLO Manager** | Rolling compliance evaluation, traffic reduction, auto-rollback triggers |
| **Safety Mesh** | Fleet-wide kill switch (<1s), per-agent circuit breakers, probe recovery |
| **Dashboard** | React SPA embedded in the binary — fleet status, costs, traces, alerts |
| **CLI (apctl)** | Full fleet management from the terminal with table/JSON/YAML output |

---

## Architecture

![AgentPlane Architecture](docs/architecture.png)

All components communicate internally via gRPC. External clients use REST, MCP, or A2A protocols.

<details>
<summary>Text diagram (for terminals)</summary>

```
┌──────────────────────────────────────────────────────────────┐
│                     External Interfaces                        │
│  REST (:8080)   MCP (:8081)   A2A (:8082)   Dashboard   CLI  │
└──────────────────────────┬───────────────────────────────────┘
                           │
┌──────────────────────────▼───────────────────────────────────┐
│                   API Gateway (chi router)                     │
│         Auth (API Key + OAuth2) · Rate limiting               │
└──────────────────────────┬───────────────────────────────────┘
                           │ gRPC (internal)
┌──────────────────────────▼───────────────────────────────────┐
│                      Core Services                            │
│  Registry · Scheduler · Policy · Lifecycle · Observability    │
│  Cost · Communication · SLO · Safety · Reconciliation         │
└──────────────────────────┬───────────────────────────────────┘
                           │
┌──────────────────────────▼───────────────────────────────────┐
│                     Storage Layer                              │
│            SQLite (default) │ PostgreSQL (production)          │
└──────────────────────────────────────────────────────────────┘
```

</details>

---

## Quick Start

### Prerequisites

- Go 1.23+
- GCC (for SQLite via CGO)
- Node.js 18+ (optional, for dashboard development)

### Build

```bash
# Build both binaries for your platform
make build

# Outputs:
#   bin/agentplane   (control plane server)
#   bin/apctl        (CLI tool)
```

### Run

```bash
# Start with defaults (SQLite, all ports on localhost)
./bin/agentplane

# Server starts on:
#   :8080  REST API + Dashboard
#   :8081  MCP interface
#   :8082  A2A interface
#   :9090  Internal gRPC
```

AgentPlane auto-initializes a local SQLite database (`agentplane.db`) on first run. No external dependencies required.

### Register an Agent

Create a manifest file `code-reviewer.yaml`:

```yaml
apiVersion: agentplane.io/v1
kind: Agent
metadata:
  name: code-reviewer
  namespace: engineering
  version: "1.0.0"
  labels:
    team: platform
    env: production
spec:
  runtime: claude
  capabilities:
    - name: code-review
      type: mcp-tool
    - name: test-generation
      type: a2a-message
  slo:
    latency: 5000
    accuracy: 95.0
    cost: 0.05
  resources:
    maxConcurrentMissions: 10
    maxMemoryMB: 512
  deployment:
    strategy: canary
    canaryPercent: 10
    maxInstances: 5
    drainTimeout: 300s
```

Apply it:

```bash
export AGENTPLANE_ENDPOINT=http://localhost:8080
export AGENTPLANE_TOKEN=your-api-key

apctl agent apply -f code-reviewer.yaml
apctl agent list
apctl fleet status
```

---

## Configuration

AgentPlane accepts configuration via **YAML file**, **environment variables**, and **CLI flags**, with precedence: flags > env > YAML.

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `AGENTPLANE_HTTP_ADDR` | `:8080` | REST API listen address |
| `AGENTPLANE_GRPC_ADDR` | `:9090` | Internal gRPC address |
| `AGENTPLANE_MCP_ADDR` | `:8081` | MCP interface address |
| `AGENTPLANE_A2A_ADDR` | `:8082` | A2A interface address |
| `AGENTPLANE_STORAGE_BACKEND` | `sqlite` | `sqlite` or `postgres` |
| `AGENTPLANE_SQLITE_DSN` | `file:agentplane.db?...` | SQLite connection string |
| `AGENTPLANE_POSTGRES_DSN` | (empty) | PostgreSQL connection string |
| `AGENTPLANE_SESSION_TIMEOUT` | `30m` | Dashboard session idle timeout |
| `AGENTPLANE_OTLP_ENDPOINT` | (empty) | OpenTelemetry collector endpoint |
| `AGENTPLANE_RETENTION_DAYS` | `30` | Trace retention period |
| `AGENTPLANE_MISSION_TIMEOUT` | `3600s` | Default mission timeout |
| `AGENTPLANE_CIRCUIT_BREAKER_COOLDOWN` | `60` | Circuit breaker cooldown (seconds) |
| `AGENTPLANE_BUDGET_PERIOD` | `monthly` | Budget enforcement period |
| `AGENTPLANE_LOG_LEVEL` | `info` | Log verbosity |

### YAML Configuration File

```yaml
httpAddr: ":8080"
storageBackend: postgres
postgresDSN: "postgres://user:pass@host:5432/agentplane?sslmode=require"
schedulerCostWeight: 0.4
schedulerLatencyWeight: 0.3
schedulerLoadWeight: 0.3
otlpEndpoint: "otel-collector:4317"
retentionDays: 90
logLevel: info
```

Pass via flag: `agentplane --config /etc/agentplane/config.yaml`

---

## CLI Reference (apctl)

| Command | Description |
|---------|-------------|
| `apctl agent apply -f manifest.yaml` | Register or update an agent |
| `apctl agent list` | List registered agents |
| `apctl agent describe <name>` | Show agent details |
| `apctl mission submit -f mission.json` | Submit a mission |
| `apctl mission status <id>` | Check mission status |
| `apctl policy apply -f policy.yaml` | Apply a policy |
| `apctl fleet status` | Show fleet-wide status |
| `apctl migrate --source sqlite --target postgres` | Migrate data between backends |
| `apctl auth create-key --name <name>` | Create an API key |

All commands support `--output table|json|yaml` (default: table).

---

## Storage

### SQLite (Default)

Zero-config, embedded. Suitable for development, small deployments, and single-node production.

### PostgreSQL

For multi-instance or high-availability deployments:

```bash
export AGENTPLANE_STORAGE_BACKEND=postgres
export AGENTPLANE_POSTGRES_DSN="postgres://user:pass@host:5432/agentplane"
./bin/agentplane
```

### Migration

Migrate from SQLite to PostgreSQL without downtime:

```bash
apctl migrate --source sqlite --target postgres
```

Records are transferred table-by-table with count verification. Source remains unmodified on failure.

---

## Protocols

### REST API (`:8080`)

Full CRUD for agents, missions, policies, costs, budgets, and fleet operations. Authentication via API key or OAuth2 bearer token.

### MCP Interface (`:8081`)

Model Context Protocol compatible. Exposes registered agent capabilities as tools. Routes `tools/call` through the scheduler and policy engine.

### A2A Interface (`:8082`)

Agent-to-Agent protocol for inter-agent communication. Messages routed through the Communication Bus with policy enforcement and dead letter queue support.

---

## Safety & Policy

### Kill Switch

Instantly halts all fleet activity:
- Cancels all active missions within 1 second
- Prevents new assignments
- Transitions agents to idle-safe state
- Preserves interrupted mission state

### Circuit Breakers

Per-agent automatic protection:
- Opens when error rate exceeds 50% over a 5-minute window (minimum 5 missions)
- Blocks all missions while open
- Probe recovery after configurable cooldown (10–3600 seconds)

### Policy Engine

CEL-based rules with:
- Budget enforcement (reject when estimated + accumulated ≥ cap)
- Organizational inheritance (team overrides org-level)
- Fail-safe deny on errors or empty policies
- Max 50 rules per policy, evaluated within 500ms

---

## Observability

- **Mission Traces**: Full goal → steps → outcome tracing via OpenTelemetry
- **Drift Detection**: Alerts when metrics deviate >2σ from 7-day rolling baseline
- **OTLP Export**: Compatible with Jaeger, Grafana Tempo, Datadog, and any OTLP collector
- **Retention**: Configurable (default 30 days), auto-cleanup within 24h of expiry

---

## Cost Control

- **Metering**: Tokens, compute time, tool invocations per mission
- **Attribution**: Costs attributed to team, project, and mission
- **Budget Warning**: Event emitted at 80% threshold
- **Budget Block**: New missions blocked at 100%; in-progress complete without new tool calls
- **Query API**: Filter by team/agent/project/time, max 1000 records, <2s response

---

## Cross-Compilation

```bash
# Build for all platforms
make release

# Individual targets
make build-linux-amd64
make build-linux-arm64
make build-darwin-amd64
make build-darwin-arm64
make build-windows-amd64
```

Linux builds produce fully static binaries. The `apctl` binary is pure Go (no CGO) and cross-compiles trivially. The `agentplane` binary requires CGO for SQLite.

---

## Testing

```bash
# Run all tests (unit + property-based)
make test

# Short mode (skips long-running property tests)
make test-short
```

Property-based tests use [pgregory.net/rapid](https://pgregory.net/rapid) to validate correctness properties including:
- Registration round-trips and duplicate rejection
- Scheduler scoring, tie-breaking, and agent exclusion
- Policy inheritance, budget enforcement, fail-safe deny
- Canary traffic distribution and auto-rollback
- Mission trace completeness and drift detection
- Kill switch halts, circuit breaker state machines
- Configuration precedence and health endpoint correctness
- Failed reconciliation preserves fleet state

---

## Project Structure

```
agentplane/
├── cmd/
│   ├── agentplane/       # Control plane server entry point
│   └── apctl/            # CLI binary entry point
├── internal/
│   ├── domain/           # Core types, enums, manifest validation
│   ├── registry/         # Agent Registry service
│   ├── scheduler/        # Mission Scheduler service
│   ├── policy/           # Policy Engine service
│   ├── lifecycle/        # Lifecycle Manager service
│   ├── observability/    # Observability Plane service
│   ├── cost/             # Cost Controller service
│   ├── communication/    # Communication Bus service
│   ├── slo/              # SLO Manager service
│   ├── safety/           # Safety Mesh service
│   ├── reconciliation/   # Manifest reconciliation loop
│   ├── gateway/          # REST/MCP/A2A/gRPC gateway
│   ├── config/           # Configuration loading
│   └── store/            # Storage interfaces + SQLite/PostgreSQL backends
├── web/
│   └── dashboard/        # React SPA (embedded in binary)
├── tests/
│   ├── property/         # Property-based tests (rapid)
│   ├── unit/             # Unit tests
│   └── integration/      # Integration tests
├── Makefile              # Build system
├── go.mod
└── go.sum
```

---

## License

[Add your license here]
