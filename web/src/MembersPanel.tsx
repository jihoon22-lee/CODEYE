import { useCallback, useEffect, useId, useState, type FormEvent } from 'react';
import { MEMBER_ROLES, personName, type Member, type MemberRole } from './accounts';
import { ApiError, type Api, type Project } from './api';
import { describeError, roleLabel, type ErrorText } from './format';
import { ko as t } from './locales/ko';
import { ErrorNote } from './RunPanel';
import { useWait } from './wait';

// Project members (docs/API.md "구성원"). Only the owner and managers may list or change them;
// everyone else sees their own role. The server enforces every change; a refusal here re-reads the
// project, because the caller's own role may have changed meanwhile.

const memberName = (m: Member) => personName(m) ?? t.accounts.members.unnamed(m.user_id.slice(0, 8));

type Props = { api: Api; project: Project; me: string; onAccessChange: () => void };

export function MembersPanel({ api, project, me, onAccessChange }: Props) {
  const s = t.accounts.members;
  if (!project.can_manage) {
    return <section className="panel" aria-labelledby="members-title">
      <h2 id="members-title">{s.title}</h2>
      <p><strong>{s.myRole(roleLabel(project.role))}</strong></p>
      {Object.hasOwn(t.accounts.roleAllows, project.role) && <p className="small">{t.accounts.roleAllows[project.role]}</p>}
      <p className="small">🔒 {s.readOnly}</p>
    </section>;
  }
  return <ManagedMembers api={api} project={project} me={me} onAccessChange={onAccessChange} />;
}

function ManagedMembers({ api, project, me, onAccessChange }: Props) {
  const [members, setMembers] = useState<Member[] | null>(null);
  const [error, setError] = useState<ErrorText | null>(null);
  const [loads, setLoads] = useState(0);
  const [notice, setNotice] = useState<string | null>(null);
  const reload = useCallback(() => setLoads(n => n + 1), []);
  const s = t.accounts.members;

  useEffect(() => {
    const controller = new AbortController();
    api.listMembers(project.id, controller.signal).then(
      items => { setMembers(items); setError(null); },
      e => {
        if (controller.signal.aborted) return;
        setError(describeError(e));
        if (e instanceof ApiError && (e.status === 403 || e.status === 404)) onAccessChange();
      },
    );
    return () => controller.abort();
  }, [api, project.id, loads, onAccessChange]);

  // After any change: re-read members, and the project (the caller's own role may be different now).
  const changed = useCallback((text: string | null) => {
    setNotice(text);
    reload();
    onAccessChange();
  }, [reload, onAccessChange]);

  return <section className="panel" aria-labelledby="members-title">
    <h2 id="members-title">{s.title}</h2>
    <p><strong>{s.myRole(roleLabel(project.role))}</strong></p>
    {error && <><ErrorNote error={{ ...error, message: `${s.loadFailed} ${error.message}` }} /><button type="button" onClick={reload}>{s.reload}</button></>}
    {members === null && !error && <p>{s.loading}</p>}
    <div aria-live="polite">{notice && <p role="status">{notice}</p>}</div>
    {members && <div className="table"><table>
      <caption>{s.caption}</caption>
      <thead><tr>{s.columns.map(c => <th key={c} scope="col">{c}</th>)}</tr></thead>
      <tbody>{members.map(m => <MemberRow key={`${m.user_id}:${m.role}`} api={api} projectId={project.id} member={m} self={m.user_id === me} onChanged={changed} />)}</tbody>
    </table></div>}
    <AddMember api={api} projectId={project.id} onAdded={changed} />
  </section>;
}

