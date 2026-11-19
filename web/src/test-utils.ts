import { vi } from 'vitest';

export type Call = { method: string; url: string; headers: Record<string, string>; body: unknown; credentials?: RequestCredentials };
export type Handler = (call: Call) => Response | Promise<Response> | 'network';

export const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
export const apiError = (status: number, code: string, message = 'm', retryable = false, details?: Record<string, unknown>, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify({ error: { code, message, retryable, request_id: 'req-1', ...(details ? { details } : {}) } }), {
    status, headers: { 'Content-Type': 'application/json', ...headers },
  });
// A throttled sign-in or password change: the same wait in the header and in details.
export const rateLimited = (seconds: number) =>
  apiError(429, 'RATE_LIMITED', 'too many attempts', true, { retry_after_seconds: seconds }, { 'Retry-After': String(seconds) });

// A recording fetch. The handler decides every response; 'network' simulates a lost connection.
export function mockFetch(handler: Handler) {
  const calls: Call[] = [];
  const fn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const headers: Record<string, string> = {};
    new Headers(init?.headers).forEach((value, key) => { headers[key.toLowerCase()] = value; });
    const call: Call = {
      method: init?.method ?? 'GET',
      url: String(input),
      headers,
      body: typeof init?.body === 'string' ? JSON.parse(init.body) : undefined,
      credentials: init?.credentials,
    };
    calls.push(call);
    const result = await handler(call);
    if (result === 'network') throw new TypeError('Failed to fetch');
    return result;
  });
  return { fetch: fn as unknown as typeof fetch, calls, fn };
}

export const SESSION = {
  user_id: '11111111-2222-4333-8444-555555555555',
  csrf_token: 'csrf-secret-value',
  expires_at: '2099-01-01T00:00:00Z',
  username: 'dana',
  display_name: 'Dana',
  is_admin: false,
  must_change_password: false,
  has_password: true,
};

export const PROJECT = {
  id: 'p1',
  name: 'sample',
  source: { kind: 'local', source_root_id: 'root1', relative_path: 'team/service' },
  revision: 1,
  source_status: 'declared_unverified',
  capability_gaps: ['source_access_unverified', 'execution_unavailable', 'report_unavailable'],
  last_run: null,
  role: 'owner',
  can_run: true,
  can_manage: true,
};

export const runView = (over: Record<string, unknown> = {}, progress: Record<string, unknown> = {}) => ({
  id: 'run1',
  project_id: 'p1',
  status: 'queued',
  version: 1,
  snapshot_id: null,
  quality_verdict: null,
  progress: { stage: 'queued', plan_revision: null, completed_tasks: 0, total_tasks: null, estimated_percent: null, waiting_reason: 'execution_unavailable', ...progress },
  ...over,
});

// A scripted EventSource: the test opens, fails and emits messages by hand.
export class FakeEventSource {
  static all: FakeEventSource[] = [];
  readyState = 0;
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  closed = false;
  private listeners = new Map<string, ((event: MessageEvent) => void)[]>();
  constructor(readonly url: string) { FakeEventSource.all.push(this); }
  static factory = (url: string) => new FakeEventSource(url);
  static reset() { FakeEventSource.all = []; }
  static last(): FakeEventSource { return FakeEventSource.all[FakeEventSource.all.length - 1]!; }
  addEventListener(type: string, listener: (event: MessageEvent) => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener]);
  }
  close() { this.readyState = 2; this.closed = true; }
  open() { this.readyState = 1; this.onopen?.(new Event('open')); }
  fail() { this.readyState = 2; this.onerror?.(new Event('error')); }
  // An unnamed message is an event row (id = sequence); a named one is run.view or a control.
  emit(event: string | null, data: unknown, id?: string) {
    const message = new MessageEvent(event ?? 'message', { data: JSON.stringify(data), lastEventId: id ?? '' });
    if (event === null) this.onmessage?.(message);
    else for (const l of this.listeners.get(event) ?? []) l(message);
  }
  row(sequence: number, type = 'task.status') { this.emit(null, { run_id: 'run1', sequence, version: 1, type, at: '2026-11-18T03:00:00Z' }, String(sequence)); }
}
