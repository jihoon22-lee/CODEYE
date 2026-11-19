import { useId, useRef, useState, type FormEvent } from 'react';
import { userLabel, type CreateUserBody, type UserView, type UserWithLink } from './accounts';
import type { UsersState } from './AdminView';
import { ApiError, type Api } from './api';
import { describeError, formatInstant, type ErrorText } from './format';
import { ko as t } from './locales/ko';
import { ErrorNote } from './RunPanel';

// Users: list, create, disable, admin role, password reset. A created or reset user's one-time
// login link is shown once, in this component's memory only: never stored, never a clickable link
// (opening it here would sign this browser in as that user), and gone once dismissed.

type Props = { api: Api; me: string; users: UsersState; onChanged: () => void; onForbidden: () => void };
type Issued = { name: string; link: UserWithLink['login_link'] };

const USERNAME = /^[a-z0-9][a-z0-9._-]{1,63}$/;

export function AdminUsers({ api, me, users, onChanged, onForbidden }: Props) {
  const [issued, setIssued] = useState<Issued | null>(null);
  const s = t.accounts.admin;
  const show = (result: UserWithLink) => {
    setIssued({ name: userLabel(result.user), link: result.login_link });
    onChanged();
  };
  return <>
    {issued && <OneTimeLink key={issued.link.url} issued={issued} onClose={() => setIssued(null)} />}
    <CreateUser api={api} onCreated={show} onForbidden={onForbidden} />
    <h3>{s.tabs.users}</h3>
    {users.error && <><ErrorNote error={{ ...users.error, message: `${s.usersLoadFailed} ${users.error.message}` }} /><button type="button" onClick={onChanged}>{s.reload}</button></>}
    {users.loading && users.items.length === 0 && <p>{s.loading}</p>}
    {!users.loading && !users.error && users.items.length === 0 && <p>{s.usersEmpty}</p>}
    {users.truncated && <p className="small">{s.usersTruncated}</p>}
    {users.items.length > 0 && <div className="table"><table>
      <caption>{s.usersCaption}</caption>
      <thead><tr>{s.userColumns.map(c => <th key={c} scope="col">{c}</th>)}</tr></thead>
      <tbody>{users.items.map(u => <UserRow key={`${u.id}:${u.is_admin}:${u.disabled}`} api={api} user={u} self={u.id === me} onChanged={onChanged} onForbidden={onForbidden} onIssued={show} />)}</tbody>
    </table></div>}
  </>;
}

function OneTimeLink({ issued, onClose }: { issued: Issued; onClose: () => void }) {
  const [copy, setCopy] = useState<'idle' | 'copied' | 'failed'>('idle');
  const field = useRef<HTMLInputElement>(null);
  const uid = useId();
  const s = t.accounts.admin;
  async function copyLink() {
    try {
      if (!navigator.clipboard) throw new Error('clipboard');
      await navigator.clipboard.writeText(issued.link.url);
      setCopy('copied');
    } catch {
      setCopy('failed');
      field.current?.select();
    }
  }
  return <section className="panel one-time" role="region" aria-labelledby={`${uid}-title`}>
    <h3 id={`${uid}-title`}>{s.linkTitle(issued.name)}</h3>
    <p className="notice">{s.linkOnce}</p>
    <p className="small">{s.linkDeliver}</p>
    <div className="field">
      <label htmlFor={`${uid}-link`}>{s.linkLabel}</label>
      <input id={`${uid}-link`} ref={field} type="text" readOnly value={issued.link.url} spellCheck={false} autoComplete="off" onFocus={e => e.currentTarget.select()} />
    </div>
    <p className="small">{s.linkExpires(formatInstant(issued.link.expires_at))}</p>
    <div className="actions">
      <button type="button" className="primary" onClick={() => void copyLink()}>{s.copy}</button>
      <button type="button" onClick={onClose}>{s.linkClose}</button>
    </div>
    <div aria-live="polite">
      {copy === 'copied' && <p role="status">{s.copied}</p>}
      {copy === 'failed' && <p role="alert" className="error">{s.copyFailed}</p>}
    </div>
  </section>;
}