function MemberRow({ api, projectId, member, self, onChanged }: { api: Api; projectId: string; member: Member; self: boolean; onChanged: (notice: string | null) => void }) {
  const [role, setRole] = useState<string>(member.role);
  const [step, setStep] = useState<'idle' | 'saving' | 'confirm' | 'removing'>('idle');
  const [error, setError] = useState<ErrorText | null>(null);
  const s = t.accounts.members;
  const name = memberName(member);
  const label = <>{name}{self && <> {s.you}</>}</>;
  const status = <td>{member.disabled ? s.disabled : s.active}</td>;

  if (member.role === 'owner') {
    return <tr><td>{label}</td><td>{roleLabel('owner')}</td>{status}<td className="small">🔒 {s.ownerFixed}</td></tr>;
  }

  function fail(e: unknown) {
    setError(e instanceof ApiError && e.status === 409 ? { message: s.ownerConflict, detail: null, requestId: e.requestId } : describeError(e));
    setStep('idle');
    // A refusal may mean this member or the caller's role changed elsewhere.
    if (e instanceof ApiError && (e.status === 403 || e.status === 404 || e.status === 409)) onChanged(null);
  }
  async function save() {
    setStep('saving');
    setError(null);
    try {
      await api.setMemberRole(projectId, member.user_id, role as MemberRole);
      setStep('idle');
      onChanged(s.saved(name));
    } catch (e) { fail(e); }
  }
  async function remove() {
    setStep('removing');
    setError(null);
    try {
      await api.removeMember(projectId, member.user_id);
      setStep('idle');
      onChanged(s.removed(name));
    } catch (e) { fail(e); }
  }
  const busy = step === 'saving' || step === 'removing';
  return <tr>
    <td>{label}</td>
    <td>
      <label className="visually-hidden" htmlFor={`role-${member.user_id}`}>{s.roleFor(name)}</label>
      <select id={`role-${member.user_id}`} value={role} onChange={e => setRole(e.target.value)} disabled={busy}>
        {MEMBER_ROLES.map(r => <option key={r} value={r}>{roleLabel(r)}</option>)}
      </select>
    </td>
    {status}
    <td>
      {step === 'confirm' || step === 'removing'
        ? <div role="group" aria-label={s.removeFor(name)}>
          <p>{s.removeConfirm(name)}{self && <> {s.removeSelf}</>}</p>
          <button type="button" className="danger" onClick={remove} disabled={busy}>{step === 'removing' ? s.removing : s.removeYes}</button>
          <button type="button" onClick={() => setStep('idle')} disabled={busy}>{s.removeNo}</button>
        </div>
        : <div className="actions">
          <button type="button" onClick={save} disabled={busy || role === member.role}>{step === 'saving' ? s.saving : s.save}</button>
          <button type="button" aria-label={s.removeFor(name)} onClick={() => setStep('confirm')} disabled={busy}>{s.remove}</button>
        </div>}
      {error && <ErrorNote error={error} />}
    </td>
  </tr>;
}

function AddMember({ api, projectId, onAdded }: { api: Api; projectId: string; onAdded: (notice: string) => void }) {
  const [username, setUsername] = useState('');
  const [role, setRole] = useState<MemberRole>('viewer');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<ErrorText | null>(null);
  const { remaining, wait } = useWait();
  const uid = useId();
  const s = t.accounts.members;

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (pending || remaining > 0) return;
    const name = username.trim();
    if (name === '') return setError({ message: s.usernameEmpty, detail: null, requestId: null });
    setPending(true);
    setError(null);
    try {
      const member = await api.addMember(projectId, name, role);
      setUsername('');
      onAdded(s.added(memberName(member)));
    } catch (e) {
      if (e instanceof ApiError && e.status === 429) wait(e.retryAfter);
      // The same answer for "no such user" and "disabled user": nothing else is revealed.
      else if (e instanceof ApiError && e.status === 404) setError({ message: s.addNotFound, detail: null, requestId: e.requestId });
      else setError(describeError(e));
    } finally {
      setPending(false);
    }
  }

  const blocked = remaining > 0;
  return <form onSubmit={submit} noValidate aria-labelledby={`${uid}-title`}>
    <h3 id={`${uid}-title`}>{s.add}</h3>
    <div className="field">
      <label htmlFor={`${uid}-user`}>{s.addUsername}</label>
      <input id={`${uid}-user`} type="text" autoComplete="off" autoCapitalize="none" spellCheck={false} maxLength={64} value={username}
        onChange={e => setUsername(e.target.value)} aria-describedby={`${uid}-help`} />
      <small id={`${uid}-help`} className="small">{s.addHelp}</small>
    </div>
    <div className="field">
      <label htmlFor={`${uid}-role`}>{s.addRole}</label>
      <select id={`${uid}-role`} value={role} onChange={e => setRole(e.target.value as MemberRole)}>
        {MEMBER_ROLES.map(r => <option key={r} value={r}>{roleLabel(r)}</option>)}
      </select>
    </div>
    <button type="submit" className="primary" disabled={pending || blocked} aria-busy={pending}>{pending ? s.adding : s.addSubmit}</button>
    <div aria-live="polite">
      {blocked && <p role="alert" className="error">{s.addThrottled(remaining)}</p>}
      {!blocked && error && <ErrorNote error={error} />}
    </div>
  </form>;
}
