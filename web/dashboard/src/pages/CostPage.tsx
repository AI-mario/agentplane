import { useCallback, useState } from 'react';
import { usePolling } from '@/hooks/usePolling';
import { api } from '@/lib/api';
import type { CostReport, BudgetStatus } from '@/types';
import { ConnectivityBanner } from '@/components/ConnectivityBanner';

/**
 * Cost attribution view: breakdowns by team, project, agent.
 * Data max 60 seconds stale (Requirement 13.2).
 */
export function CostPage() {
  const [groupBy, setGroupBy] = useState<'team' | 'project' | 'agent'>('team');

  const costsFetcher = useCallback(() => api.get<CostReport>('/costs?limit=100'), []);
  const budgetsFetcher = useCallback(() => api.get<BudgetStatus[]>('/budgets'), []);

  const costs = usePolling(costsFetcher, 60000);
  const budgets = usePolling(budgetsFetcher, 60000);

  const isStale = costs.isStale || budgets.isStale;
  const lastUpdated = costs.lastUpdated;

  // Group records by selected dimension
  const grouped = (costs.data?.records ?? []).reduce<Record<string, number>>((acc, r) => {
    const key = groupBy === 'team' ? r.teamId : groupBy === 'project' ? r.projectId : r.agentId;
    acc[key] = (acc[key] ?? 0) + r.totalCost;
    return acc;
  }, {});

  return (
    <div className="page cost-page">
      <h2>Cost Attribution</h2>
      {isStale && <ConnectivityBanner lastUpdated={lastUpdated} error={costs.error} />}

      <div className="controls">
        <label htmlFor="group-by">Group By:</label>
        <select
          id="group-by"
          value={groupBy}
          onChange={(e) => setGroupBy(e.target.value as 'team' | 'project' | 'agent')}
        >
          <option value="team">Team</option>
          <option value="project">Project</option>
          <option value="agent">Agent</option>
        </select>
      </div>

      <h3>Breakdown by {groupBy}</h3>
      <table className="data-table">
        <thead>
          <tr>
            <th>{groupBy}</th>
            <th>Total Cost</th>
          </tr>
        </thead>
        <tbody>
          {Object.entries(grouped)
            .sort(([, a], [, b]) => b - a)
            .map(([key, cost]) => (
              <tr key={key}>
                <td>{key || '(unattributed)'}</td>
                <td>${cost.toFixed(2)}</td>
              </tr>
            ))}
        </tbody>
      </table>

      <h3>Budget Status</h3>
      {budgets.data && (
        <table className="data-table">
          <thead>
            <tr>
              <th>Team</th>
              <th>Budget</th>
              <th>Used</th>
              <th>Utilization</th>
              <th>Status</th>
            </tr>
          </thead>
          <tbody>
            {budgets.data.map((b) => (
              <tr key={b.teamId}>
                <td>{b.teamId}</td>
                <td>${b.budgetCap.toFixed(2)}</td>
                <td>${b.accumulatedCost.toFixed(2)}</td>
                <td className={b.utilizationPct >= 80 ? 'stat-warning' : ''}>
                  {b.utilizationPct.toFixed(1)}%
                </td>
                <td>{b.isBlocked ? '🚫 Blocked' : '✓ Active'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
