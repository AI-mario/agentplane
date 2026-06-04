/** Matches internal/domain/cost.go */

export interface CostRecord {
  id: string;
  missionId: string;
  agentId: string;
  teamId: string;
  projectId: string;
  tokenCount: number;
  computeTimeMs: number;
  toolInvocations: number;
  totalCost: number;
  recordedAt: string;
}

export interface CostFilter {
  teamId?: string;
  agentId?: string;
  projectId?: string;
  startTime?: string;
  endTime?: string;
  limit?: number;
}

export interface CostAggregation {
  totalCost: number;
  totalTokens: number;
  totalComputeMs: number;
  totalToolCalls: number;
  recordCount: number;
}

export interface CostReport {
  records: CostRecord[];
  total: number;
}

export interface BudgetStatus {
  teamId: string;
  budgetCap: number;
  accumulatedCost: number;
  utilizationPct: number;
  periodStart: string;
  periodEnd: string;
  isBlocked: boolean;
}
