import { parseAnalysisReport, reportURL } from './analysisReport';
import { comparisonURL, parseComparison, parseFindingReview, type FindingReviewBody } from './comparison';
import {
  auditQuery, parseAdminProviders, parseAdminSourceRoots, parseAuditPage, parseMember, parseMembers, parseRole, parseSession,
  parseUser, parseUserPage, parseUserWithLink, type AuditFilter, type CreateUserBody, type MemberRole, type ProjectRole,
} from './accounts';
// Same-origin client for the CODEYE control API (docs/API.md, api/openapi.yaml).
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
    // Seconds to wait before the next attempt (429: Retry-After or details.retry_after_seconds).
    readonly retryAfter: number | null = null,
  ) {
    super(`${kind}:${status}:${code}`);
    this.name = 'ApiError';
  }
}

export type Source =
  | { kind: 'local'; source_root_id: string; relative_path: string }
  // branch absent: the repository's default branch, resolved at each acquisition.
  | { kind: 'git'; provider_id: string; repository_url: string; branch?: string };

// A project's own analysis choices; personal-mode nodes follow them. fetch_dependencies:
// fetch the third-party dependencies the modules declare before the analyzers run.
export type AnalysisChoices = { send_source_to_model: boolean; run_build_scripts: boolean; fetch_dependencies: boolean };

export type Project = {
  id: string;
  name: string;
  source: Source;
  revision: number;
  source_status: string;
  capability_gaps: string[];
  last_run: { id: string; status: string } | null;
  // The caller's role and what it allows (the server still enforces every action).
  role: ProjectRole;
  can_run: boolean;
  can_manage: boolean;
  // Optional: older servers do not send it (docs/API.md).
  activity?: ProjectActivity;
  analysis?: AnalysisChoices;
};
export type ProjectPage = { items: Project[]; next_cursor: string | null };
export type SourceRoot = { id: string; label: string };
export type Provider = { id: string; kind: string; host: string }; // kind: github | ghes
export type { SessionInfo } from './accounts';
export type RunAck = { id: string; status: string; version: number; url: string | null };
// One displayed stage. `key` and `state` are open strings: an unrecognised value must reach the UI
// as "unknown (raw)", never be read as success.
export type StageView = {
  key: string;
  state: string;
  planned: number | null; // a number only when the plan is confirmed
  finished: number;
  succeeded: number;
  counts: Record<string, number>; // non-zero terminal statuses only
  reasons: string[];
  started_at: string | null;
  finished_at: string | null;
};
export type StageSummary = {
  detail: string; // recorded | not_recorded (open string)
  stages: StageView[];
  last_event_at: string | null;
  checked_at: string | null;
};
// detail is null exactly when the project has no run (run === null).
export type ProjectActivity = Omit<StageSummary, 'detail'> & {
  detail: string | null;
  run: { id: string; status: string; requested_at: string } | null;
  other_active_runs: number;
  waiting_reason?: string | null; // only when the representative run is queued
  last_report: { run_id: string; revision: number; published_at: string } | null;
};
// One plan revision after the first: a task added after the plan was fixed.
export type PlanChange = { revision: number; reason: string; stage: string | null; weight: number; planned_weight: number; recorded_at: string | null };
export type RunProgress = {
  stage: string;
  plan_revision: number | null;
  completed_tasks: number;
  total_tasks: number | null;
  estimated_percent: number | null;
  waiting_reason: string | null;
  // Optional additions; undefined when the server predates them (treated as not recorded).
  stages?: StageSummary;
  // Optional additions: the weighted plan behind estimated_percent and why its denominator changed.
  planned_weight?: number | null;
  finished_weight?: number | null;
  weight_basis?: string | null; // task_count | declared | mixed (open string)
  plan_changes?: PlanChange[];
};
// Why the run exists. kind is an open string; schedule runs name their occurrence.
export type RunTrigger = { kind: string; schedule_id?: string; scheduled_for?: string };
export type RunView = {
  id: string;
  project_id: string;
  status: string; // open string: an unrecognised value must reach the UI as "unknown"
  version: number;
  // The run's event sequence when the view was read; the event stream cursor. Absent on servers without streaming.
  event_sequence?: number;
  trigger?: RunTrigger;
  snapshot_id: string | null;
  quality_verdict: string | null;
  reportRevision?: number;
  progress: RunProgress;
  // When retention (codeye cleanup) removed this run's raw tool output; the report itself is unchanged.
  raw_evidence_removed_at?: string | null;
};

