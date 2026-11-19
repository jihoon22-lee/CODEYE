import type { AnalysisChoices, Project, Setup } from './api';
import { ko as t } from './locales/ko';

export type Gap = { section: string; code: string; subject: string | null; detail: string | null };

// One line of "더 나은 결과를 얻으려면". turnOn names the project choice whose action
// ("켜고 다시 시도") changes it and retries; only a manager can take it.
export type Hint = { key: string; text: string; turnOn?: keyof AnalysisChoices };

// Analyzer task keys whose tool has another name in the tool table.
const TOOL_OF: Record<string, string> = { 'go-vet': 'go', 'cargo-check': 'cargo', 'cargo-clippy': 'clippy' };
// A task of one module carries its suffix (go-vet.m02).
const taskOf = (subject: string) => subject.replace(/\.m\d+$/, '');
const toolOf = (subject: string) => TOOL_OF[taskOf(subject)] ?? taskOf(subject);

const MISSING_TOOL = new Set(['tool_not_configured', 'tool_unavailable']);
// The tools a dependency task of an ecosystem fetches with (the tool table's names).
const PACKAGE_MANAGERS: Record<string, string[]> = { go: ['go'], rust: ['cargo'], javascript: ['npm', 'pnpm', 'yarn'] };
// Tasks whose build-script grant also lets deps.rust fetch the crates they read.
const RUST_TASKS = new Set(['cargo-check', 'clippy']);
// A dependency task (deps.go, deps.go.m02) names its ecosystem second.
const ecosystemOf = (subject: string) => taskOf(subject).split('.')[1] ?? taskOf(subject);
// A reason may carry a detail after a colon.
const codeOf = (detail: string) => detail.split(':')[0];

// improvementHints turns a report's gaps into the few actions that would fill them: install a
// missing tool or package manager, set a model key, raise the call ceiling, reach the
// dependency registry, or turn on a project choice or node switch. Gaps
// without a known remedy are left to the report's own gap list.
export function improvementHints(gaps: Gap[], setup: Setup | null, project: Project): Hint[] {
  const out: Hint[] = [];
  const seen = new Set<string>();
  const push = (h: Hint) => { if (!seen.has(h.key)) { seen.add(h.key); out.push(h); } };
  const personal = setup?.mode === 'personal';
  const blocked: string[] = [];
  const unresolved: string[] = [];
  let sourceWithheld = false;
  for (const g of gaps) {
    const detail = g.detail ?? '';
    if (MISSING_TOOL.has(detail) && g.subject) {
      const name = toolOf(g.subject);
      const tool = setup?.tools?.tools.find(x => x.name === name);
      push({ key: `tool:${name}`, text: tool?.hint ? t.improve.tool(name, tool.hint) : t.improve.toolGeneric(name) });
    } else if (detail === 'toolchain_not_configured' && g.subject) {
      push({ key: `toolchain:${taskOf(g.subject)}`, text: t.improve.toolchain(taskOf(g.subject)) });
    } else if (detail === 'model_not_configured') {
      push({ key: 'model', text: t.improve.model });
    } else if (g.code === 'review_scope_budget_exhausted') {
      push({ key: 'budget', text: t.improve.budget });
    } else if (detail === 'repository_code_not_allowed' && g.subject) {
      if (!blocked.includes(taskOf(g.subject))) blocked.push(taskOf(g.subject));
    } else if (detail === 'model_source_not_approved') {
      sourceWithheld = true;
    } else if (g.code === 'dependencies_not_fetched' && g.subject) {
      if (!unresolved.includes(taskOf(g.subject))) unresolved.push(taskOf(g.subject));
    } else if (g.code === 'dependencies_not_prepared' && codeOf(detail) === 'package_manager_not_configured' && g.subject) {
      // The same key as a missing analyzer tool of that name, so installing go is said once.
      const ecosystem = ecosystemOf(g.subject);
      const names = PACKAGE_MANAGERS[ecosystem] ?? [ecosystem];
      // Of several managers (npm, pnpm, yarn) name the ones this computer lacks.
      const missing = names.filter(n => setup?.tools?.tools.find(x => x.name === n)?.status !== 'found');
      for (const name of missing.length > 0 ? missing : names) {
        const tool = setup?.tools?.tools.find(x => x.name === name);
        push({ key: `tool:${name}`, text: tool?.hint ? t.improve.tool(name, tool.hint) : t.improve.packageManager(name) });
      }
    } else if (g.code === 'dependencies_not_prepared' && codeOf(detail) === 'registry_unreachable') {
      push({ key: 'dependency_registry', text: t.improve.registry });
    }
  }
  if (blocked.length > 0) {
    const tasks = blocked.join(', ');
    // In personal mode the project's own choice decides; elsewhere the node operator's grant.
    if (personal && project.analysis && !project.analysis.run_build_scripts) {
      // With dependency fetching on, the same grant lets deps.rust fetch the crates first.
      const crates = project.analysis.fetch_dependencies && blocked.some(b => RUST_TASKS.has(b));
      push({ key: 'build_scripts', text: crates ? t.improve.buildScriptsCrates(tasks) : t.improve.buildScripts(tasks), turnOn: 'run_build_scripts' });
    } else if (!personal) {
      push({ key: 'build_scripts', text: t.improve.operatorGrant(tasks) });
    }
  }
  if (unresolved.length > 0) {
    const tasks = unresolved.join(', ');
    // Personal mode: the project's choice, or (when it is on) the node switch in the
    // configuration. Elsewhere the node operator starts the agent with fetching on.
    if (personal && project.analysis && !project.analysis.fetch_dependencies) {
      push({ key: 'fetch_dependencies', text: t.improve.fetchDependencies(tasks), turnOn: 'fetch_dependencies' });
    } else if (personal) {
      push({ key: 'fetch_dependencies', text: t.improve.dependencySwitch(tasks) });
    } else {
      push({ key: 'fetch_dependencies', text: t.improve.dependencyOperator(tasks) });
    }
  }
  if (sourceWithheld && personal && project.analysis && !project.analysis.send_source_to_model) {
    push({ key: 'send_source', text: t.improve.sendSource, turnOn: 'send_source_to_model' });
  }
  return out;
}
