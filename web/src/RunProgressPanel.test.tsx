// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, screen } from '@testing-library/react';
import { advance, backend, flush, renderApp, runCalls } from './harness';
import { formatInstant } from './format';
import { ko as t } from './locales/ko';
import { FakeEventSource, apiError, json, runView } from './test-utils';

beforeEach(() => { vi.useFakeTimers(); FakeEventSource.reset(); });
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

const click = (name: string | RegExp) => fireEvent.click(screen.getByRole('button', { name }));
const running = (seq: number, version: number, progress: Record<string, unknown> = {}) => runView(
  { status: 'running', version, event_sequence: seq, trigger: { kind: 'schedule', schedule_id: 's1', scheduled_for: '2026-11-18T03:00:00Z' } },
  { stage: 'running', plan_revision: 1, completed_tasks: 1, total_tasks: 4, estimated_percent: 25, planned_weight: 4, finished_weight: 1, weight_basis: 'mixed', plan_changes: [], waiting_reason: null, ...progress });

describe('run panel over the event stream', () => {
  async function started(handler = backend({ 'GET /api/v1/runs/run1': () => json(200, running(3, 2)) })) {
    const app = renderApp(handler, { eventSource: FakeEventSource.factory });
    await flush();
    fireEvent.click(screen.getByRole('button', { name: /sample/ }));
    click(t.runs.start);
    await flush();
    return app;
  }

  it('streams from the GET cursor, shows the plan revision and why the denominator changed', async () => {
    const { calls } = await started();
    const es = FakeEventSource.last();
    expect(es.url).toBe('/api/v1/runs/run1/events?cursor=3');
    act(() => es.open());
    expect(screen.getByText(new RegExp(t.runs.streamLive))).toBeTruthy();
    expect(screen.getByText(t.runs.planValue(1, 1, 4))).toBeTruthy();
    expect(screen.getByText(t.runs.weightBasis.mixed!)).toBeTruthy();
    expect(screen.getByText(t.runs.triggerSchedule(formatInstant('2026-11-18T03:00:00Z')))).toBeTruthy();
    act(() => {
      es.row(4, 'task.created');
      es.emit('run.view', running(4, 3, { plan_revision: 2, total_tasks: 5, estimated_percent: 14.3, planned_weight: 7,
        plan_changes: [{ revision: 2, reason: 'task_added', stage: 'code_review', weight: 3, planned_weight: 7, recorded_at: '2026-11-18T03:00:20Z' }] }));
    });
    expect(screen.getByText(t.runs.planValue(2, 1, 7))).toBeTruthy();
    expect(screen.getByText(new RegExp(t.runs.planChange(2, t.stages.names.code_review!, 3, 7).replace(/[()]/g, '\\$&')))).toBeTruthy();
    expect(screen.getByText(new RegExp(t.stages.lastConfirmed('')))).toBeTruthy();
    // Live: no polling.
    await advance(12_000);
    expect(runCalls(calls)).toHaveLength(1);
  });

  it('a gap re-reads the run (version-guarded) and reconnects from its event_sequence', async () => {
    let n = 0;
    const { calls } = await started(backend({ 'GET /api/v1/runs/run1': () => json(200, ++n === 1 ? running(3, 2) : running(8, 5, { completed_tasks: 3, estimated_percent: 75, finished_weight: 3 })) }));
    const es = FakeEventSource.last();
    act(() => { es.open(); es.row(5); });
    await flush();
    expect(es.closed).toBe(true);
    expect(runCalls(calls)).toHaveLength(2);
    expect(FakeEventSource.last().url).toBe('/api/v1/runs/run1/events?cursor=8');
    expect(screen.getByText(t.runs.tasksValue(3, 4))).toBeTruthy();
    // An older view arriving later is not applied.
    act(() => { FakeEventSource.last().open(); FakeEventSource.last().emit('run.view', running(6, 4)); });
    expect(screen.getByText(t.runs.tasksValue(3, 4))).toBeTruthy();
  });

  it('falls back to polling every 5 seconds when the stream fails, and says so', async () => {
    const { calls } = await started();
    act(() => FakeEventSource.last().fail());
    await flush();
    expect(screen.getByText(t.runs.streamFallback)).toBeTruthy();
    const before = runCalls(calls).length;
    await advance(5000);
    expect(runCalls(calls).length).toBe(before + 1);
  });

  it('a closed stream (session ended) surfaces the GET error instead of retrying silently', async () => {
    let n = 0;
    await started(backend({ 'GET /api/v1/runs/run1': () => (++n === 1 ? json(200, running(3, 2)) : apiError(404, 'NOT_FOUND')) }));
    act(() => { FakeEventSource.last().open(); FakeEventSource.last().emit('closed', { run_id: 'run1', reason: 'access_revoked' }); });
    await flush();
    expect(screen.getByRole('alert').textContent).toContain(t.errors.not_found);
    expect(screen.getAllByText(new RegExp(t.runs.pollStopped)).length).toBeGreaterThan(0);
    await advance(120_000);
    expect(FakeEventSource.all).toHaveLength(1);
  });
});

