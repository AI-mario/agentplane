/** Matches internal/domain/slo.go */

export interface SLOStatus {
  agentId: string;
  violationStart?: string;
  violationMinutes: number;
  currentMetrics: Record<string, number>;
  trafficReduction: number;
}

export interface SLOCompliance {
  agentId: string;
  window: string;
  compliancePct: number;
  lastCalculated: string;
}
