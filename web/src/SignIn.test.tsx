// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, screen } from '@testing-library/react';
import { advance, backend, flush, renderApp } from './harness';
import { ko as t } from './locales/ko';
import { SESSION, apiError, json, rateLimited, type Call } from './test-utils';

beforeEach(() => vi.useFakeTimers());
afterEach(() => { cleanup(); vi.useRealTimers(); localStorage.clear(); sessionStorage.clear(); });

const s = t.accounts.signIn;
const PASSWORD = 'correct horse battery staple';

// No cookie session; POST /session answers with `login`.
function anonymous(login: (call: Call) => Response) {
  return backend({
    'GET /api/v1/session': () => apiError(401, 'UNAUTHENTICATED'),
    'POST /api/v1/session': call => ((call.body as Record<string, unknown>).launch_token !== undefined ? undefined : login(call)),
  });
}
const passwordField = () => screen.getByLabelText(s.password) as HTMLInputElement;
function signIn(username: string, password: string) {
  fireEvent.change(screen.getByLabelText(s.username), { target: { value: username } });
  passwordField().value = password;
  fireEvent.click(screen.getByRole('button', { name: s.submit }));
}

describe('password sign-in', () => {
  it('signs in, sends the password only in the POST body, and keeps it nowhere else', async () => {
    const { calls, container } = renderApp(anonymous(() => json(201, SESSION)));
    await flush();
    signIn('Dana', PASSWORD);
    await flush();
    expect(screen.getByRole('heading', { name: t.projects.title })).toBeTruthy();
    const post = calls.filter(c => c.method === 'POST' && c.url === '/api/v1/session');
    expect(post.map(c => c.body)).toEqual([{ username: 'Dana', password: PASSWORD }]);
    const everything = [
      container.innerHTML, document.cookie, JSON.stringify({ ...localStorage }), JSON.stringify({ ...sessionStorage }),
      ...calls.filter(c => !post.includes(c)).map(c => JSON.stringify(c)),
    ].join('\n');
    expect(everything).not.toContain(PASSWORD);
    // The CSRF token from the answer is used, and the header shows who signed in.
    expect(screen.getByText('Dana (dana)')).toBeTruthy();
  });

  it('shows one message for every refused sign-in and clears the password field each time', async () => {
    let answer = 0;
    const answers = [apiError(401, 'UNAUTHENTICATED', 'username or password is incorrect'), apiError(400, 'INVALID_REQUEST', 'bad'), apiError(403, 'ORIGIN_REJECTED', 'origin')];
    renderApp(anonymous(() => answers[answer++]!));
    await flush();
    const seen: string[] = [];
    for (let i = 0; i < answers.length; i++) {
      signIn(`user${i}`, PASSWORD);
      expect(passwordField().value).toBe('');
      await flush();
      seen.push(screen.getByRole('alert').textContent ?? '');
    }
    expect(new Set(seen)).toEqual(new Set([s.rejected]));
    // The server's wording never reaches the page.
    expect(document.body.textContent).not.toContain('username or password is incorrect');
    expect(screen.getByRole('heading', { name: t.session.anonymous.title })).toBeTruthy();
  });

  it('waits the server-given time after a throttled sign-in, with the button disabled until then', async () => {
    let throttled = true;
    const { calls } = renderApp(anonymous(() => (throttled ? rateLimited(3) : json(201, SESSION))));
    await flush();
    signIn('dana', PASSWORD);
    await flush();
    const button = screen.getByRole('button', { name: s.submit }) as HTMLButtonElement;
    expect(screen.getByRole('alert').textContent).toBe(s.throttled(3));
    expect(button.disabled).toBe(true);
    // Submitting while blocked sends nothing.
    fireEvent.submit(button.closest('form')!);
    await flush();
    expect(calls.filter(c => c.method === 'POST')).toHaveLength(1);
    await advance(1000);
    expect(screen.getByRole('alert').textContent).toBe(s.throttled(2));
    await advance(2000);
    expect(button.disabled).toBe(false);
    expect(screen.queryByRole('alert')).toBeNull();
    throttled = false;
    signIn('dana', PASSWORD);
    await flush();
    expect(screen.getByRole('heading', { name: t.projects.title })).toBeTruthy();
  });

  it('says the server is unreachable instead of blaming the credentials', async () => {
    renderApp(anonymous(() => apiError(503, 'UNAVAILABLE', 'm', true)));
    await flush();
    signIn('dana', PASSWORD);
    await flush();
    expect(screen.getByRole('alert').textContent).toBe(s.unavailable);
  });

  it('keeps the launch-link flow: a link still signs in without the form', async () => {
    const { calls, replaceState } = renderApp(backend(), { hash: '#launch=LINK-SECRET' });
    expect(replaceState).toHaveBeenCalledTimes(1);
    await flush();
    expect(calls.find(c => c.method === 'POST')!.body).toEqual({ launch_token: 'LINK-SECRET' });
    expect(screen.getByRole('heading', { name: t.projects.title })).toBeTruthy();
    expect(screen.queryByLabelText(s.password)).toBeNull();
  });
});

describe('account menu', () => {
  it('shows the display name and username instead of the user ID, and an admin badge only for admins', async () => {
    renderApp();
    await flush();
    const menu = screen.getByRole('group', { name: t.accounts.menu.label });
    expect(menu.textContent).toContain('Dana (dana)');
    expect(menu.textContent).not.toContain(SESSION.user_id.slice(0, 8));
    expect(menu.textContent).not.toContain(t.accounts.menu.admin);
    cleanup();
    renderApp(backend({ 'GET /api/v1/session': () => json(200, { ...SESSION, is_admin: true }) }));
    await flush();
    expect(screen.getByRole('group', { name: t.accounts.menu.label }).textContent).toContain(t.accounts.menu.admin);
  });

  it('falls back to the short ID for a link-only user', async () => {
    renderApp(backend({ 'GET /api/v1/session': () => json(200, { ...SESSION, username: null, display_name: null, has_password: false }) }));
    await flush();
    expect(screen.getByRole('group', { name: t.accounts.menu.label }).textContent).toContain(t.accounts.menu.unnamed(SESSION.user_id.slice(0, 8)));
  });
});