function CreateUser({ api, onCreated, onForbidden }: { api: Api; onCreated: (r: UserWithLink) => void; onForbidden: () => void }) {
  const [username, setUsername] = useState('');
  const [display, setDisplay] = useState('');
  const [admin, setAdmin] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<ErrorText | null>(null);
  const uid = useId();
  const s = t.accounts.admin;

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (pending) return;
    const name = username.trim().toLowerCase();
    if (!USERNAME.test(name)) return setError({ message: s.createUsernameInvalid, detail: null, requestId: null });
    const body: CreateUserBody = { username: name, ...(display.trim() ? { display_name: display.trim() } : {}), ...(admin ? { is_admin: true } : {}) };
    setPending(true);
    setError(null);
    try {
      const result = await api.createUser(body);
      setUsername('');
      setDisplay('');
      setAdmin(false);
      onCreated(result);
    } catch (e) {
      setError(e instanceof ApiError && e.status === 409 ? { message: s.createTaken, detail: null, requestId: e.requestId } : describeError(e));
      if (e instanceof ApiError && e.status === 403) onForbidden();
    } finally {
      setPending(false);
    }
  }

  return <form onSubmit={submit} noValidate aria-labelledby={`${uid}-title`}>
    <h3 id={`${uid}-title`}>{s.create}</h3>
    <p className="small">{s.createNote}</p>
    <div className="field">
      <label htmlFor={`${uid}-user`}>{s.createUsername}</label>
      <input id={`${uid}-user`} type="text" autoComplete="off" autoCapitalize="none" spellCheck={false} maxLength={64} value={username}
        onChange={e => setUsername(e.target.value)} aria-describedby={`${uid}-help`} />
      <small id={`${uid}-help`} className="small">{s.createUsernameHelp}</small>
    </div>
    <div className="field">
      <label htmlFor={`${uid}-display`}>{s.createDisplay}</label>
      <input id={`${uid}-display`} type="text" autoComplete="off" maxLength={200} value={display} onChange={e => setDisplay(e.target.value)} />
    </div>
    <label className="inline"><input type="checkbox" checked={admin} onChange={e => setAdmin(e.target.checked)} /> {s.createAdmin}</label>
    <div className="actions"><button type="submit" className="primary" disabled={pending} aria-busy={pending}>{pending ? s.creating : s.createSubmit}</button></div>
    {error && <ErrorNote error={error} />}
  </form>;
}

type Step = 'idle' | 'confirm-disable' | 'confirm-reset' | 'sending';

function UserRow({ api, user, self, onChanged, onForbidden, onIssued }: { api: Api; user: UserView; self: boolean; onChanged: () => void; onForbidden: () => void; onIssued: (r: UserWithLink) => void }) {
  const [step, setStep] = useState<Step>('idle');
  const [error, setError] = useState<ErrorText | null>(null);
  const s = t.accounts.admin;
  const name = userLabel(user);

  async function send(action: 'disable' | 'admin' | 'reset') {
    setStep('sending');
    setError(null);
    try {
      const issued = action === 'reset' ? await api.resetPassword(user.id) : null;
      if (action === 'disable') await api.disableUser(user.id);
      else if (action === 'admin') await api.setUserAdmin(user.id, !user.is_admin);
      setStep('idle');
      // onIssued re-reads the list itself.
      if (issued) onIssued(issued);
      else onChanged();
    } catch (e) {
      setStep('idle');
      if (e instanceof ApiError && e.status === 409) {
        const message = action === 'reset' ? s.selfConflict : action === 'admin' && !user.is_admin ? s.disabledCannotBeAdmin : s.lastAdmin;
        setError({ message, detail: null, requestId: e.requestId });
      } else setError(describeError(e));
      if (e instanceof ApiError && e.status === 403) onForbidden();
      else onChanged();
    }
  }

  const badges = [
    user.is_admin && s.badgeAdmin,
    user.disabled ? s.badgeDisabled : s.badgeActive,
    !user.disabled && user.must_change_password && s.badgeMustChange,
    !user.disabled && !user.has_password && !user.must_change_password && s.badgeNoPassword,
  ].filter(Boolean) as string[];
  const busy = step === 'sending';
  return <tr>
    <td>{name}{self && <> {s.you}</>}<br /><code className="small">{user.id}</code></td>
    <td>{badges.map(b => <span key={b} className="badge role-badge">{b}</span>)}</td>
    <td>{user.last_login_at ? formatInstant(user.last_login_at) : s.neverLoggedIn}</td>
    <td>
      {user.disabled ? null : step === 'confirm-disable' || step === 'confirm-reset'
        ? <div role="group" aria-label={step === 'confirm-disable' ? s.disable : s.reset}>
          <p>{step === 'confirm-disable' ? <>{s.disableConfirm(name)}{self && <> {s.disableSelf}</>}</> : s.resetConfirm(name)}</p>
          <button type="button" className="danger" onClick={() => void send(step === 'confirm-disable' ? 'disable' : 'reset')}>
            {step === 'confirm-disable' ? s.disableYes : s.resetYes}
          </button>
          <button type="button" onClick={() => setStep('idle')}>{step === 'confirm-disable' ? s.disableNo : s.resetNo}</button>
        </div>
        : <div className="actions" role="group" aria-label={s.actionsFor(name)}>
          <button type="button" onClick={() => void send('admin')} disabled={busy}>{busy ? s.adminSaving : user.is_admin ? s.revokeAdmin : s.grantAdmin}</button>
          {self ? <span className="small">{s.resetSelf}</span>
            : <button type="button" onClick={() => setStep('confirm-reset')} disabled={busy}>{s.reset}</button>}
          <button type="button" onClick={() => setStep('confirm-disable')} disabled={busy}>{s.disable}</button>
        </div>}
      {error && <ErrorNote error={error} />}
    </td>
  </tr>;
}
