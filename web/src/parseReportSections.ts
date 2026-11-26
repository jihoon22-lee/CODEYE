// Analysis sections of reports from builder 5 on (docs/REPORT.md#분석-종류별-절).
// They reference observations and findings by ID. The parser checks that
// every observation and finding belongs to exactly one section and that the
// section counts add up to the summary, so the screen never shows numbers the
// stored document does not hold.
export type Excerpt = { start: number; end: number; text: string };
export type Citation = { id: string; kind: string; path: string; line: number; end: number; excerpt: Excerpt | null; directivePath?: string };
type AdditionalObservation = { reason: string; codeRefs: string[] };
export type RelatedWorkflow = { read: string; change: string; test: string };

const diagnosticExcerptLines = 9;
const modelExcerptLines = 50;
const excerptBytes = 1800;
const documentExcerptBytes = 2000;
const documentExcerptLines = 40;
const excerptEncoder = new TextEncoder();
const TEXT_OMITTED = '원문 생략(코드 미포함 내보내기).';

export function parseExcerpt(v: unknown, documentBounds?: { start: number; end: number }): Excerpt | null {
  if (v == null) return null;
  const x = object(v);
  const excerpt = { start: count(x.start_line), end: count(x.end_line), text: str(x.text) };
  const lines = excerpt.end - excerpt.start + 1;
  // Go's excerpt budget counts the JSON string, including HTML escaping.
  const encoded = JSON.stringify(excerpt.text).replace(/[<>&\u2028\u2029]/g,
    character => '\\u' + character.charCodeAt(0).toString(16).padStart(4, '0'));
  const size = excerptEncoder.encode(encoded).length;
  if (excerpt.start < 1 || lines < 1 || lines > modelExcerptLines || lines !== excerpt.text.split('\n').length ||
    !/\P{White_Space}/u.test(excerpt.text) || /[\u0000-\u0008\u000b-\u001f\u007f-\u009f]/.test(excerpt.text)) invalid();
  if (size > excerptBytes && (!documentBounds || size > documentExcerptBytes || lines > documentExcerptLines ||
    excerpt.start !== documentBounds.start || excerpt.end !== documentBounds.end)) invalid();
  return excerpt;
}

export function excerptFits(excerpt: Excerpt, start: number, end: number, model: boolean) {
  return excerpt.start <= start && start <= excerpt.end &&
    (excerpt.end - excerpt.start < diagnosticExcerptLines || model && excerpt.start === start && excerpt.end <= end);
}

function citation(v: unknown, includesCode: boolean, expandedDocuments: boolean): Citation {
  const x = object(v);
  const kind = str(x.kind), line = count(x.start_line), end = count(x.end_line);
  const bounds = expandedDocuments && includesCode && kind === 'document' ? { start: line, end } : undefined;
  const result = { id: str(x.id), kind, path: str(x.path), line, end, excerpt: parseExcerpt(x.excerpt, bounds) };
  if (result.excerpt && (!includesCode || !excerptFits(result.excerpt, result.line, result.end, true))) invalid();
  if (x.directive_path === undefined) return result;
  const directivePath = str(x.directive_path);
  const handbook = /\.(?:md|markdown)$/i.test(directivePath);
  const validDirective = result.path.endsWith('.py') && (handbook || /\.rst$/i.test(directivePath)) ||
    result.path.endsWith('.rs') && handbook;
  if (result.kind !== 'document' || !validDirective) {
    throw new Error('invalid source API documentation citation');
  }
  return { ...result, directivePath };
}
export type SectionStage = { state: string; tasks: number; succeeded: number };
export type ModelState = { task: string; performed: boolean; reason: string | null };
export type StaticTool = {
  task: string;
  required: boolean;
  status: string;
  reason: string | null;
  result: string;
  analyzer: string | null;
  tool: string | null;
  version: string | null;
  purpose: string | null;
  inScope: number | null;
  processed: number | null;
  rulePack: string | null;
  units: Record<string, number>;
  limitations: string[];
  paths?: string[];
  observations: string[];
  findings: string[];
  rules: { rule: string; count: number }[];
  metrics: number;
  secrets: number;
};
// One dependency task (deps.go, deps.go.m02) of the environment section. The
// record fields are null when its record did not reach the report (result says why).
export type Dependency = {
  task: string;
  status: string;
  taskReason: string | null;
  result: string;
  module: string | null;
  ecosystem: string | null;
  path: string | null;
  manager: string | null;
  managerVersion: string | null;
  state: string | null;
  reason: string | null;
  pinned: boolean | null;
  lockfile: string | null;
  declared: number | null;
  prepared: number | null;
  failed: number | null;
  cacheKind: string | null;
  failedItems: { name: string; reason: string | null }[];
  limitations: string[];
};
export type GoImportEdge = { from: string; to: string; kind: string; path: string; line: number };
export type GoPackageGraph = {
  task: string; module: string; directory?: string; platform: string; scope: string; status: string;
  parsed: number; ignored: number; unlisted?: number; incomplete: number; unresolved: number; unparsed: number;
  limitations: string[]; packages: string[]; edges: GoImportEdge[];
};

