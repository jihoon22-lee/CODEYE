// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { createApi } from './api';
import { parseAnalysisReport } from './analysisReport';
import { parseComparison, type Comparison } from './comparison';
import { ReportDocument } from './ReportPanel';
import { apiError, json, mockFetch } from './test-utils';
import example from '../../examples/comparison.json';

afterEach(cleanup);

const RUN = example.run_id;
const clone = <T,>(v: T): T => JSON.parse(JSON.stringify(v)) as T;

// A published report whose findings are the comparison example's current findings.
function report() {
  const findings = example.findings.map((s, i) => ({
    id: s.finding_id,
    fingerprint: s.fingerprint,
    title: `go vet 진단 ${i + 1}`,
    category: 'correctness',
    severity: 'medium',
    validation: 'unreviewed',
    disposition: s.publication_disposition,
    severity_reason: '도구 진단',
    impact: '검토 필요',
    conditions: [{ statement: '도구 진단' }],
    recommendation: { summary: '확인' },
    verification: ['go vet 재실행'],
    limitations: [],
    counterevidence_ids: [],
    claims: [{ assertion: '진단', kind: 'observed', evidence_ids: ['ev1'] }],
    evidence: [{ id: 'ev1', kind: 'tool', description: 'go vet', location: { path: 'bad/bad.go', start_line: 14, end_line: 14 } }],
  }));
  return parseAnalysisReport(
    {
      schema_version: '0.1',
      demo: false,
      run_id: RUN,
      revision: example.revision,
      status: 'completed',
      quality_verdict: 'issues_found',
      inputs: { snapshot_id: 'snap2' },
      summary: { total_findings: findings.length, actionable_findings: 0, pending_candidates: findings.length, refuted_findings: 0, raw_observations: 0 },
      coverage: { ai_review: { status: 'not_requested' } },
      observations: [],
      tasks: [],
      limitations: [],
      findings,
    },
    RUN,
  );
}

function show(c: Comparison, fetchImpl?: typeof fetch, reload = vi.fn()) {
  const api = createApi({ fetch: fetchImpl ?? mockFetch(() => json(500, {})).fetch });
  render(<ReportDocument report={report()} history={{ load: { state: 'ready', value: c }, api, reload }} />);
  return reload;
}

const reviewed = example.findings.find((f) => f.review_match === 'applied')!;
const open = example.findings.find((f) => f.review_match === 'none')!;
const card = (findingID: string) => {
  const i = example.findings.findIndex((f) => f.finding_id === findingID);
  const el = screen.getByText(`go vet 진단 ${i + 1}`).closest('details')!;
  fireEvent.click(el.querySelector('summary')!);
  return el;
};

describe('W23 comparison contract parsing', () => {
  it('parses the repository comparison example', () => {
    const c = parseComparison(example, RUN, example.revision);
    expect(c.baseline).toMatchObject({ runID: example.baseline!.run_id, revision: 1, selection: 'default' });
    expect(c.counts).toMatchObject({ persisting: 4, resolved_observed: 1, new: 0 });
    expect(c.summary.current.hidden).toBe(1);
    expect(c.findings.find((f) => f.findingID === reviewed.finding_id)).toMatchObject({ current: 'false_positive', match: 'applied' });
  });

  it('rejects another run or revision, counts that disagree with entries and non-fp2 identities', () => {
    expect(() => parseComparison(example, 'other', example.revision)).toThrow();
    expect(() => parseComparison(example, RUN, example.revision + 1)).toThrow();
    const counts = clone(example);
    counts.counts.persisting = 5;
    expect(() => parseComparison(counts, RUN)).toThrow();
    const identity = clone(example);
    identity.entries[0]!.fingerprint = 'go-vet-fp-v1:abc';
    expect(() => parseComparison(identity, RUN)).toThrow();
    const baseline = clone(example) as Record<string, unknown>;
    baseline.baseline = null;
    expect(() => parseComparison(baseline, RUN)).toThrow();
  });

  it('asks for the shown revision', async () => {
    const m = mockFetch(() => json(200, example));
    await createApi({ fetch: m.fetch }).getComparison(RUN, example.revision);
    expect(m.calls[0]!.url).toBe(`/api/v1/runs/${RUN}/comparison?revision=${example.revision}`);
  });
});

