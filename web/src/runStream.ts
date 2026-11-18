// Run status over the W22 event stream (GET /runs/{id}/events) with polling as the fallback.
//
// The stream is an invalidation channel: event rows only move the cursor, and the
// state shown is always a whole run view (the run.view event or GET /runs/{id}),
// accepted only when it is not older than the one shown. The hook owns
// reconnection: it closes the EventSource on resync, closed, error, terminal
// status, hidden tab and stop, and reopens from a fresh GET's event_sequence, so
// the browser never retries a stale cursor on its own. Heartbeats only confirm
// that the server is still checking; they never change progress.
import { parseRun, type EventSourceLike, type RunView } from './api';
import { isTerminalRun } from './format';
import { createPoller, type Poller } from './poller';

// polling: no EventSource (or a server without event_sequence); fallback: the stream failed.
export type StreamMode = 'connecting' | 'live' | 'polling' | 'fallback' | 'paused' | 'stopped';

export type RunStreamOptions = {
  fetch: (signal: AbortSignal) => Promise<RunView>;
  open: ((cursor: number) => EventSourceLike) | null;
  onView: (view: RunView) => void;
  onError: (error: unknown, willRetry: boolean) => void;
  onMode?: (mode: StreamMode) => void;
  // A heartbeat or run.view arrived: the server checked the run at this time.
  onConfirmed?: (at: Date) => void;
  retryable: (error: unknown) => boolean;
  intervalMs?: number; // polling, and the GET while a streamed run is queued (default 5000)
  watchdogMs?: number; // no stream message for this long counts as a failure (default 45000)
  reopenMs?: number; // first delay before reopening a failed stream (default 30000, doubling)
  maxReopens?: number; // stream attempts after failures before polling only (default 3)
  maxResyncs?: number; // consecutive resyncs before falling back (default 3)
  visible?: () => boolean;
};

// A view replaces the shown one unless it is older: by event_sequence when both have it, then by version.
export function isNotOlder(prev: RunView | null, next: RunView): boolean {
  if (!prev || prev.id !== next.id) return true;
  if (prev.event_sequence !== undefined && next.event_sequence !== undefined && next.event_sequence < prev.event_sequence) return false;
  return next.version >= prev.version;
}

