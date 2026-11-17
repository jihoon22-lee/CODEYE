// Same-origin client for the CODEYE control API (docs/11, api/openapi.yaml).
// Credentials are the HttpOnly session cookie, which the browser attaches itself.
// The CSRF token lives only in this object's memory.

export type ErrorKind = 'network' | 'http' | 'invalid_response' | 'aborted';

export class ApiError extends Error {
  constructor(
    readonly kind: ErrorKind,
    readonly status: number,
    readonly code: string,
    readonly retryable: boolean,
    readonly requestId: string | null = null,
    // Server text, shown only for validation-style rejections; never for session calls.
    readonly serverMessage: string | null = null,
  ) {
    super(`${kind}:${status}:${code}`);
    this.name = 'ApiError';
  }
}

export type Source =
  | { kind: 'local'; source_root_id: string; relative_path: string }
  | { kind: 'git'; provider_id: string; repository_url: string; branch: string };

export type Project = {
  id: string;
  name: string;
  source: Source;
  revision: number;
  source_status: string;
  capability_gaps: string[];
  last_run: { id: string; status: string } | null;
};
export type ProjectPage = { items: Project[]; next_cursor: string | null };
export type SourceRoot = { id: string; label: string };
export type Provider = { id: string; kind: string; host: string }; // kind: github | ghes
export type SessionInfo = { user_id: string; csrf_token: string; expires_at: string };
export type RunAck = { id: string; status: string; version: number; url: string | null };
export type RunProgress = {
  stage: string;
  plan_revision: number | null;
  completed_tasks: number;
  total_tasks: number | null;
  estimated_percent: number | null;
  waiting_reason: string | null;
};
export type RunView = {
  id: string;
  project_id: string;
  status: string; // open string: an unrecognised value must reach the UI as "unknown"
  version: number;
  snapshot_id: string | null;
  quality_verdict: string | null;
  progress: RunProgress;
};

export const RUN_BODY = { profile: 'deep-review', source_policy: 'latest_at_acquire', scope: { kind: 'full' } } as const;

export type CreateProjectBody = { name?: string; source: Source };

export type ApiOptions = {
  fetch?: typeof fetch;
  onUnauthorized?: () => void;
};

type Query = { method: string; path: string; body?: unknown; headers?: Record<string, string>; signal?: AbortSignal; session?: boolean };

export function createApi(options: ApiOptions = {}) {
  let csrf: string | null = null;
  let onUnauthorized = options.onUnauthorized;
  const doFetch: typeof fetch = (...args) => (options.fetch ?? globalThis.fetch)(...args);

  async function request<T>(q: Query, parse: (value: unknown) => T): Promise<T> {
    const headers: Record<string, string> = { Accept: 'application/json', ...q.headers };
    if (q.body !== undefined) headers['Content-Type'] = 'application/json';
    if (q.method !== 'GET' && csrf) headers['X-CODEYE-CSRF'] = csrf;
    let response: Response;
    try {
      response = await doFetch(q.path, {
        method: q.method,
        headers,
        body: q.body === undefined ? undefined : JSON.stringify(q.body),
        credentials: 'same-origin',
        cache: 'no-store',
        redirect: 'error',
        referrerPolicy: 'no-referrer',
        signal: q.signal,
      });
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') throw new ApiError('aborted', 0, 'aborted', false);
      throw new ApiError('network', 0, 'network', true);
    }
    let text: string;
    try {
      text = await response.text();
    } catch {
      throw new ApiError('network', response.status, 'network', true);
    }
    if (!response.ok) {
      const error = httpError(response.status, text);
      if (response.status === 401 && !q.session) onUnauthorized?.();
      throw error;
    }
    let value: unknown = undefined;
    if (text !== '') {
      try {
        value = JSON.parse(text);
      } catch {
        // An HTML page (for example a dev server's fallback) is not an API answer.
        throw new ApiError('invalid_response', response.status, 'invalid_response', false);
      }
    }
    try {
      return parse(value);
    } catch {
      throw new ApiError('invalid_response', response.status, 'invalid_response', false);
    }
  }

  const json = <T>(q: Query, parse: (v: unknown) => T) => request(q, parse);
  const run = (id: string) => `/api/v1/runs/${encodeURIComponent(id)}`;

  return {
    setCsrf(token: string | null) { csrf = token; },
    setOnUnauthorized(handler: (() => void) | undefined) { onUnauthorized = handler; },
    getSession: (signal?: AbortSignal) => json({ method: 'GET', path: '/api/v1/session', signal, session: true }, parseSession),
    createSession: (launchToken: string) => json({ method: 'POST', path: '/api/v1/session', body: { launch_token: launchToken }, session: true }, parseSession),
    deleteSession: () => json({ method: 'DELETE', path: '/api/v1/session', session: true }, () => undefined),
    listSourceRoots: (signal?: AbortSignal) => json({ method: 'GET', path: '/api/v1/source-roots', signal }, items(parseSourceRoot)),
    listProviders: (signal?: AbortSignal) => json({ method: 'GET', path: '/api/v1/providers', signal }, items(parseProvider)),
    listProjects: (cursor?: string | null, signal?: AbortSignal) =>
      json({ method: 'GET', path: '/api/v1/projects' + (cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''), signal }, parseProjectPage),
    createProject: (body: CreateProjectBody) => json({ method: 'POST', path: '/api/v1/projects', body }, parseProject),
    createRun: (projectId: string, idempotencyKey: string) =>
      json({ method: 'POST', path: `/api/v1/projects/${encodeURIComponent(projectId)}/runs`, body: RUN_BODY, headers: { 'Idempotency-Key': idempotencyKey } }, parseAck),
    getRun: (id: string, signal?: AbortSignal) => json({ method: 'GET', path: run(id), signal }, parseRun),
    cancelRun: (id: string) => json({ method: 'POST', path: `${run(id)}/cancel` }, parseAck),
  };
}

