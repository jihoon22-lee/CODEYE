import { expect, test, type Browser, type Page } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

// One service, two people (scripts/web_service_smoke.py starts `codeye up` for other machines and
// writes the administrator's sign-in link to a 0600 file). The administrator invites a second
// user; the second user gets the same start screen, is told that no folder is allowed yet,
// connects a GitHub token of their own; the administrator opens a folder to everyone, approves a
// Git host and turns a switch on; the second user then analyzes the folder and is refused a
// private pull request their token does not see.
const env = process.env;
const linkFile = env.CODEYE_TEST_SERVICE_LINK_FILE;
const full = env.CODEYE_TEST_SERVICE_FLOW === 'full';
const tls = env.CODEYE_TEST_SERVICE_TLS === '1';
const shots = env.CODEYE_TEST_SCREENSHOTS;
const variant = tls ? 'tls' : 'plain';
test.use({ actionTimeout: 15_000 });
const NO_FOLDER = '이 서버에서 읽도록 허용된 폴더가 없습니다. Git 주소를 쓰거나 관리자에게 폴더 허용을 요청하세요.';

async function shot(page: Page, name: string) {
  if (shots) await page.screenshot({ path: join(shots, `${variant}-${name}.png`), fullPage: true });
}

async function adminTab(page: Page, name: string) {
  await page.getByRole('navigation', { name: '관리 화면' }).getByRole('button', { name, exact: true }).click();
}

async function invite(admin: Page, browser: Browser, username: string) {
  await admin.getByRole('button', { name: '관리', exact: true }).click();
  await adminTab(admin, '사용자');
  await admin.getByLabel('사용자 이름', { exact: true }).fill(username);
  await admin.getByRole('button', { name: '사용자 만들기', exact: true }).click();
  const box = admin.getByRole('region', { name: `${username}의 일회용 로그인 링크` });
  const link = await box.getByLabel('로그인 링크').inputValue();
  // The link is on the address other people open, not on this machine's loopback.
  expect(link.startsWith(`${env.CODEYE_TEST_BASE_URL}/#launch=`)).toBe(true);
  await box.getByRole('button', { name: '전달했습니다 · 닫기' }).click();
  const context = await browser.newContext({ baseURL: env.CODEYE_TEST_BASE_URL, ignoreHTTPSErrors: tls });
  const page = await context.newPage();
  await page.goto(link);
  await expect(page.getByRole('heading', { name: '비밀번호를 정하세요' })).toBeVisible();
  const chosen = `${username}-service-password-42`;
  await page.getByLabel('새 비밀번호', { exact: true }).fill(chosen);
  await page.getByLabel('새 비밀번호 확인').fill(chosen);
  await page.getByRole('button', { name: '비밀번호 변경', exact: true }).click();
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

  const { context: bobContext, page: bob } = await invite(admin, browser, 'bob');
  try {
    // The same start screen; no folder is allowed to bob yet.
    await expect(bob.locator('#start-target')).toBeVisible();
    await expect(bob.locator('#start-unavailable [data-unavailable="local_path"]')).toBeVisible();
    await expect(bob.locator('#start-unavailable [data-unavailable="build_scripts"]')).toContainText('관리자가 다른 사용자에게 허용하지 않았습니다');
    await shot(bob, 'second-user-start');
    await bob.locator('#start-target').fill(env.CODEYE_TEST_SERVICE_ROOT!);
    await bob.locator('#start-submit').click();
    const refusal = bob.locator('#start-folder-error');
    await expect(refusal).toContainText(NO_FOLDER);
    await expect(refusal).toContainText('내가 읽을 수 있는 폴더: 없음');
    await shot(bob, 'no-folder');
    if (!full) return;

    // bob's own GitHub token: stored, never shown, removable.
    const token = readFileSync(env.CODEYE_TEST_SERVICE_BOB_TOKEN_FILE!, 'utf8').trim();
    await bob.locator('#menu-github').click();
    await expect(bob.locator('#github-status')).toHaveAttribute('data-connected', 'false');
    await bob.locator('#github-token').fill(token);
    await bob.locator('#github-save').click();
    await expect(bob.locator('#github-status')).toHaveAttribute('data-connected', 'true');
    await expect(bob.locator('#github-status')).toContainText(env.CODEYE_TEST_SERVICE_BOB_LOGIN!);
    await expect(bob.locator('#github-token')).toHaveValue('');
    expect(await bob.content()).not.toContain(token);
    await shot(bob, 'account-github');
    await bob.locator('#github-remove').click();
    await expect(bob.locator('#github-status')).toHaveAttribute('data-connected', 'false');
    await bob.locator('#github-token').fill(token);
    await bob.locator('#github-save').click();
    await expect(bob.locator('#github-status')).toHaveAttribute('data-connected', 'true');

    // The administrator: a folder for everyone, a Git host, the build-script switch.
    await adminTab(admin, '폴더');
    await admin.locator('#add-root-path').fill(env.CODEYE_TEST_SERVICE_ROOT!);
    await admin.locator('#add-root-label').fill('shared');
    await admin.locator('#add-root-everyone').check();
    await admin.locator('#add-root-submit').click();
    await expect(admin.locator('li[data-root="shared"] input[data-root-everyone="shared"]')).toBeChecked();
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

    // bob again: the folder is his to read now and the switch is on.
    await bob.getByRole('navigation', { name: '주 메뉴' }).getByRole('button', { name: '프로젝트·분석', exact: true }).click();
    await bob.reload();
    await expect(bob.locator('#start-target')).toBeVisible();
    await expect(bob.locator('#start-unavailable [data-unavailable="build_scripts"]')).toHaveCount(0);
    await expect(bob.locator('#start-unavailable [data-unavailable="local_path"]')).toHaveCount(0);
    await bob.locator('#start-target').fill(env.CODEYE_TEST_SERVICE_OUTSIDE!);
    await bob.locator('#start-submit').click();
    await expect(bob.locator('#start-folder-error')).toContainText(env.CODEYE_TEST_SERVICE_ROOT!);
    await shot(bob, 'outside-folder');
    await bob.locator('#start-target').fill(env.CODEYE_TEST_SERVICE_ROOT!);
    await bob.locator('#start-submit').click();
    await expect(bob.getByRole('heading', { name: '현재 실행', exact: true })).toBeVisible();
    await expect(bob.getByRole('article', { name: '분석 보고서' })).toBeVisible({ timeout: 240_000 });
    await shot(bob, 'second-user-report');

    // A private pull request bob's own token does not see: refused, never read with the
    // administrator's token (the smoke checks the fake GitHub's record afterwards).
    await bob.getByRole('navigation', { name: '주 메뉴' }).getByRole('button', { name: '프로젝트·분석', exact: true }).click();
    await bob.locator('#start-target').fill(env.CODEYE_TEST_SERVICE_PRIVATE_PR!);
    await bob.locator('#start-submit').click();
    await expect(bob.getByText('GitHub이 그 pull request를 내 연결에 보여 주지 않습니다.', { exact: false })).toBeVisible();
    expect(await bob.content()).not.toContain(token);
  } finally {
    await bobContext.close();
    await adminContext.close();
  }
});
