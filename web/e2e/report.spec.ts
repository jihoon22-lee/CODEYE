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

test('mobile source wraps every code line and can scroll complete unwrapped code', async ({page}) => {
  const assets = new URL('../../internal/report/reportui/', import.meta.url);
  const css = ['tabler.css', 'report.css', 'workbench.css'].map(name => readFileSync(new URL(name, assets), 'utf8')).join('\n');
  const line = '    fmt.Printf("the report records this format expression", firstArgument, secondArgument)';
  await page.setViewportSize({width:390,height:844});
  await page.setContent(`<!doctype html><meta name="viewport" content="width=device-width, initial-scale=1"><style>${css}</style>
    <article class="report-document review-theme web-report"><div class="report-selected-source"><pre class="source-window" tabindex="0"><code>
    <span class="source-line"><span class="source-line-number" aria-hidden="true">4</span><span class="source-line-code">${'long_context_'.repeat(34)}</span></span>
    <span class="source-line" data-focus="true"><span class="source-line-number" aria-hidden="true">5</span><span class="source-line-code">${line}</span></span>
    </code></pre></div></article>`);
  const source = page.locator('.source-window');
  expect(await source.evaluate(pre => pre.scrollWidth - pre.clientWidth)).toBeLessThanOrEqual(1);
  const selection = await source.locator('[data-focus] .source-line-code').evaluate(element => {
    const range = document.createRange();
    range.selectNodeContents(element);
    const pre = element.closest('pre')!.getBoundingClientRect();
    const lines = Array.from(range.getClientRects());
    return {count:lines.length, contained:lines.every(line => line.left >= pre.left && line.right <= pre.right + 1)};
  });
  expect(selection.count).toBeGreaterThan(1);
  expect(selection.contained).toBe(true);
  expect(await source.locator('.source-line:first-child .source-line-code').evaluate(line => line.getBoundingClientRect().height)).toBeGreaterThan(20);
  await page.locator('.report-selected-source').evaluate(preview => { preview.dataset.wrap = 'false'; });
  expect(await source.evaluate(pre => pre.scrollWidth > pre.clientWidth)).toBe(true);
  expect(await source.evaluate(pre => {
    pre.scrollLeft = pre.scrollWidth;
    return pre.scrollLeft > 0 && Math.abs(pre.scrollWidth - pre.clientWidth - pre.scrollLeft) < 2;
  })).toBe(true);
});

