// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import sectioned from '../../examples/report.json';
import flat from '../../examples/report-go-vet.json';
import withSlices from '../../examples/report-slices.json';
import unscreened from '../../internal/report/testdata/report-unscreened.json';
import { parseAnalysisReport } from './analysisReport';
import { ReportDocument } from './ReportPanel';
import { reportKo as t } from './locales/report-ko';

afterEach(cleanup);

// examples/report.json is the builder's output for fixtures/modules/input
// (internal/worker/review/report_example_test.go), stored with demo: true;
// examples/report-go-vet.json is a revision published before the sections.
function example(name: 'report.json' | 'report-go-vet.json' | 'report-slices.json' | 'report-unscreened.json'): Record<string, any> {
  const docs = { 'report.json': sectioned, 'report-go-vet.json': flat, 'report-slices.json': withSlices, 'report-unscreened.json': unscreened };
  const doc = structuredClone(docs[name]) as Record<string, any>;
  doc.demo = false;
  return doc;
}

describe('analysis sections', () => {
  it('shows the overview first, one collapsible section per analysis kind and the same numbers as the document', () => {
    const doc = example('report.json');
    const r = parseAnalysisReport(doc, doc.run_id);
    expect(r.sections).not.toBeNull();
    const { container } = render(<ReportDocument report={r} />);
    const article = screen.getByRole('article', { name: t.title });
    // The overview comes before every section.
    const overview = within(article).getByRole('heading', { name: t.sections.overview });
    const details = Array.from(article.querySelectorAll('details.report-section[id]'));
    expect(details.map((d) => d.id)).toEqual(doc.overview.sections.map((x: { key: string }) => 'report-section-' + x.key));
    for (const d of details) expect(overview.compareDocumentPosition(d) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    // The summary row the browser test reads.
    const total = Array.from(container.querySelectorAll('.report-counts > div')).find((x) => x.querySelector('dt')?.textContent === t.counts.total);
    expect(total?.querySelector('dd')?.textContent).toBe(String(doc.summary.total_findings));
    // Every section count is the stored number.
    for (const s of doc.overview.sections) {
      if (s.key === 'gaps') continue;
      const dl = container.querySelector(`[data-section-counts="${s.key}"]`)!;
      for (const [k, n] of Object.entries(s.counts as Record<string, number>)) {
        const row = Array.from(dl.querySelectorAll('div')).find((x) => x.querySelector('dt')?.textContent === t.sections.countLabels[s.key + '.' + k]);
        expect(row?.querySelector('dd')?.textContent, s.key + '.' + k).toBe(String(n));
      }
    }
    // States are text with an icon, never color alone.
    expect(within(article).getAllByText(/△ 일부 수행/).length).toBeGreaterThan(0);
    // A tool that did not run is not zero diagnostics.
    const jscpd = within(article).getAllByText('jscpd').map((x) => x.closest('tr')!).find((tr) => tr.cells.length === 9)!;
    expect(jscpd.textContent).toContain(t.sections.notRun);
    expect(jscpd.textContent).not.toMatch(/\b0\b.*규칙/);
    // Model interpretation is labeled with its role.
    expect(within(article).getAllByText(t.sections.model('review.documents')).length).toBe(1);
    expect(within(article).getByText(/하위 호출이 실패하면 최대 3번 재시도한다/)).toBeTruthy();
    // Each finding is listed once, in the static or the code review section.
    expect(container.querySelectorAll('details.finding-card').length).toBe(doc.findings.length);
    // The overview lists the summary's priority findings in order.
    const priorities = Array.from(container.querySelectorAll('.report-priorities > li')).map((li) => li.textContent ?? '');
    expect(priorities.length).toBe(doc.summary.priorities.length);
    doc.summary.priorities.forEach((id: string, i: number) =>
      expect(priorities[i].startsWith(doc.findings.find((f: { id: string }) => f.id === id).title)).toBe(true));
  });

  it('opens a section from the overview and moves focus to it from the keyboard', async () => {
    const doc = example('report.json');
    const r = parseAnalysisReport(doc, doc.run_id);
    const { container } = render(<ReportDocument report={r} />);
    const environment = container.querySelector('#report-section-environment') as HTMLDetailsElement;
    // Sections the overview points at start open; the rest are collapsed.
    expect(environment.open).toBe(true);
    const table = screen.getByRole('heading', { name: t.sections.stateTable }).nextElementSibling!;
    const button = within(table as HTMLElement).getByRole('button', { name: t.sections.titles.metrics });
    const metrics = container.querySelector('#report-section-metrics') as HTMLDetailsElement;
    expect(metrics.open).toBe(false);
    fireEvent.click(button);
    expect(metrics.open).toBe(true);
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(document.activeElement).toBe(metrics.querySelector('summary'));
  });

  it('keeps the flat layout for a revision published before the sections', () => {
    const doc = example('report-go-vet.json');
    const r = parseAnalysisReport(doc, doc.run_id);
    expect(r.sections).toBeNull();
    const { container } = render(<ReportDocument report={r} />);
    expect(container.querySelector('details.report-section')).toBeNull();
    expect(screen.getByRole('heading', { name: t.findings })).toBeTruthy();
    expect(container.querySelectorAll('details.finding-card').length).toBe(doc.findings.length);
  });

  it('rejects sections that drop, duplicate or invent an observation, finding or count', () => {
    const base = () => example('report.json');
    const ok = base();
    expect(() => parseAnalysisReport(ok, ok.run_id)).not.toThrow();
    const cases: Array<[string, (d: Record<string, any>) => void]> = [
      ['block missing', (d) => delete d.secrets],
      ['finding in both sections', (d) => d.code_review.finding_ids.push(d.static_analysis.languages[0].tools.find((x: any) => x.finding_ids.length).finding_ids[0])],
      ['finding dropped', (d) => d.code_review.finding_ids.pop()],
      ['observation dropped', (d) => (d.metrics.metrics[0].observation_ids = [])],
      ['observation in two sections', (d) => d.metrics.metrics[0].observation_ids.push(d.secrets.detections[0].observation_id)],
      ['section counts do not add up', (d) => d.overview.sections.find((s: any) => s.key === 'code_review').counts.findings++],
      ['gap count invented', (d) => d.overview.sections.find((s: any) => s.key === 'gaps').counts.gaps++],
      ['model performed with a reason', (d) => (d.documents.model.reason = 'x')],
      ['gaps without sections', (d) => {
        for (const k of ['overview', 'environment', 'documents', 'static_analysis', 'metrics', 'secrets', 'code_review']) delete d[k];
      }],
    ];
    for (const [name, edit] of cases) {
      const d = base();
      edit(d);
      expect(() => parseAnalysisReport(d, d.run_id), name).toThrow();
    }
  });

  it('shows what each dependency task prepared, or why not, in the environment section', () => {
    const doc = example('report.json');
    const row = (over: Record<string, unknown>) => ({
      task_key: 'deps.go', task_status: 'succeeded', task_reason: null, result: 'accepted',
      module: 'go:.', ecosystem: 'go', path: '.', manager: 'go', manager_version: 'go1.27.4',
      state: 'prepared', reason: null, pinned: true, lockfile: 'go.sum', declared: 6, prepared: 6, failed: 0, cache_kind: 'shared',
      failed_items: [], limitations: ['dependencies_prepared'], ...over,
    });
    doc.environment.dependencies = [
      row({}),
      row({ task_key: 'deps.go.m01', module: 'go:svc/a', path: 'svc/a', state: 'partial', reason: 'module_not_on_proxy', declared: 4, prepared: 3, failed: 1,
        failed_items: [{ name: 'example.com/private/lib', reason: 'module_not_on_proxy' }], limitations: ['dependencies_partially_prepared', 'dependency_versions_not_pinned'], pinned: false }),
      row({ task_key: 'deps.go.m02', module: 'go:svc/b', task_status: 'skipped_policy', task_reason: 'no_dependencies', state: 'skipped', reason: 'no_dependencies', declared: 0, prepared: 0, limitations: [] }),
      // The record did not reach the report: only the task's facts.
      { task_key: 'deps.go.m03', task_status: 'failed', task_reason: null, result: 'result_missing', module: null, ecosystem: null, path: null, manager: null, manager_version: null,
        state: null, reason: null, pinned: null, lockfile: null, declared: null, prepared: null, failed: null, cache_kind: null, failed_items: [], limitations: [] },
    ];
    doc.gaps.push({ section: 'environment', code: 'dependency_versions_not_pinned', subject: 'deps.go.m01', detail: null });
    doc.overview.sections.find((s: any) => s.key === 'gaps').counts.gaps = doc.gaps.length;
    const r = parseAnalysisReport(doc, doc.run_id);
    expect(r.sections!.environment.dependencies.length).toBe(4);
    const { container } = render(<ReportDocument report={r} />);
    const section = container.querySelector('#report-section-environment') as HTMLElement;
    const d = t.sections.dependencies;
    within(section).getByRole('heading', { name: d.heading });
    expect(within(section).getByText(d.lead)).toBeTruthy();
    const table = Array.from(section.querySelectorAll('table')).find((x) => x.querySelector('th')?.textContent === t.sections.col.module && x.textContent!.includes(d.col.manager))!;
    const cells = Array.from(table.querySelectorAll('tbody tr')).map((tr) => Array.from(tr.querySelectorAll('td')).map((td) => td.textContent));
    expect(cells).toEqual([
      ['go:.', 'go 1.27.4', d.state.prepared, '6/6', '—', t.reasons.dependencies_prepared],
      ['go:svc/a', 'go 1.27.4', d.state.partial, '3/4 ' + d.failed(1), t.reasons.module_not_on_proxy, t.reasons.dependencies_partially_prepared + ' ' + t.reasons.dependency_versions_not_pinned],
      ['go:svc/b', 'go 1.27.4', d.state.skipped, '0/0', t.reasons.no_dependencies, '—'],
      ['deps.go.m03', '—', t.sections.taskStatus.failed, '—', d.record.result_missing, '—'],
    ]);
    const failed = within(section).getByRole('heading', { name: d.failedItems }).nextElementSibling!;
    expect(failed.textContent).toBe('go:svc/a example.com/private/lib — ' + t.reasons.module_not_on_proxy);
    const gaps = container.querySelector('#report-section-gaps') as HTMLElement;
    expect(within(gaps).getByText(t.sections.gapLabels.dependency_versions_not_pinned)).toBeTruthy();
  });

  it('leaves the dependency table out when no dependency task was planned', () => {
    const doc = example('report.json');
    delete doc.environment.dependencies;
    const env = doc.overview.sections.find((s: any) => s.key === 'environment').counts;
    delete env.dependencies;
    delete env.dependencies_prepared;
    const r = parseAnalysisReport(doc, doc.run_id);
    expect(r.sections!.environment.dependencies).toEqual([]);
    const { container } = render(<ReportDocument report={r} />);
    const section = container.querySelector('#report-section-environment') as HTMLElement;
    expect(within(section).queryByRole('heading', { name: t.sections.dependencies.heading })).toBeNull();
    expect(section.querySelector('[data-count="dependencies"]')).toBeNull();
  });

  // examples/report-slices.json is the builder's output for
  // fixtures/modules/slices/input under call ceiling 15
  // (internal/worker/review/report_slices_example_test.go).
  it('shows the per-module review ledger with the stored numbers and never calls the sent scope verified', () => {
    const doc = example('report-slices.json');
    const r = parseAnalysisReport(doc, doc.run_id);
    const ledger = r.sections!.codeReview.ledger!;
    expect(ledger.modules.length).toBe(doc.code_review.ledger.modules.length);
    const { container } = render(<ReportDocument report={r} />);
    const section = container.querySelector('#report-section-code_review') as HTMLElement;
    const heading = within(section).getByRole('heading', { name: t.sections.ledger.heading });
    let next = heading.nextElementSibling;
    while (next && !next.querySelector('table')) next = next.nextElementSibling;
    const table = next!.querySelector('table')!;
    for (const m of doc.code_review.ledger.modules) {
      const row = Array.from(table.querySelectorAll('tbody tr')).find((tr) => tr.querySelector('code')?.textContent === m.module_id)!;
      const cells = Array.from(row.querySelectorAll('td')).map((td) => td.textContent);
      expect(cells.slice(2)).toEqual([m.files_eligible, m.files_sent_to_model, m.bytes_sent, m.files_truncated, m.files_withheld, m.files_unreadable,
        m.files_budget_exhausted, m.files_not_planned, m.files_not_sent, m.files_with_completed_review].map(String));
    }
    expect(within(section).getAllByText('review.investigate.s004').length).toBe(1);
    expect(within(section).getByText(t.sections.ledger.calls(String(doc.code_review.ledger.calls_used), String(doc.code_review.ledger.max_calls)))).toBeTruthy();
    expect(section.textContent).not.toMatch(/검증된 범위입니다|verified/);
    // Slice findings are listed once, in the code review section.
    const slice = doc.findings.filter((f: { id: string }) => f.id.startsWith('review_s')).length;
    expect(slice).toBeGreaterThan(0);
    expect(section.querySelectorAll('details.finding-card').length).toBe(doc.code_review.finding_ids.length);
  });

  it('rejects a ledger whose module files do not add up or whose overview scope differs', () => {
    const cases: Array<[string, (d: Record<string, any>) => void]> = [
      ['module files do not add up', (d) => d.code_review.ledger.modules[0].files_sent_to_model++],
      ['completed beyond sent', (d) => (d.code_review.ledger.modules[0].files_with_completed_review = 99)],
      ['overview scope differs', (d) => d.overview.sections.find((s: any) => s.key === 'code_review').counts.files_sent_to_model++],
    ];
    for (const [name, edit] of cases) {
      const d = example('report-slices.json');
      edit(d);
      expect(() => parseAnalysisReport(d, d.run_id), name).toThrow();
    }
  });

  // internal/report/testdata/report-unscreened.json: the same fixture without
  // a secrets scan at the default ceiling
  // (internal/worker/review/report_slices_example_test.go).
  it('reviews the modules without a secrets screen and says the input was not screened', () => {
    const doc = example('report-unscreened.json');
    const r = parseAnalysisReport(doc, doc.run_id);
    const ledger = r.sections!.codeReview.ledger!;
    expect(ledger.modules.length).toBeGreaterThan(0);
    expect(ledger.status).toBe('complete');
    const { container } = render(<ReportDocument report={r} />);
    const section = container.querySelector('#report-section-code_review') as HTMLElement;
    expect(within(section).getByText(t.sections.ledger.status.complete)).toBeTruthy();
    const gaps = container.querySelector('#report-section-gaps') as HTMLElement;
    expect(within(gaps).getByText(t.sections.gapLabels.model_input_not_screened)).toBeTruthy();
    expect(within(gaps).queryByText(t.sections.gapLabels.review_scope_skipped)).toBeNull();
  });
});
