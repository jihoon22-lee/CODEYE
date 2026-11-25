// Grouping of the summary view (docs/REPORT.md#내보내기-상세도): the same
// groups and order as the summary Markdown and HTML (internal/report/
// render_summary.go). Everything is computed from the stored report.
import type { AnalysisReport, Evidence, Finding, Observation } from './analysisReport';
import type { Citation, Ledger, Sections, StaticTool } from './parseReportSections';
import { reportKo as t } from './locales/report-ko';
import { label } from './format';

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
export const sevRank = (s: string) => SEVERITY[s] ?? 0;

// The validation class every ranked or cut finding list orders by before
// severity (report.ValidationClass): reproduced, supported, a diagnostic
// triage problem not yet confirmed, any other unconfirmed finding, refuted.
export function validationClass(f: { id: string; validation: string }): number {
  switch (f.validation) {
    case 'reproduced': return 0;
    case 'supported': return 1;
    case 'unreviewed':
    case 'inconclusive': return /^review_t[0-9]{3}_/.test(f.id) ? 2 : 3;
  }
  return 4;
}

// RuleGroup is the diagnostics of one rule of one analyzer in one language.
export type RuleGroup = { analyzer: string; rule: string; count: number; severity: string; message: string | null; primary: string[]; secondary: string[]; observations: string[] };

export function regularStaticLanguages(sections: Pick<Sections, 'staticAnalysis' | 'metrics' | 'secrets'>) {
  const specialized = new Set([...sections.metrics.tasks, ...sections.secrets.tasks]);
  return sections.staticAnalysis.languages.map(({ ecosystem, tools }) => ({
    ecosystem,
    tools: tools.filter(tool => !specialized.has(tool.task) && (tool.purpose ?? 'diagnostics') === 'diagnostics'),
  })).filter(language => language.tools.length > 0);
}

export function acceptedStaticResult(tool: StaticTool) {
  return tool.status === 'succeeded' && tool.result === 'accepted';
}

// ruleGroups groups a language's diagnostics (not suppressed, not metrics)
// by analyzer and rule. Higher severity comes first, then main source locations.
export function ruleGroups(tools: StaticTool[], byID: Map<string, Observation>, severityOf: Map<string, string>): RuleGroup[] {
  const order = new Map<string, number>();
  const groups = new Map<string, RuleGroup & { order: number; sev: number; main: boolean }>();
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
        g = { analyzer, rule: o.rule, count: 0, severity: '', sev: -1, message: o.message, primary: [], secondary: [], observations: [], order: order.get(analyzer)!, main: false };
        groups.set(key, g);
      }
      g.count++;
      g.observations.push(id);
      const sev = severityOf.get(id) ?? o.severity ?? '';
      if (sevRank(sev) > g.sev) {
        g.severity = sev;
        g.sev = sevRank(sev);
      }
      const loc = o.path ?? '—';
      if (o.location && o.evidenceState === 'verified_against_source_manifest' && !secondaryReviewPath(o.location.path)) g.main = true;
      if (o.path && secondaryPath(locationPath(o.path))) g.secondary.push(loc);
      else g.primary.push(loc);
    }
  }
  return [...groups.values()]
    .sort((a, b) => b.sev - a.sev || Number(b.main) - Number(a.main) || b.count - a.count || a.order - b.order || (a.rule < b.rule ? -1 : a.rule > b.rule ? 1 : 0))
    .map(({ analyzer, rule, count, severity, message, primary, secondary, observations }) => ({ analyzer, rule, count, severity, message, primary, secondary, observations }));
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
  return secondaryPath(path) || segments.some(dir => ['test', 'tests', '__tests__', 'spec', 'specs', 'e2e', 'integration-tests', 'unit-tests', 'fuzz', 'fuzzer', 'fuzzers', 'fuzzing'].includes(dir)) ||
    /_test\.(go|py|rs)$|\.(test|spec)\.(js|jsx|ts|tsx|mjs|cjs|mts|cts)$|^test_.*\.py$|^(test|qa)[_-].*\.(sh|bash)$/.test(file);
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

export type Decision = { finding: Finding; source: LocatedEvidence; condition?: string; limitation?: string; next?: string };
const hiddenDispositions = new Set(['accepted_risk', 'false_positive', 'suppressed', 'fixed_by_user']);

