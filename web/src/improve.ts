import type { AnalysisChoices, Project, Setup } from './api';
import type { Dependency, Ledger } from './parseReportSections';
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
// The JS package managers are entry scripts run by node.
const JS_RUNTIME = 'node';
// Tasks whose build-script grant also lets deps.rust fetch the crates they read.
const RUST_TASKS = new Set(['cargo-check', 'clippy']);
// A dependency task (deps.go, deps.go.m02) names its ecosystem second.
const ecosystemOf = (subject: string) => taskOf(subject).split('.')[1] ?? taskOf(subject);
// A reason may carry a detail after a colon.
const codeOf = (detail: string) => detail.split(':')[0];

// improvementHints turns a report's gaps into the few actions that would fill them: install a
// missing tool or package manager, set a model key, raise the call ceiling, reach the
// dependency registry, or turn on a project choice or node switch. Gaps
// without a known remedy are left to the report's own gap list. The review
// ledger, when the report has one, says how many calls the refused slots need.
// dependencies are the report's dependency tasks: a JS task's record names the
// manager its lockfile chose.
export function improvementHints(gaps: Gap[], setup: Setup | null, project: Project, ledger: Ledger | null = null, dependencies: Dependency[] = []): Hint[] {
  const out: Hint[] = [];
  const seen = new Set<string>();
  const push = (h: Hint) => { if (!seen.has(h.key)) { seen.add(h.key); out.push(h); } };
  // A service started by `codeye up` records its setup (model null otherwise); its node follows
  // the project's own choices. A separately run controller leaves them to the node operator.
  const followsProject = setup !== null && setup.model !== null;
  const blocked: string[] = [];
  const notOffered: string[] = [];
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
      push({ key: 'model', text: setup?.is_admin ? (followsProject ? t.improve.model : t.improve.modelOperator) : t.improve.askAdminModel });
    } else if (g.code === 'review_scope_budget_exhausted') {
      push(budgetHint(ledger));
    } else if (g.code === 'review_scope_not_planned' && ledger) {
      const n = ledger.modules.filter((m) => m.cls !== 'secondary').reduce((s, m) => s + m.notPlanned, 0);
      if (n > 0) push({ key: 'not_planned', text: t.improve.notPlanned(n) });
    } else if (detail === 'repository_code_not_allowed' && g.subject) {
      if (!blocked.includes(taskOf(g.subject))) blocked.push(taskOf(g.subject));
    } else if (detail === 'repository_code_not_offered' && g.subject) {
      if (!notOffered.includes(taskOf(g.subject))) notOffered.push(taskOf(g.subject));
    } else if (detail === 'model_review_not_offered') {
      push({ key: 'model_not_offered', text: t.improve.modelNotOffered });
    } else if (detail === 'model_source_not_approved') {
      sourceWithheld = true;
    } else if (g.code === 'dependencies_not_fetched' && g.subject) {
      if (!unresolved.includes(taskOf(g.subject))) unresolved.push(taskOf(g.subject));
    } else if (g.code === 'dependencies_not_prepared' && codeOf(detail) === 'package_manager_not_configured' && g.subject) {
      // The same key as a missing analyzer tool of that name, so installing go is said once.
      const ecosystem = ecosystemOf(g.subject);
      const status = (n: string) => setup?.tools?.tools.find(x => x.name === n)?.status;
      let names = PACKAGE_MANAGERS[ecosystem] ?? [ecosystem];
      if (ecosystem === 'javascript') {
        // Name the missing piece: node when detection did not find it, else the manager the
        // record chose (by lockfile), else the managers this computer lacks.
        const chosen = dependencies.find(d => d.task === g.subject)?.manager;
        if (setup?.tools && status(JS_RUNTIME) !== 'found') names = [JS_RUNTIME];
        else if (chosen && names.includes(chosen)) names = [chosen];
      }
      const missing = names.filter(n => status(n) !== 'found');
      for (const name of missing.length > 0 ? missing : names) {
        const tool = setup?.tools?.tools.find(x => x.name === name);
        push({ key: `tool:${name}`, text: tool?.hint ? t.improve.tool(name, tool.hint) : t.improve.packageManager(name) });
      }
    } else if (g.code === 'dependencies_not_prepared' && codeOf(detail) === 'registry_unreachable') {
      push({ key: 'dependency_registry', text: t.improve.registry });
    } else if (g.code === 'dependencies_not_prepared' && codeOf(detail) === 'offline_no_registry_configured') {
      push({ key: 'dependency_offline', text: t.improve.offlineRegistry });
    }
  }
  if (blocked.length > 0) {
    const tasks = blocked.join(', ');
    // Under `codeye up` the project's own choice decides; elsewhere the node operator's grant.
    if (followsProject && project.analysis && !project.analysis.run_build_scripts) {
      // With dependency fetching on, the same grant lets deps.rust fetch the crates first.
      const crates = project.analysis.fetch_dependencies && blocked.some(b => RUST_TASKS.has(b));
      push({ key: 'build_scripts', text: crates ? t.improve.buildScriptsCrates(tasks) : t.improve.buildScripts(tasks), turnOn: 'run_build_scripts' });
    } else if (!followsProject) {
      push({ key: 'build_scripts', text: t.improve.operatorGrant(tasks) });
    }
  }
  // Only the administrator's switch took it away: nothing for the project to turn on.
  if (notOffered.length > 0) push({ key: 'build_scripts_not_offered', text: t.improve.buildScriptsNotOffered(notOffered.join(', ')) });
  if (unresolved.length > 0) {
    const tasks = unresolved.join(', ');
    // Under `codeye up`: the project's choice, or (when it is on) the node switch in the
    // configuration. Elsewhere the node operator starts the agent with fetching on.
    if (followsProject && project.analysis && !project.analysis.fetch_dependencies) {
      push({ key: 'fetch_dependencies', text: t.improve.fetchDependencies(tasks), turnOn: 'fetch_dependencies' });
    } else if (followsProject) {
      push({ key: 'fetch_dependencies', text: t.improve.dependencySwitch(tasks) });
    } else {
      push({ key: 'fetch_dependencies', text: t.improve.dependencyOperator(tasks) });
    }
  }
  if (sourceWithheld && followsProject && project.analysis && !project.analysis.send_source_to_model) {
    push({ key: 'send_source', text: t.improve.sendSource, turnOn: 'send_source_to_model' });
  }
  return followsProject && !setup.is_admin ? forMember(out) : out;
}

