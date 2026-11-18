// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, screen, within } from '@testing-library/react';
import { advance, backend, flush, renderApp } from './harness';
import { formatClock } from './format';
import { ko as t } from './locales/ko';
import { PROJECT, apiError, json, runView } from './test-utils';

beforeEach(() => vi.useFakeTimers());
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
});

// Assertions are on text only: state must be readable without color.
const stage = (key: string, state: string, over: Record<string, unknown> = {}) => ({
  key, state, planned: null, finished: 0, succeeded: 0, counts: {}, reasons: [], started_at: null, finished_at: null, ...over,
});
const activity = (status: string, stages: unknown[], over: Record<string, unknown> = {}) => ({
  run: { id: 'run1', status, requested_at: '2026-11-18T03:00:00Z' }, other_active_runs: 0, detail: 'recorded', stages,
  last_report: null, last_event_at: '2026-11-18T03:01:10Z', checked_at: '2026-11-18T03:01:12Z', ...over,
});
const proj = (id: string, name: string, a?: unknown, last_run: unknown = null) => ({ ...PROJECT, id, name, last_run, ...(a === undefined ? {} : { activity: a }) });
const projects = (items: unknown[]) => backend({ 'GET /api/v1/projects': () => json(200, { items, next_cursor: null }) });
const row = (name: string) => screen.getByRole('button', { name: new RegExp(name) }).closest('li')!;
const projectCalls = (calls: { url: string }[]) => calls.filter(c => c.url === '/api/v1/projects').length;

describe('project list activity', () => {
  it('shows each project with its own status and all concurrently active stages', async () => {
    renderApp(projects([
      proj('p1', 'orders', activity('running', [stage('source', 'succeeded'), stage('environment', 'succeeded'), stage('static_analysis', 'running', { planned: 3, finished: 1 }), stage('code_review', 'running'), stage('report', 'waiting')], { other_active_runs: 2 })),
      proj('p2', 'billing', activity('queued', [stage('source', 'waiting')])),
      proj('p3', 'empty', activity('queued', [], { run: null })),
      proj('p4', 'legacy', activity('completed', [], { detail: 'not_recorded' })),
    ]));
    await flush();
    const orders = row('orders').textContent!;
    expect(orders).toContain('◉ 실행 중 · ◉ 정적 분석 중 · ◉ 코드 분석 중');
    expect(orders).toContain('다른 활성 실행 2건');
    expect(orders).not.toContain('소스 확보');
    const billing = row('billing').textContent!;
    expect(billing).toContain('◷ 대기열에 있음 · 시작한 단계 없음');
    expect(billing).not.toContain('소스 확보');
    expect(row('empty').textContent).toContain('실행 기록 없음');
    expect(row('legacy').textContent).toContain('✓ 완료 · 세부 단계 미기록');
  });

  it('shows the partial stage of a finished run and a report link pinned to its run and revision', async () => {
    renderApp(projects([proj('p1', 'converter', activity('partial', [stage('static_analysis', 'succeeded'), stage('code_review', 'partial', { counts: { timed_out: 1 } }), stage('report', 'succeeded')],
      { last_report: { run_id: 'run0', revision: 3, published_at: '2026-11-17T00:00:00Z' } }))]));
    await flush();
    const li = row('converter');
    expect(li.textContent).toContain('△ 일부만 완료 · 코드 분석 △ 일부 수행');
    const link = within(li).getByRole('link');
    expect(link.getAttribute('href')).toBe('/api/v1/runs/run0/report?format=html&include_code=false&revision=3');
    expect(link.textContent).toContain('리비전 3');
  });

  it('falls back to last_run for a server without activity, and to the empty text without a run', async () => {
    renderApp(projects([proj('p1', 'old', undefined, { id: 'r', status: 'failed' }), proj('p2', 'none')]));
    await flush();
    expect(row('old').textContent).toContain('✕ 실패 · 세부 단계 미기록');
    expect(row('none').textContent).toContain('실행 기록 없음');
  });

  it('renders unknown run status, stage and state safely with the raw value, never as success', async () => {
    renderApp(projects([proj('p1', 'odd', activity('teleporting', [stage('warp', 'levitating')]))]));
    await flush();
    const text = row('odd').textContent!;
    expect(text).toContain('상태를 알 수 없음 (teleporting)');
    expect(text).toContain('알 수 없는 단계 (warp)');
    expect(text).toContain('알 수 없는 상태 (levitating)');
    expect(text).not.toContain('✓');
  });

  it('polls while a run is active, keeps stale data with the last confirmed time on failure, then stops', async () => {
    vi.setSystemTime(new Date(2026, 10, 18, 3, 0, 0));
    let n = 0;
    const { calls } = renderApp(backend({
      'GET /api/v1/projects': () => {
        n++;
        if (n === 1) return json(200, { items: [proj('p1', 'orders', activity('running', [stage('static_analysis', 'running')]))], next_cursor: null });
        if (n === 2) return apiError(503, 'UNAVAILABLE', 'm', true);
        return json(200, { items: [proj('p1', 'orders', activity('completed', [stage('static_analysis', 'succeeded')]))], next_cursor: null });
      },
    }));
    await flush();
    const first = formatClock(new Date());
    expect(row('orders').textContent).toContain('◉ 정적 분석 중');
    await advance(5000);
    expect(row('orders').textContent).toContain('◉ 정적 분석 중');
    expect(screen.getByRole('alert').textContent).toContain(t.projects.loadFailed);
    expect(screen.getByText(new RegExp(`마지막 확인 ${first}`))).toBeTruthy();
    await advance(10_000);
    expect(row('orders').textContent).toContain('✓ 완료');
    expect(screen.queryByRole('alert')).toBeNull();
    const settled = projectCalls(calls);
    await advance(60_000);
    expect(projectCalls(calls)).toBe(settled);
  });

  it('does not poll when no project has an active run', async () => {
    const { calls } = renderApp(projects([proj('p1', 'idle', activity('completed', [stage('report', 'succeeded')]))]));
    await flush();
    await advance(30_000);
    expect(projectCalls(calls)).toBe(1);
  });

  it('pauses polling while the tab is hidden', async () => {
    const { calls } = renderApp(projects([proj('p1', 'busy', activity('running', [stage('static_analysis', 'running')]))]));
    await flush();
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' });
    document.dispatchEvent(new Event('visibilitychange'));
    await advance(30_000);
    expect(projectCalls(calls)).toBe(1);
  });
});

