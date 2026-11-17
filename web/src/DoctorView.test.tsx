// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { DoctorView } from './DoctorView';
import example from '../../examples/doctor.json';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
function upload(text: () => Promise<string>) {
  fireEvent.change(screen.getByLabelText('진단 JSON 파일 열기'), { target: { files: [{ size: 100, text }] } });
}
it('reads diagnostics locally and clears stale results when the next file is invalid', async () => {
  const fetch = vi.fn(); vi.stubGlobal('fetch', fetch);
  render(<DoctorView />);
  upload(async () => JSON.stringify(example));
  expect(await screen.findByRole('table')).toBeTruthy();
  expect(screen.getByText('미설정')).toBeTruthy();
  upload(async () => '{}');
  expect(await screen.findByRole('alert')).toBeTruthy();
  expect(screen.queryByRole('table')).toBeNull();
  expect(fetch).not.toHaveBeenCalled();
});
it('keeps the newest import when an older file finishes later', async () => {
  render(<DoctorView />);
  let finish!: (text: string) => void;
  upload(() => new Promise(resolve => { finish = resolve; }));
  upload(async () => '{}');
  await screen.findByRole('alert');
  await act(async () => finish(JSON.stringify(example)));
  expect(screen.queryByRole('table')).toBeNull();
  expect(screen.getByRole('alert')).toBeTruthy();
});