const SCHEDULE = {
  id: 's1', project_id: 'p1', cron: '0 12 * * 1-5', timezone: 'Asia/Seoul', overlap: 'coalesce_latest', misfire: 'latest_once', enabled: true,
  run: { profile: 'deep-review', source_policy: 'latest_at_acquire', scope: { kind: 'full' } }, version: 2,
  next_run_at: '2026-11-19T03:00:00Z', created_at: '2026-11-10T08:30:00Z', updated_at: '2026-11-10T08:30:00Z',
  last_occurrence: { scheduled_for: '2026-11-18T03:00:00Z', state: 'skipped', reason: 'overlap_active', missed_from: '2026-11-17T03:00:00Z', decided_at: '2026-11-18T03:00:01Z', run_id: null },
  pending_occurrence: null,
};

describe('schedules', () => {
  async function opened(over: Parameters<typeof backend>[0]) {
    const app = renderApp(backend(over));
    await flush();
    fireEvent.click(screen.getByRole('button', { name: /sample/ }));
    await flush();
    return app;
  }

  it('shows a viewer the schedules with server decisions and no create or delete', async () => {
    await opened({ 'GET /api/v1/projects/p1/schedules': () => json(200, { items: [SCHEDULE], can_manage: false }) });
    expect(screen.getByText('0 12 * * 1-5')).toBeTruthy();
    expect(screen.getByText(new RegExp(t.schedules.reasons.overlap_active!))).toBeTruthy();
    expect(screen.getByText(new RegExp(t.schedules.states.skipped!))).toBeTruthy();
    expect(screen.getByText(t.schedules.viewerNote)).toBeTruthy();
    expect(screen.queryByRole('button', { name: t.schedules.create })).toBeNull();
    expect(screen.queryByRole('button', { name: t.schedules.delete })).toBeNull();
  });

  it('lets a manager create with an Idempotency-Key reused on retry, and delete with If-Match', async () => {
    let posts = 0;
    let items: unknown[] = [];
    const { calls } = await opened({
      'GET /api/v1/projects/p1/schedules': () => json(200, { items, can_manage: true }),
      'POST /api/v1/projects/p1/schedules': () => {
        posts += 1;
        if (posts === 1) return 'network';
        items = [SCHEDULE];
        return json(201, SCHEDULE);
      },
      'DELETE /api/v1/projects/p1/schedules/s1': () => { items = []; return new Response(null, { status: 204 }); },
    });
    expect(screen.getByText(t.schedules.empty)).toBeTruthy();
    fireEvent.change(screen.getByLabelText(t.schedules.cron), { target: { value: ' 0 12 * * 1-5 ' } });
    fireEvent.change(screen.getByLabelText(t.schedules.timezone), { target: { value: 'Asia/Seoul' } });
    click(t.schedules.create);
    await flush();
    expect(screen.getByText(t.schedules.retryNote)).toBeTruthy();
    click(t.schedules.create);
    await flush();
    const sent = calls.filter(c => c.method === 'POST' && c.url === '/api/v1/projects/p1/schedules');
    expect(sent).toHaveLength(2);
    expect(sent[0]!.headers['idempotency-key']).toBeTruthy();
    expect(sent[1]!.headers['idempotency-key']).toBe(sent[0]!.headers['idempotency-key']);
    expect(sent[1]!.headers['x-codeye-csrf']).toBe('csrf-secret-value');
    expect(sent[1]!.body).toEqual({ cron: '0 12 * * 1-5', timezone: 'Asia/Seoul', overlap: 'coalesce_latest', misfire: 'latest_once', run: { profile: 'deep-review', source_policy: 'latest_at_acquire', scope: { kind: 'full' } } });
    expect(screen.getByText(t.schedules.created)).toBeTruthy();
    expect(screen.getByText('0 12 * * 1-5')).toBeTruthy();
    click(t.schedules.delete);
    click(t.schedules.deleteYes);
    await flush();
    const del = calls.find(c => c.method === 'DELETE')!;
    expect(del.headers['if-match']).toBe('"2"');
    expect(screen.getByText(t.schedules.empty)).toBeTruthy();
  });

  it('shows a rejected cron as a validation error without the retry note', async () => {
    await opened({
      'GET /api/v1/projects/p1/schedules': () => json(200, { items: [], can_manage: true }),
      'POST /api/v1/projects/p1/schedules': () => apiError(422, 'VALIDATION_FAILED', 'cron: never fires'),
    });
    fireEvent.change(screen.getByLabelText(t.schedules.cron), { target: { value: '0 0 30 2 *' } });
    click(t.schedules.create);
    await flush();
    expect(screen.getByText(/cron: never fires/)).toBeTruthy();
    expect(screen.queryByText(t.schedules.retryNote)).toBeNull();
  });
});
