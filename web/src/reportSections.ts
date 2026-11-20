// Analysis sections of reports from builder 5 on (docs/REPORT.md#분석-종류별-절).
// They reference observations and findings by ID. The parser checks that
// every observation and finding belongs to exactly one section and that the
// section counts add up to the summary, so the screen never shows numbers the
// stored document does not hold.
export type Excerpt = { start: number; end: number; text: string };
export type Citation = { id: string; kind: string; path: string; line: number; end: number; excerpt: Excerpt | null; directivePath?: string };

const diagnosticExcerptLines = 9;
const modelExcerptLines = 50;
const excerptBytes = 1800;
const excerptEncoder = new TextEncoder();

export function parseExcerpt(v: unknown): Excerpt | null {
  if (v == null) return null;
  const x = object(v);
  const excerpt = { start: count(x.start_line), end: count(x.end_line), text: str(x.text) };
  const lines = excerpt.end - excerpt.start + 1;
  // Go's excerpt budget counts the JSON string, including HTML escaping.
  const encoded = JSON.stringify(excerpt.text).replace(/[<>&\u2028\u2029]/g,
    character => '\\u' + character.charCodeAt(0).toString(16).padStart(4, '0'));
  if (excerpt.start < 1 || lines < 1 || lines > modelExcerptLines || lines !== excerpt.text.split('\n').length ||
    !excerpt.text.trim() || /[\u0000-\u0008\u000b-\u001f\u007f-\u009f]/.test(excerpt.text) ||
    excerptEncoder.encode(encoded).length > excerptBytes) invalid();
  return excerpt;
}

export function excerptFits(excerpt: Excerpt, start: number, end: number, model: boolean) {
  return excerpt.start <= start && start <= excerpt.end &&
    (excerpt.end - excerpt.start < diagnosticExcerptLines || model && excerpt.start === start && excerpt.end <= end);
}

function citation(v: unknown, includesCode: boolean): Citation {
  const x = object(v);
  const result = { id: str(x.id), kind: str(x.kind), path: str(x.path), line: count(x.start_line), end: count(x.end_line), excerpt: parseExcerpt(x.excerpt) };
  if (result.excerpt && (!includesCode || !excerptFits(result.excerpt, result.line, result.end, true))) invalid();
  if (x.directive_path === undefined) return result;
  const directivePath = str(x.directive_path);
  if (result.kind !== 'document' || !result.path.endsWith('.py') || !/\.(?:md|markdown)$/i.test(directivePath)) {
    throw new Error('invalid Python API documentation citation');
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
export type Sections = {
  overview: { key: string; state: string | null; counts: Record<string, number> }[];
  attention: { section: string; code: string; count: number }[];
  environment: {
    citations: Citation[];
    stage: SectionStage;
    discovery: string;
    modules: { id: string; ecosystem: string; path: string; inScope: boolean }[];
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
    quality: {aspect: string; assessment: string; statement: string; refs: string[]}[];
    comparisons: {statement: string; scope: string; status: string; reason: string; documentRefs: string[]; codeRefs: string[]}[];
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
  };
  gaps: { section: string; code: string; subject: string | null; detail: string | null }[];
};

export type LedgerModule = {
  id: string;
  discovered: boolean;
  eligible: number;
  sent: number;
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
};

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
    modules: array(l.modules, (m) => {
      const x = object(m);
      const cls = x.class == null ? null : str(x.class);
      if (cls !== null && cls !== 'primary' && cls !== 'secondary') invalid();
      return {
        id: str(x.module_id),
        discovered: bool(x.discovered),
        eligible: count(x.files_eligible),
        sent: count(x.files_sent_to_model),
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
  };
  // Each module's files are counted once.
  for (const m of out.modules) {
    if (m.sent + m.withheld + m.unreadable + m.budgetExhausted + m.notPlanned + m.secondaryNotReached + m.notSent !== m.eligible || m.completed > m.sent) invalid();
    if (m.secondary > m.eligible || m.secondarySent > Math.min(m.sent, m.secondary) || (m.cls === null && m.secondary + m.secondaryNotReached > 0)) invalid();
  }
  return out;
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
      citations: array(e.evidence ?? [], v => citation(v, includesCode)),
      stage: stage(e.stage),
      discovery: str(e.discovery),
      modules: array(e.modules, (v) => {
        const x = object(v);
        return { id: str(x.id), ecosystem: str(x.ecosystem), path: str(x.path), inScope: bool(x.in_scope) };
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
      citations: array(d.evidence ?? [], v => citation(v, includesCode)),
      quality: array(d.quality ?? [], v => {
        const x = object(v);
        return { aspect: str(x.aspect), assessment: str(x.assessment), statement: str(x.statement), refs: array(x.evidence_ids, str) };
      }),
      comparisons: array(d.comparisons ?? [], v => {
        const x = object(v);
        return { statement: str(x.statement), scope: str(x.applies_to), status: str(x.status), reason: str(x.reason),
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
      citations: array(cr.evidence ?? [], v => citation(v, includesCode)),
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
  const ledger = out.codeReview.ledger;
  if (ledger !== null && ledger.modules.length > 0) {
    // The overview's reviewed scope is the ledger's: files sent to the model.
    const sent = ledger.modules.reduce((n, m) => n + m.sent, 0);
    const eligible = ledger.modules.reduce((n, m) => n + m.eligible, 0);
    if (sectionCount('code_review', 'files_sent_to_model') !== sent || sectionCount('code_review', 'files_not_sent_to_model') !== eligible - sent) invalid();
  }
  return out;
}