function goPackageGraph(value: unknown): GoPackageGraph {
  const g = object(value);
  const packages = array(g.packages, entry => str(object(entry).import_path));
  const names = new Set(packages);
  const edges = array(g.edges, entry => {
    const e = object(entry), witness = object(e.witness);
    const from = str(e.from), to = str(e.to), kind = str(e.kind), path = str(witness.path), line = count(witness.line);
    if (!names.has(from) || !names.has(to) || !['production', 'internal_test', 'external_test'].includes(kind) ||
      path.split('/').some(part => !part || part === '.' || part === '..') || /[:\\\u0000-\u001f\u007f]/.test(path) || line < 1) invalid();
    return { from, to, kind, path, line };
  });
  if (packages.length > 1024 || names.size !== packages.length || edges.length > 12000) invalid();
  return { task: str(g.task_key), module: str(g.module), directory: str(g.module_dir), platform: str(g.platform), scope: str(g.scope), status: str(g.status),
    parsed: count(g.parsed_files), ignored: count(g.ignored_variant_files),
    unlisted: g.unlisted_files === undefined ? undefined : count(g.unlisted_files), incomplete: count(g.incomplete_packages),
    unresolved: count(g.unresolved_internal_imports), unparsed: count(g.unparsed_files), limitations: array(g.limitations, str), packages, edges };
}

export type Sections = {
  overview: { key: string; state: string | null; counts: Record<string, number> }[];
  attention: { section: string; code: string; count: number }[];
  environment: {
    citations: Citation[];
    stage: SectionStage;
    discovery: string;
    modules: { id: string; ecosystem: string; path: string; inScope: boolean; buildSystem?: string; buildRoot?: string }[];
    requirements: { module: string; tool: string; constraint: string | null; source: string }[];
    observed: { tool: string; status: string; version: string | null; detail: string | null }[];
    verdicts: { module: string; tool: string; requirement: string | null; status: string; reason: string | null }[];
    analyzerTools: { tool: string; analyzers: string[]; status: string; version: string | null; detail: string | null }[];
    model: ModelState;
    notes: { module: string; affected: string; still: string; needed: string; evidence: string[] }[];
    // Empty when no dependency task was planned (fetching was off).
    dependencies: Dependency[];
  };
  documents: {
    citations: Citation[];
    documentationGaps?: {
      kind: 'cli_option' | 'cli_environment_variable'; symbol: string | null; defaultValue: string | null; scope: string;
      source: { path: string; line: number; end: number }; searched: { path: string; lines: number }[];
    }[];
    quality: {aspect: string; assessment: string; statement: string; refs: string[]}[];
    comparisons: {statement: string; scope: string; status: string; reason: string; documentRefs: string[]; codeRefs: string[]; followup?: true; additional?: AdditionalObservation; workflow?: RelatedWorkflow}[];
    stage: SectionStage;
    discovery: string;
    budget: { perDocument: number; total: number; read: number } | null;
    items: { path: string; cls: string; appliesTo: string; status: string; reason: string | null; size: number | null }[];
    modules: { module: string; state: string; own: string[]; inherited: string[] }[];
    overlaps: { path: string; documents: string[]; assessment: string }[];
    model: ModelState;
    contracts: { statement: string; appliesTo: string; evidence: string[] }[];
    conflicts: { scope: string; summary: string; impact: string; evidence: string[] }[];
    questions: { question: string; appliesTo: string; evidence: string[] }[];
    notes: { note: string; evidence: string[] }[];
  };
  staticAnalysis: { stage: SectionStage; languages: { ecosystem: string | null; tools: StaticTool[] }[] };
  metrics: {
    stage: SectionStage;
    tasks: string[];
    groups: { name: string; unit: string; thresholds: number[]; max: number; observations: string[] }[];
    goDependencies?: GoPackageGraph[];
  };
  secrets: {
    stage: SectionStage;
    tasks: string[];
    modelInput: string;
    detections: { observation: string; rule: string; place: string; finding: string | null }[];
  };
  codeReview: {
    citations: Citation[];
    strengths: {title: string; benefit: string; scope: string; refs: string[]}[];
    stage: SectionStage;
    // planned is whether the run planned model review roles at all.
    planned: boolean;
    packID: string | null;
    calls: number;
    acceptedCalls: number;
    requestBytes: number | null;
    exposed: number | null;
    inventory: number;
    roles: { key: string; status: string | null; result: string; reason: string | null }[];
    findings: string[];
    limits: { statement: string; observations: string[] }[];
    // The per-module review ledger (builder 6), null when no slice was planned.
    ledger: Ledger | null;
    // The diagnostic triage (builder 22), null when no triage slot was planned.
    triage: DiagnosticTriage | null;
    // The lead follow-ups (builder 26), null when none was planned.
    leadFollowups: LeadFollowups | null;
    // Investigation candidates left out of the findings because all their
    // evidence is on paths that are not shipped code (builder 24; 0 otherwise).
    candidatesNotShipped: number;
    // Model hypotheses about tool diagnostics that all lie on paths that are
    // not shipped code: listed with the secondary paths, no action items
    // (builder 25; 0 otherwise).
    hypothesesNotShipped: number;
  };
  gaps: { section: string; code: string; subject: string | null; detail: string | null }[];
};

export type LedgerModule = {
  id: string;
  discovered: boolean;
  eligible: number;
  sent: number;
  requested: number | null;
  accepted: number | null;
  bytesSent: number;
  truncated: number;
  withheld: number;
  unreadable: number;
  budgetExhausted: number;
  notPlanned: number;
  notSent: number;
  completed: number;
  // Builder 8 (a schema 0.2 scope artifact): the module's class and its
  // secondary files (test input, examples, generated), those sent and those
  // the ceiling or the slot limit left out. Null class: an earlier builder.
  cls: 'primary' | 'secondary' | null;
  secondary: number;
  secondarySent: number;
  secondaryNotReached: number;
  slices: { task: string; status: string; reason: string | null }[];
};

