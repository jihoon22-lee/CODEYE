import { expect, test, type Page } from '@playwright/test';
import { readFileSync } from 'node:fs';

// The service `codeye up` starts, in the browser (scripts/instance_smoke.py): one field to a folder's and a
// repository's report, the improvement hints, a failed run retried, and the tools panel.
// One sign-in (the link works once) is shared by the steps, in order.
const env = process.env;
test.describe.configure({ mode: 'serial' });
test.skip(!env.CODEYE_TEST_START_LINK_FILE, 'codeye up 설치의 로그인 링크로 실행한다');

let page: Page;
test.beforeAll(async ({ browser }) => {
  page = await browser.newPage();
  await page.goto(readFileSync(env.CODEYE_TEST_START_LINK_FILE!, 'utf8').trim());
  await expect(page.getByRole('button', { name: '로그아웃', exact: true })).toBeVisible();
});
test.afterAll(async () => { await page?.close(); });

// The project opens under the name of the folder or repository (its last path segment).
async function analyze(target: string) {
  await page.getByRole('button', { name: '프로젝트·분석', exact: true }).click();
  await page.getByLabel('폴더 경로 또는 Git 주소', { exact: true }).fill(target);
  await page.getByRole('button', { name: '분석', exact: true }).click();
  const name = target.replace(/\/+$/, '').split('/').pop()!;
  await expect(page.getByRole('heading', { level: 2, name, exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: '현재 실행', exact: true })).toBeVisible();
}

test('the tools panel lists what was found and how to add the rest', async () => {
  await page.getByRole('button', { name: '도구', exact: true }).click();
  const table = page.getByRole('table');
  await expect(table.getByRole('row').filter({ hasText: /^go/ }).first()).toContainText('찾음');
  await expect(page.getByText('서버 폴더', { exact: true })).toBeVisible();
  await expect(page.getByText(/연결할 수 있는 호스트: github\.com/)).toBeVisible();
});

test('a folder path goes straight to its run, and the report opens with what would make it better', async () => {
  // Allow the report wait plus navigation and the hint check.
  test.setTimeout(180_000);
  await analyze(env.CODEYE_TEST_START_FOLDER!);
  await expect(page.getByRole('article', { name: '분석 보고서' })).toBeVisible({ timeout: 150_000 });
  // No model key in the smoke: the first hint says how to set one.
  const hints = page.getByRole('region', { name: '더 나은 결과를 얻으려면' });
  await expect(hints).toContainText('모델 키를 설정하세요');
});

test('a GitHub address is analyzed the same way; turning the build scripts on retries it', async () => {
  // Two analyses end to end (the waits below allow 150 s each).
  test.setTimeout(330_000);
  await analyze(env.CODEYE_TEST_START_REPO!);
  await expect(page.getByRole('article', { name: '분석 보고서' })).toBeVisible({ timeout: 150_000 });
  if (!env.CODEYE_TEST_START_BUILD_HINT) return;
  const hints = page.getByRole('region', { name: '더 나은 결과를 얻으려면' });
  await expect(hints).toContainText('저장소의 빌드 스크립트 실행');
  await hints.getByRole('button', { name: '켜고 다시 시도', exact: true }).click();
  await expect(page.getByText('소스를 다시 받아 새 실행을 시작했습니다.', { exact: true })).toBeVisible();
  await expect(page.getByRole('article', { name: '분석 보고서' })).toBeVisible({ timeout: 150_000 });
  await expect(page.getByRole('region', { name: '더 나은 결과를 얻으려면' })).not.toContainText('저장소의 빌드 스크립트 실행');
});

test('a run that could not read its source says why and can be retried', async () => {
  test.setTimeout(240_000);
  await analyze(env.CODEYE_TEST_START_MISSING_REPO!);
  await expect(page.getByText('실패', { exact: true }).first()).toBeVisible({ timeout: 90_000 });
  const retry = page.getByRole('button', { name: '다시 시도', exact: true });
  await retry.click();
  await expect(page.getByText(/같은 입력으로 다시 실행합니다\.|소스를 다시 받아 새 실행을 시작했습니다\./)).toBeVisible();
  await expect(page.getByRole('button', { name: '다시 시도', exact: true })).toBeVisible({ timeout: 90_000 });
});
