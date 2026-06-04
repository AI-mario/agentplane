/** Matches internal/domain/trace.go */

export type StepStatus = 'success' | 'failure' | 'skipped';

export type MissionOutcome = 'success' | 'failure' | 'partial' | 'timeout';

export interface TraceStep {
  spanId: string;
  traceId: string;
  parentSpanId: string;
  name: string;
  startTime: string;
  duration: number; // milliseconds
  status: StepStatus;
  attributes: Record<string, string>;
}

export interface MissionTrace {
  traceId: string;
  missionId: string;
  agentId: string;
  goal: string;
  steps: TraceStep[];
  outcome: MissionOutcome;
  startTime: string;
  endTime?: string;
  duration: number; // milliseconds
}

export interface DriftAlert {
  agentId: string;
  metricName: string;
  observedValue: number;
  baselineValue: number;
  deviation: number;
  detectedAt: string;
}
