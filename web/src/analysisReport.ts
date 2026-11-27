// Validate the projection used by the report view before rendering any model or tool text.
import { excerptFits, parseSections, parseExcerpt, type Excerpt, type Sections } from './parseReportSections';
import { diagnosticFindingTitle } from './locales/report-ko';

export type Evidence = {
  id: string;
  kind: string;
  description: string;
  path?: string;
  lines?: { start: number; end: number };
  artifact?: string;
  excerpt?: Excerpt | null;
};
export type Finding = {
  id: string;
  // fp2 identity; absent in code-free exports and in reports from older builders.
  fingerprint: string | null;
  // The disposition stamped at publication (immutable); the current one comes from the comparison.
  disposition: string;
  title: string;
  displayTitle?: string;
  category: string;
  severity: string;
  validation: string;
  impact: string;
  claims: { assertion: string; kind: string; evidence: string[] }[];
  severityReason: string;
  conditions: { statement: string; status: string; evidence: string[] }[];
  recommendation: string;
  verification: string[];
  limitations: string[];
  evidence: Evidence[];
  counter: string[];
  // Snapshot paths of the finding's locations and its tool diagnostics.
  locations: string[];
  observations: string[];
  // The candidate a lead follow-up refined, its lead, the follow-up task and
  // the original title (builder 26; the omission text in a code-free export).
  refinement?: { task: string; candidate: string; lead: string; originalTitle: string };
};
export type Task = { key: string; status: string; reason: string | null; required?: boolean };
export type GoTestFailure = { name?: string; path: string; line: number };
export type CaseResults = { runner: string; passed: number; failed: number; skipped: number;
  cases: { name: string; status: string; path?: string; line?: number }[] };
type TestResult = Task & {
  cases?: CaseResults;
  attempted: boolean; directory: string | null; exit: number | null; result: 'passed' | 'failed' | 'unverified';
  failures: GoTestFailure[];
};
export type ObservationPlace = { path: string; start: number; end: number };
export type RelatedObservation = {
  kind: string; message: string | null; location: ObservationPlace | null; state: string; excerpt: Excerpt | null;
};
export type Observation = {
  id: string;
  rule: string;
  tool: string | null;
  message: string | null;
  path: string | null;
  location: ObservationPlace | null;
  state: string;
  evidenceState: string | null;
  // The tool's own severity, and whether an in-source suppression hid it.
  severity: string | null;
  suppressed: boolean;
  // Why the observation's file is not shipped code (test, example, benchmark,
  // generated, vendored; builder 23); null for shipped code and older reports.
  pathClass?: string | null;
  related?: RelatedObservation[];
  // Structure or duplication measurement (never a finding).
  metric: { name: string; value: number; threshold: number; unit: string; counterpart?: ObservationPlace; excerpt?: Excerpt | null; counterpartExcerpt?: Excerpt | null } | null;
};
export type AnalysisReport = {
  project?: {name: string; source: string | null; start: string | null; finish: string | null};
  commit?: string | null;
  sourceBase?: string;
  includesCode?: boolean;
  published?: string | null;
  runID: string;
  revision: number;
  snapshot: string;
  status: string;
  verdict: string;
  aiReview: string;
  aiReviewReasons: string[];
  counts: {
    total: number;
    actionable: number;
    pending: number;
    refuted: number;
    raw: number;
    unresolved: number;
  };
  // Observations of counts.raw that are structure metrics (omitted as 0).
  metricObservations: number;
  // Finding IDs to look at first (summary.priorities), in the builder's order.
  priorities: string[];
  scope: {
    inventory: number | null;
    processed: number | null;
    exposed: number | null;
  };
  calls: {
    role: string;
    requested?: boolean;
    purpose?: string;
    model: string;
    version: string;
    outcome: string;
    pack: string;
  }[];
  findings: Finding[];
  observations: Observation[];
  tasks: Task[];
  testResults?: { status: string; tasks: TestResult[] };
  limitations: string[];
  // Analysis sections; null for revisions published before they existed,
  // which keep the flat layout.
  sections: Sections | null;
};

