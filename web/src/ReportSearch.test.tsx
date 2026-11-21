// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import flatExample from '../../examples/report-go-vet.json';
import sectionedExample from '../../examples/report.json';
import comparisonExample from '../../examples/comparison.json';
import { parseAnalysisReport } from './analysisReport';
import { parseComparison } from './comparison';
import { createApi } from './api';
import { ReportDocument } from './ReportPanel';
import { reportKo as t } from './locales/report-ko';
import { reportSearchKo as s } from './locales/report-search-ko';
import { json, mockFetch } from './test-utils';

afterEach(cleanup);

const trigger = () => screen.getByRole('button', { name: s.title });
const panel = () => trigger().closest('.report-search') as HTMLElement;

function enter(query: string) {
  if (trigger().getAttribute('aria-expanded') === 'false') fireEvent.click(trigger());
  const input = within(panel()).getByRole('searchbox', { name: s.query });
  fireEvent.change(input, { target: { value: query } });
  return input;
}

function sectioned(project = true) {
  const report = parseAnalysisReport({ ...sectionedExample, demo: false }, sectionedExample.run_id);
  report.project = project ? { name: 'Billing', source: null, start: null, finish: null } : undefined;
  return report;
}

describe('published report search', () => {
  it('searches Korean and published source symbols and focuses the exact comparison from another tab', () => {
    const report = sectioned();
    const row = report.sections!.documents.comparisons[0];
    render(<ReportDocument report={report} />);
    const tab = screen.getByRole('tab', { name: t.sections.titles.code_review });
    fireEvent.click(tab);
    expect(within(panel()).queryByRole('searchbox')).toBeNull();
    const input = enter('재시도'.normalize('NFD') + ' MAXRETRIES');
    expect(input).toBe(document.activeElement);
    const result = within(panel()).getByRole('button', { name: new RegExp(row.statement) });
    expect(result.textContent).not.toContain('const MaxRetries');
    fireEvent.click(result);
    const documentTab = screen.getByRole('tabpanel', { name: t.sections.titles.documents });
    expect(document.activeElement).toBe(within(documentTab).getByRole('heading', { name: row.statement }));
    expect(trigger().getAttribute('aria-expanded')).toBe('false');
  });

  it('opens exact findings by rule and path, including an unopened long list', () => {
    const report = sectioned();
    const finding = report.findings.find(f => f.locations.includes('svc/a/normalize.go'))!;
    render(<ReportDocument report={report} />);
    enter('sa6005 svc/a/normalize.go');
    fireEvent.submit(within(panel()).getByRole('search'));
    const staticPanel = screen.getByRole('tabpanel', { name: t.sections.titles.static_analysis });
    const summary = within(staticPanel).getByText(finding.title).closest('summary');
    expect(document.activeElement).toBe(summary);
    expect(summary?.closest<HTMLDetailsElement>('.finding-card')?.open).toBe(true);
    expect(summary?.closest<HTMLDetailsElement>('details.report-more')?.open).toBe(true);
    enter('sa6005');
    fireEvent.click(within(panel()).getByRole('button', { name: /SA6005/ }));
    expect(document.activeElement).toBe(summary);
  });

  it('opens an archived section disclosure without leaving focus in a hidden panel', () => {
    render(<ReportDocument report={sectioned(false)} />);
    enter('svc/b/readme.md');
    fireEvent.submit(within(panel()).getByRole('search'));
    const target = document.activeElement;
    expect(target?.textContent).toBe('결제 호출은 요청마다 정확히 한 번만 한다.');
    expect(target?.closest<HTMLDetailsElement>('#report-section-documents')?.open).toBe(true);
  });

  it('opens one stored document excerpt and keeps cross-document comparison links focused', () => {
    const report = sectioned();
    const documents = report.sections!.documents;
    documents.citations.push(
      { id: 'wide', kind: 'document', path: 'GUIDE.md', line: 1, end: 3, excerpt: { start: 1, end: 3, text: 'opening\ncanonical marker\nclosing' } },
      { id: 'narrow', kind: 'document', path: 'GUIDE.md', line: 2, end: 2, excerpt: { start: 2, end: 2, text: 'canonical marker' } },
      { id: 'config', kind: 'document', path: 'docs/config.md', line: 4, end: 4, excerpt: { start: 4, end: 4, text: 'configuration marker' } },
    );
    const first = { ...documents.comparisons[0], statement: '파일을 읽는다', status: 'unknown', documentRefs: ['wide', 'config'] };
    const second = { ...first, statement: '설정을 읽는다', documentRefs: ['narrow'] };
    documents.comparisons = [first, second];
    documents.quality = [{ aspect: 'clarity', assessment: 'weakness', statement: '실행 조건을 설명해야 한다', refs: ['narrow'] }];
    render(<ReportDocument report={report} />);
    enter('docs/config.md');
    fireEvent.submit(within(panel()).getByRole('search'));
    const documentTab = screen.getByRole('tabpanel', { name: t.sections.titles.documents });
    const heading = within(documentTab).getByRole('heading', { name: first.statement });
    expect(document.activeElement).toBe(heading);
    fireEvent.click(within(documentTab).getByRole('link', { name: 'docs/config.md' }));
    expect(document.activeElement).toBe(within(documentTab).getByRole('heading', { name: 'docs/config.md' }));
    fireEvent.click(within(documentTab).getByRole('link', { name: first.statement }));
    expect(document.activeElement).toBe(heading);
    const guide = heading.closest('.report-reading-group') as HTMLElement;
    expect(within(guide).getByRole('heading', { name: t.qualityTitle })).toBeTruthy();
    const sources = within(guide).getByText(t.documentReading.sources).closest('details')!;
    expect(sources.open).toBe(false);
    const exact = within(guide).getAllByRole('link', { name: 'GUIDE.md:2' })[0];
    fireEvent.click(exact);
    expect(sources.open).toBe(true);
    expect(document.activeElement?.id).toBe(exact.getAttribute('href')?.slice(1));
    expect(within(sources).getAllByText('canonical marker')).toHaveLength(1);
  });

  it.each([true, false])('searches related workflow evidence and focuses its comparison (includesCode=%s)', includesCode => {
    const report = sectioned();
    report.includesCode = includesCode;
    const documents = report.sections!.documents;
    const row = documents.comparisons[1];
    const refs = [
      { id: 'workflow-read', kind: 'code', path: 'archive/read_route.go', marker: 'ArchiveReadProbe' },
      { id: 'workflow-change', kind: 'code', path: 'archive/change_route.go', marker: 'ArchiveChangeProbe' },
      { id: 'workflow-test', kind: 'test', path: 'archive/route_assertions_test.go', marker: 'ArchiveTestProbe' },
    ];
    row.workflow = { read: refs[0].id, change: refs[1].id, test: refs[2].id };
    for (const { id, kind, path, marker } of refs) {
      documents.citations.push({ id, kind, path, line: 8, end: 8, excerpt: { start: 8, end: 8, text: marker } });
    }
    if (!includesCode) {
      const omitted = '원문 생략(코드 미포함 내보내기).';
      documents.comparisons[0].statement = row.statement = omitted;
    }
    render(<ReportDocument report={report} />);
    const codeReview = screen.getByRole('tab', { name: t.sections.titles.code_review });
    fireEvent.click(codeReview);
    for (const { path, marker } of refs) {
      enter(marker);
      expect(within(panel()).getByRole('status').textContent).toBe(includesCode ? s.count(1) : s.empty);
      enter(path.toUpperCase());
      expect(within(panel()).getByRole('status').textContent).toBe(s.count(1));
      expect(within(panel()).getByText(path).closest('button')?.textContent).not.toContain(marker);
    }
    fireEvent.submit(within(panel()).getByRole('search'));
    const documentTab = screen.getByRole('tabpanel', { name: t.sections.titles.documents });
    const target = within(documentTab).getByRole('heading', { name: includesCode ? row.statement : t.documentReading.withheldBehavior(2) });
    expect(target).toBeDefined();
    expect(document.activeElement).toBe(target);
  });

  it('searches code-free published paths and rules without searching discarded code, raw messages or history', () => {
    const report = parseAnalysisReport(flatExample, flatExample.run_id);
    expect(report.includesCode).toBe(false);
    const original = report.findings[0];
    original.claims = [{ assertion: 'UnpublishedClaimMarker', kind: 'observed', evidence: [] }];
    report.observations[0].message = 'RawToolMessageMarker';
    report.findings.push({ ...original, id: 'second-finding', title: '다른 진단' });
    const comparison = parseComparison(comparisonExample, comparisonExample.run_id);
    comparison.findings = [
      { findingID: original.id, fingerprint: null, state: null, publication: 'open', current: 'false_positive', match: 'none', review: null },
      { findingID: 'second-finding', fingerprint: null, state: null, publication: 'open', current: 'suppressed', match: 'none', review: null },
    ];
    comparison.differences = [{ key: 'HistoryOnlyMarker', baseline: 'secret', current: null }];
    const m = mockFetch(() => json(500, {}));
    render(<ReportDocument report={report} history={{ load: { state: 'ready', value: comparison }, api: createApi({ fetch: m.fetch }), reload: () => {} }} />);
    for (const query of ['UnpublishedClaimMarker', 'RawToolMessageMarker', 'HistoryOnlyMarker', original.id]) {
      enter(query);
      expect(within(panel()).getByRole('status').textContent).toBe(s.empty);
    }
    expect(screen.queryByText(original.title)).toBeNull();
    const filter = screen.getByRole('searchbox', { name: t.filter });
    fireEvent.change(filter, { target: { value: '제목 필터에 없는 값' } });
    enter('printf example.go');
    fireEvent.click(within(panel()).getByRole('button', { name: /go.vet printf/ }));
    const finding = screen.getByText(original.title).closest('details')!;
    expect(finding.open).toBe(true);
    expect(document.activeElement).toBe(finding.querySelector('summary'));
    expect(screen.queryByText('다른 진단')).toBeNull();
    expect((filter as HTMLInputElement).value).toBe('제목 필터에 없는 값');
    fireEvent.change(filter, { target: { value: '아직 없는 값' } });
    expect(screen.queryByText(original.title)).toBeNull();
    expect(m.calls).toHaveLength(0);
  });

  it('closes with Escape, restores focus and does not retain a previous revision query', () => {
    const report = sectioned();
    const { rerender } = render(<ReportDocument report={report} />);
    const input = enter('no-such-result');
    expect(within(panel()).getByRole('status').textContent).toBe(s.empty);
    fireEvent.keyDown(input, { key: 'Escape' });
    expect(document.activeElement).toBe(trigger());
    expect(within(panel()).queryByRole('searchbox')).toBeNull();
    rerender(<ReportDocument report={{ ...report, revision: report.revision + 1 }} />);
    fireEvent.click(trigger());
    expect((within(panel()).getByRole('searchbox') as HTMLInputElement).value).toBe('');
    expect(within(panel()).getByRole('status').textContent).toBe('');
  });
});
