// A link printed by `codeye up` opens one project and one of its runs: #project=ID&run=ID
// (after the launch token, which the session controller removes). Only these keys are read,
// only canonical UUIDs are accepted, and nothing else in the fragment is kept.
export const DEEP_LINK_KEYS = ['project', 'run'] as const;

export type DeepLink = { project: string | null; run: string | null };

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

export function readDeepLink(hash: string): DeepLink {
  const params = new URLSearchParams(hash.startsWith('#') ? hash.slice(1) : '');
  const pick = (key: string) => {
    const v = params.get(key);
    return v !== null && UUID.test(v) ? v : null;
  };
  const project = pick('project');
  return { project, run: project === null ? null : pick('run') };
}

// The fragment to keep when the launch token is removed ('' when there is no deep link).
export function deepLinkFragment(params: URLSearchParams): string {
  const kept = new URLSearchParams();
  for (const key of DEEP_LINK_KEYS) {
    const v = params.get(key);
    if (v !== null) kept.set(key, v);
  }
  const link = readDeepLink(`#${kept.toString()}`);
  if (link.project === null) return '';
  const out = new URLSearchParams({ project: link.project });
  if (link.run !== null) out.set('run', link.run);
  return `#${out.toString()}`;
}
