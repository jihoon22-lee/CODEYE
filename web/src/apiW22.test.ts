// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { ApiError, createApi, parseRun, runEventsURL } from './api';
import { json, mockFetch, runView } from './test-utils';
import runProgress from '../../examples/run-progress.json';
import schedules from '../../examples/schedules.json';

const rejection = async (promise: Promise<unknown>) => promise.then(() => null, (e: unknown) => e as ApiError);

describe('W22 contract parsing', () => {
  it('parses the repository RunView example with trigger, event_sequence and plan changes', () => {
    const v = parseRun(runProgress);
    expect(v.event_sequence).toBe(22);
    expect(v.trigger).toEqual({ kind: 'schedule', schedule_id: runProgress.trigger.schedule_id, scheduled_for: runProgress.trigger.scheduled_for });
    expect(v.progress).toMatchObject({ plan_revision: 2, planned_weight: 11, finished_weight: 3, weight_basis: 'mixed', estimated_percent: 27.3 });
    expect(v.progress.plan_changes).toEqual([{ revision: 2, reason: 'task_added', stage: 'code_review', weight: 3, planned_weight: 11, recorded_at: '2026-11-18T03:00:20Z' }]);
  });

  it('keeps pre-W22 run views valid (fields absent, not invented)', () => {
    const v = parseRun(runView());
    expect(v.event_sequence).toBeUndefined();
    expect(v.trigger).toBeUndefined();
    expect('planned_weight' in v.progress || 'plan_changes' in v.progress).toBe(false);
  });

  it('rejects malformed plan changes and sequences instead of guessing', async () => {
    for (const bad of [
      runView({ event_sequence: -1 }),
      runView({}, { plan_changes: [{ revision: 1, reason: 'task_added', stage: null, weight: 1, planned_weight: 1, recorded_at: null }] }),
      runView({}, { plan_changes: [{ revision: 2, reason: 'task_added', stage: null, weight: 0, planned_weight: 1, recorded_at: null }] }),
      runView({ trigger: { kind: 'schedule', scheduled_for: 'yesterday' } }),
    ]) {
      expect(await rejection(createApi({ fetch: mockFetch(() => json(200, bad)).fetch }).getRun('run1'))).toMatchObject({ kind: 'invalid_response' });
    }
  });

  it('parses the repository schedule list example', async () => {
    const m = mockFetch(() => json(200, schedules));
    const list = await createApi({ fetch: m.fetch }).listSchedules('p1');
    expect(m.calls[0]!.url).toBe('/api/v1/projects/p1/schedules');
    expect(list.can_manage).toBe(true);
    expect(list.items.map(s => [s.overlap, s.misfire, s.enabled, s.next_run_at])).toEqual([
      ['coalesce_latest', 'latest_once', true, '2026-11-19T03:00:00Z'],
      ['skip', 'skip', false, null],
    ]);
    expect(list.items[1]!.last_occurrence).toMatchObject({ state: 'skipped', reason: 'overlap_active', run_id: null });
  });

  it('opens the event stream only when an EventSource exists', () => {
    const urls: string[] = [];
    const fake = (url: string) => { urls.push(url); return { readyState: 0, onopen: null, onmessage: null, onerror: null, addEventListener() {}, close() {} }; };
    expect(createApi({ eventSource: null }).openRunEvents).toBeNull();
    createApi({ eventSource: fake }).openRunEvents!('run/1', 7);
    expect(urls).toEqual(['/api/v1/runs/run%2F1/events?cursor=7']);
    expect(runEventsURL('r', 0)).toBe('/api/v1/runs/r/events?cursor=0');
  });
});
