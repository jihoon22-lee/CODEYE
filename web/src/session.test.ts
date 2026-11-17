// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApi } from './api';
import { createSessionController, takeLaunchToken } from './session';
import { SESSION, apiError, json, mockFetch } from './test-utils';

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

function fakeEnv(hash: string, order: string[] = []) {
  const location = { hash, pathname: '/app', search: '?x=1' };
  const history = {
    state: { s: 1 },
    replaceState: vi.fn((_state: unknown, _title: string, url?: string | URL | null) => {
      order.push('replaceState');
      location.hash = '';
      expect(url).toBe('/app?x=1'); // path and query kept, fragment removed
    }),
  };
  return { location, history };
}

const settle = () => vi.advanceTimersByTimeAsync(0);

describe('takeLaunchToken', () => {
  it('returns the token and removes the fragment from the address bar', () => {
    const env = fakeEnv('#launch=abc.def');
    expect(takeLaunchToken(env)).toBe('abc.def');
    expect(env.history.replaceState).toHaveBeenCalledTimes(1);
    expect(env.location.hash).toBe('');
  });
  it('clears the whole fragment even with other parameters, and ignores unrelated fragments', () => {
    const env = fakeEnv('#a=1&launch=tok&b=2');
    expect(takeLaunchToken(env)).toBe('tok');
    const other = fakeEnv('#section');
    expect(takeLaunchToken(other)).toBeNull();
    expect(other.history.replaceState).not.toHaveBeenCalled();
  });
  it('still clears the fragment when the token is empty or absurdly long, and returns null', () => {
    for (const hash of ['#launch=', `#launch=${'a'.repeat(5000)}`]) {
      const env = fakeEnv(hash);
      expect(takeLaunchToken(env)).toBeNull();
      expect(env.history.replaceState).toHaveBeenCalledTimes(1);
    }
  });
});

describe('launch and recovery', () => {
  it('clears the fragment before any network call and sends the token only in the POST body', async () => {
    const order: string[] = [];
    const m = mockFetch(call => { order.push(`${call.method} ${call.url}`); return json(200, SESSION); });
    const controller = createSessionController(createApi({ fetch: m.fetch }), fakeEnv('#launch=SECRET', order));
    const started = controller.start();
    expect(order[0]).toBe('replaceState'); // synchronous, before any request is issued
    await started;
    expect(order).toEqual(['replaceState', 'POST /api/v1/session']);
    expect(m.calls).toHaveLength(1);
    expect(m.calls[0]!.body).toEqual({ launch_token: 'SECRET' });
    expect(m.calls[0]!.url).not.toContain('SECRET');
    expect(controller.getState()).toMatchObject({ status: 'authenticated', userId: SESSION.user_id });
    expect(JSON.stringify(controller.getState())).not.toContain('SECRET');
    expect(JSON.stringify(controller.getState())).not.toContain(SESSION.csrf_token); // the CSRF token is not UI state
  });

  it('consumes a launch link once even if start() is called repeatedly (StrictMode)', async () => {
    const m = mockFetch(() => json(200, SESSION));
    const env = fakeEnv('#launch=once');
    const controller = createSessionController(createApi({ fetch: m.fetch }), env);
    await Promise.all([controller.start(), controller.start()]);
    expect(m.calls.map(c => c.method)).toEqual(['POST']); // no second exchange and no stray GET
    expect(env.history.replaceState).toHaveBeenCalledTimes(1);
  });

  it('recovers an existing cookie session with GET when there is no launch link', async () => {
    const m = mockFetch(() => json(200, SESSION));
    const controller = createSessionController(createApi({ fetch: m.fetch }), fakeEnv(''));
    await controller.start();
    expect(m.calls.map(c => `${c.method} ${c.url}`)).toEqual(['GET /api/v1/session']);
    expect(controller.getState().status).toBe('authenticated');
  });

  it('is anonymous (not authenticated) when the server says 401', async () => {
    const m = mockFetch(() => apiError(401, 'UNAUTHENTICATED'));
    const controller = createSessionController(createApi({ fetch: m.fetch }), fakeEnv(''));
    await controller.start();
    expect(controller.getState()).toEqual({ status: 'anonymous', reason: 'none' });
  });

  it('reports a rejected launch link, but keeps a still-valid cookie session', async () => {
    const rejected = mockFetch(() => apiError(401, 'UNAUTHENTICATED'));
    const a = createSessionController(createApi({ fetch: rejected.fetch }), fakeEnv('#launch=used'));
    await a.start();
    expect(a.getState()).toEqual({ status: 'anonymous', reason: 'launch_rejected' });
    expect(rejected.calls.map(c => c.method)).toEqual(['POST', 'GET']);

    const m = mockFetch(call => (call.method === 'POST' ? apiError(400, 'INVALID_LAUNCH_TOKEN') : json(200, SESSION)));
    const b = createSessionController(createApi({ fetch: m.fetch }), fakeEnv('#launch=used'));
    await b.start();
    expect(b.getState().status).toBe('authenticated');
  });

  it.each([
    ['network failure', () => 'network' as const],
    ['a dev server answering with HTML', () => new Response('<html></html>', { status: 200 })],
    ['a missing API (404)', () => new Response('not found', { status: 404 })],
    ['a server error', () => apiError(503, 'UNAVAILABLE', 'm', true)],
  ])('shows the server as unavailable on %s and never as authenticated', async (_, answer) => {
    const m = mockFetch(answer);
    const controller = createSessionController(createApi({ fetch: m.fetch }), fakeEnv(''));
    await controller.start();
    expect(controller.getState()).toEqual({ status: 'unavailable' });
    const launched = createSessionController(createApi({ fetch: m.fetch }), fakeEnv('#launch=t'));
    await launched.start();
    expect(launched.getState()).toEqual({ status: 'unavailable' });
  });

  it('can reconnect after being unavailable', async () => {
    let up = false;
    const m = mockFetch(() => (up ? json(200, SESSION) : 'network'));
    const controller = createSessionController(createApi({ fetch: m.fetch }), fakeEnv(''));
    await controller.start();
    expect(controller.getState().status).toBe('unavailable');
    up = true;
    await controller.retry();
    expect(controller.getState().status).toBe('authenticated');
  });
});

