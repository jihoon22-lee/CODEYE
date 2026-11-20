// Grouping of the summary view (docs/REPORT.md#내보내기-상세도): the same
// groups and order as the summary Markdown and HTML (internal/report/
// render_summary.go). Everything is computed from the stored report.
import type { AnalysisReport, Evidence, Finding, Observation } from './analysisReport';
import type { Citation, Ledger, Sections, StaticTool } from './reportSections';
import { reportKo as t } from './locales/report-ko';

// The default secondary path rule of review priority (docs/REVIEW.md#검토-우선순위,
// record.DefaultSecondary). A test checks it against the rule the scope
// example records. Path-only: generated-file headers are not seen here.
export const SECONDARY_SEGMENTS = ['fixtures', 'testdata', 'test-data', 'tests/data', 'examples', 'samples', 'demo', 'vendor', 'third_party', 'node_modules', 'generated', 'gen', 'mocks', '__snapshots__', 'dist', 'build'];
export const SECONDARY_NOT_FOR_GO = ['build', 'dist'];

// secondaryPath reports a file path whose directories hold a secondary
// segment (case-insensitive; "tests/data" matches two consecutive directories).
export function secondaryPath(p: string): boolean {
  const dirs = p.toLowerCase().split('/').slice(0, -1);
  return SECONDARY_SEGMENTS.some((seg) => {
    if (p.toLowerCase().endsWith('.go') && SECONDARY_NOT_FOR_GO.includes(seg)) return false;
    const parts = seg.split('/');
    for (let i = 0; i + parts.length <= dirs.length; i++) {
      if (parts.every((x, j) => dirs[i + j] === x)) return true;
    }
    return false;
  });
}

// secondaryDir reports a module or document directory below a secondary path.
export function secondaryDir(dir: string): boolean {
  return dir !== '' && dir !== '.' && secondaryPath(dir.replace(/\/$/, '') + '/x');
}

// moduleDir is the directory of a module ID <ecosystem>:<dir>.
export function moduleDir(id: string): string {
  const i = id.indexOf(':');
  return i < 0 ? id : id.slice(i + 1);
}

// The file part of an observation location "path:line".
export function locationPath(loc: string): string {
  const i = loc.lastIndexOf(':');
  return i < 0 ? loc : loc.slice(0, i);
}

export const SHOWN = 5;

// Places: every location when there are at most SHOWN, else the first
// primary ones (or two secondary ones when there is none), how many more and
// how many of those are below a secondary path.
export type Places = { shown: string[]; more: number; moreSecondary: number };
export function places(primary: string[], secondary: string[]): Places {
  if (primary.length + secondary.length <= SHOWN) return { shown: [...primary, ...secondary], more: 0, moreSecondary: 0 };
  let shown = primary.slice(0, SHOWN);
  if (shown.length === 0) shown = secondary.slice(0, 2);
  const more = primary.length + secondary.length - shown.length;
  return { shown, more, moreSecondary: primary.length === 0 ? secondary.length - shown.length : secondary.length };
}

const SEVERITY: Record<string, number> = { critical: 5, high: 4, medium: 3, low: 2, info: 1 };
const sevRank = (s: string) => SEVERITY[s] ?? 0;

// RuleGroup is the diagnostics of one rule of one analyzer in one language.
export type RuleGroup = { analyzer: string; rule: string; count: number; severity: string; message: string | null; primary: string[]; secondary: string[] };

// ruleGroups groups a language's diagnostics (not suppressed, not metrics)
// by analyzer and rule, the .mNN tasks of an analyzer together: by analyzer,
// then groups with a primary location, highest severity, most diagnostics, rule.
export function ruleGroups(tools: StaticTool[], byID: Map<string, Observation>, severityOf: Map<string, string>): RuleGroup[] {
  const order = new Map<string, number>();
  const groups = new Map<string, RuleGroup & { order: number; sev: number }>();
  for (const x of tools) {
    if ((x.purpose ?? 'diagnostics') !== 'diagnostics') continue;
    const analyzer = x.analyzer ?? x.task;
    if (!order.has(analyzer)) order.set(analyzer, order.size);
    for (const id of x.observations) {
      const o = byID.get(id);
      if (!o || o.suppressed || o.metric) continue;
      const key = analyzer + '\u0000' + o.rule;
      let g = groups.get(key);
      if (!g) {
        g = { analyzer, rule: o.rule, count: 0, severity: '', sev: -1, message: o.message, primary: [], secondary: [], order: order.get(analyzer)! };
        groups.set(key, g);
      }
      g.count++;
      const sev = severityOf.get(id) ?? o.severity ?? '';
      if (sevRank(sev) > g.sev) {
        g.severity = sev;
        g.sev = sevRank(sev);
      }
      const loc = o.path ?? '—';
      if (o.path && secondaryPath(locationPath(o.path))) g.secondary.push(loc);
      else g.primary.push(loc);
    }
  }
  return [...groups.values()]
    .sort((a, b) => a.order - b.order || Number(b.primary.length > 0) - Number(a.primary.length > 0) || b.sev - a.sev || b.count - a.count || (a.rule < b.rule ? -1 : a.rule > b.rule ? 1 : 0))
    .map(({ analyzer, rule, count, severity, message, primary, secondary }) => ({ analyzer, rule, count, severity, message, primary, secondary }));
}

