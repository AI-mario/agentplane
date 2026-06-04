import { useState, useCallback } from 'react';
import { usePolling } from '@/hooks/usePolling';
import { api } from '@/lib/api';
import type { AgentEntry } from '@/types';
import { ConnectivityBanner } from '@/components/ConnectivityBanner';

/**
 * Agent filtering: search by name, tag, capability, status.
 * Max 100 results, input max 256 chars (Requirement 13.5).
 */
export function AgentsPage() {
  const [search, setSearch] = useState('');
  const [statusFilter, setStatusFilter] = useState('');

  const buildQuery = useCallback(() => {
    const params = new URLSearchParams();
    params.set('limit', '100');
    if (search) params.set('name', search);
    if (statusFilter) params.set('status', statusFilter);
    return params.toString();
  }, [search, statusFilter]);

  const fetcher = useCallback(
    () => api.get<AgentEntry[]>(`/agents?${buildQuery()}`),
    [buildQuery],
  );

  const { data, error, lastUpdated, isStale } = usePolling(fetcher, 5000);

  return (
    <div className="page agents-page">
      <h2>Agents</h2>
      {isStale && <ConnectivityBanner lastUpdated={lastUpdated} error={error} />}

      <div className="controls">
        <input
          type="text"
          placeholder="Search by name, tag, or capability…"
          value={search}
          onChange={(e) => setSearch(e.target.value.slice(0, 256))}
          maxLength={256}
          aria-label="Search agents"
        />
        <select
          value={statusFilter}
          onChange={(e) => setStatusFilter(e.target.value)}
          aria-label="Filter by status"
        >
          <option value="">All statuses</option>
          <option value="active">Active</option>
          <option value="inactive">Inactive</option>
          <option value="unhealthy">Unhealthy</option>
          <option value="draining">Draining</option>
          <option value="deprecated">Deprecated</option>
          <option value="idle-safe">Idle Safe</option>
        </select>
      </div>

      <table className="data-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Version</th>
            <th>Runtime</th>
            <th>Status</th>
            <th>Capabilities</th>
            <th>Namespace</th>
          </tr>
        </thead>
        <tbody>
          {(data ?? []).map((agent) => (
            <tr key={agent.id}>
              <td>{agent.name}</td>
              <td>{agent.version}</td>
              <td>{agent.runtimeType}</td>
              <td className={agent.status === 'unhealthy' ? 'stat-warning' : ''}>
                {agent.status}
              </td>
              <td>{agent.capabilities.map((c) => c.name).join(', ')}</td>
              <td>{agent.namespace}</td>
            </tr>
          ))}
          {data?.length === 0 && (
            <tr>
              <td colSpan={6}>No agents found</td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}
