// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, screen, within } from '@testing-library/react';
import sectioned from '../../examples/report.json';
import { PROVIDERS, advance, backend, flush, renderApp, runCalls } from './harness';
import { ko as t } from './locales/ko';
import { reportKo } from './locales/report-ko';
import { PROJECT, apiError, json, runView } from './test-utils';

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
    expect(item.textContent).toContain('팀 저장소 루트/team/service');
    fireEvent.click(item);
    expect(screen.getByRole('heading', { name: 'sample' })).toBeTruthy();
    expect(screen.getByText(t.projects.sourceStatuses.declared_unverified!)).toBeTruthy();
    for (const gap of ['source_access_unverified', 'execution_unavailable', 'report_unavailable']) expect(screen.getByText(t.projects.gapLabels[gap]!)).toBeTruthy();
  });

  it('shows an empty state and keeps the form usable', async () => {
    renderApp(backend({ 'GET /api/v1/projects': () => json(200, { items: [], next_cursor: null }) }));
    await flush();
    expect(screen.getByText(t.projects.listEmpty)).toBeTruthy();
    expect(screen.getByLabelText(t.projects.localPath)).toBeTruthy();
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

describe('shared run links', () => {
  const projectId = '01969cc2-a4dc-7b69-bc31-0a784675c954';
  const runId = '01969cc2-a4dc-7b69-bc31-0a784675c955';
  const hash = `#project=${projectId}&run=${runId}`;
  const project = { ...PROJECT, id: projectId, last_run: { id: runId, status: 'partial' } };
  const finished = runView({ id: runId, project_id: projectId, status: 'partial', report: { revision: 1 } });
  const report = () => json(200, { ...sectioned, demo: false, run_id: runId });
  const reportHeading = () => within(screen.getByRole('article', { name: reportKo.title }))
    .getByRole('heading', { level: 4, name: sectioned.project.name });
  const scroll = vi.fn();
  const reportScrolls = () => scroll.mock.contexts.filter(element => element instanceof HTMLElement && element.matches('.report-document'));
  const originalScroll = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollIntoView');
  const showTab = (value: 'visible' | 'hidden') => {
    Object.defineProperty(document, 'visibilityState', { configurable: true, value });
    fireEvent(document, new Event('visibilitychange'));
  };

  function open(reportResponse: () => Response | Promise<Response> = report, url = hash, runResponse = () => json(200, finished)) {
    return renderApp(backend({
      'GET /api/v1/projects': () => json(200, { items: [project], next_cursor: null }),
      [`GET /api/v1/projects/${projectId}/schedules`]: () => json(200, { items: [], can_manage: false }),
      [`GET /api/v1/runs/${runId}`]: runResponse,
      [`GET /api/v1/runs/${runId}/report`]: reportResponse,
    }), { hash: url });
  }

  beforeEach(() => {
    scroll.mockClear();
    Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', { configurable: true, value: scroll });
  });
  afterEach(() => {
    if (originalScroll) Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', originalScroll);
    else Reflect.deleteProperty(HTMLElement.prototype, 'scrollIntoView');
  });

  it('focuses and scrolls to the loaded report once without replacing the link', async () => {
    const { replaceState } = open();
    await flush();
    expect(document.activeElement).toBe(reportHeading());
    expect(reportHeading().tabIndex).toBe(-1);
    expect(reportScrolls()).toHaveLength(1);
    expect(replaceState).not.toHaveBeenCalled();
    click(t.accounts.tabs.members);
    click(t.accounts.tabs.run);
    await flush();
    expect(reportHeading()).toBeTruthy();
    expect(reportScrolls()).toHaveLength(1);
  });

  it('waits to land until the browser tab is visible', async () => {
    let reply!: (response: Response) => void;
    open(() => new Promise(resolve => { reply = resolve; }));
    await flush();
    showTab('hidden');
    reply(report());
    await flush();
    expect(document.activeElement).toBe(document.body);
    expect(reportScrolls()).toHaveLength(0);
    showTab('visible');
    await flush();
    expect(document.activeElement).toBe(reportHeading());
    expect(reportScrolls()).toHaveLength(1);
  });

  it('keeps the user on their chosen project tab when a report loads later', async () => {
    let reply!: (response: Response) => void;
    let calls = 0;
    open(() => calls++ === 0 ? new Promise(resolve => { reply = resolve; }) : report());
    await flush();
    click(t.accounts.tabs.members);
    reply(report());
    await flush();
    expect(screen.getByRole('button', { name: t.accounts.tabs.members }).getAttribute('aria-current')).toBe('page');
    click(t.accounts.tabs.run);
    await flush();
    expect(reportHeading()).toBeTruthy();
    expect(reportScrolls()).toHaveLength(0);
  });

  it('respects focus already moved to a form while the report was loading', async () => {
    let reply!: (response: Response) => void;
    open(() => new Promise(resolve => { reply = resolve; }));
    await flush();
    const field = screen.getByRole('textbox', { name: t.quick.field });
    field.focus();
    reply(report());
    await flush();
    expect(reportHeading()).toBeTruthy();
    expect(document.activeElement).toBe(field);
    expect(reportScrolls()).toHaveLength(0);
  });

  it.each(['', `#project=${projectId}`])('keeps ordinary project selection at the project details (%s)', async url => {
    open(report, url);
    await flush();
    if (!url) fireEvent.click(screen.getByRole('button', { name: /sample/ }));
    await flush();
    expect(reportHeading()).toBeTruthy();
    expect(reportScrolls()).toHaveLength(0);
  });

  it('keeps a linked active run on progress until it has a report', async () => {
    let published = false;
    const { calls } = open(report, hash, () => json(200, published
      ? { ...finished, version: 2 }
      : { ...finished, status: 'running', report: null }));
    await flush();
    expect(within(screen.getByRole('region', { name: t.runs.title })).getByText(t.runs.statusLabels.running!)).toBeTruthy();
    expect(calls.some(call => call.url.includes('/report'))).toBe(false);
    expect(reportScrolls()).toHaveLength(0);
    published = true;
    await advance(5000);
    await flush();
    expect(document.activeElement).toBe(reportHeading());
    expect(reportScrolls()).toHaveLength(1);
  });

  it.each([403, 404])('keeps the report error in view without moving focus (%s)', async status => {
    open(() => apiError(status, status === 403 ? 'FORBIDDEN' : 'NOT_FOUND'));
    await flush();
    expect(within(screen.getByRole('region', { name: t.runs.title })).getByRole('alert')).toBeTruthy();
    expect(screen.getByRole('button', { name: reportKo.retry })).toBeTruthy();
    expect(screen.queryByRole('article', { name: reportKo.title })).toBeNull();
    expect(reportScrolls()).toHaveLength(0);
  });
});

describe('project registration', () => {
  it('validates before sending, then posts exactly the documented local body without a description', async () => {
    const { calls } = renderApp();
    await flush();
    click(t.projects.submit);
    expect(screen.getByText(t.quick.validation.notAbsolute)).toBeTruthy();
    expect(screen.getByLabelText(t.projects.localPath).getAttribute('aria-invalid')).toBe('true');
    expect(calls.some(c => c.method === 'POST')).toBe(false);

    type(t.projects.localPath, '../escape');
    click(t.projects.submit);
    expect(screen.getByText(t.quick.validation.notAbsolute)).toBeTruthy();
    expect(calls.some(c => c.method === 'POST')).toBe(false);

    type(t.projects.localPath, '/srv/team/service');
    click(t.projects.submit);
    await flush();
    const post = calls.find(c => c.method === 'POST' && c.url === '/api/v1/projects')!;
    expect(post.body).toEqual({ local_path: '/srv/team/service' });
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
    type(t.projects.localPath, '/srv/a/b');
    click(t.projects.submit);
    await flush();
    const alert = screen.getByText(new RegExp(t.errors.codes.VALIDATION_FAILED!));
    expect(alert.textContent).toContain('relative_path escapes the root');
    expect(alert.textContent).toContain('req-1');
    expect((screen.getByLabelText(t.projects.localPath) as HTMLInputElement).value).toBe('/srv/a/b');
  });

  it('prevents double submission while a request is in flight', async () => {
    let release!: (r: Response) => void;
    const { calls } = renderApp(backend({ 'POST /api/v1/projects': () => new Promise<Response>(r => { release = r; }) as never }));
    await flush();
    type(t.projects.localPath, '/srv/a');
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
  it('shows and submits normalized selected paths, with a separate key from a full run', async () => {
    let attempt = 0;
    const { calls } = renderApp(backend({ 'POST /api/v1/projects/p1/runs': () => (++attempt === 1 ? 'network' : undefined) }));
    await flush();
    fireEvent.click(screen.getByRole('button', { name: /sample/ }));
    const field = screen.getByLabelText<HTMLInputElement>(t.runs.pathScope.label);
    fireEvent.change(field, { target: { value: '../outside' } });
    expect(screen.getByText(t.runs.pathScope.invalid)).toBeTruthy();
    expect((screen.getByRole('button', { name: t.runs.start }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(field, { target: { value: 'web/\n./internal/report\nweb' } });
    click(t.runs.pathScope.start(2));
    await flush();
    fireEvent.change(field, { target: { value: 'internal/report/\nweb' } });
    click(t.runs.pathScope.start(2));
    await flush();
    fireEvent.change(field, { target: { value: '' } });
    click(t.runs.start);
    await flush();
    const posts = calls.filter(c => c.method === 'POST');
    expect(posts.map(p => p.url)).toEqual(Array(3).fill('/api/v1/projects/p1/runs'));
    expect(posts[0].body).toMatchObject({ scope: { kind: 'paths', paths: ['internal/report', 'web'] } });
    expect(posts[1].headers['idempotency-key']).toBe(posts[0].headers['idempotency-key']);
    expect(posts[2].body).toMatchObject({ scope: { kind: 'full' } });
    expect(posts[2].headers['idempotency-key']).not.toBe(posts[0].headers['idempotency-key']);
  });

  it('keeps the test run request separate from a normal run and retries its own request', async () => {
    let attempt = 0;
    const project = { ...PROJECT, analysis: { send_source_to_model: true, run_build_scripts: true, fetch_dependencies: true } };
    const { calls } = renderApp(backend({
      'GET /api/v1/projects': () => json(200, { items: [project], next_cursor: null }),
      'POST /api/v1/projects/p1/runs': () => (++attempt === 1 ? 'network' : undefined),
    }));
    await flush();
    fireEvent.click(screen.getByRole('button', { name: /sample/ }));
    click(t.runs.startWithTests);
    await flush();
    expect(screen.getByText(t.runs.retryNote)).toBeTruthy();
    click(t.runs.start);
    await flush();
    click(t.runs.startWithTests);
    await flush();
    const posts = calls.filter(c => c.method === 'POST' && c.url === '/api/v1/projects/p1/runs');
    expect(posts.map(p => (p.body as { profile: string }).profile)).toEqual(['with-tests', 'deep-review', 'with-tests']);
    expect(posts[0].headers['idempotency-key']).toBe(posts[2].headers['idempotency-key']);
    expect(posts[0].headers['idempotency-key']).not.toBe(posts[1].headers['idempotency-key']);
  });

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

  it('only opts into expanded model coverage for the selected run', async () => {
    const { calls } = renderApp(backend());
    await flush();
    fireEvent.click(screen.getByRole('button', { name: /sample/ }));
    fireEvent.click(screen.getByText(t.runs.settings));
    fireEvent.click(screen.getByLabelText(t.runs.extendedReview));
    click(t.runs.start);
    await flush();
    const req = calls.find(c => c.method === 'POST' && c.url === '/api/v1/projects/p1/runs');
    expect(req?.body).toEqual({ profile: 'deep-review', source_policy: 'latest_at_acquire', scope: { kind: 'full', review_coverage: 'extended' } });
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

  it('opens the project last run from its recorded id when the project is selected', async () => {
    const { calls } = renderApp(backend({ 'GET /api/v1/projects': () => json(200, { items: [{ ...PROJECT, last_run: { id: 'run1', status: 'queued' } }], next_cursor: null }) }));
    await flush();
    expect(runCalls(calls)).toHaveLength(0);
    fireEvent.click(screen.getByRole('button', { name: /sample/ }));
    await flush();
    expect(runCalls(calls)).toHaveLength(1);
    expect(screen.getByText(t.runs.statusLabels.queued!)).toBeTruthy();
    // The latest run is already shown: no button to open it.
    expect(screen.queryByRole('button', { name: t.projects.lastRunOpen })).toBeNull();
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
