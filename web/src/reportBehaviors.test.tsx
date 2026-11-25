// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, expect, it } from 'vitest';
import example from '../../examples/report.json';
import workflowDocuments from '../../examples/review/documents-workflow-reading.json';
import workflowInvestigation from '../../examples/review/investigate-workflow-reading.json';
import { parseAnalysisReport } from './analysisReport';
import { ReportDocument } from './ReportPanel';
import { reportKo as t } from './locales/report-ko';
import type { Citation } from './parseReportSections';
import { projectBehaviors } from './reportBehaviors';

afterEach(cleanup);

it('places the fifth implemented behavior before bare promises and leaves stored order intact', () => {
  const report = parseAnalysisReport({ ...example, demo: false }, example.run_id);
  const sections = report.sections!;
  const documents = sections.documents;
  report.findings = [];
  sections.codeReview.findings = [];
  const cite = (id: string, kind: string, path: string, text: string): Citation => {
    const end = text.split('\n').length;
    return { id, kind, path, line: 1, end, excerpt: { start: 1, end, text } };
  };
  documents.citations = [
    cite('guide', 'document', 'README.md', '# Delivery\n\n## Features\n\nDeliver and calculate totals.'),
    cite('notes', 'document', 'README.md', '## Development history\n\nIn this release, the total reflects all items.'),
    cite('imports', 'code', 'src/overview.ts', "import { StatusReady } from './state';\nimport type { Options } from './options';"),
    cite('execution', 'code', 'src/totals.py', '    return total * multiplier'),
  ];
  const row = (statement: string, status = 'unknown', ...codeRefs: string[]) => ({
    statement, status, reason: '저장한 대조 이유입니다.', scope: '.', documentRefs: ['guide'], codeRefs,
  });
  documents.comparisons = [
    row('첫 약속은 가져오기 줄만 인용합니다.', 'unknown', 'imports'),
    row('두 번째 약속입니다.'),
    row('세 번째 약속입니다.'),
    row('네 번째 약속입니다.'),
    row('다섯 번째 동작은 합계를 반환합니다.', 'unknown', 'execution'),
    row('같은 수준의 후속 동작도 값을 반환합니다.', 'unknown', 'execution'),
  ];
  for (const i of [4, 5]) documents.comparisons[i].documentRefs = ['notes'];
  const { rerender } = render(<ReportDocument report={report} />);
  const titles = () => [...screen.getByRole('region', { name: t.behavior.heading })
    .querySelectorAll('.report-behavior > h6, .report-behavior > summary')].map(node => node.textContent);
  expect(titles()).toEqual([4, 5, 0, 1].map(index => documents.comparisons[index].statement));

  documents.comparisons = [...documents.comparisons,
    row('저장된 일치입니다.', 'match', 'execution'),
    row('저장된 부분 일치입니다.', 'partial', 'execution'),
    row('저장된 불일치입니다.', 'mismatch', 'execution'),
    { ...row('릴리스 안내가 아직 확인되지 않았습니다.', 'unknown', 'imports'), documentRefs: ['notes'] },
    { ...row('배포 명령은 별도 옵션을 받습니다.', 'unknown', 'execution'), documentRefs: ['notes'] },
  ];
  const stored = structuredClone(documents.comparisons);
  rerender(<ReportDocument report={report} />);
  expect(titles()).toEqual([7, 6, 4, 5].map(index => stored[index].statement));
  expect(screen.getByText(stored[8].statement, { selector: '#report-judgment' })).toBeTruthy();
  expect(projectBehaviors(documents, true).rows).toHaveLength(9);
  expect(projectBehaviors(documents, true)).toEqual(projectBehaviors(documents, true));
  expect(projectBehaviors(documents, false).rows).toHaveLength(0);
  expect(documents.comparisons).toEqual(stored);
});

