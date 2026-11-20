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
  // Selecting the project opens its latest run and the report.
  await target.click();
  const report = page.getByRole('article', {name:'분석 보고서'});
  await expect(report).toBeVisible();
  const response = await page.request.get('/api/v1/runs/' + id + '/report?format=json&include_code=true');
  expect(response.ok()).toBe(true);
  const doc = await response.json();
  const rows = report.locator('.report-counts > div');
  await expect(rows.filter({hasText:'발견 사항'}).locator('dd')).toHaveText(String(doc.summary.total_findings));
  if (doc.overview) {
    for (const section of doc.overview.sections) {
      if (section.key === 'gaps') continue;
      await report.getByRole('tablist').locator('[aria-controls="report-section-' + section.key + '"]').click();
      const counts = report.locator('[data-section-counts="' + section.key + '"]');
      for (const [key, value] of Object.entries(section.counts as Record<string, number>)) {
        const cell = counts.locator('[data-count="' + key + '"] dd');
        await expect(cell).toBeVisible();
        await expect(cell).toHaveText(String(value));
      }
    }
  }
  if (doc.findings.length) {
    // The summary view keeps the full lists (static findings, the other
    // review findings) behind disclosures that render when opened.
    const key = doc.code_review.finding_ids.includes(doc.findings[0].id) ? 'code_review' : 'static_analysis';
    await report.getByRole('tablist').locator('[aria-controls="report-section-' + key + '"]').click();
    const more = report.locator('details.report-more:not([open]) > summary:visible');
    while (await more.count() > 0) await more.first().click();
    const item = doc.findings[0];
    const card = report.locator('details.finding-card').filter({has: page.locator('summary > strong').getByText(item.title, {exact:true})}).first();
    await card.locator('summary').first().click();
    await expect(card.getByText(item.impact, {exact:true}).first()).toBeVisible();
    // The claims and evidence sit behind the card's own disclosure.
    await card.locator('details.finding-evidence > summary').click();
    const first = item.evidence.find((entry: { location?: unknown }) => entry.location);
    if (first) await expect(card.locator('[id]').filter({hasText:first.location.path}).first()).toBeVisible();
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
