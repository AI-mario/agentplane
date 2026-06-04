# Installation & Deployment Guide

## Table of Contents

- [Prerequisites](#prerequisites)
- [Installation Methods](#installation-methods)
- [Configuration](#configuration)
- [Deployment Options](#deployment-options)
- [Post-Installation Verification](#post-installation-verification)
- [Agent Configuration Guide](#agent-configuration-guide)
- [Gemini Enterprise Integration (Step-by-Step)](#gemini-enterprise-integration-step-by-step)
- [Troubleshooting](#troubleshooting)

---

## Prerequisites

### System Requirements

| Component | Minimum | Recommended |
|-----------|---------|-------------|
| CPU | 1 core | 2+ cores |
| RAM | 256 MB | 512 MB+ |
| Disk | 100 MB (binary + DB) | 1 GB+ |
| OS | Linux (amd64/arm64), macOS, Windows | Linux amd64 |

### Build Requirements (from source)

| Tool | Version | Purpose |
|------|---------|---------|
| Go | 1.23+ | Compilation |
| GCC | Any recent | SQLite CGO binding |
| Make | GNU Make 3.8+ | Build system |
| Node.js | 18+ | Dashboard build (optional) |
| npm | 9+ | Dashboard dependencies (optional) |

### Runtime Requirements

- **No external dependencies** for default mode (SQLite)
- **PostgreSQL 14+** if using Postgres backend
- **Network**: Ports 8080, 8081, 8082, 9090 (configurable)

---

## Installation Methods

### Method 1: Download Pre-built Binary (Recommended)

```bash
# Linux amd64
curl -L https://github.com/AI-mario/agentplane/releases/latest/download/agentplane-linux-amd64 -o agentplane
curl -L https://github.com/AI-mario/agentplane/releases/latest/download/apctl-linux-amd64 -o apctl
chmod +x agentplane apctl
sudo mv agentplane apctl /usr/local/bin/
```

```bash
# macOS arm64 (Apple Silicon)
curl -L https://github.com/AI-mario/agentplane/releases/latest/download/agentplane-darwin-arm64 -o agentplane
curl -L https://github.com/AI-mario/agentplane/releases/latest/download/apctl-darwin-arm64 -o apctl
chmod +x agentplane apctl
mv agentplane apctl /usr/local/bin/
```

### Method 2: Build from Source

```bash
git clone https://github.com/AI-mario/agentplane.git
cd agentplane

# Build for current platform
make build

# Binaries are in bin/
ls bin/
# agentplane  apctl
```

### Method 3: Build with Dashboard

```bash
git clone https://github.com/AI-mario/agentplane.git
cd agentplane

# Build dashboard first
make dashboard

# Then build server (dashboard assets are embedded)
make build
```

### Method 4: Docker (coming soon)

```dockerfile
FROM golang:1.23-alpine AS builder
RUN apk add --no-cache gcc musl-dev
WORKDIR /app
COPY . .
RUN make build

FROM alpine:3.19
COPY --from=builder /app/bin/agentplane /usr/local/bin/
COPY --from=builder /app/bin/apctl /usr/local/bin/
EXPOSE 8080 8081 8082 9090
ENTRYPOINT ["agentplane"]
```

---

## Configuration

### Quick Start (Zero Config)

```bash
# Just run it — SQLite auto-initialized, all defaults applied
agentplane
```

This starts with:
- REST API on `:8080`
- MCP interface on `:8081`
- A2A interface on `:8082`
- gRPC on `:9090`
- SQLite database at `./agentplane.db`
- Dashboard at `http://localhost:8080`

### Configuration File

Create `/etc/agentplane/config.yaml` (or any path):

```yaml
# Server addresses
httpAddr: ":8080"
grpcAddr: ":9090"
mcpAddr: ":8081"
a2aAddr: ":8082"

# Storage backend: "sqlite" or "postgres"
storageBackend: sqlite
sqliteDSN: "file:/var/lib/agentplane/data.db?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on"

# PostgreSQL (uncomment for production)
# storageBackend: postgres
# postgresDSN: "postgres://agentplane:secretpassword@db.example.com:5432/agentplane?sslmode=require"

# Session management
sessionTimeout: 30m

# Scheduler weights (must sum to 1.0)
schedulerCostWeight: 0.4
schedulerLatencyWeight: 0.3
schedulerLoadWeight: 0.3

# Observability
otlpEndpoint: ""  # e.g., "otel-collector:4317"
retentionDays: 30
missionTimeout: 3600s

# Safety
circuitBreakerCooldown: 60  # seconds

# Cost control
budgetPeriod: monthly

# Lifecycle
deployTimeout: 60s
maxDrainTimeout: 300s

# Logging
logLevel: info  # debug, info, warn, error
```

Run with config file:

```bash
agentplane --config /etc/agentplane/config.yaml
```

### Environment Variables

All settings can be overridden via environment variables with `AGENTPLANE_` prefix:

```bash
export AGENTPLANE_HTTP_ADDR=":8080"
export AGENTPLANE_STORAGE_BACKEND="postgres"
export AGENTPLANE_POSTGRES_DSN="postgres://user:pass@host:5432/agentplane"
export AGENTPLANE_OTLP_ENDPOINT="otel-collector:4317"
export AGENTPLANE_LOG_LEVEL="debug"
export AGENTPLANE_SESSION_TIMEOUT="1h"
export AGENTPLANE_RETENTION_DAYS="90"

agentplane
```

### Configuration Precedence

```
CLI flags  >  Environment variables  >  YAML config file  >  Defaults
```

### Authentication Setup

#### Create an API Key

```bash
# Start the server first, then:
apctl auth create-key --name "platform-team"

# Output:
# API Key created:
#   Name: platform-team
#   Key:  ap_k_xxxxxxxxxxxxxxxxxxxx
#   
# Store this key securely — it cannot be retrieved again.
```

#### Configure CLI

```bash
# Via environment (recommended for CI/CD)
export AGENTPLANE_ENDPOINT="http://localhost:8080"
export AGENTPLANE_TOKEN="ap_k_xxxxxxxxxxxxxxxxxxxx"

# Or via config file (~/.agentplane/config.yaml)
cat > ~/.agentplane/config.yaml << EOF
endpoint: "http://localhost:8080"
token: "ap_k_xxxxxxxxxxxxxxxxxxxx"
EOF
```

---

## Deployment Options

### Option A: Laptop / Development

```bash
# Single command, everything auto-configured
agentplane
```

SQLite is created in current directory. Perfect for local development and testing.

### Option B: Single Server (Production)

```bash
# 1. Create system user
sudo useradd -r -s /bin/false agentplane

# 2. Create directories
sudo mkdir -p /etc/agentplane /var/lib/agentplane /var/log/agentplane

# 3. Copy binary
sudo cp bin/agentplane /usr/local/bin/
sudo cp bin/apctl /usr/local/bin/

# 4. Copy config
sudo cp config.yaml /etc/agentplane/config.yaml
sudo chown -R agentplane:agentplane /var/lib/agentplane

# 5. Create systemd service
sudo cat > /etc/systemd/system/agentplane.service << 'EOF'
[Unit]
Description=AgentPlane Control Plane
After=network.target

[Service]
Type=simple
User=agentplane
Group=agentplane
ExecStart=/usr/local/bin/agentplane --config /etc/agentplane/config.yaml
Restart=always
RestartSec=5
LimitNOFILE=65535

# Security hardening
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/var/lib/agentplane /var/log/agentplane

[Install]
WantedBy=multi-user.target
EOF

# 6. Start
sudo systemctl daemon-reload
sudo systemctl enable --now agentplane

# 7. Verify
curl http://localhost:8080/health
```

### Option C: PostgreSQL Backend (Scalable Production)

```bash
# 1. Create PostgreSQL database
psql -h db.example.com -U postgres -c "CREATE DATABASE agentplane;"
psql -h db.example.com -U postgres -c "CREATE USER agentplane WITH PASSWORD 'strongpassword';"
psql -h db.example.com -U postgres -c "GRANT ALL PRIVILEGES ON DATABASE agentplane TO agentplane;"

# 2. Configure
export AGENTPLANE_STORAGE_BACKEND=postgres
export AGENTPLANE_POSTGRES_DSN="postgres://agentplane:strongpassword@db.example.com:5432/agentplane?sslmode=require"

# 3. Start (migrations run automatically)
agentplane --config /etc/agentplane/config.yaml
```

### Option D: Migrate from SQLite to PostgreSQL

```bash
# After PostgreSQL is configured:
apctl migrate --source sqlite --target postgres

# Output:
# Migrating agents... 42 records
# Migrating missions... 1,203 records
# Migrating policies... 8 records
# ...
# Migration complete. 1,412 total records transferred.
```

### Option E: Kubernetes

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: agentplane
spec:
  replicas: 1
  selector:
    matchLabels:
      app: agentplane
  template:
    metadata:
      labels:
        app: agentplane
    spec:
      containers:
      - name: agentplane
        image: ghcr.io/ai-mario/agentplane:latest
        ports:
        - containerPort: 8080
          name: http
        - containerPort: 8081
          name: mcp
        - containerPort: 8082
          name: a2a
        - containerPort: 9090
          name: grpc
        env:
        - name: AGENTPLANE_STORAGE_BACKEND
          value: "postgres"
        - name: AGENTPLANE_POSTGRES_DSN
          valueFrom:
            secretKeyRef:
              name: agentplane-db
              key: dsn
        - name: AGENTPLANE_OTLP_ENDPOINT
          value: "otel-collector.monitoring:4317"
        livenessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 5
        readinessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 3
        resources:
          requests:
            memory: "256Mi"
            cpu: "250m"
          limits:
            memory: "512Mi"
            cpu: "1000m"
---
apiVersion: v1
kind: Service
metadata:
  name: agentplane
spec:
  selector:
    app: agentplane
  ports:
  - name: http
    port: 8080
  - name: mcp
    port: 8081
  - name: a2a
    port: 8082
  - name: grpc
    port: 9090
```

---

## Post-Installation Verification

```bash
# 1. Health check
curl -s http://localhost:8080/health | jq .
# Expected: {"status": "healthy", "subsystems": {...}}

# 2. CLI connectivity
export AGENTPLANE_ENDPOINT=http://localhost:8080
export AGENTPLANE_TOKEN=ap_k_your_key_here
apctl fleet status

# 3. Dashboard
open http://localhost:8080  # Opens web dashboard

# 4. Register a test agent
cat > /tmp/test-agent.yaml << 'EOF'
apiVersion: agentplane.io/v1
kind: Agent
metadata:
  name: test-agent
  namespace: default
  version: "0.1.0"
  labels:
    env: test
spec:
  runtime: custom
  capabilities:
    - name: echo
      type: mcp-tool
  slo:
    latency: 5000
    accuracy: 99.0
    cost: 0.01
  resources:
    maxConcurrentMissions: 5
    maxMemoryMB: 256
  deployment:
    strategy: immediate
    maxInstances: 1
    drainTimeout: 60s
EOF

apctl agent apply -f /tmp/test-agent.yaml
apctl agent list
```

---

## Agent Configuration Guide

### Manifest Structure

Every agent is defined by a YAML manifest:

```yaml
apiVersion: agentplane.io/v1
kind: Agent
metadata:
  name: "<unique-name>"           # Required: 1-253 chars, lowercase + hyphens
  namespace: "<namespace>"        # Required: logical grouping
  version: "<semver>"             # Required: MAJOR.MINOR.PATCH
  labels:                         # Optional: up to 50 key-value pairs
    team: "my-team"
    env: "production"
spec:
  runtime: "<runtime>"            # Required: claude | kiro | bedrock | custom
  capabilities:                   # Required: 1-20 entries
    - name: "<capability-name>"
      type: "<mcp-tool|a2a-message>"
  slo:                            # Required: performance contracts
    latency: <ms>                 # 1-60000 milliseconds
    accuracy: <percent>           # 0.0-100.0
    cost: <decimal>               # per-invocation cost
  resources:
    maxConcurrentMissions: <int>  # Max parallel missions
    maxMemoryMB: <int>            # Memory limit
  deployment:
    strategy: "<strategy>"        # rolling | canary | immediate
    canaryPercent: <int>          # 1-50 (only for canary)
    maxInstances: <int>           # 1-100
    drainTimeout: "<duration>"    # e.g., "300s"
```

### Configuring a Claude Agent

```yaml
apiVersion: agentplane.io/v1
kind: Agent
metadata:
  name: claude-code-reviewer
  namespace: engineering
  version: "1.0.0"
  labels:
    team: platform
    env: production
    model: claude-sonnet-4
spec:
  runtime: claude
  capabilities:
    - name: code-review
      type: mcp-tool
    - name: security-audit
      type: mcp-tool
    - name: test-generation
      type: a2a-message
  slo:
    latency: 10000        # 10 seconds max
    accuracy: 95.0        # 95% accuracy target
    cost: 0.08            # $0.08 per invocation
  resources:
    maxConcurrentMissions: 5
    maxMemoryMB: 1024
  deployment:
    strategy: canary
    canaryPercent: 10
    maxInstances: 3
    drainTimeout: 120s
```

### Configuring a Kiro Agent

```yaml
apiVersion: agentplane.io/v1
kind: Agent
metadata:
  name: kiro-task-planner
  namespace: engineering
  version: "2.1.0"
  labels:
    team: platform
    env: production
    specialization: planning
spec:
  runtime: kiro
  capabilities:
    - name: task-decomposition
      type: mcp-tool
    - name: spec-generation
      type: mcp-tool
    - name: implementation-review
      type: a2a-message
  slo:
    latency: 30000        # 30 seconds (complex tasks)
    accuracy: 90.0
    cost: 0.12
  resources:
    maxConcurrentMissions: 3
    maxMemoryMB: 2048
  deployment:
    strategy: rolling
    maxInstances: 2
    drainTimeout: 300s
```

### Configuring a Bedrock Agent

```yaml
apiVersion: agentplane.io/v1
kind: Agent
metadata:
  name: bedrock-summarizer
  namespace: data-team
  version: "1.3.0"
  labels:
    team: data-engineering
    env: production
    model: anthropic.claude-v2
spec:
  runtime: bedrock
  capabilities:
    - name: document-summarization
      type: mcp-tool
    - name: data-extraction
      type: mcp-tool
    - name: report-generation
      type: a2a-message
  slo:
    latency: 15000
    accuracy: 92.0
    cost: 0.05
  resources:
    maxConcurrentMissions: 10
    maxMemoryMB: 512
  deployment:
    strategy: immediate
    maxInstances: 5
    drainTimeout: 60s
```

### Configuring a Custom Agent

```yaml
apiVersion: agentplane.io/v1
kind: Agent
metadata:
  name: custom-ml-predictor
  namespace: ml-ops
  version: "0.5.0"
  labels:
    team: ml-ops
    env: staging
    framework: pytorch
spec:
  runtime: custom
  capabilities:
    - name: predict
      type: mcp-tool
    - name: batch-inference
      type: mcp-tool
    - name: model-feedback
      type: a2a-message
  slo:
    latency: 500          # Fast inference
    accuracy: 98.0        # High accuracy required
    cost: 0.002           # Low cost per call
  resources:
    maxConcurrentMissions: 50
    maxMemoryMB: 4096
  deployment:
    strategy: canary
    canaryPercent: 5
    maxInstances: 10
    drainTimeout: 30s
```

### Registering Agents

```bash
# Apply a single agent
apctl agent apply -f agents/claude-code-reviewer.yaml

# Apply multiple agents (GitOps style)
for f in agents/*.yaml; do
  apctl agent apply -f "$f"
done

# Verify registration
apctl agent list
apctl agent describe claude-code-reviewer
```

### Updating an Agent

```bash
# Bump version in manifest, then:
apctl agent apply -f agents/claude-code-reviewer.yaml

# For canary deployments, traffic shifts automatically:
# - 10% to new version
# - If error rate > 5% or p99 > 3x baseline → auto-rollback
# - Otherwise promotes to 100% after evaluation window
```

### Defining Policies

```yaml
apiVersion: agentplane.io/v1
kind: Policy
metadata:
  name: engineering-team-policy
  scope: team
  teamId: platform
spec:
  rules:
    - id: budget-cap
      type: budget
      condition: "request.estimatedCost <= 500.00"
      effect: allow
    - id: allowed-runtimes
      type: runtime
      condition: "agent.runtime in ['claude', 'kiro', 'bedrock']"
      effect: allow
    - id: capability-scope
      type: capability
      condition: "mission.capabilities.all(c, c in ['code-review', 'test-generation', 'task-decomposition'])"
      effect: allow
```

```bash
apctl policy apply -f policies/engineering-team-policy.yaml
```

### Setting Team Budgets

Budgets are managed via the REST API:

```bash
# Set budget for a team (monthly cap of $1000)
curl -X POST http://localhost:8080/api/v1/budgets \
  -H "Authorization: Bearer $AGENTPLANE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "teamId": "platform",
    "capAmount": 1000.00,
    "periodType": "monthly"
  }'
```

### Submitting Missions

```bash
# Via CLI
apctl mission submit -f mission.json

# Mission payload:
cat > mission.json << 'EOF'
{
  "requiredCapabilities": ["code-review"],
  "priority": 1,
  "teamId": "platform",
  "projectId": "backend-api",
  "payload": {
    "goal": "Review PR #423 for security vulnerabilities",
    "context": {
      "repository": "github.com/org/repo",
      "pr_number": 423
    }
  },
  "timeout": "3600s"
}
EOF
```

### Monitoring

```bash
# Fleet overview
apctl fleet status

# Mission tracking
apctl mission status <mission-id>

# Cost report
curl -s "http://localhost:8080/api/v1/costs?teamId=platform&startTime=2025-01-01" \
  -H "Authorization: Bearer $AGENTPLANE_TOKEN" | jq .
```

---

## Gemini Enterprise Integration (Step-by-Step)

This guide walks through integrating Google Gemini Enterprise agents with AgentPlane, from GCP setup through mission dispatch.

### Prerequisites for Gemini

- A Google Cloud Platform account with billing enabled
- A GCP project with the Vertex AI API or Generative Language API enabled
- AgentPlane running (any deployment option)

### Step 1: Choose Authentication Mode

AgentPlane supports two authentication modes for Gemini:

| Mode | Best for | Setup complexity |
|------|----------|-----------------|
| **API Key** | Development, prototyping | Low |
| **Service Account (Vertex AI)** | Production, enterprise | Medium |

#### Option A: API Key (Simple)

1. Go to [Google AI Studio](https://aistudio.google.com/apikey)
2. Click "Create API Key"
3. Select your GCP project
4. Copy the generated key (starts with `AIza...`)

#### Option B: Service Account (Production)

```bash
# 1. Create a service account
gcloud iam service-accounts create agentplane-gemini \
  --display-name="AgentPlane Gemini Adapter" \
  --project=YOUR_PROJECT_ID

# 2. Grant Vertex AI User role
gcloud projects add-iam-policy-binding YOUR_PROJECT_ID \
  --member="serviceAccount:agentplane-gemini@YOUR_PROJECT_ID.iam.gserviceaccount.com" \
  --role="roles/aiplatform.user"

# 3. Create and download key file
gcloud iam service-accounts keys create gemini-sa-key.json \
  --iam-account=agentplane-gemini@YOUR_PROJECT_ID.iam.gserviceaccount.com

# 4. Store the key securely
sudo mkdir -p /etc/agentplane/secrets
sudo mv gemini-sa-key.json /etc/agentplane/secrets/
sudo chmod 600 /etc/agentplane/secrets/gemini-sa-key.json
```

### Step 2: Enable the API

```bash
# For Vertex AI (recommended for enterprise)
gcloud services enable aiplatform.googleapis.com --project=YOUR_PROJECT_ID

# OR for Generative Language API (simpler, API key mode)
gcloud services enable generativelanguage.googleapis.com --project=YOUR_PROJECT_ID
```

### Step 3: Configure AgentPlane

#### Option A: Environment Variables

```bash
# --- API Key mode ---
export AGENTPLANE_GEMINI_API_KEY="AIzaSy..."
export AGENTPLANE_GEMINI_MODEL="gemini-2.5-pro"   # optional, defaults to gemini-2.5-pro

# --- OR Vertex AI mode ---
export AGENTPLANE_GEMINI_PROJECT_ID="my-gcp-project"
export AGENTPLANE_GEMINI_LOCATION="us-central1"
export AGENTPLANE_GEMINI_MODEL="gemini-2.5-pro"
export AGENTPLANE_GEMINI_SERVICE_ACCOUNT_JSON="/etc/agentplane/secrets/gemini-sa-key.json"
```

#### Option B: YAML Configuration

Add to your `config.yaml`:

```yaml
# API Key mode
geminiApiKey: "AIzaSy..."
geminiModel: "gemini-2.5-pro"

# OR Vertex AI mode
geminiProjectId: "my-gcp-project"
geminiLocation: "us-central1"
geminiModel: "gemini-2.5-pro"
geminiServiceAccountJson: "/etc/agentplane/secrets/gemini-sa-key.json"
```

### Step 4: Start AgentPlane

```bash
# Verify Gemini adapter activates on startup
agentplane --config config.yaml

# Look for this log line:
# dispatch: gemini adapter enabled (model=gemini-2.5-pro)
```

### Step 5: Register a Gemini Agent

Create `agents/gemini-analyst.yaml`:

```yaml
apiVersion: agentplane.io/v1
kind: Agent
metadata:
  name: gemini-analyst
  namespace: data-team
  version: "1.0.0"
  labels:
    team: data-engineering
    provider: google
    model: gemini-2.5-pro
    tier: enterprise
spec:
  runtime: gemini
  capabilities:
    - name: data-analysis
      type: mcp-tool
    - name: document-summarization
      type: mcp-tool
    - name: code-generation
      type: mcp-tool
    - name: multi-modal-reasoning
      type: a2a-message
  slo:
    latency: 20000        # 20s max (Gemini can be slow for complex tasks)
    accuracy: 93.0        # 93% accuracy target
    cost: 0.10            # $0.10 per invocation budget
  resources:
    maxConcurrentMissions: 8
    maxMemoryMB: 2048
  deployment:
    strategy: canary
    canaryPercent: 10
    maxInstances: 5
    drainTimeout: 120s
```

Apply it:

```bash
apctl agent apply -f agents/gemini-analyst.yaml

# Verify registration
apctl agent describe gemini-analyst
```

### Step 6: Submit a Mission to Gemini

```bash
cat > /tmp/gemini-mission.json << 'EOF'
{
  "requiredCapabilities": ["data-analysis"],
  "priority": 1,
  "teamId": "data-engineering",
  "projectId": "quarterly-report",
  "payload": {
    "goal": "Analyze Q4 sales data and identify top 3 growth opportunities",
    "context": {
      "data_source": "sales_db",
      "time_range": "2025-10-01 to 2025-12-31",
      "format": "executive summary with bullet points"
    }
  },
  "timeout": "120s"
}
EOF

apctl mission submit -f /tmp/gemini-mission.json
```

### Step 7: Verify Execution

```bash
# Check mission was assigned to the Gemini agent
apctl mission status <mission-id> --output json

# Expected output includes:
# {
#   "agentId": "...",
#   "agentRuntime": "gemini",
#   "status": "completed",
#   "tokensUsed": 1420,
#   "latencyMs": 8500
# }

# Check cost was recorded
apctl fleet status
```

### Step 8: Set Budget Policies for Gemini

Gemini Enterprise can be expensive. Set budget controls:

```yaml
apiVersion: agentplane.io/v1
kind: Policy
metadata:
  name: gemini-budget-policy
  scope: team
  teamId: data-engineering
spec:
  rules:
    - id: gemini-cost-cap
      type: budget
      condition: "request.estimatedCost <= 50.00"
      effect: allow
    - id: gemini-runtime-only
      type: runtime
      condition: "agent.runtime == 'gemini'"
      effect: allow
```

```bash
apctl policy apply -f policies/gemini-budget-policy.yaml
```

### Available Gemini Models

| Model | Use Case | Speed | Cost |
|-------|----------|-------|------|
| `gemini-2.5-pro` | Complex reasoning, code, analysis | Medium | Higher |
| `gemini-2.5-flash` | Fast responses, simple tasks | Fast | Lower |
| `gemini-2.0-flash` | Legacy, stable | Fast | Lower |

Change model via:
```bash
export AGENTPLANE_GEMINI_MODEL="gemini-2.5-flash"
```

### Multi-Agent Orchestration with Gemini

Register multiple specialized Gemini agents:

```bash
# Agent 1: Fast, cheap (flash model for simple tasks)
cat > agents/gemini-fast.yaml << 'EOF'
apiVersion: agentplane.io/v1
kind: Agent
metadata:
  name: gemini-fast-responder
  namespace: support
  version: "1.0.0"
  labels:
    model: gemini-2.5-flash
    tier: standard
spec:
  runtime: gemini
  capabilities:
    - name: quick-answer
      type: mcp-tool
    - name: classification
      type: mcp-tool
  slo:
    latency: 3000
    accuracy: 85.0
    cost: 0.02
  resources:
    maxConcurrentMissions: 20
    maxMemoryMB: 512
  deployment:
    strategy: immediate
    maxInstances: 10
    drainTimeout: 30s
EOF

# Agent 2: Powerful, expensive (pro model for complex analysis)
cat > agents/gemini-deep.yaml << 'EOF'
apiVersion: agentplane.io/v1
kind: Agent
metadata:
  name: gemini-deep-analyst
  namespace: research
  version: "1.0.0"
  labels:
    model: gemini-2.5-pro
    tier: premium
spec:
  runtime: gemini
  capabilities:
    - name: deep-research
      type: mcp-tool
    - name: multi-document-synthesis
      type: mcp-tool
    - name: code-audit
      type: mcp-tool
  slo:
    latency: 60000
    accuracy: 96.0
    cost: 0.50
  resources:
    maxConcurrentMissions: 3
    maxMemoryMB: 4096
  deployment:
    strategy: canary
    canaryPercent: 5
    maxInstances: 2
    drainTimeout: 300s
EOF

apctl agent apply -f agents/gemini-fast.yaml
apctl agent apply -f agents/gemini-deep.yaml
```

The Scheduler automatically routes missions to the right Gemini agent based on required capabilities and weighted scoring.

### Monitoring Gemini Usage

```bash
# Real-time fleet status (shows Gemini agents + SLO compliance)
apctl fleet status

# Cost breakdown by agent
curl -s "http://localhost:8080/api/v1/costs?agentId=gemini-analyst" \
  -H "Authorization: Bearer $AGENTPLANE_TOKEN" | jq .

# Dashboard view
open http://localhost:8080
# → Cost Attribution panel shows Gemini spending per team/project
```

### Troubleshooting Gemini

| Symptom | Cause | Fix |
|---------|-------|-----|
| `dispatch: gemini adapter enabled` not in logs | Missing config | Set `AGENTPLANE_GEMINI_API_KEY` or Vertex AI vars |
| `gemini API error 401` | Invalid API key | Regenerate key at [AI Studio](https://aistudio.google.com/apikey) |
| `gemini API error 403` | API not enabled | Run `gcloud services enable generativelanguage.googleapis.com` |
| `gemini API error 429` | Rate limited | Adapter retries automatically (3x with backoff). Reduce `maxConcurrentMissions` |
| `gemini API error 500/503` | Transient server error | Automatic retry. Circuit breaker opens if persistent |
| Circuit breaker opens | >50% error rate over 5 min | Check GCP quota, API status. Breaker auto-recovers after cooldown |
| High costs | Unexpected token usage | Set budget policy, reduce `maxConcurrentMissions`, use `gemini-2.5-flash` |

---

## Troubleshooting

### Server won't start

```bash
# Check logs
agentplane --config config.yaml 2>&1 | head -50

# Common issues:
# - Port already in use → change httpAddr/grpcAddr
# - SQLite permission denied → check directory write permissions
# - PostgreSQL connection refused → verify DSN and network
```

### CLI can't connect

```bash
# Verify endpoint
curl -v http://localhost:8080/health

# Check config
echo $AGENTPLANE_ENDPOINT
echo $AGENTPLANE_TOKEN

# Test auth
curl -H "Authorization: Bearer $AGENTPLANE_TOKEN" http://localhost:8080/api/v1/agents
```

### Agent registration fails

```bash
# Validate manifest locally
apctl agent apply -f manifest.yaml --output json

# Common errors:
# - "name must be 1-253 lowercase alphanumeric" → fix naming
# - "version must be valid semver" → use X.Y.Z format
# - "capabilities must have 1-20 entries" → adjust list
# - "manifest exceeds 1MB" → reduce content
```

### Circuit breaker keeps opening

```bash
# Check agent error rate
curl -s "http://localhost:8080/api/v1/agents/<agent-id>" \
  -H "Authorization: Bearer $AGENTPLANE_TOKEN" | jq .circuitBreaker

# Increase cooldown if recovering slowly
# In config.yaml:
# circuitBreakerCooldown: 120  # seconds
```

### Migration fails

```bash
# Re-run is safe (source unchanged on failure)
apctl migrate --source sqlite --target postgres

# Check which table failed in output
# Fix data issues, then retry
```