it('puts workflows and exact code excerpts before configuration and opens the selected comparison', () => {
  const doc = structuredClone(example);
  doc.demo = false;
  const report = parseAnalysisReport(doc, doc.run_id);
  const documents = report.sections!.documents;
  const cite = (id: string, kind: string, path: string, start: number, text: string): Citation => {
    const end = start + text.split('\n').length - 1;
    return { id, kind, path, line: start, end, excerpt: { start, end, text } };
  };
  const usage = cite('readme-use', 'document', 'README.md', 1,
    '# Parcel\n\nParcel delivers receipts to customers.\n\n## Features\n\nOpen a receipt.\n\n## Configuration\nChange storage.');
  const module = cite('module-use', 'document', 'receipts/README.md', 1, '# Receipts\n\nSave or email copies of receipts.');
  const config = cite('readme-config', 'document', 'README.md', 22, '## Parameters\n\nStore path defaults to receipts.db.');
  const branch = cite('handler', 'code', 'receipts/server.ts', 16,
    "if (error instanceof ReceiptNotFound) {\n  return res.status(404).send('unknown receipt');\n}\nauditReceipt(receipt);");
  documents.citations = [usage, module, config, branch];
  const reason = '생성된 링크의 유효 기간은 확인하지 못했습니다.';
  const additional = '요청한 항목이 없을 때의 응답 분기를 확인했습니다.';
  documents.comparisons = [
    { statement: '기본 저장 경로를 사용합니다.', reason: '프로세스 기본 경로로 초기화합니다.', scope: '.', status: 'match', codeRefs: [branch.id], documentRefs: [config.id] },
    { statement: '영수증 링크를 열 수 있습니다.', reason, scope: '.', status: 'unknown', codeRefs: [branch.id], documentRefs: [usage.id],
      additional: { reason: additional, codeRefs: [branch.id] } },
    { statement: '사본을 저장할 수 있습니다.', reason: '사본 저장 경로는 검토하지 않았습니다.', scope: 'receipts', status: 'unknown', codeRefs: [], documentRefs: [module.id] },
  ];
  report.sections!.codeReview.findings = [];
  const { rerender } = render(<ReportDocument report={report} />);

  const overview = screen.getByRole('region', { name: t.behavior.heading });
  const hero = screen.getByRole('heading', { name: report.project!.name }).closest('header')!;
  expect(hero.querySelector('.report-purpose')?.textContent).toContain('Parcel delivers receipts to customers. README.md:3');
  expect(screen.getByRole('article', { name: t.title }).querySelectorAll('.report-purpose')).toHaveLength(1);
  expect(overview.querySelector('pre')?.textContent).toBe(branch.excerpt!.text.split('\n').slice(0, 2).join('\n'));
  const expandCode = within(overview).getAllByLabelText(t.sourceActionAt(t.behavior.fullSource, 'receipts/server.ts:16–19'))[0];
  const fullCode = expandCode.closest('details')!;
  expect(fullCode.open).toBe(false);
  fireEvent.click(expandCode);
  expect(fullCode.open).toBe(true);
  expect(fullCode.querySelector('pre')?.textContent).toContain('auditReceipt(receipt);');
  expect(overview.textContent).toContain('receipts/server.ts:16–17');
  expect(overview.textContent).toContain(reason);
  expect(overview.textContent).toContain(additional);
  expect(overview.textContent).toContain(t.reading.comparisonStatus.unknown);
  expect(overview.textContent).not.toContain('기본 저장 경로를 사용합니다.');
  const configDecision = screen.getByText('기본 저장 경로를 사용합니다.', { selector: 'h6' }).closest('details');
  expect(configDecision?.open).toBe(false);

  const other = within(overview).getByText('사본을 저장할 수 있습니다.').closest('details')!;
  other.open = true;
  fireEvent.click(within(other).getByRole('button', { name: t.behavior.openComparison }));
  expect(screen.getByRole('tab', { name: t.sections.titles.documents }).getAttribute('aria-selected')).toBe('true');
  expect(document.activeElement?.textContent).toContain('사본을 저장할 수 있습니다.');

  fireEvent.click(screen.getByRole('tab', { name: t.sections.overview }));
  documents.citations = documents.citations.map(citation => citation.id !== branch.id ? citation : {
    ...citation, excerpt: { start: 16, end: 17, text: branch.excerpt!.text.split('\n').slice(0, 2).join('\n') },
  });
  expect(projectBehaviors(documents, true).rows[0].code).toBeUndefined();
  rerender(<ReportDocument report={{ ...report, includesCode: false }} />);
  expect(screen.queryByRole('region', { name: t.behavior.heading })).toBeNull();
  expect(screen.queryByText('Parcel delivers receipts to customers.', { exact: false })).toBeNull();
});

