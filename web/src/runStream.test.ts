// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, parseRun, type RunView } from './api';
import { createRunStream, isNotOlder, type RunStreamOptions, type StreamMode } from './runStream';
import { FakeEventSource, runView } from './test-utils';
import { retryable } from './useRunPolling';

beforeEach(() => { vi.useFakeTimers(); FakeEventSource.reset(); });
afterEach(() => { vi.useRealTimers(); });

const tick = async (ms = 0) => { await vi.advanceTimersByTimeAsync(ms); };
const view = (seq: number | undefined, version: number, status = 'running', progress: Record<string, unknown> = {}): RunView =>
  parseRun(runView({ status, version, ...(seq === undefined ? {} : { event_sequence: seq }) }, { stage: status, waiting_reason: null, ...progress }));
const wire = (v: RunView) => ({ ...runView({ status: v.status, version: v.version, event_sequence: v.event_sequence }, { stage: v.status, waiting_reason: v.progress.waiting_reason, plan_revision: v.progress.plan_revision }) });

// fetch answers from a queue; the last answer repeats.
function setup(answers: Array<RunView | Error>, over: Partial<RunStreamOptions> = {}) {
  const shown: RunView[] = [];
  const errors: Array<[unknown, boolean]> = [];
  const modes: StreamMode[] = [];
  const confirmed: Date[] = [];
  const fetch = vi.fn(async () => {
    const answer = answers.length > 1 ? answers.shift()! : answers[0]!;
    if (answer instanceof Error) throw answer;
    return answer;
  });
  const opened: number[] = [];
  const s = createRunStream({
    fetch,
    open: cursor => { opened.push(cursor); return FakeEventSource.factory(`/api/v1/runs/run1/events?cursor=${cursor}`); },
    onView: v => shown.push(v),
    onError: (e, retry) => errors.push([e, retry]),
    onMode: m => modes.push(m),
    onConfirmed: d => confirmed.push(d),
    retryable,
    visible: () => true,
    ...over,
  });
  return { s, fetch, shown, errors, modes, confirmed, opened, mode: () => s.mode() };
}