export type Ledger = {
  scope: { key: string; status: string | null; result: string; reason: string | null };
  maxCalls: number | null;
  maxRequestBytes: number | null;
  callsUsed: number | null;
  requestBytesSent: number | null;
  status: string;
  // Slots of primary files the call ceiling refused (builder 8; 0 otherwise).
  primarySlotsRefused: number;
  modules: LedgerModule[];
  files: LedgerFile[] | null;
  extended: boolean;
};

type LedgerFile = {
  path: string;
  module: string;
  total: number;
  sent: number;
  accepted: number;
  investigation: string;
  reason: string | null;
  remaining: { start: number; end: number; reason: string }[];
};

// DiagnosticTriage is the model triage of the tool diagnostics
// (docs/REPORT.md#도구-진단-분류). The counts come from the report's own
// observations; diagnostics - triaged - interpreted were not triaged.
export type TriageGroup = {
  task: string;
  id: string;
  classification: 'problem' | 'harmless' | 'unclear';
  bucket: 'attention' | 'refuted' | 'harmless';
  observations: string[];
  crossTool: boolean;
  reason: string | null;
  locations: { path: string; start: number; end: number }[];
  finding: string | null;
};
export type DiagnosticTriage = {
  diagnostics: number;
  triaged: number;
  interpreted: number;
  groups: TriageGroup[];
  limitations: string[];
};

const TRIAGE_BUCKETS: Record<string, string[]> = { problem: ['attention', 'refuted'], harmless: ['harmless'], unclear: ['attention'] };

// LeadFollowups counts the leads the investigations left and their follow-up
// tasks (docs/REPORT.md#단서-후속-조사). Every count comes from the review
// records, the task rows and the call records, none from model text.
export type LeadFollowups = {
  asked: number;
  resolved: number;
  unresolved: Record<string, number>;
  planned: number;
  run: number;
  accepted: number;
  notRun: Record<string, number>;
  calls: number;
  newExcerpts: number;
  newLines: number;
  dispositions: Record<string, number>;
};

const LEAD_DISPOSITIONS = ['kept', 'refined', 'contested', 'added'];
const LEADS_TASK = /^review\.leads(\.s[0-9]{3})?$/;

function parseLeadFollowups(v: unknown): LeadFollowups | null {
  if (v == null) return null;
  const x = object(v);
  return {
    asked: count(x.leads_asked), resolved: count(x.leads_resolved), unresolved: counts(x.leads_unresolved),
    planned: count(x.tasks_planned), run: count(x.tasks_run), accepted: count(x.tasks_accepted), notRun: counts(x.tasks_not_run),
    calls: count(x.calls), newExcerpts: count(x.new_excerpts), newLines: count(x.new_lines), dispositions: counts(x.dispositions),
  };
}

// validateLeadFollowups checks the counts against the report: builder 26 on,
// the planned tasks are the follow-up task rows, a run task is one with a
// call, the others are counted by reason, and the lead counts add up.
function validateLeadFollowups(r: Record<string, unknown>, l: LeadFollowups) {
  const sum = (m: Record<string, number>) => Object.values(m).reduce((a, n) => a + n, 0);
  const tasks = array(r.tasks, object).filter(t => LEADS_TASK.test(str(t.key)));
  const calls = array(object(r.coverage).review_calls ?? [], object).filter(c => typeof c.task_key === 'string' && LEADS_TASK.test(c.task_key));
  const run = tasks.filter(t => calls.some(c => c.task_key === t.key)).length;
  if (Number(str(object(r.builder).version)) < 26 || tasks.length === 0 || l.planned !== tasks.length || l.run !== run ||
    l.run + sum(l.notRun) !== l.planned || l.accepted > l.run || l.calls !== calls.length ||
    l.asked !== l.resolved + sum(l.unresolved) || Object.keys(l.dispositions).some(d => !LEAD_DISPOSITIONS.includes(d)) ||
    (l.newExcerpts === 0 && l.newLines !== 0) || (l.accepted === 0 && (l.newExcerpts !== 0 || sum(l.dispositions) !== 0))) invalid();
}

function parseTriage(v: unknown): DiagnosticTriage | null {
  if (v == null) return null;
  const x = object(v);
  const groups = array(x.groups, (g) => {
    const y = object(g);
    const classification = str(y.classification);
    const bucket = str(y.bucket);
    if (!(TRIAGE_BUCKETS[classification] ?? []).includes(bucket)) invalid();
    const finding = nullable(y.finding_id);
    const reason = nullable(y.reason);
    if ((classification === 'problem') !== (reason === null) || (classification !== 'problem' && finding !== null)) invalid();
    return {
      task: str(y.task_key),
      id: str(y.group_id),
      classification: classification as TriageGroup['classification'],
      bucket: bucket as TriageGroup['bucket'],
      observations: array(y.observation_ids, str),
      crossTool: bool(y.cross_tool),
      reason,
      locations: array(y.locations, (l) => {
        const z = object(l);
        return { path: str(z.path), start: count(z.start_line), end: count(z.end_line) };
      }),
      finding,
    };
  });
  return { diagnostics: count(x.diagnostics), triaged: count(x.triaged), interpreted: count(x.interpreted), groups, limitations: array(x.limitations, str) };
}

