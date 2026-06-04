/** Matches internal/domain/mission.go */

export type MissionStatus =
  | 'pending'
  | 'assigned'
  | 'running'
  | 'completed'
  | 'failed'
  | 'cancelled'
  | 'queued';

export type MissionOutcome = 'success' | 'failure' | 'partial' | 'timeout';

export interface ScoringWeights {
  cost: number;
  latency: number;
  load: number;
}

export interface SelectionRationale {
  costScore: number;
  latencyScore: number;
  loadScore: number;
  weights: ScoringWeights;
}

export interface Mission {
  id: string;
  agentId: string;
  teamId: string;
  projectId: string;
  status: MissionStatus;
  requiredCapabilities: string[];
  payload: unknown;
  assignmentRationale?: SelectionRationale;
  priority: number;
  timeout: string;
  submittedAt: string;
  assignedAt?: string;
  completedAt?: string;
}
