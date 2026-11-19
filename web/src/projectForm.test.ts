import { describe, expect, it } from 'vitest';
import { unsafeRelativePath, urlHasCredentials, validateProjectForm, type FormValues } from './projectForm';

const base: FormValues = { name: '', kind: 'local', sourceRootId: 'root1', relativePath: 'team/service', providerId: '', repositoryUrl: '', branch: '' };
const git: FormValues = { ...base, kind: 'git', sourceRootId: '', relativePath: '', providerId: 'gh', repositoryUrl: 'https://github.com/org/repo.git', branch: 'main' };

describe('validateProjectForm', () => {
  it('builds the local source body and omits an empty name (no description field exists)', () => {
    const { body, errors } = validateProjectForm(base);
    expect(errors).toEqual({});
    expect(body).toEqual({ source: { kind: 'local', source_root_id: 'root1', relative_path: 'team/service' } });
    expect(body).not.toHaveProperty('description');
  });
  it('includes a trimmed name when given', () => {
    expect(validateProjectForm({ ...base, name: '  my project ' }).body).toMatchObject({ name: 'my project' });
  });
  it('builds the git source body from the selected provider', () => {
    expect(validateProjectForm(git).body).toEqual({ source: { kind: 'git', provider_id: 'gh', repository_url: 'https://github.com/org/repo.git', branch: 'main' } });
  });
  it('reports each missing field and returns no body', () => {
    const local = validateProjectForm({ ...base, sourceRootId: '', relativePath: ' ' });
    expect(local.body).toBeNull();
    expect(Object.keys(local.errors).sort()).toEqual(['relativePath', 'sourceRootId']);
    const remote = validateProjectForm({ ...git, providerId: '', repositoryUrl: '', branch: '' });
    expect(Object.keys(remote.errors).sort()).toEqual(['providerId', 'repositoryUrl']);
    // No branch: the repository's default branch.
    expect(validateProjectForm({ ...git, branch: '' }).body).toEqual({ source: { kind: 'git', provider_id: 'gh', repository_url: 'https://github.com/org/repo.git' } });
  });
  it('rejects names over 200 characters', () => {
    expect(validateProjectForm({ ...base, name: 'x'.repeat(201) }).errors.name).toBeTruthy();
  });
  it('rejects repository addresses that embed credentials', () => {
    expect(validateProjectForm({ ...git, repositoryUrl: 'https://user:pw@github.com/o/r.git' }).body).toBeNull();
    expect(validateProjectForm({ ...git, repositoryUrl: 'https://ghp_token@github.com/o/r.git' }).errors.repositoryUrl).toBeTruthy();
  });
});

describe('path and URL checks', () => {
  it.each(['/etc', '../x', 'a/../b', 'a/./b', 'a//b', 'a/', 'a\\b', 'C:foo', 'a:b', '..'])('flags %j as unsafe', path => {
    expect(unsafeRelativePath(path)).toBe(true);
  });
  it.each(['.', 'a', 'team/service', '한글/경로', '..hidden/file', 'a/b.c'])('accepts %j', path => {
    expect(unsafeRelativePath(path)).toBe(false);
  });
  it('allows ssh-style addresses and plain https', () => {
    expect(urlHasCredentials('git@github.com:org/repo.git')).toBe(false);
    expect(urlHasCredentials('ssh://git@host/org/repo.git')).toBe(false);
    expect(urlHasCredentials('https://github.com/org/repo')).toBe(false);
    expect(urlHasCredentials('https://:pw@github.com/org/repo')).toBe(true);
  });
});
