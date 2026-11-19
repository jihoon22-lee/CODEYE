import { describe, expect, it } from 'vitest';
import type { Project, Setup } from './api';
import { improvementHints, type Gap } from './improve';
import { ko as t } from './locales/ko';
import { choicesFor, parseTarget, quickBody } from './quickStart';

const SETUP: Setup = {
  mode: 'personal',
  model: { configured: false, id: null, probe: null },
  git: { available: true, hosts: ['github.com'] },
  readable_folders: ['/home/me'],
  analysis_defaults: { local: { send_source_to_model: true, run_build_scripts: true, fetch_dependencies: true }, git: { send_source_to_model: true, run_build_scripts: false, fetch_dependencies: true } },
  tools: { detected_at: null, tools: [{ name: 'cppcheck', status: 'not_found', version: null, path: null, hint: 'apt install cppcheck', reason: null }] },
};
const PROVIDERS = [{ id: 'gh', kind: 'github', host: 'github.com' }];

describe('the one field', () => {
  it('reads an absolute path as a folder and anything else as a repository address', () => {
    expect(parseTarget(' /home/me/app ').target).toEqual({ kind: 'local', path: '/home/me/app' });
    expect(parseTarget('C:\\src\\app').target).toEqual({ kind: 'local', path: 'C:\\src\\app' });
    expect(parseTarget('./app').error).toBe(t.quick.validation.notAbsolute);
    expect(parseTarget('~/app').error).toBe(t.quick.validation.notAbsolute);
    expect(parseTarget('').error).toBe(t.quick.validation.empty);
    expect(parseTarget('https://github.com/Owner/Repo/').target).toEqual({ kind: 'git', url: 'https://github.com/Owner/Repo', host: 'github.com', branch: null });
    expect(parseTarget('github.com/o/r').target).toEqual({ kind: 'git', url: 'https://github.com/o/r', host: 'github.com', branch: null });
  });

  it('takes the branch of a /tree/ address and refuses credentials, plain http and non-repository addresses', () => {
    expect(parseTarget('https://github.com/o/r/tree/release/1.2').target).toEqual({ kind: 'git', url: 'https://github.com/o/r', host: 'github.com', branch: 'release/1.2' });
    expect(parseTarget('https://user:tok@github.com/o/r').error).toBe(t.quick.validation.credentials);
    expect(parseTarget('http://github.com/o/r').error).toBe(t.quick.validation.notHttps);
    expect(parseTarget('https://github.com/o').error).toBe(t.quick.validation.notRepository);
    expect(parseTarget('https://github.com/o/r?tab=readme').error).toBe(t.quick.validation.notRepository);
  });

  it('defaults the analysis choices by kind until a box is touched', () => {
    expect(choicesFor('local', SETUP, {})).toEqual({ send_source_to_model: true, run_build_scripts: true, fetch_dependencies: true });
    expect(choicesFor('git', SETUP, {})).toEqual({ send_source_to_model: true, run_build_scripts: false, fetch_dependencies: true });
    expect(choicesFor('git', SETUP, { run_build_scripts: true })).toEqual({ send_source_to_model: true, run_build_scripts: true, fetch_dependencies: true });
    expect(choicesFor('local', SETUP, { fetch_dependencies: false })).toEqual({ send_source_to_model: true, run_build_scripts: true, fetch_dependencies: false });
    // Without the setup both kinds fetch dependencies.
    expect(choicesFor('git', null, {})).toEqual({ send_source_to_model: true, run_build_scripts: false, fetch_dependencies: true });
    expect(choicesFor('local', null, {})).toEqual({ send_source_to_model: true, run_build_scripts: true, fetch_dependencies: true });
    const off = { ...SETUP, analysis_defaults: { ...SETUP.analysis_defaults, git: { send_source_to_model: true, run_build_scripts: false, fetch_dependencies: false } } };
    expect(choicesFor('git', off, {}).fetch_dependencies).toBe(false);
  });

  it('builds the registration: a folder by path, a repository with the provider of its host', () => {
    const on = { send_source_to_model: true, run_build_scripts: true, fetch_dependencies: false };
    expect(quickBody({ kind: 'local', path: '/home/me/app' }, '', '', on, PROVIDERS, SETUP).body).toEqual({ analysis: on, local_path: '/home/me/app' });
    const repo = parseTarget('https://github.com/o/r/tree/dev').target!;
    expect(quickBody(repo, ' mine ', '', on, PROVIDERS, SETUP).body).toEqual({ name: 'mine', analysis: on, source: { kind: 'git', provider_id: 'gh', repository_url: 'https://github.com/o/r', branch: 'dev' } });
    expect(quickBody(parseTarget('https://github.com/o/r').target!, '', '', on, PROVIDERS, SETUP).body).toEqual({ analysis: on, source: { kind: 'git', provider_id: 'gh', repository_url: 'https://github.com/o/r' } });
    expect(quickBody(parseTarget('https://gitlab.com/o/r').target!, '', '', on, PROVIDERS, SETUP).error).toBe(t.quick.validation.hostNotAllowed('gitlab.com'));
    expect(quickBody(repo, '', '', on, PROVIDERS, { ...SETUP, git: { available: false, hosts: [] } }).error).toBe(t.quick.validation.noGit);
  });
});

