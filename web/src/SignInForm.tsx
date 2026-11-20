import { useId, useRef, useState, type FormEvent } from 'react';
import { ApiError } from './api';
import { ko as t } from './locales/ko';
import type { SessionController } from './session';
import { useWait } from './wait';

// The password lives in the input, is sent in the POST body, and is cleared on submit.
export function SignInForm({ controller }: { controller: SessionController }) {
  const [identifier, setIdentifier] = useState('');
  const [registering, setRegistering] = useState(false);
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const password = useRef<HTMLInputElement>(null);
  const { remaining, wait } = useWait();
  const uid = useId();
  const s = t.accounts.signIn;
  const registration = t.accounts.registration;
  const form = registering ? registration : s;
  const blocked = !registering && remaining > 0;

  function switchForm() {
    if (password.current) password.current.value = '';
    setRegistering(!registering);
    setMessage(null);
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    const field = password.current;
    if (pending || blocked || !field) return;
    const secret = field.value;
    field.value = '';
    const name = identifier.trim();
    if (name === '' || secret === '') {
      setMessage(registering ? registration.invalid : s.rejected);
      return;
    }
    setPending(true);
    setMessage(null);
    const result = await (registering ? controller.register(name, secret) : controller.login(name, secret));
    // On success this form unmounts with the anonymous screen.
    if (!result.ok) {
      setPending(false);
      if (result.kind === 'throttled') wait(result.retryAfter);
      else if (result.kind === 'unavailable') setMessage(s.unavailable);
      else if (registering) {
        const taken = result.kind === 'error' && result.error instanceof ApiError && result.error.code === 'EMAIL_TAKEN';
        setMessage(taken ? registration.taken : registration.invalid);
      } else setMessage(s.rejected);
      password.current?.focus();
    }
  }

  return <form onSubmit={submit} noValidate aria-labelledby={`${uid}-title`}>
    <h2 id={`${uid}-title`}>{form.title}</h2>
    <div className="field">
      <label htmlFor={`${uid}-user`}>{registering ? registration.email : s.username}</label>
      <input id={`${uid}-user`} type={registering ? 'email' : 'text'} name="username" autoComplete="username" autoCapitalize="none" spellCheck={false} maxLength={1024}
        value={identifier} onChange={e => setIdentifier(e.target.value)} required />
    </div>
    <div className="field">
      <label htmlFor={`${uid}-password`}>{s.password}</label>
      <input id={`${uid}-password`} ref={password} type="password" name="password" autoComplete={registering ? 'new-password' : 'current-password'}
        maxLength={512} aria-describedby={registering ? `${uid}-rules` : undefined} required />
      {registering && <small id={`${uid}-rules`}>{registration.rules}</small>}
    </div>
    <button type="submit" className="primary" disabled={pending || blocked} aria-busy={pending}>{pending ? form.submitting : form.submit}</button>
    {' '}<button type="button" onClick={switchForm} disabled={pending}>{form.switchForm}</button>
    <div aria-live="polite">
      {blocked && <p role="alert" className="error">{s.throttled(remaining)}</p>}
      {!blocked && message && <p role="alert" className="error">{message}</p>}
    </div>
    {!registering && <p className="small">{s.linkNote}</p>}
  </form>;
}
