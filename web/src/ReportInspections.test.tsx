// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it } from 'vitest';
import type { Observation } from './analysisReport';
import type { StaticTool } from './parseReportSections';
import { InspectionReading, MeasurementReading, StaticDiagnosticReading } from './ReportInspections';

afterEach(cleanup);

const stage = { state: 'succeeded', tasks: 1, succeeded: 1 };
function tool(task: string, analyzer: string, changes: Partial<StaticTool> = {}): StaticTool {
  return {
    task, analyzer, required: false, status: 'succeeded', reason: null, result: 'accepted',
    tool: task, version: '1', purpose: 'diagnostics', inScope: 5, processed: 5,
    rulePack: null, units: {}, limitations: [], observations: [], findings: [], rules: [], metrics: 0, secrets: 0,
    ...changes,
  };
}

it('separates formatter success, failed Go package analysis, and standalone type checking that was not planned', () => {
  const tools = [
    tool('go-vet', 'go.vet', { status: 'failed', reason: 'dependency_unavailable', processed: null }),
    tool('gofmt', 'go.gofmt'),
    tool('staticcheck', 'go.staticcheck', { status: 'unsupported', reason: 'tool_not_configured', processed: 0 }),
  ];
  render(<InspectionReading language={{ ecosystem: 'go', tools }} byID={new Map()} />);
  expect(screen.getByText(/검사한 파일의 서식 일치.*대상 파일 5개 중 5개 처리/)).toBeTruthy();
  expect(screen.getByText(/실패.*의존성/)).toBeTruthy();
  expect(screen.getByText(/미지원.*도구 경로/)).toBeTruthy();
  expect(screen.getByText('이 실행에 계획된 검사 없음')).toBeTruthy();
  expect(screen.queryByText('검사 범위에서 보고된 진단 없음')).toBeNull();
});

it('displays both verified clone ranges and submitted file scope', () => {
  const jscpd = tool('jscpd', 'any.jscpd', { purpose: 'metrics', metrics: 1, processed: 3, units: { excluded_third_party_directory: 2 } });
  const observation: Observation = {
    id: 'observation1', rule: 'duplicated_code', tool: 'any.jscpd', message: null,
    path: 'src/a.ts:4', location: { path: 'src/a.ts', start: 4, end: 19 }, state: 'resolved',
    evidenceState: 'verified_against_source_manifest', severity: null, suppressed: false,
    metric: { name: 'duplicated_lines', value: 15, threshold: 10, unit: 'lines', counterpart: { path: 'test/b.ts', start: 7, end: 22 } },
  };
  const sections = { staticAnalysis: { stage, languages: [{ ecosystem: 'any', tools: [jscpd] }] }, metrics: {
    stage, tasks: ['jscpd'], groups: [{ name: 'duplicated_lines', unit: 'lines', thresholds: [10], max: 15, observations: [observation.id] }],
  } };
  render(<MeasurementReading sections={sections} byID={new Map([[observation.id, observation]])} />);
  expect(screen.getByText(/대상 파일 5개 중 3개를 도구에 전달/)).toBeTruthy();
  expect(screen.getByText(/외부 코드 디렉터리에서 2개 파일 제외/)).toBeTruthy();
  expect(screen.getByText('src/a.ts:4–19')).toBeTruthy();
  expect(screen.getByText('test/b.ts:7–22')).toBeTruthy();
});

it('groups repeated analyzer outcomes while retaining task keys and scopes', () => {
  const failure = { status: 'unsupported', reason: 'tool_not_configured', processed: 0 };
  const tools = [
    tool('staticcheck', 'go.staticcheck', failure),
    tool('staticcheck.m01', 'go.staticcheck', failure),
    tool('staticcheck.m02', 'go.staticcheck', { paths: ['src/main.go'] }),
  ];
  const { container } = render(<InspectionReading language={{ ecosystem: 'go', tools }} byID={new Map()} />);
  expect(screen.getAllByText(/미지원.*도구 경로/)).toHaveLength(1);
  expect(screen.getAllByText('Go의 오류 가능성·성능·유지보수 규칙을 검사합니다.')).toHaveLength(1);
  const group = screen.getByText('모듈별 검사 2개 보기').closest('details');
  expect(group?.textContent).toContain('staticcheck.m01');
  expect(container.textContent).toContain('기록된 처리 파일 1개 보기');
});