export const RUN_BODY = { profile: 'deep-review', source_policy: 'latest_at_acquire', scope: { kind: 'full' } } as const;

// Either a declared source, or (personal mode) an absolute folder path the server maps onto a readable folder.
export type CreateProjectBody = { name?: string; analysis?: AnalysisChoices } & ({ source: Source } | { local_path: string });
export type RetryAck = RunAck & { retry: string }; // same_run | new_run (open string)
// POST /runs/{id}/recover: the run that runs again (or the confirmed run when retry is not_queued).
export type RecoveryAck = RetryAck & { attempt_id: string; epoch: number };

// GET /setup: what this installation can do (personal mode); a server-mode controller sends nulls.
export type SetupTool = { name: string; status: string; version: string | null; path: string | null; hint: string | null; reason: string | null };
export type Setup = {
  mode: string; // personal | server (open string)
  model: { configured: boolean; id: string | null; probe: string | null } | null;
  git: { available: boolean; hosts: string[] } | null;
  readable_folders: string[];
  analysis_defaults: { local: AnalysisChoices; git: AnalysisChoices };
  tools: { detected_at: string | null; tools: SetupTool[] } | null;
};

// ---- schedules (members list; managers create and delete) ----
export type ScheduleOccurrence = { scheduled_for: string; state: string; reason: string | null; missed_from: string | null; decided_at: string; run_id: string | null };
export type Schedule = {
  id: string;
  project_id: string;
  cron: string;
  timezone: string;
  overlap: string;
  misfire: string;
  enabled: boolean;
  version: number;
  next_run_at: string | null;
  created_at: string;
  updated_at: string;
  last_occurrence: ScheduleOccurrence | null;
  pending_occurrence: { scheduled_for: string; missed_from: string | null } | null;
};
export type ScheduleList = { items: Schedule[]; can_manage: boolean };
export type CreateScheduleBody = { cron: string; timezone: string; overlap?: string; misfire?: string; run: typeof RUN_BODY };

// The subset of EventSource the run stream uses; injectable for tests.
export type EventSourceLike = {
  readonly readyState: number;
  onopen: ((event: Event) => void) | null;
  onmessage: ((event: MessageEvent) => void) | null;
  onerror: ((event: Event) => void) | null;
  addEventListener(type: string, listener: (event: MessageEvent) => void): void;
  close(): void;
};
export type EventSourceFactory = (url: string) => EventSourceLike;

function defaultEventSource(): EventSourceFactory | null {
  const ES = (globalThis as { EventSource?: new (url: string) => EventSourceLike }).EventSource;
  // Same origin: the browser attaches the session cookie itself.
  return typeof ES === 'function' ? url => new ES(url) : null;
}

export type ApiOptions = {
  fetch?: typeof fetch;
  onUnauthorized?: () => void;
  // A 403 PASSWORD_CHANGE_REQUIRED from any call: the session may only change its password.
  onPasswordChangeRequired?: () => void;
  // undefined: the browser's EventSource when it exists; null: never stream (polling only).
  eventSource?: EventSourceFactory | null;
};

type Query = { method: string; path: string; body?: unknown; headers?: Record<string, string>; signal?: AbortSignal; session?: boolean };

