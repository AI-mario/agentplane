/**
 * Connectivity error indicator showing last data staleness (Requirement 13.7).
 */
interface Props {
  lastUpdated: Date | null;
  error: Error | null;
}

export function ConnectivityBanner({ lastUpdated, error }: Props) {
  return (
    <div className="connectivity-banner" role="alert">
      <span className="connectivity-icon" aria-hidden="true">⚠️</span>
      <span>
        Unable to reach backend
        {error && `: ${error.message}`}
        {lastUpdated && ` — showing data from ${lastUpdated.toLocaleTimeString()}`}
      </span>
    </div>
  );
}
