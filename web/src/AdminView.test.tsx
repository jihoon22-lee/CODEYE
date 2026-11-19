// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, screen, within } from '@testing-library/react';
import { backend, flush, renderApp } from './harness';
import { ko as t } from './locales/ko';
import { SESSION, apiError, json, type Call } from './test-utils';

beforeEach(() => vi.useFakeTimers());
afterEach(() => { cleanup(); vi.useRealTimers(); localStorage.clear(); sessionStorage.clear(); });

const a = t.accounts.admin;
const ADMIN = { ...SESSION, is_admin: true };
const user = (over: Record<string, unknown>) => ({
  id: SESSION.user_id, username: 'dana', display_name: 'Dana', is_admin: true, must_change_password: false, has_password: true,
  disabled: false, disabled_at: null, created_at: '2026-11-01T09:00:00Z', last_login_at: '2026-11-19T08:00:00Z', ...over,
});
const DANA = user({});
const ERIN = user({ id: '6c2a9d14-31f8-4be0-8d57-91a3e0c4f7b2', username: 'erin', display_name: 'Erin', is_admin: false, must_change_password: true, has_password: false, last_login_at: null });
const LINK = 'https://codeye.example/#launch=ONE-TIME-LINK-SECRET-0000000000000000000';
const ROOT = { id: 'r0000000-0000-4000-8000-000000000001', label: 'team-root', created_at: '2026-11-01T00:00:00Z', grants: [{ user_id: SESSION.user_id, username: 'dana', granted_at: '2026-11-02T00:00:00Z' }] };

type Over = Record<string, (c: Call) => Response | undefined>;
async function openAdmin(over: Over = {}) {
  const app = renderApp(backend({
    'GET /api/v1/session': () => json(200, ADMIN),
    'GET /api/v1/admin/users': c => json(200, c.url.includes('cursor=')
      ? { items: [ERIN], next_cursor: null }
      : { items: [DANA], next_cursor: SESSION.user_id }),
    'GET /api/v1/admin/source-roots': () => json(200, { items: [ROOT] }),
    'GET /api/v1/admin/providers': () => json(200, { items: [{ id: 'gh', kind: 'github', host: 'github.com', api_url: null }] }),
    'GET /api/v1/audit': () => json(200, { items: [], next_cursor: null }),
    ...over,
  }));
  await flush();
  fireEvent.click(screen.getByRole('button', { name: t.accounts.nav.admin }));
  await flush();
  return app;
}
const adminTab = (name: string) => fireEvent.click(within(screen.getByRole('navigation', { name: a.tabs.label })).getByRole('button', { name }));
const userRow = (name: RegExp) => within(screen.getByRole('table', { name: a.usersCaption })).getAllByRole('row').find(r => name.test(r.textContent ?? ''))!;

describe('admin navigation', () => {
  it('has no admin area for a non-admin', async () => {
    const { calls } = renderApp();
    await flush();
    expect(screen.queryByRole('button', { name: t.accounts.nav.admin })).toBeNull();
    expect(calls.some(c => c.url.startsWith('/api/v1/admin') || c.url.startsWith('/api/v1/audit'))).toBe(false);
  });

  it('opens the admin area for an admin and says it grants no project access', async () => {
    await openAdmin();
    expect(screen.getByRole('heading', { name: a.title, level: 1 })).toBeTruthy();
    expect(screen.getByText(a.noProjectAccess)).toBeTruthy();
  });

  it('drops the admin area when the server says the role is gone', async () => {
    let admin = true;
    await openAdmin({
      'GET /api/v1/session': () => json(200, admin ? ADMIN : SESSION),
      'GET /api/v1/admin/providers': () => { admin = false; return apiError(403, 'FORBIDDEN'); },
    });
    adminTab(a.tabs.providers);
    await flush();
    expect(screen.queryByRole('button', { name: t.accounts.nav.admin })).toBeNull();
    expect(screen.getByRole('heading', { name: t.projects.title })).toBeTruthy();
  });
});

