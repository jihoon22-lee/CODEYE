import { useCallback, useEffect, useId, useRef, useState, type FormEvent } from 'react';
import { userLabel, type AdminProvider, type AdminSourceRoot, type UserView } from './accounts';
import { AdminUsers } from './AdminUsers';
import { ApiError, type Api, type ServiceSettings } from './api';
import { CopyText } from './CopyText';
import { isAbsolutePath } from './quickStart';
import { AuditLog, type ActorOption } from './AuditLog';
import { describeError, formatInstant, shorten, type ErrorText } from './format';
import { ko as t } from './locales/ko';
import { ErrorNote } from './RunPanel';
import type { SessionController } from './session';

// The administrator area: the address to share, accounts, the service switches, the folders on
// the server and who may read them, the approved Git hosts and the server-wide audit log. Being
// an administrator opens no project; the screen says so.

type Tab = 'users' | 'service' | 'roots' | 'providers' | 'audit';
const TABS: Tab[] = ['users', 'service', 'roots', 'providers', 'audit'];
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
  // The settings carry the service address shown above the tabs and the switches of the service tab.
  const settings = useAdminList<ServiceSettings>(useCallback(signal => api.getSettings(signal).then(x => [x]), [api]), forbidden);
  const current = settings.items?.[0] ?? null;

  return <>
    <section aria-labelledby="admin-title">
      <h1 id="admin-title">{s.title}</h1>
      <p>{s.intro}</p>
      <p className="notice">{s.noProjectAccess}</p>
      {current && <div id="service-address" className="field">
        <span>{s.serviceAddress}: </span>
        {current.service_origin ? <><CopyText text={current.service_origin} id="service-address-value" /><small className="small">{s.serviceAddressHelp}</small></>
          : <small className="small">{s.serviceAddressLocal}</small>}
      </div>}
    </section>
    <nav aria-label={s.tabs.label} className="tabs">
      {TABS.map(x => <button key={x} type="button" aria-current={tab === x ? 'page' : undefined} onClick={() => setTab(x)}>{s.tabs[x]}</button>)}
    </nav>
    <section className="panel" aria-labelledby="admin-tab-title">
      <h2 id="admin-tab-title">{s.tabs[tab]}</h2>
      {tab === 'users' && <AdminUsers api={api} me={me} users={users} onChanged={reloadUsers} onForbidden={forbidden} />}
      {tab === 'service' && <ServiceSwitches api={api} settings={current} error={settings.error} reload={settings.reload} onSaved={settings.set} onForbidden={forbidden} />}
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
  return { items, error, reload: useCallback(() => setLoads(n => n + 1), []), set: useCallback((value: T[]) => setItems(value), []) };
}

function ServiceSwitches({ api, settings, error, reload, onSaved, onForbidden }: { api: Api; settings: ServiceSettings | null; error: ErrorText | null; reload: () => void; onSaved: (v: ServiceSettings[]) => void; onForbidden: () => void }) {
  const [busy, setBusy] = useState<string | null>(null);
  const [saveError, setSaveError] = useState<ErrorText | null>(null);
  const [done, setDone] = useState(false);
  const s = t.accounts.admin;
  if (error) return <><ErrorNote error={{ ...error, message: `${s.settingsLoadFailed} ${error.message}` }} /><button type="button" onClick={reload}>{s.reload}</button></>;
  if (!settings) return <p>{s.loading}</p>;
  async function change(key: 'others_model_review' | 'others_build_scripts', value: boolean) {
    setBusy(key);
    setSaveError(null);
    setDone(false);
    try {
      onSaved([await api.putSettings({ [key]: value })]);
      setDone(true);
    } catch (e) {
      setSaveError(describeError(e));
      if (e instanceof ApiError && e.status === 403) onForbidden();
    } finally {
      setBusy(null);
    }
  }
  const box = (key: 'others_model_review' | 'others_build_scripts', label: string, help: string) => <div className="field">
    <label className="inline"><input type="checkbox" id={`switch-${key.replaceAll('_', '-')}`} checked={settings[key]} disabled={busy !== null} aria-describedby={`switch-${key}-help`}
      onChange={e => void change(key, e.target.checked)} /> {label}{busy === key ? ` ${s.settingsSaving}` : ''}</label>
    <small id={`switch-${key}-help`} className="small">{help}</small>
  </div>;
  return <>
    <h3>{s.switches}</h3>
    <p className="small">{s.switchesNote}</p>
    {box('others_model_review', s.othersModelReview, s.othersModelReviewHelp)}
    {box('others_build_scripts', s.othersBuildScripts, s.othersBuildScriptsHelp)}
    <div aria-live="polite">{done && <p role="status">{s.changed}</p>}</div>
    {saveError && <ErrorNote error={saveError} />}
  </>;
}

