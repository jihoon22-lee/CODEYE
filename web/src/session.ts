import type { SessionInfo } from './accounts';
import type { Api } from './api';
import { ApiError } from './api';
import { deepLinkFragment } from './deepLink';

export type AnonymousReason = 'none' | 'launch_rejected' | 'expired' | 'logged_out';
// Who is signed in, from the server's SessionView. The CSRF token is not part of it.
export type SessionUser = {
  userId: string;
  username: string | null;
  displayName: string | null;
  isAdmin: boolean;
  mustChangePassword: boolean;
  hasPassword: boolean;
};
export type SessionState =
  | { status: 'checking' }
  | { status: 'unavailable' }
  | { status: 'anonymous'; reason: AnonymousReason }
  | ({ status: 'authenticated'; expiresAt: string; loggingOut: boolean; logoutFailed: boolean } & SessionUser);

// The outcome of a sign-in or password change, for the form that asked. The password is never part of it.
export type CredentialResult =
  | { ok: true }
  | { ok: false; kind: 'rejected' } // every refused sign-in looks the same
  | { ok: false; kind: 'throttled'; retryAfter: number | null }
  | { ok: false; kind: 'unavailable' }
  | { ok: false; kind: 'error'; error: unknown };

type Env = {
  location: Pick<Location, 'hash' | 'pathname' | 'search'>;
  history: Pick<History, 'replaceState' | 'state'>;
};

const MAX_TIMER_MS = 2 ** 31 - 1;
const MIN_RECHECK_MS = 30_000;

// A launch link carries a single-use secret in the URL fragment (#launch=...). It is read
// once and removed from the address bar and history entry before any network call is
// made; only a deep link (#project=...&run=..., deepLink.ts) is kept. The secret is never
// stored, logged or rendered.
export function takeLaunchToken(env: Env): string | null {
  const hash = env.location.hash;
  if (!hash.startsWith('#')) return null;
  const params = new URLSearchParams(hash.slice(1));
  if (!params.has('launch')) return null;
  const token = params.get('launch');
  env.history.replaceState(env.history.state, '', env.location.pathname + env.location.search + deepLinkFragment(params));
  return token && token.length <= 4096 ? token : null;
}

// Not reachable, not the CODEYE API, or broken: say the server is unavailable.
const unreachable = (error: unknown) =>
  !(error instanceof ApiError) || error.kind !== 'http' || error.status >= 500 || error.status === 404;

export function createSessionController(api: Api, env?: Env) {
  let state: SessionState = { status: 'checking' };
  const listeners = new Set<() => void>();
  let started: Promise<void> | null = null;
  let timer: ReturnType<typeof setTimeout> | undefined;

  const set = (next: SessionState) => {
    state = next;
    listeners.forEach(l => l());
  };
  const authenticated = (info: SessionInfo) => {
    api.setCsrf(info.csrf_token);
    set({
      status: 'authenticated',
      userId: info.user_id,
      username: info.username,
      displayName: info.display_name,
      isAdmin: info.is_admin,
      mustChangePassword: info.must_change_password,
      hasPassword: info.has_password,
      expiresAt: info.expires_at,
      loggingOut: false,
      logoutFailed: false,
    });
    schedule(info.expires_at);
  };
  const anonymous = (reason: AnonymousReason) => {
    api.setCsrf(null);
    clearTimeout(timer);
    set({ status: 'anonymous', reason });
  };
  function schedule(expiresAt: string) {
    clearTimeout(timer);
    // Never sooner than MIN_RECHECK_MS: a stale or skewed expires_at must not become a request loop.
    const wait = Math.min(Math.max(Date.parse(expiresAt) - Date.now(), MIN_RECHECK_MS), MAX_TIMER_MS);
    // At the stated expiry, ask the server instead of assuming: a sliding session may live on.
    timer = setTimeout(() => void recover('expired'), wait);
  }
  async function recover(onUnauthorized: AnonymousReason) {
    try {
      authenticated(await api.getSession());
    } catch (error) {
      if (error instanceof ApiError && error.kind === 'http' && (error.status === 401 || error.status === 403)) anonymous(onUnauthorized);
      else {
        api.setCsrf(null);
        set({ status: 'unavailable' });
      }
    }
  }
  async function begin(launch: string | null) {
    if (launch) {
      try {
        authenticated(await api.createSession(launch));
        return;
      } catch (error) {
        if (unreachable(error)) return set({ status: 'unavailable' });
        // The link was refused; a cookie session from an earlier visit may still be valid.
        try {
          authenticated(await api.getSession());
        } catch {
          anonymous('launch_rejected');
        }
        return;
      }
    }
    try {
      authenticated(await api.getSession());
    } catch (error) {
      if (unreachable(error)) set({ status: 'unavailable' });
      else anonymous('none');
    }
  }

  api.setOnUnauthorized(() => {
    if (state.status === 'authenticated') anonymous('expired');
  });
  // Any call refused with PASSWORD_CHANGE_REQUIRED: only the password screen may be shown.
  api.setOnPasswordChangeRequired(() => {
    if (state.status === 'authenticated' && !state.mustChangePassword) set({ ...state, mustChangePassword: true });
  });

  return {
    getState: () => state,
    subscribe(listener: () => void) {
      listeners.add(listener);
      return () => void listeners.delete(listener);
    },
    // Idempotent: React StrictMode and re-renders must not consume the launch link twice.
    start(): Promise<void> {
      started ??= begin(takeLaunchToken(env ?? window));
      return started;
    },
    // Used by the "reconnect" button after the server was unreachable.
    retry(): Promise<void> {
      set({ status: 'checking' });
      started = begin(null);
      return started;
    },
    // Username and password sign-in. The caller clears its password field whatever the outcome.
    async login(username: string, password: string): Promise<CredentialResult> {
      try {
        authenticated(await api.loginWithPassword(username, password));
        return { ok: true };
      } catch (error) {
        if (error instanceof ApiError && error.kind === 'http' && error.status === 429) return { ok: false, kind: 'throttled', retryAfter: error.retryAfter };
        if (unreachable(error) && !(error instanceof ApiError && error.status === 404)) return { ok: false, kind: 'unavailable' };
        return { ok: false, kind: 'rejected' };
      }
    },
    // The server ends the user's other sessions and unused login links; this session stays.
    async changePassword(current: string | null, next: string): Promise<CredentialResult> {
      try {
        authenticated(await api.changePassword(current === null ? { new_password: next } : { current_password: current, new_password: next }));
        return { ok: true };
      } catch (error) {
        if (error instanceof ApiError && error.kind === 'http' && error.status === 429) return { ok: false, kind: 'throttled', retryAfter: error.retryAfter };
        return { ok: false, kind: 'error', error };
      }
    },
    // Re-read the session after a refusal that may mean the role changed (for example a lost admin role).
    refresh(): Promise<void> {
      if (state.status !== 'authenticated') return Promise.resolve();
      return recover('expired');
    },
    async logout() {
      if (state.status !== 'authenticated') return;
      set({ ...state, loggingOut: true, logoutFailed: false });
      try {
        await api.deleteSession();
        anonymous('logged_out');
      } catch (error) {
        if (error instanceof ApiError && error.status === 401) anonymous('logged_out');
        else if (state.status === 'authenticated') set({ ...state, loggingOut: false, logoutFailed: true });
      }
    },
    dispose() {
      clearTimeout(timer);
      listeners.clear();
    },
  };
}

export type SessionController = ReturnType<typeof createSessionController>;