test('saved report focuses one mobile finding while retaining its list, links and print source', async ({page}) => {
  const assets = new URL('../../internal/report/reportui/', import.meta.url);
  const css = ['tabler.css', 'report.css', 'workbench.css'].map(name => readFileSync(new URL(name, assets), 'utf8')).join('\n');
  const script = readFileSync(new URL('export-navigation.js', assets), 'utf8');
  const finding = (id: string, title: string) => `
    <section class="report-group"><div class="report-export-workbench">
    <div class="report-workbench-index"><table class="report-workbench-table export-workbench-table"><tbody><tr><td colspan="4">
    <details class="report-result-details" name="report-finding-rows"><summary id="${id}" class="export-workbench-select"><strong>${title}</strong></summary>
    <div class="export-workbench-body"><button type="button" data-report-inspector-close>결과 목록</button>
    <h6 class="report-finding-heading" tabindex="-1">${title}</h6>
    <div class="report-selected-source" data-wrap="false"><pre class="source-window" data-mode="short"><code>
    <span class="source-line"><span class="source-line-number">18</span><span class="source-line-code">${'long_source_line_'.repeat(25)}</span></span>
    </code></pre><pre class="source-window" data-mode="full" hidden>full source</pre></div>
    <details class="report-excerpt" data-preview-covered><summary>코드 보기</summary><pre>duplicate stored source</pre></details>
    </div></details></td></tr></tbody></table></div><aside class="export-inspector report-workbench-detail" hidden></aside>
    </div></section>`;
  await page.setViewportSize({width:390,height:844});
  await page.setContent(`<!doctype html><meta name="viewport" content="width=device-width, initial-scale=1"><style>${css}</style>
    <article class="report-export review-theme"><nav class="report-rail"><a href="#code_review">코드 분석</a></nav>
    <main class="report-main"><section class="export-section" id="code_review"><h2>코드 분석</h2>
    ${finding('finding-one', '첫 번째 결과')}${finding('finding-two', '두 번째 결과')}</section></main></article>`);
  const first = page.locator('#finding-one');
  const second = page.locator('#finding-two');
  const heading = page.getByRole('heading', {name:'두 번째 결과', exact:true});
  await second.press('Enter');
  await expect(heading).toBeVisible();
  await expect(first).toBeVisible();
  await second.press('Enter');

  await page.addScriptTag({content:script});
  await page.evaluate(() => { location.hash = '#finding-two'; });
  await expect(heading).toBeFocused();
  await expect(page.locator('.export-workbench-select:visible')).toHaveCount(0);
  await expect(page.getByText('두 번째 결과', {exact:true}).filter({visible:true})).toHaveCount(1);
  expect(await heading.evaluate(element => parseFloat(getComputedStyle(element).fontSize))).toBeGreaterThanOrEqual(16);
  const selected = heading.locator('..');
  const source = selected.locator('.source-window[data-mode=short]');
  await selected.locator('.report-selected-source').evaluate(element => { element.dataset.wrap = 'false'; });
  expect(await source.evaluate(pre => pre.scrollWidth > pre.clientWidth)).toBe(true);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(390);

  await page.emulateMedia({media:'print'});
  await page.evaluate(() => dispatchEvent(new Event('beforeprint')));
  await expect(page.getByText('두 번째 결과', {exact:true}).filter({visible:true})).toHaveCount(1);
  await expect(source).toBeVisible();
  await expect(selected.locator('.report-excerpt')).toBeHidden();
  expect((await heading.evaluate(element => parseFloat(getComputedStyle(element).fontSize))) * .75).toBeGreaterThanOrEqual(11);
  await page.evaluate(() => dispatchEvent(new Event('afterprint')));
  await page.emulateMedia({media:'screen'});
  await page.getByRole('button', {name:'결과 목록'}).press('Enter');
  await expect(second).toBeFocused();
  await expect(first).toBeVisible();
  await expect(second).toBeVisible();
});

