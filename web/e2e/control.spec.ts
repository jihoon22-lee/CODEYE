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
  await page.getByLabel('이름 (선택)').fill('browser-fixture');
  await page.getByLabel('소스 루트', { exact: true }).selectOption({ label: 'browser-fixture-root' });
  await page.getByLabel('루트 아래 상대 경로').fill('.');
  await page.getByRole('button', { name: '프로젝트 등록', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'browser-fixture', exact: true })).toBeVisible();
  const enqueued = page.waitForResponse(r => r.request().method() === 'POST' && /\/projects\/[^/]+\/runs$/.test(new URL(r.url()).pathname));
  await page.getByRole('button', { name: '분석 요청 등록' }).click();
  const response = await enqueued;
  expect(response.status()).toBe(202);
  const run = await response.json();
  expect(run.status).toBe('queued');
  await expect(page.getByText('실행기가 아직 연결되지 않아 시작되지 않았습니다.', { exact: true })).toBeVisible();
  // W15: stages come from the same server aggregation for detail and list. A queued
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
  await page.getByRole('button', { name: /browser-fixture/ }).click();
  await page.getByRole('button', { name: '마지막 실행 상태 보기' }).click();
  await expect(page.getByText('취소됨', { exact: true })).toBeVisible();
  // After a reload the cancelled run keeps its recorded stages: source never completed.
  await expect(page.getByRole('button', { name: '소스 확보 ■ 취소됨', exact: true })).toBeVisible();
  await page.getByRole('button', { name: '로그아웃', exact: true }).click();
  await expect(page.getByRole('heading', { name: '로그인이 필요합니다' })).toBeVisible();
  expect((await page.request.get(`/api/v1/runs/${run.id}`)).status()).toBe(401);
});
