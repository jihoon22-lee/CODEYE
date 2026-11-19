// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, screen } from '@testing-library/react';
import { advance, backend, flush, renderApp } from './harness';
import { ko as t } from './locales/ko';
import { SESSION, apiError, json, rateLimited, type Call } from './test-utils';

beforeEach(() => vi.useFakeTimers());
afterEach(() => { cleanup(); vi.useRealTimers(); localStorage.clear(); sessionStorage.clear(); });

const p = t.accounts.password;
const NEW = 'a-long-new-password-1';
const MUST = { ...SESSION, must_change_password: true, has_password: false };

const fill = (label: string, value: string) => { (screen.getByLabelText(label) as HTMLInputElement).value = value; };
const submit = () => fireEvent.click(screen.getByRole('button', { name: p.submit }));
const nonSession = (calls: Call[]) => calls.filter(c => !c.url.startsWith('/api/v1/session'));

describe('forced password change', () => {
  it('shows only the password screen and logout, and makes no other request until the password is set', async () => {
    let changed = false;
    const { calls } = renderApp(backend({
      'GET /api/v1/session': () => json(200, changed ? SESSION : MUST),
      'POST /api/v1/session/password': () => { changed = true; return json(200, { ...SESSION, csrf_token: 'csrf-after-change' }); },
    }));
    await flush();
    expect(screen.getByRole('heading', { name: p.forcedTitle })).toBeTruthy();
    expect(screen.queryByRole('navigation', { name: t.nav.label })).toBeNull();
    expect(screen.queryByRole('button', { name: t.accounts.menu.account })).toBeNull();
    expect(screen.getByRole('button', { name: t.session.logout })).toBeTruthy();
    // A user without a password sets one without the current-password field.
    expect(screen.queryByLabelText(p.current)).toBeNull();
    expect(nonSession(calls)).toEqual([]);
    fill(p.next, NEW);
    fill(p.confirm, NEW);
    submit();
    expect((screen.getByLabelText(p.next) as HTMLInputElement).value).toBe('');
    await flush();
    const post = calls.find(c => c.url === '/api/v1/session/password')!;
    expect(post.body).toEqual({ new_password: NEW });
    expect(post.headers['x-codeye-csrf']).toBe(SESSION.csrf_token);
    expect(screen.getByRole('heading', { name: t.projects.title })).toBeTruthy();
    expect(screen.getByText(p.done)).toBeTruthy();
    // Later calls use the session view's new CSRF token.
    fireEvent.click(screen.getByRole('button', { name: t.session.logout }));
    await flush();
    expect(calls.find(c => c.method === 'DELETE')!.headers['x-codeye-csrf']).toBe('csrf-after-change');
  });

  it('goes to the password screen when any call answers PASSWORD_CHANGE_REQUIRED', async () => {
    renderApp(backend({ 'GET /api/v1/projects': () => apiError(403, 'PASSWORD_CHANGE_REQUIRED') }));
    await flush();
    expect(screen.getByRole('heading', { name: p.forcedTitle })).toBeTruthy();
    expect(screen.queryByRole('heading', { name: t.projects.title })).toBeNull();
    // This user has a password: the current one is asked for.
    expect(screen.getByLabelText(p.current)).toBeTruthy();
  });

  it('checks the confirmation locally and sends nothing on a mismatch', async () => {
    const { calls } = renderApp(backend({ 'GET /api/v1/session': () => json(200, MUST) }));
    await flush();
    fill(p.next, NEW);
    fill(p.confirm, `${NEW}x`);
    submit();
    await flush();
    expect(screen.getByRole('alert').textContent).toBe(p.mismatch);
    expect(calls.some(c => c.url === '/api/v1/session/password')).toBe(false);
  });

  it('explains instead of offering a form to a link-only user', async () => {
    renderApp(backend({ 'GET /api/v1/session': () => json(200, { ...MUST, username: null }) }));
    await flush();
    expect(screen.getByText(p.linkOnly)).toBeTruthy();
    expect(screen.queryByLabelText(p.next)).toBeNull();
  });
});

describe('voluntary password change', () => {
  async function open(over: Record<string, (c: Call) => Response>) {
    const app = renderApp(backend(over));
    await flush();
    fireEvent.click(screen.getByRole('button', { name: t.accounts.menu.account }));
    return app;
  }

  it('requires the current password, explains the effect on other sessions, and confirms success', async () => {
    const { calls } = await open({ 'POST /api/v1/session/password': () => json(200, SESSION) });
    expect(screen.getByRole('heading', { name: p.title })).toBeTruthy();
    expect(screen.getByText(p.effect)).toBeTruthy();
    fill(p.next, NEW);
    fill(p.confirm, NEW);
    submit();
    await flush();
    expect(screen.getByRole('alert').textContent).toBe(p.currentEmpty);
    fill(p.current, 'old-password-value');
    fill(p.next, NEW);
    fill(p.confirm, NEW);
    submit();
    for (const label of [p.current, p.next, p.confirm]) expect((screen.getByLabelText(label) as HTMLInputElement).value).toBe('');
    await flush();
    expect(calls.find(c => c.url === '/api/v1/session/password')!.body).toEqual({ current_password: 'old-password-value', new_password: NEW });
    expect(screen.getByRole('status').textContent).toBe(p.done);
    expect(document.body.innerHTML).not.toContain(NEW);
  });

  it('shows a wrong current password, the server validation text, and a throttle wait', async () => {
    const answers = [
      apiError(403, 'PASSWORD_INCORRECT'),
      apiError(422, 'VALIDATION_FAILED', 'password must be at least 12 characters'),
      rateLimited(5),
    ];
    let n = 0;
    const { calls } = await open({ 'POST /api/v1/session/password': () => answers[n++]! });
    const attempt = async () => {
      fill(p.current, 'old-password-value');
      fill(p.next, NEW);
      fill(p.confirm, NEW);
      submit();
      await flush();
    };
    await attempt();
    expect(screen.getByRole('alert').textContent).toBe(p.incorrect);
    // A wrong current password does not log out or force anything.
    expect(screen.getByRole('heading', { name: p.title })).toBeTruthy();
    await attempt();
    expect(screen.getByRole('alert').textContent).toContain('password must be at least 12 characters');
    await attempt();
    expect(screen.getByRole('alert').textContent).toBe(p.throttled(5));
    expect((screen.getByRole('button', { name: p.submit }) as HTMLButtonElement).disabled).toBe(true);
    await advance(5000);
    expect((screen.getByRole('button', { name: p.submit }) as HTMLButtonElement).disabled).toBe(false);
    expect(calls.filter(c => c.url === '/api/v1/session/password')).toHaveLength(3);
  });

  it('returns to the projects with the back button', async () => {
    await open({});
    fireEvent.click(screen.getByRole('button', { name: p.back }));
    expect(screen.getByRole('heading', { name: t.projects.title })).toBeTruthy();
  });
});