// severityOf maps each observation of a static finding to the finding's severity.
export function severityOf(findings: Finding[], ids: string[]): Map<string, string> {
  const out = new Map<string, string>();
  const want = new Set(ids);
  for (const f of findings) {
    if (!want.has(f.id)) continue;
    for (const o of f.observations) if (sevRank(f.severity) > sevRank(out.get(o) ?? '')) out.set(o, f.severity);
  }
  return out;
}

// ToolRow is one analyzer of a language: its tasks and the tasks that did not succeed.
export type ToolRow = { name: string; tasks: number; required: number; succeeded: number; diagnostics: number; purpose: string; failed: { task: string; status: string; reason: string | null; result: string }[] };
export function toolRows(tools: StaticTool[], byID: Map<string, Observation>): ToolRow[] {
  const rows = new Map<string, ToolRow>();
  for (const x of tools) {
    const name = (x.analyzer ?? x.task) + (x.tool ? ' (' + x.tool + ')' : '');
    let r = rows.get(name);
    if (!r) {
      r = { name, tasks: 0, required: 0, succeeded: 0, diagnostics: 0, purpose: x.purpose ?? 'diagnostics', failed: [] };
      rows.set(name, r);
    }
    r.tasks++;
    if (x.required) r.required++;
    if (x.status === 'succeeded' && x.result === 'accepted') r.succeeded++;
    else r.failed.push({ task: x.task, status: x.status, reason: x.reason, result: x.result });
    if (r.purpose === 'metrics') r.diagnostics += x.metrics;
    else if (r.purpose === 'secrets') r.diagnostics += x.secrets;
    else r.diagnostics += x.observations.filter((id) => { const o = byID.get(id); return o && !o.suppressed && !o.metric; }).length;
  }
  return [...rows.values()];
}

// findingSecondary reports a finding whose every location is below a secondary path.
export function findingSecondary(f: Finding): boolean {
  return f.locations.length > 0 && f.locations.every(secondaryPath);
}

// orderFindings puts findings on primary paths before the others, keeping the stored order.
export function orderFindings(findings: Finding[]): Finding[] {
  return [...findings.filter((f) => !findingSecondary(f)), ...findings.filter(findingSecondary)];
}

// Test files can corroborate product behavior, but never establish it by themselves.
// The report exports use the same scope and ordering for their opening view.
export function secondaryReviewPath(path: string): boolean {
  const segments = path.toLowerCase().split('/');
  const file = segments.pop() ?? '';
  return secondaryPath(path) || segments.some(dir => ['test', 'tests', '__tests__', 'spec', 'specs'].includes(dir)) ||
    /_test\.(go|py|rs)$|\.(test|spec)\.(js|jsx|ts|tsx|mjs|cjs|mts|cts)$|^test_.*\.py$/.test(file);
}

export function secondaryReviewScope(scope: string): boolean {
  return scope !== '.' && (secondaryReviewPath(scope) || secondaryReviewPath(scope + '/x'));
}

type LocatedEvidence = Evidence & Required<Pick<Evidence, 'path' | 'lines'>>;

function sourceEvidence(finding: Finding): LocatedEvidence[] {
  return finding.evidence.filter((e): e is LocatedEvidence =>
    (e.kind === 'code' || e.kind === 'test') && Boolean(e.path) && Boolean(e.lines && e.lines.start > 0 && e.lines.end >= e.lines.start),
  );
}

