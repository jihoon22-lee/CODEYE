import { expect, test } from '@playwright/test';
import { readFileSync } from 'node:fs';

// The link printed by `codeye up PATH` (scripts/instance_smoke.py writes it to a 0600 file):
// one visit signs in, removes the token from the address bar, opens the project and its
// queued run, and the run ends with a published report.
test('the codeye up link signs in and opens the project run with its report', async ({ page }) => {
  const file = process.env.CODEYE_TEST_UP_LINK_FILE;
  test.skip(!file, 'codeye up이 출력한 링크로 실행한다');
  const project = process.env.CODEYE_TEST_UP_PROJECT!;
  await page.goto(readFileSync(file!, 'utf8').trim());
  await expect(page.getByRole('button', { name: '로그아웃', exact: true })).toBeVisible();
  expect(page.url()).not.toContain('launch=');
  expect(new URL(page.url()).hash).toContain('project=');
  await expect(page.getByRole('heading', { level: 2, name: project, exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: '현재 실행', exact: true })).toBeVisible();
  // The published report opens by itself.
  await expect(page.getByRole('article', { name: '분석 보고서' })).toBeVisible({ timeout: 25_000 });
  await page.getByRole('button', { name: '관리', exact: true }).click();
  await page.getByRole('button', { name: '언어 도구 경로', exact: true }).click();
  await page.getByLabel('JDK', { exact: true }).fill('/nonexistent/codeye-missing-jdk');
  await page.getByRole('button', { name: '경로 저장' }).click();
  await expect(page.getByRole('alert')).toContainText('JDK');
});
