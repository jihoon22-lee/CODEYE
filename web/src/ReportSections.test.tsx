// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import sectioned from '../../examples/report.json';
import flat from '../../examples/report-go-vet.json';
import withSlices from '../../examples/report-slices.json';
import unscreened from '../../internal/report/testdata/report-unscreened.json';
import screenUnusable from '../../internal/report/testdata/report-screen-unusable.json';
import { parseAnalysisReport, sourceEvidenceURL } from './analysisReport';
import { ReportDocument } from './ReportPanel';
import { reportKo as t } from './locales/report-ko';

afterEach(cleanup);

function openDisclosure(summary: HTMLElement) {
  const details = summary.closest('details')!;
  details.open = true;
  fireEvent(details, new Event('toggle'));
  return details;
}

// examples/report.json is the builder's output for fixtures/modules/input
// (internal/worker/review/report_example_test.go), stored with demo: true;
// examples/report-go-vet.json is a revision published before the sections.
function example(name: 'report.json' | 'report-go-vet.json' | 'report-slices.json' | 'report-unscreened.json' | 'report-screen-unusable.json'): Record<string, any> {
  const docs = { 'report.json': sectioned, 'report-go-vet.json': flat, 'report-slices.json': withSlices, 'report-unscreened.json': unscreened, 'report-screen-unusable.json': screenUnusable };
  const doc = structuredClone(docs[name]) as Record<string, any>;
  doc.demo = false;
  // Exercise the archived disclosure layout; new publication metadata enables tabs.
  delete doc.project;
  return doc;
}

