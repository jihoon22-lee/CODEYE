import { describe, expect, it } from 'vitest';
import { ApiError } from './api';
import { canCancelRun, describeError, isTerminalRun, runStatusLabel } from './format';
import { ko } from './locales/ko';

describe('run status helpers', () => {
  it('labels every documented status and treats anything else as unknown', () => {
    for (const s of ['queued', 'preparing', 'running', 'finalizing', 'cancel_requested', 'cancelling', 'recovery_required', 'completed', 'partial', 'failed', 'cancelled']) {
      expect(runStatusLabel(s)).not.toBe(ko.runs.statusUnknown);
    }
    for (const s of ['', 'success', 'done', 'COMPLETED', 'toString', '__proto__']) expect(runStatusLabel(s)).toBe(ko.runs.statusUnknown);
  });
  it('terminal and cancellable sets never include unknown statuses', () => {
    expect(['completed', 'partial', 'failed', 'cancelled'].every(isTerminalRun)).toBe(true);
    expect(isTerminalRun('success')).toBe(false);
    expect(canCancelRun('success')).toBe(false);
    expect(canCancelRun('recovery_required')).toBe(false);
    expect(['queued', 'preparing', 'running', 'finalizing'].every(canCancelRun)).toBe(true);
  });
});

describe('describeError', () => {
  const http = (status: number, code: string, message: string | null = null, requestId: string | null = 'req-9') =>
    new ApiError('http', status, code, false, requestId, message);
  it('maps the backend error codes and shows server text only for validation', () => {
    expect(describeError(http(400, 'VALIDATION_FAILED', 'relative_path is bad'))).toMatchObject({ message: ko.errors.codes.VALIDATION_FAILED, detail: 'relative_path is bad', requestId: 'req-9' });
    expect(describeError(http(422, 'SOURCE_UNSUPPORTED', '/srv/private/path'))).toMatchObject({ message: ko.errors.codes.SOURCE_UNSUPPORTED, detail: null });
    expect(describeError(http(400, 'ATTACHMENTS_UNSUPPORTED')).message).toBe(ko.errors.codes.ATTACHMENTS_UNSUPPORTED);
  });
  it('falls back to the status when the code is unknown, without echoing server text', () => {
    expect(describeError(http(403, 'X', 'secret')).message).toBe(ko.errors.forbidden);
    expect(describeError(http(409, 'X')).message).toBe(ko.errors.conflict);
    expect(describeError(http(503, 'X')).message).toBe(ko.errors.server);
    expect(describeError(http(418, 'X', 'text')).detail).toBeNull();
  });
  it('describes transport problems and non-API errors', () => {
    expect(describeError(new ApiError('network', 0, 'network', true)).message).toBe(ko.errors.network);
    expect(describeError(new ApiError('invalid_response', 200, 'invalid_response', false)).message).toBe(ko.errors.invalid_response);
    expect(describeError(new Error('boom')).message).toBe(ko.errors.unknown);
  });
});