function findingIssueContext(finding: Finding) {
  return JSON.stringify([finding.category, finding.validation, finding.disposition]);
}

function findingCodeRefs(finding: Finding) {
  if (!finding.evidence.length || finding.evidence.some(e => e.kind !== 'code' || !e.path || !e.lines)) return '';
  return JSON.stringify([findingIssueContext(finding), [...finding.locations].sort(), finding.evidence.map(e =>
    JSON.stringify([e.id, e.path, e.lines?.start, e.lines?.end])).sort()]);
}

// Matching locations alone cannot identify an issue. A model-only sibling can
// be linked to one observed rule when exact code evidence and title agree.
export function duplicateReadingFindings(findings: Finding[], observations: Observation[]): Set<string> {
  const rules = new Map(observations.map(o => [o.id, o.rule.trim().toLowerCase()]));
  const observed = new Map<string, Finding[]>();
  const seen = new Set<string>();
  const duplicates = new Set<string>();
  for (const finding of findings) {
    if (!finding.observations.length) continue;
    const ids = [...new Set(finding.observations)].sort();
    const key = JSON.stringify([findingIssueContext(finding), ids]);
    if (seen.has(key)) { duplicates.add(finding.id); continue; }
    seen.add(key);
    const evidence = findingCodeRefs(finding);
    if (!evidence || ids.length !== 1 || !rules.get(ids[0])) continue;
    const related = observed.get(evidence) ?? [];
    related.push(finding);
    observed.set(evidence, related);
  }
  const words = (title: string) => new Set(title.toLowerCase().split(/[^\p{L}\p{N}_]+/u).filter(Boolean));
  for (const finding of findings) {
    if (finding.observations.length) continue;
    const sources = observed.get(findingCodeRefs(finding));
    if (!sources || sources.length !== 1) continue;
    const own = words(finding.title);
    const prior = words(sources[0].title);
    const rule = rules.get(sources[0].observations[0]);
    const shared = [...own].filter(word => prior.has(word)).length;
    if (rule && own.has(rule) && prior.has(rule) && shared >= 3 && shared * 2 >= Math.max(own.size, prior.size)) duplicates.add(finding.id);
  }
  return duplicates;
}

export type ReportTone = 'ready' | 'partial' | 'attention' | 'failed' | 'neutral';
export type ReadingSummaryRow = { section: string; label: string; value: string; status?: string; tone?: ReportTone; count?: string };

export function reportStageTone(value: string | undefined): ReportTone {
  switch (value) {
    case 'succeeded': return 'ready';
    case 'partial': return 'partial';
    case 'failed': case 'cancelled': return 'failed';
    default: return 'neutral';
  }
}

export function testResultSummary(results: NonNullable<AnalysisReport['testResults']>) {
  if (results.status === 'failed' && results.tasks.some(task => task.failures.length)) return t.tests.testFailed;
  if (results.status === 'not_run' && results.tasks.every(task =>
    !task.attempted && !['succeeded', 'failed', 'timed_out', 'cancelled'].includes(task.status))) return t.tests.notStarted;
  return label(t.tests.summary, results.status);
}

