/** Matches internal/domain/agent.go */

export type RuntimeType = 'claude' | 'kiro' | 'bedrock' | 'custom';

export type AgentStatus =
  | 'active'
  | 'inactive'
  | 'draining'
  | 'deprecated'
  | 'unhealthy'
  | 'idle-safe';

export interface Capability {
  name: string;
  type: 'mcp-tool' | 'a2a-message';
}

export interface ResourceLimits {
  maxConcurrentMissions: number;
  maxMemoryMB: number;
}

export interface DeploymentStrategy {
  type: 'rolling' | 'canary' | 'immediate';
  canaryPercent: number;
  maxInstances: number;
  drainTimeout: string; // duration string e.g. "300s"
}

export interface SLODefinition {
  latency?: { maxMs: number };
  accuracy?: { minPercent: number };
  cost?: { maxCost: number };
}

export interface AgentEntry {
  id: string;
  name: string;
  namespace: string;
  version: string;
  runtimeType: RuntimeType;
  capabilities: Capability[];
  labels: Record<string, string>;
  slos: SLODefinition;
  resources: ResourceLimits;
  deployment: DeploymentStrategy;
  status: AgentStatus;
  createdAt: string;
  updatedAt: string;
}

export interface AgentFilter {
  name?: string;
  capability?: string;
  label?: Record<string, string>;
  status?: AgentStatus;
  runtime?: RuntimeType;
  limit?: number;
  offset?: number;
}
