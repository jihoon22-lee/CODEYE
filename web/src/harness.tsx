import { act, render } from '@testing-library/react';
import { vi } from 'vitest';
import { App } from './App';
import { createApi } from './api';
import { createIdempotency } from './idempotency';
import { createSessionController } from './session';
import { PROJECT, SESSION, apiError, json, mockFetch, runView, type Call, type Handler } from './test-utils';

// An override may decline a request by returning undefined; the default backend then answers.
type Override = (call: Call) => ReturnType<Handler> | undefined;

export const ROOTS = { items: [{ id: 'root1', label: '팀 저장소 루트' }] };
export const PROVIDERS = { items: [{ id: 'gh', kind: 'github', host: 'github.com' }] };

// Default backend: an authenticated session with one project. Override per test.
export function backend(over: Partial<Record<string, Override>> = {}): Handler {
  return call => {
    const route = `${call.method} ${call.url.split('?')[0]}`;
    const custom = over[route] ?? over['*'];
    if (custom) {
      const result = custom(call);
      if (result !== undefined) return result;
    }
    switch (route) {
      case 'GET /api/v1/session':
      case 'POST /api/v1/session': return json(200, SESSION);
      case 'DELETE /api/v1/session': return new Response(null, { status: 204 });
      case 'GET /api/v1/source-roots': return json(200, ROOTS);
      case 'GET /api/v1/providers': return json(200, PROVIDERS);
      case 'GET /api/v1/projects': return json(200, { items: [PROJECT], next_cursor: null });
      case 'POST /api/v1/projects': return json(201, { ...PROJECT, id: 'p-new', name: (call.body as { name?: string }).name ?? '' });
      case 'POST /api/v1/projects/p1/runs': return json(202, { id: 'run1', status: 'queued', version: 1, url: '/api/v1/runs/run1' });
      case 'GET /api/v1/runs/run1': return json(200, runView());
      case 'POST /api/v1/runs/run1/cancel': return json(202, { id: 'run1', status: 'cancel_requested', version: 2, url: null });
    }
    return apiError(404, 'NOT_FOUND');
  };
}

export function renderApp(handler: Handler = backend(), options: { hash?: string } = {}) {
  const m = mockFetch(handler);
  const api = createApi({ fetch: m.fetch });
  const replaceState = vi.fn();
  const controller = createSessionController(api, {
    location: { hash: options.hash ?? '', pathname: '/', search: '' },
    history: { state: null, replaceState },
  });
  const idempotency = createIdempotency(null);
  const view = render(<App api={api} controller={controller} idempotency={idempotency} />);
  return { ...m, api, controller, idempotency, replaceState, ...view };
}

// Let queued promises and zero-delay timers run (works with vitest fake timers).
export const flush = async (times = 6) => {
  for (let i = 0; i < times; i++) await act(async () => { await vi.advanceTimersByTimeAsync(0); });
};
export const advance = (ms: number) => act(async () => { await vi.advanceTimersByTimeAsync(ms); });
export const runCalls = (calls: Call[]) => calls.filter(c => c.method === 'GET' && c.url === '/api/v1/runs/run1');