export function readingSummary(report: AnalysisReport): ReadingSummaryRow[] {
  const recorded = report.findings.filter(f => !hiddenDispositions.has(f.disposition) &&
    (f.validation === 'supported' || f.validation === 'reproduced'));
  const duplicates = duplicateReadingFindings(recorded, report.observations);
  const actionable = recorded.filter(f => !duplicates.has(f.id));
  const secondary = actionable.filter(secondaryReviewFinding).length;
  const modules = report.sections?.codeReview.ledger?.modules ?? [];
  const eligible = modules.reduce((count, mod) => count + mod.eligible, 0);
  const requested = modules.reduce((count, mod) => count + mod.sent, 0);
  const accepted = eligible > 0 && modules.every(mod => typeof mod.accepted === 'number')
    ? modules.reduce((count, mod) => count + (mod.accepted ?? 0), 0) : null;
  const required = report.tasks.filter(task => task.required && !task.key.startsWith('test.'));
  const rows: ReadingSummaryRow[] = [];
  const sections = report.sections;
  const stage = (value: string | undefined) => value === undefined ? undefined : label(t.sections.states, value).replace(/^[^\p{L}\p{N}]+/u, '');
  const number = new Intl.NumberFormat('ko-KR');
  const fraction = (part: number, total: number) => number.format(part) + '/' + number.format(total);
  if (actionable.length > 0 || accepted !== null && accepted > 0) {
    rows.push({ section: 'code_review', label: t.reading.codeOutcome, status: stage(sections?.codeReview.stage.state), tone: reportStageTone(sections?.codeReview.stage.state), count: number.format(actionable.length - secondary), value: t.reading.actionCounts(actionable.length - secondary, secondary) });
  }
  if (eligible > 0) {
    const lines = sections?.codeReview.ledger?.files?.reduce((sum, file) => ({ accepted: sum.accepted + file.accepted, total: sum.total + file.total }), { accepted: 0, total: 0 });
    rows.push({ section: 'code_review', label: t.reading.modelScopeLabel, status: accepted === null ? t.reading.requestedState : accepted === eligible ? t.reading.availableState : t.reading.partialInvestigation,
      tone: accepted === null ? 'neutral' : accepted === eligible ? 'ready' : 'partial', count: fraction(accepted ?? requested, eligible), value: accepted !== null ? t.reading.modelScope(accepted, eligible, lines) : t.reading.modelSent(requested, eligible) });
  }
  const staticTools = sections ? regularStaticLanguages(sections).flatMap(language => language.tools) : [];
  if (staticTools.length > 0) {
    const successful = staticTools.filter(acceptedStaticResult);
    const observed = new Set(successful.flatMap(tool => tool.observations));
    const primary = report.observations.filter(o => observed.has(o.id) && !o.suppressed && !o.metric && o.location &&
      o.evidenceState === 'verified_against_source_manifest' && !secondaryReviewPath(o.location.path));
    rows.push({ section: 'static_analysis', label: t.sections.titles.static_analysis,
      status: stage(successful.length === staticTools.length ? 'succeeded' : successful.length > 0 ? 'partial' : sections?.staticAnalysis.stage.state),
      tone: successful.length === staticTools.length ? 'ready' : successful.length > 0 ? 'partial' : reportStageTone(sections?.staticAnalysis.stage.state), count: fraction(successful.length, staticTools.length),
      value: t.reading.staticResults(successful.length, staticTools.length) + ' · ' + t.reading.staticDiagnostics(primary.length) });
  }
  if (report.testResults) {
    const results = report.testResults;
    rows.push({ section: 'tests', label: t.reading.testsOutcome, tone: results.status === 'passed' ? 'ready' : results.status === 'failed' ? 'failed' : results.status === 'partial' ? 'partial' : 'neutral',
      value: testResultSummary(results) });
  }
  const incomplete = ['failed', 'timed_out', 'unsupported', 'skipped_policy'].flatMap(status => {
    const count = required.filter(task => task.status === status).length;
    return count ? [`${t.reading.outcomeStatus[status]} ${count}건`] : [];
  });
  if (incomplete.length > 0) rows.push({ section: 'gaps', label: t.reading.requiredOutcome, status: t.reading.incompleteState, tone: required.some(task => task.status === 'failed') ? 'failed' : 'attention', count: number.format(required.filter(task => ['failed', 'timed_out', 'unsupported', 'skipped_policy'].includes(task.status)).length), value: incomplete.join(' · ') });
  if (sections) {
    const d = sections.documents;
    const considered = d.comparisons.filter(c => c.status !== 'unknown').length;
    const unknown = d.comparisons.length - considered;
    const read = d.items.filter(item => item.status === 'selected').length;
    if (d.items.length || d.comparisons.length) rows.push({ section: 'documents', label: t.sections.titles.documents,
      status: unknown > 0 ? t.reading.documentsPending : read > 0 ? t.reading.documentsRead : stage(d.stage.state),
      tone: unknown > 0 ? 'attention' : reportStageTone(d.stage.state), count: number.format(read),
      value: t.reading.documentsOutcome(read, considered, unknown) });
    const mismatch = sections.environment.verdicts.filter(v => v.status === 'mismatch').length;
    if (sections.environment.modules.length) rows.push({ section: 'environment', label: t.sections.titles.environment,
      status: mismatch > 0 ? t.reading.environmentMismatch : stage(sections.environment.stage.state),
      tone: mismatch > 0 ? 'attention' : reportStageTone(sections.environment.stage.state), count: number.format(sections.environment.modules.length),
      value: t.reading.environmentOutcome(sections.environment.modules.length, mismatch) });
    if (sections.metrics.tasks.length) {
      const tone = reportStageTone(sections.metrics.stage.state);
      const status = tone === 'ready' ? t.reading.measuredState : stage(sections.metrics.stage.state);
      for (const { name, observations } of sections.metrics.groups) {
        const [label, value] = name === 'cyclomatic_complexity' ? [t.reading.complexityLabel, t.reading.complexityOutcome(observations.length)] :
          name === 'duplicated_lines' ? [t.reading.duplicationLabel, t.reading.duplicationOutcome(observations.length)] :
            [name, t.reading.metricOutcome(name, observations.length)];
        rows.push({ section: 'metrics', label, status, tone, value, count: number.format(observations.length) });
      }
      if (!sections.metrics.groups.length) rows.push({ section: 'metrics', label: t.sections.titles.metrics, status, tone, value: t.reading.metricsEmpty });
    }
    const graphs = sections.metrics.goDependencies ?? [];
    if (graphs.length) {
      const complete = graphs.every(graph => graph.status === 'complete');
      const available = graphs.some(graph => graph.status !== 'unavailable');
      const packages = graphs.reduce((count, graph) => count + graph.packages.length, 0);
      rows.push({ section: 'metrics', label: t.reading.goGraphLabel,
        status: complete ? t.reading.graphReady : available ? t.reading.graphPartial : t.reading.graphUnavailable,
        tone: complete ? 'ready' : available ? 'partial' : 'neutral',
        count: number.format(graphs.length), value: t.reading.graphOutcome(graphs.length, packages,
          graphs.some(graph => graph.scope === 'selected_packages')) });
    }
    const secrets = sections.secrets;
    if (secrets.tasks.length) {
      const count = secrets.detections.length;
      const state = secrets.stage.state;
      const complete = state === 'succeeded';
      const phase = stage(state);
      const values = [complete || count > 0 ? t.reading.secretsOutcome(count) : t.reading.secretsUndetermined];
      if (count > 0 && !complete && phase) values.push(phase);
      if (sections.gaps.some(gap => gap.section === 'secrets' && gap.code === 'history_not_scanned')) {
        values.push(t.reading.secretsWorkspaceScope);
      }
      rows.push({ section: 'secrets', label: t.sections.titles.secrets,
        status: count > 0 ? t.reading.secretsDetected : complete ? t.reading.secretsClear : phase,
        tone: count > 0 ? 'attention' : reportStageTone(state), count: number.format(count), value: values.join(' · ') });
    }
  }
  const flow = ['environment', 'documents', 'static_analysis', 'metrics', 'secrets', 'code_review', 'tests', 'gaps'];
  return rows.sort((a, b) => flow.indexOf(a.section) - flow.indexOf(b.section));
}

