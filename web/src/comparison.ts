// W23 run comparison and human finding reviews (docs/REPORT.md, api/openapi.yaml RunComparison).
// States, reasons and dispositions are open strings: an unrecognised value reaches the UI raw,
// never as "resolved" or "hidden". Counts come from the server's structured data only.

export type ReviewOverlay = {
  version: number;
  disposition: string;
  reason: string;
  pathPrefix: string | null;
  expiresAt: string | null;
  expired: boolean;
  actorID: string;
  createdAt: string;
  runID: string;
  reportRevision: number;
  findingID: string;
};
export type Occurrence = { findingID: string; path: string; startLine: number; endLine: number };
export type ComparisonEntry = {
  fingerprint: string;
  state: string;
  reason: string | null;
  ambiguous: boolean;
  producer: string;
  analyzer: string;
  rule: string;
  paths: string[];
  current: Occurrence[];
  baseline: Occurrence[];
  changes: string[];
  moveCandidates: string[];
};
export type FindingStatus = {
  findingID: string;
  fingerprint: string | null;
  state: string | null;
  publication: string;
  current: string;
  match: string;
  review: ReviewOverlay | null;
};
export type DispositionCounts = { total: number; actionable: number; pending: number; refuted: number; hidden: number };
export type Comparison = {
  runID: string;
  projectID: string;
  revision: number;
  baseline: { runID: string; revision: number; createdAt: string; selection: string } | null;
  comparable: boolean;
  notComparableReason: string | null;
  differences: { key: string; baseline: string | null; current: string | null }[];
  counts: Record<string, number>;
  entries: ComparisonEntry[];
  unidentified: { current: number; baseline: number };
  findings: FindingStatus[];
  summary: { atPublication: DispositionCounts; current: DispositionCounts };
  canReview: boolean;
  checkedAt: string;
};
export type FindingReviewBody = {
  fingerprint: string;
  disposition: string;
  reason: string;
  expires_at: string | null;
  path_prefix: string | null;
  run_id: string;
  revision: number;
  finding_id: string;
};
export type FindingReview = { version: number; disposition: string; fingerprint: string };

export const FINGERPRINT = /^fp2:[0-9a-f]{64}$/;

const invalid = (): never => {
  throw new TypeError('invalid comparison');
};
function object(v: unknown): Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : invalid();
}
function str(v: unknown): string {
  return typeof v === 'string' && v !== '' ? v : invalid();
}
function text(v: unknown): string {
  return typeof v === 'string' ? v : invalid();
}
function nullable(v: unknown): string | null {
  return v === null ? null : str(v);
}
function int(v: unknown, min = 0): number {
  return typeof v === 'number' && Number.isSafeInteger(v) && v >= min ? v : invalid();
}
function bool(v: unknown): boolean {
  return typeof v === 'boolean' ? v : invalid();
}
function date(v: unknown): string {
  const s = str(v);
  return Number.isNaN(Date.parse(s)) ? invalid() : s;
}
function nullableDate(v: unknown): string | null {
  return v === null ? null : date(v);
}
function array<T>(v: unknown, fn: (value: unknown) => T): T[] {
  return Array.isArray(v) ? v.map(fn) : invalid();
}
function fingerprint(v: unknown): string {
  const s = str(v);
  return FINGERPRINT.test(s) ? s : invalid();
}