describe('queued waiting reason', () => {
  it('shows the localized reason, a raw fallback, and the default when absent', async () => {
    // As the server sends it: a queued run's task stages are plan_pending.
    const queued = [stage('source', 'waiting'), stage('environment', 'plan_pending'), stage('code_review', 'plan_pending'), stage('report', 'waiting')];
    renderApp(projects([
      proj('p1', 'known', activity('queued', queued, { waiting_reason: 'capacity_full' })),
      proj('p2', 'rawone', activity('queued', [stage('source', 'waiting')], { waiting_reason: 'odd_reason' })),
      proj('p3', 'absent', activity('queued', [stage('source', 'waiting')], { waiting_reason: null })),
    ]));
    await flush();
    expect(row('known').textContent).toContain(`◷ 대기열에 있음 · ${t.runs.waitingLabels.capacity_full}`);
    expect(row('known').textContent).not.toContain('시작한 단계 없음');
    expect(row('known').textContent).not.toContain('계획 미확정');
    expect(row('rawone').textContent).toContain('◷ 대기열에 있음 · odd_reason');
    expect(row('absent').textContent).toContain('◷ 대기열에 있음 · 시작한 단계 없음');
  });
});

describe('run detail stages', () => {
  const open = async (view: unknown) => {
    renderApp(backend({
      'GET /api/v1/projects': () => json(200, { items: [{ ...PROJECT, last_run: { id: 'run1', status: 'running' } }], next_cursor: null }),
      'GET /api/v1/runs/run1': () => json(200, view),
    }));
    await flush();
    fireEvent.click(screen.getByRole('button', { name: /sample/ }));
    fireEvent.click(screen.getByRole('button', { name: t.projects.lastRunOpen }));
    await flush();
  };
  const progress = (stages: unknown[], over: Record<string, unknown> = {}) => ({ detail: 'recorded', stages, last_event_at: '2026-11-18T03:01:10Z', checked_at: '2026-11-18T03:01:12Z', ...over });

  it('renders partial counts, distinct skip reasons, cancelled, in order, and hides stages without tasks', async () => {
    await open(runView({ status: 'partial' }, progress([
      stage('report', 'cancelled'),
      stage('source', 'succeeded', { planned: 1, finished: 1, succeeded: 1 }),
      stage('environment', 'no_tasks'),
      stage('documents', 'skipped', { planned: 1, finished: 1, reasons: ['no_documents'] }),
      stage('static_analysis', 'skipped', { reasons: ['model_source_not_approved'] }),
      stage('code_review', 'partial', { planned: 4, finished: 4, succeeded: 2, counts: { failed: 1, timed_out: 1 } }),
    ])));
    const text = document.body.textContent!;
    expect(text).not.toContain('해당 작업 없음');
    expect(text).not.toContain('환경 분석');
    expect(text).toContain('사유: 스냅샷 범위에 문서가 없어 문서 검토를 생략함');
    expect(text).toContain('사유: 소스의 모델 전송이 승인되지 않아 제외됨 (정책 제외');
    expect(text).toContain('실패 1건 · 시간 초과 1건');
    expect(text).toContain('△ 일부 수행');
    expect(text).toContain('■ 취소됨');
    expect(text).toContain('✓ 성공');
    const order = ['소스 확보', '문서 검토', '정적 분석', '코드 분석', '보고서 생성'].map(n => text.indexOf(n, text.indexOf('세부 단계')));
    expect(order.every(i => i >= 0)).toBe(true);
    expect([...order].sort((a, b) => a - b)).toEqual(order);
    expect(text).toContain('마지막 이벤트');
    expect(text).toContain('서버 집계');
  });

  it('expands a stage with a real button and shows the planned count only when known', async () => {
    await open(runView({ status: 'running' }, progress([
      stage('static_analysis', 'running', { planned: 3, finished: 1, succeeded: 1, started_at: '2026-11-18T03:00:30Z' }),
      stage('code_review', 'waiting'),
    ])));
    const button = screen.getByRole('button', { name: /정적 분석/ });
    expect(button.tagName).toBe('BUTTON');
    expect(button.getAttribute('aria-expanded')).toBe('false');
    expect(document.getElementById(button.getAttribute('aria-controls')!)!.hidden).toBe(true);
    fireEvent.click(button);
    expect(button.getAttribute('aria-expanded')).toBe('true');
    expect(screen.getByText('1/3 종료')).toBeTruthy();
    expect(screen.getByText('성공 1건')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: /코드 분석/ }));
    expect(screen.getByText('0건 종료 (계획 수 미확정)')).toBeTruthy();
    expect(screen.getByText('시작 기록 없음')).toBeTruthy();
    expect(screen.queryByText(/0\/\d+ 종료/)).toBeNull();
    fireEvent.click(button);
    expect(button.getAttribute('aria-expanded')).toBe('false');
  });

  it('shows "세부 단계 미기록" with the run status for legacy runs, with or without the fields', async () => {
    await open(runView({ status: 'completed' }, progress([], { detail: 'not_recorded' })));
    expect(screen.getByText('세부 단계 미기록 · 실행 상태: ✓ 완료')).toBeTruthy();
    cleanup();
    await open(runView({ status: 'completed' }));
    expect(screen.getByText('세부 단계 미기록 · 실행 상태: ✓ 완료')).toBeTruthy();
  });

  it('shows an unknown stage, state and detail safely', async () => {
    await open(runView({ status: 'running' }, progress([stage('static_analysis', 'levitating'), stage('mystery', 'succeeded')])));
    expect(screen.getByRole('button', { name: /정적 분석/ }).textContent).toContain('? 알 수 없는 상태 (levitating)');
    expect(screen.getByRole('button', { name: /알 수 없는 단계 \(mystery\)/ })).toBeTruthy();
    cleanup();
    await open(runView({ status: 'running' }, progress([], { detail: 'sideways' })));
    expect(screen.getByText('세부 단계 기록 방식을 알 수 없음 (sideways)')).toBeTruthy();
  });

  it('keeps the last stages and shows the last confirmed time when a poll fails', async () => {
    vi.setSystemTime(new Date(2026, 10, 18, 4, 5, 6));
    let fail = false;
    renderApp(backend({
      'GET /api/v1/projects': () => json(200, { items: [{ ...PROJECT, last_run: { id: 'run1', status: 'running' } }], next_cursor: null }),
      'GET /api/v1/runs/run1': () => (fail ? apiError(503, 'UNAVAILABLE', 'm', true) : json(200, runView({ status: 'running' }, progress([stage('static_analysis', 'running')])))),
    }));
    await flush();
    fireEvent.click(screen.getByRole('button', { name: /sample/ }));
    fireEvent.click(screen.getByRole('button', { name: t.projects.lastRunOpen }));
    await flush();
    const confirmed = formatClock(new Date());
    fail = true;
    await advance(5000);
    expect(screen.getByRole('alert').textContent).toContain(t.runs.pollError);
    expect(screen.getByRole('button', { name: /정적 분석/ })).toBeTruthy();
    expect(screen.getByText(new RegExp(`마지막 확인 ${confirmed}`))).toBeTruthy();
  });
});