export function secondaryReviewFinding(finding: Finding): boolean {
  const evidence = sourceEvidence(finding);
  return evidence.length > 0
    ? evidence.every(e => e.kind === 'test' || secondaryReviewPath(e.path))
    : findingSecondary(finding);
}

export function primaryCitation(citation: Citation, kind: 'document' | 'code'): boolean {
  return citation.kind === kind && Boolean(citation.path) && citation.line > 0 && citation.end >= citation.line && !secondaryReviewPath(citation.path);
}

export type Decision = { finding: Finding; source: LocatedEvidence; condition?: string; next?: string };
const hiddenDispositions = new Set(['accepted_risk', 'false_positive', 'suppressed', 'fixed_by_user']);

const genericTitleWords = new Set([
  '일부', '조건', '동작', '결과', '검사', '검증', '확인', '관리', '처리', '분기', '경로', '실패', '오류', '예외', '반환', '발생', '가능', '필요', '있음', '없음',
  'the', 'and', 'for', 'after', 'before', 'from', 'with', 'when', 'error', 'errors', 'failure', 'failures', 'failed', 'check', 'checks', 'checking', 'handle', 'handling', 'result', 'state', 'case', 'path', 'paths', 'can', 'may',
]);

function titleWords(title: string) {
  return (title.toLowerCase().match(/[\p{L}\p{N}_]+/gu) ?? [])
    .filter(word => [...word].length >= 2 && /\p{L}/u.test(word) && !genericTitleWords.has(word));
}

export function shareTitleSubject(a: string, b: string) {
  const subject = new Set(titleWords(a));
  return titleWords(b).some(word => subject.has(word));
}

