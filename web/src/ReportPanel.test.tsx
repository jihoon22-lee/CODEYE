// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { parseAnalysisReport } from './analysisReport';
import { ReportDocument } from './ReportPanel';

afterEach(cleanup);

function doc() {
  return {
    schema_version: '0.1',
    demo: false,
    run_id: 'run1',
    revision: 2,
    status: 'partial',
    quality_verdict: 'inconclusive',
    inputs: { snapshot_id: 'snap1' },
    summary: {
      total_findings: 1,
      actionable_findings: 0,
      pending_candidates: 1,
      refuted_findings: 0,
      raw_observations: 0,
    },
    coverage: { ai_review: { status: 'partial' } },
    observations: [],
    tasks: [
      { key: 'review.verify', status: 'failed', reason: 'model_timeout' },
    ],
    limitations: ['모델 반증 미완료'],
    findings: [
      {
        id: 'finding1',
        title: '마지막 표본이 누락될 수 있습니다',
        category: 'correctness',
        severity: 'low',
        validation: 'inconclusive',
        disposition: 'open',
        severity_reason: '미검토 영향',
        impact: '합계가 달라짐',
        conditions: [{ statement: '입력 한 개' }],
        recommendation: { summary: '종료 조건을 확인' },
        verification: ['빈 입력과 단일 입력 확인'],
        limitations: [],
        counterevidence_ids: ['ev1'],
        claims: [
          {
            assertion: '루프가 마지막 표본 전에 종료됨',
            kind: 'inferred',
            evidence_ids: ['ev1'],
          },
        ],
        evidence: [
          {
            id: 'ev1',
            kind: 'code',
            description: '<img src="https://example.invalid/x">',
            location: { path: 'metrics.go', start_line: 2, end_line: 5 },
          },
        ],
      },
    ],
  };
}
describe('published report', () => {
  it('shows structured gaps, evidence and the pinned revision with code-free export as the default', () => {
    const r = parseAnalysisReport(doc(), 'run1');
    const { container } = render(<ReportDocument report={r} />);
    expect(screen.getByText('모델 반증 미완료')).toBeTruthy();
    expect(screen.getByText('미확정')).toBeTruthy();
    const card = screen.getByText(r.findings[0]!.title).closest('details')!;
    fireEvent.click(card.querySelector('summary')!);
    expect(card.open).toBe(true);
    expect(screen.getByText('metrics.go:2–5')).toBeTruthy();
    expect(container.querySelector('img')).toBeNull();
    const link = screen.getByRole('link', { name: 'HTML' });
    expect(link.getAttribute('href')).toContain(
      'include_code=false&revision=2',
    );
    fireEvent.click(screen.getByRole('checkbox', { name: '코드 포함' }));
    expect(link.getAttribute('href')).toContain('include_code=true&revision=2');
  });
  it('rejects another run, malformed counts, and demonstration records', () => {
    expect(() => parseAnalysisReport(doc(), 'other')).toThrow();
    const r = doc();
    r.summary.total_findings++;
    expect(() => parseAnalysisReport(r, 'run1')).toThrow();
    expect(() =>
      parseAnalysisReport({ ...doc(), demo: true }, 'run1'),
    ).toThrow();
  });
  // (docs/ANALYZERS.md) a structure metric is counted apart, marked as no defect,
  // and the summary count must match the observations that carry one.
  it('shows structure metrics apart from findings and rejects a count that does not match', () => {
    const metricDoc = () => ({
      ...doc(),
      summary: { ...doc().summary, raw_observations: 1, metric_observations: 1 },
      observations: [
        {
          id: 'obs1',
          rule_id: 'cyclomatic_complexity',
          raw_message: 'classify(...) at src/complex.py:4: NCSS 34, CCN 17',
          location_state: 'resolved',
          location: { path: 'src/complex.py', start_line: 4, end_line: 4 },
          metric: { name: 'cyclomatic_complexity', value: 17, threshold: 16, unit: 'count' },
        },
      ],
    });
    const r = parseAnalysisReport(metricDoc(), 'run1');
    expect(r.metricObservations).toBe(1);
    expect(r.counts.total).toBe(1);
    render(<ReportDocument report={r} />);
    expect(screen.getByText('그중 구조 지표(결함 아님)')).toBeTruthy();
    expect(screen.getByText('(구조 지표, 결함 아님: 17 ≥ 16)')).toBeTruthy();
    const missing = metricDoc();
    delete (missing.summary as { metric_observations?: number }).metric_observations;
    expect(() => parseAnalysisReport(missing, 'run1')).toThrow();
    const unmarked = metricDoc();
    (unmarked.observations[0] as { metric?: unknown }).metric = undefined;
    expect(() => parseAnalysisReport(unmarked, 'run1')).toThrow();
    const plain = parseAnalysisReport(doc(), 'run1');
    expect(plain.metricObservations).toBe(0);
  });
});
