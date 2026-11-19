import { readFileSync } from 'node:fs';
import { test, expect } from '@playwright/test';

// A real server with an administrator that has a password (scripts/browser_smoke.py). The
// administrator signs in with the form, creates a user, and the new user opens the one-time
// link in a separate browser, is held on the password screen, sets a password and signs in again.
test('password sign-in, admin-created user, forced password change', async ({ page, browser }) => {
  const username = process.env.CODEYE_TEST_ADMIN_USERNAME;
  const file = process.env.CODEYE_TEST_ADMIN_PASSWORD_FILE;
  if (!username || !file) throw new Error('run scripts/test_postgres.py --web to provide an isolated real server');
  const password = readFileSync(file, 'utf8').split('\n')[0]!;

  await page.goto('/');
  await expect(page.getByRole('heading', { name: '로그인이 필요합니다' })).toBeVisible();
  // A wrong password gets the one generic answer and an empty field.
  await page.getByLabel('사용자 이름').fill(username);
  await page.getByLabel('비밀번호', { exact: true }).fill(`${password}-wrong`);
  await page.getByRole('button', { name: '로그인', exact: true }).click();
  await expect(page.getByText('로그인하지 못했습니다. 사용자 이름과 비밀번호를 확인하세요.')).toBeVisible();
  await expect(page.getByLabel('비밀번호', { exact: true })).toHaveValue('');
  await page.getByLabel('비밀번호', { exact: true }).fill(password);
  await page.getByRole('button', { name: '로그인', exact: true }).click();
  await expect(page.getByRole('heading', { name: '프로젝트', exact: true })).toBeVisible();
  const account = page.getByRole('group', { name: '내 계정' });
  await expect(account).toContainText(username);
  await expect(account).toContainText('★ 관리자');
  expect(await page.content()).not.toContain(password);

  // Admin: create a user; the one-time link is shown once, as text.
  await page.getByRole('button', { name: '관리', exact: true }).click();
  await expect(page.getByText(/관리자 권한은 프로젝트를 열거나 볼 권한을 주지 않습니다/)).toBeVisible();
  await page.getByLabel('사용자 이름', { exact: true }).fill('browser-newcomer');
  await page.getByRole('button', { name: '사용자 만들기', exact: true }).click();
  const box = page.getByRole('region', { name: 'browser-newcomer의 일회용 로그인 링크' });
  await expect(box).toBeVisible();
  const link = await box.getByLabel('로그인 링크').inputValue();
  expect(new URL(link).hash).toMatch(/^#launch=/);
  await box.getByRole('button', { name: '전달했습니다 · 닫기' }).click();
  await expect(box).toHaveCount(0);
  expect(await page.content()).not.toContain(new URL(link).hash.slice('#launch='.length));

  // The new user, in another browser: the link signs in, and only the password screen is offered.
  const other = await browser.newContext({ baseURL: test.info().project.use.baseURL });
  try {
    const newcomer = await other.newPage();
    await newcomer.goto(link);
    await expect(newcomer.getByRole('heading', { name: '비밀번호를 정하세요' })).toBeVisible();
    expect(new URL(newcomer.url()).hash).toBe('');
    await expect(newcomer.getByRole('navigation', { name: '주 메뉴' })).toHaveCount(0);
    expect((await newcomer.request.get('/api/v1/projects')).status()).toBe(403);
    const chosen = 'newcomer-browser-password-42';
    await newcomer.getByLabel('새 비밀번호', { exact: true }).fill(chosen);
    await newcomer.getByLabel('새 비밀번호 확인').fill(chosen);
    await newcomer.getByRole('button', { name: '비밀번호 변경', exact: true }).click();
    await expect(newcomer.getByRole('heading', { name: '프로젝트', exact: true })).toBeVisible();
    await newcomer.getByRole('button', { name: '로그아웃', exact: true }).click();
    await expect(newcomer.getByRole('heading', { name: '로그인이 필요합니다' })).toBeVisible();
    await newcomer.getByLabel('사용자 이름').fill('browser-newcomer');
    await newcomer.getByLabel('비밀번호', { exact: true }).fill(chosen);
    await newcomer.getByRole('button', { name: '로그인', exact: true }).click();
    await expect(newcomer.getByRole('heading', { name: '프로젝트', exact: true })).toBeVisible();
  } finally {
    await other.close();
  }

  // Back in the admin's browser, the new user no longer waits for a password.
  await page.reload();
  await page.getByRole('button', { name: '관리', exact: true }).click();
  const row = page.getByRole('row').filter({ hasText: 'browser-newcomer' });
  await expect(row).toContainText('✓ 활성');
  await expect(row).not.toContainText('비밀번호 설정 대기');
  await page.getByRole('button', { name: '로그아웃', exact: true }).click();
  await expect(page.getByRole('heading', { name: '로그인이 필요합니다' })).toBeVisible();
});
