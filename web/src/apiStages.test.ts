// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { type ApiError, createApi } from './api';
import { PROJECT, json, mockFetch, runView } from './test-utils';

const rejection = async (promise: Promise<unknown>) => promise.then(() => null, (e: unknown) => e as ApiError);

describe('W15 stage fields (optional, strict when present)', () => {
  const stage = (over: Record<string, unknown> = {}) => ({ key: 'static_analysis', state: 'running', planned: 2, finished: 1, succeeded: 1, counts: {}, reasons: [], started_at: '2026-11-18T03:00:00Z', finished_at: null, ...over });
  const activity = (over: Record<string, unknown> = {}) => ({
    run: { id: 'run1', status: 'running', requested_at: '2026-11-18T03:00:00Z' }, other_active_runs: 1, detail: 'recorded', stages: [stage()],
    last_report: { run_id: 'run0', revision: 2, published_at: '2026-11-17T03:00:00Z' }, last_event_at: '2026-11-18T03:01:10Z', checked_at: '2026-11-18T03:01:12Z', ...over,
  });
  const list = (a: unknown) => createApi({ fetch: mockFetch(() => json(200, { items: [{ ...PROJECT, activity: a }], next_cursor: null })).fetch }).listProjects();

  it('parses activity and leaves old payloads without it untouched', async () => {
    const p = (await list(activity())).items[0]!;
    expect(p.activity).toMatchObject({ other_active_runs: 1, detail: 'recorded', run: { id: 'run1' }, last_report: { run_id: 'run0', revision: 2 } });
    expect(p.activity!.stages[0]).toMatchObject({ key: 'static_analysis', planned: 2, finished: 1, succeeded: 1 });
    const old = await createApi({ fetch: mockFetch(() => json(200, { items: [PROJECT], next_cursor: null })).fetch }).listProjects();
    expect('activity' in old.items[0]!).toBe(false);
    expect((await list(activity({ run: null, stages: [], last_report: null }))).items[0]!.activity!.run).toBeNull();
  });

  it('keeps unknown enum values as opaque strings', async () => {
    const a = (await list(activity({ stages: [stage({ key: 'x_stage', state: 'levitating' })] }))).items[0]!.activity!;
    expect(a.stages[0]).toMatchObject({ key: 'x_stage', state: 'levitating' });
  });

  it.each([
    ['planned a string', activity({ stages: [stage({ planned: '2' })] })],
    ['negative finished', activity({ stages: [stage({ finished: -1 })] })],
    ['counts with a non-integer', activity({ stages: [stage({ counts: { failed: 'a' } })] })],
    ['reasons not strings', activity({ stages: [stage({ reasons: [1] })] })],
    ['bad started_at', activity({ stages: [stage({ started_at: 'yesterday' })] })],
    ['stages not an array', activity({ stages: {} })],
    ['missing detail', activity({ detail: undefined })],
    ['run without id', activity({ run: { status: 'running', requested_at: '2026-11-18T03:00:00Z' } })],
    ['report revision 0', activity({ last_report: { run_id: 'r', revision: 0, published_at: '2026-11-17T03:00:00Z' } })],
    ['negative other_active_runs', activity({ other_active_runs: -1 })],
    ['bad checked_at', activity({ checked_at: 'now' })],
  ])('rejects activity with %s', async (_, a) => {
    expect(await rejection(list(a))).toMatchObject({ kind: 'invalid_response' });
  });

  it('parses progress stages on a run and treats a run without them as before', async () => {
    const withStages = runView({}, { detail: 'recorded', stages: [stage({ state: 'partial', counts: { timed_out: 1 } })], last_event_at: '2026-11-18T03:01:10Z', checked_at: null });
    const run = await createApi({ fetch: mockFetch(() => json(200, withStages)).fetch }).getRun('run1');
    expect(run.progress.stages).toMatchObject({ detail: 'recorded', last_event_at: '2026-11-18T03:01:10Z', checked_at: null });
    expect(run.progress.stages!.stages[0]!.counts).toEqual({ timed_out: 1 });
    expect((await createApi({ fetch: mockFetch(() => json(200, runView())).fetch }).getRun('run1')).progress.stages).toBeUndefined();
    const bad = runView({}, { detail: 'recorded', stages: [stage({ finished: 1.5 })] });
    expect(await rejection(createApi({ fetch: mockFetch(() => json(200, bad)).fetch }).getRun('run1'))).toMatchObject({ kind: 'invalid_response' });
  });
});
