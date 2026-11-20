import { useCallback, useEffect, useRef, useState } from 'react';
import type { Api, Project, ProjectPage, Provider, Setup, SourceRoot } from './api';
import { QuickStart } from './QuickStart';
import { describeError, formatClock, isTerminalRun, roleLabel, runStatusText, shorten, type ErrorText } from './format';
import { createPoller, type Poller } from './poller';
import { LastReportLink, ProjectActivity } from './StageView';
import { retryable } from './useRunPolling';
import type { Idempotency } from './idempotency';
import type { DeepLink } from './deepLink';
import { ko as t } from './locales/ko';
import { ProjectForm } from './ProjectForm';
import { ErrorNote, RunPanel } from './RunPanel';
import { SchedulePanel } from './SchedulePanel';
import { MembersPanel } from './MembersPanel';
import { ProjectAudit } from './ProjectAudit';

type Props = { api: Api; userId: string; idempotency: Idempotency; link?: DeepLink };

type Lookup<T> = { loading: boolean; items: T[]; error: ErrorText | null };
const initial = { loading: true, items: [], error: null };

function useLookup<T>(load: (signal: AbortSignal) => Promise<T[]>): Lookup<T> {
  const [state, setState] = useState<Lookup<T>>(initial);
  useEffect(() => {
    const controller = new AbortController();
    load(controller.signal).then(
      items => setState({ loading: false, items, error: null }),
      error => { if (!controller.signal.aborted) setState({ loading: false, items: [], error: describeError(error) }); },
    );
    return () => controller.abort();
  }, [load]);
  return state;
}

export function describeSource(project: Project, roots: SourceRoot[], providers: Provider[]): string {
  const s = project.source;
  if (s.kind === 'local') {
    const root = roots.find(r => r.id === s.source_root_id)?.label ?? t.projects.unknownRoot;
    return t.projects.localSource(root, s.relative_path);
  }
  const host = providers.find(p => p.id === s.provider_id)?.host ?? t.projects.unknownProvider;
  return t.projects.gitSource(host, s.repository_url, s.branch);
}

// A project needs polling while its representative run is not terminal. Servers without
// `activity` only expose last_run, which is the fallback.
export function hasActiveRun(p: Project): boolean {
  const status = p.activity ? p.activity.run?.status : p.last_run?.status;
  return status !== undefined && !isTerminalRun(status) || (p.activity?.other_active_runs ?? 0) > 0;
}

// Replace first-page entries with fresh values, keep entries loaded by "more" that the first page does not cover.
export function mergeFirstPage(prev: Project[], first: Project[]): Project[] {
  const ids = new Set(first.map(p => p.id));
  return [...first, ...prev.filter(p => !ids.has(p.id))];
}

