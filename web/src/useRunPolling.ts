import { useCallback, useEffect, useRef, useState } from 'react';
import { ApiError, type Api, type RunAck, type RunView } from './api';
import { isTerminalRun } from './format';
import { createPoller, type Poller } from './poller';

const retryable = (error: unknown) => {
  if (!(error instanceof ApiError)) return false;
  if (error.kind === 'network' || error.kind === 'invalid_response') return true;
  return error.retryable && ![401, 403, 404].includes(error.status);
};

export function useRunPolling(api: Api, runId: string) {
  const [run, setRun] = useState<RunView | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [stopped, setStopped] = useState(false);
  const [hidden, setHidden] = useState(() => typeof document !== 'undefined' && document.visibilityState === 'hidden');
  const poller = useRef<Poller<RunView> | null>(null);

  useEffect(() => {
    setRun(null);
    setError(null);
    setStopped(false);
    const p = createPoller<RunView>({
      fetch: signal => api.getRun(runId, signal),
      onData: value => {
        setError(null);
        // A slow older response must not overwrite a newer state.
        setRun(prev => (prev && prev.id === value.id && prev.version > value.version ? prev : value));
        if (isTerminalRun(value.status)) setStopped(true);
      },
      onError: (e, willRetry) => {
        setError(e);
        if (!willRetry) setStopped(true);
      },
      done: value => isTerminalRun(value.status),
      retryable,
    });
    poller.current = p;
    p.start();
    return () => {
      p.stop();
      poller.current = null;
    };
  }, [api, runId]);

  useEffect(() => {
    const onChange = () => setHidden(document.visibilityState === 'hidden');
    document.addEventListener('visibilitychange', onChange);
    return () => document.removeEventListener('visibilitychange', onChange);
  }, []);

  // Fold in the acknowledgement of a mutation (cancel) without waiting for the next poll.
  const applyAck = useCallback((ack: RunAck) => {
    setRun(prev => (prev && prev.id === ack.id && ack.version >= prev.version ? { ...prev, status: ack.status, version: ack.version } : prev));
    poller.current?.refresh();
  }, []);
  const refresh = useCallback(() => {
    const p = poller.current;
    if (!p) return;
    setStopped(false);
    if (p.isRunning()) p.refresh();
    else p.start();
  }, []);

  return { run, error, stopped, hidden, applyAck, refresh };
}