export function createRunStream(o: RunStreamOptions) {
  const interval = o.intervalMs ?? 5000;
  const watchdogMs = o.watchdogMs ?? 45_000;
  const reopenMs = o.reopenMs ?? 30_000;
  const maxReopens = o.maxReopens ?? 3;
  const maxResyncs = o.maxResyncs ?? 3;
  const visible = o.visible ?? (() => typeof document === 'undefined' || document.visibilityState !== 'hidden');

  let stopped = true;
  let mode: StreamMode = 'stopped';
  let shown: RunView | null = null;
  let es: EventSourceLike | null = null;
  let cursor = 0;
  let poller: Poller<RunView> | null = null;
  let inflight: AbortController | null = null;
  let watchdog: ReturnType<typeof setTimeout> | undefined;
  let reopenTimer: ReturnType<typeof setTimeout> | undefined;
  let reconcileTimer: ReturnType<typeof setTimeout> | undefined;
  let failures = 0; // stream failures since the last good message
  let resyncs = 0;

  const setMode = (m: StreamMode) => { if (mode !== m) { mode = m; o.onMode?.(m); } };

  function deliver(view: RunView) {
    if (!isNotOlder(shown, view)) return;
    shown = view;
    o.onView(view);
    if (isTerminalRun(view.status)) finish();
  }

  function finish() {
    stopAll();
    setMode('stopped');
  }

  function closeStream() {
    clearTimeout(watchdog);
    clearTimeout(reconcileTimer);
    watchdog = reconcileTimer = undefined;
    if (es) {
      es.onopen = es.onmessage = es.onerror = null;
      es.close();
      es = null;
    }
  }

  function stopAll() {
    stopped = true;
    closeStream();
    clearTimeout(reopenTimer);
    reopenTimer = undefined;
    inflight?.abort();
    inflight = null;
    poller?.stop();
    poller = null;
    if (typeof document !== 'undefined') document.removeEventListener('visibilitychange', onVisibility);
  }

  function startPolling(m: StreamMode) {
    closeStream();
    setMode(m);
    if (poller) return;
    poller = createPoller<RunView>({
      fetch: o.fetch,
      onData: deliver,
      onError: (e, willRetry) => {
        o.onError(e, willRetry);
        if (!willRetry) { stopAll(); setMode('stopped'); }
      },
      done: v => isTerminalRun(v.status),
      retryable: o.retryable,
      intervalMs: interval,
      visible,
    });
    poller.start();
  }

  // A failed stream: poll now and try the stream again later, a bounded number of times.
  function fail() {
    failures += 1;
    startPolling('fallback');
    clearTimeout(reopenTimer);
    if (failures <= maxReopens) reopenTimer = setTimeout(() => { reopenTimer = undefined; connect(); }, reopenMs * 2 ** (failures - 1));
  }

  // GET the run (version-guarded), then stream from its event_sequence.
  function connect() {
    if (stopped || !o.open) return;
    if (!visible()) { pause(); return; }
    closeStream();
    poller?.stop();
    poller = null;
    setMode('connecting');
    const controller = new AbortController();
    inflight?.abort();
    inflight = controller;
    o.fetch(controller.signal).then(view => {
      if (stopped || controller !== inflight) return;
      inflight = null;
      deliver(view);
      if (stopped) return; // terminal
      if (view.event_sequence === undefined) { startPolling('polling'); return; } // server without W22 streams
      openStream(view.event_sequence);
    }, error => {
      if (stopped || controller !== inflight || controller.signal.aborted) return;
      inflight = null;
      const retry = o.retryable(error);
      o.onError(error, retry);
      if (!retry) { stopAll(); setMode('stopped'); return; }
      fail();
    });
  }

  function touch() {
    clearTimeout(watchdog);
    watchdog = setTimeout(() => { watchdog = undefined; fail(); }, watchdogMs);
  }

  function parse(e: MessageEvent): Record<string, unknown> | null {
    try {
      const v: unknown = JSON.parse(String(e.data));
      return typeof v === 'object' && v !== null && !Array.isArray(v) ? v as Record<string, unknown> : null;
    } catch { return null; }
  }

  function resync() {
    closeStream();
    resyncs += 1;
    if (resyncs > maxResyncs) { fail(); return; }
    connect();
  }

  // A queued run's waiting reason is computed at read time and has no event: GET it while queued.
  function reconcileWhileQueued() {
    clearTimeout(reconcileTimer);
    reconcileTimer = undefined;
    if (!es || shown?.status !== 'queued') return;
    reconcileTimer = setTimeout(() => {
      reconcileTimer = undefined;
      if (!es) return;
      const controller = new AbortController();
      o.fetch(controller.signal).then(view => { if (es) { deliver(view); reconcileWhileQueued(); } }, error => {
        if (!es) return;
        if (!o.retryable(error)) { o.onError(error, false); stopAll(); setMode('stopped'); return; }
        reconcileWhileQueued();
      });
    }, interval);
  }

  function openStream(from: number) {
    if (!o.open) return;
    cursor = from;
    let source: EventSourceLike;
    try {
      source = o.open(from);
    } catch {
      fail();
      return;
    }
    es = source;
    touch();
    // Open means the server answered 200 text/event-stream (a 401/404/503 fires onerror instead).
    source.onopen = () => {
      if (es !== source) return;
      touch();
      setMode('live');
      reconcileWhileQueued();
    };
    source.onmessage = e => {
      if (es !== source) return;
      touch();
      const data = parse(e);
      const seq = data?.sequence;
      if (typeof seq !== 'number' || seq !== cursor + 1) { resync(); return; } // gap or garbage: never skip
      cursor = seq;
    };
    source.addEventListener('run.view', e => {
      if (es !== source) return;
      touch();
      let view: RunView;
      try {
        view = parseRun(parse(e));
      } catch { resync(); return; }
      failures = resyncs = 0;
      clearTimeout(reopenTimer);
      reopenTimer = undefined;
      setMode('live');
      o.onConfirmed?.(new Date());
      deliver(view);
      reconcileWhileQueued();
    });
    source.addEventListener('heartbeat', () => {
      if (es !== source) return;
      touch();
      failures = 0;
      setMode('live');
      o.onConfirmed?.(new Date());
    });
    source.addEventListener('state.resync', () => { if (es === source) resync(); });
    // The session ended or the run is no longer readable: one GET surfaces 401/404 as an error.
    source.addEventListener('closed', () => { if (es === source) { failures = maxReopens; fail(); } });
    source.onerror = () => { if (es === source) fail(); };
  }

  function pause() {
    closeStream();
    clearTimeout(reopenTimer);
    reopenTimer = undefined;
    inflight?.abort();
    inflight = null;
    poller?.stop();
    poller = null;
    setMode('paused');
  }

  function onVisibility() {
    if (stopped || !o.open) return;
    if (visible()) { failures = resyncs = 0; connect(); } else pause();
  }

  function start() {
    if (!stopped) return;
    stopped = false;
    failures = resyncs = 0;
    if (!o.open) { startPolling('polling'); return; } // the poller handles visibility itself
    if (typeof document !== 'undefined') document.addEventListener('visibilitychange', onVisibility);
    connect();
  }

  return {
    start,
    // Fetch now (for example after a cancel request), or start again after a stop.
    refresh() {
      if (stopped) { start(); return; }
      if (poller) { poller.refresh(); return; }
      if (!es) return;
      const controller = new AbortController();
      o.fetch(controller.signal).then(view => { if (!stopped) deliver(view); }, error => { if (!stopped) o.onError(error, o.retryable(error)); });
    },
    stop() {
      stopAll();
      setMode('stopped');
    },
    isRunning: () => !stopped,
    mode: () => mode,
  };
}

export type RunStream = ReturnType<typeof createRunStream>;
