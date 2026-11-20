// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, screen, within } from '@testing-library/react';
import sectioned from '../../examples/report.json';
import { backend, flush, renderApp } from './harness';
import { ko as t } from './locales/ko';
import { PROJECT, apiError, json, runView, type Call } from './test-utils';

beforeEach(() => vi.useFakeTimers());
afterEach(() => { cleanup(); vi.useRealTimers(); });

const SETUP = {
  is_admin: true,
  service_origin: null,
  model: { configured: false, id: null, probe: null },
  git: { available: true, hosts: ['github.com'] },
  readable_folders: ['/home/me'],
  analysis_defaults: { local: { send_source_to_model: true, run_build_scripts: true }, git: { send_source_to_model: true, run_build_scripts: false } },
  tools: {
    schema_version: '0.1', detected_at: '2026-11-19T01:00:00Z', python_both_type_checkers: false,
    tools: [
      { name: 'go', status: 'found', version: '1.27.4', path: '/usr/local/go/bin/go', version_tested: true },
      { name: 'cppcheck', status: 'not_found', version_tested: false, hint: 'apt install cppcheck' },
    ],
  },
  github: { enabled: true, host: 'github.com', token: false, account: null, source: null, reason: null },
  offline: false,
};
// What a user who is not an administrator gets by default: no folder, no build scripts.
const MEMBER_SETUP = {
  ...SETUP, is_admin: false, readable_folders: [], tools: null,
  analysis_defaults: { local: { send_source_to_model: true, run_build_scripts: false }, git: { send_source_to_model: true, run_build_scripts: false } },
  available: {
    git_url: { available: true, reason: null }, local_path: { available: false, reason: 'no_allowed_folder' },
    pull_requests: { available: true, reason: null }, github_token: { available: true, reason: null },
    model_review: { available: true, reason: null }, build_scripts: { available: false, reason: 'not_offered' },
    fetch_dependencies: { available: true, reason: null },
  },
};
// SETUP above and the projects below leave fetch_dependencies out, as older servers do: it reads as on.
const ANALYSIS_ON = { send_source_to_model: true, run_build_scripts: true };
const FOLDER = { ...PROJECT, id: 'p-folder', name: 'app', source: { kind: 'local', source_root_id: 'root1', relative_path: 'app' }, analysis: ANALYSIS_ON };
const REPO = { ...PROJECT, id: 'p-repo', name: 'r', source: { kind: 'git', provider_id: 'gh', repository_url: 'https://github.com/o/r' }, analysis: { send_source_to_model: true, run_build_scripts: false } };
const ack = (id: string, status = 'queued') => json(202, { id, status, version: 1, url: `/api/v1/runs/${id}` });
const type = (label: string, value: string) => fireEvent.change(screen.getByLabelText(label), { target: { value } });

// A published partial report whose gaps are a missing tool, no model and a Rust check that needs the build scripts.
function report(runId: string) {
  const doc = structuredClone(sectioned) as Record<string, any>;
  doc.demo = false;
  doc.run_id = runId;
  doc.gaps = [
    { section: 'static_analysis', code: 'task_unsupported', subject: 'cargo-check.m01', detail: 'repository_code_not_allowed' },
    { section: 'static_analysis', code: 'task_unsupported', subject: 'cppcheck', detail: 'tool_not_configured' },
    { section: 'code_review', code: 'role_not_succeeded', subject: 'review.interpret', detail: 'model_not_configured' },
  ];
  doc.overview.sections.find((s: { key: string }) => s.key === 'gaps').counts.gaps = 3;
  return doc;
}

function personal(over: Record<string, (call: Call) => Response | undefined> = {}) {
  return backend({
    'GET /api/v1/setup': () => json(200, SETUP),
    'GET /api/v1/projects': () => json(200, { items: [], next_cursor: null }),
    'POST /api/v1/projects': call => json(201, (call.body as { local_path?: string }).local_path ? FOLDER : REPO),
    'POST /api/v1/projects/p-folder/runs': () => ack('run-f'),
    'POST /api/v1/projects/p-repo/runs': () => ack('run-r'),
    'GET /api/v1/runs/run-f': () => json(200, runView({ id: 'run-f', project_id: 'p-folder', status: 'running' })),
    'GET /api/v1/projects/p-folder/schedules': () => json(200, { items: [], can_manage: true }),
    'GET /api/v1/projects/p-repo/schedules': () => json(200, { items: [], can_manage: true }),
    ...over,
  });
}

