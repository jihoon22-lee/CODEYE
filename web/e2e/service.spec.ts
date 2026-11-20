import { expect, test, type Browser, type Page } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

// Two web accounts use the same installation and its platform source access.
const env = process.env;
const linkFile = env.CODEYE_TEST_SERVICE_LINK_FILE;
const full = env.CODEYE_TEST_SERVICE_FLOW === 'full';
const tls = env.CODEYE_TEST_SERVICE_TLS === '1';
const shots = env.CODEYE_TEST_SCREENSHOTS;
const variant = tls ? 'tls' : 'plain';
test.use({ actionTimeout: 15_000 });

async function shot(page: Page, name: string) {
  if (shots) await page.screenshot({ path: join(shots, `${variant}-${name}.png`), fullPage: true });
}

async function adminTab(page: Page, name: string) {
  await page.getByRole('navigation', { name: '관리 화면' }).getByRole('button', { name, exact: true }).click();
}

async function register(browser: Browser) {
  const context = await browser.newContext({ baseURL: env.CODEYE_TEST_BASE_URL, ignoreHTTPSErrors: tls });
  const page = await context.newPage();
  await page.goto('/');
  await page.getByRole('button', { name: '회원가입', exact: true }).click();
  await page.getByLabel('이메일', { exact: true }).fill('bob@example.test');
  await page.getByLabel('비밀번호', { exact: true }).fill('bob-service-password-42');
  await page.getByRole('button', { name: '가입하기', exact: true }).click();
  await expect(page.getByRole('heading', { name: '프로젝트', exact: true })).toBeVisible();
  return { context, page };
}

test('two people on one codeye up service', async ({ browser }) => {
  test.skip(!linkFile, 'scripts/web_service_smoke.py가 띄운 서비스로 실행한다');
  test.setTimeout(420_000);
  const adminContext = await browser.newContext({ baseURL: env.CODEYE_TEST_BASE_URL, ignoreHTTPSErrors: tls });
  const admin = await adminContext.newPage();
  await admin.goto(readFileSync(linkFile!, 'utf8').trim());
  await expect(admin.getByRole('button', { name: '로그아웃', exact: true })).toBeVisible();
  expect(admin.url()).not.toContain('launch=');
  if (tls) {
    const cookies = await adminContext.cookies();
    expect(cookies.some(c => c.name === '__Host-codeye_session' && c.secure && c.httpOnly)).toBe(true);
  }

  // The address to share is the public origin.
  await admin.getByRole('button', { name: '관리', exact: true }).click();
  await expect(admin.locator('#service-address-value')).toContainText(env.CODEYE_TEST_BASE_URL!);

  const { context: bobContext, page: bob } = await register(browser);
  try {
    await expect(bob.locator('#start-target')).toBeVisible();
    await expect(bob.locator('#start-unavailable [data-unavailable="build_scripts"]')).toContainText('관리자가 다른 사용자에게 허용하지 않았습니다');
    await shot(bob, 'second-user-start');
    await bob.locator('#start-target').fill(env.CODEYE_TEST_SERVICE_ROOT!);
    await bob.locator('#start-submit').click();
    await expect(bob.getByRole('heading', { name: '현재 실행', exact: true })).toBeVisible();
    if (!full) return;
    await expect(bob.getByRole('article', { name: '분석 보고서' })).toBeVisible({ timeout: 240_000 });
    await shot(bob, 'second-user-report');

    await adminTab(admin, 'Git 공급자');
    await admin.locator('#add-provider-host').fill('ghe.example.com');
    await admin.locator('#add-provider-submit').click();
    await expect(admin.getByText('ghe.example.com').first()).toBeVisible();
    await adminTab(admin, '서비스');
    await expect(admin.locator('#switch-others-model-review')).toBeChecked();
    await expect(admin.locator('#switch-others-build-scripts')).not.toBeChecked();
    // A controlled box: it turns on once the server accepted the change.
    await admin.locator('#switch-others-build-scripts').click();
    await expect(admin.getByRole('status')).toBeVisible();
    await expect(admin.locator('#switch-others-build-scripts')).toBeChecked();
    await shot(admin, 'admin-settings');

    await bob.reload();
    await expect(bob.locator('#start-unavailable [data-unavailable="build_scripts"]')).toHaveCount(0);
    await bob.locator('#start-target').fill(env.CODEYE_TEST_SERVICE_STATE!);
    await bob.locator('#start-submit').click();
    await expect(bob.getByRole('alert')).toContainText('CODEYE');
    await bob.getByRole('button', { name: '도구', exact: true }).click();
    await expect(bob.getByText('계정 codeye-platform')).toBeVisible();
    await expect(bob.locator('#menu-github')).toHaveCount(0);

    await bob.getByRole('navigation', { name: '주 메뉴' }).getByRole('button', { name: '프로젝트·분석', exact: true }).click();
    await bob.locator('#start-target').fill(env.CODEYE_TEST_SERVICE_PRIVATE_PR!);
    const enqueued = bob.waitForResponse(r => r.request().method() === 'POST' && /\/projects\/[^/]+\/runs$/.test(new URL(r.url()).pathname));
    await bob.locator('#start-submit').click();
    expect((await enqueued).status()).toBe(202);
    await expect(bob.getByRole('article', { name: '분석 보고서' })).toBeVisible({ timeout: 240_000 });
  } finally {
    await bobContext.close();
    await adminContext.close();
  }
});