export function createApi(options: ApiOptions = {}) {
  let csrf: string | null = null;
  let onUnauthorized = options.onUnauthorized;
  let onPasswordChangeRequired = options.onPasswordChangeRequired;
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
      const error = httpError(response.status, text, response.headers);
      if (response.status === 401 && !q.session) onUnauthorized?.();
      if (response.status === 403 && error.code === 'PASSWORD_CHANGE_REQUIRED') onPasswordChangeRequired?.();
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
  const schedules = (projectId: string) => `/api/v1/projects/${encodeURIComponent(projectId)}/schedules`;
  const project = (id: string) => `/api/v1/projects/${encodeURIComponent(id)}`;
  const adminUser = (id: string) => `/api/v1/admin/users/${encodeURIComponent(id)}`;
  const grant = (rootId: string, userId: string) => `/api/v1/admin/source-roots/${encodeURIComponent(rootId)}/grants/${encodeURIComponent(userId)}`;
  const eventSource = options.eventSource === undefined ? defaultEventSource() : options.eventSource;

  return {
    setCsrf(token: string | null) { csrf = token; },
    setOnUnauthorized(handler: (() => void) | undefined) { onUnauthorized = handler; },
    setOnPasswordChangeRequired(handler: (() => void) | undefined) { onPasswordChangeRequired = handler; },
    getSession: (signal?: AbortSignal) => json({ method: 'GET', path: '/api/v1/session', signal, session: true }, parseSession),
    createSession: (launchToken: string) => json({ method: 'POST', path: '/api/v1/session', body: { launch_token: launchToken }, session: true }, parseSession),
    deleteSession: () => json({ method: 'DELETE', path: '/api/v1/session', session: true }, () => undefined),
    // Every failed sign-in is the same 401; it is not a lost session, so it does not call onUnauthorized.
    loginWithPassword: (username: string, password: string) =>
      json({ method: 'POST', path: '/api/v1/session', body: { username, password }, session: true }, parseSession),
    // current_password is sent only when the user has one (has_password).
    changePassword: (body: { current_password?: string; new_password: string }) =>
      json({ method: 'POST', path: '/api/v1/session/password', body }, parseSession),
    // ---- project members and project audit (owner and manager) ----
    listMembers: (projectId: string, signal?: AbortSignal) => json({ method: 'GET', path: `${project(projectId)}/members`, signal }, parseMembers),
    addMember: (projectId: string, username: string, role: MemberRole) =>
      json({ method: 'POST', path: `${project(projectId)}/members`, body: { username, role } }, parseMember),
    setMemberRole: (projectId: string, userId: string, role: MemberRole) =>
      json({ method: 'PUT', path: `${project(projectId)}/members/${encodeURIComponent(userId)}`, body: { role } }, parseMember),
    removeMember: (projectId: string, userId: string) =>
      json({ method: 'DELETE', path: `${project(projectId)}/members/${encodeURIComponent(userId)}` }, () => undefined),
    listProjectAudit: (projectId: string, filter: AuditFilter, cursor: string | null, signal?: AbortSignal) =>
      json({ method: 'GET', path: `${project(projectId)}/audit${auditQuery(filter, cursor)}`, signal }, parseAuditPage),
    // ---- administrator (accounts and access grants only; no project access) ----
    listUsers: (cursor: string | null, signal?: AbortSignal) =>
      json({ method: 'GET', path: `/api/v1/admin/users?limit=200${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''}`, signal }, parseUserPage),
    createUser: (body: CreateUserBody) => json({ method: 'POST', path: '/api/v1/admin/users', body }, parseUserWithLink),
    disableUser: (id: string) => json({ method: 'POST', path: `${adminUser(id)}/disable` }, parseUser),
    setUserAdmin: (id: string, isAdmin: boolean) => json({ method: 'PUT', path: `${adminUser(id)}/admin`, body: { is_admin: isAdmin } }, parseUser),
    resetPassword: (id: string) => json({ method: 'POST', path: `${adminUser(id)}/password-reset` }, parseUserWithLink),
    listAdminSourceRoots: (signal?: AbortSignal) => json({ method: 'GET', path: '/api/v1/admin/source-roots', signal }, parseAdminSourceRoots),
    grantSourceRoot: (rootId: string, userId: string) => json({ method: 'PUT', path: grant(rootId, userId) }, () => undefined),
    revokeSourceRoot: (rootId: string, userId: string) => json({ method: 'DELETE', path: grant(rootId, userId) }, () => undefined),
    listAdminProviders: (signal?: AbortSignal) => json({ method: 'GET', path: '/api/v1/admin/providers', signal }, parseAdminProviders),
    listAudit: (filter: AuditFilter, cursor: string | null, signal?: AbortSignal) =>
      json({ method: 'GET', path: `/api/v1/audit${auditQuery(filter, cursor)}`, signal }, parseAuditPage),
    listSourceRoots: (signal?: AbortSignal) => json({ method: 'GET', path: '/api/v1/source-roots', signal }, items(parseSourceRoot)),
    listProviders: (signal?: AbortSignal) => json({ method: 'GET', path: '/api/v1/providers', signal }, items(parseProvider)),
    listProjects: (cursor?: string | null, signal?: AbortSignal) =>
      json({ method: 'GET', path: '/api/v1/projects' + (cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''), signal }, parseProjectPage),
    // 201 a new project; 200 (personal mode) the existing project of the same folder or repository.
    createProject: (body: CreateProjectBody) => json({ method: 'POST', path: '/api/v1/projects', body }, parseProject),
    setProjectAnalysis: (projectId: string, choices: AnalysisChoices) =>
      json({ method: 'PUT', path: `${project(projectId)}/analysis`, body: choices }, parseProject),
    getSetup: (signal?: AbortSignal) => json({ method: 'GET', path: '/api/v1/setup', signal }, parseSetup),
    // The run to follow: the same run again (same_run) or a new one (new_run).
    retryRun: (id: string) => json({ method: 'POST', path: `${run(id)}/retry` }, parseRetryAck),
    // A recovery_required run: the user states that no process of its attempt is left on the node.
    recoverRun: (id: string) => json({ method: 'POST', path: `${run(id)}/recover`, body: { cleanup_confirmed: true } }, parseRecoveryAck),
    createRun: (projectId: string, idempotencyKey: string) =>
      json({ method: 'POST', path: `/api/v1/projects/${encodeURIComponent(projectId)}/runs`, body: RUN_BODY, headers: { 'Idempotency-Key': idempotencyKey } }, parseAck),
    getRun: (id: string, signal?: AbortSignal) => json({ method: 'GET', path: run(id), signal }, parseRun),
    getReport: (id: string, signal?: AbortSignal) => json({ method: 'GET', path: reportURL(id, 'json', true), signal }, v => parseAnalysisReport(v, id)),
    cancelRun: (id: string) => json({ method: 'POST', path: `${run(id)}/cancel` }, parseAck),
    // null when this browser (or test) has no EventSource: callers poll instead.
    openRunEvents: eventSource === null ? null : (id: string, cursor: number) => eventSource(runEventsURL(id, cursor)),
    listSchedules: (projectId: string, signal?: AbortSignal) => json({ method: 'GET', path: schedules(projectId), signal }, parseScheduleList),
    createSchedule: (projectId: string, body: CreateScheduleBody, idempotencyKey: string) =>
      json({ method: 'POST', path: schedules(projectId), body, headers: { 'Idempotency-Key': idempotencyKey } }, parseSchedule),
    // The comparison of one published revision with its baseline, and human finding reviews.
    getComparison: (id: string, revision: number, signal?: AbortSignal) =>
      json({ method: 'GET', path: comparisonURL(id, revision), signal }, v => parseComparison(v, id, revision)),
    // version: the fingerprint's current review version (If-Match), or null for its first review (If-None-Match: *).
    createFindingReview: (projectId: string, body: FindingReviewBody, version: number | null, idempotencyKey: string) =>
      json({
        method: 'POST',
        path: `/api/v1/projects/${encodeURIComponent(projectId)}/finding-reviews`,
        body,
        headers: { 'Idempotency-Key': idempotencyKey, ...(version === null ? { 'If-None-Match': '*' } : { 'If-Match': `"${version}"` }) },
      }, parseFindingReview),
    deleteSchedule: (projectId: string, id: string, version: number) =>
      json({ method: 'DELETE', path: `${schedules(projectId)}/${encodeURIComponent(id)}`, headers: { 'If-Match': `"${version}"` } }, () => undefined),
  };
}

