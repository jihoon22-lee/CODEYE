// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { CopyText, Time } from './CopyText';
import { formatInstant, formatRelative } from './format';
import { ko as t } from './locales/ko';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

async function click(name: string) {
  await act(async () => { fireEvent.click(screen.getByRole('button', { name })); });
}

describe('copy button', () => {
  it('copies the command and says so', async () => {
    const writeText = vi.fn(() => Promise.resolve());
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    render(<CopyText text="codeye up" />);
    await click(t.copy.labelFor('codeye up'));
    expect(writeText).toHaveBeenCalledWith('codeye up');
    expect(screen.getByRole('button').textContent).toBe(t.copy.done);
  });
  it('says it could not copy when there is no clipboard (plain HTTP)', async () => {
    vi.stubGlobal('navigator', {});
    render(<CopyText text="codeye open" />);
    await click(t.copy.labelFor('codeye open'));
    expect(screen.getByRole('button').textContent).toBe(t.copy.failedShort);
    expect(screen.getByRole('status').textContent).toBe(t.copy.failed);
    expect(screen.getByText('codeye open').tagName).toBe('CODE');
  });
  it('says it could not copy when the browser refuses', async () => {
    vi.stubGlobal('navigator', { clipboard: { writeText: () => Promise.reject(new Error('denied')) } });
    render(<CopyText text="codeye up" />);
    await click(t.copy.labelFor('codeye up'));
    expect(screen.getByRole('status').textContent).toBe(t.copy.failed);
  });
});

describe('relative time', () => {
  const now = new Date('2026-11-20T10:00:00Z');
  it('reads as minutes, hours and days and keeps the exact time', () => {
    expect(formatRelative('2026-11-20T09:59:40Z', now)).toBe(t.relative.now);
    expect(formatRelative('2026-11-20T10:00:20Z', now)).toBe(t.relative.now);
    expect(formatRelative('2026-11-20T09:57:00Z', now)).toBe(t.relative.minutes(3));
    expect(formatRelative('2026-11-20T07:00:00Z', now)).toBe(t.relative.hours(3));
    expect(formatRelative('2026-11-18T10:00:00Z', now)).toBe(t.relative.days(2));
    render(<Time iso="2026-11-20T09:57:00Z" now={now} />);
    const el = screen.getByText(t.relative.minutes(3));
    expect(el.tagName).toBe('TIME');
    expect(el.getAttribute('dateTime')).toBe('2026-11-20T09:57:00Z');
    expect(el.getAttribute('title')).toBe(formatInstant('2026-11-20T09:57:00Z'));
  });
  it('does not guess for a future or unreadable time', () => {
    expect(formatRelative('2026-11-20T10:05:00Z', now)).toBe(formatInstant('2026-11-20T10:05:00Z'));
    expect(formatRelative('not a time', now)).toBe('not a time');
  });
});
