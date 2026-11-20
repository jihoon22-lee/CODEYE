import { describe, expect, it } from 'vitest';
import { urlHasCredentials, validateProjectForm, type FormValues } from './projectForm';

const base: FormValues = { name: '', kind: 'local', localPath: '/srv/team/service', providerId: '', repositoryUrl: '', branch: '' };
const git: FormValues = { ...base, kind: 'git', localPath: '', providerId: 'gh', repositoryUrl: 'https://github.com/org/repo.git', branch: 'main' };

describe('validateProjectForm', () => {
  it('builds the local source body and omits an empty name (no description field exists)', () => {
    const { body, errors } = validateProjectForm(base);
    expect(errors).toEqual({});
    expect(body).toEqual({ local_path: '/srv/team/service' });
    expect(body).not.toHaveProperty('description');
  });
  it('includes a trimmed name when given', () => {
    expect(validateProjectForm({ ...base, name: '  my project ' }).body).toMatchObject({ name: 'my project' });
  });
  it('builds the git source body from the selected provider', () => {
    expect(validateProjectForm(git).body).toEqual({ source: { kind: 'git', provider_id: 'gh', repository_url: 'https://github.com/org/repo.git', branch: 'main' } });
  });
  it('reports each missing field and returns no body', () => {
    const local = validateProjectForm({ ...base, localPath: ' ' });
    expect(local.body).toBeNull();
    expect(Object.keys(local.errors).sort()).toEqual(['localPath']);
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
  it.each(['../x', 'a/../b', 'C:foo', 'a:b', '..'])('rejects relative path %j', localPath => {
    expect(validateProjectForm({ ...base, localPath }).body).toBeNull();
  });
  it('allows ssh-style addresses and plain https', () => {
    expect(urlHasCredentials('git@github.com:org/repo.git')).toBe(false);
    expect(urlHasCredentials('ssh://git@host/org/repo.git')).toBe(false);
    expect(urlHasCredentials('https://github.com/org/repo')).toBe(false);
    expect(urlHasCredentials('https://:pw@github.com/org/repo')).toBe(true);
  });
});