describe('W23 report history view', () => {
  it('shows the baseline, state counts in text, baseline-only entries and hides reviewed findings only from the list', () => {
    const c = parseComparison(example, RUN, example.revision);
    show(c);
    const header = screen.getByRole('heading', { name: '이전 실행과 비교' }).closest('section')!;
    expect(within(header).getByText(/기준: 실행 c971678a… · 리비전 1/)).toBeTruthy();
    expect(within(header).getByText('＝ 계속 남음')).toBeTruthy();
    // Once as a count label, once on the baseline-only entry.
    expect(within(header).getAllByText('✓ 해결 관찰')).toHaveLength(2);
    expect(within(header).getByText('기준에만 있던 발견 (1)')).toBeTruthy();
    expect(within(header).getByText('bad/bad.go:12')).toBeTruthy();
    // The false_positive finding is out of the list, but counted and one click away.
    expect(screen.queryByText(`go vet 진단 ${example.findings.indexOf(reviewed) + 1}`)).toBeNull();
    expect(screen.getByText(/숨긴 발견 1건은 목록에서만 뺐습니다/)).toBeTruthy();
    fireEvent.click(screen.getByRole('checkbox', { name: '판정으로 숨긴 발견 1건 보기' }));
    const el = card(reviewed.finding_id);
    expect(within(el).getByText(/＝ 계속 남음 · 오탐/)).toBeTruthy();
    expect(within(el).getByText(/판정이 이 발견에 적용되어 있습니다/)).toBeTruthy();
    expect(within(el).getByText(/판정한 보고서: 실행 c971678a…/)).toBeTruthy();
    // A viewer reads reviews but gets no form.
    expect(within(el).queryByRole('form')).toBeNull();
    expect(within(el).getByText(/owner·manager·runner만/)).toBeTruthy();
  });

  it('keeps a candidate match open and visible', () => {
    const doc = clone(example);
    const f = doc.findings.find((x) => x.review_match === 'applied')!;
    f.review_match = 'candidate';
    f.current_disposition = 'open';
    doc.summary.current.hidden_findings = 0;
    show(parseComparison(doc, RUN));
    expect(screen.queryByRole('checkbox', { name: /숨긴 발견/ })).toBeNull();
    const el = card(f.finding_id);
    expect(within(el).getByText(/확정할 수 없어 열린 상태로 둡니다/)).toBeTruthy();
  });

  it('records a review with a required reason, the version precondition and one key per request', async () => {
    const doc = clone(example);
    doc.can_review = true;
    let reply: Response | 'network' = 'network';
    const m = mockFetch(() => reply);
    const reload = show(parseComparison(doc, RUN), m.fetch);
    const el = card(open.finding_id);
    const form = within(el).getByRole('form', { name: '판정 기록' });
    fireEvent.change(within(form).getByLabelText('판정'), { target: { value: 'accepted_risk' } });
    fireEvent.click(within(form).getByRole('button', { name: '판정 기록' }));
    expect(await within(form).findByText('사유를 입력하세요.')).toBeTruthy();
    expect(m.calls).toHaveLength(0);

    fireEvent.change(within(form).getByLabelText('사유(필수)'), { target: { value: '  내부 전용 경로다 ' } });
    fireEvent.change(within(form).getByLabelText('적용 경로(선택)'), { target: { value: 'bad/' } });
    fireEvent.click(within(form).getByRole('button', { name: '판정 기록' }));
    await within(form).findByText(/같은 내용으로 다시 보내면 중복으로 기록되지 않습니다/);
    const first = m.calls[0]!;
    expect(first.method).toBe('POST');
    expect(first.url).toBe(`/api/v1/projects/${example.project_id}/finding-reviews`);
    expect(first.headers['if-none-match']).toBe('*');
    expect(first.headers['if-match']).toBeUndefined();
    expect(first.body).toEqual({
      fingerprint: open.fingerprint,
      disposition: 'accepted_risk',
      reason: '내부 전용 경로다',
      expires_at: null,
      path_prefix: 'bad/',
      run_id: RUN,
      revision: example.revision,
      finding_id: open.finding_id,
    });

    // The retry after a lost response reuses the key; a conflict re-reads the comparison.
    reply = apiError(409, 'CONFLICT');
    fireEvent.click(within(form).getByRole('button', { name: '판정 기록' }));
    expect(await within(form).findByText(/다른 사용자가 먼저 판정을 바꿨습니다/)).toBeTruthy();
    expect(m.calls[1]!.headers['idempotency-key']).toBe(first.headers['idempotency-key']);
    expect(reload).toHaveBeenCalledTimes(1);

    reply = json(201, { version: 1, disposition: 'accepted_risk', fingerprint: open.fingerprint });
    fireEvent.click(within(form).getByRole('button', { name: '판정 기록' }));
    expect(await within(form).findByText('판정을 기록했습니다.')).toBeTruthy();
    expect(reload).toHaveBeenCalledTimes(2);
  });

  it('sends If-Match with the current review version and no scope for open', async () => {
    const doc = clone(example);
    doc.can_review = true;
    const m = mockFetch(() => json(201, { version: 2, disposition: 'open', fingerprint: reviewed.fingerprint }));
    show(parseComparison(doc, RUN), m.fetch);
    fireEvent.click(screen.getByRole('checkbox', { name: /숨긴 발견/ }));
    const form = within(card(reviewed.finding_id)).getByRole('form', { name: '판정 기록' });
    expect((within(form).getByLabelText('판정') as HTMLSelectElement).value).toBe('open');
    expect(within(form).queryByLabelText('만료일(선택)')).toBeNull();
    fireEvent.change(within(form).getByLabelText('사유(필수)'), { target: { value: '다시 확인 필요' } });
    fireEvent.click(within(form).getByRole('button', { name: '판정 기록' }));
    await waitFor(() => expect(m.calls).toHaveLength(1));
    expect(m.calls[0]!.headers['if-match']).toBe(`"${reviewed.review!.version}"`);
    expect(m.calls[0]!.body).toMatchObject({ disposition: 'open', expires_at: null, path_prefix: null });
  });

  it('rejects a past expiry before sending', async () => {
    const doc = clone(example);
    doc.can_review = true;
    const m = mockFetch(() => json(500, {}));
    show(parseComparison(doc, RUN), m.fetch);
    const form = within(card(open.finding_id)).getByRole('form', { name: '판정 기록' });
    fireEvent.change(within(form).getByLabelText('사유(필수)'), { target: { value: '임시' } });
    fireEvent.change(within(form).getByLabelText('만료일(선택)'), { target: { value: '2020-01-01' } });
    fireEvent.click(within(form).getByRole('button', { name: '판정 기록' }));
    expect(await within(form).findByText('만료일은 오늘 이후여야 합니다.')).toBeTruthy();
    expect(m.calls).toHaveLength(0);
  });

  it('keeps run differences visible when the identity schemes differ', () => {
    const doc = clone(example) as Record<string, unknown> & typeof example;
    doc.comparable = false;
    (doc as Record<string, unknown>).not_comparable_reason = 'fingerprint_scheme_differs';
    (doc as Record<string, unknown>).differences = [{ key: 'builder_version', baseline: '2', current: '3' }, { key: 'tool:go.vet:version', baseline: null, current: 'go1.26' }];
    doc.entries = [];
    doc.counts = { new: 0, persisting: 0, changed: 0, absent_unverified: 0, resolved_observed: 0 };
    for (const f of doc.findings) (f as Record<string, unknown>).state = null;
    show(parseComparison(doc, RUN));
    expect(screen.getByText(/발견 식별 방식이 달라 비교하지 않았습니다/)).toBeTruthy();
    expect(screen.getByText('보고서 생성기 버전: 2 → 3')).toBeTruthy();
    expect(screen.getByText('도구 go.vet 버전: 없음 → go1.26')).toBeTruthy();
    expect(screen.queryByText('비교 결과(식별 단위)')).toBeNull();
  });

  it('explains a vanished finding hidden by an in-source suppression and shows a rule-context difference', () => {
    const doc = clone(example) as Record<string, unknown> & typeof example;
    const gone = doc.entries.find((e) => e.state === 'resolved_observed')!;
    gone.state = 'absent_unverified';
    (gone as Record<string, unknown>).reason = 'suppressed_in_source';
    doc.counts = { ...doc.counts, resolved_observed: 0, absent_unverified: 1 };
    (doc as Record<string, unknown>).differences = [
      { key: 'tool:go.staticcheck:rule_context', baseline: 'codeye-go-staticcheck@1 config=overridden suppressions=honored', current: 'codeye-go-staticcheck@2 config=overridden suppressions=reported' },
    ];
    show(parseComparison(doc, RUN));
    expect(screen.getByText('도구가 이 진단을 소스 안 억제로 숨겨진 것으로 보고했습니다(억제는 수정이 아닙니다).')).toBeTruthy();
    expect(screen.getByText(/^도구 go\.staticcheck 규칙 문맥: codeye-go-staticcheck@1 .* → codeye-go-staticcheck@2 /)).toBeTruthy();
    // An unknown future reason reaches the UI raw rather than failing the parse.
    (gone as Record<string, unknown>).reason = 'future_reason';
    cleanup();
    show(parseComparison(doc, RUN));
    expect(screen.getByText('future_reason')).toBeTruthy();
  });

  it('says when there is no baseline instead of inventing states', () => {
    const doc = clone(example) as Record<string, unknown> & typeof example;
    (doc as Record<string, unknown>).baseline = null;
    doc.comparable = false;
    (doc as Record<string, unknown>).not_comparable_reason = 'no_baseline';
    doc.entries = [];
    doc.counts = { new: 0, persisting: 0, changed: 0, absent_unverified: 0, resolved_observed: 0 };
    for (const f of doc.findings) (f as Record<string, unknown>).state = null;
    show(parseComparison(doc, RUN));
    expect(screen.getByText(/비교할 이전 게시 보고서가 없습니다/)).toBeTruthy();
    expect(screen.queryByText('＝ 계속 남음')).toBeNull();
  });
});