const invalid = (): never => {
  throw new TypeError('invalid report');
};
function object(v: unknown): Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
    ? (v as Record<string, unknown>)
    : invalid();
}
function str(v: unknown): string {
  return typeof v === 'string' ? v : invalid();
}
function nullable(v: unknown): string | null {
  return v == null ? null : str(v);
}
function count(v: unknown): number {
  return typeof v === 'number' && Number.isSafeInteger(v) && v >= 0
    ? v
    : invalid();
}
function array<T>(v: unknown, fn: (value: unknown) => T): T[] {
  return Array.isArray(v) ? v.map(fn) : invalid();
}

function parseCaseResults(raw: unknown, includesCode: boolean): CaseResults {
  const entry = object(raw);
  const runner = str(entry.runner);
  const totals = object(entry.counts);
  const passed = count(totals.passed), failed = count(totals.failed), skipped = count(totals.skipped);
  const total = passed + failed + skipped;
  if (!['go','unittest','pytest','jest','vitest','node-test','playwright','cargo','nextest','ctest','maven','gradle'].includes(runner) ||
      total === 0 || total > 1_000_000 || !includesCode && entry.cases !== undefined) invalid();
  const remaining = new Map([['passed', passed], ['failed', failed], ['skipped', skipped]]);
  const cases = array(entry.cases ?? [], v => {
    const item = object(v);
    const name = str(item.name), status = str(item.status);
    const path = item.path === undefined ? undefined : str(item.path);
    const line = item.line === undefined ? undefined : count(item.line);
    const available = remaining.get(status) ?? 0;
    if (!name.trim() || name.trim() !== name || /\p{Cc}/u.test(name) || new TextEncoder().encode(name).length > 512 ||
        available < 1 || (path === undefined) !== (line === undefined) || path !== undefined && (!snapshotPath(path) || !line)) invalid();
    remaining.set(status, available - 1);
    return { name, status, path, line };
  });
  if (cases.length > 128) invalid();
  return { runner, passed, failed, skipped, cases };
}

// Accept the same HTTPS owner/repository form as the registered Git source.
// Parsing the original string prevents URL normalization from accepting a path
// that contains escaped delimiters or dot segments.
function pinnedSourceBase(source: string | null | undefined, commit: string | null, head: string | null) {
  if (!source || source.length > 2048 || !commit || ![40, 64].includes(commit.length) || /[^a-f0-9]/.test(commit)) return;
  const repo = /^https:\/\/([a-z0-9.-]+(?::[0-9]{1,5})?)\/([a-z0-9][a-z0-9._-]{0,99})\/([a-z0-9._-]{1,100})\/?$/i.exec(source);
  if (!repo || repo[0] !== source || repo[1].length > 253 || source.includes('..')) return;
  const [, authority, owner, repository] = repo;
  const name = repository.endsWith('.git') ? repository.slice(0, -4) : repository;
  if (!name || name === '.' || !/^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*(?::[0-9]{1,5})?$/i.test(authority)) return;
  try {
    const url = new URL(source);
    if (/^[0-9.]+$/.test(url.hostname) || (head && head.toLowerCase() !== `${owner}/${name}`.toLowerCase())) return;
    return `${url.origin}/${owner}/${name}/blob/${commit}`;
  } catch {
    return;
  }
}

function snapshotPath(path: string) {
  return !/[\\:\x00-\x1f\x7f]/.test(path) && new TextEncoder().encode(path).length <= 4096 &&
    path.split('/').every(part => part && part !== '.' && part !== '..');
}