describe('the start screen', () => {
  it('analyzes a folder from one field and opens its run', async () => {
    const app = renderApp(personal());
    await flush();
    // The root form is folded away.
    expect((screen.getByLabelText(t.projects.relativePath).closest('details') as HTMLDetailsElement).open).toBe(false);
    type(t.quick.field, '/home/me/app');
    fireEvent.click(screen.getByRole('button', { name: t.quick.submit }));
    await flush();
    const created = app.calls.find(c => c.method === 'POST' && c.url === '/api/v1/projects')!;
    expect(created.body).toEqual({ local_path: '/home/me/app', analysis: { ...ANALYSIS_ON, fetch_dependencies: true }, open_existing: true });
    expect(app.calls.some(c => c.method === 'POST' && c.url === '/api/v1/projects/p-folder/runs')).toBe(true);
    expect(screen.getByRole('heading', { level: 2, name: 'app' })).toBeTruthy();
    expect(screen.getByText(t.runs.statusLabels.running!)).toBeTruthy();
  });

  it('takes a GitHub /tree/ address with its branch and the repository defaults, and shows a refused host', async () => {
    const app = renderApp(personal());
    await flush();
    type(t.quick.field, 'https://gitlab.com/o/r');
    fireEvent.click(screen.getByRole('button', { name: t.quick.submit }));
    expect(screen.getByRole('alert').textContent).toBe(t.quick.validation.hostNotAllowed('gitlab.com'));
    type(t.quick.field, 'https://github.com/o/r/tree/dev');
    expect((screen.getByLabelText(t.quick.branch) as HTMLInputElement).value).toBe('dev');
    expect((screen.getByLabelText(t.quick.runBuildScripts) as HTMLInputElement).checked).toBe(false);
    expect((screen.getByLabelText(t.quick.sendSource) as HTMLInputElement).checked).toBe(true);
    expect((screen.getByLabelText(t.quick.fetchDependencies) as HTMLInputElement).checked).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: t.quick.submit }));
    await flush();
    const created = app.calls.find(c => c.method === 'POST' && c.url === '/api/v1/projects')!;
    expect(created.body).toEqual({ source: { kind: 'git', provider_id: 'gh', repository_url: 'https://github.com/o/r', branch: 'dev' }, analysis: { send_source_to_model: true, run_build_scripts: false, fetch_dependencies: true }, open_existing: true });
  });

  it('sends the dependency choice the user turned off', async () => {
    const current = { ...SETUP, analysis_defaults: { local: { ...ANALYSIS_ON, fetch_dependencies: true }, git: { send_source_to_model: true, run_build_scripts: false, fetch_dependencies: true } } };
    const app = renderApp(personal({ 'GET /api/v1/setup': () => json(200, current) }));
    await flush();
    type(t.quick.field, '/home/me/app');
    const box = screen.getByLabelText(t.quick.fetchDependencies) as HTMLInputElement;
    expect(box.checked).toBe(true);
    fireEvent.click(box);
    fireEvent.click(screen.getByRole('button', { name: t.quick.submit }));
    await flush();
    const created = app.calls.find(c => c.method === 'POST' && c.url === '/api/v1/projects')!;
    expect(created.body).toEqual({ local_path: '/home/me/app', analysis: { ...ANALYSIS_ON, fetch_dependencies: false }, open_existing: true });
  });

  it('tells a user with no allowed folder to use a Git address or ask the administrator, without asking the server', async () => {
    const app = renderApp(personal({ 'GET /api/v1/setup': () => json(200, MEMBER_SETUP) }));
    await flush();
    // What is not available is said under the field.
    const limits = document.getElementById('start-unavailable')!;
    expect(limits.textContent).toContain(t.quick.reasons.no_allowed_folder);
    expect(limits.textContent).toContain(t.quick.reasons.not_offered);
    type(t.quick.field, '/srv/team/app');
    fireEvent.click(screen.getByRole('button', { name: t.quick.submit }));
    await flush();
    const alert = document.getElementById('start-folder-error')!;
    expect(alert.getAttribute('role')).toBe('alert');
    expect(alert.textContent).toContain('이 서버에서 읽도록 허용된 폴더가 없습니다. Git 주소를 쓰거나 관리자에게 폴더 허용을 요청하세요.');
    expect(alert.textContent).toContain(t.quick.allowedFoldersNone);
    expect(app.calls.some(c => c.method === 'POST' && c.url === '/api/v1/projects')).toBe(false);
    // The build-scripts box is off, cannot be turned on, and says why.
    const box = screen.getByLabelText(t.quick.runBuildScripts) as HTMLInputElement;
    expect(box.checked).toBe(false);
    expect(box.disabled).toBe(true);
    expect(document.querySelector('[data-unavailable="run_build_scripts"]')!.textContent).toContain(t.quick.reasons.not_offered);
    // A Git address still works.
    type(t.quick.field, 'https://github.com/o/r');
    expect(document.getElementById('start-folder-error')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: t.quick.submit }));
    await flush();
    expect(app.calls.find(c => c.method === 'POST' && c.url === '/api/v1/projects')!.body).toMatchObject({ analysis: { run_build_scripts: false } });
  });

  it('shows the folders the server allows with its refusal of a path', async () => {
    renderApp(personal({ 'POST /api/v1/projects': () => apiError(422, 'FOLDER_NOT_READABLE', 'outside', false, { readable_folders: ['/srv/team', '/home/me'] }) }));
    await flush();
    type(t.quick.field, '/opt/elsewhere');
    fireEvent.click(screen.getByRole('button', { name: t.quick.submit }));
    await flush();
    const alert = document.getElementById('start-folder-error')!;
    expect(alert.textContent).toContain(t.quick.outsideFolders);
    expect(alert.textContent).toContain('/srv/team');
    expect(alert.textContent).toContain('/home/me');
  });

  it('says there is no allowed folder when the server answers LOCAL_PATH_NOT_ALLOWED', async () => {
    renderApp(personal({ 'POST /api/v1/projects': () => apiError(422, 'LOCAL_PATH_NOT_ALLOWED', 'no folder', false, { readable_folders: [] }) }));
    await flush();
    type(t.quick.field, '/home/me/app');
    fireEvent.click(screen.getByRole('button', { name: t.quick.submit }));
    await flush();
    expect(document.getElementById('start-folder-error')!.textContent).toContain(t.quick.noFolder);
  });

  it('opens the run still going for a folder registered before instead of requesting another', async () => {
    const going = { ...FOLDER, activity: { stages: [], last_event_at: null, checked_at: null, detail: 'recorded', run: { id: 'run-f', status: 'running', requested_at: '2026-11-19T01:00:00Z' }, other_active_runs: 0, last_report: null } };
    const app = renderApp(personal({ 'POST /api/v1/projects': () => json(200, going) }));
    await flush();
    type(t.quick.field, '/home/me/app');
    fireEvent.click(screen.getByRole('button', { name: t.quick.submit }));
    await flush();
    expect(app.calls.some(c => c.url === '/api/v1/projects/p-folder/runs')).toBe(false);
    expect(screen.getByText(t.runs.statusLabels.running!)).toBeTruthy();
  });

  it('names the folder that holds CODEYE data', async () => {
    renderApp(personal({ 'POST /api/v1/projects': () => apiError(422, 'FOLDER_HOLDS_CODEYE_DATA', 'the folder /home/me contains CODEYE\'s data directory /home/me/.local/state/codeye (database password, model key); pick a subfolder that holds the project instead') }));
    await flush();
    type(t.quick.field, '/home/me');
    fireEvent.click(screen.getByRole('button', { name: t.quick.submit }));
    await flush();
    const alert = screen.getByRole('alert');
    expect(alert.textContent).toContain(t.errors.codes.FOLDER_HOLDS_CODEYE_DATA!);
    expect(alert.textContent).toContain('/home/me/.local/state/codeye');
  });
});

