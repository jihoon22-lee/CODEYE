import { useEffect, useState } from 'react';
import type { Api, Setup } from './api';
import { DoctorView } from './DoctorView';
import { describeError, formatInstant, type ErrorText } from './format';
import { CopyText } from './CopyText';
import { ko } from './locales/ko';
import { ErrorNote } from './RunPanel';

const t = ko.tools;

// The tools panel: the tools `codeye up` found (<state>/tools.json), how to add a missing one,
// the model and platform source access. A controller that recorded no setup (started
// on its own) does not know its nodes' tools; there the doctor file view stays. Tool paths and
// hints reach administrators only.
export function ToolsView({ api, onOpenCode, onRuntimePaths }: { api: Api; onOpenCode?: () => void; onRuntimePaths?: () => void }) {
  const [setup, setSetup] = useState<Setup | null>(null);
  const [error, setError] = useState<ErrorText | null>(null);
  const [loads, setLoads] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    setError(null);
    api.getSetup(controller.signal).then(setSetup, e => { if (!controller.signal.aborted) setError(describeError(e)); });
    return () => controller.abort();
  }, [api, loads]);
  if (error) return <section className="panel"><ErrorNote error={{ ...error, message: `${t.failed} ${error.message}` }} /><button type="button" onClick={() => setLoads(n => n + 1)}>{t.reload}</button></section>;
  if (!setup) return <section className="panel" role="status"><p>{t.loading}</p></section>;
  const openCode = setup.is_admin && onOpenCode && <button type="button" onClick={onOpenCode}>{ko.accounts.admin.tabs.opencode}</button>;
  const runtimePaths = setup.is_admin && onRuntimePaths && <button type="button" onClick={onRuntimePaths}>{ko.accounts.admin.tabs.runtimes}</button>;
  if (setup.tools === null && setup.model === null) return <>{runtimePaths}{openCode}<DoctorView /></>;
  const tools = setup.tools?.tools ?? [];
  const status = (s: string) => (Object.hasOwn(t.statuses, s) ? t.statuses[s] : t.statusUnknown(s));
  const model = setup.model;
  const probe = model?.probe && Object.hasOwn(t.probes, model.probe) ? t.probes[model.probe] : t.probes.not_probed;
  const modelStatus = setup.is_admin && model?.configured && model.id
    ? t.modelSet(model.id, probe)
    : setup.available.model_review?.available === true || (setup.available.model_review === undefined && model?.configured)
      ? t.modelAvailable
      : model?.configured ? t.modelUnavailable : t.modelNone;
  return <>
    <section aria-labelledby="tools-title"><h1 id="tools-title">{t.title}</h1><p>{t.intro}</p></section>
    <section className="panel" aria-labelledby="tools-table">
      <h2 id="tools-table">{t.table}</h2>
      {runtimePaths}
      {setup.tools?.detected_at && <p className="small">{t.detectedAt(formatInstant(setup.tools.detected_at))}</p>}
      {tools.length === 0 ? <p>{t.none}</p> : <div className="table"><table>
        <thead><tr>{t.columns.map(c => <th key={c} scope="col">{c}</th>)}</tr></thead>
        <tbody>{tools.map(x => <tr key={x.name} className={x.status === 'found' ? undefined : 'missing'}>
          <th scope="row">{x.name}</th>
          <td>{status(x.status)}</td>
          <td>{x.version ?? '—'}</td>
          <td>{x.status === 'found' ? <code>{x.path ?? '—'}</code> : <>{x.hint ?? x.reason ?? '—'}</>}</td>
        </tr>)}</tbody>
      </table></div>}
      {!setup.is_admin && <p className="small">{t.pathsAdminOnly}</p>}
      {setup.is_admin && tools.some(x => x.status !== 'found') && <><p className="small">{t.addAfter}</p><CopyText text="codeye up" label={t.command} /></>}
    </section>
    <section className="panel" aria-labelledby="tools-setup">
      <h2 id="tools-setup" className="visually-hidden">{t.model}</h2>
      <dl>
        <dt>{t.model}</dt>
        <dd>{modelStatus}{openCode && <div className="actions">{openCode}</div>}</dd>
        <dt>{t.git}</dt>
        <dd>{setup.git?.available ? t.gitOn(setup.git.hosts.join(', ')) : t.gitOff}</dd>
        <dt>{t.folders}</dt>
        <dd>{t.foldersNote}</dd>
        {(setup.github_connections?.length ?? 0) > 0 && <>
          <dt>{t.githubConnections}</dt>
          <dd><ul className="plain">{setup.github_connections?.map(g => <li key={g.host}><code>{g.host}</code> {g.account ? t.githubAccount(g.account) : g.token ? t.githubConfigured : t.githubPublic}</li>)}</ul>
          {setup.is_admin && <p className="small">{t.githubConfigHelp}</p>}</dd>
        </>}
      </dl>
    </section>
  </>;
}