describe('session lifetime', () => {
  it('keeps the CSRF token in memory and sends it on logout, then forgets it', async () => {
    const m = mockFetch(call => (call.method === 'DELETE' ? new Response(null, { status: 204 }) : json(200, SESSION)));
    const api = createApi({ fetch: m.fetch });
    const controller = createSessionController(api, fakeEnv(''));
    await controller.start();
    await controller.logout();
    const del = m.calls.find(c => c.method === 'DELETE')!;
    expect(del.headers['x-codeye-csrf']).toBe(SESSION.csrf_token);
    expect(controller.getState()).toEqual({ status: 'anonymous', reason: 'logged_out' });
    m.calls.length = 0;
    await api.cancelRun('r').catch(() => undefined);
    expect(m.calls[0]!.headers['x-codeye-csrf']).toBeUndefined();
  });

  it('stays signed in and says so when logout fails', async () => {
    const m = mockFetch(call => (call.method === 'DELETE' ? apiError(500, 'INTERNAL', 'm', true) : json(200, SESSION)));
    const controller = createSessionController(createApi({ fetch: m.fetch }), fakeEnv(''));
    await controller.start();
    await controller.logout();
    expect(controller.getState()).toMatchObject({ status: 'authenticated', loggingOut: false, logoutFailed: true });
  });

  it('turns anonymous when any API call reports 401', async () => {
    const m = mockFetch(call => (call.url === '/api/v1/session' ? json(200, SESSION) : apiError(401, 'UNAUTHENTICATED')));
    const api = createApi({ fetch: m.fetch });
    const controller = createSessionController(api, fakeEnv(''));
    await controller.start();
    await api.listProjects().catch(() => undefined);
    expect(controller.getState()).toEqual({ status: 'anonymous', reason: 'expired' });
  });

  it('asks the server at the stated expiry instead of assuming the session ended', async () => {
    vi.setSystemTime(new Date('2030-01-01T00:00:00Z'));
    let alive = true;
    const expiring = { ...SESSION, expires_at: '2030-01-01T00:10:00Z' };
    const m = mockFetch(() => (alive ? json(200, expiring) : apiError(401, 'UNAUTHENTICATED')));
    const controller = createSessionController(createApi({ fetch: m.fetch }), fakeEnv(''));
    await controller.start();
    await vi.advanceTimersByTimeAsync(9 * 60_000);
    expect(m.calls).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(61_000);
    expect(m.calls).toHaveLength(2);
    expect(controller.getState().status).toBe('authenticated'); // server still accepted it
    await vi.advanceTimersByTimeAsync(5 * 60_000);
    expect(m.calls.length).toBeLessThan(15); // a past expires_at is rechecked slowly, not in a loop
    alive = false;
    await vi.advanceTimersByTimeAsync(11 * 60_000);
    await settle();
    expect(controller.getState()).toEqual({ status: 'anonymous', reason: 'expired' });
  });
});
