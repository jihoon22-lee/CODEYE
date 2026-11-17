import type { CreateProjectBody } from './api';
import { ko as t } from './locales/ko';

export type FormValues = {
  name: string;
  kind: 'local' | 'git';
  sourceRootId: string;
  relativePath: string;
  providerId: string;
  repositoryUrl: string;
  branch: string;
};
export type FormErrors = Partial<Record<keyof FormValues, string>>;

const v = t.projects.validation;

export function unsafeRelativePath(path: string): boolean {
  if (path === '.') return false;
  if (path.startsWith('/') || /[\\:\0]/.test(path)) return true;
  return path.split('/').some(segment => segment === '' || segment === '.' || segment === '..');
}

// Embedded credentials (https://user:token@host/...) must never be sent or stored.
export function urlHasCredentials(value: string): boolean {
  try {
    const url = new URL(value);
    return url.password !== '' || (url.username !== '' && (url.protocol === 'http:' || url.protocol === 'https:'));
  } catch {
    return false; // scp-like git@host:org/repo is not a URL; the server validates it
  }
}

export function validateProjectForm(values: FormValues): { body: CreateProjectBody | null; errors: FormErrors } {
  const errors: FormErrors = {};
  const name = values.name.trim();
  if (name.length > 200) errors.name = v.nameTooLong;
  let source: CreateProjectBody['source'] | null = null;
  if (values.kind === 'local') {
    const path = values.relativePath.trim();
    if (values.sourceRootId === '') errors.sourceRootId = v.sourceRootMissing;
    if (path === '') errors.relativePath = v.relativePathEmpty;
    else if (unsafeRelativePath(path)) errors.relativePath = v.relativePathUnsafe;
    if (!errors.sourceRootId && !errors.relativePath) source = { kind: 'local', source_root_id: values.sourceRootId, relative_path: path };
  } else {
    const url = values.repositoryUrl.trim();
    const branch = values.branch.trim();
    if (values.providerId === '') errors.providerId = v.providerMissing;
    if (url === '') errors.repositoryUrl = v.repositoryMissing;
    else if (urlHasCredentials(url)) errors.repositoryUrl = v.repositoryCredentials;
    if (branch === '') errors.branch = v.branchMissing;
    if (!errors.providerId && !errors.repositoryUrl && !errors.branch) source = { kind: 'git', provider_id: values.providerId, repository_url: url, branch };
  }
  if (Object.keys(errors).length > 0 || !source) return { body: null, errors };
  return { body: name === '' ? { source } : { name, source }, errors };
}