// validateTriage checks the triage against the report: builder 22 on, the
// grouped diagnostics are the report's and in one group each, the triaged
// count is theirs and a problem group's finding is a code review finding.
function validateTriage(triage: DiagnosticTriage, builder: number, observationIDs: string[], reviewFindings: string[]) {
  if (builder < 22) invalid();
  const known = new Set(observationIDs);
  const grouped = triage.groups.flatMap(g => g.observations);
  if (new Set(grouped).size !== grouped.length || grouped.some(id => !known.has(id))) invalid();
  if (grouped.length !== triage.triaged || triage.triaged + triage.interpreted > triage.diagnostics) invalid();
  if (triage.groups.some(g => g.finding !== null && !reviewFindings.includes(g.finding))) invalid();
}

function parseLedger(v: unknown): Ledger | null {
  if (v == null) return null;
  const l = object(v);
  const s = object(l.scope);
  const out: Ledger = {
    scope: { key: str(s.key), status: nullable(s.task_status), result: str(s.result), reason: nullable(s.reason) },
    maxCalls: optCount(l.max_calls),
    maxRequestBytes: optCount(l.max_request_bytes),
    callsUsed: optCount(l.calls_used),
    requestBytesSent: optCount(l.request_bytes_sent),
    status: str(l.status),
    primarySlotsRefused: l.primary_slots_budget_exhausted == null ? 0 : count(l.primary_slots_budget_exhausted),
    extended: l.extended_coverage == null ? false : bool(l.extended_coverage),
    modules: array(l.modules, (m) => {
      const x = object(m);
      const cls = x.class == null ? null : str(x.class);
      if (cls !== null && cls !== 'primary' && cls !== 'secondary') invalid();
      return {
        id: str(x.module_id),
        discovered: bool(x.discovered),
        eligible: count(x.files_eligible),
        sent: count(x.files_sent_to_model),
        requested: optCount(x.files_requested),
        accepted: optCount(x.files_accepted),
        bytesSent: count(x.bytes_sent),
        truncated: count(x.files_truncated),
        withheld: count(x.files_withheld),
        unreadable: count(x.files_unreadable),
        budgetExhausted: count(x.files_budget_exhausted),
        notPlanned: count(x.files_not_planned),
        notSent: count(x.files_not_sent),
        completed: count(x.files_with_completed_review),
        cls: cls as LedgerModule['cls'],
        secondary: x.files_secondary == null ? 0 : count(x.files_secondary),
        secondarySent: x.files_secondary_sent == null ? 0 : count(x.files_secondary_sent),
        secondaryNotReached: x.files_secondary_not_reached == null ? 0 : count(x.files_secondary_not_reached),
        slices: array(x.slices, (e) => {
          const y = object(e);
          return { task: str(y.task_key), status: str(y.status), reason: nullable(y.reason) };
        }),
      };
    }),
    files: l.files == null ? null : array(l.files, (v) => {
      const f = object(v);
      return { path: str(f.path), module: str(f.module_id), total: count(f.total_lines), sent: count(f.sent_lines),
        accepted: count(f.reviewed_lines), investigation: str(f.investigation), reason: nullable(f.reason),
        remaining: array(f.remaining, (v) => {
          const r = object(v);
          return { start: count(r.start_line), end: count(r.end_line), reason: str(r.reason) };
        }),
      };
    }),
  };
  // Each module's files are counted once.
  for (const m of out.modules) {
    if (m.sent + m.withheld + m.unreadable + m.budgetExhausted + m.notPlanned + m.secondaryNotReached + m.notSent !== m.eligible || m.completed > m.sent) invalid();
    if (m.secondary > m.eligible || m.secondarySent > Math.min(m.sent, m.secondary) || (m.cls === null && m.secondary + m.secondaryNotReached > 0)) invalid();
  }
  validateLedgerFileCoverage(out);
  if ((out.maxCalls ?? 0) > 207 && !out.extended || out.extended && out.scope.result !== 'accepted') invalid();
  return out;
}

const requestNotAccepted = 'requested_without_accepted_response';
const requestUnconfirmed = 'request_unconfirmed';
const coverageGaps = new Set([
  requestNotAccepted, requestUnconfirmed, 'not_requested', 'review_input_trimmed', 'review_budget_exhausted',
  'review_line_input_limit', 'review_slice_limit', 'source_blank_range',
]);
const unreadableReasons = new Set(['secret_file_withheld', 'source_file_unreadable', 'source_file_too_large', 'source_file_empty']);

function validateLedgerFileCoverage(ledger: Ledger) {
  if (ledger.files === null) {
    if (ledger.modules.some((m) => m.requested !== null || m.accepted !== null)) invalid();
    return;
  }
  if (ledger.scope.result !== 'accepted') invalid();
  const modules = new Map(ledger.modules.map((m) => [m.id, { files: 0, sent: 0, accepted: 0, partial: 0 }]));
  let previous = '';
  for (const f of ledger.files) {
    const m = modules.get(f.module) ?? invalid();
    if (!f.path || f.path <= previous || f.accepted > f.sent || f.sent > f.total || f.total > 512 * 1024 ||
        (f.reason !== null && !unreadableReasons.has(f.reason)) || (f.total === 0) !== (f.reason !== null)) invalid();
    previous = f.path;
    let end = 0;
    let remaining = 0;
    let unaccepted = 0;
    let uncertain = false;
    for (const r of f.remaining) {
      if (r.start <= end || r.start > r.end || r.end > f.total || !coverageGaps.has(r.reason)) invalid();
      end = r.end;
      const length = r.end - r.start + 1;
      remaining += length;
      if (r.reason === requestNotAccepted) unaccepted += length;
      uncertain ||= r.reason === requestUnconfirmed;
    }
    const state = f.accepted > 0 ? 'accepted' : f.sent > 0 ? requestNotAccepted : uncertain ? requestUnconfirmed : 'not_requested';
    if (f.investigation !== state || remaining !== f.total - f.accepted || unaccepted !== f.sent - f.accepted) invalid();
    m.files++;
    if (f.sent > 0) m.sent++;
    if (f.accepted > 0) m.accepted++;
    if (f.sent > 0 && f.sent < f.total) m.partial++;
  }
  for (const m of ledger.modules) {
    const counts = modules.get(m.id) ?? invalid();
    if (m.eligible !== counts.files || m.requested !== counts.sent || m.sent !== counts.sent ||
        m.accepted !== counts.accepted || m.completed > counts.accepted || m.truncated !== counts.partial) invalid();
  }
}

