import type { Api } from './api';
import { ApiError } from './api';

export type AnonymousReason = 'none' | 'launch_rejected' | 'expired' | 'logged_out';
export type SessionState =
  | { status: 'checking' }
  | { status: 'unavailable' }
  | { status: 'anonymous'; reason: AnonymousReason }
  | { status: 'authenticated'; userId: string; expiresAt: string; loggingOut: boolean; logoutFailed: boolean };

type Env = {
  location: Pick<Location, 'hash' | 'pathname' | 'search'>;
  history: Pick<History, 'replaceState' | 'state'>;
};

const MAX_TIMER_MS = 2 ** 31 - 1;
const MIN_RECHECK_MS = 30_000;

// A launch link carries a single-use secret in the URL fragment (#launch=...). It is read
// once and the fragment is removed from the address bar and history entry before any
// network call is made. The secret is never stored, logged or rendered.
export function takeLaunchToken(env: Env): string | null {
  const hash = env.location.hash;
  if (!hash.startsWith('#')) return null;
  const params = new URLSearchParams(hash.slice(1));
  if (!params.has('launch')) return null;
  const token = params.get('launch');
  env.history.replaceState(env.history.state, '', env.location.pathname + env.location.search);
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
  const authenticated = (info: { user_id: string; csrf_token: string; expires_at: string }) => {
    api.setCsrf(info.csrf_token);
    set({ status: 'authenticated', userId: info.user_id, expiresAt: info.expires_at, loggingOut: false, logoutFailed: false });
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