function AddSourceRoot({ api, onAdded, onForbidden }: { api: Api; onAdded: () => void; onForbidden: () => void }) {
  const [path, setPath] = useState('');
  const [label, setLabel] = useState('');
  const [everyone, setEveryone] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | ErrorText | null>(null);
  const [done, setDone] = useState<string | null>(null);
  const s = t.accounts.admin;
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (busy) return;
    setDone(null);
    const value = path.trim();
    if (value === '') { setError(s.addRootEmpty); return; }
    if (!isAbsolutePath(value)) { setError(s.addRootNotAbsolute); return; }
    setBusy(true);
    setError(null);
    try {
      const root = await api.addSourceRoot({ path: value, ...(label.trim() === '' ? {} : { label: label.trim() }), everyone });
      setPath('');
      setLabel('');
      setEveryone(false);
      setDone(s.rootAdded(root.label));
      onAdded();
    } catch (e) {
      setError(e instanceof ApiError && e.code === 'CONFLICT' ? s.rootConflict : describeError(e));
      if (e instanceof ApiError && e.status === 403) onForbidden();
    } finally {
      setBusy(false);
    }
  }
  return <form className="panel" onSubmit={submit} noValidate aria-labelledby="add-root-title">
    <h3 id="add-root-title">{s.addRoot}</h3>
    <div className="field">
      <label htmlFor="add-root-path">{s.addRootPath}</label>
      <input id="add-root-path" type="text" autoComplete="off" spellCheck={false} value={path} aria-describedby="add-root-path-help" onChange={e => { setPath(e.target.value); setError(null); }} />
      <small id="add-root-path-help" className="small">{s.addRootPathHelp}</small>
    </div>
    <div className="field">
      <label htmlFor="add-root-label">{s.addRootLabel}</label>
      <input id="add-root-label" type="text" maxLength={100} autoComplete="off" value={label} onChange={e => setLabel(e.target.value)} />
    </div>
    <div className="field">
      <label className="inline"><input id="add-root-everyone" type="checkbox" checked={everyone} onChange={e => setEveryone(e.target.checked)} /> {s.addRootEveryone}</label>
    </div>
    <button id="add-root-submit" type="submit" className="primary" disabled={busy} aria-busy={busy}>{busy ? s.addingRoot : s.addRootSubmit}</button>
    <div aria-live="polite">{done && <p role="status">{done}</p>}</div>
    {typeof error === 'string' ? <p role="alert" className="error">{error}</p> : error && <ErrorNote error={error} />}
  </form>;
}

function AddProvider({ api, onAdded, onForbidden }: { api: Api; onAdded: () => void; onForbidden: () => void }) {
  const [host, setHost] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | ErrorText | null>(null);
  const [done, setDone] = useState<string | null>(null);
  const s = t.accounts.admin;
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (busy) return;
    setDone(null);
    const value = host.trim();
    if (value === '') { setError(s.addProviderEmpty); return; }
    setBusy(true);
    setError(null);
    try {
      const before = await api.listAdminProviders();
      const added = await api.addProvider(value);
      setHost('');
      setDone(before.some(p => p.id === added.id) ? s.providerExisted(added.host) : s.providerAdded(added.host));
      onAdded();
    } catch (e) {
      setError(describeError(e));
      if (e instanceof ApiError && e.status === 403) onForbidden();
    } finally {
      setBusy(false);
    }
  }
  return <form className="panel" onSubmit={submit} noValidate aria-labelledby="add-provider-title">
    <h3 id="add-provider-title">{s.addProvider}</h3>
    <div className="field">
      <label htmlFor="add-provider-host">{s.addProviderHost}</label>
      <input id="add-provider-host" type="text" autoComplete="off" spellCheck={false} value={host} aria-describedby="add-provider-help" onChange={e => { setHost(e.target.value); setError(null); }} />
      <small id="add-provider-help" className="small">{s.addProviderHelp}</small>
    </div>
    <button id="add-provider-submit" type="submit" className="primary" disabled={busy} aria-busy={busy}>{busy ? s.addingProvider : s.addProviderSubmit}</button>
    <div aria-live="polite">{done && <p role="status">{done}</p>}</div>
    {typeof error === 'string' ? <p role="alert" className="error">{error}</p> : error && <ErrorNote error={error} />}
  </form>;
}

function SourceRoots({ api, users, onForbidden }: { api: Api; users: UsersState; onForbidden: () => void }) {
  const { items, error, reload } = useAdminList<AdminSourceRoot>(signal => api.listAdminSourceRoots(signal), onForbidden);
  const s = t.accounts.admin;
  return <>
    <p className="small">{s.rootsIntro}</p>
    <AddSourceRoot api={api} onAdded={reload} onForbidden={onForbidden} />
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

  async function setEveryone(everyone: boolean) {
    setBusy('everyone');
    setError(null);
    setDone(false);
    try {
      await api.setSourceRootEveryone(root.id, everyone);
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

  return <li data-root={root.label}>
    <h3>{root.label}</h3>
    {root.path && <p>{s.rootPath}: <code>{root.path}</code></p>}
    <p className="small">{s.rootCreated(formatInstant(root.created_at))} · <code>{root.id}</code></p>
    <div className="field">
      <label className="inline"><input type="checkbox" data-root-everyone={root.label} checked={root.everyone} disabled={busy !== null} aria-label={s.rootEveryoneFor(root.label)}
        onChange={e => void setEveryone(e.target.checked)} /> {s.rootEveryone}</label>
      {root.everyone && <small className="small">{s.rootEveryoneOn}</small>}
    </div>
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
    <AddProvider api={api} onAdded={reload} onForbidden={onForbidden} />
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
