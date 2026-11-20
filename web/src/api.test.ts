// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { ApiError, RUN_BODY, createApi } from './api';
import { PROJECT, SESSION, apiError, json, mockFetch, runView } from './test-utils';

const rejection = async (promise: Promise<unknown>) => promise.then(() => null, (e: unknown) => e as ApiError);

describe('request policy', () => {
  it('uses same-origin relative URLs with ambient cookie credentials and no cache', async () => {
    const m = mockFetch(() => json(200, { items: [] }));
    await createApi({ fetch: m.fetch }).listProviders();
    expect(m.calls[0]).toMatchObject({ method: 'GET', url: '/api/v1/providers', credentials: 'same-origin' });
    expect(m.fn.mock.calls[0]![1]).toMatchObject({ cache: 'no-store', redirect: 'error', referrerPolicy: 'no-referrer' });
  });

  it('sends the CSRF header only on unsafe methods and only after a session set it', async () => {
    const m = mockFetch(call => (call.method === 'GET' ? json(200, SESSION) : json(202, { id: 'run1', status: 'queued', version: 1 })));
    const api = createApi({ fetch: m.fetch });
    await api.cancelRun('run1');
    expect(m.calls[0]!.headers['x-codeye-csrf']).toBeUndefined();
    api.setCsrf('tok');
    await api.getSession();
    await api.cancelRun('run1');
    expect(m.calls[1]!.headers['x-codeye-csrf']).toBeUndefined(); // GET
    expect(m.calls[2]!.headers['x-codeye-csrf']).toBe('tok');
    api.setCsrf(null);
    await api.cancelRun('run1');
    expect(m.calls[3]!.headers['x-codeye-csrf']).toBeUndefined();
  });

  it('exchanges a launch token in the body only, never in the URL or headers', async () => {
    const m = mockFetch(() => json(200, SESSION));
    await createApi({ fetch: m.fetch }).createSession('launch-secret');
    const call = m.calls[0]!;
    expect(call).toMatchObject({ method: 'POST', url: '/api/v1/session', body: { launch_token: 'launch-secret' } });
    expect(call.url).not.toContain('launch-secret');
    expect(JSON.stringify(call.headers)).not.toContain('launch-secret');
  });

  it('creates runs with the fixed body and the given Idempotency-Key', async () => {
    const m = mockFetch(() => json(202, { id: 'run1', status: 'queued', version: 1, url: '/api/v1/runs/run1' }));
    const ack = await createApi({ fetch: m.fetch }).createRun('p/1', 'key-1');
    expect(m.calls[0]).toMatchObject({ method: 'POST', url: '/api/v1/projects/p%2F1/runs', body: RUN_BODY });
    expect(m.calls[0]!.headers['idempotency-key']).toBe('key-1');
    expect(ack).toEqual({ id: 'run1', status: 'queued', version: 1, url: '/api/v1/runs/run1' });
  });

  it('encodes pagination cursors', async () => {
    const m = mockFetch(() => json(200, { items: [], next_cursor: null }));
    await createApi({ fetch: m.fetch }).listProjects('a b&c');
    expect(m.calls[0]!.url).toBe('/api/v1/projects?cursor=a%20b%26c');
  });
});

describe('failures', () => {
  it('classifies a lost connection as a retryable network error', async () => {
    const e = await rejection(createApi({ fetch: mockFetch(() => 'network').fetch }).getRun('r'));
    expect(e).toMatchObject({ kind: 'network', retryable: true });
  });

  it('treats an HTML page with status 200 as an invalid response, not a result', async () => {
    const html = mockFetch(() => new Response('<!doctype html><title>dev</title>', { status: 200, headers: { 'Content-Type': 'text/html' } }));
    expect(await rejection(createApi({ fetch: html.fetch }).getSession())).toMatchObject({ kind: 'invalid_response' });
  });

  it('parses the documented error envelope', async () => {
    const m = mockFetch(() => apiError(409, 'CONFLICT', 'text', true));
    expect(await rejection(createApi({ fetch: m.fetch }).getRun('r'))).toMatchObject({ kind: 'http', status: 409, code: 'CONFLICT', retryable: true, requestId: 'req-1' });
  });

  it('falls back to status-derived defaults for a non-JSON error body', async () => {
    const m = mockFetch(() => new Response('bad gateway', { status: 502 }));
    expect(await rejection(createApi({ fetch: m.fetch }).getRun('r'))).toMatchObject({ code: 'http_502', retryable: true });
  });

  it('reports 401 to the session owner except for session calls themselves', async () => {
    const m = mockFetch(() => apiError(401, 'UNAUTHENTICATED'));
    const onUnauthorized = vi.fn();
    const api = createApi({ fetch: m.fetch, onUnauthorized });
    await rejection(api.getSession());
    await rejection(api.createSession('x'));
    expect(onUnauthorized).not.toHaveBeenCalled();
    await rejection(api.listProjects());
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
  });

  it('reports an abort distinctly from a network failure', async () => {
    const controller = new AbortController();
    const fetch = (async (_: unknown, init?: RequestInit) => new Promise<Response>((_, reject) => {
      init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
    })) as unknown as typeof globalThis.fetch;
    const pending = rejection(createApi({ fetch }).getRun('r', controller.signal));
    controller.abort();
    expect(await pending).toMatchObject({ kind: 'aborted' });
  });
});

