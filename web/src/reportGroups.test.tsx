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
import { SECONDARY_NOT_FOR_GO, SECONDARY_SEGMENTS, gapGroups, overviewFacts, places, projectReadings, ruleGroups, secondaryPath, secondaryReviewPath, severityOf, slotRuns } from './reportGroups';

afterEach(cleanup);

function load(doc: unknown) {
  const d = structuredClone(doc) as Record<string, any>;
  d.demo = false;
  return parseAnalysisReport(d, d.run_id);
}

describe('summary grouping', () => {
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
      ['s001', 's003', 'succeeded'],
      ['s004', 's006', 'budget_exhausted'],
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
    expect(ledger.modules.find((x) => x.id === 'go:store')).toMatchObject({ cls: 'secondary', secondary: 30, secondarySent: 17, secondaryNotReached: 13, budgetExhausted: 0 });
    const f = overviewFacts(r, r.sections!);
    expect(f.scope).toEqual({ primary: 9, primarySent: 6, secondary: 30, secondarySent: 17 });
    const { container } = render(<ReportDocument report={r} />);
    const section = container.querySelector('#report-section-code_review') as HTMLElement;
    const row = within(section).getByText(t.sections.ledger.secondaryModules(1)).closest('tr')!;
    expect(Array.from(row.querySelectorAll('td')).map((td) => td.textContent)).toEqual([
      t.sections.ledger.secondaryModules(1), t.sections.ledger.classes.secondary, '—', '30', '17', '20815', '17', '0', '0', '0', '0', '13', '0', '17',
    ]);
    // The module table holds the primary modules and the sum row only.
    expect(Array.from(row.closest('tbody')!.querySelectorAll('tr')).map((tr) => tr.cells[0].textContent)).toEqual(['go:api', 'javascript:web', 'python:worker', t.sections.ledger.secondaryModules(1)]);
    // A secondary class with secondary counts on a builder 7 ledger is invalid.
    const bad = structuredClone(doc);
    for (const x of bad.code_review.ledger.modules) delete x.class;
    expect(() => load(bad)).toThrow();
  });

  // A slot is admitted while 6 calls are left and uses 2: the example used 10
  // of 15 and three primary slots were refused, so 10 + 2·2 + 6 = 20 admit them.
  it('says how many calls the refused slots need', () => {
    const r = load(withSlices);
    const ledger = r.sections!.codeReview.ledger!;
    const project = { analysis: null } as never;
    const hints = improvementHints(r.sections!.gaps, null, project, ledger);
    expect(hints.find((h) => h.key === 'budget')!.text).toBe(ko.improve.budgetCalls(10, 3, 20));
    expect(improvementHints(r.sections!.gaps, null, project).find((h) => h.key === 'budget')!.text).toBe(ko.improve.budget);
    const many = { ...ledger, callsUsed: 99, primarySlotsRefused: 60 };
    expect(improvementHints(r.sections!.gaps, null, project, many).find((h) => h.key === 'budget')!.text).toBe(ko.improve.budgetCallsMax(99, 60, 99 + 118 + 6));
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