export function sourceEvidenceURL(base: string | undefined, path: string, start: number, end: number) {
  if (!base || !Number.isSafeInteger(start) || !Number.isSafeInteger(end) || start < 1 || end < start || !snapshotPath(path)) return;
  try {
    return `${base}/${path.split('/').map(encodeURIComponent).join('/')}#L${start}-L${end}`;
  } catch {
    return;
  }
}
function evidence(v: unknown, expandedDocuments: boolean): Evidence {
  const e = object(v);
  const loc = e.location == null ? null : object(e.location);
  const kind = str(e.kind);
  const lines = loc ? { start: count(loc.start_line), end: count(loc.end_line) } : undefined;
  return {
    id: str(e.id),
    kind,
    description: str(e.description),
    path: loc ? str(loc.path) : undefined,
    lines,
    excerpt: parseExcerpt(e.excerpt, expandedDocuments && kind === 'document' ? lines : undefined),
    artifact: nullable(e.artifact_id) ?? undefined,
  };
}
// LATER_MARKERS are the limitation codes the report builder gave a meaning at
// a later builder: the first builder that writes them. In a revision of an
// older builder the same string is model text (the older builder did not strip
// it), so it is no marker. The Go reader applies the same rule
// (internal/report/validate.go, withoutLaterMarkers); a test reads the Go
// constants.
export const LATER_MARKERS: Record<string, number> = { candidate_not_shipped: 24, hypothesis_not_shipped: 25 };

export function withoutLaterMarkers(limitations: string[], builder: unknown): string[] {
  if (typeof builder !== 'string' || !/^[0-9]+$/.test(builder)) return limitations;
  const canonical = String(Number(builder)) === builder;
  return limitations.filter(note => !(note in LATER_MARKERS) || (canonical && Number(builder) >= LATER_MARKERS[note]));
}

function finding(v: unknown, expandedDocuments: boolean, builder: unknown): Finding {
  const f = object(v);
  return {
    id: str(f.id),
    fingerprint: nullable(f.fingerprint),
    disposition: str(f.disposition),
    title: str(f.title),
    category: str(f.category),
    severity: str(f.severity),
    validation: str(f.validation),
    impact: str(f.impact),
    severityReason: str(f.severity_reason),
    claims: array(f.claims, (v) => {
      const c = object(v);
      return {
        assertion: str(c.assertion),
        kind: str(c.kind),
        evidence: array(c.evidence_ids, str),
      };
    }),
    conditions: array(f.conditions, (v) => {
      const c = object(v);
      return { statement: str(c.statement), status: typeof c.status === 'string' ? c.status : '', evidence: array(c.evidence_ids ?? [], str) };
    }),
    recommendation: str(object(f.recommendation).summary),
    verification: array(f.verification, str),
    limitations: withoutLaterMarkers(array(f.limitations, str), builder),
    evidence: array(f.evidence, v => evidence(v, expandedDocuments)),
    counter: array(f.counterevidence_ids, str),
    locations: array(f.locations ?? [], (l) => str(object(l).path)),
    observations: array(f.observation_ids ?? [], str),
    ...(f.lead_refinement == null ? {} : { refinement: leadRefinement(f.lead_refinement, str(f.id), builder) }),
  };
}

// leadRefinement reads the lineage of a refined investigation finding
// (builder 26 on): the follow-up task of the finding's own slot, an
// investigation candidate and a lead, as internal/report validates it.
function leadRefinement(v: unknown, id: string, builder: unknown): NonNullable<Finding['refinement']> {
  const x = object(v);
  const out = { task: str(x.task_key), candidate: str(x.candidate_id), lead: str(x.lead), originalTitle: str(x.original_title) };
  const slot = /^review_(?:(s[0-9]{3})_)?investigate_[0-9]+$/.exec(id);
  if (Number(builder) < 26 || !slot || out.task !== (slot[1] ? 'review.leads.' + slot[1] : 'review.leads') ||
    !/^investigate_[1-9][0-9]*$/.test(out.candidate) || !/^lead_[1-9]$/.test(out.lead) || out.originalTitle === '') invalid();
  return out;
}