// HOST_SIDE are the hints only the person who runs the service can act on (install, configure,
// run codeye up again). A user who is not the administrator gets them as a request to that person.
const HOST_SIDE = /^(tool:|toolchain:|budget$|dependency_registry$|dependency_offline$|fetch_dependencies$)/;

function forMember(hints: Hint[]): Hint[] {
  const tools = hints.filter(h => h.key.startsWith('tool:')).map(h => h.key.slice('tool:'.length));
  const out: Hint[] = [];
  for (const h of hints) {
    if (h.key.startsWith('tool:')) {
      if (h.key === `tool:${tools[0]}`) out.push({ key: 'ask_admin_tools', text: t.improve.askAdminTools(tools.join(', ')) });
    } else if (HOST_SIDE.test(h.key) && !h.turnOn) {
      out.push({ ...h, text: t.improve.askAdmin(h.text) });
    } else {
      out.push(h);
    }
  }
  return out;
}

// operatorHints reports whether the hints are for the person who runs the service (the terminal
// command is offered then): the administrator, or a setup that is not known.
export function operatorHints(setup: Setup | null): boolean {
  return !setup || setup.is_admin;
}

// The largest call ceiling (docs/REVIEW.md#호출-상한과-승인).
export const MAX_REVIEW_CALLS = 207;
const MAX_EXTENDED_REVIEW_CALLS = 2013;

// Estimate enough calls to admit the remaining slots and verify one reply
// per slot. Historical file-only scope artifacts reserved all six calls.
function budgetHint(ledger: Ledger | null): Hint {
  if (!ledger || ledger.callsUsed === null) return { key: 'budget', text: t.improve.budget };
  let slots = ledger.primarySlotsRefused;
  if (slots === 0) {
    const refused = new Set<string>();
    for (const m of ledger.modules) for (const x of m.slices) if (/^review\.investigate\.s\d{3}$/.test(x.task) && x.status === 'budget_exhausted') refused.add(x.task);
    slots = refused.size;
  }
  if (slots === 0) return { key: 'budget', text: t.improve.budget };
  const needed = ledger.callsUsed + 2 * (slots - 1) + (ledger.files === null ? 6 : 2);
  const ceiling = ledger.extended ? MAX_EXTENDED_REVIEW_CALLS : MAX_REVIEW_CALLS;
  return { key: 'budget', text: needed > ceiling ? t.improve.budgetCallsMax(ledger.callsUsed, slots, needed, ceiling) : t.improve.budgetCalls(ledger.callsUsed, slots, needed) };
}