function review(v: unknown): ReviewOverlay | null {
  if (v === null) return null;
  const r = object(v);
  return {
    version: int(r.version, 1),
    disposition: str(r.disposition),
    reason: str(r.reason),
    pathPrefix: nullable(r.path_prefix),
    expiresAt: nullableDate(r.expires_at),
    expired: bool(r.expired),
    actorID: str(r.actor_id),
    createdAt: date(r.created_at),
    runID: str(r.run_id),
    reportRevision: int(r.report_revision, 1),
    findingID: str(r.finding_id),
  };
}
function occurrence(v: unknown): Occurrence {
  const o = object(v);
  return { findingID: str(o.finding_id), path: str(o.path), startLine: int(o.start_line), endLine: int(o.end_line) };
}
function entry(v: unknown): ComparisonEntry {
  const e = object(v);
  return {
    fingerprint: fingerprint(e.fingerprint),
    state: str(e.state),
    reason: nullable(e.reason),
    ambiguous: bool(e.ambiguous),
    producer: str(e.producer),
    analyzer: str(e.analyzer),
    rule: str(e.rule),
    paths: array(e.paths, str),
    current: array(e.current, occurrence),
    baseline: array(e.baseline, occurrence),
    changes: array(e.changes, str),
    moveCandidates: array(e.move_candidates, fingerprint),
  };
}
function dispositionCounts(v: unknown): DispositionCounts {
  const c = object(v);
  return {
    total: int(c.total_findings),
    actionable: int(c.actionable_findings),
    pending: int(c.pending_candidates),
    refuted: int(c.refuted_findings),
    hidden: int(c.hidden_findings),
  };
}

export function parseComparison(value: unknown, expectedRun: string, expectedRevision?: number): Comparison {
  const c = object(value);
  if (c.run_id !== expectedRun) invalid();
  const revision = int(c.revision, 1);
  if (expectedRevision !== undefined && revision !== expectedRevision) invalid();
  let baseline: Comparison['baseline'] = null;
  if (c.baseline !== null) {
    const b = object(c.baseline);
    baseline = { runID: str(b.run_id), revision: int(b.revision, 1), createdAt: date(b.report_created_at), selection: str(b.selection) };
  }
  const counts: Record<string, number> = {};
  for (const [k, n] of Object.entries(object(c.counts))) counts[k] = int(n);
  const entries = array(c.entries, entry);
  // The header counts are the server's; they must agree with the entries it sent.
  for (const [state, n] of Object.entries(counts)) if (entries.filter((e) => e.state === state).length !== n) invalid();
  if (entries.some((e) => !Object.hasOwn(counts, e.state))) invalid();
  const unidentified = object(c.unidentified);
  const summary = object(c.summary);
  const comparable = bool(c.comparable);
  const notComparableReason = nullable(c.not_comparable_reason);
  if (comparable !== (notComparableReason === null) || (baseline === null) !== (notComparableReason === 'no_baseline')) invalid();
  return {
    runID: expectedRun,
    projectID: str(c.project_id),
    revision,
    baseline,
    comparable,
    notComparableReason,
    differences: array(c.differences, (v) => {
      const d = object(v);
      return { key: str(d.key), baseline: d.baseline === null ? null : text(d.baseline), current: d.current === null ? null : text(d.current) };
    }),
    counts,
    entries,
    unidentified: { current: int(unidentified.current), baseline: int(unidentified.baseline) },
    findings: array(c.findings, (v) => {
      const f = object(v);
      return {
        findingID: str(f.finding_id),
        fingerprint: f.fingerprint === null ? null : fingerprint(f.fingerprint),
        state: nullable(f.state),
        publication: str(f.publication_disposition),
        current: str(f.current_disposition),
        match: str(f.review_match),
        review: review(f.review),
      };
    }),
    summary: { atPublication: dispositionCounts(summary.at_publication), current: dispositionCounts(summary.current) },
    canReview: bool(c.can_review),
    checkedAt: date(c.checked_at),
  };
}

export function parseFindingReview(value: unknown): FindingReview {
  const r = object(value);
  return { version: int(r.version, 1), disposition: str(r.disposition), fingerprint: fingerprint(r.fingerprint) };
}

export function comparisonURL(runID: string, revision?: number) {
  const q = revision === undefined ? '' : '?revision=' + String(revision);
  return '/api/v1/runs/' + encodeURIComponent(runID) + '/comparison' + q;
}