describe('retry, report and hints', () => {
  it('retries a failed run and follows the new run', async () => {
    const app = renderApp(personal({
      'GET /api/v1/runs/run-f': () => json(200, runView({ id: 'run-f', project_id: 'p-folder', status: 'failed', version: 3 }, { stages: undefined, detail: 'recorded', stages_list: undefined })),
      'POST /api/v1/runs/run-f/retry': () => json(202, { id: 'run-n', status: 'queued', version: 1, url: '/api/v1/runs/run-n', retry: 'new_run' }),
      'GET /api/v1/runs/run-n': () => json(200, runView({ id: 'run-n', project_id: 'p-folder', status: 'queued' })),
    }));
    await flush();
    type(t.quick.field, '/home/me/app');
    fireEvent.click(screen.getByRole('button', { name: t.quick.submit }));
    await flush();
    fireEvent.click(screen.getByRole('button', { name: t.runs.retry }));
    await flush();
    expect(app.calls.some(c => c.method === 'POST' && c.url === '/api/v1/runs/run-f/retry')).toBe(true);
    expect(screen.getByText(t.runs.retried('new_run'))).toBeTruthy();
    expect(screen.getByText('run-n')).toBeTruthy();
    expect(screen.queryByRole('button', { name: t.runs.retry })).toBeNull();
  });

  it('a recovery_required run runs again only after the user checks that no process is left', async () => {
    const app = renderApp(personal({
      'GET /api/v1/runs/run-f': () => json(200, runView({ id: 'run-f', project_id: 'p-folder', status: 'recovery_required', version: 4, raw_evidence_removed_at: '2026-11-19T01:00:00Z' })),
      'POST /api/v1/runs/run-f/recover': () => json(202, { id: 'run-n', status: 'queued', version: 1, url: '/api/v1/runs/run-n', retry: 'new_run', attempt_id: 'a1', epoch: 1 }),
      'GET /api/v1/runs/run-n': () => json(200, runView({ id: 'run-n', project_id: 'p-folder', status: 'queued' })),
    }));
    await flush();
    type(t.quick.field, '/home/me/app');
    fireEvent.click(screen.getByRole('button', { name: t.quick.submit }));
    await flush();
    expect(screen.getByText(t.runs.recoverTitle)).toBeTruthy();
    expect(screen.getByRole('note').textContent).toContain('원시 증거가 삭제됨');
    // No cancel for a run that needs recovery; the button waits for the check box.
    expect(screen.queryByRole('button', { name: t.runs.cancel })).toBeNull();
    const recover = screen.getByRole('button', { name: t.runs.recover }) as HTMLButtonElement;
    expect(recover.disabled).toBe(true);
    fireEvent.click(screen.getByLabelText(t.runs.recoverCheck));
    fireEvent.click(recover);
    await flush();
    const call = app.calls.find(c => c.method === 'POST' && c.url === '/api/v1/runs/run-f/recover');
    expect(call?.body).toEqual({ cleanup_confirmed: true });
    expect(screen.getByText(t.runs.recovered('new_run'))).toBeTruthy();
    expect(screen.getByText('run-n')).toBeTruthy();
  });

  it('opens the published report by itself and lists what would make it better, turning the build scripts on with a retry', async () => {
    const app = renderApp(personal({
      'GET /api/v1/runs/run-r': () => json(200, runView({ id: 'run-r', project_id: 'p-repo', status: 'partial', version: 4, report: { revision: 1 } })),
      'GET /api/v1/runs/run-r/report': () => json(200, report('run-r')),
      'PUT /api/v1/projects/p-repo/analysis': call => json(200, { ...REPO, revision: 2, analysis: call.body }),
      'POST /api/v1/runs/run-r/retry': () => json(202, { id: 'run-r2', status: 'queued', version: 1, url: '/api/v1/runs/run-r2', retry: 'new_run' }),
      'GET /api/v1/runs/run-r2': () => json(200, runView({ id: 'run-r2', project_id: 'p-repo', status: 'queued' })),
    }));
    await flush();
    type(t.quick.field, 'https://github.com/o/r');
    fireEvent.click(screen.getByRole('button', { name: t.quick.submit }));
    await flush(10);
    expect(screen.getByRole('article', { name: '분석 보고서' })).toBeTruthy();
    const hints = screen.getByRole('region', { name: t.improve.title });
    const items = within(hints).getAllByRole('listitem').map(li => li.textContent);
    expect(items[0]).toContain(t.improve.tool('cppcheck', 'apt install cppcheck'));
    expect(items[1]).toContain(t.improve.model);
    expect(items[2]).toContain(t.improve.buildScriptsCrates('cargo-check'));
    fireEvent.click(within(hints).getByRole('button', { name: t.improve.buildScriptsAction }));
    await flush();
    const put = app.calls.find(c => c.method === 'PUT')!;
    expect(put.url).toBe('/api/v1/projects/p-repo/analysis');
    expect(put.body).toEqual({ send_source_to_model: true, run_build_scripts: true, fetch_dependencies: true });
    expect(app.calls.some(c => c.method === 'POST' && c.url === '/api/v1/runs/run-r/retry')).toBe(true);
    expect(screen.getByText('run-r2')).toBeTruthy();
  });

  it('turns dependency fetching on with a retry when an analyzer could not resolve the imports', async () => {
    const repo = { ...REPO, analysis: { send_source_to_model: true, run_build_scripts: false, fetch_dependencies: false } };
    const doc = report('run-r');
    doc.gaps = [{ section: 'environment', code: 'dependencies_not_fetched', subject: 'go-vet', detail: 'dependency_unavailable' }];
    doc.overview.sections.find((s: { key: string }) => s.key === 'gaps').counts.gaps = 1;
    const app = renderApp(personal({
      'POST /api/v1/projects': () => json(201, repo),
      'GET /api/v1/runs/run-r': () => json(200, runView({ id: 'run-r', project_id: 'p-repo', status: 'partial', version: 4, report: { revision: 1 } })),
      'GET /api/v1/runs/run-r/report': () => json(200, doc),
      'PUT /api/v1/projects/p-repo/analysis': call => json(200, { ...repo, revision: 2, analysis: call.body }),
      'POST /api/v1/runs/run-r/retry': () => json(202, { id: 'run-r2', status: 'queued', version: 1, url: '/api/v1/runs/run-r2', retry: 'new_run' }),
      'GET /api/v1/runs/run-r2': () => json(200, runView({ id: 'run-r2', project_id: 'p-repo', status: 'queued' })),
    }));
    await flush();
    type(t.quick.field, 'https://github.com/o/r');
    fireEvent.click(screen.getByRole('button', { name: t.quick.submit }));
    await flush(10);
    const hints = screen.getByRole('region', { name: t.improve.title });
    expect(within(hints).getByText(t.improve.fetchDependencies('go-vet'))).toBeTruthy();
    fireEvent.click(within(hints).getByRole('button', { name: t.improve.fetchDependenciesAction }));
    await flush();
    const put = app.calls.find(c => c.method === 'PUT')!;
    expect(put.body).toEqual({ send_source_to_model: true, run_build_scripts: false, fetch_dependencies: true });
    expect(app.calls.some(c => c.method === 'POST' && c.url === '/api/v1/runs/run-r/retry')).toBe(true);
  });
});

