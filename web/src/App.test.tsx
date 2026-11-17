// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, screen } from '@testing-library/react';
import { backend, flush, renderApp } from './harness';
import { ko as t } from './locales/ko';
import { SESSION, apiError } from './test-utils';

beforeEach(() => vi.useFakeTimers());
afterEach(() => { cleanup(); vi.useRealTimers(); vi.unstubAllGlobals(); localStorage.clear(); sessionStorage.clear(); });

describe('connection states', () => {
  it('says the server is unavailable when the API cannot be reached, and offers no project UI', async () => {
    renderApp(() => 'network');
    await flush();
    expect(screen.getByText(t.session.unavailable.title)).toBeTruthy();
    expect(screen.queryByText(t.session.logout)).toBeNull();
    expect(screen.queryByRole('button', { name: t.projects.submit })).toBeNull();
  });

  it('does not pretend to be authenticated when a dev server answers every path with HTML', async () => {
    renderApp(() => new Response('<!doctype html><div id=root>', { status: 200, headers: { 'Content-Type': 'text/html' } }));
    await flush();
    expect(screen.getByText(t.session.unavailable.title)).toBeTruthy();
    expect(screen.queryByText(t.session.logout)).toBeNull();
  });

  it('keeps the diagnostics viewer usable without a server', async () => {
    renderApp(() => 'network');
    await flush();
    fireEvent.click(screen.getByRole('button', { name: t.nav.doctor }));
    expect(screen.getByLabelText(t.open)).toBeTruthy();
    expect(screen.getByText(t.limitation)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: t.nav.projects }));
    expect(screen.getByText(t.session.unavailable.title)).toBeTruthy();
  });

  it('asks for a launch link when there is no session and offers no token input', async () => {
    const { container } = renderApp(() => apiError(401, 'UNAUTHENTICATED'));
    await flush();
    expect(screen.getByText(t.session.anonymous.title)).toBeTruthy();
    expect(screen.getByText(t.session.anonymous.none)).toBeTruthy();
    expect(container.querySelector('input')).toBeNull();
  });

  it('explains a rejected launch link', async () => {
    renderApp(() => apiError(401, 'UNAUTHENTICATED'), { hash: '#launch=spent' });
    await flush();
    expect(screen.getByText(t.session.anonymous.launch_rejected)).toBeTruthy();
  });

  it('can reconnect after the server was unavailable', async () => {
    let up = false;
    renderApp(backend({ '*': () => (up ? undefined : 'network') }));
    await flush();
    up = true;
    fireEvent.click(screen.getByRole('button', { name: t.session.unavailable.retry }));
    await flush();
    expect(screen.getByRole('heading', { name: t.projects.title })).toBeTruthy();
  });
});

describe('launch and logout', () => {
  it('consumes the launch link without leaving the secret anywhere in the page or storage', async () => {
    const secret = 'LAUNCH-SECRET-123';
    const { replaceState, calls, container } = renderApp(backend(), { hash: `#launch=${secret}` });
    expect(replaceState).toHaveBeenCalledTimes(1);
    await flush();
    expect(screen.getByRole('heading', { name: t.projects.title })).toBeTruthy();
    const everything = [
      container.innerHTML, document.title, document.cookie,
      JSON.stringify({ ...localStorage }), JSON.stringify({ ...sessionStorage }),
      ...calls.filter(c => c.method !== 'POST' || c.url !== '/api/v1/session').map(c => JSON.stringify(c)),
    ].join('\n');
    expect(everything).not.toContain(secret);
    expect(everything).not.toContain(SESSION.csrf_token);
    expect(calls.find(c => c.method === 'POST' && c.url === '/api/v1/session')!.body).toEqual({ launch_token: secret });
  });

  it('logs out with the CSRF header and returns to the login message', async () => {
    const { calls } = renderApp();
    await flush();
    fireEvent.click(screen.getByRole('button', { name: t.session.logout }));
    await flush();
    expect(calls.find(c => c.method === 'DELETE')!.headers['x-codeye-csrf']).toBe(SESSION.csrf_token);
    expect(screen.getByText(t.session.anonymous.logged_out)).toBeTruthy();
    expect(screen.queryByRole('button', { name: t.projects.submit })).toBeNull();
  });

  it('drops the project screen when the session expires mid-use', async () => {
    renderApp(backend({ 'GET /api/v1/projects': () => apiError(401, 'UNAUTHENTICATED') }));
    await flush();
    expect(screen.getByText(t.session.anonymous.title)).toBeTruthy();
    expect(screen.getByText(t.session.anonymous.expired)).toBeTruthy();
  });
});
