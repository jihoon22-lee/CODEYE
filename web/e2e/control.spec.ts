import { readFileSync } from 'node:fs';
import { test, expect } from '@playwright/test';

test('real session, project registration, durable request and cancellation', async ({ page }) => {
  const file = process.env.CODEYE_TEST_LOGIN_FILE;
  if (!file) throw new Error('run scripts/test_postgres.py --web to provide an isolated real server');
  const launch = readFileSync(file, 'utf8').trim();
  const secret = new URL(launch).hash.slice('#launch='.length);
  // The generated fixture token is never printed or saved in traces/screenshots.
  await page.goto(launch);
  await expect(page.getByRole('heading', { name: '프로젝트', exact: true })).toBeVisible();
  expect(new URL(page.url()).hash).toBe('');
  expect(await page.content()).not.toContain(secret);
  await page.reload();
  await expect(page.getByRole('heading', { name: '프로젝트', exact: true })).toBeVisible();
  // The start field leads; registering from a source root is folded under it.
  await page.getByText('허용된 폴더(소스 루트)나 Git 공급자에서 골라 등록', { exact: true }).click();
  await page.getByLabel('이름 (선택)', { exact: true }).fill('browser-fixture');
  await page.getByLabel('소스 루트', { exact: true }).selectOption({ label: 'browser-fixture-root' });
  await page.getByLabel('루트 아래 상대 경로').fill('.');
  await page.getByRole('button', { name: '프로젝트 등록', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'browser-fixture', exact: true })).toBeVisible();
  const enqueued = page.waitForResponse(r => r.request().method() === 'POST' && /\/projects\/[^/]+\/runs$/.test(new URL(r.url()).pathname));
  await page.getByRole('button', { name: '새로 분석', exact: true }).click();
  const response = await enqueued;
  expect(response.status()).toBe(202);
  const run = await response.json();
  expect(run.status).toBe('queued');
  await expect(page.getByText('분석 에이전트가 아직 연결되지 않아 시작하지 못했습니다.', { exact: true })).toBeVisible();
  // Chromium has EventSource, so the panel follows the run's event stream (same origin,
  // session cookie, CSP connect-src 'self') instead of polling.
  await expect(page.getByText(/^실시간으로 갱신하고 있습니다/)).toBeVisible();
  // Stages come from the same server aggregation for detail and list. A queued
  // run has not started source acquisition and has no plan yet.
  const source = page.getByRole('button', { name: '소스 확보 ◷ 대기', exact: true });
  await expect(source).toBeVisible();
  await expect(page.getByRole('button', { name: '정적 분석 ○ 계획 미확정', exact: true })).toBeVisible();
  await source.focus();
  await page.keyboard.press('Enter');
  await expect(source).toHaveAttribute('aria-expanded', 'true');
  await expect(page.getByText('시작 기록 없음', { exact: true }).first()).toBeVisible();
  await page.getByRole('button', { name: '취소 요청', exact: true }).click();
  await page.getByRole('button', { name: '취소 요청 보내기' }).click();
  await expect(page.getByText('취소됨', { exact: true })).toBeVisible();
  await page.reload();
  // Selecting the project opens its latest run.
  await page.getByRole('button', { name: /browser-fixture/ }).click();
  await expect(page.getByText('취소됨', { exact: true })).toBeVisible();
  // After a reload the cancelled run keeps its recorded stages: source never completed.
  await expect(page.getByRole('button', { name: '소스 확보 ■ 취소됨', exact: true })).toBeVisible();
  // The owner manages members and reads the project's audit log from the real server.
  const tabs = page.getByRole('navigation', { name: '프로젝트 화면' });
  await tabs.getByRole('button', { name: '구성원', exact: true }).click();
  const members = page.getByRole('table', { name: '구성원 목록' });
  await expect(members).toContainText('Browser fixture');
  await expect(members).toContainText('🔒 소유자는 바꾸거나 제거할 수 없습니다.');
  await tabs.getByRole('button', { name: '감사 기록', exact: true }).click();
  const audit = page.getByRole('table', { name: '감사 기록(최신순)' });
  await expect(audit).toContainText('프로젝트 등록');
  await expect(audit).toContainText('취소 요청');
  await page.getByLabel('작업').selectOption('project.registered');
  await page.getByRole('button', { name: '적용', exact: true }).click();
  await expect(audit).not.toContainText('취소 요청');
  await expect(audit).toContainText('프로젝트 등록');
  await page.getByRole('button', { name: '로그아웃', exact: true }).click();
  await expect(page.getByRole('heading', { name: '로그인이 필요합니다' })).toBeVisible();
  expect((await page.request.get(`/api/v1/runs/${run.id}`)).status()).toBe(401);
});
