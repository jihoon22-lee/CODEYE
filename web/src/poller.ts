// Visibility-aware polling with error backoff. It never overlaps requests, stops
// when asked, and drops results that arrive after stop().

export type PollerOptions<T> = {
  fetch: (signal: AbortSignal) => Promise<T>;
  onData: (value: T) => void;
  onError: (error: unknown, willRetry: boolean) => void;
  done: (value: T) => boolean;
  retryable: (error: unknown) => boolean;
  intervalMs?: number;
  maxBackoffMs?: number;
  visible?: () => boolean;
};

export function createPoller<T>(o: PollerOptions<T>) {
  const interval = o.intervalMs ?? 5000;
  const maxBackoff = o.maxBackoffMs ?? 60_000;
  const visible = o.visible ?? (() => typeof document === 'undefined' || document.visibilityState !== 'hidden');
  let stopped = true;
  let inflight: AbortController | null = null;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let failures = 0;

  function schedule(ms: number) {
    clearTimeout(timer);
    timer = setTimeout(tick, ms);
  }
  async function tick() {
    clearTimeout(timer);
    timer = undefined;
    if (stopped || inflight) return;
    if (!visible()) return; // resume() restarts when the tab is shown again
    const controller = new AbortController();
    inflight = controller;
    try {
      const value = await o.fetch(controller.signal);
      if (stopped || controller !== inflight) return;
      failures = 0;
      o.onData(value);
      if (o.done(value)) { stopped = true; return; }
      schedule(interval);
    } catch (error) {
      if (stopped || controller !== inflight || controller.signal.aborted) return;
      const retry = o.retryable(error);
      o.onError(error, retry);
      if (!retry) { stopped = true; return; }
      failures += 1;
      schedule(Math.min(interval * 2 ** failures, maxBackoff));
    } finally {
      if (inflight === controller) inflight = null;
    }
  }
  const onVisibility = () => {
    if (stopped) return;
    if (visible()) { if (!inflight) void tick(); }
    else { clearTimeout(timer); timer = undefined; }
  };
  return {
    start() {
      if (!stopped) return;
      stopped = false;
      failures = 0;
      if (typeof document !== 'undefined') document.addEventListener('visibilitychange', onVisibility);
      void tick();
    },
    // Fetch now (for example after a cancel request) unless a request is already running.
    refresh() { if (!stopped && !inflight) void tick(); },
    stop() {
      stopped = true;
      clearTimeout(timer);
      timer = undefined;
      inflight?.abort();
      inflight = null;
      if (typeof document !== 'undefined') document.removeEventListener('visibilitychange', onVisibility);
    },
    isRunning: () => !stopped,
  };
}

export type Poller<T> = ReturnType<typeof createPoller<T>>;
