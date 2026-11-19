import { useCallback, useEffect, useState } from 'react';

// When the server gives no usable wait, the form still pauses instead of inviting a retry loop.
export const DEFAULT_WAIT_SECONDS = 30;

// A countdown to a server-given deadline (429 Retry-After). remaining is whole seconds, 0 when free.
// The deadline, not a tick count, decides: a throttled tab that wakes late is not held longer.
export function useWait(): { remaining: number; wait: (seconds: number | null) => void } {
  const [until, setUntil] = useState<number | null>(null);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (until === null) return;
    const id = setInterval(() => {
      const t = Date.now();
      setNow(t);
      if (t >= until) setUntil(null);
    }, 1000);
    return () => clearInterval(id);
  }, [until]);
  const wait = useCallback((seconds: number | null) => {
    const t = Date.now();
    setNow(t);
    setUntil(t + Math.max(1, seconds ?? DEFAULT_WAIT_SECONDS) * 1000);
  }, []);
  const remaining = until === null ? 0 : Math.max(0, Math.ceil((until - now) / 1000));
  return { remaining, wait };
}