export type Api = ReturnType<typeof createApi>;

export const runEventsURL = (id: string, cursor: number) => `/api/v1/runs/${encodeURIComponent(id)}/events?cursor=${cursor}`;

// A wait in whole seconds, or null when the value is absent or not a sane non-negative number.
function seconds(v: unknown): number | null {
  const n = typeof v === 'string' && /^\d{1,6}$/.test(v.trim()) ? Number(v.trim()) : typeof v === 'number' ? v : NaN;
  return Number.isInteger(n) && n >= 0 && n <= 86_400 ? n : null;
}

function httpError(status: number, text: string, headers?: Headers): ApiError {
  let code = `http_${status}`;
  let retryable = status >= 500 || status === 429 || status === 408;
  let requestId: string | null = null;
  let message: string | null = null;
  let retryAfter = status === 429 ? seconds(headers?.get('Retry-After') ?? undefined) : null;
  try {
    const body: unknown = JSON.parse(text);
    const error = isRecord(body) && isRecord(body.error) ? body.error : null;
    if (error) {
      if (typeof error.code === 'string' && error.code !== '') code = error.code.slice(0, 80);
      if (typeof error.retryable === 'boolean') retryable = error.retryable;
      if (typeof error.request_id === 'string') requestId = error.request_id.slice(0, 80);
      if (typeof error.message === 'string') message = error.message.slice(0, 300);
      if (status === 429 && isRecord(error.details)) {
        // Both carry the same seconds; take the longer one if they ever disagree.
        const fromBody = seconds(error.details.retry_after_seconds);
        if (fromBody !== null) retryAfter = Math.max(retryAfter ?? 0, fromBody);
      }
    }
  } catch { /* non-JSON error body: keep the status-derived defaults */ }
  return new ApiError('http', status, code, retryable, requestId, message, retryAfter);
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
  if (o.kind === 'git') return { kind: 'git', provider_id: str(o.provider_id), repository_url: str(o.repository_url), ...(o.branch == null ? {} : { branch: str(o.branch) }) };
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
    role: parseRole(o.role),
    can_run: bool(o.can_run),
    can_manage: bool(o.can_manage),
    ...(o.activity == null ? {} : { activity: parseActivity(o.activity) }),
    ...(o.analysis == null ? {} : { analysis: parseChoices(o.analysis) }),
  };
}
function parseChoices(v: unknown): AnalysisChoices {
  const o = rec(v);
  // Older servers and stored records do not send fetch_dependencies: absent means on.
  return { send_source_to_model: bool(o.send_source_to_model), run_build_scripts: bool(o.run_build_scripts), fetch_dependencies: o.fetch_dependencies == null ? true : bool(o.fetch_dependencies) };
}
function optionalStr(v: unknown): string | null {
  return typeof v === 'string' && v !== '' ? v : null;
}
function parseSetup(v: unknown): Setup {
  const o = rec(v);
  const d = rec(o.analysis_defaults);
  let model: Setup['model'] = null;
  if (o.model != null) {
    const m = rec(o.model);
    model = { configured: bool(m.configured), id: nullableStr(m.id), probe: nullableStr(m.probe) };
  }
  let git: Setup['git'] = null;
  if (o.git != null) {
    const g = rec(o.git);
    git = { available: bool(g.available), hosts: list(str)(g.hosts ?? []) };
  }
  let tools: Setup['tools'] = null;
  if (o.tools != null) {
    const r = rec(o.tools);
    tools = {
      detected_at: optionalStr(r.detected_at),
      tools: list((x: unknown): SetupTool => {
        const t = rec(x);
        return { name: str(t.name), status: str(t.status), version: optionalStr(t.version), path: optionalStr(t.path), hint: optionalStr(t.hint), reason: optionalStr(t.reason) };
      })(r.tools ?? []),
    };
  }
  return { mode: str(o.mode), model, git, readable_folders: list(str)(o.readable_folders ?? []), analysis_defaults: { local: parseChoices(d.local), git: parseChoices(d.git) }, tools };
}
function date(v: unknown): string {
  const s = str(v);
  if (Number.isNaN(Date.parse(s))) throw new TypeError('date');
  return s;
}
function nullableDate(v: unknown): string | null {
  return v === null || v === undefined ? null : date(v);
}
function counts(v: unknown): Record<string, number> {
  if (v === null || v === undefined) return {};
  const o = rec(v);
  const out: Record<string, number> = {};
  for (const [k, n] of Object.entries(o)) out[k] = int(n);
  return out;
}
function parseStage(v: unknown): StageView {
  const o = rec(v);
  return {
    key: str(o.key),
    state: str(o.state),
    planned: nullableInt(o.planned),
    finished: int(o.finished ?? 0),
    succeeded: int(o.succeeded ?? 0),
    counts: counts(o.counts),
    reasons: list(str)(o.reasons ?? []),
    started_at: nullableDate(o.started_at),
    finished_at: nullableDate(o.finished_at),
  };
}
// Shared by project activity and run progress: detail, stages, last_event_at, checked_at.
function parseStageSummary(o: Record<string, unknown>): StageSummary {
  return { detail: str(o.detail), ...parseStageFacts(o) };
}
function parseStageFacts(o: Record<string, unknown>): Omit<StageSummary, 'detail'> {
  return {
    stages: list(parseStage)(o.stages ?? []),
    last_event_at: nullableDate(o.last_event_at),
    checked_at: nullableDate(o.checked_at),
  };
}
function parseActivity(v: unknown): ProjectActivity {
  const o = rec(v);
  let run: ProjectActivity['run'] = null;
  if (o.run != null) {
    const r = rec(o.run);
    run = { id: str(r.id), status: str(r.status), requested_at: date(r.requested_at) };
  }
  let lastReport: ProjectActivity['last_report'] = null;
  if (o.last_report != null) {
    const r = rec(o.last_report);
    lastReport = { run_id: str(r.run_id), revision: int(r.revision, 1), published_at: date(r.published_at) };
  }
  return {
    ...parseStageFacts(o),
    // The server sends detail: null exactly when there is no run.
    detail: run === null ? nullableStr(o.detail) : str(o.detail),
    run,
    other_active_runs: int(o.other_active_runs ?? 0),
    ...(o.waiting_reason === undefined ? {} : { waiting_reason: nullableStr(o.waiting_reason) }),
    last_report: lastReport,
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
function parseRetryAck(v: unknown): RetryAck {
  return { ...parseAck(v), retry: str(rec(v).retry) };
}
function parseRecoveryAck(v: unknown): RecoveryAck {
  const o = rec(v);
  return { ...parseRetryAck(v), attempt_id: str(o.attempt_id), epoch: int(o.epoch, 1) };
}
function parsePlanChange(v: unknown): PlanChange {
  const o = rec(v);
  return { revision: int(o.revision, 2), reason: str(o.reason), stage: nullableStr(o.stage), weight: int(o.weight, 1), planned_weight: int(o.planned_weight, 1), recorded_at: nullableDate(o.recorded_at) };
}
function parseTrigger(v: unknown): RunTrigger {
  const o = rec(v);
  return {
    kind: str(o.kind),
    ...(o.schedule_id == null ? {} : { schedule_id: str(o.schedule_id) }),
    ...(o.scheduled_for == null ? {} : { scheduled_for: date(o.scheduled_for) }),
  };
}
// Also parses the run.view event of the run event stream (the same document).
export function parseRun(v: unknown): RunView {
  const o = rec(v);
  const p = rec(o.progress);
  const percent = p.estimated_percent === null || p.estimated_percent === undefined ? null : p.estimated_percent;
  if (percent !== null && (typeof percent !== 'number' || percent < 0 || percent > 100)) throw new TypeError('percent');
  return {
    id: str(o.id),
    project_id: str(o.project_id),
    status: str(o.status),
    version: int(o.version, 1),
    ...(o.event_sequence === undefined ? {} : { event_sequence: int(o.event_sequence) }),
    ...(o.trigger == null ? {} : { trigger: parseTrigger(o.trigger) }),
    snapshot_id: nullableStr(o.snapshot_id),
    quality_verdict: nullableStr(o.quality_verdict),
    ...(o.report == null ? {} : {reportRevision:int(rec(o.report).revision, 1)}),
    ...(o.raw_evidence_removed_at == null ? {} : { raw_evidence_removed_at: date(o.raw_evidence_removed_at) }),
    progress: {
      stage: typeof p.stage === 'string' ? p.stage : '',
      plan_revision: nullableInt(p.plan_revision, 1),
      completed_tasks: int(p.completed_tasks),
      total_tasks: nullableInt(p.total_tasks),
      estimated_percent: percent,
      waiting_reason: nullableStr(p.waiting_reason),
      ...(p.detail == null ? {} : { stages: parseStageSummary(p) }),
      ...(p.planned_weight === undefined ? {} : { planned_weight: nullableInt(p.planned_weight) }),
      ...(p.finished_weight === undefined ? {} : { finished_weight: nullableInt(p.finished_weight) }),
      ...(p.weight_basis === undefined ? {} : { weight_basis: nullableStr(p.weight_basis) }),
      ...(p.plan_changes === undefined ? {} : { plan_changes: list(parsePlanChange)(p.plan_changes) }),
    },
  };
}
function parseOccurrence(v: unknown): ScheduleOccurrence {
  const o = rec(v);
  return { scheduled_for: date(o.scheduled_for), state: str(o.state), reason: nullableStr(o.reason), missed_from: nullableDate(o.missed_from), decided_at: date(o.decided_at), run_id: nullableStr(o.run_id) };
}
function bool(v: unknown): boolean {
  if (typeof v !== 'boolean') throw new TypeError('boolean');
  return v;
}
function parseSchedule(v: unknown): Schedule {
  const o = rec(v);
  let pending: Schedule['pending_occurrence'] = null;
  if (o.pending_occurrence != null) {
    const q = rec(o.pending_occurrence);
    pending = { scheduled_for: date(q.scheduled_for), missed_from: nullableDate(q.missed_from) };
  }
  return {
    id: str(o.id),
    project_id: str(o.project_id),
    cron: str(o.cron),
    timezone: str(o.timezone),
    overlap: str(o.overlap),
    misfire: str(o.misfire),
    enabled: bool(o.enabled),
    version: int(o.version, 1),
    next_run_at: nullableDate(o.next_run_at),
    created_at: date(o.created_at),
    updated_at: date(o.updated_at),
    last_occurrence: o.last_occurrence == null ? null : parseOccurrence(o.last_occurrence),
    pending_occurrence: pending,
  };
}
function parseScheduleList(v: unknown): ScheduleList {
  const o = rec(v);
  return { items: list(parseSchedule)(o.items), can_manage: bool(o.can_manage) };
}
