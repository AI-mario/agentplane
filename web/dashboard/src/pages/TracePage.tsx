import { useCallback, useState } from 'react';
import { api } from '@/lib/api';
import type { MissionTrace } from '@/types';

/**
 * Mission trace timeline: visual rendering of goal → steps (up to 200) → outcome.
 * (Requirement 13.3)
 */
export function TracePage() {
  const [missionId, setMissionId] = useState('');
  const [trace, setTrace] = useState<MissionTrace | null>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  const loadTrace = useCallback(async () => {
    if (!missionId.trim()) return;
    setLoading(true);
    setError('');
    try {
      const data = await api.get<MissionTrace>(`/missions/${missionId}/trace`);
      setTrace(data);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load trace');
      setTrace(null);
    } finally {
      setLoading(false);
    }
  }, [missionId]);

  return (
    <div className="page trace-page">
      <h2>Mission Trace</h2>

      <div className="controls">
        <label htmlFor="mission-id">Mission ID:</label>
        <input
          id="mission-id"
          type="text"
          value={missionId}
          onChange={(e) => setMissionId(e.target.value)}
          placeholder="Enter mission ID"
          maxLength={256}
        />
        <button onClick={loadTrace} disabled={loading}>
          {loading ? 'Loading…' : 'Load Trace'}
        </button>
      </div>

      {error && <div className="error" role="alert">{error}</div>}

      {trace && (
        <div className="trace-view">
          <div className="trace-header">
            <h3>{trace.goal}</h3>
            <div className="trace-meta">
              <span>Agent: {trace.agentId}</span>
              <span>Outcome: <strong className={`outcome-${trace.outcome}`}>{trace.outcome}</strong></span>
              <span>Duration: {trace.duration}ms</span>
              <span>Steps: {trace.steps.length}</span>
            </div>
          </div>

          <div className="trace-timeline" role="list" aria-label="Mission steps timeline">
            {trace.steps.slice(0, 200).map((step, i) => (
              <div
                key={step.spanId}
                className={`trace-step trace-step--${step.status}`}
                role="listitem"
              >
                <div className="step-index">{i + 1}</div>
                <div className="step-content">
                  <span className="step-name">{step.name}</span>
                  <span className="step-duration">{step.duration}ms</span>
                  <span className={`step-status step-status--${step.status}`}>{step.status}</span>
                </div>
                <div
                  className="step-bar"
                  style={{ width: `${Math.min((step.duration / trace.duration) * 100, 100)}%` }}
                />
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
