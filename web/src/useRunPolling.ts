import { useCallback, useEffect, useRef, useState } from 'react';
import { ApiError, type Api, type RunAck, type RunView } from './api';
import { isTerminalRun } from './format';
import { createRunStream, isNotOlder, type RunStream, type StreamMode } from './runStream';

export const retryable = (error: unknown) => {
  if (!(error instanceof ApiError)) return false;
  if (error.kind === 'network' || error.kind === 'invalid_response') return true;
  return error.retryable && ![401, 403, 404].includes(error.status);
};

// Run status from the event stream when the browser has EventSource, otherwise (or when the
// stream fails) by polling GET /runs/{id}. Either way the shown state is a whole run view.
export function useRunPolling(api: Api, runId: string) {
  const [run, setRun] = useState<RunView | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [stopped, setStopped] = useState(false);
  // Client time of the last answer from the server: shown as "last confirmed".
  const [confirmedAt, setConfirmedAt] = useState<Date | null>(null);
  const [mode, setMode] = useState<StreamMode>('stopped');
  const [hidden, setHidden] = useState(() => typeof document !== 'undefined' && document.visibilityState === 'hidden');
  const stream = useRef<RunStream | null>(null);

  useEffect(() => {
    setRun(null);
    setConfirmedAt(null);
    setError(null);
    setStopped(false);
    const open = api.openRunEvents;
    const s = createRunStream({
      fetch: signal => api.getRun(runId, signal),
      open: open === null ? null : cursor => open(runId, cursor),
      onView: value => {
        setError(null);
        setConfirmedAt(new Date());
        // A slow older response must not overwrite a newer state.
        setRun(prev => (isNotOlder(prev, value) ? value : prev));
        if (isTerminalRun(value.status)) setStopped(true);
      },
      onConfirmed: at => setConfirmedAt(at),
      onError: (e, willRetry) => {
        setError(e);
        if (!willRetry) setStopped(true);
      },
      onMode: setMode,
      retryable,
    });
    stream.current = s;
    s.start();
    return () => {
      s.stop();
      stream.current = null;
    };
  }, [api, runId]);

  useEffect(() => {
    const onChange = () => setHidden(document.visibilityState === 'hidden');
    document.addEventListener('visibilitychange', onChange);
    return () => document.removeEventListener('visibilitychange', onChange);
  }, []);

  // Fold in the acknowledgement of a mutation (cancel) without waiting for the next update.
  const applyAck = useCallback((ack: RunAck) => {
    setRun(prev => (prev && prev.id === ack.id && ack.version >= prev.version ? { ...prev, status: ack.status, version: ack.version } : prev));
    stream.current?.refresh();
  }, []);
  const refresh = useCallback(() => {
    const s = stream.current;
    if (!s) return;
    setStopped(false);
    s.refresh();
  }, []);

  return { run, error, stopped, hidden, confirmedAt, mode, applyAck, refresh };
}
