// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, screen } from '@testing-library/react';
import { backend, flush, renderApp } from './harness';
import { ko as t } from './locales/ko';
import { apiError, json, type Call } from './test-utils';

beforeEach(() => vi.useFakeTimers());
afterEach(() => { cleanup(); vi.useRealTimers(); });

const g = t.accounts.github;
const NONE = { host: 'github.com', connected: false, account: null, source: null, storable: true, reason: null };
const TOKEN = 'ghp_example_token_value_1234567890';

async function open(over: Record<string, (c: Call) => Response | undefined>) {
  const app = renderApp(backend(over));
  await flush();
  fireEvent.click(document.getElementById('menu-github')!);
  await flush();
  return app;
}

describe('GitHub connection', () => {
  it('stores a pasted token, never shows it, and removes it again', async () => {
    let view: Record<string, unknown> = NONE;
    const app = await open({
      'GET /api/v1/account/github': () => json(200, view),
      'PUT /api/v1/account/github': () => { view = { ...NONE, connected: true, account: 'octo', source: 'web' }; return json(200, view); },
      'DELETE /api/v1/account/github': () => { view = NONE; return new Response(null, { status: 204 }); },
    });
    expect(screen.getByRole('heading', { level: 1, name: g.title })).toBeTruthy();
    expect(screen.getByText(g.intro)).toBeTruthy();
    expect(document.getElementById('github-status')!.textContent).toBe(g.notConnected);
    const input = document.getElementById('github-token') as HTMLInputElement;
    expect(input.type).toBe('password');
    fireEvent.click(document.getElementById('github-save')!);
    expect(screen.getByRole('alert').textContent).toBe(g.empty);
    fireEvent.change(input, { target: { value: ` ${TOKEN} ` } });
    fireEvent.click(document.getElementById('github-save')!);
    await flush();
    expect(app.calls.find(c => c.method === 'PUT')!.body).toEqual({ token: TOKEN });
    expect(document.getElementById('github-status')!.textContent).toBe(g.connected('octo'));
    expect(screen.getByText(g.sources.web!)).toBeTruthy();
    expect((document.getElementById('github-token') as HTMLInputElement).value).toBe('');
    expect(document.body.textContent).not.toContain(TOKEN);
    fireEvent.click(document.getElementById('github-remove')!);
    await flush();
    expect(app.calls.some(c => c.method === 'DELETE' && c.url === '/api/v1/account/github')).toBe(true);
    expect(document.getElementById('github-status')!.textContent).toBe(g.notConnected);
    expect(screen.getByText(g.removed)).toBeTruthy();
  });

  it('says when GitHub refused the token', async () => {
    await open({
      'GET /api/v1/account/github': () => json(200, NONE),
      'PUT /api/v1/account/github': () => apiError(422, 'GITHUB_TOKEN_REFUSED'),
    });
    fireEvent.change(document.getElementById('github-token')!, { target: { value: TOKEN } });
    fireEvent.click(document.getElementById('github-save')!);
    await flush();
    expect(screen.getByRole('alert').textContent).toBe(g.refused);
  });

  it('explains why a token cannot be stored here', async () => {
    await open({ 'GET /api/v1/account/github': () => json(200, { ...NONE, storable: false, reason: 'offline_mode' }) });
    expect(document.getElementById('github-unavailable')!.textContent).toBe(g.unavailable.offline_mode);
    expect(document.getElementById('github-token')).toBeNull();
  });
});
