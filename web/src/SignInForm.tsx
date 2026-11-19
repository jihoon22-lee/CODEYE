import { useId, useRef, useState, type FormEvent } from 'react';
import { ko as t } from './locales/ko';
import type { SessionController } from './session';
import { useWait } from './wait';

// Username and password sign-in, next to the launch-link flow. The password lives only in the
// input element: it is read on submit, sent once in the POST body and cleared right away.
// Every refused attempt shows the same message; a throttled one waits for the server's time.
export function SignInForm({ controller }: { controller: SessionController }) {
  const [username, setUsername] = useState('');
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState<'rejected' | 'unavailable' | null>(null);
  const password = useRef<HTMLInputElement>(null);
  const { remaining, wait } = useWait();
  const uid = useId();
  const s = t.accounts.signIn;

  async function submit(event: FormEvent) {
    event.preventDefault();
    const field = password.current;
    if (pending || remaining > 0 || !field) return;
    const secret = field.value;
    field.value = '';
    const name = username.trim();
    if (name === '' || secret === '') {
      setMessage('rejected');
      return;
    }
    setPending(true);
    setMessage(null);
    const result = await controller.login(name, secret);
    // On success this form unmounts with the anonymous screen.
    if (!result.ok) {
      setPending(false);
      if (result.kind === 'throttled') wait(result.retryAfter);
      else setMessage(result.kind === 'unavailable' ? 'unavailable' : 'rejected');
      password.current?.focus();
    }
  }

  const blocked = remaining > 0;
  return <form onSubmit={submit} noValidate aria-labelledby={`${uid}-title`}>
    <h2 id={`${uid}-title`}>{s.title}</h2>
    <div className="field">
      <label htmlFor={`${uid}-user`}>{s.username}</label>
      <input id={`${uid}-user`} type="text" name="username" autoComplete="username" autoCapitalize="none" spellCheck={false} maxLength={64}
        value={username} onChange={e => setUsername(e.target.value)} />
    </div>
    <div className="field">
      <label htmlFor={`${uid}-password`}>{s.password}</label>
      <input id={`${uid}-password`} ref={password} type="password" name="password" autoComplete="current-password" maxLength={256} />
    </div>
    <button type="submit" className="primary" disabled={pending || blocked} aria-busy={pending}>{pending ? s.submitting : s.submit}</button>
    <div aria-live="polite">
      {blocked && <p role="alert" className="error">{s.throttled(remaining)}</p>}
      {!blocked && message && <p role="alert" className="error">{s[message]}</p>}
    </div>
    <p className="small">{s.linkNote}</p>
  </form>;
}
