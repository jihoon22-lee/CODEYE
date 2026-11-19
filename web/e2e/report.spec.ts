import { expect, test } from '@playwright/test';
import { readFileSync } from 'node:fs';

test('published report exposes the same counts, evidence and revision in the browser and exports', async ({page}) => {
  const login = process.env.CODEYE_TEST_REPORT_LOGIN;
  test.skip(!login, '실제 게시 보고서는 회귀 파일럿에서 생성한다');
  const project = process.env.CODEYE_TEST_REPORT_PROJECT!;
  const id = process.env.CODEYE_TEST_REPORT_ID!;
  await page.goto(readFileSync(login!, 'utf8').trim());
  await expect(page.getByRole('heading', {name:'프로젝트', exact:true})).toBeVisible();
  const entries = page.locator('.projects .item');
  await expect(entries.first()).toBeVisible();
  const target = entries.filter({has:page.getByText(project,{exact:true})});
  while (await target.count() === 0) {
    const previous = await entries.count();
    const more = page.getByRole('button',{name:'더 보기',exact:true});
    await expect(more).toBeVisible();
    await more.click();
    await expect.poll(() => entries.count()).toBeGreaterThan(previous);
  }
  await target.click();
  await page.getByRole('button', {name:'마지막 실행 상태 보기'}).click();
  await page.getByRole('button', {name:'보고서 열기'}).click();
  const report = page.getByRole('article', {name:'분석 보고서'});
  await expect(report).toBeVisible();
  const response = await page.request.get('/api/v1/runs/' + id + '/report?format=json&include_code=true');
  expect(response.ok()).toBe(true);
  const doc = await response.json();
  const rows = report.locator('.report-counts > div');
  await expect(rows.filter({hasText:'발견 사항'}).locator('dd')).toHaveText(String(doc.summary.total_findings));
  if (doc.overview) {
    // Analysis sections: collapsed ones are opened from their summary, then
    // every stored count must be on the screen.
    const sections = report.locator('details.report-section[id]');
    await expect(sections).toHaveCount(doc.overview.sections.length);
    for (let i = 0; i < await sections.count(); i++) {
      const section = sections.nth(i);
      if (!(await section.evaluate((d) => (d as HTMLDetailsElement).open))) await section.locator('summary').first().click();
    }
    for (const section of doc.overview.sections) {
      if (section.key === 'gaps') continue;
      const counts = report.locator('[data-section-counts="' + section.key + '"]');
      for (const [key, value] of Object.entries(section.counts as Record<string, number>)) {
        const cell = counts.locator('[data-count="' + key + '"] dd');
        await expect(cell).toBeVisible();
        await expect(cell).toHaveText(String(value));
      }
    }
  }
  if (doc.findings.length) {
    const item = doc.findings[0];
    await report.locator('details.finding-card summary').getByText(item.title, {exact:true}).first().click();
    await expect(report.getByText(item.impact, {exact:true}).first()).toBeVisible();
    const first = item.evidence.find((entry: { location?: unknown }) => entry.location);
    if (first) await expect(report.locator('[id]').filter({hasText:first.location.path}).first()).toBeVisible();
  }
  for (const format of ['json','markdown','html']) {
    const ref = await report.getByRole('link', {name:format.toUpperCase(),exact:true}).getAttribute('href');
    const target = new URL(ref!, page.url());
    expect(target.origin).toBe(new URL(page.url()).origin);
    expect(target.searchParams.get('revision')).toBe(String(doc.revision));
    expect(target.searchParams.get('include_code')).toBe('false');
    const exported = await page.request.get(target.href);
    expect(exported.status()).toBe(200);
    if (format === 'json') expect((await exported.json()).summary).toEqual(doc.summary);
  }
});
