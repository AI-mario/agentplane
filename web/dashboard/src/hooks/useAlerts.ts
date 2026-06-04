import { useState, useCallback, useEffect, useRef } from 'react';
import { api } from '@/lib/api';
import type { DriftAlert } from '@/types/trace';

export interface Alert {
  id: string;
  type: 'slo_breach' | 'drift';
  message: string;
  agentId: string;
  detectedAt: string;
  dismissed: boolean;
}

/**
 * Hook that polls for alerts every 3 seconds (Requirement 13.4).
 * Alerts persist until dismissed or resolved.
 */
export function useAlerts() {
  const [alerts, setAlerts] = useState<Alert[]>([]);
  const timerRef = useRef<ReturnType<typeof setInterval>>();

  const fetchAlerts = useCallback(async () => {
    try {
      const driftAlerts = await api.get<DriftAlert[]>('/fleet/alerts');
      const newAlerts: Alert[] = driftAlerts.map((d) => ({
        id: `${d.agentId}-${d.metricName}-${d.detectedAt}`,
        type: 'drift',
        message: `Drift: ${d.metricName} on ${d.agentId} (observed: ${d.observedValue.toFixed(2)}, baseline: ${d.baselineValue.toFixed(2)})`,
        agentId: d.agentId,
        detectedAt: d.detectedAt,
        dismissed: false,
      }));

      setAlerts((prev) => {
        const existingIds = new Set(prev.map((a) => a.id));
        const merged = [...prev];
        for (const alert of newAlerts) {
          if (!existingIds.has(alert.id)) {
            merged.push(alert);
          }
        }
        return merged;
      });
    } catch {
      // connectivity errors handled at layout level
    }
  }, []);

  const dismiss = useCallback((id: string) => {
    setAlerts((prev) => prev.map((a) => (a.id === id ? { ...a, dismissed: true } : a)));
  }, []);

  const activeAlerts = alerts.filter((a) => !a.dismissed);

  useEffect(() => {
    fetchAlerts();
    timerRef.current = setInterval(fetchAlerts, 3000);
    return () => {
      if (timerRef.current) clearInterval(timerRef.current);
    };
  }, [fetchAlerts]);

  return { alerts: activeAlerts, dismiss };
}
