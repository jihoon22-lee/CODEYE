import { vi } from 'vitest';

export type Call = { method: string; url: string; headers: Record<string, string>; body: unknown; credentials?: RequestCredentials };
export type Handler = (call: Call) => Response | Promise<Response> | 'network';

export const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
export const apiError = (status: number, code: string, message = 'm', retryable = false) =>
  json(status, { error: { code, message, retryable, request_id: 'req-1' } });

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

export const SESSION = { user_id: '11111111-2222-4333-8444-555555555555', csrf_token: 'csrf-secret-value', expires_at: '2099-01-01T00:00:00Z' };

export const PROJECT = {
  id: 'p1',
  name: 'sample',
  source: { kind: 'local', source_root_id: 'root1', relative_path: 'team/service' },
  revision: 1,
  source_status: 'declared_unverified',
  capability_gaps: ['source_access_unverified', 'execution_unavailable', 'report_unavailable'],
  last_run: null,
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