test('action cards keep status, line numbers and expanders inside phone and desktop widths', async ({page}) => {
  const assets = new URL('../../internal/report/reportui/', import.meta.url);
  const css = ['tabler.css', 'report.css', 'workbench.css', 'actions.css'].map(name => readFileSync(new URL(name, assets), 'utf8')).join('\n');
  const longPath = 'internal/gateway/' + 'very_long_directory_name/'.repeat(4) + 'forward.go';
  const line = (n: number, text: string) => `<span class="source-line"><span class="source-line-number" aria-hidden="true">${n}</span><span class="source-line-code">${text}</span></span>`;
  await page.setContent(`<!doctype html><meta name="viewport" content="width=device-width, initial-scale=1"><style>${css}</style>
    <article class="report-document review-theme web-report"><div class="report-layout"><div class="report-main">
    <p class="report-coverage-note"><strong>분석 범위의 한계</strong> 판정에 필요한 분석 작업 1개가 성공하지 않았습니다. <button type="button" class="report-plain-action">분석 범위와 상태 보기</button></p>
    <section class="report-actions"><div class="report-group" data-tone="action">
    <article class="card report-action-card" data-severity="high"><div class="card-body">
      <header class="report-action-head"><h6>재시도 반복에서 앞선 시도의 오류를 덮어씀</h6>
        <p class="report-action-badges"><span class="report-status-badge" data-tone="ready">근거 있음</span><span class="report-action-severity">심각도 높음</span></p></header>
      <dl class="report-action-rows"><div><dt>확인된 발생 조건</dt><dd>send가 두 번 이상 실패하는 경우</dd></div><div><dt>영향</dt><dd>재시도가 모두 실패하면 첫 실패의 원인을 로그에서 찾을 수 없다.</dd></div></dl>
      <div class="report-selected-source" data-wrap="true"><p class="report-excerpt-label"><code class="report-path-text">${longPath}</code></p>
        <pre class="source-window" tabindex="0" data-mode="short"><code>${line(3, 'func Forward() error {')}${line(4, '\t' + 'err = sendWithAVeryLongName(' + 'argument, '.repeat(12) + ')')}</code></pre></div>
      <button type="button" class="btn btn-sm btn-ghost-primary report-card-action">근거와 반대 근거 보기 (근거 1건 · 반대 근거 1건)</button>
    </div></article></div>
    <details class="report-more report-action-fold"><summary>추가 확인이 필요한 후보 1건</summary><div class="report-more-body"><ul class="report-action-list"><li>
      <div class="report-action-list-head"><strong>취소 신호를 확인하지 않을 수 있다</strong><span class="report-status-badge" data-tone="attention">판단 보류</span></div><code class="report-path-text">${longPath}:3–16</code></li></ul></div></details>
    <details class="report-more report-action-fold"><summary>도구 진단 2건 (규칙 2종, 도구 1종)</summary><div class="report-diag-groups"><details class="report-diag-group"><summary>
      <span class="report-diag-name"><code>go.staticcheck</code> <code>SA4006</code></span><span class="report-diag-count">1건</span><span class="report-diag-description">이 값은 이전 시도의 오류를 기록하기 전에 덮어쓰인다.</span></summary><ul><li><code class="report-path-text">${longPath}:13</code></li></ul></details></div></details>
    </section></div></div></article>`);

  for (const width of [390, 1280]) {
    await page.setViewportSize({width, height: 844});
    await page.locator('.report-action-fold > summary').first().click();
    await page.locator('.report-diag-group').locator('xpath=ancestor::details[1]').evaluate(details => { (details as HTMLDetailsElement).open = true; });
    await page.locator('.report-diag-group > summary').click();
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(width);
    for (const selector of ['.report-status-badge', '.report-action-severity', '.source-line-number', '.report-action-fold > summary', '.report-diag-group > summary', '.report-card-action']) {
      for (const element of await page.locator(selector).all()) {
        const box = (await element.boundingBox())!;
        expect(box.x, selector).toBeGreaterThanOrEqual(0);
        expect(box.x + box.width, selector).toBeLessThanOrEqual(width + 1);
      }
    }
    // Long source wraps inside its own window, never the page.
    const source = page.locator('.report-action-card .source-window');
    expect(await source.evaluate(pre => pre.scrollWidth - pre.clientWidth)).toBeLessThanOrEqual(1);
    await page.locator('.report-diag-group > summary').click();
    await page.locator('.report-action-fold > summary').first().click();
  }
  // Keyboard: a fold opens and closes with Enter and Space.
  const fold = page.locator('.report-action-fold > summary').first();
  await fold.focus();
  await page.keyboard.press('Enter');
  await expect(page.locator('.report-action-list')).toBeVisible();
  await page.keyboard.press('Space');
  await expect(page.locator('.report-action-list')).toBeHidden();
});