const project = (analysis: Project['analysis']): Project => ({
  id: 'p1', name: 'r', source: { kind: 'git', provider_id: 'gh', repository_url: 'https://github.com/o/r' }, revision: 1, source_status: 'declared_unverified',
  capability_gaps: [], last_run: null, role: 'owner', can_run: true, can_manage: true, analysis,
});
const gap = (code: string, subject: string | null, detail: string | null, section = 'static_analysis'): Gap => ({ section, code, subject, detail });

describe('better results', () => {
  it('turns gaps into actions: the missing tool with its hint, the model key, the call ceiling and the build scripts', () => {
    const hints = improvementHints([
      gap('task_unsupported', 'cppcheck', 'tool_not_configured'),
      gap('task_unsupported', 'cppcheck.m02', 'tool_not_configured'),
      gap('task_unsupported', 'go-vet', 'tool_not_configured'),
      gap('role_not_succeeded', 'review.interpret', 'model_not_configured', 'code_review'),
      gap('role_not_succeeded', 'review.investigate', 'model_not_configured', 'code_review'),
      gap('review_scope_budget_exhausted', null, null, 'code_review'),
      gap('task_unsupported', 'cargo-check.m01', 'repository_code_not_allowed'),
      gap('tests_not_run', null, null, 'report'),
    ], SETUP, project({ send_source_to_model: true, run_build_scripts: false, fetch_dependencies: true }));
    expect(hints).toEqual([
      { key: 'tool:cppcheck', text: t.improve.tool('cppcheck', 'apt install cppcheck') },
      { key: 'tool:go', text: t.improve.toolGeneric('go') },
      { key: 'model', text: t.improve.model },
      { key: 'budget', text: t.improve.budget },
      { key: 'build_scripts', text: t.improve.buildScripts('cargo-check'), turnOn: 'run_build_scripts' },
    ]);
  });

  it('offers the build-script switch only while it is off, and leaves a server-mode grant to the operator', () => {
    const blocked = [gap('task_unsupported', 'cargo-check', 'repository_code_not_allowed')];
    expect(improvementHints(blocked, SETUP, project({ send_source_to_model: true, run_build_scripts: true, fetch_dependencies: true }))).toEqual([]);
    expect(improvementHints(blocked, { ...SETUP, mode: 'server' }, project(undefined))).toEqual([{ key: 'build_scripts', text: t.improve.operatorGrant('cargo-check') }]);
    const withheld = [gap('role_skipped', 'review.interpret', 'model_source_not_approved', 'code_review')];
    expect(improvementHints(withheld, SETUP, project({ send_source_to_model: false, run_build_scripts: true, fetch_dependencies: true }))).toEqual([{ key: 'send_source', text: t.improve.sendSource, turnOn: 'send_source_to_model' }]);
    expect(improvementHints([gap('tests_not_run', null, null, 'report')], SETUP, project(undefined))).toEqual([]);
  });

  it('asks to fetch dependencies the analyzers could not resolve: the project choice, the node switch or the operator', () => {
    const unresolved = [
      gap('dependencies_not_fetched', 'go-vet', 'dependency_unavailable', 'environment'),
      gap('dependencies_not_fetched', 'staticcheck.m02', 'dependency_unavailable', 'environment'),
      gap('dependencies_not_fetched', 'staticcheck', 'dependency_unavailable', 'environment'),
    ];
    // The project's choice is off: turn it on and retry.
    expect(improvementHints(unresolved, SETUP, project({ send_source_to_model: true, run_build_scripts: false, fetch_dependencies: false }))).toEqual([
      { key: 'fetch_dependencies', text: t.improve.fetchDependencies('go-vet, staticcheck'), turnOn: 'fetch_dependencies' },
    ]);
    // The choice is on, so this computer's switch is off.
    expect(improvementHints(unresolved, SETUP, project({ send_source_to_model: true, run_build_scripts: false, fetch_dependencies: true }))).toEqual([
      { key: 'fetch_dependencies', text: t.improve.dependencySwitch('go-vet, staticcheck') },
    ]);
    expect(improvementHints(unresolved, { ...SETUP, mode: 'server' }, project(undefined))).toEqual([
      { key: 'fetch_dependencies', text: t.improve.dependencyOperator('go-vet, staticcheck') },
    ]);
  });

  it('names the missing package manager once and the unreachable registry for dependencies not prepared', () => {
    const notPrepared = [
      gap('dependencies_not_prepared', 'deps.go', 'package_manager_not_configured', 'environment'),
      gap('dependencies_not_prepared', 'deps.go.m02', 'package_manager_not_configured', 'environment'),
      gap('dependencies_not_prepared', 'deps.go.m03', 'registry_unreachable', 'environment'),
      gap('dependencies_not_prepared', 'deps.go.m04', 'registry_unreachable', 'environment'),
      // Nothing to suggest for a checksum mismatch or an unpinned version.
      gap('dependencies_not_prepared', 'deps.go.m05', 'checksum_mismatch', 'environment'),
      gap('dependency_versions_not_pinned', 'deps.go', null, 'environment'),
    ];
    const choices = { send_source_to_model: true, run_build_scripts: true, fetch_dependencies: true };
    expect(improvementHints(notPrepared, SETUP, project(choices))).toEqual([
      { key: 'tool:go', text: t.improve.packageManager('go') },
      { key: 'dependency_registry', text: t.improve.registry },
    ]);
    // A missing go analyzer tool and a missing go package manager are one install.
    const withTool = { ...SETUP, tools: { detected_at: null, tools: [{ name: 'go', status: 'not_found', version: null, path: null, hint: 'install go 1.27', reason: null }] } };
    expect(improvementHints([gap('task_unsupported', 'go-vet', 'tool_not_configured'), notPrepared[0]!], withTool, project(choices))).toEqual([
      { key: 'tool:go', text: t.improve.tool('go', 'install go 1.27') },
    ]);
  });

  // Python dependency preparation.
  it('asks to fetch the packages a Python type checker could not import and names the Python install hint', () => {
    const choices = { send_source_to_model: true, run_build_scripts: true, fetch_dependencies: false };
    expect(improvementHints([gap('dependencies_not_fetched', 'mypy.m01', 'imports_unresolved', 'environment')], SETUP, project(choices))).toEqual([
      { key: 'fetch_dependencies', text: t.improve.fetchDependencies('mypy'), turnOn: 'fetch_dependencies' },
    ]);
    const withPython = { ...SETUP, tools: { detected_at: null, tools: [{ name: 'python', status: 'unusable', version: null, path: null, hint: 'install Python 3 with pip', reason: 'pip is not installed' }] } };
    expect(improvementHints([gap('dependencies_not_prepared', 'deps.python', 'package_manager_not_configured', 'environment')], withPython, project({ ...choices, fetch_dependencies: true }))).toEqual([
      { key: 'tool:python', text: t.improve.tool('python', 'install Python 3 with pip') },
    ]);
  });
});
