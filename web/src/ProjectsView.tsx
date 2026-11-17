import { useCallback, useEffect, useState } from 'react';
import type { Api, Project, Provider, SourceRoot } from './api';
import { describeError, shorten, type ErrorText } from './format';
import type { Idempotency } from './idempotency';
import { ko as t } from './locales/ko';
import { ProjectForm } from './ProjectForm';
import { ErrorNote, RunPanel } from './RunPanel';

type Props = { api: Api; userId: string; idempotency: Idempotency };

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

export function ProjectsView({ api, userId, idempotency }: Props) {
  const roots = useLookup<SourceRoot>(useCallback(signal => api.listSourceRoots(signal), [api]));
  const providers = useLookup<Provider>(useCallback(signal => api.listProviders(signal), [api]));
  const [projects, setProjects] = useState<Project[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ErrorText | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [reloads, setReloads] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    api.listProjects(null, controller.signal).then(
      page => { setProjects(page.items); setCursor(page.next_cursor); setLoading(false); },
      e => { if (!controller.signal.aborted) { setError(describeError(e)); setLoading(false); } },
    );
    return () => controller.abort();
  }, [api, reloads]);

  async function loadMore() {
    if (!cursor || loading) return;
    setLoading(true);
    try {
      const page = await api.listProjects(cursor);
      setProjects(prev => [...prev, ...page.items.filter(item => !prev.some(p => p.id === item.id))]);
      setCursor(page.next_cursor);
      setError(null);
    } catch (e) {
      setError(describeError(e));
    } finally {
      setLoading(false);
    }
  }

  const selected = projects.find(p => p.id === selectedId) ?? null;
  const p = t.projects;
  return <>
    <section aria-labelledby="projects-title">
      <h1 id="projects-title">{p.title}</h1>
      <p>{p.intro}</p>
    </section>
    <div className="grid">
      <section className="panel" aria-labelledby="list-title">
        <h2 id="list-title">{p.listTitle}</h2>
        {error && <><ErrorNote error={{ ...error, message: `${p.loadFailed} ${error.message}` }} /><button type="button" onClick={() => setReloads(n => n + 1)}>{p.reload}</button></>}
        <div aria-live="polite" aria-busy={loading}>
          {loading && projects.length === 0 ? <p>{p.loading}</p> : projects.length === 0 && !error ? <p>{p.listEmpty}</p> : null}
        </div>
        <ul className="plain projects">
          {projects.map(project => {
            const summary = describeSource(project, roots.items, providers.items);
            return <li key={project.id}>
              <button type="button" className="item" aria-pressed={project.id === selectedId} onClick={() => setSelectedId(project.id)}>
                <strong>{project.name === '' ? shorten(summary, 60) : project.name}</strong>
                {project.name !== '' && <span className="small">{summary}</span>}
              </button>
            </li>;
          })}
        </ul>
        {cursor && <button type="button" onClick={loadMore} disabled={loading}>{p.loadMore}</button>}
      </section>
      <section className="panel" aria-labelledby="create-title">
        <h2 id="create-title" className="visually-hidden">{p.createTitle}</h2>
        {(roots.error || providers.error) && <p role="alert" className="error">{p.lookupFailed} {(roots.error ?? providers.error)!.message}</p>}
        <ProjectForm api={api} roots={roots.items} providers={providers.items} onCreated={project => { setProjects(prev => [project, ...prev.filter(x => x.id !== project.id)]); setSelectedId(project.id); }} />
      </section>
    </div>
    {selected && <ProjectDetail key={selected.id} api={api} userId={userId} project={selected} idempotency={idempotency} source={describeSource(selected, roots.items, providers.items)} />}
  </>;
}

function ProjectDetail({ api, userId, project, idempotency, source }: { api: Api; userId: string; project: Project; idempotency: Idempotency; source: string }) {
  const [openRun, setOpenRun] = useState<string | null>(null);
  const p = t.projects;
  const status = Object.hasOwn(p.sourceStatuses, project.source_status) ? p.sourceStatuses[project.source_status] : p.sourceStatusUnknown;
  return <>
    <section className="panel" aria-labelledby="detail-title">
      <h2 id="detail-title">{project.name === '' ? shorten(source, 60) : project.name}</h2>
      <dl>
        <dt>{p.sourceLabel}</dt><dd>{source}</dd>
        <dt>{p.sourceStatus}</dt><dd>{status}</dd>
        <dt>{p.projectId}</dt><dd><code>{project.id}</code></dd>
        <dt>{p.revisionLabel}</dt><dd>{project.revision}</dd>
      </dl>
      {project.capability_gaps.length > 0 && <>
        <h3>{p.gaps}</h3>
        <ul>{project.capability_gaps.map(gap => <li key={gap}>{Object.hasOwn(p.gapLabels, gap) ? p.gapLabels[gap] : shorten(gap, 80)}</li>)}</ul>
      </>}
      {project.last_run && <>
        <h3>{p.lastRun}</h3>
        <p className="small">{p.lastRunHint(shorten(project.last_run.status, 32))}</p>
        <button type="button" onClick={() => setOpenRun(project.last_run!.id)} disabled={openRun === project.last_run.id}>{p.lastRunOpen}</button>
      </>}
    </section>
    <RunPanel api={api} userId={userId} project={project} idempotency={idempotency} openRunId={openRun} />
  </>;
}
