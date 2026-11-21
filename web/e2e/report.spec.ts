import { expect, test } from '@playwright/test';
import { createHash } from 'node:crypto';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';
import { pathToFileURL } from 'node:url';

test('saved report navigation follows scroll and fragment links under CSP', async ({page}, info) => {
  const assets = new URL('../../internal/report/reportui/', import.meta.url);
  const css = ['tabler.css', 'report.css', 'export.css'].map(name => readFileSync(new URL(name, assets), 'utf8')).join('\n');
  const script = readFileSync(new URL('export-navigation.js', assets), 'utf8');
  const hash = createHash('sha256').update(script).digest('base64');
  const sections = { overview: '종합', documents: '문서 분석', code_review: '코드 분석', appendix: '부록' };
  const links = Object.entries(sections).map(([key, name]) => `<li><a href="#${key}" class="nav-link">${name}</a></li>`).join('');
  const file = info.outputPath('report.html');
  mkdirSync(dirname(file), { recursive: true });
  writeFileSync(file, `<!doctype html><html lang="ko"><meta name="viewport" content="width=device-width, initial-scale=1">
    <meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'sha256-${hash}'">
    <style>${css}.export-section { min-height: 82vh; }</style>
    <body class="review-theme report-export"><div class="report-layout export-page">
    <nav class="report-rail">
      <div class="export-desktop-nav"><ul class="nav">${links}</ul></div>
      <details class="export-mobile-nav"><summary>보고서 목차 <span class="export-mobile-current"></span><span>⌄</span></summary><ul class="nav">${links}</ul></details>
    </nav><main class="report-main">
    ${Object.entries(sections).map(([key, name]) => `<section class="export-section" id="${key}"><h2>${name}</h2>
      ${key === 'documents' ? '<details><summary>근거</summary><p id="document-citation">문서 근거</p></details>' : ''}</section>`).join('')}
    </main></div><script>document.body.classList.add('unexpected-script')</script><script>${script}</script></body></html>`);

  await page.setViewportSize({width: 1280, height: 720});
  const url = pathToFileURL(file).href;
  await page.goto(url);
  const current = page.locator('.export-desktop-nav [aria-current="location"]');
  await expect(current).toHaveText(sections.overview);
  expect(await page.locator('body').evaluate(body => body.classList.contains('unexpected-script'))).toBe(false);
  await page.locator('#documents').evaluate(section => section.scrollIntoView());
  await expect(current).toHaveText(sections.documents);
  await page.locator('#code_review').evaluate(section => section.scrollIntoView());
  await expect(current).toHaveText(sections.code_review);
  await page.locator('#overview').evaluate(section => section.scrollIntoView());
  await expect(current).toHaveText(sections.overview);
  await page.locator('.export-desktop-nav').getByRole('link', {name: sections.appendix}).click();
  await expect(current).toHaveText(sections.appendix);
  await expect(page).toHaveURL(url + '#appendix');

  await page.setViewportSize({width: 390, height: 720});
  await page.goto(url + '#document-citation');
  await expect(page.locator('#document-citation')).toBeVisible();
  await expect(page.locator('.export-mobile-current')).toHaveText(sections.documents);
  await page.locator('#code_review').evaluate(section => section.scrollIntoView());
  await expect(page.locator('.export-mobile-current')).toHaveText(sections.code_review);
  await expect(page.locator('.export-mobile-nav [aria-current="location"]')).toHaveText(sections.code_review);
});

test('long saved source scrolls inside the HTML finding card at mobile and desktop widths', async ({page}) => {
  const assets = new URL('../../internal/report/reportui/', import.meta.url);
  const css = ['tabler.css', 'report.css', 'export.css'].map(name => readFileSync(new URL(name, assets), 'utf8')).join('\n');
  const tail = 'END_OF_SAVED_SOURCE';
  await page.setContent(`<!DOCTYPE html><html lang="ko"><head><meta name="viewport" content="width=device-width, initial-scale=1"><style>${css}</style></head>
    <body class="review-theme report-export"><div class="report-layout export-page"><main class="report-main">
      <section class="export-section"><div class="report-cards"><article class="card report-card"><div class="card-body">
        <h3>코드 검토 결과</h3><ul class="export-card-points"><li>근거
          <div class="report-evidence"><details class="report-excerpt">
            <summary class="btn btn-sm btn-ghost-primary export-evidence-control">코드 보기</summary>
            <pre tabindex="0" aria-label="저장된 발췌"><code>const value = '${'content_'.repeat(44)}${tail}'</code></pre>
          </details></div>
        </li></ul>
      </div></article></div></section>
    </main></div></body></html>`);

  for (const width of [390, 1280]) {
    await page.setViewportSize({width, height: 844});
    await page.getByText('코드 보기', {exact: true}).click();
    const source = page.getByLabel('저장된 발췌');
    await expect(source).toContainText(tail);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(width);
    const fitsAtEnd = await source.evaluate(pre => {
      if (pre.scrollWidth <= pre.clientWidth) return false;
      pre.scrollLeft = pre.scrollWidth;
      const code = pre.querySelector('code')?.firstChild;
      if (!code?.textContent) return false;
      const range = document.createRange();
      range.setStart(code, code.textContent.length - 1);
      range.setEnd(code, code.textContent.length);
      return range.getBoundingClientRect().right <= pre.getBoundingClientRect().right;
    });
    expect(fitsAtEnd).toBe(true);
    await page.getByText('코드 보기', {exact: true}).click();
  }
});

