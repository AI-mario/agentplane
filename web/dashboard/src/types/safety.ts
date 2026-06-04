/** Matches internal/domain/safety.go */

export type CBState = 'closed' | 'open' | 'half_open';

export interface CircuitBreakerState {
  agentId: string;
  state: CBState;
  errorRate: number;
  openedAt?: string;
  cooldownEnd?: string;
  cooldownSecs: number;
}