describe('response validation', () => {
  it('requires the {items} wrapper for source roots and providers', async () => {
    const wrapped = mockFetch(() => json(200, { items: [{ id: 'r', label: 'L' }] }));
    expect(await createApi({ fetch: wrapped.fetch }).listSourceRoots()).toEqual([{ id: 'r', label: 'L' }]);
    const bare = mockFetch(() => json(200, [{ id: 'r', label: 'L' }]));
    expect(await rejection(createApi({ fetch: bare.fetch }).listSourceRoots())).toMatchObject({ kind: 'invalid_response' });
    const providers = mockFetch(() => json(200, { items: [{ id: 'g', kind: 'ghes', host: 'git.example' }] }));
    expect(await createApi({ fetch: providers.fetch }).listProviders()).toEqual([{ id: 'g', kind: 'ghes', host: 'git.example' }]);
  });

  it('accepts a project with or without last_run and rejects malformed projects', async () => {
    const page = (items: unknown[]) => mockFetch(() => json(200, { items, next_cursor: null }));
    const a = await createApi({ fetch: page([PROJECT, { ...PROJECT, id: 'p2', last_run: { id: 'r', status: 'queued' } }]).fetch }).listProjects();
    expect(a.items.map(p => p.last_run)).toEqual([null, { id: 'r', status: 'queued' }]);
    const { last_run: _omitted, ...withoutLastRun } = PROJECT;
    expect((await createApi({ fetch: page([withoutLastRun]).fetch }).listProjects()).items[0]!.last_run).toBeNull();
    for (const bad of [{ ...PROJECT, source: { kind: 'svn' } }, { ...PROJECT, revision: 0 }, { ...PROJECT, id: '' }]) {
      expect(await rejection(createApi({ fetch: page([bad]).fetch }).listProjects())).toMatchObject({ kind: 'invalid_response' });
    }
  });

  it('reads an absent fetch_dependencies as on and only an explicit false as off', async () => {
    const page = (items: unknown[]) => mockFetch(() => json(200, { items, next_cursor: null }));
    const old = { send_source_to_model: true, run_build_scripts: false };
    const projects = (await createApi({ fetch: page([
      { ...PROJECT, analysis: old },
      { ...PROJECT, id: 'p2', analysis: { ...old, fetch_dependencies: null } },
      { ...PROJECT, id: 'p3', analysis: { ...old, fetch_dependencies: false } },
    ]).fetch }).listProjects()).items;
    expect(projects.map(p => p.analysis?.fetch_dependencies)).toEqual([true, true, false]);
    expect(await rejection(createApi({ fetch: page([{ ...PROJECT, analysis: { ...old, fetch_dependencies: 'no' } }]).fetch }).listProjects())).toMatchObject({ kind: 'invalid_response' });
    const setup = { model: null, git: null, readable_folders: [], tools: null, analysis_defaults: { local: { send_source_to_model: true, run_build_scripts: true }, git: { ...old, fetch_dependencies: false } } };
    const s = await createApi({ fetch: mockFetch(() => json(200, setup)).fetch }).getSetup();
    expect(s.analysis_defaults).toEqual({ local: { send_source_to_model: true, run_build_scripts: true, fetch_dependencies: true }, git: { ...old, fetch_dependencies: false } });
  });

  it('reads what the service offers the signed-in user, and refuses a malformed answer', async () => {
    const setup = {
      is_admin: false, service_origin: 'http://codeye.lan:7331', model: { configured: true, id: 'p/m', probe: 'verified' },
      git: { available: true, hosts: ['github.com'] }, readable_folders: ['/srv/team'],
      analysis_defaults: { local: { send_source_to_model: true, run_build_scripts: false }, git: { send_source_to_model: true, run_build_scripts: false } },
      available: { local_path: { available: true, reason: null }, build_scripts: { available: false, reason: 'not_offered' }, a_later_item: { available: true, reason: null } },
      tools: null, github: { enabled: true, host: 'github.com', token: true, account: 'octo', source: 'web', reason: null }, offline: false,
    };
    const s = await createApi({ fetch: mockFetch(() => json(200, setup)).fetch }).getSetup();
    expect(s).toMatchObject({ is_admin: false, service_origin: 'http://codeye.lan:7331', readable_folders: ['/srv/team'], offline: false,
      github: { token: true, account: 'octo', source: 'web' } });
    expect(s.available).toEqual({ local_path: { available: true, reason: null }, build_scripts: { available: false, reason: 'not_offered' } });
    expect(await rejection(createApi({ fetch: mockFetch(() => json(200, { ...setup, available: { build_scripts: { available: 'no' } } })).fetch }).getSetup())).toMatchObject({ kind: 'invalid_response' });
  });

  it('calls the administrator routes', async () => {
    const m = mockFetch(call => call.method === 'DELETE' || call.url.endsWith('/everyone') ? new Response(null, { status: 204 })
      : call.url === '/api/v1/admin/settings' ? json(200, { others_model_review: false, others_build_scripts: false, updated_at: '2026-11-20T00:00:00Z', service_origin: null, service_addresses: [{ origin: 'http://127.0.0.1:7331', kind: 'this_machine' }] })
      : call.url === '/api/v1/admin/source-roots' ? json(201, { id: 'r1', label: 'nfs', path: '/mnt/nfs', everyone: true, created_at: '2026-11-20T00:00:00Z', grants: [] })
      : call.url === '/api/v1/admin/providers' ? json(201, { id: 'p1', kind: 'ghes', host: 'ghe.example.com', api_url: null })
      : json(404, {}));
    const api = createApi({ fetch: m.fetch });
    expect((await api.addSourceRoot({ path: '/mnt/nfs', everyone: true })).path).toBe('/mnt/nfs');
    await api.setSourceRootEveryone('r1', false);
    expect((await api.addProvider('ghe.example.com')).kind).toBe('ghes');
    const saved = await api.putSettings({ others_model_review: false });
    expect(saved.others_model_review).toBe(false);
    expect(saved.service_addresses).toEqual([{ origin: 'http://127.0.0.1:7331', kind: 'this_machine' }]);
    expect(m.calls.map(c => `${c.method} ${c.url} ${JSON.stringify(c.body ?? null)}`)).toEqual([
      'POST /api/v1/admin/source-roots {"path":"/mnt/nfs","everyone":true}',
      'PUT /api/v1/admin/source-roots/r1/everyone {"everyone":false}',
      'POST /api/v1/admin/providers {"host":"ghe.example.com"}',
      'PUT /api/v1/admin/settings {"others_model_review":false}',
    ]);
  });

  it('keeps the folders a refused path may use from the error details', async () => {
    const m = mockFetch(() => apiError(422, 'FOLDER_NOT_READABLE', 'outside', false, { readable_folders: ['/srv/team', 7, ''] }));
    const e = await rejection(createApi({ fetch: m.fetch }).createProject({ local_path: '/opt/x', open_existing: true }));
    expect(e).toMatchObject({ code: 'FOLDER_NOT_READABLE', readableFolders: ['/srv/team'] });
    expect(m.calls[0]!.body).toEqual({ local_path: '/opt/x', open_existing: true });
  });

  it('sends the dependency choice with the other analysis choices', async () => {
    const m = mockFetch(() => json(200, PROJECT));
    await createApi({ fetch: m.fetch }).setProjectAnalysis('p1', { send_source_to_model: true, run_build_scripts: false, fetch_dependencies: false });
    expect(m.calls[0]).toMatchObject({ method: 'PUT', url: '/api/v1/projects/p1/analysis', body: { send_source_to_model: true, run_build_scripts: false, fetch_dependencies: false } });
  });

  it('keeps an unrecognised run status as an opaque string and null progress as null', async () => {
    const view = runView({ status: 'teleporting' }, { total_tasks: null, estimated_percent: null });
    const run = await createApi({ fetch: mockFetch(() => json(200, view)).fetch }).getRun('run1');
    expect(run.status).toBe('teleporting');
    expect(run.progress).toMatchObject({ total_tasks: null, estimated_percent: null, waiting_reason: 'execution_unavailable' });
  });

  it.each([
    ['percent out of range', runView({}, { estimated_percent: 120 })],
    ['negative tasks', runView({}, { completed_tasks: -1 })],
    ['missing progress', { ...runView(), progress: undefined }],
    ['non-integer version', runView({ version: 1.5 })],
  ])('rejects a run view with %s', async (_, body) => {
    expect(await rejection(createApi({ fetch: mockFetch(() => json(200, body)).fetch }).getRun('run1'))).toMatchObject({ kind: 'invalid_response' });
  });

  it('rejects a session without a CSRF token', async () => {
    const { csrf_token: _t, ...partial } = SESSION;
    expect(await rejection(createApi({ fetch: mockFetch(() => json(200, partial)).fetch }).getSession())).toMatchObject({ kind: 'invalid_response' });
  });
});
