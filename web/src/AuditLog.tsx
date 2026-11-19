import { useCallback, useEffect, useId, useRef, useState, type FormEvent } from 'react';
import { AUDIT_ACTIONS, UUID, type AuditEvent, type AuditFilter, type AuditPage } from './accounts';
import { ApiError } from './api';
import { describeError, formatInstant, shorten, type ErrorText } from './format';
import { ko as t } from './locales/ko';
import { ErrorNote } from './RunPanel';

// The audit log (docs/SECURITY.md#감사): newest first, filtered by time range, actor, action and,
// for the server-wide log, project. Pages come from the server's cursor; nothing is re-sorted here.

export type ActorOption = { id: string; label: string };
type Load = (filter: AuditFilter, cursor: string | null, signal?: AbortSignal) => Promise<AuditPage>;
type Props = { load: Load; actors: ActorOption[]; projectFilter: boolean; intro: string; onForbidden?: () => void };
type Draft = { since: string; until: string; actor: string; action: string; project: string };
const EMPTY: Draft = { since: '', until: '', actor: '', action: '', project: '' };

const a = () => t.accounts.audit;
export const actionLabel = (action: string) => (Object.hasOwn(a().actions, action) ? a().actions[action]! : a().actionUnknown(shorten(action, 60)));

// datetime-local values are wall-clock times in this browser's zone; the server wants RFC 3339.
function instant(local: string): string | null {
  if (local === '') return null;
  const d = new Date(local);
  return Number.isNaN(d.getTime()) ? null : d.toISOString();
}

export function toFilter(draft: Draft, projectFilter: boolean): { filter: AuditFilter } | { error: string } {
  const since = instant(draft.since);
  const until = instant(draft.until);
  if (since && until && Date.parse(until) <= Date.parse(since)) return { error: a().rangeInvalid };
  const project = draft.project.trim();
  if (projectFilter && project !== '' && !UUID.test(project)) return { error: a().projectInvalid };
  const filter: AuditFilter = {};
  if (since) filter.since = since;
  if (until) filter.until = until;
  if (draft.actor) filter.actor = draft.actor;
  if (draft.action) filter.action = draft.action;
  if (projectFilter && project) filter.project = project.toLowerCase();
  return { filter };
}

