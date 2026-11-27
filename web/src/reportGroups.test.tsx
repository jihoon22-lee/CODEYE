// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, fireEvent, render, within } from '@testing-library/react';
import sectioned from '../../examples/report.json';
import withSlices from '../../examples/report-slices.json';
import scope from '../../examples/review/slices/scope.json';
import { parseAnalysisReport } from './analysisReport';
import { improvementHints } from './improve';
import { ko } from './locales/ko';
import { reportKo as t } from './locales/report-ko';
import { ReportDocument } from './ReportPanel';
import { actionPlan } from './reportActions';
import { SECONDARY_NOT_FOR_GO, SECONDARY_SEGMENTS, gapGroups, overviewFacts, places, projectReadings, readingSummary, ruleGroups, secondaryPath, secondaryReviewPath, severityOf, slotRuns } from './reportGroups';

afterEach(cleanup);

function load(doc: unknown) {
  const d = structuredClone(doc) as Record<string, any>;
  d.demo = false;
  return parseAnalysisReport(d, d.run_id);
}

describe('summary grouping', () => {
  it('links only recorded Go dependency graphs from the summary with their measured scope', () => {
    const report = load(sectioned);
    const metrics = report.sections!.metrics;
    metrics.goDependencies = [];
    const graph = () => readingSummary(report).find(row => row.label === t.reading.goGraphLabel);
    expect(graph()).toBeUndefined();
    metrics.goDependencies.push({
      task: 'go-vet', module: 'example.test/module', platform: 'linux/amd64',
      scope: 'selected_packages', status: 'partial', parsed: 2, ignored: 0,
      incomplete: 1, unresolved: 1, unparsed: 0, limitations: ['go_package_scope_limited'],
      packages: ['example.test/module/main', 'example.test/module/worker'], edges: [],
    });
    expect(graph()).toMatchObject({ section: 'metrics', status: t.reading.graphPartial, count: '1',
      value: t.reading.graphOutcome(1, 2, true) });
  });

  it.each(['16', '17'])('distinguishes detections from successful screening in builder %s reports', version => {
    // Builder 22 added the diagnostic triage and builder 26 the lead
    // follow-ups; an older revision has neither.
    const report = load({ ...sectioned, builder: { ...sectioned.builder, version },
      code_review: { ...sectioned.code_review, diagnostic_triage: undefined, lead_followups: undefined } });
    const secrets = report.sections!.secrets;
    const row = () => readingSummary(report).find(result => result.section === 'secrets');
    expect(row()).toMatchObject({ status: t.reading.secretsDetected, tone: 'attention', count: '1',
      value: t.reading.secretsOutcome(1) + ' · ' + t.reading.secretsWorkspaceScope });
    secrets.stage.state = 'partial';
    expect(row()).toMatchObject({ status: t.reading.secretsDetected, tone: 'attention',
      value: expect.stringContaining('일부 수행') });
    secrets.detections = [];
    expect(row()).toMatchObject({ status: '일부 수행', tone: 'partial',
      value: t.reading.secretsUndetermined + ' · ' + t.reading.secretsWorkspaceScope });
    secrets.stage.state = 'failed';
    expect(row()).toMatchObject({ status: '실패', tone: 'failed' });
    secrets.stage.state = 'skipped';
    expect(row()).toMatchObject({ status: '제외', tone: 'neutral' });
    secrets.stage.state = 'succeeded';
    expect(row()).toMatchObject({ status: t.reading.secretsClear, tone: 'ready', count: '0' });
    report.sections!.gaps = [];
    expect(row()?.value).toBe(t.reading.secretsOutcome(0));
    secrets.tasks = [];
    expect(row()).toBeUndefined();
  });

  // The screen classifies paths with a copy of the default secondary rule;
  // the scope artifact records the rule the worker used.
  it('uses the secondary path rule the review scope records', () => {
    expect(SECONDARY_SEGMENTS).toEqual(scope.priority.secondary.segments);
    expect(SECONDARY_NOT_FOR_GO).toEqual(scope.priority.secondary.not_for_go);
    expect(secondaryPath('fixtures/go/a.go')).toBe(true);
    expect(secondaryPath('internal/Tests/Data/x.py')).toBe(true);
    expect(secondaryPath('tests/x.py')).toBe(false);
    expect(secondaryPath('cmd/build/main.go')).toBe(false);
    expect(secondaryPath('web/build/app.js')).toBe(true);
    // The file name is not a directory.
    expect(secondaryPath('internal/examples')).toBe(false);
    expect(secondaryReviewPath('internal/examples/demo.go')).toBe(true);
    expect(secondaryReviewPath('cmd/build/main.go')).toBe(false);
    expect(secondaryReviewPath('cmd/run_test.go')).toBe(true);
    expect(secondaryReviewPath('service/test_worker.py')).toBe(true);
    expect(secondaryReviewPath('web/MyComponent.spec.tsx')).toBe(true);
    expect(secondaryReviewPath('service/tests/load.go')).toBe(true);
  });

  it('omits secondary, unanchored and suppressed candidates from the project opening', () => {
    const report = load(sectioned);
    const sections = report.sections!;
    const supported = report.findings.find(f => f.validation === 'supported')!;
    const alternate = (id: string, fields: Partial<typeof supported>) => ({ ...supported, id, ...fields });
    report.findings.push(
      alternate('sample-only', { evidence: supported.evidence.map(e => ({ ...e, path: 'examples/charges.go' })) }),
      alternate('test-only', { evidence: supported.evidence.map(e => ({ ...e, path: 'charges_test.go' })) }),
      alternate('counter-only', { counter: supported.evidence.map(e => e.id) }),
      alternate('accepted-risk', { disposition: 'accepted_risk' }),
      alternate('unanchored', { validation: 'inconclusive', conditions: [], claims: [] }),
      alternate('reproduced', { validation: 'reproduced' }),
    );
    sections.codeReview.findings.push('sample-only', 'test-only', 'counter-only', 'accepted-risk', 'unanchored', 'reproduced');
    const rows = projectReadings(report, sections);
    expect(rows.actions.map(r => r.finding.id)).toEqual(['reproduced', supported.id]);
    expect(rows.followups).toHaveLength(0);
  });

  // The project opening reads the report's own decision, not a second path
  // guess: a hypothesis the report marked is no action whatever evidence the
  // verification added or whatever the path heuristic says, and a candidate
  // that was not sent to verification is no follow-up.
  it('keeps findings the report held back out of the project opening', () => {
    const report = load(sectioned);
    const sections = report.sections!;
    const supported = report.findings.find(f => f.validation === 'supported')!;
    const alternate = (id: string, fields: Partial<typeof supported>) => ({ ...supported, id, ...fields });
    // Its own citations are on test paths, the verification added shipped code.
    const verifiedWithShipped = alternate('hypothesis-shipped-support', { limitations: ['hypothesis_not_shipped'], severity: 'high' });
    // benches/ is not shipped code by the report's rule; the screen's guess does not know it.
    const benchmark = alternate('hypothesis-benchmark', { limitations: ['hypothesis_not_shipped'], severity: 'high',
      evidence: supported.evidence.map(e => ({ ...e, path: 'benches/charges.go' })) });
    const followup = alternate('candidate-not-shipped', { validation: 'unreviewed', limitations: ['candidate_not_shipped'] });
    const pending = alternate('hypothesis-pending', { validation: 'inconclusive', limitations: ['hypothesis_not_shipped'] });
    const ordinary = alternate('ordinary', { severity: 'low' });
    report.findings.push(verifiedWithShipped, benchmark, followup, pending, ordinary);
    sections.codeReview.findings.push(verifiedWithShipped.id, benchmark.id, followup.id, pending.id, ordinary.id);
    const rows = projectReadings(report, sections);
    expect(rows.actions.map(r => r.finding.id)).toContain(supported.id);
    expect(rows.actions.map(r => r.finding.id)).not.toContain('hypothesis-shipped-support');
    expect(rows.actions.map(r => r.finding.id)).not.toContain('hypothesis-benchmark');
    expect(rows.followups.map(r => r.finding.id)).not.toContain('candidate-not-shipped');
    expect(rows.followups.map(r => r.finding.id)).not.toContain('hypothesis-pending');
    // The same findings never rank among the actions of the action plan or the reading summary.
    expect(actionPlan(report, sections).actions.map(f => f.id)).not.toContain('hypothesis-benchmark');
  });

  it('labels example and test findings together in the code review section', () => {
    const report = load(sectioned);
    const supported = report.findings.find(f => f.validation === 'supported')!;
    const example = { ...supported, id: 'example-only', title: '예제의 인자 검증', observations: [], evidence: supported.evidence.map(e => ({ ...e, path: 'examples/charges.go' })) };
    const test = { ...supported, id: 'test-only', title: '시험의 예상값', validation: 'inconclusive', observations: [], evidence: supported.evidence.map(e => ({ ...e, path: 'charges_test.go' })) };
    report.findings.push(example, test);
    report.sections!.codeReview.findings.push(example.id, test.id);
    const { container } = render(<ReportDocument report={report} />);
    if (report.project) fireEvent.click(within(container).getByRole('tab', { name: t.sections.titles.code_review }));
    const section = within(container.querySelector('#report-section-code_review') as HTMLElement);
    const selected = section.getByRole('button', { name: supported.title });
    for (const title of [example.title, test.title]) {
      const row = section.getByText(title, { selector: 'button.report-finding-select' }).closest('tr')!;
      expect(within(row).getByText(t.reading.secondary)).toBeTruthy();
      expect(row.dataset.active).toBe('false');
    }
    fireEvent.click(selected);
    const detail = section.getByRole('region', { name: t.evidence });
    expect(within(detail).getByRole('heading', { name: supported.title })).toBe(document.activeElement);
    expect(selected.getAttribute('aria-pressed')).toBe('true');
    fireEvent.click(within(detail).getByRole('button', { name: new RegExp(t.codeReading.resultList) }));
    expect(selected.getAttribute('aria-pressed')).toBe('false');
  });

  it('counts every diagnostic once in the rule groups and shows five places with the rest counted', () => {
    const r = load(sectioned);
    const byID = new Map(r.observations.map((o) => [o.id, o]));
    const sa = r.sections!.staticAnalysis;
    const sevOf = severityOf(r.findings, sa.languages.flatMap((l) => l.tools.flatMap((x) => x.findings)));
    let groups = 0;
    for (const lang of sa.languages) {
      const diagnostics = lang.tools
        .filter((x) => (x.purpose ?? 'diagnostics') === 'diagnostics')
        .flatMap((x) => x.observations)
        .filter((id) => !byID.get(id)!.suppressed && !byID.get(id)!.metric);
      const g = ruleGroups(lang.tools, byID, sevOf);
      groups += g.length;
      expect(g.reduce((n, x) => n + x.count, 0)).toBe(diagnostics.length);
      for (const x of g) {
        expect(x.primary.length + x.secondary.length).toBe(x.count);
        const p = places(x.primary, x.secondary);
        expect(p.shown.length + p.more).toBe(x.count);
        expect(p.shown.length).toBeLessThanOrEqual(5);
      }
    }
    expect(groups).toBeGreaterThan(0);
    // Primary places first; without any, two secondary ones.
    expect(places(['a', 'b', 'c', 'd', 'e', 'f'], ['x'])).toEqual({ shown: ['a', 'b', 'c', 'd', 'e'], more: 2, moreSecondary: 1 });
    expect(places([], ['x', 'y', 'z', 'u', 'v', 'w'])).toEqual({ shown: ['x', 'y'], more: 4, moreSecondary: 4 });
    expect(places(['a'], ['x'])).toEqual({ shown: ['a', 'x'], more: 0, moreSecondary: 0 });
  });

  it('folds slots into runs and gaps into groups without losing one', () => {
    const r = load(withSlices);
    const ledger = r.sections!.codeReview.ledger!;
    const runs = slotRuns(ledger);
    expect(runs.map((x) => [x.first, x.last, x.status])).toEqual([
      ['s001', 's005', 'succeeded'],
      ['s006', 's009', 'budget_exhausted'],
    ]);
    const gaps = r.sections!.gaps;
    expect(gapGroups(gaps).reduce((n, g) => n + g.subjects.length, 0)).toBe(gaps.length);
    const facts = overviewFacts(r, r.sections!);
    const sent = ledger.modules.reduce((n, m) => n + m.sent, 0);
    expect(facts.scope!.primarySent + facts.scope!.secondarySent).toBe(sent);
  });

  // A builder 8 ledger with a secondary module (as in a repository with test
  // input): it parses, the module goes into the sum row and its secondary
  // files left out within the ceiling have their own column.
  it('reads a ledger with a secondary module and sums it in one row', () => {
    const doc = structuredClone(withSlices) as Record<string, any>;
    const m = doc.code_review.ledger.modules.find((x: { module_id: string }) => x.module_id === 'go:store');
    Object.assign(m, { class: 'secondary', files_secondary: m.files_eligible, files_secondary_sent: m.files_sent_to_model, files_secondary_not_reached: m.files_budget_exhausted, files_budget_exhausted: 0 });
    const r = load(doc);
    const ledger = r.sections!.codeReview.ledger!;
    expect(ledger.modules.find((x) => x.id === 'go:store')).toMatchObject({ cls: 'secondary', secondary: m.files_eligible,
      secondarySent: m.files_sent_to_model, secondaryNotReached: m.files_secondary_not_reached, budgetExhausted: 0 });
    const f = overviewFacts(r, r.sections!);
    expect(f.scope).toEqual({ primary: 9, primarySent: 9, secondary: m.files_eligible, secondarySent: m.files_sent_to_model });
    const { container } = render(<ReportDocument report={r} />);
    const section = container.querySelector('#report-section-code_review') as HTMLElement;
    const row = within(section).getByText(t.sections.ledger.secondaryModules(1)).closest('tr')!;
    expect(Array.from(row.querySelectorAll('td .report-cell-value')).map((cell) => cell.textContent)).toEqual([
      t.sections.ledger.secondaryModules(1), t.sections.ledger.classes.secondary, '—',
      ...[m.files_eligible, m.files_sent_to_model, m.files_accepted, m.bytes_sent, m.files_truncated, m.files_withheld, m.files_unreadable, m.files_budget_exhausted,
        m.files_not_planned, m.files_secondary_not_reached, m.files_not_sent, m.files_with_completed_review].map(String),
    ]);
    // The module table holds the primary modules and the sum row only.
    expect(Array.from(row.closest('tbody')!.querySelectorAll('tr')).map((tr) => tr.cells[0].querySelector('.report-cell-value')!.textContent)).toEqual(['go:api', 'javascript:web', 'python:worker', t.sections.ledger.secondaryModules(1)]);
    // A secondary class with secondary counts on a builder 7 ledger is invalid.
    const bad = structuredClone(doc);
    for (const x of bad.code_review.ledger.modules) delete x.class;
    expect(() => load(bad)).toThrow();
  });

  // The line ledger admits a slot with two calls remaining.
  it('says how many calls the refused slots need', () => {
    const r = load(withSlices);
    const ledger = r.sections!.codeReview.ledger!;
    const project = { analysis: null } as never;
    const hints = improvementHints(r.sections!.gaps, null, project, ledger);
    expect(hints.find((h) => h.key === 'budget')!.text).toBe(ko.improve.budgetCalls(14, 4, 22));
    expect(improvementHints(r.sections!.gaps, null, project).find((h) => h.key === 'budget')!.text).toBe(ko.improve.budget);
    const many = { ...ledger, callsUsed: 99, primarySlotsRefused: 60 };
    expect(improvementHints(r.sections!.gaps, null, project, many).find((h) => h.key === 'budget')!.text).toBe(ko.improve.budgetCallsMax(99, 60, 99 + 118 + 2));
  });

  it('keeps analysis statistics and full gap rows behind keyboard-reachable disclosures', () => {
    const r = load(sectioned);
    const { container } = render(<ReportDocument report={r} />);
    expect(container.querySelector('.report-summary')).toBeNull();
    const stats = within(container).getByText(t.reading.operations).closest('details')!;
    stats.open = true;
    fireEvent(stats, new Event('toggle'));
    const lines = Array.from(container.querySelectorAll('.report-summary > li')).map((x) => x.textContent);
    const f = overviewFacts(r, r.sections!);
    expect(lines.some((x) => x!.startsWith(t.sections.summary.static(f.diagnostics.total, f.diagnostics.rules, f.diagnostics.tools)))).toBe(true);
    if (r.project) fireEvent.click(within(container).getByRole('tab',{name:t.sections.titles.gaps}));
    const gaps = container.querySelector('#report-section-gaps') as HTMLElement;
    const more = within(gaps).getByText(t.sections.summary.allGaps(r.sections!.gaps.length));
    // A native summary element: focusable and toggled by the keyboard.
    expect(more.tagName).toBe('SUMMARY');
    const details = more.closest('details') as HTMLDetailsElement;
    expect(details.querySelector('table')).toBeNull();
    details.open = true;
    fireEvent(details, new Event('toggle'));
    expect(details.querySelectorAll('tbody tr').length).toBe(r.sections!.gaps.length);
  });
});
