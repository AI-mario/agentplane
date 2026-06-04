import { useState, useEffect, useRef, useCallback } from 'react';

interface PollingState<T> {
  data: T | null;
  error: Error | null;
  lastUpdated: Date | null;
  isStale: boolean;
}

/**
 * Hook that polls an async fetcher at a given interval.
 * Tracks connectivity errors and staleness per Requirement 13.7.
 */
export function usePolling<T>(
  fetcher: () => Promise<T>,
  intervalMs: number,
): PollingState<T> & { refresh: () => void } {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<Error | null>(null);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  const [isStale, setIsStale] = useState(false);
  const timerRef = useRef<ReturnType<typeof setInterval>>();

  const doFetch = useCallback(async () => {
    try {
      const result = await fetcher();
      setData(result);
      setError(null);
      setLastUpdated(new Date());
      setIsStale(false);
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)));
      setIsStale(true);
    }
  }, [fetcher]);

  useEffect(() => {
    doFetch();
    timerRef.current = setInterval(doFetch, intervalMs);
    return () => {
      if (timerRef.current) clearInterval(timerRef.current);
    };
  }, [doFetch, intervalMs]);

  return { data, error, lastUpdated, isStale, refresh: doFetch };
}
