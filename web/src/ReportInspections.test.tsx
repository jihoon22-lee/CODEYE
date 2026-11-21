// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, expect, it } from 'vitest';
import type { Observation } from './analysisReport';
import type { StaticTool } from './parseReportSections';
import { InspectionReading, MeasurementReading } from './ReportInspections';

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
  expect(screen.getByText('중복 2쌍의 위치 보기')).toBeTruthy();
  expect(screen.getByText('도구가 기록한 위치 보기')).toBeTruthy();
  expect(screen.getByText(/이전 보고서에는 상대 블록 위치/)).toBeTruthy();
  expect(screen.queryByText(/스냅샷에서 확인하지 못했습니다/)).toBeNull();
});
