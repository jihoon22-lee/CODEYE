// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import sectioned from '../../examples/report.json';
import withSlices from '../../examples/report-slices.json';
import pullRequest from '../../examples/report-pull-request.json';
import { parseAnalysisReport, type AnalysisReport, type Finding } from './analysisReport';
import { ReportDocument } from './ReportPanel';
import { actionPlan } from './reportActions';
import { readingSummary } from './reportGroups';
import { reportKo as t } from './locales/report-ko';
import { reportActionsKo as a } from './locales/report-actions-ko';

afterEach(cleanup);

type Doc = Record<string, any>;
const docs = { 'report.json': sectioned, 'report-slices.json': withSlices, 'report-pull-request.json': pullRequest };
function example(name: keyof typeof docs): Doc {
  const doc = structuredClone(docs[name]) as Doc;
  doc.demo = false;
  doc.project = { name: 'Billing gateway', source: 'fixtures/modules/input' };
  return doc;
}

// The report example with findings and comparisons to act on, like the
// saved export's scenario (internal/report/render_actions_test.go). The
// parsed report is edited: the parser checks the stored counts.
function scenario(): AnalysisReport {
  const doc = example('report.json');
  const report = parseAnalysisReport(doc, doc.run_id);
  const base = report.findings.find(f => f.id === 'review_interpret_1')!;
  base.severity = 'high';
  base.observations = [];
  base.conditions = [
    { statement: 'send가 두 번 이상 실패하는 경우', status: 'verified', evidence: [base.evidence[0].id] },
    { statement: '호출자가 반환 오류만 기록하는 경우', status: 'assumed', evidence: [] },
  ];
  base.evidence[0].excerpt = { start: 3, end: 9, text: 'func Forward() error {\n\tvar err error\n\tfor i := 0; i < MaxRetries; i++ {\n\t\terr = send()\n\t}\n\treturn err\n}' };
  base.limitations = ['호출자가 오류를 따로 기록하는지는 확인하지 못했다.'];
  const sections = report.sections!;
  const copy = (id: string, patch: Partial<Finding>) => {
    const f: Finding = { ...structuredClone(base), id, ...patch };
    report.findings.push(f);
    sections.codeReview.findings.push(id);
  };
  copy('review_second', { title: '빈 응답을 성공으로 취급한다', severity: 'medium', validation: 'reproduced' });
  copy('review_third', { title: '로그에 요청 본문이 남는다', severity: 'low' });
  copy('review_fixture', { title: '예제 입력의 검증이 없다', locations: ['fixtures/data.go'],
    evidence: [{ id: 'ev_fixture', kind: 'code', description: 'x', path: 'fixtures/data.go', lines: { start: 4, end: 6 } }], conditions: [], claims: [] });
  copy('review_pending', { title: '취소 신호를 확인하지 않을 수 있다', validation: 'inconclusive', verification: ['취소된 컨텍스트로 Forward를 호출해 반환 시간을 확인한다.'] });
  const first = sections.documents.comparisons[0];
  sections.documents.comparisons.push(
    { ...first, status: 'partial', statement: '재시도 사이에 지연을 둔다.', reason: '지연 없이 곧바로 다시 호출한다.' },
    { ...first, status: 'mismatch', statement: '마지막 오류를 호출자에게 돌려준다.', scope: 'svc/a', reason: '`Forward`는 마지막이 아닌 첫 오류를 돌려준다.' });
  return report;
}

function parsed(name: keyof typeof docs): AnalysisReport {
  const doc = example(name);
  return parseAnalysisReport(doc, doc.run_id);
}

function overviewOf(report: AnalysisReport) {
  render(<ReportDocument report={report} />);
  return { report, overview: screen.getByRole('tabpanel', { name: t.sections.overview }) };
}