describe('admin users', () => {
  it('lists every page of users with their state', async () => {
    const { calls } = await openAdmin();
    expect(calls.filter(c => c.url.startsWith('/api/v1/admin/users')).map(c => c.url))
      .toEqual(['/api/v1/admin/users?limit=200', `/api/v1/admin/users?limit=200&cursor=${SESSION.user_id}`]);
    expect(userRow(/Dana/).textContent).toContain(a.badgeAdmin);
    expect(userRow(/Dana/).textContent).toContain(a.you);
    expect(userRow(/Erin/).textContent).toContain(a.badgeMustChange);
    expect(userRow(/Erin/).textContent).toContain(a.neverLoggedIn);
    // No reset for oneself: the server refuses it.
    expect(within(userRow(/Dana/)).queryByRole('button', { name: a.reset })).toBeNull();
  });

  it('shows a created user\'s login link once, with a copy button, and never again after closing', async () => {
    const writeText = vi.fn(async () => {});
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
    const { calls, container } = await openAdmin({
      'POST /api/v1/admin/users': () => json(201, { user: user({ id: 'f0000000-0000-4000-8000-000000000002', username: 'finn', display_name: null, is_admin: false, must_change_password: true, has_password: false }), login_link: { url: LINK, expires_at: '2026-11-19T09:10:00Z' } }),
    });
    fireEvent.change(screen.getByLabelText(a.createUsername), { target: { value: 'Finn' } });
    fireEvent.click(screen.getByRole('button', { name: a.createSubmit }));
    await flush();
    expect(calls.find(c => c.method === 'POST' && c.url === '/api/v1/admin/users')!.body).toEqual({ username: 'finn' });
    const box = screen.getByRole('region', { name: a.linkTitle('finn') });
    expect((within(box).getByLabelText(a.linkLabel) as HTMLInputElement).value).toBe(LINK);
    expect(within(box).getByText(a.linkOnce)).toBeTruthy();
    // Not a link: opening it here would sign this browser in as the new user.
    expect(container.querySelector(`a[href*="launch"]`)).toBeNull();
    fireEvent.click(within(box).getByRole('button', { name: a.copy }));
    await flush();
    expect(writeText).toHaveBeenCalledWith(LINK);
    expect(within(box).getByRole('status').textContent).toBe(a.copied);
    fireEvent.click(within(box).getByRole('button', { name: a.linkClose }));
    await flush();
    expect(screen.queryByRole('region', { name: a.linkTitle('finn') })).toBeNull();
    // Gone from the page and never stored; switching tabs does not bring it back.
    adminTab(a.tabs.roots);
    adminTab(a.tabs.users);
    await flush();
    expect([container.innerHTML, JSON.stringify({ ...localStorage }), JSON.stringify({ ...sessionStorage }), location.href].join('\n')).not.toContain('ONE-TIME-LINK-SECRET');
  });

  it('says when a username is taken', async () => {
    await openAdmin({ 'POST /api/v1/admin/users': () => apiError(409, 'CONFLICT') });
    fireEvent.change(screen.getByLabelText(a.createUsername), { target: { value: 'erin' } });
    fireEvent.click(screen.getByRole('button', { name: a.createSubmit }));
    await flush();
    expect(screen.getByRole('alert').textContent).toContain(a.createTaken);
  });

  it('disables a user only after confirmation', async () => {
    const { calls } = await openAdmin({ 'POST /api/v1/admin/users/6c2a9d14-31f8-4be0-8d57-91a3e0c4f7b2/disable': () => json(200, { ...ERIN, disabled: true }) });
    fireEvent.click(within(userRow(/Erin/)).getByRole('button', { name: a.disable }));
    expect(calls.some(c => c.url.endsWith('/disable'))).toBe(false);
    expect(within(userRow(/Erin/)).getByText(a.disableConfirm('Erin (erin)'))).toBeTruthy();
    fireEvent.click(within(userRow(/Erin/)).getByRole('button', { name: a.disableYes }));
    await flush();
    expect(calls.filter(c => c.method === 'POST' && c.url.endsWith('/disable'))).toHaveLength(1);
  });

  it('grants and revokes admin, and explains the last-admin refusal', async () => {
    const { calls } = await openAdmin({
      'PUT /api/v1/admin/users/6c2a9d14-31f8-4be0-8d57-91a3e0c4f7b2/admin': () => json(200, { ...ERIN, is_admin: true }),
      [`PUT /api/v1/admin/users/${SESSION.user_id}/admin`]: () => apiError(409, 'CONFLICT'),
    });
    fireEvent.click(within(userRow(/Erin/)).getByRole('button', { name: a.grantAdmin }));
    await flush();
    expect(calls.find(c => c.method === 'PUT' && c.url.includes('6c2a9d14'))!.body).toEqual({ is_admin: true });
    fireEvent.click(within(userRow(/Dana/)).getByRole('button', { name: a.revokeAdmin }));
    await flush();
    expect(within(userRow(/Dana/)).getByRole('alert').textContent).toContain(a.lastAdmin);
  });

  it('resets a password after confirmation and shows the new link once', async () => {
    const { calls } = await openAdmin({
      'POST /api/v1/admin/users/6c2a9d14-31f8-4be0-8d57-91a3e0c4f7b2/password-reset': () => json(200, { user: ERIN, login_link: { url: LINK, expires_at: '2026-11-19T09:10:00Z' } }),
    });
    fireEvent.click(within(userRow(/Erin/)).getByRole('button', { name: a.reset }));
    expect(calls.some(c => c.url.endsWith('/password-reset'))).toBe(false);
    fireEvent.click(within(userRow(/Erin/)).getByRole('button', { name: a.resetYes }));
    await flush();
    expect(screen.getByRole('region', { name: a.linkTitle('Erin (erin)') })).toBeTruthy();
  });
});

