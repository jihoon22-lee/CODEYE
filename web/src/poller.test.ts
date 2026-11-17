// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createPoller } from './poller';

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

function setup(over: Partial<Parameters<typeof createPoller<number>>[0]> = {}) {
  const fetch = vi.fn<(signal: AbortSignal) => Promise<number>>(over.fetch ?? (async () => 1));
  const onData = vi.fn();
  const onError = vi.fn();
  const poller = createPoller<number>({ onData, onError, done: v => v >= 100, retryable: () => true, ...over, fetch });
  return { fetch, onData, onError, poller };
}
const advance = (ms: number) => vi.advanceTimersByTimeAsync(ms);

describe('createPoller', () => {
  it('fetches at once, then every 5 seconds', async () => {
    const { fetch, poller } = setup();
    poller.start();
    await advance(0);
    expect(fetch).toHaveBeenCalledTimes(1);
    await advance(4999);
    expect(fetch).toHaveBeenCalledTimes(1);
    await advance(1);
    expect(fetch).toHaveBeenCalledTimes(2);
    poller.stop();
  });

  it('backs off exponentially after errors, caps the delay, and resets after a success', async () => {
    let fail = true;
    const { fetch, onError, poller } = setup({ fetch: vi.fn(async () => { if (fail) throw new Error('x'); return 1; }) });
    poller.start();
    await advance(0);
    const delays: number[] = [];
    for (const expected of [10_000, 20_000, 40_000, 60_000, 60_000]) {
      const before = fetch.mock.calls.length;
      await advance(expected - 1);
      expect(fetch.mock.calls.length).toBe(before);
      await advance(1);
      delays.push(expected);
    }
    expect(delays).toHaveLength(5);
    expect(onError).toHaveBeenCalledTimes(6);
    fail = false;
    await advance(60_000);
    const calls = fetch.mock.calls.length;
    await advance(4999);
    expect(fetch.mock.calls.length).toBe(calls);
    await advance(1);
    expect(fetch.mock.calls.length).toBe(calls + 1); // back to the normal interval
    poller.stop();
  });

  it('stops by itself when the value is final', async () => {
    const { fetch, onData, poller } = setup({ fetch: vi.fn(async () => 100) });
    poller.start();
    await advance(60_000);
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(onData).toHaveBeenCalledWith(100);
    expect(poller.isRunning()).toBe(false);
  });

  it('stops on a non-retryable error and says so', async () => {
    const { fetch, onError, poller } = setup({ fetch: vi.fn(async () => { throw new Error('404'); }), retryable: () => false });
    poller.start();
    await advance(120_000);
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(onError).toHaveBeenCalledWith(expect.any(Error), false);
    expect(poller.isRunning()).toBe(false);
  });

  it('never overlaps requests', async () => {
    let release!: (v: number) => void;
    const { fetch, poller } = setup({ fetch: vi.fn(() => new Promise<number>(r => { release = r; })) });
    poller.start();
    await advance(0);
    poller.refresh();
    poller.refresh();
    await advance(30_000);
    expect(fetch).toHaveBeenCalledTimes(1);
    release(1);
    await advance(0);
    poller.stop();
  });

  it('aborts the request and ignores its late result after stop', async () => {
    let release!: (v: number) => void;
    let signal!: AbortSignal;
    const { onData, poller } = setup({ fetch: vi.fn((s: AbortSignal) => { signal = s; return new Promise<number>(r => { release = r; }); }) });
    poller.start();
    await advance(0);
    poller.stop();
    expect(signal.aborted).toBe(true);
    release(1);
    await advance(10_000);
    expect(onData).not.toHaveBeenCalled();
  });

  it('pauses while hidden and resumes immediately when visible again', async () => {
    let hidden = false;
    const { fetch, poller } = setup({ visible: () => !hidden });
    poller.start();
    await advance(0);
    expect(fetch).toHaveBeenCalledTimes(1);
    hidden = true;
    document.dispatchEvent(new Event('visibilitychange'));
    await advance(60_000);
    expect(fetch).toHaveBeenCalledTimes(1);
    hidden = false;
    document.dispatchEvent(new Event('visibilitychange'));
    await advance(0);
    expect(fetch).toHaveBeenCalledTimes(2);
    poller.stop();
  });

  it('does not request when started in a hidden tab, and removes its listener on stop', async () => {
    let hidden = true;
    const { fetch, poller } = setup({ visible: () => !hidden });
    poller.start();
    await advance(20_000);
    expect(fetch).not.toHaveBeenCalled();
    poller.stop();
    hidden = false;
    document.dispatchEvent(new Event('visibilitychange'));
    await advance(0);
    expect(fetch).not.toHaveBeenCalled();
  });
});