describe('tools panel', () => {
  it('shows the tools found and how to add a missing one, with the model and Git', async () => {
    renderApp(personal());
    await flush();
    fireEvent.click(screen.getByRole('button', { name: t.nav.doctor }));
    await flush();
    const table = screen.getByRole('table');
    const rows = within(table).getAllByRole('row').map(r => r.textContent);
    expect(rows[1]).toContain('go');
    expect(rows[1]).toContain(t.tools.statuses.found!);
    expect(rows[2]).toContain(t.tools.statuses.not_found!);
    expect(rows[2]).toContain('apt install cppcheck');
    expect(screen.getByText(t.tools.modelNone)).toBeTruthy();
    expect(screen.getByText(t.tools.gitOn('github.com'))).toBeTruthy();
    expect(screen.getByText('/home/me')).toBeTruthy();
  });

  it('hides the tool hints from a user who is not an administrator', async () => {
    const member = { ...SETUP, is_admin: false, tools: { ...SETUP.tools, tools: [{ name: 'cppcheck', status: 'not_found', version_tested: false }] } };
    renderApp(personal({ 'GET /api/v1/setup': () => json(200, member) }));
    await flush();
    fireEvent.click(screen.getByRole('button', { name: t.nav.doctor }));
    await flush();
    expect(screen.getByText(t.tools.pathsAdminOnly)).toBeTruthy();
    expect(screen.queryByText(t.tools.addAfter)).toBeNull();
  });

  it('keeps the doctor file view on a controller that recorded no setup', async () => {
    renderApp(backend({ 'GET /api/v1/setup': () => json(200, { ...SETUP, model: null, git: null, tools: null, readable_folders: [] }) }));
    await flush();
    fireEvent.click(screen.getByRole('button', { name: t.nav.doctor }));
    await flush();
    expect(screen.getByLabelText(t.open)).toBeTruthy();
  });
});
