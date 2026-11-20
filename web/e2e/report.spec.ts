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
  const stats = report.locator('details.report-more > summary').filter({hasText: /^분석 상태와 통계$/});
  await expect(report.locator('.report-summary')).toHaveCount(0);
  const hero = report.locator('.report-hero');
  await expect(hero.getByRole('heading', { name: project, exact: true })).toBeVisible();
  await expect(hero.getByText(doc.project.source, {exact: true})).toBeVisible();
  const status = {completed: '분석 완료', partial: '분석 일부 완료', failed: '분석 실패', cancelled: '분석 취소됨'}[doc.status as string];
  await expect(hero.getByText(status!, {exact: true})).toBeVisible();
  if (doc.project.finished_at) await expect(hero.locator('time')).toHaveAttribute('datetime', doc.project.finished_at);
  const judgment = report.locator('.report-executive');
  await expect(judgment).toHaveCount(1);
  await expect(judgment.locator('table, .model-reading-list')).toHaveCount(0);
  await expect(judgment.getByText('적용 범위', {exact:true})).toHaveCount(0);
  expect(await hero.locator('.report-verdict .icon').evaluate(icon => getComputedStyle(icon).maskImage)).not.toBe('none');
  expect(await hero.evaluate(async () => {
    await document.fonts.ready;
    return [...document.fonts].some(font => font.family.replaceAll('"', '') === 'CODEYE Noto Sans KR' && font.status === 'loaded');
  })).toBe(true);
  await page.setViewportSize({ width: 390, height: 844 });
  const overview = report.getByRole('tab', { name: '종합', exact: true });
  await expect(overview).toBeVisible();
  await expect.poll(() => report.evaluate(element => element.scrollWidth - element.clientWidth)).toBeLessThanOrEqual(1);
  const glossary = judgment.locator('details.report-glossary');
  if (await glossary.count()) {
    await expect(glossary).toHaveCount(1);
    await expect(glossary.locator('dl')).toBeHidden();
    await glossary.locator('summary').press('Enter');
    await expect(glossary.locator('dl')).toBeVisible();
    await expect.poll(() => report.evaluate(element => element.scrollWidth - element.clientWidth)).toBeLessThanOrEqual(1);
    await glossary.locator('summary').press('Space');
    await expect(glossary.locator('dl')).toBeHidden();
  }
  const rail = report.locator('.report-rail');
  await expect.poll(async () => (await hero.boundingBox())!.y < (await rail.boundingBox())!.y).toBe(true);
  await overview.press('ArrowDown');
  const next = report.locator('[aria-controls="report-section-environment"]');
  await expect(next).toBeFocused();
  await expect(next).toHaveAttribute('aria-selected', 'true');
  await next.press('Home');
  await expect(overview).toBeFocused();
  await page.setViewportSize({ width: 1280, height: 960 });
  await expect.poll(async () => (await rail.boundingBox())!.x < (await hero.boundingBox())!.x).toBe(true);
  await expect.poll(() => report.evaluate(element => element.scrollWidth - element.clientWidth)).toBeLessThanOrEqual(1);
  await stats.click();
  const rows = report.getByRole('tabpanel', {name: '종합', exact: true}).locator('.report-counts > div');
  await expect(rows.filter({hasText:'발견 사항'}).locator('dd')).toHaveText(String(doc.summary.total_findings));
  if (doc.overview) {
    const tabOrder = ['overview', 'environment', 'documents', 'static_analysis', 'metrics', 'secrets', 'code_review', 'gaps'];
    const tabs = report.getByRole('tablist').getByRole('tab');
    expect(await tabs.evaluateAll(elements => elements.map(element => element.getAttribute('aria-controls')))).toEqual(tabOrder.map(key => 'report-section-' + key));
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
    const card = report.locator('#report-section-' + key).locator('details.finding-card').filter({has: page.locator('summary > strong').getByText(item.title, {exact:true})}).first();
    await card.locator('summary').first().click();
    await expect(card.getByText(item.impact, {exact:true}).first()).toBeVisible();
    // The claims and evidence sit behind the card's own disclosure.
    await card.locator('details.finding-evidence > summary').click();
    const first = item.evidence.find((entry: { location?: unknown }) => entry.location);
    if (first) {
      const evidence = card.locator('.finding-evidence li[id]').filter({hasText:first.location.path}).first();
      await expect(evidence).toBeVisible();
      const excerpt = evidence.locator('.report-excerpt summary');
      if (await excerpt.count()) {
        await expect(excerpt).toHaveCount(1);
        await expect(evidence.locator('pre')).toBeHidden();
        await excerpt.press('Enter');
        await expect(evidence.locator('pre')).toBeVisible();
      }
    }
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
  await report.getByText('화면 자산 라이선스', {exact:true}).click();
  for (const name of ['Tabler Core (MIT)', 'Tabler Icons (MIT)', 'Bootstrap (MIT)', 'Noto Sans KR (OFL)']) {
    const asset = await report.getByRole('link', {name, exact:true}).getAttribute('href');
    const local = new URL(asset!, page.url());
    expect(local.origin).toBe(new URL(page.url()).origin);
    const license = await page.request.get(local.href);
    expect(license.ok()).toBe(true);
    expect(await license.text()).toContain(name.endsWith('(MIT)') ? 'MIT License' : 'SIL OPEN FONT LICENSE');
  }
});