export type Api = ReturnType<typeof createApi>;

function httpError(status: number, text: string): ApiError {
  let code = `http_${status}`;
  let retryable = status >= 500 || status === 429 || status === 408;
  let requestId: string | null = null;
  let message: string | null = null;
  try {
    const body: unknown = JSON.parse(text);
    const error = isRecord(body) && isRecord(body.error) ? body.error : null;
    if (error) {
      if (typeof error.code === 'string' && error.code !== '') code = error.code.slice(0, 80);
      if (typeof error.retryable === 'boolean') retryable = error.retryable;
      if (typeof error.request_id === 'string') requestId = error.request_id.slice(0, 80);
      if (typeof error.message === 'string') message = error.message.slice(0, 300);
    }
  } catch { /* non-JSON error body: keep the status-derived defaults */ }
  return new ApiError('http', status, code, retryable, requestId, message);
}

// ---- response validation: anything unexpected is an invalid_response, never a guess ----

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}
function str(v: unknown): string {
  if (typeof v !== 'string' || v === '') throw new TypeError('string');
  return v;
}
function nullableStr(v: unknown): string | null {
  if (v === null || v === undefined) return null;
  return str(v);
}
function int(v: unknown, min = 0): number {
  if (typeof v !== 'number' || !Number.isInteger(v) || v < min) throw new TypeError('integer');
  return v;
}
function nullableInt(v: unknown, min = 0): number | null {
  return v === null || v === undefined ? null : int(v, min);
}
function rec(v: unknown): Record<string, unknown> {
  if (!isRecord(v)) throw new TypeError('object');
  return v;
}
function list<T>(item: (v: unknown) => T) {
  return (v: unknown): T[] => {
    if (!Array.isArray(v)) throw new TypeError('array');
    return v.map(item);
  };
}

// source-roots and providers answer {items: [...]}, never a bare array.
function items<T>(item: (v: unknown) => T) {
  return (v: unknown): T[] => list(item)(rec(v).items);
}

function parseSession(v: unknown): SessionInfo {
  const o = rec(v);
  const expires = str(o.expires_at);
  if (Number.isNaN(Date.parse(expires))) throw new TypeError('date');
  return { user_id: str(o.user_id), csrf_token: str(o.csrf_token), expires_at: expires };
}
function parseSourceRoot(v: unknown): SourceRoot {
  const o = rec(v);
  return { id: str(o.id), label: str(o.label) };
}
function parseProvider(v: unknown): Provider {
  const o = rec(v);
  return { id: str(o.id), kind: str(o.kind), host: str(o.host) };
}
function parseSource(v: unknown): Source {
  const o = rec(v);
  if (o.kind === 'local') return { kind: 'local', source_root_id: str(o.source_root_id), relative_path: str(o.relative_path) };
  if (o.kind === 'git') return { kind: 'git', provider_id: str(o.provider_id), repository_url: str(o.repository_url), branch: str(o.branch) };
  throw new TypeError('source kind');
}
function parseProject(v: unknown): Project {
  const o = rec(v);
  return {
    id: str(o.id),
    name: typeof o.name === 'string' ? o.name : '',
    source: parseSource(o.source),
    revision: int(o.revision, 1),
    source_status: str(o.source_status),
    capability_gaps: list(str)(o.capability_gaps ?? []),
    last_run: o.last_run === null || o.last_run === undefined ? null : parseLastRun(o.last_run),
  };
}
function parseLastRun(v: unknown): { id: string; status: string } {
  const o = rec(v);
  return { id: str(o.id), status: str(o.status) };
}
function parseProjectPage(v: unknown): ProjectPage {
  const o = rec(v);
  return { items: list(parseProject)(o.items), next_cursor: nullableStr(o.next_cursor) };
}
function parseAck(v: unknown): RunAck {
  const o = rec(v);
  return { id: str(o.id), status: str(o.status), version: int(o.version, 1), url: nullableStr(o.url) };
}
function parseRun(v: unknown): RunView {
  const o = rec(v);
  const p = rec(o.progress);
  const percent = p.estimated_percent === null || p.estimated_percent === undefined ? null : p.estimated_percent;
  if (percent !== null && (typeof percent !== 'number' || percent < 0 || percent > 100)) throw new TypeError('percent');
  return {
    id: str(o.id),
    project_id: str(o.project_id),
    status: str(o.status),
    version: int(o.version, 1),
    snapshot_id: nullableStr(o.snapshot_id),
    quality_verdict: nullableStr(o.quality_verdict),
    progress: {
      stage: typeof p.stage === 'string' ? p.stage : '',
      plan_revision: nullableInt(p.plan_revision, 1),
      completed_tasks: int(p.completed_tasks),
      total_tasks: nullableInt(p.total_tasks),
      estimated_percent: percent,
      waiting_reason: nullableStr(p.waiting_reason),
    },
  };
}