it('opens three related positions without changing the comparison and keeps only locations in a code-free report', () => {
  const doc: Record<string, any> = structuredClone(example);
  doc.demo = false;
  doc.builder.version = '16';
  // Builder 22 added the diagnostic triage; an older revision has none.
  delete doc.code_review.diagnostic_triage;
  const contract = workflowDocuments.contracts[0];
  const { contract_index: _index, ...comparison } = workflowInvestigation.contract_comparisons[0];
  const { read_evidence_id, change_evidence_id, test_evidence_id } = workflowInvestigation.workflow_reading;
  const refs = { read_evidence_id, change_evidence_id, test_evidence_id };
  const evidence = structuredClone(workflowInvestigation.evidence) as Array<Record<string, any>>;
  const testSource = evidence.find(e => e.id === test_evidence_id)!;
  const docSource = evidence.find(e => e.id === contract.evidence_ids[0])!;
  docSource.excerpt = { start_line: 1, end_line: 3, text: '# 책장 안내\n\n목록에서 책을 지운 뒤 다시 찾습니다。' };
  const testText = 'func TestRelatedArchive(t *testing.T) {';
  testSource.excerpt = { start_line: testSource.start_line, end_line: testSource.start_line, text: testText };
  doc.documents = { ...doc.documents, contracts: [contract], evidence,
    comparisons: [{ ...comparison, statement: contract.statement, related_workflow: refs }] };
  const artifact = doc.artifacts.find((a: Record<string, unknown>) =>
    a.task_key === 'review.investigate' && a.media_type === 'application/vnd.codeye.review+json');
  artifact.decoder = artifact.media_type + ';schema_version=0.6';
  const report = parseAnalysisReport(doc, doc.run_id);
  const renderReport = (input: Record<string, any>) => <ReportDocument report={parseAnalysisReport(input, input.run_id)} />;
  const { rerender } = render(<ReportDocument report={report} />);
  const overview = screen.getByRole('region', { name: t.behavior.heading });
  expect(overview.textContent).toContain(t.reading.comparisonStatus.unknown);
  const overviewContext = within(overview).getByText(t.workflow.heading).closest('details')!;
  expect(overviewContext.open).toBe(false);
  expect(within(overviewContext).getByText(t.workflow.context)).toBeTruthy();
  fireEvent.click(within(overview).getByRole('button', { name: t.behavior.openComparison }));
  const documents = screen.getByRole('tabpanel', { name: t.sections.titles.documents });
  expect(document.activeElement?.textContent).toBe(contract.statement);
  const related = within(documents).getByText(t.workflow.heading).closest('details')!;
  related.open = true;
  expect(within(related).getByText(t.workflow.read)).toBeTruthy();
  expect(within(related).getByText(t.workflow.change)).toBeTruthy();
  expect(within(related).getByText(t.workflow.test)).toBeTruthy();
  expect(related.querySelector('pre')?.textContent).toContain(testText);

  const old = structuredClone(doc);
  old.builder.version = '14';
  delete old.code_review.ledger.files;
  for (const m of old.code_review.ledger.modules) {
    delete m.files_requested;
    delete m.files_accepted;
  }
  expect(() => parseAnalysisReport(old, old.run_id)).toThrow();
  delete old.documents.comparisons[0].related_workflow;
  old.artifacts.find((a: Record<string, string>) => a.id === artifact.id).decoder = artifact.media_type + ';schema_version=0.2';
  expect(() => parseAnalysisReport(old, old.run_id)).not.toThrow();

  const invalid = structuredClone(doc);
  invalid.documents.comparisons[0].related_workflow.test_evidence_id = read_evidence_id;
  expect(() => parseAnalysisReport(invalid, invalid.run_id)).toThrow();
  doc.export.include_code = false;
  for (const { metric } of doc.observations) {
    if (!metric) continue;
    delete metric.excerpt;
    delete metric.counterpart_excerpt;
  }
  const omitted = '원문 생략(코드 미포함 내보내기).';
  doc.documents.comparisons[0].statement = doc.documents.comparisons[0].reason = omitted;
  doc.documents.contracts[0].statement = omitted;
  for (const section of [doc.documents, doc.environment, doc.code_review]) for (const e of section.evidence) delete e.excerpt;
  for (const finding of doc.findings) for (const e of finding.evidence) delete e.excerpt;
  rerender(renderReport(doc));
  expect(within(documents).getByText(t.workflow.heading)).toBeTruthy();
  expect(within(documents).getByText(/archive\/routes_test\.go:/)).toBeTruthy();
  expect(documents.querySelector('.report-workflow-reading pre')).toBeNull();
});
