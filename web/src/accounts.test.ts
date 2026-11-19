import { describe, expect, it, vi } from 'vitest';
import { auditQuery, parseSession, parseUserWithLink } from './accounts';
import { ApiError, createApi } from './api';
import { PROJECT, SESSION, apiError, json, mockFetch, rateLimited } from './test-utils';

const failure = (p: Promise<unknown>) => p.then(() => { throw new Error('expected a failure'); }, (e: unknown) => e as ApiError);

describe('account API plumbing', () => {
  it('reads the wait of a throttled call from Retry-After and details, preferring the longer one', async () => {
    const both = await failure(createApi({ fetch: mockFetch(() => rateLimited(42)).fetch }).loginWithPassword('dana', 'x'));
    expect(both).toMatchObject({ status: 429, code: 'RATE_LIMITED', retryAfter: 42 });
    const headerOnly = await failure(createApi({ fetch: mockFetch(() => apiError(429, 'RATE_LIMITED', 'm', true, undefined, { 'Retry-After': '7' })).fetch }).loginWithPassword('a', 'b'));
    expect(headerOnly.retryAfter).toBe(7);
    const disagree = await failure(createApi({ fetch: mockFetch(() => apiError(429, 'RATE_LIMITED', 'm', true, { retry_after_seconds: 90 }, { 'Retry-After': '30' })).fetch }).loginWithPassword('a', 'b'));
    expect(disagree.retryAfter).toBe(90);
    const garbage = await failure(createApi({ fetch: mockFetch(() => apiError(429, 'RATE_LIMITED', 'm', true, { retry_after_seconds: -1 }, { 'Retry-After': 'soon' })).fetch }).loginWithPassword('a', 'b'));
    expect(garbage.retryAfter).toBeNull();
    // Other statuses never carry a wait.
    const other = await failure(createApi({ fetch: mockFetch(() => apiError(401, 'UNAUTHENTICATED', 'm', false, { retry_after_seconds: 5 })).fetch }).loginWithPassword('a', 'b'));
    expect(other.retryAfter).toBeNull();
  });

  it('sends a password sign-in in the POST body only and does not treat its 401 as a lost session', async () => {
    const onUnauthorized = vi.fn();
    const m = mockFetch(() => apiError(401, 'UNAUTHENTICATED'));
    await failure(createApi({ fetch: m.fetch, onUnauthorized }).loginWithPassword('dana', 'pw-secret'));
    expect(m.calls[0]).toMatchObject({ method: 'POST', url: '/api/v1/session', body: { username: 'dana', password: 'pw-secret' } });
    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  it('reports PASSWORD_CHANGE_REQUIRED from any call, but not a wrong current password', async () => {
    const onPasswordChangeRequired = vi.fn();
    const api = createApi({
      fetch: mockFetch(c => (c.url.startsWith('/api/v1/projects') ? apiError(403, 'PASSWORD_CHANGE_REQUIRED') : apiError(403, 'PASSWORD_INCORRECT'))).fetch,
      onPasswordChangeRequired,
    });
    await failure(api.changePassword({ current_password: 'a', new_password: 'b' }));
    expect(onPasswordChangeRequired).not.toHaveBeenCalled();
    await failure(api.listProjects());
    expect(onPasswordChangeRequired).toHaveBeenCalledTimes(1);
  });

  it('rejects a project without its role and permissions instead of guessing', async () => {
    const { can_run: _r, ...noRun } = PROJECT;
    for (const bad of [noRun, { ...PROJECT, role: 'admin' }, { ...PROJECT, can_manage: 'yes' }]) {
      const e = await failure(createApi({ fetch: mockFetch(() => json(200, { items: [bad], next_cursor: null })).fetch }).listProjects());
      expect(e.kind).toBe('invalid_response');
    }
    const ok = await createApi({ fetch: mockFetch(() => json(200, { items: [{ ...PROJECT, role: 'viewer', can_run: false, can_manage: false }], next_cursor: null })).fetch }).listProjects();
    expect(ok.items[0]).toMatchObject({ role: 'viewer', can_run: false, can_manage: false });
  });

  it('requires the whole session view', () => {
    expect(parseSession(SESSION)).toMatchObject({ username: 'dana', is_admin: false, must_change_password: false });
    expect(parseSession({ ...SESSION, username: null, display_name: null })).toMatchObject({ username: null });
    const { is_admin: _a, ...noAdmin } = SESSION;
    expect(() => parseSession(noAdmin)).toThrow();
    expect(() => parseSession({ ...SESSION, must_change_password: 'false' })).toThrow();
  });

  it('accepts only a launch-fragment login link', () => {
    const user = { id: 'u1', username: 'erin', display_name: null, is_admin: false, must_change_password: true, has_password: false, disabled: false, disabled_at: null, created_at: '2026-11-19T09:00:00Z', last_login_at: null };
    const ok = parseUserWithLink({ user, login_link: { url: 'https://codeye.example/#launch=abc', expires_at: '2026-11-19T09:10:00Z' } });
    expect(ok.login_link.url).toBe('https://codeye.example/#launch=abc');
    for (const url of ['javascript:alert(1)//#launch=x', 'https://codeye.example/?launch=abc', 'not a url']) {
      expect(() => parseUserWithLink({ user, login_link: { url, expires_at: '2026-11-19T09:10:00Z' } })).toThrow();
    }
  });

  it('sends only the audit filters that are set, with the cursor', () => {
    expect(auditQuery({}, null)).toBe('');
    expect(auditQuery({ since: '2026-11-19T00:00:00.000Z', action: 'auth.login_failed', actor: '' }, '4170'))
      .toBe('?since=2026-11-19T00%3A00%3A00.000Z&action=auth.login_failed&cursor=4170');
  });
});
