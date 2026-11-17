import { ApiError } from './api';
import { ko as t } from './locales/ko';

export const TERMINAL_RUN = new Set(['completed', 'partial', 'failed', 'cancelled']);
const CANCELLABLE_RUN = new Set(['queued', 'preparing', 'running', 'finalizing']);

export const isKnownRunStatus = (status: string) => Object.hasOwn(t.runs.statusLabels, status);
export const isTerminalRun = (status: string) => TERMINAL_RUN.has(status);
export const canCancelRun = (status: string) => CANCELLABLE_RUN.has(status);

// An unrecognised status is shown as unknown and is never read as success or completion.
export function runStatusLabel(status: string): string {
  return isKnownRunStatus(status) ? t.runs.statusLabels[status]! : t.runs.statusUnknown;
}

export function shorten(value: string, max = 48): string {
  return value.length > max ? `${value.slice(0, max)}…` : value;
}

export type ErrorText = { message: string; detail: string | null; requestId: string | null };

export function describeError(error: unknown): ErrorText {
  if (!(error instanceof ApiError)) return { message: t.errors.unknown, detail: null, requestId: null };
  const requestId = error.requestId;
  if (error.kind === 'network') return { message: t.errors.network, detail: null, requestId };
  if (error.kind === 'invalid_response') return { message: t.errors.invalid_response, detail: null, requestId };
  const known = Object.hasOwn(t.errors.codes, error.code) ? t.errors.codes[error.code] : undefined;
  // The server's own wording is shown only for input validation, as plain text.
  const detail = error.code === 'VALIDATION_FAILED' ? error.serverMessage : null;
  if (known) return { message: known, detail, requestId };
  const byStatus =
    error.status === 401 ? t.errors.unauthorized
    : error.status === 403 ? t.errors.forbidden
    : error.status === 404 ? t.errors.not_found
    : error.status === 409 ? t.errors.conflict
    : error.status === 400 || error.status === 422 ? t.errors.invalid
    : error.status >= 500 ? t.errors.server
    : t.errors.unknown;
  return { message: byStatus, detail, requestId };
}
