// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, screen, within } from '@testing-library/react';
import { advance, backend, flush, renderApp } from './harness';
import { ko as t } from './locales/ko';
import { PROJECT, SESSION, apiError, json, rateLimited, runView, type Call } from './test-utils';

beforeEach(() => vi.useFakeTimers());
afterEach(() => { cleanup(); vi.useRealTimers(); });

const m = t.accounts.members;
const OWNER = { user_id: 'a1d0f6b2-58c4-4e7a-b9f3-0c2e6d8a4b11', username: 'owner.kim', display_name: 'Kim', role: 'owner', disabled: false };
const ME = { user_id: SESSION.user_id, username: 'dana', display_name: 'Dana', role: 'manager', disabled: false };
const ERIN = { user_id: '6c2a9d14-31f8-4be0-8d57-91a3e0c4f7b2', username: 'erin', display_name: 'Erin', role: 'viewer', disabled: false };
const MEMBERS = { items: [OWNER, ME, ERIN] };
const AUDIT_ROW = {
  id: '4177', occurred_at: '2026-11-19T09:10:02.880Z', actor_kind: 'user', actor_user_id: OWNER.user_id, actor_username: 'owner.kim', actor_node_id: null,
  action: 'project.member_set', target_type: 'user', target_id: ERIN.user_id, project_id: 'p1', request_id: 'req_0a1b2c3d4e5f60718293a4b5',
  detail: { role: 'viewer', previous_role: null },
};

type Over = Record<string, (c: Call) => Response | undefined>;
async function open(project: Record<string, unknown>, over: Over = {}) {
  const app = renderApp(backend({
    'GET /api/v1/projects': () => json(200, { items: [{ ...PROJECT, ...project }], next_cursor: null }),
    'GET /api/v1/projects/p1/members': () => json(200, MEMBERS),
    ...over,
  }));
  await flush();
  fireEvent.click(screen.getByRole('button', { name: /sample/ }));
  return app;
}
const tab = (name: string) => fireEvent.click(within(screen.getByRole('navigation', { name: t.accounts.tabs.label })).getByRole('button', { name }));
const MANAGER = { role: 'manager', can_run: true, can_manage: true };
const VIEWER = { role: 'viewer', can_run: false, can_manage: false };

describe('role-gated project actions', () => {
  it('disables the run request for a viewer and says why, without hiding the run state', async () => {
    const { calls } = await open({ ...VIEWER, last_run: { id: 'run1', status: 'queued' } });
    const start = screen.getByRole('button', { name: t.runs.start }) as HTMLButtonElement;
    expect(start.disabled).toBe(true);
    expect(screen.getByText(t.accounts.runNotAllowed(t.accounts.roles.viewer!))).toBeTruthy();
    fireEvent.click(start);
    await flush();
    expect(calls.some(c => c.method === 'POST' && c.url.endsWith('/runs'))).toBe(false);
    // The run can still be watched, but not cancelled.
    // Selecting the project opens its latest run.
    await flush();
    expect(screen.getByText(t.runs.statusLabels.queued!)).toBeTruthy();
    expect(screen.queryByRole('button', { name: t.runs.cancel })).toBeNull();
    expect(screen.getByText(t.accounts.cancelNotAllowed)).toBeTruthy();
  });

  it('lets a runner request and cancel, and re-reads the project after a 403', async () => {
    let projectReads = 0;
    const { calls } = await open({}, {
      'GET /api/v1/projects': () => { projectReads++; return json(200, { items: [{ ...PROJECT, role: 'runner', can_run: true, can_manage: false }], next_cursor: null }); },
      'POST /api/v1/projects/p1/runs': () => apiError(403, 'FORBIDDEN'),
    });
    const before = projectReads;
    fireEvent.click(screen.getByRole('button', { name: t.runs.start }));
    await flush();
    expect(screen.getByRole('alert').textContent).toContain(t.errors.forbidden);
    expect(projectReads).toBeGreaterThan(before);
    expect(calls.filter(c => c.method === 'POST' && c.url.endsWith('/runs'))).toHaveLength(1);
  });

  it('keeps cancel for a runner with an active run', async () => {
    await open({ role: 'runner', can_run: true, can_manage: false, last_run: { id: 'run1', status: 'queued' } }, { 'GET /api/v1/runs/run1': () => json(200, runView()) });
    // Selecting the project opens its latest run.
    await flush();
    expect(screen.getByRole('button', { name: t.runs.cancel })).toBeTruthy();
  });

  it('offers the audit tab only to the owner and managers', async () => {
    await open(VIEWER);
    const nav = screen.getByRole('navigation', { name: t.accounts.tabs.label });
    expect(within(nav).queryByRole('button', { name: t.accounts.tabs.audit })).toBeNull();
    cleanup();
    await open(MANAGER);
    expect(within(screen.getByRole('navigation', { name: t.accounts.tabs.label })).getByRole('button', { name: t.accounts.tabs.audit })).toBeTruthy();
  });
});