const invalid = (): never => {
  throw new TypeError('invalid report');
};
function object(v: unknown): Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : invalid();
}
function str(v: unknown): string {
  return typeof v === 'string' ? v : invalid();
}
function nullable(v: unknown): string | null {
  return v == null ? null : str(v);
}
function count(v: unknown): number {
  return typeof v === 'number' && Number.isSafeInteger(v) && v >= 0 ? v : invalid();
}
function optCount(v: unknown): number | null {
  return v == null ? null : count(v);
}
function bool(v: unknown): boolean {
  return typeof v === 'boolean' ? v : invalid();
}
function optBool(v: unknown): boolean | null {
  return v == null ? null : bool(v);
}
function array<T>(v: unknown, fn: (value: unknown) => T): T[] {
  return Array.isArray(v) ? v.map(fn) : invalid();
}
function counts(v: unknown): Record<string, number> {
  const out: Record<string, number> = {};
  for (const [k, n] of Object.entries(object(v))) out[k] = count(n);
  return out;
}
function dependency(v: unknown): Dependency {
  const x = object(v);
  return {
    task: str(x.task_key),
    status: str(x.task_status),
    taskReason: nullable(x.task_reason),
    result: str(x.result),
    module: nullable(x.module),
    ecosystem: nullable(x.ecosystem),
    path: nullable(x.path),
    manager: nullable(x.manager),
    managerVersion: nullable(x.manager_version),
    state: nullable(x.state),
    reason: nullable(x.reason),
    pinned: optBool(x.pinned),
    lockfile: nullable(x.lockfile),
    declared: optCount(x.declared),
    prepared: optCount(x.prepared),
    failed: optCount(x.failed),
    cacheKind: nullable(x.cache_kind),
    failedItems: array(x.failed_items ?? [], (f) => {
      const y = object(f);
      return { name: str(y.name), reason: nullable(y.reason) };
    }),
    limitations: array(x.limitations ?? [], str),
  };
}
function stage(v: unknown): SectionStage {
  const s = object(v);
  return { state: str(s.state), tasks: count(s.tasks), succeeded: count(s.succeeded) };
}
function model(v: unknown): ModelState {
  const m = object(v);
  const performed = str(m.state) === 'performed';
  const reason = nullable(m.reason);
  if (performed !== (reason === null)) invalid();
  return { task: str(m.task_key), performed, reason };
}

const BLOCKS = ['overview', 'environment', 'documents', 'static_analysis', 'metrics', 'secrets', 'code_review'];