export function ProjectsView({ api, userId, idempotency, link }: Props) {
  const roots = useLookup<SourceRoot>(useCallback(signal => api.listSourceRoots(signal), [api]));
  const providers = useLookup<Provider>(useCallback(signal => api.listProviders(signal), [api]));
  // What this service offers the user: the one-field start and the improvement hints use it.
  const setupLookup = useLookup<Setup>(useCallback(signal => api.getSetup(signal).then(s => [s]), [api]));
  const setup = setupLookup.items[0] ?? null;
  // The run to open in the selected project: a deep link's, then the one the quick start requested.
  const [opened, setOpened] = useState<{ project: string; run: string } | null>(link?.project && link.run ? { project: link.project, run: link.run } : null);
  // After the quick start the run panel is brought into view (it sits below the forms).
  const [started, setStarted] = useState(0);
  useEffect(() => {
    if (started === 0) return;
    const id = setTimeout(() => document.getElementById('run-title')?.scrollIntoView?.({ block: 'start' }), 0);
    return () => clearTimeout(id);
  }, [started]);
  const [projects, setProjects] = useState<Project[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ErrorText | null>(null);
  const [lastOk, setLastOk] = useState<Date | null>(null);
  // A deep link (codeye up) opens its project once it is in the list.
  const [selectedId, setSelectedId] = useState<string | null>(link?.project ?? null);
  const [reloads, setReloads] = useState(0);
  const latest = useRef<Project[]>([]);
  const poller = useRef<Poller<ProjectPage> | null>(null);
  const firstCursor = useRef(false);

  const commit = useCallback((next: Project[]) => { latest.current = next; setProjects(next); }, []);
  // Start (or fetch now) after a run was accepted or changed state; no-op before the first poller exists.
  const wake = useCallback(() => {
    const p = poller.current;
    if (!p) return;
    if (p.isRunning()) p.refresh(); else p.start();
  }, []);

  // Polls the first page. Polling stops once no project has a non-terminal run (nothing can change
  // without a user action) and is restarted by wake(), loadMore() and project creation.
  useEffect(() => {
    latest.current = [];
    firstCursor.current = false;
    setLoading(true);
    setError(null);
    const p = createPoller<ProjectPage>({
      fetch: signal => api.listProjects(null, signal),
      onData: page => {
        commit(mergeFirstPage(latest.current, page.items));
        if (!firstCursor.current) { firstCursor.current = true; setCursor(page.next_cursor); }
        setLoading(false);
        setError(null);
        setLastOk(new Date());
      },
      onError: e => { setError(describeError(e)); setLoading(false); },
      done: () => !latest.current.some(hasActiveRun),
      retryable,
    });
    poller.current = p;
    p.start();
    return () => { p.stop(); poller.current = null; };
  }, [api, reloads, commit]);

  async function loadMore() {
    if (!cursor || loading) return;
    setLoading(true);
    try {
      const page = await api.listProjects(cursor);
      commit([...latest.current, ...page.items.filter(item => !latest.current.some(p => p.id === item.id))]);
      setCursor(page.next_cursor);
      setError(null);
      if (page.items.some(hasActiveRun)) wake();
    } catch (e) {
      setError(describeError(e));
    } finally {
      setLoading(false);
    }
  }

  const selected = projects.find(p => p.id === selectedId) ?? null;
  const p = t.projects;
  useEffect(() => { if (selected === null) document.title = t.pageTitle(p.title); }, [selected, p.title]);
  // Every user starts with the one-field start; the projects are listed under it.
  const created = (project: Project) => { commit([project, ...latest.current.filter(x => x.id !== project.id)]); setSelectedId(project.id); };
  const list = <section className="panel list-panel" aria-labelledby="list-title">
    <h2 id="list-title">{p.listTitle}</h2>
    {error && <><ErrorNote error={{ ...error, message: `${p.loadFailed} ${error.message}` }} />{projects.length > 0 && lastOk && <p className="small">{t.stages.listPollError} {t.stages.lastConfirmed(formatClock(lastOk))}</p>}<button type="button" onClick={() => setReloads(n => n + 1)}>{p.reload}</button></>}
    <div aria-live="polite" aria-busy={loading}>
      {loading && projects.length === 0 ? <p>{p.loading}</p> : projects.length === 0 && !error ? <p className="small">{p.listEmpty}</p> : null}
    </div>
    <ul className="plain projects">
      {projects.map(project => {
        const summary = describeSource(project, roots.items, providers.items);
        return <li key={project.id}>
          <button type="button" className="item" aria-pressed={project.id === selectedId} onClick={() => setSelectedId(project.id)}>
            <strong>{project.name === '' ? shorten(summary, 60) : project.name}</strong>
            {project.name !== '' && <span className="small">{summary}</span>}
          </button>
          <ProjectActivity project={project} />
          <LastReportLink project={project} />
        </li>;
      })}
    </ul>
    {cursor && <div className="actions"><button type="button" onClick={loadMore} disabled={loading}>{p.loadMore}</button></div>}
  </section>;
  const create = <section className="panel start-panel" aria-labelledby="create-title">
    <h2 id="create-title" className="visually-hidden">{p.createTitle}</h2>
    {(roots.error || providers.error) && <p role="alert" className="error">{p.lookupFailed} {(roots.error ?? providers.error)!.message}</p>}
    <QuickStart api={api} userId={userId} idempotency={idempotency} setup={setup} providers={providers.items}
      onStarted={(project, run) => { commit([project, ...latest.current.filter(x => x.id !== project.id)]); setOpened({ project: project.id, run }); setSelectedId(project.id); setStarted(n => n + 1); wake(); }} />
    <details><summary>{p.otherForm}</summary><ProjectForm api={api} providers={providers.items} onCreated={created} /></details>
  </section>;
  return <>
    <section className="page-head" aria-labelledby="projects-title">
      <h1 id="projects-title">{p.title}</h1>
      <p>{p.intro}</p>
    </section>
    {create}{list}
    {selected && <ProjectDetail key={`${selected.id}:${opened?.project === selected.id ? opened.run : ''}`} api={api} userId={userId} project={selected} idempotency={idempotency} onRunChange={wake} source={describeSource(selected, roots.items, providers.items)}
      setup={setup} initialRun={opened?.project === selected.id ? opened.run : null} />}
  </>;
}

type DetailTab = 'run' | 'members' | 'audit';

function ProjectDetail({ api, userId, project, idempotency, source, onRunChange, initialRun, setup }: { api: Api; userId: string; project: Project; idempotency: Idempotency; source: string; onRunChange: () => void; initialRun: string | null; setup: Setup | null }) {
  // The run shown first: the one asked for (a link, the quick start), else the project's latest.
  const [openRun, setOpenRun] = useState<string | null>(initialRun ?? project.last_run?.id ?? null);
  const [chosen, setChosen] = useState<DetailTab>('run');
  // The audit tab exists only for the owner and managers; a lost role falls back to the run tab.
  const tabs: DetailTab[] = project.can_manage ? ['run', 'members', 'audit'] : ['run', 'members'];
  const tab = tabs.includes(chosen) ? chosen : 'run';
  const p = t.projects;
  const status = Object.hasOwn(p.sourceStatuses, project.source_status) ? p.sourceStatuses[project.source_status] : p.sourceStatusUnknown;
  const title = project.name === '' ? shorten(source, 60) : project.name;
  // Opening a project brings it into view: it sits below the start form and the list.
  useEffect(() => { document.getElementById('detail-title')?.scrollIntoView?.({ block: 'start', behavior: 'smooth' }); }, []);
  useEffect(() => { document.title = t.pageTitle(title); }, [title]);
  // Gaps a reader must know stay in view; "not read yet" is a detail.
  const quiet = new Set(['source_access_unverified']);
  const gapText = (gap: string) => Object.hasOwn(p.gapLabels, gap) ? p.gapLabels[gap] : shorten(gap, 80);
  const loud = project.capability_gaps.filter(gap => !quiet.has(gap));
  const soft = project.capability_gaps.filter(gap => quiet.has(gap));
  return <>
    <section className="panel" aria-labelledby="detail-title">
      <div className="detail-head">
        <h2 id="detail-title">{title}</h2>
        <p className="small">{source} · {t.accounts.members.myRoleLabel} {roleLabel(project.role)}</p>
      </div>
      {loud.length > 0 && <div className="notice"><h3 className="visually-hidden">{p.gaps}</h3><ul className="plain">{loud.map(gap => <li key={gap}>{gapText(gap)}</li>)}</ul></div>}
      {project.last_run && openRun !== project.last_run.id && <div className="actions">
        <p className="small">{p.lastRunHint(runStatusText(project.last_run.status))}</p>
        <button type="button" onClick={() => { setOpenRun(project.last_run!.id); setChosen('run'); }}>{p.lastRunOpen}</button>
      </div>}
      <details className="disclosure">
        <summary>{p.details}</summary>
        <dl>
          <dt>{p.sourceLabel}</dt><dd>{source}</dd>
          <dt>{p.sourceStatus}</dt><dd>{status}</dd>
          <dt>{p.projectId}</dt><dd><code>{project.id}</code></dd>
          <dt>{p.revisionLabel}</dt><dd>{project.revision}</dd>
          <dt>{t.accounts.members.myRoleLabel}</dt><dd>{roleLabel(project.role)}</dd>
        </dl>
        {soft.length > 0 && <ul className="small">{soft.map(gap => <li key={gap}>{gapText(gap)}</li>)}</ul>}
      </details>
    </section>
    <nav aria-label={t.accounts.tabs.label} className="tabs">
      {tabs.map(x => <button key={x} type="button" aria-current={tab === x ? 'page' : undefined} onClick={() => setChosen(x)}>{t.accounts.tabs[x]}</button>)}
    </nav>
    {tab === 'run' && <>
      <RunPanel api={api} userId={userId} project={project} idempotency={idempotency} openRunId={openRun} onRunChange={onRunChange} setup={setup} />
      <SchedulePanel api={api} projectId={project.id} />
    </>}
    {tab === 'members' && <MembersPanel api={api} project={project} me={userId} onAccessChange={onRunChange} />}
    {tab === 'audit' && <ProjectAudit api={api} projectId={project.id} onForbidden={onRunChange} />}
  </>;
}
