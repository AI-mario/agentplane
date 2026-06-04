import { useCallback } from 'react';
import { usePolling } from '@/hooks/usePolling';
import { api } from '@/lib/api';
import type { FleetStatus } from '@/types';
import { ConnectivityBanner } from '@/components/ConnectivityBanner';

/**
 * Fleet status view: agent health, active missions, SLO compliance.
 * Refreshes every 5 seconds (Requirement 13.1).
 */
export function FleetStatusPage() {
  const fetcher = useCallback(() => api.get<FleetStatus>('/fleet/status'), []);
  const { data, error, lastUpdated, isStale } = usePolling(fetcher, 5000);

  return (
    <div className="page fleet-status">
      <h2>Fleet Status</h2>
      {isStale && <ConnectivityBanner lastUpdated={lastUpdated} error={error} />}

      {data && (
        <>
          <div className="stats-grid">
            <div className="stat-card">
              <span className="stat-label">Total Agents</span>
              <span className="stat-value">{data.totalAgents}</span>
            </div>
            <div className="stat-card">
              <span className="stat-label">Active</span>
              <span className="stat-value">{data.activeAgents}</span>
            </div>
            <div className="stat-card">
              <span className="stat-label">Unhealthy</span>
              <span className="stat-value stat-warning">{data.unhealthyAgents}</span>
            </div>
            <div className="stat-card">
              <span className="stat-label">Active Missions</span>
              <span className="stat-value">{data.activeMissions}</span>
            </div>
            <div className="stat-card">
              <span className="stat-label">Queued</span>
              <span className="stat-value">{data.queuedMissions}</span>
            </div>
            <div className="stat-card">
              <span className="stat-label">Kill Switch</span>
              <span className={`stat-value ${data.killSwitchActive ? 'stat-danger' : ''}`}>
                {data.killSwitchActive ? 'ACTIVE' : 'Off'}
              </span>
            </div>
          </div>

          <h3>SLO Compliance</h3>
          <table className="data-table">
            <thead>
              <tr>
                <th>Agent</th>
                <th>Compliance %</th>
                <th>Last Calculated</th>
              </tr>
            </thead>
            <tbody>
              {data.sloCompliance.map((slo) => (
                <tr key={slo.agentId}>
                  <td>{slo.agentId}</td>
                  <td className={slo.compliancePct < 95 ? 'stat-warning' : ''}>
                    {slo.compliancePct.toFixed(1)}%
                  </td>
                  <td>{new Date(slo.lastCalculated).toLocaleTimeString()}</td>
                </tr>
              ))}
            </tbody>
          </table>

          <h3>Circuit Breakers</h3>
          <table className="data-table">
            <thead>
              <tr>
                <th>Agent</th>
                <th>State</th>
                <th>Error Rate</th>
              </tr>
            </thead>
            <tbody>
              {data.circuitBreakers
                .filter((cb) => cb.state !== 'closed')
                .map((cb) => (
                  <tr key={cb.agentId}>
                    <td>{cb.agentId}</td>
                    <td className="stat-warning">{cb.state}</td>
                    <td>{(cb.errorRate * 100).toFixed(1)}%</td>
                  </tr>
                ))}
            </tbody>
          </table>
        </>
      )}
    </div>
  );
}