describe('members panel', () => {
  it('shows a non-manager only their own role and never asks for the member list', async () => {
    const { calls } = await open(VIEWER);
    tab(t.accounts.tabs.members);
    await flush();
    expect(screen.getByText(m.myRole(t.accounts.roles.viewer!))).toBeTruthy();
    expect(screen.getByText(t.accounts.roleAllows.viewer!)).toBeTruthy();
    expect(screen.queryByLabelText(m.addUsername)).toBeNull();
    expect(calls.some(c => c.url.includes('/members'))).toBe(false);
  });

  it('lists members with the owner row fixed', async () => {
    await open(MANAGER);
    tab(t.accounts.tabs.members);
    await flush();
    const table = screen.getByRole('table', { name: m.caption });
    const rows = within(table).getAllByRole('row').slice(1);
    expect(rows).toHaveLength(3);
    expect(rows[0]!.textContent).toContain('Kim (owner.kim)');
    expect(rows[0]!.textContent).toContain(m.ownerFixed);
    expect(within(rows[0]!).queryByRole('combobox')).toBeNull();
    expect(within(rows[0]!).queryByRole('button')).toBeNull();
    expect(rows[1]!.textContent).toContain(m.you);
    expect((within(rows[2]!).getByRole('combobox', { name: m.roleFor('Erin (erin)') }) as HTMLSelectElement).value).toBe('viewer');
  });

  it('adds a member by exact username and shows the generic not-found answer', async () => {
    let added = false;
    const { calls } = await open(MANAGER, {
      'POST /api/v1/projects/p1/members': c => {
        if ((c.body as { username: string }).username === 'nobody') return apiError(404, 'NOT_FOUND');
        added = true;
        return json(200, { user_id: 'f0000000-0000-4000-8000-000000000001', username: 'finn', display_name: null, role: 'runner', disabled: false });
      },
      'GET /api/v1/projects/p1/members': () => json(200, added ? { items: [...MEMBERS.items, { user_id: 'f0000000-0000-4000-8000-000000000001', username: 'finn', display_name: null, role: 'runner', disabled: false }] } : MEMBERS),
    });
    tab(t.accounts.tabs.members);
    await flush();
    fireEvent.change(screen.getByLabelText(m.addUsername), { target: { value: 'nobody' } });
    fireEvent.click(screen.getByRole('button', { name: m.addSubmit }));
    await flush();
    expect(screen.getByRole('alert').textContent).toContain(m.addNotFound);
    fireEvent.change(screen.getByLabelText(m.addUsername), { target: { value: ' finn ' } });
    fireEvent.change(screen.getByLabelText(m.addRole), { target: { value: 'runner' } });
    fireEvent.click(screen.getByRole('button', { name: m.addSubmit }));
    await flush();
    const post = calls.filter(c => c.method === 'POST' && c.url === '/api/v1/projects/p1/members');
    expect(post.map(c => c.body)).toEqual([{ username: 'nobody', role: 'viewer' }, { username: 'finn', role: 'runner' }]);
    expect(post[1]!.headers['x-codeye-csrf']).toBe(SESSION.csrf_token);
    expect(screen.getByRole('status').textContent).toBe(m.added('finn'));
    expect(within(screen.getByRole('table', { name: m.caption })).getAllByRole('row')).toHaveLength(5);
    expect((screen.getByLabelText(m.addUsername) as HTMLInputElement).value).toBe('');
  });

  it('waits after too many failed lookups', async () => {
    await open(MANAGER, { 'POST /api/v1/projects/p1/members': () => rateLimited(4) });
    tab(t.accounts.tabs.members);
    await flush();
    fireEvent.change(screen.getByLabelText(m.addUsername), { target: { value: 'guess' } });
    fireEvent.click(screen.getByRole('button', { name: m.addSubmit }));
    await flush();
    expect(screen.getByRole('alert').textContent).toBe(m.addThrottled(4));
    expect((screen.getByRole('button', { name: m.addSubmit }) as HTMLButtonElement).disabled).toBe(true);
    await advance(4000);
    expect((screen.getByRole('button', { name: m.addSubmit }) as HTMLButtonElement).disabled).toBe(false);
  });

  it('changes a role and removes a member after confirmation', async () => {
    const { calls } = await open(MANAGER, {
      'PUT /api/v1/projects/p1/members/6c2a9d14-31f8-4be0-8d57-91a3e0c4f7b2': () => json(200, { ...ERIN, role: 'runner' }),
      'DELETE /api/v1/projects/p1/members/6c2a9d14-31f8-4be0-8d57-91a3e0c4f7b2': () => new Response(null, { status: 204 }),
    });
    tab(t.accounts.tabs.members);
    await flush();
    const row = () => within(screen.getByRole('table', { name: m.caption })).getAllByRole('row')[3]!;
    const save = within(row()).getByRole('button', { name: m.save }) as HTMLButtonElement;
    expect(save.disabled).toBe(true); // nothing changed yet
    fireEvent.change(within(row()).getByRole('combobox'), { target: { value: 'runner' } });
    fireEvent.click(within(row()).getByRole('button', { name: m.save }));
    await flush();
    expect(calls.find(c => c.method === 'PUT')!.body).toEqual({ role: 'runner' });
    expect(screen.getByRole('status').textContent).toBe(m.saved('Erin (erin)'));
    fireEvent.click(within(row()).getByRole('button', { name: m.removeFor('Erin (erin)') }));
    expect(calls.some(c => c.method === 'DELETE')).toBe(false);
    expect(within(row()).getByText(m.removeConfirm('Erin (erin)'))).toBeTruthy();
    fireEvent.click(within(row()).getByRole('button', { name: m.removeYes }));
    await flush();
    expect(calls.filter(c => c.method === 'DELETE').map(c => c.url)).toEqual(['/api/v1/projects/p1/members/6c2a9d14-31f8-4be0-8d57-91a3e0c4f7b2']);
  });

  it('warns before a manager removes themself', async () => {
    await open(MANAGER);
    tab(t.accounts.tabs.members);
    await flush();
    const row = within(screen.getByRole('table', { name: m.caption })).getAllByRole('row')[2]!;
    fireEvent.click(within(row).getByRole('button', { name: m.removeFor('Dana (dana)') }));
    expect(within(row).getByText(new RegExp(m.removeSelf))).toBeTruthy();
  });

  it('falls back to the read-only view when the server says the role is gone', async () => {
    let lost = false;
    await open(MANAGER, {
      'GET /api/v1/projects': () => json(200, { items: [{ ...PROJECT, ...(lost ? VIEWER : MANAGER) }], next_cursor: null }),
      'PUT /api/v1/projects/p1/members/6c2a9d14-31f8-4be0-8d57-91a3e0c4f7b2': () => { lost = true; return apiError(403, 'FORBIDDEN'); },
    });
    tab(t.accounts.tabs.members);
    await flush();
    const row = within(screen.getByRole('table', { name: m.caption })).getAllByRole('row')[3]!;
    fireEvent.change(within(row).getByRole('combobox'), { target: { value: 'manager' } });
    fireEvent.click(within(row).getByRole('button', { name: m.save }));
    await flush();
    expect(screen.getByText(m.myRole(t.accounts.roles.viewer!))).toBeTruthy();
    expect(screen.queryByRole('table', { name: m.caption })).toBeNull();
  });
});