it('groups clones sharing one stored range and keeps older tool text expandable', () => {
  const jscpd = tool('jscpd', 'any.jscpd', { purpose: 'metrics', metrics: 2 });
  const shared = { path: 'src/a.ts', start: 4, end: 19 };
  const observations: Observation[] = [
    { id: 'first', rule: 'duplicated_code', tool: 'any.jscpd', message: null, path: 'src/a.ts', location: shared,
      state: 'resolved', evidenceState: 'verified_against_source_manifest', severity: null, suppressed: false,
      metric: { name: 'duplicated_lines', value: 15, threshold: 10, unit: 'lines', counterpart: { path: 'src/b.ts', start: 1, end: 16 } } },
    { id: 'legacy', rule: 'duplicated_code', tool: 'any.jscpd', message: 'Found 15 lines (4-19) duplicated on file old.ts (1-16)', path: 'src/a.ts', location: shared,
      state: 'resolved', evidenceState: 'verified_against_source_manifest', severity: null, suppressed: false,
      metric: { name: 'duplicated_lines', value: 15, threshold: 10, unit: 'lines' } },
  ];
  const sections = { staticAnalysis: { stage, languages: [{ ecosystem: 'any', tools: [jscpd] }] }, metrics: {
    stage, tasks: ['jscpd'], groups: [{ name: 'duplicated_lines', unit: 'lines', thresholds: [10], max: 15, observations: observations.map(o => o.id) }],
  } };
  render(<MeasurementReading sections={sections} byID={new Map(observations.map(o => [o.id, o]))} />);
  expect(screen.getByText('같은 범위를 공유한 중복 2쌍 · 저장된 위치 2개')).toBeTruthy();
  expect(screen.getByText('중복 1쌍의 위치 보기')).toBeTruthy();
  expect(screen.queryByText(/이전 보고서에는 상대 블록 위치/)).toBeNull();
  fireEvent.click(screen.getAllByRole('button', { name: '상세 보기', hidden: true })[1]);
  expect(screen.getByText('도구가 기록한 위치 보기')).toBeTruthy();
  expect(screen.getByText(/이전 보고서에는 상대 블록 위치/)).toBeTruthy();
  expect(screen.queryByText(/스냅샷에서 확인하지 못했습니다/)).toBeNull();
});

it('expands one individual metric result and reveals its exact stored code', () => {
  const name = 'cyclomatic_complexity';
  const lizard = tool('lizard', 'any.lizard', { purpose: 'metrics', metrics: 2 });
  const observations: Observation[] = ['first', 'second'].map((id, index) => ({
    id, rule: 'complex_function', tool: 'any.lizard', message: null, path: `src/${id}.go:4`,
    location: { path: `src/${id}.go`, start: 4, end: 19 }, state: 'resolved',
    evidenceState: 'verified_against_source_manifest', severity: null, suppressed: false,
    metric: { name, value: 30 - index, threshold: 16, unit: 'ccn',
      excerpt: { start: 3, end: 5, text: `// detail\nfunc ${id}() {\n}` } },
  }));
  const sections = { staticAnalysis: { stage, languages: [{ ecosystem: 'any', tools: [lizard] }] }, metrics: {
    stage, tasks: ['lizard'], groups: [{ name, unit: 'ccn', thresholds: [16], max: 30, observations: observations.map(o => o.id) }],
  } };
  const { container } = render(<MeasurementReading sections={sections} byID={new Map(observations.map(o => [o.id, o]))} />);
  expect(container.querySelectorAll('pre')).toHaveLength(0);
  fireEvent.click(screen.getAllByRole('button', { name: '상세 보기' })[0]);
  expect(container.querySelectorAll('pre')).toHaveLength(1);
  expect(container.querySelector('pre')?.textContent).toContain('func first()');
  fireEvent.click(screen.getByRole('button', { name: '상세 보기' }));
  expect(container.querySelectorAll('pre')).toHaveLength(1);
  expect(container.querySelector('pre')?.textContent).toContain('func second()');
});

it('opens only the selected static diagnostic with stored, matching source', () => {
  const longerDiagnostic = 'A detailed diagnostic ' + 'with additional tool context '.repeat(5).trim();
  const observations: Observation[] = ['redundantAssignment', 'anotherRule'].map((rule, n) => ({
    id: rule, rule, tool: 'cpp.cppcheck', message: n === 0 ? 'Recorded diagnostic' : longerDiagnostic, path: 'src/state.c:' + (n + 4),
    location: { path: 'src/state.c', start: n + 4, end: n + 4 }, state: 'resolved',
    evidenceState: 'verified_against_source_manifest', severity: null, suppressed: false, metric: null,
  }));
  const languages = [{ ecosystem: 'cpp', tools: [tool('cppcheck', 'cpp.cppcheck', { observations: observations.map(o => o.id) })] }];
  const findings = [{ observations: ['redundantAssignment'], evidence: [{
    id: 'source', kind: 'code', description: '', path: 'src/state.c', lines: { start: 4, end: 4 },
    excerpt: { start: 4, end: 4, text: 'state.left = 5;' },
  }] }];
  const { container } = render(<StaticDiagnosticReading languages={languages} findings={findings} byID={new Map(observations.map(o => [o.id, o]))} severityOf={new Map()} />);
  expect(container.querySelector('pre')).toBeNull();
  const button = (rule: string) => screen.getByText(rule).closest('tr')!.querySelector('button')!;
  fireEvent.click(button('redundantAssignment'));
  expect(container.querySelector('pre')?.textContent).toContain('state.left = 5;');
  expect(screen.getAllByText('Recorded diagnostic')).toHaveLength(1);
  fireEvent.click(button('anotherRule'));
  expect(container.querySelector('pre')).toBeNull();
  expect(screen.getByText(longerDiagnostic)).toBeTruthy();
  expect(screen.getByText('이 위치에 저장된 코드 발췌가 없습니다.')).toBeTruthy();
});