describe('run event stream', () => {
  it('starts from the GET event_sequence, applies run.view and treats rows only as the cursor', async () => {
    const h = setup([view(3, 2)]);
    h.s.start();
    await tick();
    expect(h.opened).toEqual([3]);
    const es = FakeEventSource.last();
    expect(es.url).toBe('/api/v1/runs/run1/events?cursor=3');
    expect(h.mode()).toBe('connecting');
    es.open();
    expect(h.mode()).toBe('live');
    es.row(4);
    es.row(5);
    expect(h.shown.map(v => v.event_sequence)).toEqual([3]); // rows never become state by themselves
    es.emit('run.view', wire(view(5, 3)));
    expect(h.shown.map(v => [v.event_sequence, v.version])).toEqual([[3, 2], [5, 3]]);
    expect(h.confirmed).toHaveLength(1);
    // A heartbeat confirms the connection but never changes the run.
    es.emit('heartbeat', { run_id: 'run1' });
    expect(h.shown).toHaveLength(2);
    expect(h.confirmed).toHaveLength(2);
    expect(h.fetch).toHaveBeenCalledTimes(1); // no polling while live
    for (let i = 0; i < 6; i++) {
      await tick(15_000);
      es.emit('heartbeat', { run_id: 'run1' });
    }
    expect(h.fetch).toHaveBeenCalledTimes(1);
    expect(h.shown).toHaveLength(2);
    h.s.stop();
    expect(es.closed).toBe(true);
  });

  it('on a sequence gap closes the stream, re-reads the run and reopens from the new cursor', async () => {
    const h = setup([view(3, 2), view(9, 4)]);
    h.s.start();
    await tick();
    const first = FakeEventSource.last();
    first.open();
    first.row(4);
    first.row(6); // 5 is missing: never skipped
    expect(first.closed).toBe(true);
    await tick();
    expect(h.fetch).toHaveBeenCalledTimes(2);
    expect(h.opened).toEqual([3, 9]);
    expect(h.shown.map(v => v.event_sequence)).toEqual([3, 9]);
    // The server's own resync signal does the same.
    const second = FakeEventSource.last();
    second.open();
    second.emit('state.resync', { run_id: 'run1', reason: 'cursor_expired', head: 2000 });
    expect(second.closed).toBe(true);
    await tick();
    expect(h.opened).toEqual([3, 9, 9]);
  });

  it('ignores a view older than the one shown (event_sequence first, then version)', async () => {
    const h = setup([view(5, 3)]);
    h.s.start();
    await tick();
    const es = FakeEventSource.last();
    es.open();
    es.emit('run.view', wire(view(4, 3)));
    es.emit('run.view', wire(view(5, 2)));
    expect(h.shown.map(v => [v.event_sequence, v.version])).toEqual([[5, 3]]);
    expect(isNotOlder(view(5, 3), view(6, 3))).toBe(true);
    expect(isNotOlder(view(undefined, 3), view(undefined, 2))).toBe(false);
  });

  it('falls back to polling when the stream fails and retries the stream a bounded number of times', async () => {
    const h = setup([view(3, 2)], { reopenMs: 1000, maxReopens: 2 });
    h.s.start();
    await tick();
    FakeEventSource.last().fail(); // for example 503 at the per-user stream cap
    expect(FakeEventSource.last().closed).toBe(true);
    expect(h.mode()).toBe('fallback');
    await tick();
    expect(h.fetch).toHaveBeenCalledTimes(2); // the poller's immediate GET
    await tick(1000); // first reopen: GET then a new stream
    expect(FakeEventSource.all).toHaveLength(2);
    FakeEventSource.last().fail();
    await tick(2000); // second reopen after a doubled delay
    expect(FakeEventSource.all).toHaveLength(3);
    FakeEventSource.last().fail();
    await tick(60_000);
    expect(FakeEventSource.all).toHaveLength(3); // no third reopen: polling only
    expect(h.mode()).toBe('fallback');
    const polls = h.fetch.mock.calls.length;
    await tick(5000);
    expect(h.fetch.mock.calls.length).toBe(polls + 1);
    h.s.stop();
  });

  it('reconnects at once when an open stream ends (server time limit), and falls back after repeated ends', async () => {
    const h = setup([view(3, 2), view(5, 3)], { maxResyncs: 2 });
    h.s.start();
    await tick();
    const first = FakeEventSource.last();
    first.open();
    first.fail(); // the server closed a stream that was open
    expect(first.closed).toBe(true);
    await tick();
    expect(h.opened).toEqual([3, 5]); // fresh GET, then the new cursor; no polling detour
    expect(h.mode()).toBe('connecting');
    FakeEventSource.last().open();
    expect(h.mode()).toBe('live');
    FakeEventSource.last().fail();
    await tick();
    FakeEventSource.last().open();
    FakeEventSource.last().fail(); // third end without a heartbeat or run.view in between
    await tick();
    expect(h.mode()).toBe('fallback');
    h.s.stop();
  });

  it('treats silence past the watchdog as a failure', async () => {
    const h = setup([view(3, 2)], { watchdogMs: 1000, reopenMs: 60_000 });
    h.s.start();
    await tick();
    const es = FakeEventSource.last();
    es.open();
    await tick(900);
    es.emit('heartbeat', { run_id: 'run1' });
    await tick(900);
    expect(es.closed).toBe(false);
    await tick(200);
    expect(es.closed).toBe(true);
    expect(h.mode()).toBe('fallback');
    h.s.stop();
  });

  it('closed (logout, lost access) stops the stream and lets a GET report the error', async () => {
    const h = setup([view(3, 2), new ApiError('http', 404, 'NOT_FOUND', false)]);
    h.s.start();
    await tick();
    FakeEventSource.last().open();
    FakeEventSource.last().emit('closed', { run_id: 'run1', reason: 'access_revoked' });
    await tick();
    expect(h.errors).toHaveLength(1);
    expect(h.errors[0]![1]).toBe(false);
    expect(h.mode()).toBe('stopped');
    await tick(120_000);
    expect(FakeEventSource.all).toHaveLength(1);
  });

  it('stops for good at a terminal status', async () => {
    const h = setup([view(3, 2)]);
    h.s.start();
    await tick();
    const es = FakeEventSource.last();
    es.open();
    es.emit('run.view', wire(view(9, 5, 'completed')));
    expect(es.closed).toBe(true);
    expect(h.mode()).toBe('stopped');
    expect(h.s.isRunning()).toBe(false);
  });

  it('re-reads a queued run while streaming (its waiting reason has no event)', async () => {
    const h = setup([view(1, 1, 'queued')], { intervalMs: 5000 });
    h.s.start();
    await tick();
    FakeEventSource.last().open();
    await tick(5000);
    expect(h.fetch).toHaveBeenCalledTimes(2);
    await tick(5000);
    expect(h.fetch).toHaveBeenCalledTimes(3);
    // Running: events carry every change, no more GETs.
    FakeEventSource.last().emit('run.view', wire(view(4, 2, 'running')));
    await tick(20_000);
    expect(h.fetch).toHaveBeenCalledTimes(3);
    h.s.stop();
  });

  it('polls only when there is no EventSource or the server has no event_sequence', async () => {
    const none = setup([view(3, 2)], { open: null });
    none.s.start();
    await tick();
    await tick(5000);
    expect(none.fetch).toHaveBeenCalledTimes(2);
    expect(none.mode()).toBe('polling');
    none.s.stop();
    const old = setup([view(undefined, 2)]);
    old.s.start();
    await tick();
    expect(FakeEventSource.all).toHaveLength(0);
    expect(old.mode()).toBe('polling');
    old.s.stop();
  });

  it('closes the stream while the tab is hidden and resumes from a fresh GET', async () => {
    let shown = true;
    const h = setup([view(3, 2), view(7, 3)], { visible: () => shown });
    h.s.start();
    await tick();
    const es = FakeEventSource.last();
    es.open();
    shown = false;
    document.dispatchEvent(new Event('visibilitychange'));
    expect(es.closed).toBe(true);
    expect(h.mode()).toBe('paused');
    await tick(60_000);
    expect(h.fetch).toHaveBeenCalledTimes(1);
    shown = true;
    document.dispatchEvent(new Event('visibilitychange'));
    await tick();
    expect(h.opened).toEqual([3, 7]);
    h.s.stop();
  });
});