// parseSections reads the sections of a report document; null when the
// revision has none (it keeps the flat layout).
export function parseSections(
  r: Record<string, unknown>,
  observationIDs: string[],
  findingIDs: string[],
  totalFindings: number,
  includesCode: boolean,
  expandedDocuments: boolean,
): Sections | null {
  const present = BLOCKS.filter((k) => r[k] != null);
  if (present.length === 0) {
    if (r.gaps != null) invalid();
    return null;
  }
  if (present.length !== BLOCKS.length) invalid();
  const ov = object(r.overview);
  const e = object(r.environment);
  const d = object(r.documents);
  const sa = object(r.static_analysis);
  const toolCoverage = new Map(array(object(r.coverage).tools ?? [], (v) => {
    const x = object(v);
    const units = Object.fromEntries(Object.entries(object(x.packages ?? {})).map(([key, value]) => [key, count(value)]));
    return [str(x.task_key), { units, limitations: array(x.limitations ?? [], str), paths: array(x.processed_paths ?? [], str) }] as const;
  }));
  const m = object(r.metrics);
  const sc = object(r.secrets);
  const cr = object(r.code_review);
  const out: Sections = {
    overview: array(ov.sections, (v) => {
      const x = object(v);
      return { key: str(x.key), state: nullable(x.state), counts: counts(x.counts) };
    }),
    attention: array(ov.attention, (v) => {
      const x = object(v);
      return { section: str(x.section), code: str(x.code), count: count(x.count) };
    }),
    environment: {
      citations: array(e.evidence ?? [], v => citation(v, includesCode, expandedDocuments)),
      stage: stage(e.stage),
      discovery: str(e.discovery),
      modules: array(e.modules, (v) => {
        const x = object(v);
        return { id: str(x.id), ecosystem: str(x.ecosystem), path: str(x.path), inScope: bool(x.in_scope),
          buildSystem: x.build_system === undefined ? undefined : str(x.build_system),
          buildRoot: x.build_root === undefined ? undefined : str(x.build_root) };
      }),
      requirements: array(e.requirements, (v) => {
        const x = object(v);
        return { module: str(x.module), tool: str(x.tool), constraint: nullable(x.constraint), source: str(x.path) + ':' + count(x.start_line) };
      }),
      observed: array(e.observed, (v) => {
        const x = object(v);
        return { tool: str(x.tool), status: str(x.status), version: nullable(x.version), detail: nullable(x.detail) };
      }),
      verdicts: array(e.verdicts, (v) => {
        const x = object(v);
        return { module: str(x.module), tool: str(x.tool), requirement: nullable(x.requirement), status: str(x.status), reason: nullable(x.reason) };
      }),
      analyzerTools: array(e.analyzer_tools, (v) => {
        const x = object(v);
        return { tool: str(x.tool), analyzers: array(x.analyzers, str), status: str(x.status), version: nullable(x.version), detail: nullable(x.detail) };
      }),
      model: model(e.model),
      notes: array(e.notes, (v) => {
        const x = object(v);
        return {
          module: str(x.module),
          affected: str(x.affected_checks),
          still: str(x.still_checkable),
          needed: str(x.needed_observation),
          evidence: array(x.evidence_ids, str),
        };
      }),
      dependencies: array(e.dependencies ?? [], dependency),
    },
    documents: {
      citations: array(d.evidence ?? [], v => citation(v, includesCode, expandedDocuments)),
      documentationGaps: array(d.documentation_gaps ?? [], v => {
        const x = object(v);
        const source = object(x.source);
        const kind = x.kind === 'cli_option' || x.kind === 'cli_environment_variable' ? x.kind : invalid();
        const symbol = nullable(x.symbol), defaultValue = nullable(x.default);
        const line = count(source.start_line), end = count(source.end_line);
        if (line < 1 || end < line || end - line >= 25 ||
            (includesCode ? !symbol : symbol !== null || defaultValue !== null)) invalid();
        const searched = array(x.searched, entry => {
          const file = object(entry);
          return { path: str(file.path), lines: count(file.line_count) };
        });
        if (!searched.length || searched.length > 24) invalid();
        return { kind, symbol, defaultValue, scope: str(x.scope), source: { path: str(source.path), line, end }, searched };
      }),
      quality: array(d.quality ?? [], v => {
        const x = object(v);
        return { aspect: str(x.aspect), assessment: str(x.assessment), statement: str(x.statement), refs: array(x.evidence_ids, str) };
      }),
      comparisons: array(d.comparisons ?? [], v => {
        const x = object(v);
        const followup = x.followup === undefined ? undefined : x.followup === true ? true : invalid();
        const extra = x.additional_observation === undefined ? undefined : object(x.additional_observation);
        const additional = extra && { reason: str(extra.reason), codeRefs: array(extra.code_evidence_ids, str) };
        const related = x.related_workflow === undefined ? undefined : object(x.related_workflow);
        if (related && Object.keys(related).length !== 3) invalid();
        const workflow = related && { read: str(related.read_evidence_id), change: str(related.change_evidence_id), test: str(related.test_evidence_id) };
        return { statement: str(x.statement), scope: str(x.applies_to), status: str(x.status), reason: str(x.reason), followup, additional, workflow,
          documentRefs: array(x.document_evidence_ids, str), codeRefs: array(x.code_evidence_ids, str) };
      }),
      stage: stage(d.stage),
      discovery: str(d.discovery),
      budget:
        d.budget == null
          ? null
          : (() => {
              const b = object(d.budget);
              return { perDocument: count(b.per_document_bytes), total: count(b.total_bytes), read: count(b.read_bytes) };
            })(),
      items: array(d.items, (v) => {
        const x = object(v);
        return { path: str(x.path), cls: str(x.class), appliesTo: str(x.applies_to), status: str(x.status), reason: nullable(x.reason), size: optCount(x.size_bytes) };
      }),
      modules: array(d.modules, (v) => {
        const x = object(v);
        return { module: str(x.module), state: str(x.state), own: array(x.own, str), inherited: array(x.inherited, str) };
      }),
      overlaps: array(d.overlaps, (v) => {
        const x = object(v);
        return { path: str(x.path), documents: array(x.documents, str), assessment: str(x.assessment) };
      }),
      model: model(d.model),
      contracts: array(d.contracts, (v) => {
        const x = object(v);
        return { statement: str(x.statement), appliesTo: str(x.applies_to), evidence: array(x.evidence_ids, str) };
      }),
      conflicts: array(d.conflicts, (v) => {
        const x = object(v);
        return {
          scope: str(x.scope),
          summary: str(x.summary),
          impact: str(x.impact),
          evidence: [...array(x.first_evidence_ids, str), ...array(x.second_evidence_ids, str)],
        };
      }),
      questions: array(d.questions, (v) => {
        const x = object(v);
        return { question: str(x.question), appliesTo: str(x.applies_to), evidence: array(x.evidence_ids, str) };
      }),
      notes: array(d.instruction_notes, (v) => {
        const x = object(v);
        return { note: str(x.note), evidence: array(x.evidence_ids, str) };
      }),
    },
    staticAnalysis: {
      stage: stage(sa.stage),
      languages: array(sa.languages, (v) => {
        const x = object(v);
        return {
          ecosystem: nullable(x.ecosystem),
          tools: array(x.tools, (t): StaticTool => {
            const y = object(t);
            return {
              task: str(y.task_key),
              required: bool(y.required),
              status: str(y.task_status),
              reason: nullable(y.task_reason),
              result: str(y.result),
              analyzer: nullable(y.analyzer),
              tool: nullable(y.tool),
              version: nullable(y.tool_version),
              purpose: nullable(y.purpose),
              inScope: optCount(y.files_in_scope),
              processed: optCount(y.processed_files),
              rulePack: nullable(y.rule_pack),
              units: toolCoverage.get(str(y.task_key))?.units ?? {},
              limitations: toolCoverage.get(str(y.task_key))?.limitations ?? [],
              paths: toolCoverage.get(str(y.task_key))?.paths,
              observations: array(y.observation_ids, str),
              findings: array(y.finding_ids, str),
              rules: array(y.rules, (z) => {
                const q = object(z);
                return { rule: str(q.rule_id), count: count(q.count) };
              }),
              metrics: count(y.metric_observations),
              secrets: count(y.secret_observations),
            };
          }),
        };
      }),
    },
    metrics: {
      stage: stage(m.stage),
      tasks: array(m.task_keys, str),
      goDependencies: array(m.go_package_dependencies ?? [], goPackageGraph),
      groups: array(m.metrics, (v) => {
        const x = object(v);
        return {
          name: str(x.name),
          unit: str(x.unit),
          thresholds: array(x.thresholds, count),
          max: count(x.max_value),
          observations: array(x.observation_ids, str),
        };
      }),
    },
    secrets: {
      stage: stage(sc.stage),
      tasks: array(sc.task_keys, str),
      modelInput: str(sc.model_input),
      detections: array(sc.detections, (v) => {
        const x = object(v);
        const path = nullable(x.path);
        return {
          observation: str(x.observation_id),
          rule: str(x.rule_id),
          place: path === null ? '—' : path + ':' + count(x.start_line),
          finding: nullable(x.finding_id),
        };
      }),
    },
    codeReview: {
      citations: array(cr.evidence ?? [], v => citation(v, includesCode, expandedDocuments)),
      strengths: array(cr.strengths ?? [], v => {
        const x = object(v);
        return { title: str(x.title), benefit: str(x.benefit), scope: str(x.scope), refs: array(x.evidence_ids, str) };
      }),
      stage: stage(cr.stage),
      planned: array(cr.roles, (v) => v).length > 0,
      packID: cr.pack_id == null ? null : str(cr.pack_id) + '@' + str(cr.pack_version),
      calls: count(cr.calls),
      acceptedCalls: count(cr.accepted_calls),
      requestBytes: optCount(cr.request_bytes),
      exposed: optCount(cr.exposed_files),
      inventory: count(cr.inventory_files),
      roles: array(cr.roles, (v) => {
        const x = object(v);
        return { key: str(x.key), status: nullable(x.task_status), result: str(x.result), reason: nullable(x.reason) };
      }),
      findings: array(cr.finding_ids, str),
      limits: array(cr.interpretation_limits, (v) => {
        const x = object(v);
        return { statement: str(x.statement), observations: array(x.observation_ids, str) };
      }),
      ledger: parseLedger(cr.ledger),
      triage: parseTriage(cr.diagnostic_triage),
      leadFollowups: parseLeadFollowups(cr.lead_followups),
      candidatesNotShipped: cr.candidates_not_shipped === undefined ? 0 : count(cr.candidates_not_shipped),
      hypothesesNotShipped: cr.hypotheses_not_shipped === undefined ? 0 : count(cr.hypotheses_not_shipped),
    },
    gaps: array(r.gaps ?? [], (v) => {
      const x = object(v);
      return { section: str(x.section), code: str(x.code), subject: nullable(x.subject), detail: nullable(x.detail) };
    }),
  };
  // Partition: each observation in exactly one of the static, metrics and
  // secrets sections; each finding in exactly one of static and code review.
  const placed = [
    ...out.staticAnalysis.languages.flatMap((l) => l.tools.flatMap((t) => t.observations)),
    ...out.metrics.groups.flatMap((g) => g.observations),
    ...out.secrets.detections.map((x) => x.observation),
  ];
  const found = [...out.staticAnalysis.languages.flatMap((l) => l.tools.flatMap((t) => t.findings)), ...out.codeReview.findings];
  const same = (a: string[], b: string[]) => a.length === b.length && new Set(a).size === a.length && b.every((id) => a.includes(id));
  if (!same(placed, observationIDs) || !same(found, findingIDs)) invalid();
  const sectionCount = (key: string, name: string) => out.overview.find((s) => s.key === key)?.counts[name] ?? invalid();
  if (sectionCount('static_analysis', 'findings') + sectionCount('code_review', 'findings') !== totalFindings) invalid();
  if (sectionCount('gaps', 'gaps') !== out.gaps.length) invalid();
  checkContractFollowup(r, out.documents, includesCode);
  checkRelatedWorkflow(r, out.documents);
  if (out.codeReview.candidatesNotShipped > 0 && Number(str(object(r.builder).version)) < 24) invalid();
  if (out.codeReview.hypothesesNotShipped > 0 && Number(str(object(r.builder).version)) < 25) invalid();
  if (out.codeReview.triage) validateTriage(out.codeReview.triage, Number(str(object(r.builder).version)), observationIDs, out.codeReview.findings);
  if (out.codeReview.leadFollowups) validateLeadFollowups(r, out.codeReview.leadFollowups);
  const ledger = out.codeReview.ledger;
  if (ledger?.extended && Number(str(object(r.builder).version)) < 16) invalid();
  if (ledger?.files !== null && ledger?.files !== undefined) {
    if (Number(str(object(r.builder).version)) < 16) invalid();
    const complete = ledger.files.length > 0 && ledger.files.every((f) => f.accepted > 0 && f.accepted === f.total);
    const sliced = ledger.modules.some((m) => m.slices.some((t) => t.task.startsWith('review.investigate.') && t.status === 'succeeded'));
    if (ledger.status !== (complete ? 'complete' : sliced ? 'partial' : 'overview_only')) invalid();
  }
  if (ledger !== null && ledger.modules.length > 0) {
    // The overview's reviewed scope is the ledger's: files sent to the model.
    const sent = ledger.modules.reduce((n, m) => n + m.sent, 0);
    const eligible = ledger.modules.reduce((n, m) => n + m.eligible, 0);
    if (sectionCount('code_review', 'files_sent_to_model') !== sent || sectionCount('code_review', 'files_not_sent_to_model') !== eligible - sent) invalid();
  }
  return out;
}

