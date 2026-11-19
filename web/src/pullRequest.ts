// Pull-request runs (docs/GITHUB.md#pull-request-분석): the pinned pull request of a run view,
// its result comment state and the comment preview (GET /runs/{id}/pull-request-comment).

export type PullRequestComment = {
  requested: boolean;
  state: string; // not_requested | waiting_for_report | pending | posted | superseded | failed (open string)
  attempts: number;
  next_attempt_at: string | null;
  reason: string | null;
  url: string | null;
};

export type RunPullRequest = {
  number: number;
  url: string;
  repository: string; // HOST/OWNER/REPO
  title: string;
  base_ref: string;
  base_sha: string;
  head_ref: string;
  head_sha: string;
  head_repository: string;
  changed_files: number;
  comment: PullRequestComment;
};

export type CommentPreview = { body: string; head_sha: string; current_head_sha: string | null; head_moved: boolean; report_revision: number | null };

function rec(v: unknown): Record<string, unknown> {
  if (typeof v !== 'object' || v === null || Array.isArray(v)) throw new TypeError('object');
  return v as Record<string, unknown>;
}
function str(v: unknown): string {
  if (typeof v !== 'string') throw new TypeError('string');
  return v;
}
function nstr(v: unknown): string | null {
  return v === null || v === undefined ? null : str(v);
}
function num(v: unknown): number {
  if (typeof v !== 'number' || !Number.isInteger(v) || v < 0) throw new TypeError('integer');
  return v;
}

export function parseRunPullRequest(v: unknown): RunPullRequest {
  const o = rec(v);
  const c = rec(o.comment);
  return {
    number: num(o.number), url: str(o.url), repository: str(o.repository), title: str(o.title),
    base_ref: str(o.base_ref), base_sha: str(o.base_sha), head_ref: str(o.head_ref), head_sha: str(o.head_sha),
    head_repository: str(o.head_repository), changed_files: num(o.changed_files),
    comment: { requested: c.requested === true, state: str(c.state), attempts: num(c.attempts), next_attempt_at: nstr(c.next_attempt_at), reason: nstr(c.reason), url: nstr(c.url) },
  };
}

export function parseCommentPreview(v: unknown): CommentPreview {
  const o = rec(v);
  return {
    body: str(o.body), head_sha: str(o.head_sha), current_head_sha: nstr(o.current_head_sha), head_moved: o.head_moved === true,
    report_revision: o.report_revision === null || o.report_revision === undefined ? null : num(o.report_revision),
  };
}

// The run request of a pull request: the number and, when the user chose, the comment.
export function pullRequestRunBody(number: number, comment: boolean | null) {
  return { profile: 'deep-review', source_policy: 'latest_at_acquire', scope: { kind: 'full' }, pull_request: comment === null ? { number } : { number, comment } } as const;
}

// pullRequestOf reads /OWNER/REPO/pull/N (with an optional /files, /commits or /checks tab) of a
// repository address path; null for any other path.
export function pullRequestOf(path: string): { repoPath: string; number: number } | null {
  const m = path.match(/^(\/[^/]+\/[^/]+)\/pull\/([1-9][0-9]{0,9})(?:\/(?:files|commits|checks))?$/);
  if (!m) return null;
  return { repoPath: m[1], number: Number(m[2]) };
}

export function shortSHA(sha: string): string {
  return sha.slice(0, 7);
}