// The web panel also loads the application sheet, whose generic dl rule gives every definition list a
// max-content first column; the saved HTML does not. Both must keep long unbreakable values inside the card.
for (const surface of ['web panel', 'saved HTML']) {
  test(`${surface} keeps long unbreakable values of action cards and findings inside the report`, async ({page}) => {
    const assets = new URL('../../internal/report/reportui/', import.meta.url);
    const names = surface === 'web panel' ? ['tabler.css', 'report.css', '../../../web/src/report.css', 'workbench.css', 'actions.css', '../../../web/src/style.css']
      : ['tabler.css', 'report.css', 'export.css', 'workbench.css', 'actions.css'];
    const css = names.map(name => readFileSync(new URL(name, assets), 'utf8')).join('\n');
    const [open, close] = surface === 'web panel'
      ? ['<main><div class="project-detail"><article class="report-document review-theme web-report sectioned-report"><div class="report-layout"><div class="report-main">', '</div></div></article></div></main>']
      : ['<article class="report-document review-theme report-export"><div class="report-layout export-page"><main class="report-main">', '</main></div></article>'];
    const long = (name: string) => `<span data-long="${name}">${name}-${'x'.repeat(300)}</span>`;
    const row = (label: string, name: string) => `<div><dt>${label}</dt><dd>${long(name)} 뒤에 이어지는 설명</dd></div>`;
    const source = `<div class="report-selected-source" data-wrap="false"><p class="report-excerpt-label"><code class="report-path-text">${long('path')}</code></p>
      <pre class="source-window" tabindex="0"><code><span class="source-line"><span class="source-line-number" aria-hidden="true">4</span><span class="source-line-code">func Sum(samples []int) int {</span></span></code></pre></div>`;
    await page.setContent(`<!doctype html><meta name="viewport" content="width=device-width, initial-scale=1"><style>${css}</style>
      ${open}
      <dl class="report-counts section-counts" data-section-counts="code_review">
        <div data-count="candidates_not_shipped"><dt><span data-long="count-candidates-not-shipped">시험·예제 등 배포 코드가 아닌 경로만 인용해 검증하지 않은 모델 후보</span></dt><dd>12</dd></div>
        <div data-count="hypotheses_not_shipped"><dt><span data-long="count-hypotheses-not-shipped">시험·예제 등 배포 코드가 아닌 경로의 도구 진단에 대한 모델 해석 가설(조치 항목에서 제외, 반증된 것은 뺌)</span></dt><dd>12</dd></div>
        <div data-count="long"><dt>${long('count-label')}</dt><dd>3</dd></div></dl>
      <section class="report-actions"><div class="report-group" data-tone="action">
      <article class="card report-action-card" data-severity="medium"><div class="card-body">
        <header class="report-action-head"><h6>${long('title')}</h6><p class="report-action-badges"><span class="report-status-badge" data-tone="ready">근거 있음</span><span class="report-action-severity">심각도 보통</span></p></header>
        <dl class="report-action-rows">${row('발생 조건(가정)', 'condition')}${row('영향', 'impact')}</dl>${source}
        <dl class="report-action-rows">${row('개선 방향', 'recommendation')}${row('확인 방법', 'check')}${row('판단 범위', 'limit')}</dl>
        <button type="button" class="btn btn-sm btn-ghost-primary report-card-action">근거와 반대 근거 보기 (근거 3건 · 반대 근거 0건)</button>
      </div></article>
      <article class="card report-action-card report-mismatch-card"><div class="card-body">
        <header class="report-action-head"><h6>${long('statement')}</h6></header>
        <dl class="report-action-rows">${row('구현에서 관찰한 동작', 'observed')}${row('적용 범위', 'scope')}${row('문서 위치', 'document')}${row('코드 위치', 'code')}</dl>
      </div></article>
      <details class="report-more report-action-fold" open><summary>추가 확인이 필요한 후보 1건</summary><div class="report-more-body"><ul class="report-action-list"><li>
        <div class="report-action-list-head"><strong>${long('candidate')}</strong></div><code class="report-path-text">${long('candidate-path')}</code><p><strong>확인 방법</strong> ${long('candidate-check')}</p></li></ul></div></details>
      <details class="card report-disclosure" id="secondary-findings" open><summary>예제·시험 입력 등에서 발견한 항목 3건 · 이 중 배포 코드를 인용하지 않아 검증하지 않은 모델 후보 1건 · 이 중 배포 코드가 아닌 경로의 도구 진단에 대한 모델 해석 가설 1건</summary>
        <p>제품 코드가 아닌 경로(예제·시험 입력 등)의 항목이다. 검증 상태는 항목마다 표시한다.</p>
        <ul class="report-action-list"><li><div class="report-action-list-head"><strong>${long('not-shipped-title')}</strong><span class="report-status-badge" data-tone="attention">검증 전</span><span class="report-action-severity">심각도 낮음</span></div>
          <code class="report-path-text">${long('not-shipped-path')}</code>
          <p><strong>검증하지 않음</strong> 배포 코드가 아닌 경로의 줄만 인용한 조사 후보라 검증 요청에 보내지 않았다(검증 상태 unreviewed, 한계 candidate_not_shipped). ${long('not-shipped-text')}</p></li>
          <li><div class="report-action-list-head"><strong>${long('hypothesis-title')}</strong><span class="report-status-badge" data-tone="ready">근거 있음</span><span class="report-action-severity">심각도 낮음</span></div>
          <code class="report-path-text">${long('hypothesis-path')}</code>
          <p><strong>조치 항목 아님</strong> 배포 코드가 아닌 경로(시험·예제·벤치마크·생성물·외부 코드)의 도구 진단과 줄에 대한 해석 가설이다. 검증 상태는 모델 검증의 결과 그대로이고, 조치 항목이나 미확정 후보로 세지 않는다(한계 hypothesis_not_shipped). ${long('hypothesis-text')}</p></li></ul></details>
      <details class="card report-disclosure" id="tool-diagnostics" open><summary>도구 진단 853건 중 238건 판정(문제·무해), 120건 판단 보류 (규칙 41종, 도구 3종)</summary>
        <p>모델의 분류는 결함 확인이 아니다.</p>
        <p class="report-diag-paths">${long('paths-total')} 경로별: 시험·예제 등 배포 코드가 아닌 경로 17건, 배포 코드 2건. 배포 코드가 아닌 경로의 진단은 기본 심각도를 낮춰 매긴다.</p>
        <section class="report-diag-bucket" id="tool-diagnostics-harmless"><h4>무해하다고 판단한 진단 3건</h4><p>${long('bucket-note')}</p><p class="report-diag-paths">${long('paths-bucket')} 경로별: 시험·예제 등 배포 코드가 아닌 경로 3건, 배포 코드 0건.</p>
        <div class="report-diag-groups"><details class="report-diag-group" open><summary><span class="report-diag-name"><code>${long('triage-analyzer')}</code> <code>${long('triage-rule')}</code></span> <span class="report-diag-count">3건</span> <span class="report-diag-severity">기본 심각도 높음</span><span class="report-diag-description">${long('triage-description')}</span></summary>
          <ul class="report-diag-notes"><li><strong>무해</strong> ${long('triage-reason')} 앞선 검사가 이 경로를 막는다. <code class="report-path-text">${long('triage-location')}</code> <a href="#finding">${long('triage-link')}</a></li>
            <li><strong>판단 보류</strong> ${'호출자 코드가 입력에 없어 진단의 영향을 판단할 수 없다. '.repeat(12)}<code class="report-path-text">${long('triage-location-2')}</code></li></ul>
          <ul><li><code class="report-path-text">${long('triage-place')}</code></li></ul></details></div></section>
      </details>
      </div></section>
      <section class="card report-card report-tab-content"><div class="card-body"><div class="report-finding-workbench" data-selected="true"><section class="report-workbench-detail"><h6 class="report-finding-heading">${long('finding-title')}</h6>
        <div class="finding-body">${source}
        <dl class="key"><dt>영향</dt><dd>${long('finding-impact')}</dd><dt>개선 방향</dt><dd>${long('finding-recommendation')}</dd></dl>
        <dl><dt>발생 조건</dt><dd><ul><li>${long('finding-condition')}</li></ul></dd><dt>심각도 근거</dt><dd>${long('finding-severity')}</dd><dt>확인 방법</dt><dd><ul><li>${long('finding-check')}</li></ul></dd></dl>
        <h6>판단 한계</h6><ul><li>${long('finding-limit')}</li></ul>
        <details class="finding-evidence" open><summary>근거 1개와 판단 과정</summary><p><strong>추론 · </strong>${long('finding-claim')}</p>
          <ul><li>${long('finding-evidence')}<div class="report-evidence"><span class="report-file"><code class="report-file-name report-path-text">${long('finding-evidence-path')}</code></span></div></li></ul></details>
        </div></section></div></div></section>
      ${close}`);
    for (const width of [1280, 390]) {
      await page.setViewportSize({width, height: 900});
      const article = page.locator('article.report-document');
      await expect.poll(() => article.evaluate(element => element.scrollWidth - element.clientWidth), `${surface} ${width}px`).toBeLessThanOrEqual(1);
      expect(await page.evaluate(() => document.documentElement.scrollWidth - innerWidth), `${surface} ${width}px page`).toBeLessThanOrEqual(0);
      // Every long value breaks inside its own box instead of extending past the report edge.
      const edge = await article.evaluate(element => element.getBoundingClientRect().right);
      for (const value of await page.locator('[data-long]').all()) {
        const name = await value.getAttribute('data-long');
        const box = (await value.boundingBox())!;
        expect(box.x + box.width, `${surface} ${width}px ${name}`).toBeLessThanOrEqual(edge + 1);
      }
    }
  });
}

