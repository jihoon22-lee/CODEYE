// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, screen, within } from '@testing-library/react';
import { PROVIDERS, advance, backend, flush, renderApp, runCalls } from './harness';
import { ko as t } from './locales/ko';
import { PROJECT, apiError, json } from './test-utils';

beforeEach(() => vi.useFakeTimers());
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
});

const type = (label: string, value: string) => fireEvent.change(screen.getByLabelText(label), { target: { value } });
const click = (name: string) => fireEvent.click(screen.getByRole('button', { name }));

async function openProject() {
  const app = renderApp();
  await flush();
  fireEvent.click(screen.getByRole('button', { name: /sample/ }));
  return app;
}

describe('projects', () => {
  it('lists projects with the source shown by its root label and selects one', async () => {
    renderApp();
    await flush();
    const item = screen.getByRole('button', { name: /sample/ });
    expect(item.textContent).toContain('팀 저장소 루트 / team/service');
    fireEvent.click(item);
    expect(screen.getByRole('heading', { name: 'sample' })).toBeTruthy();
    expect(screen.getByText(t.projects.sourceStatuses.declared_unverified!)).toBeTruthy();
    for (const gap of ['source_access_unverified', 'execution_unavailable', 'report_unavailable']) expect(screen.getByText(t.projects.gapLabels[gap]!)).toBeTruthy();
  });

  it('shows an empty state and keeps the form usable', async () => {
    renderApp(backend({ 'GET /api/v1/projects': () => json(200, { items: [], next_cursor: null }) }));
    await flush();
    expect(screen.getByText(t.projects.listEmpty)).toBeTruthy();
    expect(screen.getByLabelText(t.projects.relativePath)).toBeTruthy();
  });

  it('shows an unknown source status and capability as such, not as verified', async () => {
    renderApp(backend({ 'GET /api/v1/projects': () => json(200, { items: [{ ...PROJECT, source_status: 'verified', capability_gaps: ['mystery'] }], next_cursor: null }) }));
    await flush();
    fireEvent.click(screen.getByRole('button', { name: /sample/ }));
    expect(screen.getByText(t.projects.sourceStatusUnknown)).toBeTruthy();
    expect(screen.getByText('mystery')).toBeTruthy();
  });

  it('pages with the cursor and does not duplicate items', async () => {
    const urls: string[] = [];
    renderApp(backend({
      'GET /api/v1/projects': call => {
        urls.push(call.url);
        return call.url.includes('cursor=c%2B1')
          ? json(200, { items: [PROJECT, { ...PROJECT, id: 'p2', name: 'second' }], next_cursor: null })
          : json(200, { items: [PROJECT], next_cursor: 'c+1' });
      },
    }));
    await flush();
    click(t.projects.loadMore);
    await flush();
    expect(urls).toEqual(['/api/v1/projects', '/api/v1/projects?cursor=c%2B1']);
    expect(screen.getAllByRole('button', { name: /sample|second/ })).toHaveLength(2);
    expect(screen.queryByRole('button', { name: t.projects.loadMore })).toBeNull();
  });

  it('shows a list failure with a retry that reloads', async () => {
    let fail = true;
    renderApp(backend({ 'GET /api/v1/projects': () => (fail ? apiError(503, 'UNAVAILABLE', 'm', true) : undefined) }));
    await flush();
    expect(screen.getByRole('alert').textContent).toContain(t.projects.loadFailed);
    fail = false;
    click(t.projects.reload);
    await flush();
    expect(screen.getByRole('button', { name: /sample/ })).toBeTruthy();
  });
});