function checkRelatedWorkflow(r: Record<string, unknown>, documents: Sections['documents']) {
  const rows = documents.comparisons.filter(row => row.workflow);
  if (rows.length === 0) return;
  const [row] = rows;
  const contract = documents.contracts[documents.comparisons.indexOf(row)];
  const refs = [row.workflow!.read, row.workflow!.change, row.workflow!.test];
  const kinds = ['code', 'code', 'test'];
  if (rows.length !== 1 || !contract || row.statement !== contract.statement ||
    row.documentRefs.length !== contract.evidence.length || row.documentRefs.some((id, i) => id !== contract.evidence[i]) ||
    new Set(refs).size !== refs.length ||
    refs.some((id, i) => {
      const citations = documents.citations.filter(e => e.id === id);
      const e = citations[0];
      return citations.length !== 1 || e.kind !== kinds[i] || e.line < 1 || e.end < e.line || !e.path.endsWith('.go') ||
        (e.path.toLowerCase().endsWith('_test.go') !== (i === 2)) ||
        (contract.appliesTo !== '.' && !e.path.startsWith(contract.appliesTo + '/'));
    })) invalid();
  const key = 'review.investigate';
  const version = str(object(r.builder).version);
  const mediaType = 'application/vnd.codeye.review+json';
  if (!/^(1[5-9]|[2-9][0-9]|[1-9][0-9]{2,})$/.test(version) ||
    !array(r.tasks, object).some(task => task.key === key && task.status === 'succeeded') ||
    !array(object(r.code_review).roles, object).some(role =>
      role.key === key && role.result === 'accepted' && role.task_status === 'succeeded') ||
    !array(r.artifacts, object).some(artifact => artifact.task_key === key && artifact.role === 'other' &&
      artifact.media_type === mediaType && ['0.6', '0.7', '0.9'].some(v => artifact.decoder === mediaType + ';schema_version=' + v))) invalid();
}