export function parseAnalysisReport(
  value: unknown,
  expectedID: string,
): AnalysisReport {
  const r = object(value);
  if (
    r.schema_version !== '0.1' ||
    r.demo !== false ||
    r.run_id !== expectedID ||
    !['completed', 'partial', 'failed', 'cancelled'].includes(str(r.status))
  )
    invalid();
  const s = object(r.summary);
  const counts = {
    total: count(s.total_findings),
    actionable: count(s.actionable_findings),
    pending: count(s.pending_candidates),
    refuted: count(s.refuted_findings),
    raw: count(s.raw_observations),
    unresolved: s.unresolved_observations === undefined && r.overview == null ? 0 : count(s.unresolved_observations),
  };
  const builderVersion = r.builder != null ? object(r.builder).version : undefined;
  const builder18 = typeof builderVersion === 'string' && /^(?:1[89]|[2-9][0-9]|[1-9][0-9]{2,})$/.test(builderVersion);
  const pathClasses = typeof builderVersion === 'string' && /^(?:2[3-9]|[3-9][0-9]|[1-9][0-9]{2,})$/.test(builderVersion);
  const includesCode = r.export != null && object(r.export).include_code === true;
  const expandedDocuments = builder18 && includesCode;
  const findings = array(r.findings, v => finding(v, expandedDocuments, builderVersion));
  let unresolved = 0;
  const observations = array(r.observations, (v) => {
    const o = object(v),
      loc = o.location == null ? null : object(o.location);
    if (o.evidence_state !== 'verified_against_source_manifest') unresolved++;
    const location = loc ? { path: str(loc.path), start: count(loc.start_line), end: count(loc.end_line) } : null;
    return {
      id: str(o.id),
      rule: str(o.rule_id),
      tool: nullable(o.tool),
      message: nullable(o.raw_message),
      state: str(o.location_state),
      evidenceState: nullable(o.evidence_state),
      path: location ? location.path + ':' + location.start : null,
      location,
      severity: nullable(o.raw_severity),
      suppressed: o.suppressed === true,
      pathClass: o.path_class === undefined ? null : (() => {
        const c = str(o.path_class);
        // Only builder 23 on, only for a located observation, one of the five classes.
        if (!pathClasses || !location || !['test', 'example', 'benchmark', 'generated', 'vendored'].includes(c)) invalid();
        return c;
      })(),
      related: array(o.related_locations ?? [], item => {
        const note = object(item);
        const kind = str(note.kind);
        const state = str(note.location_state);
        const excerpt = parseExcerpt(note.excerpt);
        const loc = note.location == null ? null : object(note.location);
        const position = loc && { path: str(loc.path), start: count(loc.start_line), end: count(loc.end_line) };
        if (!['reported_position', 'tool_note', 'condition_assumption', 'assumed_branch', 'tool_flow_step'].includes(kind) ||
          Boolean(position) !== (state === 'resolved') || loc && loc.snapshot_id !== object(r.inputs).snapshot_id ||
          position && (position.start < 1 || position.end < position.start) ||
          excerpt && (!position || !excerptFits(excerpt, position.start, position.end, false))) invalid();
        return { kind, message: nullable(note.raw_message), location: position, state, excerpt };
      }),
      metric: o.metric == null ? null : (() => {
        const m = object(o.metric);
        const name = str(m.name);
        const metric = { name, value: count(m.value), threshold: count(m.threshold), unit: str(m.unit),
          excerpt: parseExcerpt(m.excerpt), counterpartExcerpt: parseExcerpt(m.counterpart_excerpt) };
        if (m.counterpart === undefined) return metric;
        const other = object(m.counterpart);
        const counterpart = { path: str(other.path), start: count(other.start_line), end: count(other.end_line) };
        if (name !== 'duplicated_lines' || other.snapshot_id !== object(r.inputs).snapshot_id ||
          counterpart.start < 1 || counterpart.end < counterpart.start) invalid();
        return { ...metric, counterpart };
      })(),
    };
  });
  const metricObservations = s.metric_observations == null ? 0 : count(s.metric_observations);
  const priorities = array(s.priorities ?? [], str);
  if (
    !priorities.every((id) => findings.some((f) => f.id === id)) ||
    findings.length !== counts.total ||
    counts.raw !== observations.length ||
    (r.overview != null && counts.unresolved !== unresolved) ||
    metricObservations !== observations.filter((o) => o.metric !== null).length
  )
    invalid();
  const coverage = object(r.coverage);
  const aiReview = object(coverage.ai_review);
  const input = object(r.inputs);
  const commit = nullable(input.commit_sha);
  const project = r.project == null ? undefined : (() => {
    const p = object(r.project);
    return { name: str(p.name), source: nullable(p.source), start: nullable(p.started_at), finish: nullable(p.finished_at) };
  })();
  for (const observation of observations) {
    if (observation.related.length > 64 || observation.related.length > 0 &&
      (!['cpp.cppcheck', 'cpp.clang_tidy', 'cpp.gcc_analyzer', 'any.opengrep'].includes(observation.tool ?? '') ||
        observation.related.some(note => !includesCode && (note.message !== null || note.excerpt !== null)))) invalid();
    const metric: Observation['metric'] = observation.metric;
    if (!metric) continue;
    for (const [excerpt, place] of [[metric.excerpt, observation.location], [metric.counterpartExcerpt, metric.counterpart]] as const) {
      if (excerpt && (!includesCode || !place || observation.evidenceState !== 'verified_against_source_manifest' ||
        !excerptFits(excerpt, place.start, place.end, false))) invalid();
    }
  }
  const sections = parseSections(r, observations.map(o => o.id), findings.map(f => f.id), counts.total, includesCode, expandedDocuments);
  const observationByID = new Map(observations.map(o => [o.id, o]));
  for (const f of findings) {
    const o = observationByID.get(f.observations[0]);
    if (f.validation === 'unreviewed' && o?.tool && !o.metric) {
      f.displayTitle = diagnosticFindingTitle(f.title, o.tool, o.rule, o.message);
    }
  }
  const modelFindings = new Set(sections?.codeReview.findings);
  for (const finding of findings) {
    for (const evidence of finding.evidence) {
      if (evidence.excerpt && (!includesCode || !evidence.lines || (evidence.kind !== 'code' && evidence.kind !== 'document') ||
        !excerptFits(evidence.excerpt, evidence.lines.start, evidence.lines.end, modelFindings.has(finding.id)))) invalid();
    }
  }
  const head = r.pull_request == null ? null : nullable(object(r.pull_request).head_repository);
  const tasks = array(r.tasks, (v) => {
    const task = object(v);
    return { key: str(task.key), status: str(task.status), reason: nullable(task.reason), required: task.required_for_verdict === true };
  });
  const executions = new Map<string, Record<string, unknown>>();
  const caseResults = new Map<string, CaseResults>();
  const failedTests = new Map<string, GoTestFailure[]>();
  for (const execution of array(r.executions ?? [], object)) {
    if (execution.test_cases !== undefined) {
      const key = str(execution.task_key);
      if (input.profile !== 'with-tests' || !builder18 || Number(builderVersion) < 19 || !key.startsWith('test.') ||
          execution.name !== 'test.run' || execution.cleanup !== 'confirmed' || execution.status !== 'exited' ||
          execution.stdout_truncated !== false || execution.stderr_truncated !== false ||
          typeof execution.exit_code !== 'number' || !Number.isSafeInteger(execution.exit_code) || execution.exit_code < 0 ||
          !tasks.some(task => task.key === key) || executions.has(key)) invalid();
      caseResults.set(key, parseCaseResults(execution.test_cases, includesCode));
    }
    if (execution.name === 'test.run') {
      const key = str(execution.task_key);
      if (failedTests.has(key) || executions.has(key) && (caseResults.has(key) || execution.go_test_failures !== undefined)) invalid();
      executions.set(key, execution);
    }
    if (execution.go_test_failures === undefined) continue;
    const key = str(execution.task_key);
    const exit = execution.exit_code;
    const cwd = str(execution.cwd);
    if (input.profile !== 'with-tests' || coverage.test_status !== 'failed' || !builder18 ||
      execution.name !== 'test.run' || !/^test\.go(?:\.m[0-9]{2,})?$/.test(key) || execution.status !== 'exited' || execution.cleanup !== 'confirmed' ||
      typeof exit !== 'number' || !Number.isSafeInteger(exit) || exit < 1 || (cwd !== '.' && (cwd === '[REDACTED]' || !snapshotPath(cwd))) ||
      !tasks.some(task => task.key === key && task.status === 'failed' && ['test_command_failed', 'test_dependencies_unavailable'].includes(task.reason ?? '')) || failedTests.has(key)) invalid();
    const failures = array(execution.go_test_failures, value => {
      const record = object(value);
      const path = str(record.path);
      const line = count(record.line);
      const name = record.name === undefined ? undefined : str(record.name);
      const validName = name !== undefined && /^Test(?!\p{Ll})[\p{L}\p{Nd}_]*(?![\s\S])/u.test(name) && new TextEncoder().encode(name).length <= 128;
      if (!snapshotPath(path) || !path.endsWith('_test.go') || line < 1 || cwd !== '.' && !path.startsWith(cwd + '/') ||
        (includesCode ? !validName : name !== undefined)) invalid();
      return { name, path, line };
    });
    if (!failures.length || failures.length > 64 || includesCode &&
      new Set(failures.map(failure => JSON.stringify([failure.name, failure.path, failure.line]))).size !== failures.length) invalid();
    failedTests.set(key, failures);
  }
  const testResults = input.profile === 'with-tests' ? (() => {
    return {
      status: str(coverage.test_status),
      tasks: tasks.filter(task => task.key.startsWith('test.')).map((task): TestResult => {
        const e = executions.get(task.key);
        const exit = typeof e?.exit_code === 'number' && Number.isSafeInteger(e.exit_code) ? e.exit_code : null;
        const confirmed = e?.status === 'exited' && e.cleanup === 'confirmed' && exit !== null && exit >= 0;
        const result = confirmed && task.status === 'succeeded' && exit === 0 ? 'passed' :
          confirmed && task.status === 'failed' && (exit !== 0 || (caseResults.get(task.key)?.failed ?? 0) > 0) && task.reason !== 'no_tests_executed' ? 'failed' : 'unverified';
        const cwd = typeof e?.cwd === 'string' ? e.cwd : '';
        const directory = cwd === '.' || cwd && cwd !== '[REDACTED]' && !/[:\\\u0000-\u001f\u007f]/.test(cwd) &&
          cwd.split('/').every(segment => segment && segment !== '.' && segment !== '..') ? cwd : null;
        return { ...task, attempted: e !== undefined, directory, exit, result, failures: failedTests.get(task.key) ?? [], cases: caseResults.get(task.key) };
      }),
    };
  })() : undefined;
  function metric(key: string) {
    return coverage[key] == null ? null : count(coverage[key]);
  }
  return {
    scope: {
      inventory: metric('inventory_files'),
      processed: metric('tool_processed_files'),
      exposed: metric('llm_exposed_files'),
    },
    calls: array(coverage.review_calls ?? [], (v) => {
      const c = object(v);
      return {
        role: str(c.role),
        requested: typeof c.request_sha256 === 'string' && c.request_sha256.length > 0,
        purpose: nullable(c.purpose) ?? undefined,
        model: str(c.model),
        version: str(c.opencode_version),
        outcome: str(c.outcome),
        pack: str(c.pack_sha256),
      };
    }),
    project,
    commit,
    includesCode,
    sourceBase: includesCode && input.source_kind === 'git' ? pinnedSourceBase(project?.source, commit, head) : undefined,
    published: nullable(r.created_at),
    runID: expectedID,
    revision: count(r.revision),
    snapshot: str(input.snapshot_id),
    status: str(r.status),
    verdict: str(r.quality_verdict),
    aiReview: str(aiReview.status),
    aiReviewReasons: [
      ...array(aiReview.required_tasks ?? [], task => nullable(object(task).reason)),
      nullable(aiReview.reason),
    ].filter((reason): reason is string => Boolean(reason)),
    counts,
    metricObservations,
    priorities,
    findings,
    observations,
    tasks,
    testResults,
    limitations: array(r.limitations, str),
    sections,
  };
}

// Markdown and HTML are the summary view unless detail is 'full'
// (docs/REPORT.md#내보내기-상세도); JSON is always complete.
export function reportURL(
  id: string,
  format: string,
  includeCode: boolean,
  revision?: number,
  detail?: 'full',
) {
  const params = new URLSearchParams({
    format,
    include_code: String(includeCode),
  });
  if (revision !== undefined) params.set('revision', String(revision));
  if (detail) params.set('detail', detail);
  return (
    '/api/v1/runs/' + encodeURIComponent(id) + '/report?' + params.toString()
  );
}
