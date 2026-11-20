import type { CreateProjectBody } from './api';
import { ko as t } from './locales/ko';

export type FormValues = {
  name: string;
  kind: 'local' | 'git';
  localPath: string;
  providerId: string;
  repositoryUrl: string;
  branch: string;
};
export type FormErrors = Partial<Record<keyof FormValues, string>>;

const v = t.projects.validation;

export function isAbsolutePath(value: string): boolean {
  return value.startsWith('/');
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
  const base = name === '' ? {} : { name };
  if (values.kind === 'local') {
    const path = values.localPath.trim();
    if (!isAbsolutePath(path) || path.includes('\0')) errors.localPath = t.quick.validation.notAbsolute;
    return { body: Object.keys(errors).length ? null : { ...base, local_path: path }, errors };
  }
  const url = values.repositoryUrl.trim();
  const branch = values.branch.trim();
  if (values.providerId === '') errors.providerId = v.providerMissing;
  if (url === '') errors.repositoryUrl = v.repositoryMissing;
  else if (urlHasCredentials(url)) errors.repositoryUrl = v.repositoryCredentials;
  const source = { kind: 'git' as const, provider_id: values.providerId, repository_url: url, ...(branch === '' ? {} : { branch }) };
  return { body: Object.keys(errors).length ? null : { ...base, source }, errors };
}