describe('project audit tab', () => {
  it('reads the project log newest first, filters by member and action, and loads more by cursor', async () => {
    const { calls } = await open(MANAGER, {
      'GET /api/v1/projects/p1/audit': c => json(200, c.url.includes('cursor=4177')
        ? { items: [{ ...AUDIT_ROW, id: '4100', action: 'run.requested', detail: {} }], next_cursor: null }
        : { items: [AUDIT_ROW], next_cursor: '4177' }),
    });
    tab(t.accounts.tabs.audit);
    await flush();
    const a = t.accounts.audit;
    expect(calls.filter(c => c.url.startsWith('/api/v1/projects/p1/audit')).map(c => c.url)).toEqual(['/api/v1/projects/p1/audit']);
    const table = () => screen.getByRole('table', { name: a.caption });
    expect(within(table()).getAllByRole('row')[1]!.textContent).toContain(a.actions['project.member_set']!);
    expect(within(table()).getAllByRole('row')[1]!.textContent).toContain('사용자 owner.kim');
    // No project filter on a project's own log; actors are the members.
    expect(screen.queryByLabelText(a.project)).toBeNull();
    fireEvent.change(screen.getByLabelText(a.actor), { target: { value: OWNER.user_id } });
    fireEvent.change(screen.getByLabelText(a.action), { target: { value: 'project.member_set' } });
    fireEvent.click(screen.getByRole('button', { name: a.apply }));
    await flush();
    expect(calls.filter(c => c.url.startsWith('/api/v1/projects/p1/audit')).at(-1)!.url)
      .toBe(`/api/v1/projects/p1/audit?actor=${OWNER.user_id}&action=project.member_set`);
    fireEvent.click(screen.getByRole('button', { name: a.more }));
    await flush();
    expect(calls.filter(c => c.url.startsWith('/api/v1/projects/p1/audit')).at(-1)!.url)
      .toBe(`/api/v1/projects/p1/audit?actor=${OWNER.user_id}&action=project.member_set&cursor=4177`);
    expect(within(table()).getAllByRole('row')).toHaveLength(3);
    expect(screen.queryByRole('button', { name: a.more })).toBeNull();
    expect(screen.getByText(a.end)).toBeTruthy();
  });

  it('shows a load error with a retry', async () => {
    let fail = true;
    await open(MANAGER, { 'GET /api/v1/projects/p1/audit': () => (fail ? apiError(503, 'UNAVAILABLE', 'm', true) : json(200, { items: [], next_cursor: null })) });
    tab(t.accounts.tabs.audit);
    await flush();
    expect(screen.getByRole('alert').textContent).toContain(t.accounts.audit.loadFailed);
    fail = false;
    fireEvent.click(screen.getByRole('button', { name: t.accounts.audit.reload }));
    await flush();
    expect(screen.getByText(t.accounts.audit.empty)).toBeTruthy();
  });
});
