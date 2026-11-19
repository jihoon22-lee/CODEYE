import type { AnalysisChoices, CreateProjectBody, Provider, Setup } from './api';
import { ko as t } from './locales/ko';
import { urlHasCredentials } from './projectForm';

const v = t.quick.validation;

// What the single field holds: an absolute folder path or a Git repository address.
export type QuickTarget =
  | { kind: 'local'; path: string }
  | { kind: 'git'; url: string; host: string; branch: string | null };

export function isAbsolutePath(value: string): boolean {
  return value.startsWith('/') || /^[A-Za-z]:[\\/]/.test(value) || value.startsWith('\\\\');
}

// The field is a folder when it looks like a path; anything else is read as a repository
// address (https:// is added when no scheme is given, as `codeye up github.com/o/r` does).
export function looksLikePath(value: string): boolean {
  return isAbsolutePath(value) || value.startsWith('.') || value.startsWith('~');
}

// parseTarget reads the field. A /tree/BRANCH address names its branch; the server stores
// the repository address without it.
export function parseTarget(raw: string): { target: QuickTarget | null; error: string | null } {
  const value = raw.trim();
  if (value === '') return { target: null, error: v.empty };
  if (looksLikePath(value)) {
    if (!isAbsolutePath(value)) return { target: null, error: v.notAbsolute };
    return { target: { kind: 'local', path: value }, error: null };
  }
  const withScheme = /^[a-z][a-z0-9+.-]*:\/\//i.test(value) ? value : `https://${value}`;
  if (urlHasCredentials(withScheme)) return { target: null, error: v.credentials };
  let url: URL;
  try {
    url = new URL(withScheme);
  } catch {
    return { target: null, error: v.notRepository };
  }
  if (url.protocol !== 'https:') return { target: null, error: v.notHttps };
  if (url.search !== '' || url.hash !== '') return { target: null, error: v.notRepository };
  let path = url.pathname.replace(/\/+$/, '');
  let branch: string | null = null;
  const tree = path.match(/^(\/[^/]+\/[^/]+)\/tree\/(.+)$/);
  if (tree) {
    path = tree[1];
    branch = decodeURIComponent(tree[2]);
  }
  const parts = path.split('/').filter(Boolean);
  if (parts.length < 2) return { target: null, error: v.notRepository };
  return { target: { kind: 'git', url: `https://${url.host.toLowerCase()}${path}`, host: url.host.toLowerCase(), branch }, error: null };
}

// The analysis choices shown: the user's own once a box was touched, otherwise the defaults of
// the target's kind (a folder: all on; a repository: build scripts off).
export function choicesFor(kind: 'local' | 'git', setup: Setup | null, touched: Partial<AnalysisChoices>): AnalysisChoices {
  const d = setup?.analysis_defaults[kind] ?? { send_source_to_model: true, run_build_scripts: kind === 'local', fetch_dependencies: true };
  return {
    send_source_to_model: touched.send_source_to_model ?? d.send_source_to_model,
    run_build_scripts: touched.run_build_scripts ?? d.run_build_scripts,
    fetch_dependencies: touched.fetch_dependencies ?? d.fetch_dependencies,
  };
}

// quickBody builds the registration; a repository needs the provider of its host.
export function quickBody(target: QuickTarget, name: string, branch: string, choices: AnalysisChoices, providers: Provider[], setup: Setup | null): { body: CreateProjectBody | null; error: string | null } {
  const label = name.trim();
  const base = { ...(label === '' ? {} : { name: label }), analysis: choices };
  if (target.kind === 'local') return { body: { ...base, local_path: target.path }, error: null };
  if (setup?.git && !setup.git.available) return { body: null, error: v.noGit };
  const provider = providers.find(p => p.host.toLowerCase() === target.host);
  if (!provider) return { body: null, error: v.hostNotAllowed(target.host) };
  const b = branch.trim() || target.branch || '';
  return { body: { ...base, source: { kind: 'git', provider_id: provider.id, repository_url: target.url, ...(b === '' ? {} : { branch: b }) } }, error: null };
}