test('a link from an action card opens the collapsed blocks around its target in the saved HTML', async ({page}, info) => {
  const assets = new URL('../../internal/report/reportui/', import.meta.url);
  const css = ['tabler.css', 'report.css', 'export.css', 'workbench.css', 'actions.css'].map(name => readFileSync(new URL(name, assets), 'utf8')).join('\n');
  const script = readFileSync(new URL('export-navigation.js', assets), 'utf8');
  const hash = createHash('sha256').update(script).digest('base64');
  const file = info.outputPath('actions.html');
  mkdirSync(dirname(file), {recursive: true});
  writeFileSync(file, `<!doctype html><html lang="ko"><meta name="viewport" content="width=device-width, initial-scale=1">
    <meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'sha256-${hash}'">
    <style>${css}</style>
    <body class="review-theme report-export"><div class="report-layout export-page">
    <nav class="report-rail"><div class="export-desktop-nav"><ul class="nav"><li><a href="#overview" class="nav-link">종합</a></li></ul></div></nav>
    <main class="report-main"><section class="export-section" id="overview"><h2>종합</h2>
      <p class="report-coverage-note"><strong>분석 범위의 한계</strong> 분석 공백 1건 <a id="to-coverage" href="#coverage">분석 범위와 상태 보기</a></p>
      <article class="card report-action-card"><div class="card-body"><a id="to-evidence" href="#finding-row" class="btn btn-sm btn-ghost-primary report-card-action">근거와 반대 근거 보기</a></div></article>
      <details class="card report-disclosure" id="candidates"><summary>추가 확인이 필요한 후보 1건</summary>
        <details class="report-diag-group"><summary>규칙</summary><p id="finding-row">숨은 근거 행</p></details></details>
      <section id="coverage" tabindex="-1"><h3>주요 분석 결과</h3></section>
    </section></main></div><script>${script}</script></body></html>`);
  for (const width of [1280, 390]) {
    await page.setViewportSize({width, height: 800});
    await page.goto(pathToFileURL(file).href);
    await expect(page.locator('#finding-row')).toBeHidden();
    await page.locator('#to-evidence').click();
    await expect(page.locator('#finding-row')).toBeVisible();
    await expect(page.locator('#candidates')).toHaveJSProperty('open', true);
    await page.locator('#to-coverage').click();
    await expect(page.locator('#coverage')).toBeInViewport();
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
  const listToggle = page.getByRole('button', {name:'목록 열기',exact:true});
  const workspace = page.locator('.project-workspace');
  for (const width of [1280,390]) {
    await page.setViewportSize({width,height:844});
    await expect.poll(() => workspace.evaluate(element => element.clientWidth - element.querySelector<HTMLElement>('.project-detail')!.clientWidth)).toBeLessThanOrEqual(1);
    await listToggle.focus();
    await expect(target).toBeHidden();
    await listToggle.press('Enter');
    await expect(target).toBeVisible();
    await expect(target).toHaveAttribute('aria-pressed','true');
    await page.getByRole('button', {name:'목록 접기',exact:true}).press('Space');
    await expect(target).toBeHidden();
  }
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
  const context = report.locator('details.report-overview-context');
  await expect(context).not.toHaveAttribute('open', '');
  await expect(report.getByRole('tabpanel', {name: '종합', exact: true}).locator('details.report-overview-context pre:visible')).toHaveCount(0);
  await context.locator(':scope > summary').press('Enter');
  await expect(context).toHaveAttribute('open', '');
  // A reading with nothing to act on keeps its judgment card one fold deeper, under "문서 동작 대조".
  const assessment = context.locator('details.report-behavior-assessment');
  if (await assessment.count()) {
    await expect(judgment).toBeHidden();
    await assessment.locator(':scope > summary').press('Enter');
    await expect(assessment).toHaveAttribute('open', '');
  }
  await expect(judgment).toBeVisible();
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
      const disclosure = counts.locator('xpath=ancestor::details[not(@open)][last()]/summary');
      while (await disclosure.count()) {
        await expect(disclosure).toBeVisible();
        await disclosure.press('Enter');
      }
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
    const section = report.locator('#report-section-' + key);
    const indexed = section.locator('button.report-finding-select').filter({ hasText: item.title }).first();
    const card = await indexed.count() ? section.locator('.report-workbench-detail') :
      section.locator('details.finding-card').filter({has: page.locator('summary > strong').getByText(item.title, {exact:true})}).first();
    if (await indexed.count()) await indexed.press('Enter');
    else await card.locator('summary').first().click();
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