const genericTitleWords = new Set([
  '코드', '구현', '동작', '보류', '기능', '확인', '확인한다', '오류', '실패', '처리', '검사', '결과', '때문', '조건', '일부', '검증', '관리', '분기', '경로', '예외', '반환', '발생', '가능', '필요', '있음', '없음',
  '문서', '범위', '지원', '버전', '환경', '여부', '사용', '실제', '설정',
  'code', 'error', 'errors', 'failed', 'failure', 'failures', 'check', 'checks', 'checking', 'test', 'during', 'with', 'the', 'and', 'when', 'from', 'not', 'may', 'can', 'does', 'is', 'in', 'on', 'to', 'of', 'or',
  'for', 'after', 'before', 'handle', 'handling', 'result', 'state', 'case', 'path', 'paths',
  'document', 'documentation', 'support', 'supported', 'scope', 'version', 'environment', 'actual', 'configuration',
]);

function titleWords(title: string) {
  return (title.toLowerCase().match(/[\p{L}\p{Nd}_]+/gu) ?? [])
    .filter(word => [...word].length >= 2 && /\p{L}/u.test(word) && !genericTitleWords.has(word));
}

export function shareTitleSubject(a: string, b: string) {
  const subject = new Set(titleWords(a));
  return titleWords(b).some(word => subject.has(word));
}

