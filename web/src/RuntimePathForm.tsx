import { useEffect, useId, useState, type FormEvent } from 'react';
import { ApiError, runtimeTools, type Api, type RuntimePaths, type RuntimePathSettings } from './api';
import { describeError } from './format';
import { ko } from './locales/ko';

const t = ko.runtimePaths;

export function RuntimePathForm({ api, onForbidden }: { api: Api; onForbidden: () => void }) {
  const id = useId();
  const [settings, setSettings] = useState<RuntimePathSettings | null>(null);
  const [draft, setDraft] = useState<RuntimePaths>({});
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState('');
  const [loads, setLoads] = useState(0);

  useEffect(() => {
    const abort = new AbortController();
    setError('');
    api.getRuntimePaths(abort.signal).then(view => {
      if (abort.signal.aborted) return;
      setSettings(view);
      setDraft(view.paths);
    }, cause => {
      if (abort.signal.aborted) return;
      setError(describeError(cause).message);
      if (cause instanceof ApiError && cause.status === 403) onForbidden();
    });
    return () => abort.abort();
  }, [api, onForbidden, loads]);

  if (!settings) {
    if (!error) return <p role="status">{ko.accounts.admin.loading}</p>;
    return <><p role="alert">{error}</p><button type="button" onClick={() => setLoads(n => n + 1)}>{t.reload}</button></>;
  }
  if (!settings.editable) return <p>{t.readOnly}</p>;
  const changes: RuntimePaths = {};
  for (const name of runtimeTools) {
    const value = (draft[name] ?? '').trim();
    if (value !== (settings.paths[name] ?? '')) changes[name] = value;
  }

  async function save(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError('');
    setSaved(false);
    try {
      const view = await api.saveRuntimePaths(changes);
      setSettings(view);
      setDraft(view.paths);
      setSaved(true);
    } catch (cause) {
      const name = runtimeTools.find(key => cause instanceof ApiError && cause.reason === key);
      setError(name ? t.invalid(t.fields[name]) : describeError(cause).message);
      if (cause instanceof ApiError && cause.status === 403) onForbidden();
    } finally {
      setBusy(false);
    }
  }

  return <form className="opencode-form" onSubmit={save}>
    <p>{t.intro}</p>
    {runtimeTools.map(name => <div className="field" key={name}>
      <label htmlFor={id + name}>{t.fields[name]}</label>
      <input id={id + name} value={draft[name] ?? ''} placeholder={t.examples[name]} spellCheck={false}
        autoComplete="off" disabled={busy} aria-describedby={id + name + '-auto'}
        onChange={e => {
          setDraft({ ...draft, [name]: e.target.value });
          setError('');
          setSaved(false);
        }} />
      <small id={id + name + '-auto'}>
        {settings.auto_detect
          ? <>{t.automatic} · {settings.automatic_paths[name] ? <code>{settings.automatic_paths[name]}</code> : t.missing}</>
          : t.disabled}
      </small>
    </div>)}
    <button className="primary" type="submit" disabled={busy || Object.keys(changes).length === 0}>{busy ? t.saving : t.save}</button>
    {saved && <p role="status">{t.saved}</p>}
    {error && <p role="alert" className="error">{error}</p>}
  </form>;
}