describe('action-first overview', () => {
  it('opens with action cards, most severe first, carrying the fix and the check', () => {
    const { report, overview } = overviewOf(scenario());
    const cards = [...overview.querySelectorAll<HTMLElement>('.report-action-card:not(.report-mismatch-card)')];
    expect(cards.map(card => card.querySelector('h6')!.textContent)).toEqual([
      '재시도 반복에서 앞선 시도의 오류를 덮어씀', '빈 응답을 성공으로 취급한다', '로그에 요청 본문이 남는다']);
    const first = within(cards[0]);
    expect(first.getByText(t.validation.supported)).toBeTruthy();
    expect(first.getByText(a.conditions.verified)).toBeTruthy();
    expect(first.getByText(a.conditions.assumed)).toBeTruthy();
    expect(first.getByText(a.impact)).toBeTruthy();
    expect(first.getByText(a.recommendation)).toBeTruthy();
    expect(first.getByText(a.verification)).toBeTruthy();
    expect(first.getByText('호출자가 오류를 따로 기록하는지는 확인하지 못했다.')).toBeTruthy();
    expect(cards[0].textContent).toContain('func Forward() error {');
    expect(first.getByRole('button', { name: a.evidenceLink(1, 0) })).toBeTruthy();
    expect(within(cards[1]).getByText(t.validation.reproduced)).toBeTruthy();
    // The cards come before the status table.
    const table = overview.querySelector('#report-coverage')!;
    expect(cards[2].compareDocumentPosition(table) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    // The card count is the number the overview row prints.
    const row = readingSummary(report).find(x => x.section === 'code_review' && x.label === t.reading.codeOutcome)!;
    expect(row.count).toBe(String(cards.length));
  });

  it('lists document mismatches with the promise, the observed behaviour and both places, mismatches first', () => {
    const { overview } = overviewOf(scenario());
    const cards = [...overview.querySelectorAll<HTMLElement>('.report-mismatch-card')];
    expect(cards.map(card => card.querySelector('h6')!.textContent)).toEqual(['마지막 오류를 호출자에게 돌려준다.', '재시도 사이에 지연을 둔다.']);
    const first = within(cards[0]);
    expect(first.getByText(a.observed)).toBeTruthy();
    expect(first.getByText(a.documentLocation)).toBeTruthy();
    expect(first.getByText(a.codeLocation)).toBeTruthy();
    expect(first.getByText(a.interpretation)).toBeTruthy();
    expect(first.getByText('Forward', { selector: 'code' })).toBeTruthy();
  });

  it('collapses candidates, secondary-path items and tool diagnostics grouped by rule', () => {
    const { overview } = overviewOf(scenario());
    const fold = (title: string) => within(overview).getByText(title).closest('details')!;
    expect(fold(a.candidates(1)).open).toBe(false);
    expect(fold(a.secondary(1)).open).toBe(false);
    const diagnostics = fold(a.diagnosticsTriaged(2, 0, 1, 2, 1));
    expect(diagnostics.open).toBe(false);
    expect(within(diagnostics).getAllByText(a.diagnosticCount(1))).toHaveLength(2);
    expect(diagnostics.textContent).toContain('SA4006');
    expect(diagnostics.textContent).toContain('gateway.go:13');
    expect(fold(a.candidates(1)).textContent).toContain('취소된 컨텍스트로 Forward를 호출해 반환 시간을 확인한다.');
    // Neither is an action card.
    expect(overview.querySelectorAll('.report-action-card[data-finding-card="review_pending"]')).toHaveLength(0);
    expect(overview.querySelectorAll('.report-action-card[data-finding-card="review_fixture"]')).toHaveLength(0);
  });

  it('keeps the code-free export to locations and states', () => {
    const report = scenario();
    report.includesCode = false;
    const omitted = '(코드 미포함: 생략)';
    for (const f of report.findings) {
      f.title = `${f.category} 항목 (${f.locations[0]})`;
      f.impact = f.recommendation = f.severityReason = omitted;
      f.verification = f.limitations = [];
      f.conditions = f.claims = [];
      for (const e of f.evidence) delete e.excerpt;
    }
    for (const c of report.sections!.documents.comparisons) c.statement = c.reason = omitted;
    const { overview } = overviewOf(report);
    expect(overview.textContent).not.toContain('func Forward');
    expect(overview.textContent).toContain(a.leadCodeFree);
    expect(overview.querySelectorAll('.report-action-card:not(.report-mismatch-card)').length).toBeGreaterThan(0);
    expect(within(overview).getAllByText(t.validation.supported).length).toBeGreaterThan(0);
    const opening = overview.querySelector('.report-actions')!;
    expect(opening.textContent).not.toContain(omitted);
  });

  it('says plainly what an empty list means for an accepted review, a missing review and a failed run', () => {
    const accepted = overviewOf(parsed('report-slices.json')).overview;
    expect(within(accepted).getByText(t.codeReading.none)).toBeTruthy();
    expect(within(accepted).getByText(a.noneNotAProof)).toBeTruthy();
    expect(within(accepted).queryByText(a.notAssessed)).toBeNull();
    cleanup();
    const missing = overviewOf(parsed('report-pull-request.json')).overview;
    expect(within(missing).getByText(a.notAssessed)).toBeTruthy();
    expect(within(missing).queryByText(t.codeReading.none)).toBeNull();
    cleanup();
    const stopped = parsed('report-slices.json');
    stopped.status = 'failed';
    const failed = overviewOf(stopped).overview;
    expect(within(failed).getByText(a.notAssessed)).toBeTruthy();
    expect(within(failed).queryByText(t.codeReading.none)).toBeNull();
  });

  it('shows what was not analysed under the verdict and links to the status table', () => {
    const { overview } = overviewOf(scenario());
    const note = overview.querySelector('.report-coverage-note')!;
    expect(note.textContent).toContain(a.coverageLead);
    expect(note.textContent).toContain('분석하지 못했거나 일부만 분석한 범위가 7건 있습니다.');
    const first = overview.querySelector('.report-action-card')!;
    expect(note.compareDocumentPosition(first) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    const table = overview.querySelector<HTMLElement>('#report-coverage')!;
    fireEvent.click(within(note as HTMLElement).getByRole('button', { name: a.coverageLink }));
    expect(document.activeElement).toBe(table);
  });

  it('plans the same findings the overview row counts', () => {
    const report = scenario();
    const plan = actionPlan(report, report.sections!);
    expect(plan.actions.map(f => f.id)).toEqual(['review_interpret_1', 'review_second', 'review_third']);
    expect(plan.secondary.map(f => f.id)).toEqual(['review_fixture']);
    expect(plan.candidates.map(f => f.id).sort()).toEqual(['review_pending'].sort());
    expect(plan.assessed).toBe(true);
    expect(plan.mismatches.map(m => m.comparison.status)).toEqual(['mismatch', 'partial']);
  });
  it('lists the confirmed secondary findings before an unconfirmed more severe one', () => {
    const report = scenario();
    const fixture = report.findings.find(f => f.id === 'review_fixture')!;
    fixture.severity = 'low';
    report.findings.push({ ...structuredClone(fixture), id: 'review_fixture_pending', severity: 'critical', validation: 'unreviewed', observations: [] });
    report.sections!.codeReview.findings.push('review_fixture_pending');
    expect(actionPlan(report, report.sections!).secondary.map(f => f.id)).toEqual(['review_fixture', 'review_fixture_pending']);
  });
  it('opens the evidence of a card in the code analysis tab and names the document comparison', () => {
    const { overview } = overviewOf(scenario());
    fireEvent.click(within(overview.querySelector<HTMLElement>('[data-finding-card="review_second"]')!).getByRole('button', { name: a.evidenceLink(1, 0) }));
    const tab = screen.getByRole('tab', { name: t.sections.titles.code_review });
    expect(tab.getAttribute('aria-selected')).toBe('true');
    const panel = screen.getByRole('tabpanel', { name: t.sections.titles.code_review });
    expect(panel.querySelector('.report-finding-heading')?.textContent).toBe('빈 응답을 성공으로 취급한다');
    cleanup();
    const again = overviewOf(scenario()).overview;
    fireEvent.click(within(again.querySelector<HTMLElement>('.report-mismatch-card')!).getByRole('button', { name: a.openComparison }));
    expect(screen.getByRole('tab', { name: t.sections.titles.documents }).getAttribute('aria-selected')).toBe('true');
  });

  it('splits the diagnostics by triage result with the default severity, the reason and what was not triaged', () => {
    const { overview } = overviewOf(parsed('report.json'));
    const diagnostics = within(overview).getByText(a.diagnosticsTriaged(2, 0, 1, 2, 1)).closest('details')!;
    expect(diagnostics.textContent).toContain(a.diagnosticsTriageNote);
    const buckets = [...diagnostics.querySelectorAll<HTMLElement>('.report-diag-bucket')];
    expect(buckets.map(b => b.dataset.bucket)).toEqual(['attention', 'not_triaged']);
    const attention = within(buckets[0]);
    expect(attention.getByText(a.diagnosticBucket.attention(1))).toBeTruthy();
    expect(buckets[0].textContent).toContain('SA6005');
    expect(attention.getByText(a.triageClassification.unclear)).toBeTruthy();
    expect(buckets[0].textContent).toContain('호출자 코드가 입력에 없어 진단의 영향을 판단할 수 없다.');
    expect(attention.getByText(a.defaultSeverity(t.severity.low))).toBeTruthy();
    // The diagnostic the interpret role covered is not triaged, and the fold says so.
    expect(buckets[1].textContent).toContain('SA4006');
    expect(buckets[1].textContent).toContain(a.diagnosticBucketNote.not_triaged + ' ' + a.diagnosticInterpreted(1));
  });

  it('keeps a harmless group with its reason and cited code, and a refuted problem with a link to its finding', () => {
    const report = parsed('report.json');
    const triage = report.sections!.codeReview.triage!;
    const g = triage.groups[0];
    Object.assign(g, { classification: 'harmless', bucket: 'harmless', reason: '앞선 검사가 이 경로를 막는다.', locations: [{ path: 'svc/a/gateway.go', start: 3, end: 9 }] });
    let { overview } = overviewOf(report);
    let bucket = overview.querySelector<HTMLElement>('.report-diag-bucket[data-bucket="harmless"]')!;
    expect(within(bucket).getByText(a.diagnosticBucket.harmless(1))).toBeTruthy();
    expect(within(bucket).getByText(a.triageClassification.harmless)).toBeTruthy();
    expect(bucket.textContent).toContain('앞선 검사가 이 경로를 막는다.');
    expect(bucket.textContent).toContain('svc/a/gateway.go');
    cleanup();
    Object.assign(g, { classification: 'problem', bucket: 'refuted', reason: null, locations: [], finding: 'review_investigate_1' });
    ({ overview } = overviewOf(report));
    bucket = overview.querySelector<HTMLElement>('.report-diag-bucket[data-bucket="refuted"]')!;
    expect(within(bucket).getByText(a.diagnosticBucket.refuted(1))).toBeTruthy();
    expect(within(bucket).getByText(a.triageRefuted)).toBeTruthy();
    fireEvent.click(within(bucket).getByRole('button', { name: a.openFinding }));
    expect(screen.getByRole('tab', { name: t.sections.titles.code_review }).getAttribute('aria-selected')).toBe('true');
  });

  it('renders an older revision without triage as one list and rejects a triage it cannot place', () => {
    const doc = example('report.json');
    delete doc.code_review.diagnostic_triage;
    doc.tasks = doc.tasks.filter((x: Doc) => !/^review\.(triage|verify)\.t/.test(x.key));
    const older = parseAnalysisReport(doc, doc.run_id);
    expect(older.sections!.codeReview.triage).toBeNull();
    const { overview } = overviewOf(older);
    const diagnostics = within(overview).getByText(a.diagnostics(2, 2, 1)).closest('details')!;
    expect(diagnostics.querySelectorAll('.report-diag-bucket')).toHaveLength(0);
    const broken = (edit: (d: Doc) => void) => {
      const d = example('report.json');
      edit(d);
      return () => parseAnalysisReport(d, d.run_id);
    };
    expect(broken(d => { d.code_review.diagnostic_triage.groups[0].observation_ids = ['obs_unknown']; })).toThrow();
    expect(broken(d => { d.code_review.diagnostic_triage.groups[0].bucket = 'harmless'; })).toThrow();
    expect(broken(d => { d.code_review.diagnostic_triage.triaged = 2; })).toThrow();
  });

  it('says a partial review that found nothing may have missed what it did not read', () => {
    const report = parsed('report-slices.json');
    report.aiReview = 'partial';
    const { overview } = overviewOf(report);
    expect(within(overview).getByText(a.nonePartial)).toBeTruthy();
    expect(within(overview).getByText(a.noneNotAProof)).toBeTruthy();
  });
});
