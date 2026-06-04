import { useAlerts } from '@/hooks/useAlerts';

/**
 * Alert notifications panel: displays SLO breach/drift alerts within 3s,
 * persists until dismissed or resolved (Requirement 13.4).
 */
export function AlertsPanel() {
  const { alerts, dismiss } = useAlerts();

  if (alerts.length === 0) return null;

  return (
    <div className="alerts-panel" role="region" aria-label="Alerts">
      <h3>Alerts ({alerts.length})</h3>
      <ul className="alerts-list">
        {alerts.map((alert) => (
          <li key={alert.id} className={`alert-item alert-item--${alert.type}`}>
            <span className="alert-message">{alert.message}</span>
            <span className="alert-time">
              {new Date(alert.detectedAt).toLocaleTimeString()}
            </span>
            <button
              className="alert-dismiss"
              onClick={() => dismiss(alert.id)}
              aria-label={`Dismiss alert: ${alert.message}`}
            >
              ✕
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}