describe('project registration', () => {
  it('validates before sending, then posts exactly the documented local body without a description', async () => {
    const { calls } = renderApp();
    await flush();
    click(t.projects.submit);
    expect(screen.getByText(t.projects.validation.sourceRootMissing)).toBeTruthy();
    expect(screen.getByText(t.projects.validation.relativePathEmpty)).toBeTruthy();
    expect(screen.getByLabelText(t.projects.relativePath).getAttribute('aria-invalid')).toBe('true');
    expect(calls.some(c => c.method === 'POST')).toBe(false);

    type(t.projects.relativePath, '../escape');
    type(t.projects.sourceRoot, 'root1');
    click(t.projects.submit);
    expect(screen.getByText(t.projects.validation.relativePathUnsafe)).toBeTruthy();
    expect(calls.some(c => c.method === 'POST')).toBe(false);

    type(t.projects.relativePath, 'team/service');
    click(t.projects.submit);
    await flush();
    const post = calls.find(c => c.method === 'POST' && c.url === '/api/v1/projects')!;
    expect(post.body).toEqual({ source: { kind: 'local', source_root_id: 'root1', relative_path: 'team/service' } });
    expect(screen.getByText(t.projects.created)).toBeTruthy();
    expect(screen.getByRole('heading', { name: /team\/service|팀 저장소 루트/ })).toBeTruthy(); // new project is selected
  });

  it('posts a git source with the chosen provider and a name', async () => {
    const { calls } = renderApp();
    await flush();
    fireEvent.click(screen.getByLabelText(t.projects.kindGit));
    type(t.projects.name, 'remote');
    type(t.projects.provider, 'gh');
    type(t.projects.repositoryUrl, 'https://github.com/org/repo.git');
    type(t.projects.branch, 'main');
    click(t.projects.submit);
    await flush();
    expect(calls.find(c => c.method === 'POST' && c.url === '/api/v1/projects')!.body).toEqual({
      name: 'remote',
      source: { kind: 'git', provider_id: PROVIDERS.items[0]!.id, repository_url: 'https://github.com/org/repo.git', branch: 'main' },
    });
  });

  it('refuses a repository URL with embedded credentials without sending it', async () => {
    const { calls } = renderApp();
    await flush();
    fireEvent.click(screen.getByLabelText(t.projects.kindGit));
    type(t.projects.provider, 'gh');
    type(t.projects.repositoryUrl, 'https://user:token@github.com/org/repo.git');
    type(t.projects.branch, 'main');
    click(t.projects.submit);
    expect(screen.getByText(t.projects.validation.repositoryCredentials)).toBeTruthy();
    expect(calls.some(c => c.method === 'POST')).toBe(false);
  });

  it('shows a server validation failure with its request id and keeps the input', async () => {
    renderApp(backend({ 'POST /api/v1/projects': () => apiError(400, 'VALIDATION_FAILED', 'relative_path escapes the root') }));
    await flush();
    type(t.projects.sourceRoot, 'root1');
    type(t.projects.relativePath, 'a/b');
    click(t.projects.submit);
    await flush();
    const alert = screen.getByText(new RegExp(t.errors.codes.VALIDATION_FAILED!));
    expect(alert.textContent).toContain('relative_path escapes the root');
    expect(alert.textContent).toContain('req-1');
    expect((screen.getByLabelText(t.projects.relativePath) as HTMLInputElement).value).toBe('a/b');
  });

  it('prevents double submission while a request is in flight', async () => {
    let release!: (r: Response) => void;
    const { calls } = renderApp(backend({ 'POST /api/v1/projects': () => new Promise<Response>(r => { release = r; }) as never }));
    await flush();
    type(t.projects.sourceRoot, 'root1');
    type(t.projects.relativePath, 'a');
    click(t.projects.submit);
    await flush();
    expect((screen.getByRole('button', { name: t.projects.submitting }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.submit(screen.getByRole('button', { name: t.projects.submitting }).closest('form')!);
    await flush();
    expect(calls.filter(c => c.method === 'POST')).toHaveLength(1);
    release(json(201, { ...PROJECT, id: 'p-new' }));
    await flush();
  });
});

describe('starting a run', () => {
  it('retries a lost request with the same Idempotency-Key, then uses a new key for a new request', async () => {
    let attempt = 0;
    const { calls } = renderApp(backend({ 'POST /api/v1/projects/p1/runs': () => (++attempt === 1 ? 'network' : undefined) }));
    await flush();
    fireEvent.click(screen.getByRole('button', { name: /sample/ }));
    click(t.runs.start);
    await flush();
    expect(screen.getByText(t.runs.retryNote)).toBeTruthy();
    click(t.runs.start);
    await flush();
    click(t.runs.start);
    await flush();
    const posts = calls.filter(c => c.method === 'POST' && c.url === '/api/v1/projects/p1/runs');
    expect(posts).toHaveLength(3);
    expect(posts[0]!.headers['idempotency-key']).toBeTruthy();
    expect(posts[1]!.headers['idempotency-key']).toBe(posts[0]!.headers['idempotency-key']);
    expect(posts[2]!.headers['idempotency-key']).not.toBe(posts[0]!.headers['idempotency-key']);
    expect(posts[0]!.body).toEqual({ profile: 'deep-review', source_policy: 'latest_at_acquire', scope: { kind: 'full' } });
    expect(posts[0]!.headers['x-codeye-csrf']).toBeTruthy();
  });

  it('keeps the same key after a server error too', async () => {
    let attempt = 0;
    const { calls } = renderApp(backend({ 'POST /api/v1/projects/p1/runs': () => (++attempt === 1 ? apiError(503, 'UNAVAILABLE', 'm', true) : undefined) }));
    await flush();
    fireEvent.click(screen.getByRole('button', { name: /sample/ }));
    click(t.runs.start);
    await flush();
    click(t.runs.start);
    await flush();
    const keys = calls.filter(c => c.method === 'POST' && c.url.endsWith('/runs')).map(c => c.headers['idempotency-key']);
    expect(keys[0]).toBe(keys[1]);
  });

  it('shows the accepted run as queued with the honest waiting reason and no fake progress', async () => {
    await (async () => {
      renderApp();
      await flush();
      fireEvent.click(screen.getByRole('button', { name: /sample/ }));
      click(t.runs.start);
      await flush();
    })();
    expect(screen.getByText(t.runs.statusLabels.queued!)).toBeTruthy();
    expect(screen.getByText(t.runs.waitingLabels.execution_unavailable!)).toBeTruthy();
    expect(screen.getByText(t.runs.percentUnknown)).toBeTruthy();
    expect(document.querySelector('progress')).toBeNull();
    expect(screen.getByText(t.runs.tasksValue(0, null))).toBeTruthy();
    expect(screen.queryByText(t.runs.statusLabels.completed!)).toBeNull();
  });

  it('opens the project last run from its recorded id', async () => {
    const { calls } = renderApp(backend({ 'GET /api/v1/projects': () => json(200, { items: [{ ...PROJECT, last_run: { id: 'run1', status: 'queued' } }], next_cursor: null }) }));
    await flush();
    fireEvent.click(screen.getByRole('button', { name: /sample/ }));
    expect(runCalls(calls)).toHaveLength(0);
    click(t.projects.lastRunOpen);
    await flush();
    expect(runCalls(calls)).toHaveLength(1);
    expect(screen.getByText(t.runs.statusLabels.queued!)).toBeTruthy();
  });
});

describe('run polling', () => {
  async function started(handler = backend()) {
    const app = renderApp(handler);
    await flush();
    fireEvent.click(screen.getByRole('button', { name: /sample/ }));
    click(t.runs.start);
    await flush();
    return app;
  }

  it('polls every 5 seconds and shows progress changes', async () => {
    let n = 0;
    const { calls } = await started(backend({
      'GET /api/v1/runs/run1': () => json(200, ++n < 2
        ? { id: 'run1', project_id: 'p1', status: 'queued', version: 1, snapshot_id: null, progress: { stage: 'queued', plan_revision: null, completed_tasks: 0, total_tasks: null, estimated_percent: null, waiting_reason: null } }
        : { id: 'run1', project_id: 'p1', status: 'running', version: 3, snapshot_id: 's1', progress: { stage: 'analyze', plan_revision: 1, completed_tasks: 2, total_tasks: 8, estimated_percent: 25, waiting_reason: null } }),
    }));
    expect(runCalls(calls)).toHaveLength(1);
    await advance(4999);
    expect(runCalls(calls)).toHaveLength(1);
    await advance(1);
    expect(runCalls(calls)).toHaveLength(2);
    expect(screen.getByText(t.runs.statusLabels.running!)).toBeTruthy();
    expect(screen.getByText(t.runs.tasksValue(2, 8))).toBeTruthy();
    expect(screen.getByRole('progressbar', { name: t.runs.percent })).toBeTruthy();
  });

  it.each(['completed', 'partial', 'failed', 'cancelled'])('stops polling once the run is %s', async status => {
    const { calls } = await started(backend({ 'GET /api/v1/runs/run1': () => json(200, { id: 'run1', project_id: 'p1', status, version: 4, snapshot_id: null, progress: { stage: 'x', plan_revision: null, completed_tasks: 0, total_tasks: null, estimated_percent: null, waiting_reason: null } }) }));
    expect(screen.getByText(t.runs.statusLabels[status]!)).toBeTruthy();
    expect(screen.getByText(t.runs.terminalNote)).toBeTruthy();
    await advance(60_000);
    expect(runCalls(calls)).toHaveLength(1);
    expect(screen.queryByRole('button', { name: t.runs.cancel })).toBeNull();
  });

  it('shows an unrecognised status as unknown and never as success', async () => {
    await started(backend({ 'GET /api/v1/runs/run1': () => json(200, { id: 'run1', project_id: 'p1', status: 'succeeded', version: 2, snapshot_id: null, progress: { stage: '', plan_revision: null, completed_tasks: 0, total_tasks: null, estimated_percent: null, waiting_reason: null } }) }));
    const status = screen.getAllByRole('status').find(el => el.textContent?.includes(t.runs.statusUnknown))!;
    expect(status).toBeTruthy();
    expect(screen.getByText(new RegExp(t.runs.statusUnknownNote))).toBeTruthy();
    expect(screen.queryByText(t.runs.statusLabels.completed!)).toBeNull();
    expect(screen.queryByText(t.runs.terminalNote)).toBeNull();
    expect(screen.queryByRole('button', { name: t.runs.cancel })).toBeNull(); // cancel only for known cancellable states
  });

  it('keeps the last good value on errors, backs off, and recovers', async () => {
    let fail = false;
    const { calls } = await started(backend({ 'GET /api/v1/runs/run1': () => (fail ? apiError(503, 'UNAVAILABLE', 'm', true) : undefined) }));
    fail = true;
    await advance(5000);
    expect(screen.getByText(new RegExp(t.runs.pollError.slice(0, 12)))).toBeTruthy();
    expect(screen.getByText(t.runs.statusLabels.queued!)).toBeTruthy(); // last known value stays visible
    const before = runCalls(calls).length;
    await advance(9999);
    expect(runCalls(calls)).toHaveLength(before); // next attempt is after 10s, not 5s
    fail = false;
    await advance(1);
    await flush();
    expect(screen.queryByText(new RegExp(t.runs.pollError.slice(0, 12)))).toBeNull();
  });

  it('stops for good on a non-retryable error (run not found) and says so', async () => {
    const { calls } = await started(backend({ 'GET /api/v1/runs/run1': () => apiError(404, 'NOT_FOUND') }));
    await advance(120_000);
    expect(runCalls(calls)).toHaveLength(1);
    expect(screen.getAllByText(new RegExp(t.runs.pollStopped)).length).toBeGreaterThan(0);
  });

  it('pauses while the tab is hidden and refreshes when it is shown again', async () => {
    const { calls } = await started();
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' });
    document.dispatchEvent(new Event('visibilitychange'));
    await flush();
    await advance(60_000);
    expect(runCalls(calls)).toHaveLength(1);
    expect(screen.getByText(t.runs.pollingHidden)).toBeTruthy();
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
    document.dispatchEvent(new Event('visibilitychange'));
    await flush();
    expect(runCalls(calls)).toHaveLength(2);
  });

  it('stops requesting when the screen goes away (logout)', async () => {
    const { calls } = await started();
    click(t.session.logout);
    await flush();
    const before = runCalls(calls).length;
    await advance(60_000);
    expect(runCalls(calls)).toHaveLength(before);
  });

  it('ignores an older response that arrives after a newer one', async () => {
    let n = 0;
    const slow: Array<(r: Response) => void> = [];
    const view = (status: string, version: number) => json(200, { id: 'run1', project_id: 'p1', status, version, snapshot_id: null, progress: { stage: '', plan_revision: null, completed_tasks: 0, total_tasks: null, estimated_percent: null, waiting_reason: null } });
    await started(backend({ 'GET /api/v1/runs/run1': () => { n++; return n === 1 ? view('running', 5) : (new Promise<Response>(r => slow.push(r)) as never); } }));
    expect(screen.getByText(t.runs.statusLabels.running!)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: t.runs.refresh }));
    await flush();
    slow[0]!(view('queued', 1));
    await flush();
    expect(screen.getByText(t.runs.statusLabels.running!)).toBeTruthy();
    expect(screen.queryByText(t.runs.statusLabels.queued!)).toBeNull();
  });
});

describe('cancellation', () => {
  async function running() {
    const app = renderApp();
    await flush();
    fireEvent.click(screen.getByRole('button', { name: /sample/ }));
    click(t.runs.start);
    await flush();
    return app;
  }

  it('asks for confirmation, posts with the CSRF header, shows the acknowledged state, then follows the server', async () => {
    let cancelled = false;
    const { calls } = renderApp(backend({
      'POST /api/v1/runs/run1/cancel': () => { cancelled = true; return undefined; },
      'GET /api/v1/runs/run1': () => (cancelled ? json(200, { id: 'run1', project_id: 'p1', status: 'cancelled', version: 3, snapshot_id: null, progress: { stage: '', plan_revision: null, completed_tasks: 0, total_tasks: null, estimated_percent: null, waiting_reason: null } }) : undefined),
    }));
    await flush();
    fireEvent.click(screen.getByRole('button', { name: /sample/ }));
    click(t.runs.start);
    await flush();
    click(t.runs.cancel);
    expect(calls.some(c => c.url.endsWith('/cancel'))).toBe(false); // nothing sent before confirming
    click(t.runs.cancelYes);
    await flush();
    const post = calls.find(c => c.url === '/api/v1/runs/run1/cancel')!;
    expect(post.method).toBe('POST');
    expect(post.headers['x-codeye-csrf']).toBeTruthy();
    await flush();
    expect(screen.getByText(t.runs.statusLabels.cancelled!)).toBeTruthy(); // immediate refresh after the request
    expect(screen.getByText(t.runs.terminalNote)).toBeTruthy();
  });

  it('shows the intermediate requested state from the acknowledgement', async () => {
    const view = { id: 'run1', project_id: 'p1', status: 'queued', version: 1, snapshot_id: null, progress: { stage: '', plan_revision: null, completed_tasks: 0, total_tasks: null, estimated_percent: null, waiting_reason: null } };
    renderApp(backend({ 'GET /api/v1/runs/run1': () => json(200, view) }));
    await flush();
    fireEvent.click(screen.getByRole('button', { name: /sample/ }));
    click(t.runs.start);
    await flush();
    click(t.runs.cancel);
    click(t.runs.cancelYes);
    await flush();
    // The mocked server keeps answering "queued" at version 1; the older poll must not undo version 2.
    expect(screen.getByText(t.runs.statusLabels.cancel_requested!)).toBeTruthy();
  });

  it('reports a failed cancellation and allows going back', async () => {
    renderApp(backend({ 'POST /api/v1/runs/run1/cancel': () => apiError(409, 'CONFLICT') }));
    await flush();
    fireEvent.click(screen.getByRole('button', { name: /sample/ }));
    click(t.runs.start);
    await flush();
    click(t.runs.cancel);
    click(t.runs.cancelYes);
    await flush();
    expect(within(screen.getByRole('group', { name: t.runs.cancel })).getByRole('alert').textContent).toContain(t.errors.conflict);
    click(t.runs.cancelNo);
    expect(screen.getByRole('button', { name: t.runs.cancel })).toBeTruthy();
  });

  it('does not offer cancel when the run is already being cancelled', async () => {
    renderApp(backend({ 'GET /api/v1/runs/run1': () => json(200, { id: 'run1', project_id: 'p1', status: 'cancelling', version: 3, snapshot_id: null, progress: { stage: '', plan_revision: null, completed_tasks: 0, total_tasks: null, estimated_percent: null, waiting_reason: null } }) }));
    await flush();
    fireEvent.click(screen.getByRole('button', { name: /sample/ }));
    click(t.runs.start);
    await flush();
    expect(screen.queryByRole('button', { name: t.runs.cancel })).toBeNull();
    expect(screen.getByText(t.runs.cancelDone)).toBeTruthy();
  });
});
