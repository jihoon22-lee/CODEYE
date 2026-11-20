// Validate the projection used by the report view before rendering any model or tool text.
import { parseSections, parseExcerpt, type Excerpt, type Sections } from './reportSections';

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
  category: string;
  severity: string;
  validation: string;
  impact: string;
  claims: { assertion: string; kind: string; evidence: string[] }[];
  severityReason: string;
  conditions: { statement: string; evidence: string[] }[];
  recommendation: string;
  verification: string[];
  limitations: string[];
  evidence: Evidence[];
  counter: string[];
  // Snapshot paths of the finding's locations and its tool diagnostics.
  locations: string[];
  observations: string[];
};
export type Task = { key: string; status: string; reason: string | null };
export type Observation = {
  id: string;
  rule: string;
  tool: string | null;
  message: string | null;
  path: string | null;
  location: { path: string; start: number; end: number } | null;
  state: string;
  evidenceState: string | null;
  // The tool's own severity, and whether an in-source suppression hid it.
  severity: string | null;
  suppressed: boolean;
  // Structure or duplication measurement (never a finding).
  metric: { name: string; value: number; threshold: number; unit: string } | null;
};
export type AnalysisReport = {
  project?: {name: string; source: string | null; start: string | null; finish: string | null};
  commit?: string | null;
  sourceBase?: string;
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
    model: string;
    version: string;
    outcome: string;
    pack: string;
  }[];
  findings: Finding[];
  observations: Observation[];
  tasks: Task[];
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

export function sourceEvidenceURL(base: string | undefined, path: string, start: number, end: number) {
  if (!base || !Number.isSafeInteger(start) || !Number.isSafeInteger(end) || start < 1 || end < start || /[\\:\x00-\x1f\x7f]/.test(path)) return;
  const segments = path.split('/');
  if (segments.some(part => !part || part === '.' || part === '..') || new TextEncoder().encode(path).length > 4096) return;
  try {
    return `${base}/${segments.map(encodeURIComponent).join('/')}#L${start}-L${end}`;
  } catch {
    return;
  }
}
function evidence(v: unknown): Evidence {
  const e = object(v);
  const loc = e.location == null ? null : object(e.location);
  return {
    id: str(e.id),
    kind: str(e.kind),
    description: str(e.description),
    path: loc ? str(loc.path) : undefined,
    lines: loc ? { start: count(loc.start_line), end: count(loc.end_line) } : undefined,
    excerpt: parseExcerpt(e.excerpt),
    artifact: nullable(e.artifact_id) ?? undefined,
  };
}
function finding(v: unknown): Finding {
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
      return { statement: str(c.statement), evidence: array(c.evidence_ids ?? [], str) };
    }),
    recommendation: str(object(f.recommendation).summary),
    verification: array(f.verification, str),
    limitations: array(f.limitations, str),
    evidence: array(f.evidence, evidence),
    counter: array(f.counterevidence_ids, str),
    locations: array(f.locations ?? [], (l) => str(object(l).path)),
    observations: array(f.observation_ids ?? [], str),
  };
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
  };
  const findings = array(r.findings, finding);
  const observations = array(r.observations, (v) => {
    const o = object(v),
      loc = o.location == null ? null : object(o.location);
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
      metric: o.metric == null ? null : (() => {
        const m = object(o.metric);
        return { name: str(m.name), value: count(m.value), threshold: count(m.threshold), unit: str(m.unit) };
      })(),
    };
  });
  const metricObservations = s.metric_observations == null ? 0 : count(s.metric_observations);
  const priorities = array(s.priorities ?? [], str);
  if (
    !priorities.every((id) => findings.some((f) => f.id === id)) ||
    findings.length !== counts.total ||
    counts.raw !== observations.length ||
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
  const includesCode = r.export != null && object(r.export).include_code === true;
  const head = r.pull_request == null ? null : nullable(object(r.pull_request).head_repository);
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
        model: str(c.model),
        version: str(c.opencode_version),
        outcome: str(c.outcome),
        pack: str(c.pack_sha256),
      };
    }),
    project,
    commit,
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
    tasks: array(r.tasks, (v) => {
      const task = object(v);
      return {
        key: str(task.key),
        status: str(task.status),
        reason: nullable(task.reason),
      };
    }),
    limitations: array(r.limitations, str),
    sections: parseSections(
      r,
      observations.map((o) => o.id),
      findings.map((f) => f.id),
      counts.total,
    ),
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