describe('analysis sections', () => {
  it('shows contextual environment and document sources without raw evidence identifiers', () => {
    const doc = example('report.json');
    const commit = 'a'.repeat(40);
    doc.project = { name: 'Billing gateway', source: 'https://github.com/example/billing.git' };
    doc.inputs.source_kind = 'git';
    doc.inputs.commit_sha = commit;
    render(<ReportDocument report={parseAnalysisReport(doc, doc.run_id)} />);
    for (const key of ['environment', 'documents'] as const) {
      const name = t.sections.titles[key];
      fireEvent.click(screen.getByRole('tab', { name }));
      const panel = screen.getByRole('tabpanel', { name });
      for (const citation of doc[key].evidence) {
        expect(panel.textContent).not.toContain(citation.id);
        expect(panel.textContent).not.toContain(citation.blob_sha256.slice(0, 12));
      }
    }
    fireEvent.click(screen.getByRole('tab', { name: t.sections.titles.environment }));
    const environment = screen.getByRole('tabpanel', { name: t.sections.titles.environment });
    const evidence = doc.environment.evidence[0];
    const place = `${evidence.path}:${evidence.start_line}`;
    const control = within(environment).getByLabelText(t.sourceActionAt(t.viewManifest, place));
    const excerpt = openDisclosure(control);
    const source = within(excerpt).getByRole('link', { name: t.sourceActionAt(t.openSource, place) });
    expect(source.getAttribute('href')).toBe(`https://github.com/example/billing/blob/${commit}/${evidence.path}#L${evidence.start_line}-L${evidence.end_line}`);
  });

  it('keeps the unselected review reason in code-review details', () => {
    const doc = example('report.json');
    doc.project = { name: 'Billing gateway' };
    const text = '이 단계에서 검토할 대상이 선택되지 않았습니다.';
    render(<ReportDocument report={parseAnalysisReport(doc, doc.run_id)} />);
    const overview = screen.getByRole('tabpanel', { name: t.sections.overview });
    expect(within(overview.querySelector<HTMLElement>('.report-executive')!).queryByText(text, { exact: false })).toBeNull();
    const name = t.sections.titles.code_review;
    fireEvent.click(screen.getByRole('tab', { name }));
    expect(within(screen.getByRole('tabpanel', { name })).getByText(text, { exact: false })).toBeTruthy();
  });

  it('keeps Python API citations at their source lines and retains the Markdown page', () => {
    const doc = example('report.json');
    const evidence = doc.documents.evidence.find((item: Record<string, unknown>) => item.kind === 'document');
    evidence.path = 'src/example/api.py';
    evidence.directive_path = 'docs/api.md';
    const parsed = parseAnalysisReport(doc, doc.run_id);
    expect(parsed.sections?.documents.citations.find(item => item.id === evidence.id)).toMatchObject({
      kind: 'document', path: 'src/example/api.py', directivePath: 'docs/api.md', line: evidence.start_line,
    });
    evidence.directive_path = 'docs/api.rst';
    expect(() => parseAnalysisReport(doc, doc.run_id)).toThrow();
  });

  it('shows run identity and navigates the published report tabs by keyboard', () => {
    const doc = example('report.json');
    doc.project = {name:'Billing',source:'/srv/billing',started_at:'2026-01-01T10:00:00Z',finished_at:'2026-01-01T10:02:00Z'};
    const report = parseAnalysisReport(doc, doc.run_id);
    render(<ReportDocument report={report} />);
    const hero = screen.getByRole('heading', { name: 'Billing' }).closest('header')!;
    expect(within(hero).getByText('/srv/billing')).toBeTruthy();
    expect(report.status).toBe('partial');
    expect(within(hero).getByText('일부 검사 미완료로 종료')).toBeTruthy();
    const time = (label: string) => within(hero).getByText(label, { exact: true }).parentElement?.querySelector('time');
    expect(time(t.start)?.dateTime).toBe(doc.project.started_at);
    expect(time(t.finish)?.dateTime).toBe(doc.project.finished_at);
    expect(within(hero).getByText('2분 0초')).toBeTruthy();
    expect(within(hero).queryByText(t.commit, { exact: true })).toBeNull();
    expect(within(hero).queryByText('분석 종료', {exact: true})).toBeNull();
    const input = screen.getByText(t.reading.provenance).closest('details')!;
    expect(input.open).toBe(false);
    expect(within(input).getByText('/srv/billing')).toBeTruthy();
    expect(within(input).getByText('시작 시각')).toBeTruthy();
    expect(within(input).getByText('종료 시각')).toBeTruthy();
    const tab = screen.getByRole('tab',{name:t.sections.overview});
    expect(tab.getAttribute('aria-selected')).toBe('true');
    const order = ['environment', 'documents', 'static_analysis', 'metrics', 'secrets', 'code_review', 'gaps'];
    let previous = tab;
    for (const key of order) {
      fireEvent.keyDown(previous, { key: 'ArrowRight' });
      const current = screen.getByRole('tab', { name: t.sections.titles[key] });
      expect(current.getAttribute('aria-selected')).toBe('true');
      expect(current.getAttribute('aria-controls')).toBe('report-section-' + key);
      expect(document.activeElement).toBe(current);
      expect(screen.getByRole('tabpanel', { name: t.sections.titles[key] })).toBeTruthy();
      previous = current;
    }
    fireEvent.keyDown(previous, { key: 'Home' });
    expect(document.activeElement).toBe(tab);
  });

  it('summarizes one supported finding with its impact and next action before detailed findings', () => {
    const doc = example('report.json');
    doc.project = { name: 'Billing', source: '/srv/billing' };
    const report = parseAnalysisReport(doc, doc.run_id);
    report.sections!.documents.comparisons = [];
    const finding = report.findings.find(f => f.validation === 'supported')!;
    const { rerender } = render(<ReportDocument report={report} />);

    const overview = screen.getByRole('tabpanel', { name: t.sections.overview });
    const judgment = within(overview).getByRole('region', { name: finding.title });
    expect(within(judgment).getByText('코드 검토로 확인')).toBeTruthy();
    expect(within(judgment).getByText(finding.impact)).toBeTruthy();
    expect(judgment.textContent).toContain(finding.recommendation);
    expect(overview.querySelectorAll('.report-executive')).toHaveLength(1);
    expect(overview.querySelector('table')).toBeNull();
    expect(overview.querySelector('.model-reading-list')).toBeNull();
    expect(within(overview).queryByText(t.counts.total)).toBeNull();
    finding.validation = 'reproduced';
    rerender(<ReportDocument report={report} />);
    expect(within(judgment).getByText('실행으로 재현')).toBeTruthy();
  });

  it('offers one collapsed glossary using only the rendered decision and its citation', () => {
    const doc = example('report.json');
    doc.project = { name: 'Billing' };
    const report = parseAnalysisReport(doc, doc.run_id);
    report.sections!.documents.comparisons = [];
    const finding = report.findings.find(f => f.validation === 'supported')!;
    finding.title = '대기 중인 promise가 완료되지 않음';
    finding.impact = '잘못된 JSON 요청에 파싱 오류를 포함한 500 응답을 반환합니다.';
    finding.recommendation = 'JSON 파싱 실패를 400으로 처리합니다.';
    const { rerender } = render(<ReportDocument report={report} />);
    const overview = screen.getByRole('tabpanel', { name: t.sections.overview });
    const toggle = within(overview).getByText(t.reading.glossary);
    const glossary = toggle.closest('details')!;
    expect(glossary.open).toBe(false);
    expect(Array.from(glossary.querySelectorAll('dt'), term => term.textContent)).toEqual([
      'Promise', 'JSON 파싱', 'HTTP 400', 'HTTP 500', '파일과 줄 번호',
    ]);
    finding.title = '작업 400개를 대기열에 보관한다.';
    finding.impact = '작업을 500회 실행해 PromiseQueue의 대기 시간을 비교합니다.';
    finding.recommendation = 'JSON 파일의 크기를 5000바이트로 제한하고 요청 수를 400으로 처리합니다.';
    rerender(<ReportDocument report={report} />);
    expect(Array.from(glossary.querySelectorAll('dt'), term => term.textContent)).toEqual(['파일과 줄 번호']);
  });

  it('states an actual known reason when the overview lacks evidence', () => {
    const doc = example('report.json');
    doc.project = { name: 'Billing' };
    const report = parseAnalysisReport(doc, doc.run_id);
    const sections = report.sections!;
    sections.documents.comparisons = [];
    sections.codeReview.findings = [];
    sections.codeReview.strengths = [];
    report.observations = [];
    const { rerender } = render(<ReportDocument report={{ ...report, aiReview: 'not_performed', aiReviewReasons: ['model_source_not_approved'] }} />);
    const overview = screen.getByRole('tabpanel', { name: t.sections.overview });
    expect(within(overview).getByRole('heading', { name: t.reading.noFindings })).toBeTruthy();
    expect(within(overview).getByText(t.reasons.model_source_not_approved)).toBeTruthy();
    expect(within(overview).queryByText(t.reading.glossary)).toBeNull();
    rerender(<ReportDocument report={{ ...report, aiReviewReasons: ['unrecognized:HTTP 400'] }} />);
    expect(within(overview).queryByText(/HTTP 400/)).toBeNull();
    expect(within(overview).queryByText(t.reading.glossary)).toBeNull();
  });

  it('shows a brief document comparison and preserves distinct accessible source actions', () => {
    const doc = example('report.json');
    doc.project = { name: 'Billing' };
    const report = parseAnalysisReport(doc, doc.run_id);
    const sections = report.sections!;
    const comparison = sections.documents.comparisons.find(c => c.status === 'match')!;
    comparison.scope = '.';
    comparison.codeRefs.push(comparison.codeRefs[0]);
    sections.codeReview.findings = [];
    render(<ReportDocument report={report} />);

    const overview = screen.getByRole('tabpanel', { name: t.sections.overview });
    const judgment = within(overview).getByRole('region', { name: comparison.statement });
    expect(within(judgment).getByText(t.reading.comparisonStatus.match)).toBeTruthy();
    expect(within(judgment).getByText(comparison.reason)).toBeTruthy();
    expect(within(judgment).getAllByText(t.viewDocument, { selector: 'summary' })).toHaveLength(1);
    expect(within(judgment).getAllByText(t.viewCode, { selector: 'summary' })).toHaveLength(1);
    const excerpt = within(judgment).getByText(t.viewDocument);
    const citation = sections.documents.citations.find(c => comparison.documentRefs.includes(c.id))!;
    expect(excerpt.closest('details')!.open).toBe(false);
    expect(openDisclosure(excerpt).querySelector('pre')?.textContent).toContain(citation.excerpt!.text.split('\n')[0]);
    expect(within(judgment).queryByRole('link')).toBeNull();
    expect(within(overview).queryByText(t.scopeTitle)).toBeNull();
    expect(within(overview).queryByText(sections.codeReview.strengths[0].title)).toBeNull();

    fireEvent.click(screen.getByRole('tab', { name: t.sections.titles.documents }));
    const panel = screen.getByRole('tabpanel', { name: t.sections.titles.documents });
    expect(within(panel).getByText(comparison.statement)).toBeTruthy();
    expect(within(panel).queryByText('구현 근거', { exact: true })).toBeNull();
    expect(Array.from(panel.querySelectorAll('.report-card-meta code')).every(scope => scope.textContent !== '.')).toBe(true);
  });

  it('shows one published follow-up result and one independent high priority action', () => {
    const doc = example('report.json');
    doc.builder.version = '12';
    doc.project = { name: 'Billing', source: '/srv/billing' };
    const key = 'review.contract';
    const forOtherTask = (row: { key: string }) => row.key !== key;
    const role = { key, result: 'accepted', task_status: 'succeeded' };
    doc.tasks = [...doc.tasks.filter(forOtherTask), { key, status: 'succeeded', required_for_verdict: false, reason: null }];
    doc.coverage.ai_review.optional_tasks = [...doc.coverage.ai_review.optional_tasks.filter(forOtherTask), role];
    doc.code_review.roles = [...doc.code_review.roles.filter(forOtherTask), role];
    doc.coverage.review_calls.push({ ...doc.coverage.review_calls[0], task_key: key, role: 'contract' });
    const row = doc.documents.comparisons.find((r: { status: string }) => r.status === 'match')!;
    const action = doc.findings.find((f: { validation: string }) => f.validation === 'supported')!;
    row.followup = true;
    action.severity = 'high';
    const view = () => <ReportDocument report={parseAnalysisReport(doc, doc.run_id)} />;
    const { rerender } = render(view());
    const judgment = within(screen.getByRole('region', { name: row.statement }));
    expect(judgment.getByText(t.reading.followedContract)).toBeTruthy();
    expect(judgment.getByText(row.reason)).toBeTruthy();
    expect(judgment.getByText(t.reading.priorityAction).parentElement?.textContent).toContain(action.title);
    expect(judgment.queryByRole('heading', { name: action.title })).toBeNull();

    const rejected = structuredClone(doc);
    rejected.coverage.ai_review.optional_tasks = [];
    expect(() => parseAnalysisReport(rejected, rejected.run_id)).toThrow();
    const invalidOutcome = structuredClone(doc);
    invalidOutcome.documents.comparisons[0].status = 'unknown';
    expect(() => parseAnalysisReport(invalidOutcome, invalidOutcome.run_id)).toThrow();

    action.severity = 'low';
    rerender(view());
    const currentDecision = screen.getByRole('tabpanel', { name: t.sections.overview }).querySelector<HTMLElement>('.report-executive')!;
    expect(within(currentDecision).queryByText(t.reading.priorityAction)).toBeNull();
    action.severity = 'high';

    const modelText = row.reason;
    doc.export.include_code = false;
    const omitted = '원문 생략(코드 미포함 내보내기).';
    for (const finding of doc.findings) for (const e of finding.evidence) delete e.excerpt;
    for (const section of [doc.environment, doc.documents, doc.code_review]) for (const e of section.evidence) delete e.excerpt;
    for (const r of doc.documents.comparisons) r.reason = r.statement = omitted;
    for (const contract of doc.documents.contracts) contract.statement = omitted;
    rerender(view());
    const projected = within(screen.getByRole('region', { name: t.reading.omitted }));
    expect(projected.getByText(t.reading.comparisonStatus.match)).toBeTruthy();
    expect(projected.getByText(t.reading.followedContract)).toBeTruthy();
    expect(projected.getByText(t.reading.priorityAction)).toBeTruthy();
    expect(projected.queryByText(modelText)).toBeNull();
    expect(projected.queryByText(action.title)).toBeNull();
    expect(projected.queryByRole('link')).toBeNull();
    expect(projected.getAllByText(/gateway\.go:/).length).toBeGreaterThan(0);
  });

  it('keeps a scoped new source observation with an unresolved comparison', () => {
    const doc = example('report.json');
    doc.builder.version = '13';
    doc.project = { name: 'Callbacks' };
    const key = 'review.contract';
    const keep = (entry: {key: string}) => entry.key !== key;
    const role = { key, result: 'accepted', task_status: 'succeeded' };
    doc.tasks = [...doc.tasks.filter(keep), { key, status: 'succeeded', required_for_verdict: false }];
    doc.coverage.ai_review.optional_tasks = [...doc.coverage.ai_review.optional_tasks.filter(keep), role];
    doc.code_review.roles = [...doc.code_review.roles.filter(keep), role];
    doc.coverage.review_calls.push({ ...doc.coverage.review_calls[0], task_key: key, role: 'contract' });
    for (const comparison of doc.documents.comparisons) comparison.status = 'unknown';
    const row = doc.documents.comparisons[0];
    const priorReason = row.reason;
    const newReason = '유효한 항목에서 콜백 호출은 보이지만 공개 진입점과의 연결은 확인되지 않았습니다.';
    const priorEvidence = doc.documents.evidence.find((e: {id: string}) => row.code_evidence_ids.includes(e.id))!;
    const priorLocation = `${priorEvidence.path}:${priorEvidence.start_line}–${priorEvidence.end_line}`;
    const newEvidence = { id: 'extra-evidence', path: priorEvidence.path, start_line: 21, end_line: 22, kind: 'code',
      excerpt: { start_line: 21, end_line: 22, text: 'if valid {\n  callback(value)' } };
    const newLocation = `${newEvidence.path}:21–22`;
    doc.documents.evidence.push(newEvidence);
    const refs = [newEvidence.id, priorEvidence.id];
    row.additional_observation = { reason: newReason, code_evidence_ids: refs };
    const parse = () => {
      const report = parseAnalysisReport(doc, doc.run_id);
      report.sections!.codeReview.findings = [];
      report.sections!.codeReview.strengths = [];
      return report;
    };
    const { rerender } = render(<ReportDocument report={parse()} />);
    const opening = within(screen.getByRole('region', { name: t.reading.documentComparison }));
    expect(opening.getByText(t.reading.comparisonStatus.unknown)).toBeTruthy();
    const codeRow = opening.getByText(t.reading.comparisonCode, {exact:true}).closest('div')!;
    expect(within(codeRow).getByText(newReason)).toBeTruthy();
    expect(Array.from(codeRow.querySelectorAll('summary'), el => el.getAttribute('aria-label'))).toEqual([
      t.sourceActionAt(t.viewCode, newLocation), t.sourceActionAt(t.viewCode, priorLocation),
    ]);
    expect(opening.queryByText(priorReason)).toBeNull();
    expect(opening.queryByText(t.reading.followedContract)).toBeNull();
    expect(opening.getByText(t.reading.comparisonCount.unknown).nextElementSibling?.textContent).toBe(t.reading.comparisonItems(doc.documents.comparisons.length));
    fireEvent.click(opening.getByRole('button', { name: t.reading.openDocumentAnalysis }));
    const detail = within(screen.getByRole('tabpanel', { name: t.sections.titles.documents }));
    expect(detail.getByText(priorReason)).toBeTruthy();
    expect(detail.getByText(newReason, {exact: false})).toBeTruthy();
    const newCode = detail.getByLabelText(t.sourceActionAt(t.viewCode, newLocation));
    const activeComparison = document.activeElement?.closest('li');
    expect(activeComparison).toBeTruthy();
    expect(within(activeComparison!).getAllByLabelText(t.sourceActionAt(t.viewCode, priorLocation))).toHaveLength(1);
    expect(openDisclosure(newCode).querySelector('pre')?.textContent).toContain('callback(value)');

    row.additional_observation.code_evidence_ids = row.code_evidence_ids;
    expect(parse).toThrow();
    row.additional_observation.code_evidence_ids = refs;
    newEvidence.kind = 'test';
    expect(parse).toThrow();
    newEvidence.kind = 'code';
    doc.export.include_code = false;
    const omitted = '원문 생략(코드 미포함 내보내기).';
    for (const finding of doc.findings) for (const e of finding.evidence) delete e.excerpt;
    for (const section of [doc.environment, doc.documents, doc.code_review]) for (const e of section.evidence) delete e.excerpt;
    for (const c of doc.documents.comparisons) c.reason = c.statement = omitted;
    for (const contract of doc.documents.contracts) contract.statement = omitted;
    expect(parse).toThrow();
    row.additional_observation.reason = omitted;
    rerender(<ReportDocument report={parse()} />);
    fireEvent.click(screen.getByRole('tab', { name: t.sections.overview }));
    const projected = within(screen.getByRole('region', { name: t.reading.omitted }));
    expect(projected.queryByText(t.reading.comparisonCode)).toBeNull();
    expect(projected.getByText(newLocation)).toBeTruthy();
    expect(projected.getByText(t.reading.comparisonStatus.unknown)).toBeTruthy();
    expect(projected.queryByText(newReason, {exact: false})).toBeNull();
    delete row.additional_observation;
    expect(parse().sections?.documents.comparisons[0].additional).toBeUndefined();
  });

  it('shows one representative document claim as unverified when it is the only anchored result', () => {
    const doc = example('report.json');
    doc.project = { name: 'Documentation' };
    const report = parseAnalysisReport(doc, doc.run_id);
    const sections = report.sections!;
    const comparison = sections.documents.comparisons.find(row => row.documentRefs.length > 0)!;
    sections.documents.comparisons = [{ ...comparison, status: 'unknown', codeRefs: [] },
      { ...comparison, status: 'unknown', codeRefs: [], statement: '별도의 동작' }];
    sections.codeReview.findings = [];
    sections.codeReview.strengths = [];
    report.observations = [];
    render(<ReportDocument report={report} />);

    const overview = screen.getByRole('tabpanel', { name: t.sections.overview });
    const judgment = within(overview).getByRole('region', { name: t.reading.documentComparison });
    expect(within(judgment).getByText(t.reading.codeEvidenceMissing)).toBeTruthy();
    expect(within(judgment).getByText(comparison.statement)).toBeTruthy();
    expect(within(judgment).getByText(t.reading.comparisonDocument)).toBeTruthy();
    expect(within(judgment).getByText(t.reading.comparisonCode, {exact:true})).toBeTruthy();
    expect(within(judgment).queryByText(t.modelReading)).toBeNull();
    expect(within(judgment).getByText(t.viewDocument)).toBeTruthy();
    expect(within(judgment).queryByText(t.viewCode)).toBeNull();
    expect(within(overview).queryByText('별도의 동작')).toBeNull();
  });

  it('breaks an unknown tie with one question sharing the subject, scope and exact document citation', () => {
    const doc = example('report.json');
    doc.project = { name: 'Queue' };
    const report = parseAnalysisReport(doc, doc.run_id);
    const { documents, codeReview } = report.sections!;
    codeReview.findings = [];
    codeReview.strengths = [];
    const base = documents.comparisons.find(row => row.documentRefs.length > 0)!;
    const shared = base.documentRefs[0];
    const row = (statement: string) => ({ ...base, statement, scope: '.', status: 'unknown', codeRefs: [], followup: undefined });
    const first = row('지원 범위와 실행 환경을 설명한다.');
    const queue = row('대기열 항목은 연결을 끊어도 보존된다.');
    const restore = row('복원 기록을 읽을 수 있다.');
    documents.comparisons = [first, queue, restore];
    const question = { question: '대기열 보존 조건을 확인한다.', appliesTo: '.', evidence: [shared] };
    documents.questions = [{ ...question, appliesTo: 'other' }, { ...question, evidence: ['wrong-citation'] }, question,
      { ...question, question: '복원 기록을 확인한다.' }];

    const { rerender } = render(<ReportDocument report={report} />);
    const judgment = () => within(screen.getByRole('region', { name: t.reading.documentComparison }));
    expect(judgment().getByText(queue.statement)).toBeTruthy();
    expect(judgment().queryByText(first.statement)).toBeNull();
    expect(judgment().getByText(t.reading.codeEvidenceMissing)).toBeTruthy();
    fireEvent.click(judgment().getByRole('button', { name: t.reading.openDocumentAnalysis }));
    const selected = screen.getByRole('heading', { name: queue.statement });
    expect(document.activeElement).toBe(selected);
    expect(within(selected.closest('li')!).getByText(t.reading.comparisonStatus.unknown)).toBeTruthy();

    fireEvent.click(screen.getByRole('tab', { name: t.sections.overview }));
    documents.questions = [{ ...question, question: '대기열 또는 복원 동작을 확인한다.' }, { ...question, question: '지원 범위 확인' }];
    rerender(<ReportDocument report={report} />);
    expect(judgment().getByText(first.statement)).toBeTruthy();
    documents.questions = [];
    rerender(<ReportDocument report={report} />);
    expect(judgment().getByText(first.statement)).toBeTruthy();

    const omitted = '원문 생략(코드 미포함 내보내기).';
    documents.questions = [{ ...question, question: omitted }];
    documents.comparisons = [first, queue, restore].map(row => ({ ...row, statement: omitted, reason: omitted }));
    rerender(<ReportDocument report={{ ...report, includesCode: false }} />);
    const projected = within(screen.getByRole('region', { name: t.reading.omitted }));
    expect(projected.getByText(t.reading.comparisonStatus.unknown)).toBeTruthy();
    expect(projected.queryByText(queue.statement)).toBeNull();
    fireEvent.click(projected.getByRole('button', { name: t.reading.openDocumentAnalysis }));
    expect(document.activeElement).toBe(screen.getAllByRole('heading', { name: omitted })[0]);
  });

  it('adds a matched option for the same named behavior and exact code range', () => {
    const doc = example('report.json');
    doc.project = { name: 'Queue', source: 'https://github.com/owner/queue.git' };
    doc.inputs.source_kind = 'git';
    doc.inputs.commit_sha = 'a'.repeat(40);
    const report = parseAnalysisReport(doc, doc.run_id);
    const { documents, codeReview } = report.sections!;
    codeReview.findings = [];
    const match = documents.comparisons.find(row => row.status === 'match')!;
    const lead = { ...match, statement: 'limit.clearQueue()는 대기 중인 작업을 버린다.' };
    const peer = { ...match, statement: 'rejectOnClear가 활성화된 경우 clearQueue()는 대기 중인 promise를 AbortError로 거부한다.' };
    const wrongOperation = { ...peer, statement: 'rejectOnClear가 활성화된 경우 dispose()는 모든 작업을 버린다.' };
    const code = documents.citations.find(c => match.codeRefs.includes(c.id))!;
    documents.citations.push({ ...code, id: 'another-range', end: code.end + 1 });
    const wrongRange = { ...peer, codeRefs: ['another-range'], statement: 'rejectOnClear가 활성화된 경우 clearQueue()는 다른 동작을 한다.' };
    documents.comparisons = [lead, wrongOperation, wrongRange, peer];
    render(<ReportDocument report={report} />);

    const judgment = screen.getByRole('region', { name: lead.statement });
    expect(within(judgment).getByText(t.reading.option)).toBeTruthy();
    expect(within(judgment).getByText(peer.statement)).toBeTruthy();
    expect(within(judgment).queryByText(wrongOperation.statement)).toBeNull();
    expect(within(judgment).queryByText(wrongRange.statement)).toBeNull();
    expect(within(judgment).getAllByText(t.viewCode, { selector: 'summary' })).toHaveLength(1);
  });

  it('keeps one code-cited unknown explicitly unconfirmed', () => {
    const doc = example('report.json');
    doc.project = { name: 'Billing' };
    const report = parseAnalysisReport(doc, doc.run_id);
    const { documents, codeReview } = report.sections!;
    const known = documents.comparisons.find(c => c.status === 'match')!;
    const unanchored = documents.comparisons.find(c => c.status === 'unknown')!;
    const uncertain = { ...known, status: 'unknown', reason: '최대 반복 횟수는 보이지만 전체 호출 경로는 확인되지 않았습니다.' };
    documents.comparisons = [unanchored, uncertain, { ...uncertain, statement: '다른 계약' }];
    codeReview.findings = [];
    codeReview.strengths = [];
    const { rerender } = render(<ReportDocument report={report} />);
    const overview = () => screen.getByRole('tabpanel', { name: t.sections.overview }).querySelector<HTMLElement>('.report-executive')!;
    expect(within(overview()).getByText(t.reading.codeEvidenceMissing)).toBeTruthy();
    expect(within(overview()).getByRole('heading', { name: t.reading.documentComparison })).toBeTruthy();
    expect(within(overview()).getByText(t.reading.comparisonCode, {exact:true})).toBeTruthy();
    expect(within(overview()).getByText(uncertain.reason)).toBeTruthy();
    expect(within(overview()).queryByText(unanchored.statement)).toBeNull();
    expect(within(overview()).queryByText('다른 계약')).toBeNull();

    documents.comparisons = [uncertain, known];
    rerender(<ReportDocument report={report} />);
    expect(within(overview()).queryByText(t.reading.codeEvidenceMissing)).toBeNull();
    expect(within(overview()).getByText(t.reading.comparisonStatus.match)).toBeTruthy();
  });

  it('preserves unmatched backticks and escapes source text in the comparison rows', () => {
    const doc = example('report.json');
    doc.project = { name: 'Documentation' };
    const report = parseAnalysisReport(doc, doc.run_id);
    const { documents, codeReview } = report.sections!;
    codeReview.findings = [];
    codeReview.strengths = [];
    const base = documents.comparisons.find(row => row.documentRefs.length > 0)!;
    documents.comparisons = [{ ...base, status: 'unknown', codeRefs: [],
      statement: '설명서는 `Read(path)` 호출을 설명한다. <img src=x>',
      reason: '코드에서 `<script>probe()</script>`와 ``예시``를 확인했다. 짝 없는 `' }];
    render(<ReportDocument report={report} />);
    const judgment = screen.getByRole('region', { name: t.reading.documentComparison });
    const rows = judgment.querySelectorAll('.report-comparison-rows > div');
    expect(Array.from(rows, row => row.querySelector('dt')?.textContent)).toEqual([t.reading.comparisonDocument, t.reading.comparisonCode]);
    expect(rows[0].querySelector('p > code')?.textContent).toBe('Read(path)');
    expect(rows[0].querySelector('p')?.textContent).toContain('<img src=x>');
    expect(rows[1].querySelector('p > code')?.textContent).toBe('<script>probe()</script>');
    expect(rows[1].querySelector('p')?.textContent).toContain('``예시``를 확인했다. 짝 없는 `');
    expect(judgment.querySelectorAll('img, script')).toHaveLength(0);
  });

  it('leads with a product behavior and keeps all unknown comparisons in a compact assessment', () => {
    const doc = example('report.json');
    doc.project = { name: 'Notifications' };
    const report = parseAnalysisReport(doc, doc.run_id);
    const { documents, codeReview } = report.sections!;
    const base = documents.comparisons.find(row => row.documentRefs.length > 0)!;
    const productRow = { ...base, status: 'unknown', statement: '이전에 받은 알림을 다시 열 수 있다.', codeRefs: [] };
    const productProof = { ...codeReview.strengths[0], scope: 'gateway' };
    documents.comparisons = [
      productRow,
      { ...productRow, statement: '지원 도구는 요청 전 환경을 확인한다.' },
      { ...productRow, statement: '시험 보고서를 만든다.', scope: 'fixtures' },
    ];
    codeReview.findings = [];
    codeReview.citations = [
      { id: 'qa', kind: 'code', path: 'scripts/qa-reports.sh', line: 1, end: 2, excerpt: null },
      ...codeReview.citations,
    ];
    codeReview.strengths = [
      { title: '시험 준비', benefit: '시험용 보고서를 생성한다.', scope: '.', refs: ['qa'] },
      productProof,
    ];
    const { rerender } = render(<ReportDocument report={report} />);
    const opening = within(screen.getByRole('region', { name: productProof.title }));
    expect(opening.getByText(t.reading.recordedStrength)).toBeTruthy();
    expect(opening.getByText(productProof.benefit)).toBeTruthy();
    expect(opening.getByText(t.reading.documentAssessmentTotal(3))).toBeTruthy();
    expect(opening.getByText(t.reading.comparisonCount.unknown).nextElementSibling?.textContent).toBe('3개');
    expect(opening.getByText(productProof.scope)).toBeTruthy();
    expect(opening.getByText(productRow.statement, { exact: false })).toBeTruthy();
    expect(opening.queryByText('시험용 보고서를 생성한다.')).toBeNull();
    expect(opening.queryByText(documents.comparisons[1].statement)).toBeNull();

    fireEvent.click(opening.getByRole('button', { name: t.reading.openDocumentAnalysis }));
    expect(document.activeElement?.closest('li')?.textContent).toContain(productRow.statement);
    fireEvent.click(screen.getByRole('tab', { name: t.sections.overview }));

    rerender(<ReportDocument report={{ ...report, includesCode: false }} />);
    const codeFree = within(screen.getByRole('region', { name: t.reading.omitted }));
    expect(codeFree.getByText(t.reading.comparisonCount.unknown).nextElementSibling?.textContent).toBe('3개');
    expect(codeFree.queryByText(productProof.title)).toBeNull();
    expect(codeFree.queryByText(productProof.benefit)).toBeNull();
    expect(codeFree.queryByText(productRow.statement, { exact: false })).toBeNull();
  });

  it('shows the recorded limitation before conditional advice while retaining the stored finding', () => {
    const doc = example('report.json');
    doc.project = { name: 'Events' };
    const report = parseAnalysisReport(doc, doc.run_id);
    report.sections!.documents.comparisons = [];
    const finding = report.findings.find(f => f.validation === 'supported')!;
    finding.conditions[0].statement = '운영체제에서 파일 알림이 동시에 전달되면 큐가 재시작된다.';
    finding.verification = ['운영체제의 파일 알림 전달 순서를 확인한다.'];
    finding.recommendation = '파일 알림 큐를 교체한다.';
    const premise = '운영체제의 파일 알림이 동시에 전달되는지는 확인되지 않았다.';
    finding.limitations = ['verification_call_failed', premise, '추가 입력의 종류는 확인하지 않았다.'];
    const { rerender } = render(<ReportDocument report={report} />);
    const opening = within(screen.getByRole('region', { name: finding.title }));
    expect(opening.getByText(t.reading.supported)).toBeTruthy();
    expect(opening.getByText(premise, { exact: false })).toBeTruthy();
    const limitation = opening.getByText(t.reading.findingLimitation);
    const advice = opening.getByText(t.reading.conditionalAction);
    expect(limitation.compareDocumentPosition(advice) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(opening.queryByText(t.reading.nextAction)).toBeNull();
    expect(opening.getByText(finding.recommendation, { exact: false })).toBeTruthy();
    expect(opening.queryByText('추가 입력의 종류는 확인하지 않았다.')).toBeNull();
    expect(finding.validation).toBe('supported');

    finding.limitations = ['verification_call_failed'];
    rerender(<ReportDocument report={report} />);
    expect(opening.queryByText(t.reading.findingLimitation)).toBeNull();
    expect(opening.getByText(t.reading.nextAction)).toBeTruthy();
  });

  it('retains a related strength and its wider source without repeating a heading as its benefit', () => {
    const doc = example('report.json');
    doc.project = { name: 'Requests' };
    const report = parseAnalysisReport(doc, doc.run_id);
    const { documents, codeReview } = report.sections!;
    documents.comparisons = [];
    codeReview.findings = [];
    const proof = codeReview.strengths[0];
    const first = codeReview.citations.find(c => proof.refs.includes(c.id) && c.kind === 'code')!;
    const wider = { ...first, id: 'wider', end: first.end + 2, excerpt: null };
    const title = '재시도 중단';
    const related = { ...proof, title: '재시도 제한', benefit: '오류가 이어지면 요청 횟수를 제한한다.', refs: [wider.id] };
    codeReview.citations.push(wider);
    codeReview.strengths = [{ ...proof, title, benefit: '재시도  중단.' }, related];

    render(<ReportDocument report={report} />);
    const opening = within(screen.getByRole('region', { name: t.reading.relatedStrengths(title, related.title) }));
    expect(opening.getByText(related.benefit)).toBeTruthy();
    expect(opening.queryByText('재시도 중단.')).toBeNull();
    expect(opening.getByText(`${wider.path}:${wider.line}–${wider.end}`)).toBeTruthy();
  });

  it('shows one literal document example with its source and omits it from code-free or older reports', () => {
    const doc = example('report.json');
    doc.project = { name: 'Examples' };
    const report = parseAnalysisReport(doc, doc.run_id);
    const { documents, codeReview } = report.sections!;
    codeReview.findings = [];
    codeReview.strengths = [];
    const first = documents.comparisons[0];
    const citation = documents.citations.find(c => first.documentRefs.includes(c.id))!;
    citation.excerpt = { start: 1, end: 4, text: '```js\npreview("raw");\n//=> "<img src=x>"\n```' };
    documents.comparisons = [{ ...first, status: 'unknown', statement: 'preview("raw") returns "<img src=x>".' }];
    const { rerender } = render(<ReportDocument report={report} />);
    const overview = screen.getByRole('tabpanel', { name: t.sections.overview });
    const row = within(overview).getByText(t.reading.documentExample).closest('p')!;
    expect([...row.querySelectorAll('code')].map(code => code.textContent)).toEqual(['preview("raw")', '"<img src=x>"']);
    expect(row.querySelector('img')).toBeNull();
    expect(row.nextElementSibling?.querySelector('summary')?.getAttribute('aria-label')).toContain('README.md');
    expect(within(overview).getByText(t.reading.codeEvidenceMissing)).toBeTruthy();

    rerender(<ReportDocument report={{ ...report, includesCode: false }} />);
    expect(within(overview).queryByText(t.reading.documentExample)).toBeNull();
    citation.excerpt = null;
    rerender(<ReportDocument report={report} />);
    expect(within(overview).queryByText(t.reading.documentExample)).toBeNull();
  });

  it('opens saved overview evidence first and keeps the fixed-commit link', () => {
    const doc = example('report.json');
    const sha = 'a'.repeat(40);
    doc.project = { name: 'Billing', source: 'https://git.example.test:8443/Owner/Billing.git/' };
    doc.inputs.source_kind = 'git';
    doc.inputs.commit_sha = sha;
    const comparison = doc.documents.comparisons.find((c: { status: string }) => c.status === 'match');
    const citation = doc.documents.evidence.find((e: { id: string }) => comparison.code_evidence_ids.includes(e.id));
    citation.excerpt = { ...citation.excerpt, end_line: citation.excerpt.start_line, text: citation.excerpt.text.split('\n')[0] };
    const view = () => {
      const report = parseAnalysisReport(doc, doc.run_id);
      report.sections!.codeReview.findings = [];
      return <ReportDocument report={report} />;
    };
    const { rerender } = render(view());
    const hero = screen.getByRole('heading', { name: 'Billing' }).closest('header')!;
    expect(within(hero).getByText(doc.project.source)).toBeTruthy();
    expect(within(hero).getByText(t.commit)).toBeTruthy();
    expect(within(hero).getByText(sha.slice(0, 12)).getAttribute('title')).toBe(sha);
    expect(within(hero).getByText(t.published).parentElement?.querySelector('time')?.dateTime).toBe(doc.created_at);
    expect(within(hero).queryByText(t.duration)).toBeNull();
    const overview = screen.getByRole('tabpanel', { name: t.sections.overview });
    const url = `https://git.example.test:8443/Owner/Billing/blob/${sha}/${citation.path}#L${citation.start_line}-L${citation.end_line}`;
    const visibleRange = `${citation.path}:${citation.start_line}–${citation.end_line}`;
    const excerptRange = `${citation.path}:${citation.excerpt.start_line}`;
    const openingSource = within(overview).getByLabelText(t.sourceActionAt(t.viewCode, visibleRange));
    expect(openingSource.tagName).toBe('SUMMARY');
    expect(openingSource.closest('details')!.open).toBe(false);
    const opened = within(openDisclosure(openingSource));
    expect(opened.getByLabelText(t.excerptAt(excerptRange)).textContent).toContain(citation.excerpt.text);
    const link = opened.getByRole('link', { name: t.sourceActionAt(t.openSource, visibleRange) });
    expect(link.getAttribute('href')).toBe(url);
    expect(link.getAttribute('rel')).toBe('noopener noreferrer');

    fireEvent.click(screen.getByRole('tab', { name: t.sections.titles.documents }));
    const panel = screen.getByRole('tabpanel', { name: t.sections.titles.documents });
    const comparisonCard = within(panel).getByRole('heading', { name: comparison.statement }).closest('li')!;
    const control = within(comparisonCard).getByText(t.viewCode, { selector: 'summary' });
    expect(control.closest('details')!.open).toBe(false);
    const details = openDisclosure(control);
    expect(within(details).getByRole('link', { name: t.sourceActionAt(t.openSource, visibleRange) }).getAttribute('href')).toBe(url);
    expect(within(details).getByLabelText(t.excerptAt(excerptRange))).toBeTruthy();

    fireEvent.click(screen.getByRole('tab', { name: t.sections.overview }));
    delete citation.excerpt;
    rerender(view());
    expect(within(overview).getByRole('link', { name: t.sourceActionAt(t.viewCode, visibleRange) }).getAttribute('href')).toBe(url);
  });

  it('opens all 24 saved model lines and rejects a long excerpt outside its citation', () => {
    const doc = example('report.json');
    doc.project = { name: 'Routes' };
    const comparison = doc.documents.comparisons.find((row: {code_evidence_ids: string[]}) => row.code_evidence_ids.length)!;
    const evidence = doc.documents.evidence.find((row: {id: string}) => row.id === comparison.code_evidence_ids[0])!;
    const lines = Array.from({ length: 24 }, (_, i) => `// route line ${i + 1}`);
    lines[12] = '  const parsed = await input.json();';
    lines[21] = '  return Response.json(result, { status: 500 });';
    evidence.path = 'route.ts';
    evidence.start_line = 1;
    evidence.end_line = 24;
    evidence.excerpt = { start_line: 1, end_line: 24, text: lines.join('\n') };
    const report = parseAnalysisReport(doc, doc.run_id);
    render(<ReportDocument report={report} />);
    fireEvent.click(screen.getByRole('tab', { name: t.sections.titles.documents }));
    const panel = screen.getByRole('tabpanel', { name: t.sections.titles.documents });
    const card = within(panel).getByRole('heading', { name: comparison.statement }).closest('li')!;
    const control = within(card).getByLabelText(t.sourceActionAt(t.viewCode, 'route.ts:1–24'));
    const details = openDisclosure(control);
    const code = within(details).getByLabelText(t.excerptAt('route.ts:1–24'));
    expect(Array.from(code.querySelectorAll('.source-line'), (line, i) => line.textContent?.slice(String(i + 1).length, -1))).toEqual(lines);
    evidence.end_line = 23;
    expect(() => parseAnalysisReport(doc, doc.run_id)).toThrow();
  });

  it('does not put repository links in code-free or local reports, fork PRs or malformed ranges and URLs', () => {
    const doc = example('report.json');
    const sha = '1'.repeat(64);
    const source = 'https://github.com/owner/repo';
    doc.project = { name: 'Billing', source };
    doc.inputs.source_kind = 'git';
    doc.inputs.commit_sha = sha;
    const base = () => parseAnalysisReport(doc, doc.run_id).sourceBase;
    expect(base()).toBe(`${source}/blob/${sha}`);
    expect(sourceEvidenceURL(base(), '서비스/a #?.go', 4, 4)).toBe(`${source}/blob/${sha}/%EC%84%9C%EB%B9%84%EC%8A%A4/a%20%23%3F.go#L4-L4`);
    expect(sourceEvidenceURL(base(), 'src/a.go', 5, 4)).toBeUndefined();
    expect(sourceEvidenceURL(base(), 'src/../a.go', 4, 8)).toBeUndefined();

    doc.export.include_code = false;
    expect(base).toThrow();
    for (const finding of doc.findings) for (const evidence of finding.evidence) delete evidence.excerpt;
    for (const section of [doc.environment, doc.documents, doc.code_review]) {
      for (const evidence of section.evidence ?? []) delete evidence.excerpt;
    }
    expect(base()).toBeUndefined();
    doc.export.include_code = true;
    doc.inputs.source_kind = 'local';
    expect(base()).toBeUndefined();
    doc.inputs.source_kind = 'git';
    doc.pull_request = { head_repository: 'Owner/Repo' };
    expect(base()).toBe(`${source}/blob/${sha}`);
    doc.pull_request = { head_repository: 'fork/repo' };
    expect(base()).toBeUndefined();
    delete doc.pull_request;

    for (const invalid of [
      'http://github.com/owner/repo', 'https://user@github.com/owner/repo', 'https://127.1/owner/repo',
      'https://github.com/owner/repo?', 'https://github.com/owner/repo#', 'https://github.com/owner/../repo',
      'https://github.com/owner/%2Frepo', 'https://github.com/owner/repo\n',
    ]) {
      doc.project.source = invalid;
      expect(base(), invalid).toBeUndefined();
    }
    doc.project.source = source;
    doc.inputs.commit_sha = 'main';
    expect(base()).toBeUndefined();
  });

  it('prioritizes one supported follow-up over isolated strengths without calling it confirmed', () => {
    const doc = example('report-slices.json');
    doc.project = { name: 'Billing' };
    const report = parseAnalysisReport(doc, doc.run_id);
    report.sections!.documents.comparisons = [];
    const other = example('report.json');
    const main = parseAnalysisReport(other, other.run_id);
    report.sections!.codeReview.strengths = main.sections!.codeReview.strengths;
    report.sections!.codeReview.citations.push(...main.sections!.codeReview.citations);
    render(<ReportDocument report={report} />);
    const overview = screen.getByRole('tabpanel', { name: t.sections.overview });
    expect(within(overview).getAllByText(t.validation.inconclusive)).toHaveLength(1);
    expect(within(overview).getAllByText(t.reading.nextCheck)).toHaveLength(1);
    expect(within(overview).queryByText(t.reading.recordedStrength)).toBeNull();
    expect(within(overview).queryByText(t.reading.nextAction)).toBeNull();
  });

  it('joins a recorded strength to a verification step only when the subject and product source agree', () => {
    const doc = example('report-slices.json');
    const sha = 'b'.repeat(40);
    doc.project = { name: 'Billing', source: 'https://github.com/owner/billing.git' };
    doc.inputs.source_kind = 'git';
    doc.inputs.commit_sha = sha;
    const report = parseAnalysisReport(doc, doc.run_id);
    const { codeReview, documents } = report.sections!;
    const pending = report.findings.find(f => codeReview.findings.includes(f.id) && f.validation === 'inconclusive')!;
    pending.title = '백업 실패 시 일부 파일이 남을 수 있음';
    pending.verification = [
      '백업 호출자의 실패 처리와 부분 백업 보존 계약을 확인한다.',
      '백업 목록과 복원 경로가 부분 파일을 어떻게 취급하는지 확인한다.',
    ];
    codeReview.findings = [pending.id];
    const e = pending.evidence.find(e => e.kind === 'code' && e.path && e.lines)!;
    const path = e.path!;
    const start = e.lines!.end + 1;
    codeReview.citations = [
      { id: 'same-file', path, kind: 'code', line: start, end: start + 3, excerpt: null },
      { id: 'same-subject', path: 'other.go', kind: 'code', line: start, end: start + 3, excerpt: null },
      { id: 'both', path, kind: 'code', line: start + 4, end: start + 7, excerpt: null },
    ];
    codeReview.strengths = [
      { title: '테마 읽기 실패 처리', benefit: '기본 테마를 불러온다.', refs: ['same-file'], scope: '.' },
      { title: '백업 로그 확인', benefit: '백업 요청을 기록한다.', refs: ['same-subject'], scope: '.' },
      { title: '백업 무결성 확인', benefit: '압축 전 데이터의 무결성을 검사한다.', refs: ['both'], scope: '.' },
    ];
    documents.comparisons = [{ status: 'unknown', statement: '두 번째 동작', reason: '구현 행 미확인', scope: '.', documentRefs: [], codeRefs: [] }];
    render(<ReportDocument report={report} />);

    const overview = screen.getByRole('tabpanel', { name: t.sections.overview });
    const judgment = within(overview).getByRole('region', { name: codeReview.strengths[2].title });
    expect(within(judgment).getByText(t.reading.recordedStrength)).toBeTruthy();
    expect(within(judgment).getByText(codeReview.strengths[2].benefit)).toBeTruthy();
    expect(within(judgment).getByText(t.reading.unresolvedBehavior)).toBeTruthy();
    expect(judgment.textContent).toContain(pending.title);
    expect(judgment.textContent).toContain(pending.verification[1]);
    expect(judgment.textContent).not.toContain(pending.verification[0]);
    expect(within(judgment).getByText(t.reading.documentAssessmentTotal(1))).toBeTruthy();
    expect(within(judgment).queryByText(codeReview.strengths[0].benefit)).toBeNull();
    expect(within(judgment).queryByText(codeReview.strengths[1].benefit)).toBeNull();
    const confirmed = within(judgment).getByRole('link', { name: new RegExp(t.reading.confirmedCode) });
    const check = within(judgment).getByRole('link', { name: new RegExp(t.reading.followupCode) });
    expect(confirmed.getAttribute('href')).toContain(`/blob/${sha}/${path}#L${start + 4}-L${start + 7}`);
    expect(check.getAttribute('href')).toContain(`/blob/${sha}/${path}#L${e.lines!.start}-L${e.lines!.end}`);
    fireEvent.click(within(judgment).getByRole('button', { name: t.reading.openDocumentAnalysis }));
    const tab = screen.getByRole('tab', { name: t.sections.titles.documents });
    expect(tab.getAttribute('aria-selected')).toBe('true');
    expect(document.activeElement).toBe(tab);
  });

  it('presents located static diagnostics as observations when project behavior is unverified', () => {
    const doc = example('report.json');
    doc.project = { name: 'Billing' };
    const report = parseAnalysisReport(doc, doc.run_id);
    const sections = report.sections!;
    sections.documents.comparisons = [];
    sections.codeReview.strengths = [];
    sections.codeReview.findings = [];
    render(<ReportDocument report={{ ...report, aiReview: 'not_performed' }} />);
    const overview = screen.getByRole('tabpanel', { name: t.sections.overview });
    expect(within(overview).getByText(t.reading.staticStatus)).toBeTruthy();
    openDisclosure(within(overview).getByText(t.reading.operations));
    expect(within(overview).getByText(t.coverage).parentElement?.textContent).toContain(t.ai.not_performed);
    expect(within(overview).queryByText(t.reading.nextAction)).toBeNull();
  });

  it('shows the overview first, one collapsible section per analysis kind and the same numbers as the document', () => {
    const doc = example('report.json');
    doc.observations[0].evidence_state = 'source_manifest_mismatch';
    doc.summary.unresolved_observations = 1;
    const r = parseAnalysisReport(doc, doc.run_id);
    expect(r.sections).not.toBeNull();
    const { container } = render(<ReportDocument report={r} />);
    const article = screen.getByRole('article', { name: t.title });
    // The overview comes before the analysis sections in reading order.
    const overview = within(article).getByRole('heading', { name: t.sections.overview });
    const details = Array.from(article.querySelectorAll('details.report-section[id]'));
    const order = ['environment', 'documents', 'static_analysis', 'metrics', 'secrets', 'code_review', 'gaps'];
    expect(details.map((d) => d.id)).toEqual(order.map((k) => 'report-section-' + k));
    expect([...order].sort()).toEqual(doc.overview.sections.map((x: { key: string }) => x.key).sort());
    for (const d of details) expect(overview.compareDocumentPosition(d) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    // The same numbers are available when the reader opens analysis statistics.
    openDisclosure(screen.getByText(t.reading.operations));
    const total = Array.from(container.querySelectorAll('.report-counts > div')).find((x) => x.querySelector('dt')?.textContent === t.counts.total);
    expect(total?.querySelector('dd')?.textContent).toBe(String(doc.summary.total_findings));
    expect(within(article).getByText(t.sectionedCounts.raw).nextElementSibling?.textContent).toBe(String(doc.summary.raw_observations));
    expect(within(article).getByText(t.sectionedCounts.unresolved).nextElementSibling?.textContent).toBe('1');
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
    // A tool that did not run is not zero diagnostics, in the analyzer table
    // and in the per-task table behind the static section's disclosure.
    const jscpdRow = () => within(article).getAllByText(/^jscpd/).map((x) => x.closest('tr')!).filter(Boolean);
    expect(jscpdRow().some((tr) => tr.cells.length === 6 && tr.textContent!.includes(t.sections.notRun))).toBe(true);
    const staticSection = container.querySelector('#report-section-static_analysis') as HTMLElement;
    const all = staticSection.querySelector('details.report-more') as HTMLDetailsElement;
    all.open = true;
    fireEvent(all, new Event('toggle'));
    const jscpd = jscpdRow().find((tr) => tr.cells.length === 9)!;
    expect(jscpd.textContent).toContain(t.sections.notRun);
    expect(jscpd.textContent).not.toMatch(/\b0\b.*규칙/);
    // Model interpretation is labeled with its role.
    expect(within(article).getAllByText(t.sections.model('review.documents')).length).toBe(1);
    expect(within(article).getAllByText(/하위 호출이 실패하면 최대 3번 재시도한다/).length).toBeGreaterThan(0);
    // Each finding is listed once, in the static or the code review section
    // (with every disclosure open).
    for (const more of Array.from(container.querySelectorAll('details.report-more')) as HTMLDetailsElement[]) {
      if (more.open) continue;
      more.open = true;
      fireEvent(more, new Event('toggle'));
    }
    expect(container.querySelectorAll('details.report-section .finding-card').length).toBe(doc.findings.length);
    // The opening focuses on documented behavior with implementation evidence.
    const judgments = container.querySelectorAll('.report-executive .card-title');
    expect(judgments).toHaveLength(1);
    expect(judgments[0].textContent).toBe(r.sections!.documents.comparisons.find(c => c.status === 'match')!.statement);
  });

  it('opens a section from the overview and moves focus to it from the keyboard', async () => {
    const doc = example('report.json');
    const r = parseAnalysisReport(doc, doc.run_id);
    const { container } = render(<ReportDocument report={r} />);
    const environment = container.querySelector('#report-section-environment') as HTMLDetailsElement;
    // Sections the overview points at start open; the rest are collapsed.
    expect(environment.open).toBe(true);
    openDisclosure(screen.getByText(t.reading.operations));
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
    expect(container.querySelector('#report-section-overview')).toBeNull();
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
      // Builder 8: the class after the module, the secondary files left out
      // within the ceiling after the files not planned.
      expect(cells[1]).toBe(t.sections.ledger.classes[m.class]);
      expect(cells.slice(3)).toEqual([m.files_eligible, m.files_sent_to_model, m.bytes_sent, m.files_truncated, m.files_withheld, m.files_unreadable,
        m.files_budget_exhausted, m.files_not_planned, m.files_secondary_not_reached ?? 0, m.files_not_sent, m.files_with_completed_review].map(String));
    }
    expect(within(section).getByText(t.sections.ledger.refusedSlots(doc.code_review.ledger.primary_slots_budget_exhausted))).toBeTruthy();
    // The slots fold into runs of one state; every slice task is behind the disclosure.
    expect(within(section).getByText(t.sections.ledger.slotSpan('s001', 's003', 3))).toBeTruthy();
    expect(within(section).queryByText('review.investigate.s004')).toBeNull();
    const more = within(section).getByText(t.sections.ledger.allModules(doc.code_review.ledger.modules.length)).closest('details') as HTMLDetailsElement;
    more.open = true;
    fireEvent(more, new Event('toggle'));
    expect(within(section).getAllByText('review.investigate.s004').length).toBe(1);
    expect(within(section).getByText(t.sections.ledger.calls(String(doc.code_review.ledger.calls_used), String(doc.code_review.ledger.max_calls)))).toBeTruthy();
    expect(section.textContent).not.toMatch(/검증된 범위입니다|verified/);
    // Slice findings are listed once, in the code review section.
    const slice = doc.findings.filter((f: { id: string }) => f.id.startsWith('review_s')).length;
    expect(slice).toBeGreaterThan(0);
    const other = section.querySelector('details.report-more:not([open])') as HTMLDetailsElement | null;
    if (other) {
      other.open = true;
      fireEvent(other, new Event('toggle'));
    }
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
    // No secrets task ran: the fixed not-screened sentence.
    const secrets = container.querySelector('#report-section-secrets') as HTMLElement;
    expect(within(secrets).getByText(t.sections.modelInput.not_screened)).toBeTruthy();
  });

  // internal/report/testdata/report-screen-unusable.json: the same fixture
  // after a secrets scan that ran but failed
  // (internal/worker/review/report_slices_example_test.go).
  it('names the failed secrets task when the review ran without a usable screen', () => {
    const doc = example('report-screen-unusable.json');
    const r = parseAnalysisReport(doc, doc.run_id);
    const { container } = render(<ReportDocument report={r} />);
    const secrets = container.querySelector('#report-section-secrets') as HTMLElement;
    expect(within(secrets).getByText(/탐지 작업 gitleaks의 결과를 쓸 수 없어/)).toBeTruthy();
    expect(within(secrets).queryByText(t.sections.modelInput.not_screened)).toBeNull();
    const gaps = container.querySelector('#report-section-gaps') as HTMLElement;
    expect(within(gaps).getByText(t.sections.gapLabels.model_input_not_screened)).toBeTruthy();
    expect(doc.tasks.some((x: { reason: string | null }) => x.reason === 'secret_screen_failed')).toBe(false);
  });
});