// A second matched promise may explain an option on the same named operation.
// Require the same cited range, since shared file or title words are weaker.
export function matchingDocumentCondition(
  first: Sections['documents']['comparisons'][number],
  rows: Sections['documents']['comparisons'],
  catalog: Citation[],
) {
  const names = (text: string) => [...text.matchAll(/\b([A-Za-z_$][A-Za-z0-9_$]{2,})\s*\(/g)].map(match => match[1]);
  const options = (text: string) => [...text.matchAll(/\b([A-Za-z_$][A-Za-z0-9_$]{2,})\x60?(?:가|이|를|을|은|는)?\s*(?:={1,3}\s*(?:true|false)\b|(?:is\s+)?(?:enabled|disabled)\b|활성화|비활성화|참이면|거짓이면)/g)].map(match => match[1]);
  const primaryCode = (row: typeof first) => catalog.find(citation => row.codeRefs.includes(citation.id) && primaryCitation(citation, 'code'));
  const lead = primaryCode(first);
  const operations = names(first.statement);
  if (!lead || operations.length === 0) return;
  return rows.find(row => {
    if (row.status !== 'match' || row.scope !== first.scope || row.statement === first.statement ||
      !catalog.some(citation => row.documentRefs.includes(citation.id) && primaryCitation(citation, 'document'))) return false;
    const code = primaryCode(row);
    return code?.path === lead.path && code.line === lead.line && code.end === lead.end &&
      names(row.statement).some(operation => operations.includes(operation)) &&
      options(row.statement).some(option => !first.statement.includes(option));
  })?.statement;
}

// Relate complementary outcomes on a concrete source path only when their titles name a shared subject.
export function relatedStrength(decision: Decision, strengths: Sections['codeReview']['strengths'], citations: Citation[]) {
  for (const strength of strengths) {
    if (!shareTitleSubject(decision.finding.title, strength.title)) continue;
    const citation = citations.find(c => c.path === decision.source.path && strength.refs.includes(c.id) && primaryCitation(c, 'code'));
    if (citation) return { strength, citation };
  }
}

function nextVerification(steps: string[]) {
  const observable = /목록|복원|출력|표시|응답|반환값|로그|\b(?:list|restore|output|display|response|return value|logs?)\b/i;
  const abstract = /호출자|계약|\b(?:caller|contract)\b/i;
  return steps.find(step => observable.test(step) && !abstract.test(step)) ?? steps.find(step => step.trim());
}

export function projectReadings(report: AnalysisReport, sections: Sections) {
  const reviewed = new Set(sections.codeReview.findings);
  const actions: Decision[] = [];
  const followups: Decision[] = [];
  for (const finding of report.findings) {
    if (!reviewed.has(finding.id) || hiddenDispositions.has(finding.disposition)) continue;
    const sources = sourceEvidence(finding).filter(e => e.kind === 'code' && !secondaryReviewPath(e.path) && !finding.counter.includes(e.id));
    const basis = [
      ...finding.conditions,
      ...finding.claims.map(claim => ({ statement: claim.assertion, evidence: claim.evidence })),
    ].find(row => row.statement.trim() && sources.some(e => row.evidence.includes(e.id)));
    const source = basis && sources.find(e => basis.evidence.includes(e.id));
    if (!source) continue;
    if (finding.validation === 'supported' || finding.validation === 'reproduced') {
      actions.push({ finding, source });
    } else if (finding.validation === 'inconclusive' || finding.validation === 'unreviewed') {
      const next = nextVerification(finding.verification);
      if (next && basis) followups.push({ finding, source, condition: basis.statement, next });
    }
  }
  const severity = (a: Decision, b: Decision) => sevRank(b.finding.severity) - sevRank(a.finding.severity);
  const reproduced = (a: Decision) => Number(a.finding.validation === 'reproduced');
  actions.sort((a, b) => severity(a, b) || reproduced(b) - reproduced(a));
  followups.sort(severity);

  const { documents, codeReview } = sections;
  const cites = (catalog: Citation[], refs: string[], kind: 'document' | 'code') =>
    catalog.some(c => refs.includes(c.id) && primaryCitation(c, kind));
  const hasComparisonEvidence = (row: typeof documents.comparisons[number]) =>
    cites(documents.citations, row.documentRefs, 'document') && cites(documents.citations, row.codeRefs, 'code');
  const comparisons: Sections['documents']['comparisons'] = [];
  for (const status of ['mismatch', 'partial', 'match']) {
    comparisons.push(...documents.comparisons.filter(row => row.status === status && hasComparisonEvidence(row)).slice(0, 2));
  }
  const uncertainComparison = comparisons.length === 0
    ? documents.comparisons.find(row => row.status === 'unknown' && hasComparisonEvidence(row))
    : undefined;
  const strengths = codeReview.strengths.filter(row => cites(codeReview.citations, row.refs, 'code'));

  let diagnostic: Observation | undefined;
  if (!uncertainComparison && actions.length + comparisons.length + strengths.length + followups.length === 0) {
    const toolIDs = new Set(sections.staticAnalysis.languages.flatMap(lang => lang.tools)
      .filter(tool => (tool.purpose ?? 'diagnostics') === 'diagnostics').flatMap(tool => tool.observations));
    diagnostic = report.observations.find(o => {
      const loc = o.location;
      return toolIDs.has(o.id) && o.evidenceState === 'verified_against_source_manifest' && !o.suppressed && !o.metric &&
        loc && loc.path && loc.start > 0 && loc.end >= loc.start && !secondaryReviewPath(loc.path);
    });
  }
  const documentedBehavior = !uncertainComparison && actions.length + comparisons.length + strengths.length + followups.length === 0
    ? documents.comparisons.find(row => row.status === 'unknown' && row.statement.trim() && row.statement !== t.sections.withheld &&
      cites(documents.citations, row.documentRefs, 'document'))
    : undefined;
  return { actions: actions.slice(0, 3), comparisons: comparisons.slice(0, 3), uncertainComparison, documentedBehavior, strengths, followups: followups.slice(0, 2), diagnostic };
}

// moduleSecondary: the ledger row's class, or for an earlier builder the path rule.
export function moduleSecondary(m: { id: string; cls: string | null }): boolean {
  return m.cls === 'secondary' || (m.cls === null && secondaryDir(moduleDir(m.id)));
}

// SlotRun is a run of consecutive slots of one state and reason (investigate tasks).
export type SlotRun = { first: string; last: string; n: number; status: string; reason: string | null; modules: string[] };
export function slotRuns(ledger: Ledger): SlotRun[] {
  const slots = new Map<string, { status: string; reason: string | null; modules: string[] }>();
  for (const m of ledger.modules) {
    for (const x of m.slices) {
      const match = /^review\.investigate\.(s\d{3})$/.exec(x.task);
      if (!match) continue;
      const row = slots.get(match[1]) ?? { status: x.status, reason: x.reason, modules: [] };
      if (!row.modules.includes(m.id)) row.modules.push(m.id);
      slots.set(match[1], row);
    }
  }
  const out: SlotRun[] = [];
  for (const [slot, x] of [...slots.entries()].sort(([a], [b]) => (a < b ? -1 : 1))) {
    const last = out[out.length - 1];
    if (last && last.status === x.status && last.reason === x.reason) {
      last.last = slot;
      last.n++;
      for (const m of x.modules) if (!last.modules.includes(m)) last.modules.push(m);
    } else out.push({ first: slot, last: slot, n: 1, status: x.status, reason: x.reason, modules: [...x.modules] });
  }
  return out;
}

// GapGroup is the gaps of one section, gap and detail.
export type GapGroup = { section: string; code: string; detail: string | null; subjects: string[] };
export function gapGroups(gaps: Sections['gaps']): GapGroup[] {
  const out: GapGroup[] = [];
  const at = new Map<string, GapGroup>();
  for (const g of gaps) {
    const key = g.section + '\u0000' + g.code + '\u0000' + (g.detail ?? '');
    let x = at.get(key);
    if (!x) {
      x = { section: g.section, code: g.code, detail: g.detail, subjects: [] };
      at.set(key, x);
      out.push(x);
    }
    x.subjects.push(g.subject ?? '—');
  }
  return out;
}

// Facts the overview sentences say, from the stored report.
export type OverviewFacts = {
  model: { total: number; supported: number; reproduced: number; inconclusive: number; refuted: number; unreviewed: number } | null;
  scope: { primary: number; primarySent: number; secondary: number; secondarySent: number } | null;
  diagnostics: { total: number; suppressed: number; secondary: number; rules: number; tools: number; requiredFailed: number };
  metrics: { observations: number; kinds: number };
  secrets: { detections: number; files: number };
  gaps: { total: number; kinds: number };
};
export function overviewFacts(report: AnalysisReport, sections: Sections): OverviewFacts {
  const byID = new Map(report.observations.map((o) => [o.id, o]));
  const cr = sections.codeReview;
  let model: OverviewFacts['model'] = null;
  if (cr.findings.length > 0) {
    const by = (v: string) => report.findings.filter((f) => cr.findings.includes(f.id) && f.validation === v).length;
    model = { total: cr.findings.length, supported: by('supported'), reproduced: by('reproduced'), inconclusive: by('inconclusive'), refuted: by('refuted'), unreviewed: by('unreviewed') };
  }
  let scope: OverviewFacts['scope'] = null;
  if (cr.ledger && cr.ledger.modules.length > 0) {
    scope = { primary: 0, primarySent: 0, secondary: 0, secondarySent: 0 };
    for (const m of cr.ledger.modules) {
      const legacy = m.cls === null && secondaryDir(moduleDir(m.id));
      const sec = legacy ? m.eligible : m.secondary;
      const secSent = legacy ? m.sent : m.secondarySent;
      scope.primary += m.eligible - sec;
      scope.primarySent += m.sent - secSent;
      scope.secondary += sec;
      scope.secondarySent += secSent;
    }
  }
  const diagnostics = { total: 0, suppressed: 0, secondary: 0, rules: 0, tools: 0, requiredFailed: 0 };
  const rules = new Set<string>();
  const tools = new Set<string>();
  for (const lang of sections.staticAnalysis.languages) {
    for (const x of lang.tools) {
      if (x.required && x.status !== 'succeeded') diagnostics.requiredFailed++;
      if ((x.purpose ?? 'diagnostics') !== 'diagnostics') continue;
      for (const id of x.observations) {
        const o = byID.get(id);
        if (!o || o.metric) continue;
        diagnostics.total++;
        if (o.suppressed) {
          diagnostics.suppressed++;
          continue;
        }
        const analyzer = x.analyzer ?? x.task;
        rules.add(analyzer + '\u0000' + o.rule);
        tools.add(analyzer);
        if (o.path && secondaryPath(locationPath(o.path))) diagnostics.secondary++;
      }
    }
  }
  diagnostics.rules = rules.size;
  diagnostics.tools = tools.size;
  const files = new Set(sections.secrets.detections.filter((d) => d.place !== '—').map((d) => locationPath(d.place)));
  return {
    model,
    scope,
    diagnostics,
    metrics: { observations: sections.metrics.groups.reduce((n, g) => n + g.observations.length, 0), kinds: sections.metrics.groups.length },
    secrets: { detections: sections.secrets.detections.length, files: files.size },
    gaps: { total: sections.gaps.length, kinds: new Set(sections.gaps.map((g) => g.section + '\u0000' + g.code)).size },
  };
}
