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
  // A folder that holds CODEYE's data: the server names the folders involved.
  const detail = error.code === 'VALIDATION_FAILED' || error.code === 'FOLDER_HOLDS_CODEYE_DATA' ? error.serverMessage : null;
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

const pad = (n: number) => String(n).padStart(2, '0');
// Local wall-clock text for "last confirmed" and stage times.
export function formatClock(d: Date): string {
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}
export function formatInstant(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${formatClock(d)}`;
}

// "icon label" for a run status; an unrecognised status keeps its raw value visible and is never read as success.
export function runStatusText(status: string): string {
  if (!isKnownRunStatus(status)) return `${t.runStatusUnknownIcon} ${t.runStatusRaw(t.runs.statusUnknown, shorten(status, 32))}`;
  return `${t.runStatusIcons[status] ?? t.runStatusUnknownIcon} ${t.runs.statusLabels[status]}`;
}

export const STAGE_ORDER = ['source', 'environment', 'documents', 'build', 'test', 'static_analysis', 'code_review', 'report'];
export const stageName = (key: string): string =>
  Object.hasOwn(t.stages.names, key) ? t.stages.names[key]! : t.stages.unknownName(shorten(key, 40));
export const stageStateText = (state: string): string =>
  Object.hasOwn(t.stages.states, state) ? t.stages.states[state]! : t.stages.unknownState(shorten(state, 40));
export function orderStages<T extends { key: string }>(stages: T[]): T[] {
  const rank = (k: string) => { const i = STAGE_ORDER.indexOf(k); return i < 0 ? STAGE_ORDER.length : i; };
  return stages.map((s, i) => ({ s, i })).sort((a, b) => rank(a.s.key) - rank(b.s.key) || a.i - b.i).map(x => x.s);
}

// A project role in words; an unrecognised role keeps its raw value and grants nothing.
export const roleLabel = (role: string): string =>
  Object.hasOwn(t.accounts.roles, role) ? t.accounts.roles[role]! : t.accounts.roleUnknown(shorten(role, 32));
