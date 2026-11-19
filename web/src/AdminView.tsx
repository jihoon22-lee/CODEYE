import { useCallback, useEffect, useId, useRef, useState } from 'react';
import { userLabel, type AdminProvider, type AdminSourceRoot, type UserView } from './accounts';
import { AdminUsers } from './AdminUsers';
import { ApiError, type Api } from './api';
import { AuditLog, type ActorOption } from './AuditLog';
import { describeError, formatInstant, shorten, type ErrorText } from './format';
import { ko as t } from './locales/ko';
import { ErrorNote } from './RunPanel';
import type { SessionController } from './session';

// The administrator area: accounts, source-root grants, providers (read-only) and the
// server-wide audit log. Being an administrator opens no project; the screen says so.

type Tab = 'users' | 'roots' | 'providers' | 'audit';
const TABS: Tab[] = ['users', 'roots', 'providers', 'audit'];
// Pickers need every user; the list is small in practice, so follow the cursor to a fixed bound.
const MAX_USER_PAGES = 50;

export type UsersState = { items: UserView[]; truncated: boolean; error: ErrorText | null; loading: boolean };

type Props = { api: Api; controller: SessionController; me: string };

export function AdminView({ api, controller, me }: Props) {
  const [tab, setTab] = useState<Tab>('users');
  const [users, setUsers] = useState<UsersState>({ items: [], truncated: false, error: null, loading: true });
  const [loads, setLoads] = useState(0);
  const reloadUsers = useCallback(() => setLoads(n => n + 1), []);
  const s = t.accounts.admin;
  // A 403 here means the admin role is gone: re-read the session so the area disappears.
  const forbidden = useCallback(() => { void controller.refresh(); }, [controller]);

  useEffect(() => {
    const abort = new AbortController();
    (async () => {
      setUsers(u => ({ ...u, loading: true, error: null }));
      const items: UserView[] = [];
      let cursor: string | null = null;
      for (let page = 0; page < MAX_USER_PAGES; page++) {
        const result = await api.listUsers(cursor, abort.signal);
        items.push(...result.items);
        cursor = result.next_cursor;
        if (!cursor) break;
      }
      setUsers({ items, truncated: cursor !== null, error: null, loading: false });
    })().catch(e => {
      if (abort.signal.aborted) return;
      setUsers(u => ({ ...u, error: describeError(e), loading: false }));
      if (e instanceof ApiError && e.status === 403) forbidden();
    });
    return () => abort.abort();
  }, [api, loads, forbidden]);

  const actors: ActorOption[] = users.items.map(u => ({ id: u.id, label: userLabel(u) }));
  const loadAudit = useCallback((filter: Parameters<Api['listAudit']>[0], cursor: string | null, signal?: AbortSignal) => api.listAudit(filter, cursor, signal), [api]);

  return <>
    <section aria-labelledby="admin-title">
      <h1 id="admin-title">{s.title}</h1>
      <p>{s.intro}</p>
      <p className="notice">{s.noProjectAccess}</p>
    </section>
    <nav aria-label={s.tabs.label} className="tabs">
      {TABS.map(x => <button key={x} type="button" aria-current={tab === x ? 'page' : undefined} onClick={() => setTab(x)}>{s.tabs[x]}</button>)}
    </nav>
    <section className="panel" aria-labelledby="admin-tab-title">
      <h2 id="admin-tab-title">{s.tabs[tab]}</h2>
      {tab === 'users' && <AdminUsers api={api} me={me} users={users} onChanged={reloadUsers} onForbidden={forbidden} />}
      {tab === 'roots' && <SourceRoots api={api} users={users} onForbidden={forbidden} />}
      {tab === 'providers' && <Providers api={api} onForbidden={forbidden} />}
      {tab === 'audit' && <AuditLog load={loadAudit} actors={actors} projectFilter intro={t.accounts.audit.globalIntro} onForbidden={forbidden} />}
    </section>
  </>;
}

function useAdminList<T>(load: (signal: AbortSignal) => Promise<T[]>, onForbidden: () => void) {
  const [items, setItems] = useState<T[] | null>(null);
  const [error, setError] = useState<ErrorText | null>(null);
  const [loads, setLoads] = useState(0);
  const loadRef = useRef(load);
  loadRef.current = load;
  useEffect(() => {
    const abort = new AbortController();
    loadRef.current(abort.signal).then(
      value => { setItems(value); setError(null); },
      e => {
        if (abort.signal.aborted) return;
        setError(describeError(e));
        if (e instanceof ApiError && e.status === 403) onForbidden();
      },
    );
    return () => abort.abort();
  }, [loads, onForbidden]);
  return { items, error, reload: useCallback(() => setLoads(n => n + 1), []) };
}

