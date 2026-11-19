import { useEffect, useState } from 'react';
import type { Api, Setup } from './api';
import { DoctorView } from './DoctorView';
import { describeError, formatInstant, type ErrorText } from './format';
import { ko } from './locales/ko';
import { ErrorNote } from './RunPanel';

const t = ko.tools;

// The tools panel of a personal installation: the tools `codeye up` found (<state>/tools.json),
// how to add a missing one, the model and Git. A server-mode controller does not know its nodes'
// tools; there the doctor file view stays.
export function ToolsView({ api }: { api: Api }) {
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
  if (setup.mode !== 'personal') return <DoctorView />;
  const tools = setup.tools?.tools ?? [];
  const status = (s: string) => (Object.hasOwn(t.statuses, s) ? t.statuses[s] : t.statusUnknown(s));
  return <>
    <section aria-labelledby="tools-title"><h1 id="tools-title">{t.title}</h1><p>{t.intro}</p></section>
    <section className="panel" aria-labelledby="tools-table">
      <h2 id="tools-table">{t.table}</h2>
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
      {tools.some(x => x.status !== 'found') && <p className="small">{t.addAfter}</p>}
    </section>
    <section className="panel" aria-labelledby="tools-setup">
      <h2 id="tools-setup" className="visually-hidden">{t.model}</h2>
      <dl>
        <dt>{t.model}</dt>
        <dd>{setup.model?.configured && setup.model.id ? t.modelSet(setup.model.id, Object.hasOwn(t.probes, setup.model.probe ?? '') ? t.probes[setup.model.probe!] : setup.model.probe ?? '—') : t.modelNone}</dd>
        <dt>{t.git}</dt>
        <dd>{setup.git?.available ? t.gitOn(setup.git.hosts.join(', ')) : t.gitOff}</dd>
        <dt>{t.folders}</dt>
        <dd>{setup.readable_folders.length === 0 ? t.foldersNone : <ul className="plain">{setup.readable_folders.map(f => <li key={f}><code>{f}</code></li>)}</ul>}</dd>
      </dl>
    </section>
  </>;
}
