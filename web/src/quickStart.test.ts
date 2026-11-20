import { describe, expect, it } from 'vitest';
import type { Project, Setup } from './api';
import { improvementHints, type Gap } from './improve';
import type { Dependency } from './reportSections';
import { ko as t } from './locales/ko';
import { choiceLocked, choicesFor, noFolderFor, parseTarget, quickBody, unavailableReason } from './quickStart';

const SETUP: Setup = {
  is_admin: false,
  service_origin: null,
  model: { configured: false, id: null, probe: null },
  git: { available: true, hosts: ['github.com'] },
  readable_folders: ['/home/me'],
  analysis_defaults: { local: { send_source_to_model: true, run_build_scripts: true, fetch_dependencies: true }, git: { send_source_to_model: true, run_build_scripts: false, fetch_dependencies: true } },
  available: {},
  tools: { detected_at: null, tools: [{ name: 'cppcheck', status: 'not_found', version: null, path: null, hint: 'apt install cppcheck', reason: null }] },
  github: null,
  offline: false,
};
// A controller without a recorded setup (started on its own): the node operator decides.
const SEPARATE: Setup = { ...SETUP, model: null, tools: null };
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
    expect(quickBody({ kind: 'local', path: '/home/me/app' }, '', '', on, PROVIDERS, SETUP).body).toEqual({ analysis: on, open_existing: true, local_path: '/home/me/app' });
    const repo = parseTarget('https://github.com/o/r/tree/dev').target!;
    expect(quickBody(repo, ' mine ', '', on, PROVIDERS, SETUP).body).toEqual({ name: 'mine', analysis: on, open_existing: true, source: { kind: 'git', provider_id: 'gh', repository_url: 'https://github.com/o/r', branch: 'dev' } });
    expect(quickBody(parseTarget('https://github.com/o/r').target!, '', '', on, PROVIDERS, SETUP).body).toEqual({ analysis: on, open_existing: true, source: { kind: 'git', provider_id: 'gh', repository_url: 'https://github.com/o/r' } });
    expect(quickBody(parseTarget('https://gitlab.com/o/r').target!, '', '', on, PROVIDERS, SETUP).error).toBe(t.quick.validation.hostNotAllowed('gitlab.com'));
    expect(quickBody(repo, '', '', on, PROVIDERS, { ...SETUP, git: { available: false, hosts: [] } }).error).toBe(t.quick.validation.noGit);
  });

  it('says plainly what the service does not offer this user, and keeps a refused choice off', () => {
    const limited: Setup = { ...SETUP, readable_folders: [], available: {
      model_review: { available: false, reason: 'not_offered' },
      build_scripts: { available: false, reason: 'not_offered' },
      fetch_dependencies: { available: false, reason: 'unknown' },
      local_path: { available: false, reason: 'no_allowed_folder' },
      git_url: { available: true, reason: null },
    } };
    expect(unavailableReason(limited, 'model_review')).toBe(t.quick.reasons.not_offered);
    expect(unavailableReason(limited, 'local_path')).toBe(t.quick.reasons.no_allowed_folder);
    // Nothing recorded yet is not a refusal; neither is an available item or an older server.
    expect(unavailableReason(limited, 'fetch_dependencies')).toBeNull();
    expect(unavailableReason(limited, 'git_url')).toBeNull();
    expect(unavailableReason(SETUP, 'model_review')).toBeNull();
    expect(unavailableReason({ ...SETUP, available: { model_review: { available: false, reason: 'later_reason' } } }, 'model_review')).toBe(t.quick.reasonUnknown('later_reason'));
    expect(choiceLocked(limited, 'run_build_scripts')).toBe(true);
    expect(choicesFor('local', limited, { run_build_scripts: true, send_source_to_model: true })).toEqual({ send_source_to_model: false, run_build_scripts: false, fetch_dependencies: true });
    // A folder path with no folder allowed is refused before asking the server; Git is not.
    expect(noFolderFor({ kind: 'local', path: '/srv/x' }, limited)).toBe(true);
    expect(noFolderFor({ kind: 'local', path: '/srv/x' }, SETUP)).toBe(false);
    expect(noFolderFor(parseTarget('github.com/o/r').target!, limited)).toBe(false);
    expect(noFolderFor({ kind: 'local', path: '/srv/x' }, null)).toBe(false);
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
      // Dependency fetching is on, so the same switch fetches the crates first.
      { key: 'build_scripts', text: t.improve.buildScriptsCrates('cargo-check'), turnOn: 'run_build_scripts' },
    ]);
    const noFetch = project({ send_source_to_model: true, run_build_scripts: false, fetch_dependencies: false });
    expect(improvementHints([gap('task_unsupported', 'clippy', 'repository_code_not_allowed')], SETUP, noFetch)).toEqual([
      { key: 'build_scripts', text: t.improve.buildScripts('clippy'), turnOn: 'run_build_scripts' },
    ]);
  });

  it('offers the build-script switch only while it is off, and leaves a separate node\'s grant to the operator', () => {
    const blocked = [gap('task_unsupported', 'cargo-check', 'repository_code_not_allowed')];
    expect(improvementHints(blocked, SETUP, project({ send_source_to_model: true, run_build_scripts: true, fetch_dependencies: true }))).toEqual([]);
    expect(improvementHints(blocked, SEPARATE, project(undefined))).toEqual([{ key: 'build_scripts', text: t.improve.operatorGrant('cargo-check') }]);
    const withheld = [gap('role_skipped', 'review.interpret', 'model_source_not_approved', 'code_review')];
    expect(improvementHints(withheld, SETUP, project({ send_source_to_model: false, run_build_scripts: true, fetch_dependencies: true }))).toEqual([{ key: 'send_source', text: t.improve.sendSource, turnOn: 'send_source_to_model' }]);
    expect(improvementHints([gap('tests_not_run', null, null, 'report')], SETUP, project(undefined))).toEqual([]);
  });

  it('says that the administrator did not offer the build scripts or the model review, with nothing to turn on', () => {
    const gaps = [
      gap('task_unsupported', 'cargo-check', 'repository_code_not_offered'),
      gap('task_unsupported', 'clippy', 'repository_code_not_offered'),
      gap('role_skipped', 'review.interpret', 'model_review_not_offered', 'code_review'),
    ];
    expect(improvementHints(gaps, SETUP, project({ send_source_to_model: true, run_build_scripts: true, fetch_dependencies: true }))).toEqual([
      { key: 'model_not_offered', text: t.improve.modelNotOffered },
      { key: 'build_scripts_not_offered', text: t.improve.buildScriptsNotOffered('cargo-check, clippy') },
    ]);
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
    expect(improvementHints(unresolved, SEPARATE, project(undefined))).toEqual([
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
    // Offline without an internal mirror: name the mirror settings, never "turn fetching on".
    const offline = [
      gap('dependencies_not_prepared', 'deps.go', 'offline_no_registry_configured', 'environment'),
      gap('dependencies_not_prepared', 'deps.python', 'offline_no_registry_configured', 'environment'),
    ];
    expect(improvementHints(offline, SETUP, project(choices))).toEqual([
      { key: 'dependency_offline', text: t.improve.offlineRegistry },
    ]);
    // A missing go analyzer tool and a missing go package manager are one install.
    const withTool = { ...SETUP, tools: { detected_at: null, tools: [{ name: 'go', status: 'not_found', version: null, path: null, hint: 'install go 1.27', reason: null }] } };
    expect(improvementHints([gap('task_unsupported', 'go-vet', 'tool_not_configured'), notPrepared[0]!], withTool, project(choices))).toEqual([
      { key: 'tool:go', text: t.improve.tool('go', 'install go 1.27') },
    ]);
  });

  it('names cargo for Rust and the missing piece of a JS fetch: node or the chosen manager', () => {
    const choices = { send_source_to_model: true, run_build_scripts: true, fetch_dependencies: true };
    expect(improvementHints([gap('dependencies_not_prepared', 'deps.rust.m01', 'package_manager_not_configured', 'environment')], SETUP, project(choices))).toEqual([
      { key: 'tool:cargo', text: t.improve.packageManager('cargo') },
    ]);
    const js = [gap('dependencies_not_prepared', 'deps.javascript', 'package_manager_not_configured', 'environment')];
    const managers = { ...SETUP, tools: { detected_at: null, tools: [
      { name: 'node', status: 'found', version: '24.18.0', path: '/n/node', hint: null, reason: null },
      { name: 'npm', status: 'found', version: '11.16.0', path: '/n/npm-cli.js', hint: null, reason: null },
      { name: 'pnpm', status: 'not_found', version: null, path: null, hint: 'npm install pnpm', reason: null },
      { name: 'yarn', status: 'not_found', version: null, path: null, hint: 'npm install yarn@1', reason: null },
    ] } };
    expect(improvementHints(js, managers, project(choices))).toEqual([
      { key: 'tool:pnpm', text: t.improve.tool('pnpm', 'npm install pnpm') },
      { key: 'tool:yarn', text: t.improve.tool('yarn', 'npm install yarn@1') },
    ]);
    // Without detection results all three are named.
    expect(improvementHints(js, { ...SETUP, tools: null }, project(choices)).map(h => h.key)).toEqual(['tool:npm', 'tool:pnpm', 'tool:yarn']);
    // The record names the manager the lockfile chose: only that one is said.
    const chose = (manager: string) => [{ task: 'deps.javascript', manager } as Dependency];
    expect(improvementHints(js, managers, project(choices), null, chose('yarn'))).toEqual([
      { key: 'tool:yarn', text: t.improve.tool('yarn', 'npm install yarn@1') },
    ]);
    expect(improvementHints(js, { ...SETUP, tools: null }, project(choices), null, chose('pnpm')).map(h => h.key)).toEqual(['tool:pnpm']);
    // Without node the managers cannot run: node is the missing piece.
    const noNode = { ...managers, tools: { detected_at: null, tools: managers.tools.tools.map(x => x.name === 'node' ? { ...x, status: 'not_found', path: null } : x) } };
    expect(improvementHints(js, noNode, project(choices), null, chose('pnpm'))).toEqual([
      { key: 'tool:node', text: t.improve.packageManager('node') },
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