function SourceRoots({ api, users, onForbidden }: { api: Api; users: UsersState; onForbidden: () => void }) {
  const { items, error, reload } = useAdminList<AdminSourceRoot>(signal => api.listAdminSourceRoots(signal), onForbidden);
  const s = t.accounts.admin;
  return <>
    <p className="small">{s.rootsIntro}</p>
    <p className="small">{s.revokeNote}</p>
    {error && <><ErrorNote error={{ ...error, message: `${s.rootsLoadFailed} ${error.message}` }} /><button type="button" onClick={reload}>{s.reload}</button></>}
    {items === null && !error && <p>{s.loading}</p>}
    {items?.length === 0 && <p>{s.rootsEmpty}</p>}
    {users.truncated && <p className="small">{s.usersTruncated}</p>}
    <ul className="plain roots">{items?.map(root => <RootItem key={root.id} api={api} root={root} users={users.items} onChanged={reload} onForbidden={onForbidden} />)}</ul>
  </>;
}

function RootItem({ api, root, users, onChanged, onForbidden }: { api: Api; root: AdminSourceRoot; users: UserView[]; onChanged: () => void; onForbidden: () => void }) {
  const [pick, setPick] = useState('');
  const [busy, setBusy] = useState<string | null>(null); // user id being changed, or 'grant'
  const [error, setError] = useState<ErrorText | null>(null);
  const [done, setDone] = useState(false);
  const uid = useId();
  const s = t.accounts.admin;
  const granted = new Set(root.grants.map(g => g.user_id));
  const candidates = users.filter(u => !u.disabled && !granted.has(u.id));
  const nameOf = (id: string, username: string | null) => {
    const u = users.find(x => x.id === id);
    return u ? userLabel(u) : username ?? id.slice(0, 8);
  };

  async function change(userId: string, action: 'grant' | 'revoke') {
    setBusy(action === 'grant' ? 'grant' : userId);
    setError(null);
    setDone(false);
    try {
      if (action === 'grant') await api.grantSourceRoot(root.id, userId);
      else await api.revokeSourceRoot(root.id, userId);
      setPick('');
      setDone(true);
      onChanged();
    } catch (e) {
      setError(describeError(e));
      if (e instanceof ApiError && e.status === 403) onForbidden();
      else onChanged();
    } finally {
      setBusy(null);
    }
  }

  return <li>
    <h3>{root.label}</h3>
    <p className="small">{s.rootCreated(formatInstant(root.created_at))} · <code>{root.id}</code></p>
    <h4>{s.grants}</h4>
    {root.grants.length === 0 ? <p className="small">{s.grantsNone}</p>
      : <ul className="plain">{root.grants.map(g => {
        const name = nameOf(g.user_id, g.username);
        return <li key={g.user_id}>
          {name} <span className="small">{s.grantedAt(formatInstant(g.granted_at))}</span>{' '}
          <button type="button" aria-label={s.revokeFor(name, root.label)} onClick={() => void change(g.user_id, 'revoke')} disabled={busy !== null}>
            {busy === g.user_id ? s.revoking : s.revoke}
          </button>
        </li>;
      })}</ul>}
    <div className="field">
      <label htmlFor={`${uid}-pick`}>{s.grantUser(root.label)}</label>
      <select id={`${uid}-pick`} value={pick} onChange={e => setPick(e.target.value)} disabled={busy !== null}>
        <option value="">{candidates.length === 0 ? s.grantNone : s.grantPick}</option>
        {candidates.map(u => <option key={u.id} value={u.id}>{userLabel(u)}</option>)}
      </select>
    </div>
    <button type="button" onClick={() => void change(pick, 'grant')} disabled={pick === '' || busy !== null}>{busy === 'grant' ? s.granting : s.grant}</button>
    <div aria-live="polite">{done && <p role="status">{s.changed}</p>}</div>
    {error && <ErrorNote error={error} />}
  </li>;
}

function Providers({ api, onForbidden }: { api: Api; onForbidden: () => void }) {
  const { items, error, reload } = useAdminList<AdminProvider>(signal => api.listAdminProviders(signal), onForbidden);
  const s = t.accounts.admin;
  return <>
    <p className="small">{s.providersIntro}</p>
    {error && <><ErrorNote error={{ ...error, message: `${s.providersLoadFailed} ${error.message}` }} /><button type="button" onClick={reload}>{s.reload}</button></>}
    {items === null && !error && <p>{s.loading}</p>}
    {items?.length === 0 && <p>{s.providersEmpty}</p>}
    {items && items.length > 0 && <div className="table"><table>
      <caption>{s.providersCaption}</caption>
      <thead><tr>{s.providerColumns.map(c => <th key={c} scope="col">{c}</th>)}</tr></thead>
      <tbody>{items.map(p => <tr key={p.id}>
        <td>{Object.hasOwn(s.providerKinds, p.kind) ? s.providerKinds[p.kind] : shorten(p.kind, 32)}</td>
        <td>{p.host}</td>
        <td>{p.api_url ?? s.apiDefault}</td>
        <td><code>{p.id}</code></td>
      </tr>)}</tbody>
    </table></div>}
  </>;
}
