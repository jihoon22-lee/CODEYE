// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, screen } from '@testing-library/react';
import { backend, flush, renderApp } from './harness';
import { ko as t } from './locales/ko';
import { pullRequestOf } from './pullRequest';
import { parseTarget } from './quickStart';
import { PROJECT, apiError, json, runView, type Call } from './test-utils';

beforeEach(() => vi.useFakeTimers());
afterEach(() => { cleanup(); vi.useRealTimers(); });

const SETUP = {
  mode: 'personal',
  model: { configured: false, id: null, probe: null },
  git: { available: true, hosts: ['github.com'] },
  readable_folders: ['/home/me'],
  analysis_defaults: { local: { send_source_to_model: true, run_build_scripts: true, fetch_dependencies: true }, git: { send_source_to_model: true, run_build_scripts: false, fetch_dependencies: true } },
  tools: null,
  github: { enabled: true, host: 'github.com', token: true, account: 'octo', source: 'gh', reason: null },
};
const REPO = { ...PROJECT, id: 'p-repo', name: 'widget', can_manage: true, source: { kind: 'git', provider_id: 'gh', repository_url: 'https://github.com/acme/widget' }, analysis: { send_source_to_model: true, run_build_scripts: false, fetch_dependencies: true } };
const PR = {
  number: 7, url: 'https://github.com/acme/widget/pull/7', repository: 'github.com/acme/widget', title: 'Fix', base_ref: 'main', base_sha: 'a'.repeat(40),
  head_ref: 'fix', head_sha: 'b'.repeat(40), head_repository: 'someone/widget', changed_files: 3, resolved_at: '2026-11-20T00:00:00Z',
  comment: { requested: true, state: 'posted', attempts: 1, next_attempt_at: null, reason: null, url: 'https://github.com/acme/widget/pull/7#issuecomment-9', updated_at: '2026-11-20T00:01:00Z' },
};
const type = (label: string, value: string) => fireEvent.change(screen.getByLabelText(label), { target: { value } });

function app(over: Record<string, (call: Call) => Response | undefined> = {}) {
  return backend({
    'GET /api/v1/setup': () => json(200, SETUP),
    'GET /api/v1/projects': () => json(200, { items: [], next_cursor: null }),
    'POST /api/v1/projects': () => json(201, REPO),
    'PUT /api/v1/projects/p-repo/analysis': call => json(200, { ...REPO, analysis: call.body }),
    'POST /api/v1/projects/p-repo/runs': () => json(202, { id: 'run-p', status: 'queued', version: 1, url: '/api/v1/runs/run-p' }),
    'GET /api/v1/runs/run-p': () => json(200, { ...runView({ id: 'run-p', project_id: 'p-repo', status: 'running' }), pull_request: PR }),
    'GET /api/v1/projects/p-repo/schedules': () => json(200, { items: [], can_manage: true }),
    ...over,
  });
}

describe('pull-request addresses', () => {
  it('reads /pull/N (and its tabs) as a pull request of the repository', () => {
    expect(parseTarget('https://github.com/acme/widget/pull/7').target).toEqual({ kind: 'git', url: 'https://github.com/acme/widget', host: 'github.com', branch: null, pullRequest: 7 });
    expect(parseTarget('github.com/acme/widget/pull/12/files').target).toMatchObject({ url: 'https://github.com/acme/widget', pullRequest: 12 });
    expect(pullRequestOf('/acme/widget/pull/0')).toBeNull();
    expect(pullRequestOf('/acme/widget/pulls')).toBeNull();
    expect(parseTarget('https://github.com/acme/widget').target).not.toHaveProperty('pullRequest');
  });

  it('queues a pull-request run with the comment the user turned on and remembers it', async () => {
    const a = renderApp(app());
    await flush();
    type(t.quick.field, 'https://github.com/acme/widget/pull/7');
    const box = screen.getByLabelText(t.pullRequest.commentBox) as HTMLInputElement;
    expect(box.checked).toBe(false);
    fireEvent.click(box);
    fireEvent.click(screen.getByRole('button', { name: t.quick.submit }));
    await flush();
    const created = a.calls.find(c => c.method === 'POST' && c.url === '/api/v1/projects')!;
    expect(created.body).toMatchObject({ source: { kind: 'git', repository_url: 'https://github.com/acme/widget' } });
    expect(a.calls.find(c => c.method === 'PUT')!.body).toMatchObject({ comment_on_pull_request: true });
    const run = a.calls.find(c => c.method === 'POST' && c.url === '/api/v1/projects/p-repo/runs')!;
    expect(run.body).toEqual({ profile: 'deep-review', source_policy: 'latest_at_acquire', scope: { kind: 'full' }, pull_request: { number: 7, comment: true } });
    // The run shows its pull request and comment state.
    expect(screen.getByText(t.pullRequest.line(7, 'acme/widget'))).toBeTruthy();
    expect(screen.getByText(t.pullRequest.commentStates.posted!, { exact: false })).toBeTruthy();
  });

  it('leaves the comment to the project when untouched and shows GitHub refusals', async () => {
    const a = renderApp(app({ 'POST /api/v1/projects/p-repo/runs': () => apiError(422, 'PULL_REQUEST_NOT_FOUND') }));
    await flush();
    type(t.quick.field, 'https://github.com/acme/widget/pull/8');
    fireEvent.click(screen.getByRole('button', { name: t.quick.submit }));
    await flush();
    const run = a.calls.find(c => c.method === 'POST' && c.url === '/api/v1/projects/p-repo/runs')!;
    expect(run.body).toMatchObject({ pull_request: { number: 8 } });
    expect((run.body as { pull_request: object }).pull_request).not.toHaveProperty('comment');
    expect(a.calls.some(c => c.method === 'PUT')).toBe(false);
    expect(screen.getByText(t.pullRequest.errors.PULL_REQUEST_NOT_FOUND!, { exact: false })).toBeTruthy();
  });

  it('previews the comment of a reported run without posting', async () => {
    const reported = { ...runView({ id: 'run-p', project_id: 'p-repo', status: 'completed' }), report: { revision: 1, status: 'completed', sha256: 'c'.repeat(64), url: '/api/v1/runs/run-p/report?revision=1' }, pull_request: PR };
    const a = renderApp(app({
      'GET /api/v1/runs/run-p': () => json(200, reported),
      'GET /api/v1/runs/run-p/report': () => apiError(404, 'NOT_FOUND'),
      'GET /api/v1/runs/run-p/pull-request-comment': () => json(200, { run_id: 'run-p', report_revision: 1, format: 'markdown', body: '### CODEYE 분석 결과\n- 분석한 head', head_sha: PR.head_sha, current_head_sha: 'd'.repeat(40), head_moved: true, comment: PR.comment }),
    }));
    await flush();
    type(t.quick.field, 'https://github.com/acme/widget/pull/7');
    fireEvent.click(screen.getByRole('button', { name: t.quick.submit }));
    await flush();
    fireEvent.click(screen.getByRole('button', { name: t.pullRequest.preview }));
    await flush();
    expect(screen.getByLabelText(t.pullRequest.preview).textContent).toContain('CODEYE 분석 결과');
    expect(screen.getByText(t.pullRequest.headMoved('ddddddd'), { exact: false })).toBeTruthy();
    expect(a.calls.filter(c => c.url.includes('pull-request-comment')).every(c => c.method === 'GET')).toBe(true);
  });
});