// Match the earliest question that identifies exactly one tied comparison by
// its scope, cited document and subject. Fall back to the published order.
function questionComparison(documents: Sections['documents'], tied: Sections['documents']['comparisons']) {
  if (tied.length < 2) return tied[0];
  for (const question of documents.questions) {
    const matches = tied.filter(row => row.scope === question.appliesTo && shareTitleSubject(row.statement, question.question) &&
      documents.citations.some(c => primaryCitation(c, 'document') && row.documentRefs.includes(c.id) && question.evidence.includes(c.id)));
    if (matches.length === 1) return matches[0];
  }
  return tied[0];
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
  });
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

function findingLimitation(finding: Finding) {
  if (finding.validation !== 'supported' && finding.validation !== 'inconclusive') return;
  return finding.limitations.find(note => note.trim() && note !== '원문 생략(코드 미포함 내보내기).' && !/^[a-z][a-z0-9_]{0,63}$/.test(note));
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
    const limitation = report.includesCode === false ? undefined : findingLimitation(finding);
    if (finding.validation === 'supported' || finding.validation === 'reproduced') {
      actions.push({ finding, source, limitation });
    } else if (finding.validation === 'inconclusive' || finding.validation === 'unreviewed') {
      const next = nextVerification(finding.verification);
      if (next && basis) followups.push({ finding, source, condition: basis.statement, limitation, next });
    }
  }
  const severity = (a: Decision, b: Decision) => sevRank(b.finding.severity) - sevRank(a.finding.severity);
  const reproduced = (a: Decision) => Number(a.finding.validation === 'reproduced');
  actions.sort((a, b) => severity(a, b) || reproduced(b) - reproduced(a));
  followups.sort(severity);

  const { documents, codeReview } = sections;
  const cites = (catalog: Citation[], refs: string[], kind: 'document' | 'code') =>
    catalog.some(c => refs.includes(c.id) && primaryCitation(c, kind));
  const productRows = documents.comparisons.filter(row => row.statement.trim() && !secondaryReviewScope(row.scope) &&
    cites(documents.citations, row.documentRefs, 'document'));
  const hasProductCode = (row: typeof documents.comparisons[number]) => cites(documents.citations, row.codeRefs, 'code');
  const hasAdditionalCode = (row: typeof documents.comparisons[number]) =>
    Boolean(row.additional && cites(documents.citations, row.additional.codeRefs, 'code'));
  const comparisonCounts: { status: string; count: number }[] = [];
  const comparisons: Sections['documents']['comparisons'] = [];
  let unresolvedComparison: typeof documents.comparisons[number] | undefined;
  let exampleComparison: typeof documents.comparisons[number] | undefined;
  for (const status of ['mismatch', 'partial', 'match', 'unknown']) {
    const count = documents.comparisons.filter(row => row.status === status).length;
    if (count > 0) comparisonCounts.push({ status, count });
    const rows = productRows.filter(row => row.status === status && (status === 'unknown' || hasProductCode(row)));
    if (rows.length === 0) continue;
    if (status !== 'unknown') {
      comparisons.push(...rows.sort((a, b) => Number(Boolean(b.followup)) - Number(Boolean(a.followup))).slice(0, 2));
    } else if (comparisons.length === 0) {
      const observations = rows.filter(hasAdditionalCode);
      const cited = rows.filter(hasProductCode);
      const tied = observations.length > 0 ? observations : cited.length > 0 ? cited : rows;
      exampleComparison = tied[0];
      unresolvedComparison = report.includesCode === false ? tied[0] : questionComparison(documents, tied);
    }
  }
  const strengths = codeReview.strengths.filter(row => row.title.trim() && row.benefit.trim() && cites(codeReview.citations, row.refs, 'code'));

  let diagnostic: Observation | undefined;
  if (!unresolvedComparison && actions.length + comparisons.length + strengths.length + followups.length === 0) {
    const toolIDs = new Set(sections.staticAnalysis.languages.flatMap(lang => lang.tools)
      .filter(tool => (tool.purpose ?? 'diagnostics') === 'diagnostics').flatMap(tool => tool.observations));
    diagnostic = report.observations.find(o => {
      const loc = o.location;
      return toolIDs.has(o.id) && o.evidenceState === 'verified_against_source_manifest' && !o.suppressed && !o.metric &&
        loc && loc.path && loc.start > 0 && loc.end >= loc.start && !secondaryReviewPath(loc.path);
    });
  }
  return { actions: actions.slice(0, 3), comparisons: comparisons.slice(0, 3), unresolvedComparison, exampleComparison,
    comparisonCounts, strengths, followups: followups.slice(0, 2), diagnostic };
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