test('comparison rows keep the example and opened document within the web report', async ({page}) => {
  const assets = new URL('../../internal/report/reportui/', import.meta.url);
  const styles = ['tabler.css', 'report.css'].map(name => readFileSync(new URL(name, assets), 'utf8'));
  styles.push(...['style.css', 'report.css'].map(name => readFileSync(new URL('../src/' + name, import.meta.url), 'utf8')));
  await page.setContent(`<!doctype html><meta name="viewport" content="width=device-width, initial-scale=1"><style>${styles.join('\n')}</style>
    <article class="report-document review-theme web-report"><div class="report-layout"><aside class="report-rail">종합</aside>
    <div class="report-main"><section class="card report-executive"><div class="card-body">
      <dl class="report-comparison-rows">
        <div><dt>설명서</dt><dd><p>입력에 따라 표시할 이름을 돌려줍니다.</p>
          <div class="report-executive-sources"><div class="report-evidence">
            <p class="report-document-example"><strong>설명서 예시</strong> <code>formatName('a')</code> → <code>'A'</code></p>
            <details class="report-excerpt"><summary class="btn btn-sm btn-ghost-primary">문서 보기</summary>
              <pre class="source-window"><code>${'long_source_line_'.repeat(120)}</code></pre>
            </details>
          </div></div>
        </dd></div>
        <div><dt>코드</dt><dd>반환값을 확인하지 못했습니다.</dd></div>
      </dl>
    </div></section></div></div></article>`);

  for (const width of [390, 1280]) {
    await page.setViewportSize({width, height: 844});
    const control = page.getByText('문서 보기', {exact: true});
    await control.focus();
    await page.keyboard.press('Enter');
    await expect(page.locator('pre.source-window')).toBeVisible();
    await expect(page.locator('.report-document-example')).toBeVisible();
    const rows = await page.locator('.report-comparison-rows > div').evaluateAll(rows => rows.map(row => row.getBoundingClientRect().top));
    expect(rows[1]).toBeGreaterThan(rows[0]);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(width);
    await page.keyboard.press('Enter');
  }
});

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
  const status = {completed: '분석 완료', partial: '일부 검사 미완료로 종료', failed: '분석 실패', cancelled: '분석 취소됨'}[doc.status as string];
  await expect(hero.getByText(status!, {exact: true})).toBeVisible();
  const labeledTime = (label: string) => hero.getByText(label, {exact: true}).locator('..').locator('time');
  if (doc.project.started_at) await expect(labeledTime('시작 시각')).toHaveAttribute('datetime', doc.project.started_at);
  if (doc.project.finished_at) {
    await expect(labeledTime('종료 시각')).toHaveAttribute('datetime', doc.project.finished_at);
    await expect(hero.getByText('분석 종료', {exact: true})).toHaveCount(0);
  }
  if (doc.inputs.commit_sha) await expect(hero.getByText(doc.inputs.commit_sha.slice(0, 12), {exact: true})).toHaveAttribute('title', doc.inputs.commit_sha);
  const judgment = report.locator('.report-executive');
  await expect(judgment).toHaveCount(1);
  await expect(judgment.locator('table, .model-reading-list')).toHaveCount(0);
  await expect(judgment.getByText('적용 범위', {exact:true})).toHaveCount(0);
  const comparisonRows = judgment.locator('.report-comparison-rows');
  const checkComparisonEvidence = async () => {
    if (!await comparisonRows.count()) return;
    await expect(comparisonRows.locator('dt')).toHaveText(['설명서', '코드']);
    for (const excerpt of await comparisonRows.locator('details.report-excerpt').all()) {
      const control = excerpt.locator('summary');
      await expect(excerpt.locator('pre')).toBeHidden();
      await control.press('Enter');
      await expect(excerpt.locator('pre')).toBeVisible();
      await expect.poll(() => report.evaluate(element => element.scrollWidth - element.clientWidth)).toBeLessThanOrEqual(1);
      await control.press('Space');
      await expect(excerpt.locator('pre')).toBeHidden();
    }
  };
  expect(await hero.locator('.report-verdict .icon').evaluate(icon => getComputedStyle(icon).maskImage)).not.toBe('none');
  expect(await hero.evaluate(async () => {
    await document.fonts.ready;
    return [...document.fonts].some(font => font.family.replaceAll('"', '') === 'CODEYE Noto Sans KR' && font.status === 'loaded');
  })).toBe(true);
  await page.setViewportSize({ width: 390, height: 844 });
  const overview = report.getByRole('tab', { name: '종합', exact: true });
  await expect(overview).toBeVisible();
  await expect.poll(() => report.evaluate(element => element.scrollWidth - element.clientWidth)).toBeLessThanOrEqual(1);
  await checkComparisonEvidence();
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
  await checkComparisonEvidence();
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