describe('source roots and providers', () => {
  it('grants and revokes a root per user', async () => {
    const { calls } = await openAdmin({
      [`PUT /api/v1/admin/source-roots/${ROOT.id}/grants/6c2a9d14-31f8-4be0-8d57-91a3e0c4f7b2`]: () => new Response(null, { status: 204 }),
      [`DELETE /api/v1/admin/source-roots/${ROOT.id}/grants/${SESSION.user_id}`]: () => new Response(null, { status: 204 }),
    });
    adminTab(a.tabs.roots);
    await flush();
    expect(screen.getByRole('heading', { name: 'team-root' })).toBeTruthy();
    const pick = screen.getByLabelText(a.grantUser('team-root')) as HTMLSelectElement;
    // Only users without a grant are offered.
    expect([...pick.options].map(o => o.textContent)).toEqual([a.grantPick, 'Erin (erin)']);
    fireEvent.change(pick, { target: { value: ERIN.id } });
    fireEvent.click(screen.getByRole('button', { name: a.grant }));
    await flush();
    expect(calls.some(c => c.method === 'PUT' && c.url.endsWith(`/grants/${ERIN.id}`))).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: a.revokeFor('Dana (dana)', 'team-root') }));
    await flush();
    expect(calls.some(c => c.method === 'DELETE' && c.url.endsWith(`/grants/${SESSION.user_id}`))).toBe(true);
    expect(calls.filter(c => c.url === '/api/v1/admin/source-roots').length).toBeGreaterThanOrEqual(3);
  });

  it('lists providers read-only', async () => {
    await openAdmin();
    adminTab(a.tabs.providers);
    await flush();
    const table = screen.getByRole('table', { name: a.providersCaption });
    expect(table.textContent).toContain('github.com');
    expect(within(table).queryByRole('button')).toBeNull();
  });
});

describe('server audit log', () => {
  it('filters by time range, actor, action and project, and validates the inputs first', async () => {
    const { calls } = await openAdmin();
    adminTab(a.tabs.audit);
    await flush();
    const au = t.accounts.audit;
    const audit = () => calls.filter(c => c.url.startsWith('/api/v1/audit')).map(c => c.url);
    expect(audit()).toEqual(['/api/v1/audit']);
    fireEvent.change(screen.getByLabelText(au.project), { target: { value: 'not-a-uuid' } });
    fireEvent.click(screen.getByRole('button', { name: au.apply }));
    await flush();
    expect(screen.getByRole('alert').textContent).toBe(au.projectInvalid);
    expect(audit()).toHaveLength(1);
    fireEvent.change(screen.getByLabelText(au.since), { target: { value: '2026-11-19T10:00' } });
    fireEvent.change(screen.getByLabelText(au.until), { target: { value: '2026-11-19T09:00' } });
    fireEvent.change(screen.getByLabelText(au.project), { target: { value: '' } });
    fireEvent.click(screen.getByRole('button', { name: au.apply }));
    expect(screen.getByRole('alert').textContent).toBe(au.rangeInvalid);
    fireEvent.change(screen.getByLabelText(au.until), { target: { value: '2026-11-20T09:00' } });
    fireEvent.change(screen.getByLabelText(au.actor), { target: { value: ERIN.id } });
    fireEvent.change(screen.getByLabelText(au.action), { target: { value: 'auth.login_failed' } });
    fireEvent.change(screen.getByLabelText(au.project), { target: { value: '3F7C2E19-8A4B-4D6E-9F01-B2C3D4E5F6A7' } });
    fireEvent.click(screen.getByRole('button', { name: au.apply }));
    await flush();
    const url = new URL(audit().at(-1)!, 'http://x');
    expect(url.searchParams.get('since')).toBe(new Date('2026-11-19T10:00').toISOString());
    expect(url.searchParams.get('until')).toBe(new Date('2026-11-20T09:00').toISOString());
    expect(url.searchParams.get('actor')).toBe(ERIN.id);
    expect(url.searchParams.get('action')).toBe('auth.login_failed');
    expect(url.searchParams.get('project')).toBe('3f7c2e19-8a4b-4d6e-9f01-b2c3d4e5f6a7');
    fireEvent.click(screen.getByRole('button', { name: au.clear }));
    await flush();
    expect(audit().at(-1)).toBe('/api/v1/audit');
  });

  it('shows unknown actions and actor kinds raw instead of guessing', async () => {
    await openAdmin({
      'GET /api/v1/audit': () => json(200, { items: [{
        id: '9', occurred_at: '2026-11-19T09:00:00Z', actor_kind: 'robot', actor_user_id: null, actor_username: null, actor_node_id: null,
        action: 'future.thing', target_type: null, target_id: null, project_id: null, request_id: null, detail: {},
      }], next_cursor: null }),
    });
    adminTab(a.tabs.audit);
    await flush();
    const row = within(screen.getByRole('table', { name: t.accounts.audit.caption })).getAllByRole('row')[1]!;
    expect(row.textContent).toContain(t.accounts.audit.actionUnknown('future.thing'));
    expect(row.textContent).toContain(t.accounts.audit.actorUnknown('robot'));
  });
});
