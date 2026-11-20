import { useEffect, useState, type FormEvent } from 'react';
import { ApiError, type AccountGitHub, type Api } from './api';
import { describeError, type ErrorText } from './format';
import { ko } from './locales/ko';
import { ErrorNote } from './RunPanel';

const t = ko.accounts.github;

// The signed-in user's own GitHub connection (GET/PUT/DELETE /account/github). The token is
// typed into a password field, sent once and cleared; it is never shown or read back.
export function GitHubAccount({ api }: { api: Api }) {
  const [view, setView] = useState<AccountGitHub | null>(null);
  const [loadError, setLoadError] = useState<ErrorText | null>(null);
  const [loads, setLoads] = useState(0);
  const [token, setToken] = useState('');
  const [busy, setBusy] = useState<'save' | 'remove' | null>(null);
  const [error, setError] = useState<string | ErrorText | null>(null);
  const [done, setDone] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    setLoadError(null);
    api.getAccountGitHub(controller.signal).then(setView, e => { if (!controller.signal.aborted) setLoadError(describeError(e)); });
    return () => controller.abort();
  }, [api, loads]);

  async function save(event: FormEvent) {
    event.preventDefault();
    if (busy) return;
    setDone(null);
    const value = token.trim();
    if (value === '') { setError(t.empty); return; }
    setBusy('save');
    setError(null);
    try {
      setView(await api.putAccountGitHub(value));
      setToken('');
      setDone(t.saved);
    } catch (e) {
      setError(e instanceof ApiError && e.code === 'GITHUB_TOKEN_REFUSED' ? t.refused : describeError(e));
    } finally {
      setBusy(null);
    }
  }

  async function remove() {
    if (busy) return;
    setBusy('remove');
    setError(null);
    setDone(null);
    try {
      await api.deleteAccountGitHub();
      setView(v => v && { ...v, connected: false, account: null, source: null });
      setDone(t.removed);
    } catch (e) {
      setError(describeError(e));
    } finally {
      setBusy(null);
    }
  }

  if (loadError) return <section className="panel" aria-labelledby="github-title"><h1 id="github-title">{t.title}</h1>
    <ErrorNote error={{ ...loadError, message: `${t.loadFailed} ${loadError.message}` }} /><button type="button" onClick={() => setLoads(n => n + 1)}>{t.reload}</button></section>;
  if (!view) return <section className="panel" role="status"><p>{t.loading}</p></section>;
  const source = view.source === null ? null : Object.hasOwn(t.sources, view.source) ? t.sources[view.source] : view.source;
  const unavailable = view.storable ? null : (view.reason && Object.hasOwn(t.unavailable, view.reason) ? t.unavailable[view.reason] : t.unavailableOther);
  return <section className="panel" aria-labelledby="github-title">
    <h1 id="github-title">{t.title}</h1>
    <p>{t.intro}</p>
    <dl>
      <dt>{t.status}</dt>
      <dd id="github-status" data-connected={view.connected ? 'true' : 'false'}>{view.connected ? t.connected(view.account) : t.notConnected}</dd>
      {view.connected && source && <><dt>{t.source}</dt><dd>{source}</dd></>}
    </dl>
    {unavailable ? <p className="notice" id="github-unavailable">{unavailable}</p> : <form onSubmit={save} noValidate>
      <div className="field">
        <label htmlFor="github-token">{t.token}</label>
        <input id="github-token" type="password" autoComplete="off" spellCheck={false} value={token} aria-describedby="github-token-help"
          onChange={e => { setToken(e.target.value); setError(null); }} />
        <small id="github-token-help" className="small">{t.tokenHelp} {t.storedNote}</small>
      </div>
      <div className="actions">
        <button id="github-save" type="submit" className="primary" disabled={busy !== null} aria-busy={busy === 'save'}>{busy === 'save' ? t.saving : t.save}</button>
        {view.connected && <button id="github-remove" type="button" onClick={() => void remove()} disabled={busy !== null}>{busy === 'remove' ? t.removing : t.remove}</button>}
      </div>
    </form>}
    <div aria-live="polite">{done && <p role="status">{done}</p>}</div>
    {typeof error === 'string' ? <p role="alert" className="error">{error}</p> : error && <ErrorNote error={error} />}
  </section>;
}
