/** Fleet status overview response from GET /api/v1/fleet/status */

import type { AgentEntry } from './agent';
import type { CircuitBreakerState } from './safety';
import type { SLOCompliance } from './slo';

export interface FleetStatus {
  totalAgents: number;
  activeAgents: number;
  unhealthyAgents: number;
  activeMissions: number;
  queuedMissions: number;
  agents: AgentEntry[];
  circuitBreakers: CircuitBreakerState[];
  sloCompliance: SLOCompliance[];
  killSwitchActive: boolean;
}