export function AuditLog({ load, actors, projectFilter, intro, onForbidden }: Props) {
  const [draft, setDraft] = useState<Draft>(EMPTY);
  const [applied, setApplied] = useState<AuditFilter>({});
  const [items, setItems] = useState<AuditEvent[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ErrorText | null>(null);
  const [invalid, setInvalid] = useState<string | null>(null);
  const [runs, setRuns] = useState(0);
  const latest = useRef(0);
  const uid = useId();
  const s = a();

  const fail = useCallback((e: unknown) => {
    setError(describeError(e));
    if (e instanceof ApiError && e.status === 403) onForbidden?.();
  }, [onForbidden]);

  // The first page for the applied filter; a newer request makes older answers irrelevant.
  useEffect(() => {
    const controller = new AbortController();
    const n = ++latest.current;
    setLoading(true);
    setError(null);
    load(applied, null, controller.signal).then(
      page => { if (n !== latest.current) return; setItems(page.items); setCursor(page.next_cursor); setLoaded(true); setLoading(false); },
      e => { if (controller.signal.aborted || n !== latest.current) return; setItems([]); setCursor(null); setLoaded(false); setLoading(false); fail(e); },
    );
    return () => controller.abort();
  }, [load, applied, runs, fail]);

  async function more() {
    if (!cursor || loading) return;
    const n = ++latest.current;
    setLoading(true);
    setError(null);
    try {
      const page = await load(applied, cursor);
      if (n !== latest.current) return;
      setItems(prev => [...prev, ...page.items.filter(x => !prev.some(p => p.id === x.id))]);
      setCursor(page.next_cursor);
    } catch (e) {
      if (n === latest.current) fail(e);
    } finally {
      if (n === latest.current) setLoading(false);
    }
  }

  function apply(event: FormEvent) {
    event.preventDefault();
    const result = toFilter(draft, projectFilter);
    if ('error' in result) return setInvalid(result.error);
    setInvalid(null);
    setApplied(result.filter);
    setRuns(n => n + 1);
  }
  function clear() {
    setDraft(EMPTY);
    setInvalid(null);
    setApplied({});
    setRuns(n => n + 1);
  }
  const set = (key: keyof Draft) => (e: { target: { value: string } }) => setDraft(d => ({ ...d, [key]: e.target.value }));

  return <div>
    <p className="small">{intro}</p>
    <form onSubmit={apply} noValidate aria-labelledby={`${uid}-filters`}>
      <h3 id={`${uid}-filters`}>{s.filters}</h3>
      <div className="filters">
        <div className="field"><label htmlFor={`${uid}-since`}>{s.since}</label><input id={`${uid}-since`} type="datetime-local" step={1} value={draft.since} onChange={set('since')} /></div>
        <div className="field"><label htmlFor={`${uid}-until`}>{s.until}</label><input id={`${uid}-until`} type="datetime-local" step={1} value={draft.until} onChange={set('until')} /></div>
        <div className="field"><label htmlFor={`${uid}-actor`}>{s.actor}</label>
          <select id={`${uid}-actor`} value={draft.actor} onChange={set('actor')} aria-describedby={`${uid}-actor-hint`}>
            <option value="">{s.actorAll}</option>
            {actors.map(x => <option key={x.id} value={x.id}>{x.label}</option>)}
          </select>
          <small id={`${uid}-actor-hint`} className="small">{s.actorHint}</small>
        </div>
        <div className="field"><label htmlFor={`${uid}-action`}>{s.action}</label>
          <select id={`${uid}-action`} value={draft.action} onChange={set('action')}>
            <option value="">{s.actionAll}</option>
            {AUDIT_ACTIONS.map(x => <option key={x} value={x}>{`${actionLabel(x)} (${x})`}</option>)}
          </select>
        </div>
        {projectFilter && <div className="field"><label htmlFor={`${uid}-project`}>{s.project}</label>
          <input id={`${uid}-project`} type="text" autoComplete="off" spellCheck={false} maxLength={36} value={draft.project} onChange={set('project')} aria-describedby={`${uid}-project-help`} aria-invalid={invalid === s.projectInvalid} />
          <small id={`${uid}-project-help`} className="small">{s.projectHelp}</small>
        </div>}
      </div>
      <div className="actions">
        <button type="submit" className="primary" disabled={loading}>{s.apply}</button>
        <button type="button" onClick={clear} disabled={loading}>{s.clear}</button>
      </div>
      {invalid && <p role="alert" className="error">{invalid}</p>}
    </form>
    {error && <><ErrorNote error={{ ...error, message: `${s.loadFailed} ${error.message}` }} /><button type="button" onClick={() => setRuns(n => n + 1)}>{s.reload}</button></>}
    <div aria-live="polite" aria-busy={loading}>
      {loading && items.length === 0 && <p>{s.loading}</p>}
      {loaded && !loading && items.length === 0 && <p>{s.empty}</p>}
    </div>
    {items.length > 0 && <div className="table"><table>
      <caption>{s.caption}</caption>
      <thead><tr>{s.columns.map(c => <th key={c} scope="col">{c}</th>)}</tr></thead>
      <tbody>{items.map(e => <AuditRow key={e.id} event={e} />)}</tbody>
    </table></div>}
    {cursor && <button type="button" onClick={more} disabled={loading}>{s.more}</button>}
    {loaded && !cursor && items.length > 0 && <p className="small">{s.end}</p>}
  </div>;
}

function actorText(e: AuditEvent): string {
  const s = a();
  const kind = Object.hasOwn(s.actorKinds, e.actor_kind) ? s.actorKinds[e.actor_kind]! : s.actorUnknown(shorten(e.actor_kind, 32));
  const user = e.actor_username ?? (e.actor_user_id ? e.actor_user_id.slice(0, 8) : null);
  if (e.actor_kind === 'user' && user) return s.actorUser(user);
  if (e.actor_kind === 'node' && e.actor_node_id) return s.actorNode(e.actor_node_id.slice(0, 8));
  // System and CLI rows may still name the user they acted for (for example a schedule's actor).
  return user ? s.actorOnBehalf(kind, user) : kind;
}

function detailText(detail: Record<string, unknown>): string {
  return Object.entries(detail).map(([k, v]) => `${k}=${typeof v === 'string' ? v : JSON.stringify(v)}`).join(' · ');
}

function AuditRow({ event: e }: { event: AuditEvent }) {
  const s = a();
  const target = e.target_type === null ? s.none
    : `${Object.hasOwn(s.targetTypes, e.target_type) ? s.targetTypes[e.target_type] : shorten(e.target_type, 32)}${e.target_id ? ` ${shorten(e.target_id, 12)}` : ''}`;
  const detail = detailText(e.detail);
  return <tr>
    <td>{formatInstant(e.occurred_at)}</td>
    <td>{actorText(e)}</td>
    <td>{actionLabel(e.action)}<br /><code>{shorten(e.action, 60)}</code></td>
    <td>{target}</td>
    <td>{e.project_id ? <code>{e.project_id}</code> : s.none}</td>
    <td className="small">{detail === '' ? s.none : shorten(detail, 300)}{e.request_id && <><br />{s.requestId(e.request_id)}</>}</td>
  </tr>;
}
