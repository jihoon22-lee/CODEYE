import { useId, useRef, useState, type FormEvent } from 'react';
import { ApiError } from './api';
import { describeError, type ErrorText } from './format';
import { ko as t } from './locales/ko';
import { ErrorNote } from './RunPanel';
import type { SessionController, SessionUser } from './session';
import { useWait } from './wait';

type Props = { controller: SessionController; user: SessionUser; forced: boolean; onDone?: () => void };
type Problem = { kind: 'local'; text: string } | { kind: 'server'; error: ErrorText };

// Sets or changes the signed-in user's password. Forced when the session has must_change_password:
// then this is the only screen. Passwords stay in the input elements and are cleared on every submit.
export function PasswordForm({ controller, user, forced, onDone }: Props) {
  const current = useRef<HTMLInputElement>(null);
  const next = useRef<HTMLInputElement>(null);
  const confirm = useRef<HTMLInputElement>(null);
  const [pending, setPending] = useState(false);
  const [problem, setProblem] = useState<Problem | null>(null);
  const [done, setDone] = useState(false);
  const { remaining, wait } = useWait();
  const uid = useId();
  const s = t.accounts.password;
  const needsCurrent = user.hasPassword;

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (pending || remaining > 0) return;
    const values = [current, next, confirm].map(r => r.current?.value ?? '');
    for (const r of [current, next, confirm]) if (r.current) r.current.value = '';
    const [old, value, again] = values as [string, string, string];
    setDone(false);
    if (needsCurrent && old === '') return setProblem({ kind: 'local', text: s.currentEmpty });
    if (value === '') return setProblem({ kind: 'local', text: s.empty });
    if (value !== again) return setProblem({ kind: 'local', text: s.mismatch });
    setPending(true);
    setProblem(null);
    const result = await controller.changePassword(needsCurrent ? old : null, value);
    setPending(false);
    if (result.ok) {
      setDone(true);
      onDone?.();
      return;
    }
    if (result.kind === 'throttled') return wait(result.retryAfter);
    const error = result.kind === 'error' ? result.error : null;
    if (error instanceof ApiError && error.code === 'PASSWORD_INCORRECT') return setProblem({ kind: 'local', text: s.incorrect });
    if (error instanceof ApiError && error.status === 409) return setProblem({ kind: 'local', text: s.conflict });
    // 422 VALIDATION_FAILED carries the server's rule text; describeError shows it as plain text.
    setProblem({ kind: 'server', error: describeError(error) });
  }

  const titleId = `${uid}-title`;
  const identifier = user.email ?? user.username;
  if (identifier === null) {
    return <section className="panel" aria-labelledby={titleId}>
      <h1 id={titleId}>{forced ? s.forcedTitle : s.title}</h1>
      <p className="notice">{s.linkOnly}</p>
    </section>;
  }
  const blocked = remaining > 0;
  return <section className="panel" aria-labelledby={titleId}>
    <h1 id={titleId}>{forced ? s.forcedTitle : s.title}</h1>
    {forced && <p className="notice">{s.forcedIntro}</p>}
    <form onSubmit={submit} noValidate aria-labelledby={titleId}>
      {/* Lets password managers attach the new password to the right account. */}
      <input type="text" name="username" autoComplete="username" value={identifier} readOnly hidden />
      {needsCurrent && <div className="field">
        <label htmlFor={`${uid}-current`}>{s.current}</label>
        <input id={`${uid}-current`} ref={current} type="password" autoComplete="current-password" maxLength={512} />
      </div>}
      <div className="field">
        <label htmlFor={`${uid}-next`}>{s.next}</label>
        <input id={`${uid}-next`} ref={next} type="password" autoComplete="new-password" maxLength={512} aria-describedby={`${uid}-rules`} />
        <small id={`${uid}-rules`} className="small">{s.rules}</small>
      </div>
      <div className="field">
        <label htmlFor={`${uid}-confirm`}>{s.confirm}</label>
        <input id={`${uid}-confirm`} ref={confirm} type="password" autoComplete="new-password" maxLength={512} />
      </div>
      <p className="small">{s.effect}</p>
      <button type="submit" className="primary" disabled={pending || blocked} aria-busy={pending}>{pending ? s.submitting : s.submit}</button>
      <div aria-live="polite">
        {blocked && <p role="alert" className="error">{s.throttled(remaining)}</p>}
        {!blocked && problem?.kind === 'local' && <p role="alert" className="error">{problem.text}</p>}
        {!blocked && problem?.kind === 'server' && <ErrorNote error={problem.error} />}
        {done && <p role="status">{s.done}</p>}
      </div>
    </form>
  </section>;
}