function checkContractFollowup(r: Record<string, unknown>, documents: Sections['documents'], includesCode: boolean) {
  const rows = documents.comparisons.filter(row => row.followup || row.additional);
  if (rows.length === 0) return;
  const [row] = rows;
  const contract = documents.contracts[documents.comparisons.indexOf(row)];
  const scopeUnder = (path: string, scope: string) => scope === '.' || path === scope || path.startsWith(scope + '/');
  const docs = documents.citations.filter(e => row.documentRefs.includes(e.id));
  const cited = row.additional?.codeRefs ?? row.codeRefs;
  const code = documents.citations.filter(e => cited.includes(e.id));
  const implementation = (e: Citation) => e.kind === 'code' && !docs.some(doc =>
    doc.directivePath && e.path === doc.path && doc.line <= e.end && e.line <= doc.end);
  if (rows.length !== 1 || (row.additional ? row.followup || row.status !== 'unknown' : !['match', 'partial', 'mismatch'].includes(row.status)) ||
    !contract || row.statement !== contract.statement || !scopeUnder(row.scope, contract.appliesTo) ||
    row.documentRefs.length !== contract.evidence.length || row.documentRefs.some((id, i) => id !== contract.evidence[i]) ||
    docs.length === 0 || docs.length !== row.documentRefs.length ||
    docs.some(e => e.kind !== 'document') ||
    code.length !== cited.length || cited.length > 4 || code.some(e => !['code', 'test'].includes(e.kind) || !scopeUnder(e.path, row.scope)) ||
    !code.some(implementation) || (!includesCode && (row.statement !== TEXT_OMITTED || row.reason !== TEXT_OMITTED))) invalid();

  if (row.additional) {
    const original = documents.citations.filter(e => row.codeRefs.includes(e.id));
    const addsImplementation = (e: Citation) => {
      if (!implementation(e) || /(?:\.d\.[mc]?ts|\.pyi)$/i.test(e.path)) return false;
      for (let line = e.line; line <= e.end; line++) {
        if (!original.some(old => old.path === e.path && old.line <= line && line <= old.end)) return true;
      }
      return false;
    };
    if (!row.additional.reason.trim() || (!includesCode && row.additional.reason !== TEXT_OMITTED) ||
      code.some(e => e.line < 1 || e.end < e.line || e.end - e.line >= 80) || !code.some(addsImplementation)) invalid();
  }

  const key = 'review.contract';
  const coverage = object(r.coverage);
  const aiReview = object(coverage.ai_review);
  const tasks = array(r.tasks, object);
  const roles = array(aiReview.optional_tasks ?? [], object);
  const codeRoles = array(object(r.code_review).roles, object);
  const calls = array(coverage.review_calls ?? [], object);
  const accepted = (role: Record<string, unknown>) => role.key === key && role.task_status === 'succeeded' && role.result === 'accepted';
  if (!(Number(str(object(r.builder).version)) >= 12) ||
    !tasks.some(task => task.key === key && task.status === 'succeeded' && task.required_for_verdict === false) ||
    !roles.some(accepted) || !codeRoles.some(accepted) ||
    !calls.some(call => call.task_key === key && call.role === 'contract' && call.outcome === 'accepted' &&
      typeof call.request_sha256 === 'string' && /^[0-9a-f]{64}$/.test(call.request_sha256))) invalid();
}
