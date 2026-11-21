import { useEffect, useId, useState, type FormEvent } from 'react';
import { ApiError, type Api, type OpenCodeSettingsInput, type OpenCodeSettings as Settings } from './api';
import { describeError, type ErrorText } from './format';
import { ko } from './locales/ko';
import { ErrorNote } from './RunPanel';

const t = ko.accounts.admin.opencode;
const fields = ['executable', 'config_file', 'credential_file', 'model'] as const;

function formValues(settings: Settings): OpenCodeSettingsInput {
  const { executable, config_file, credential_file, model } = settings;
  return { executable, config_file, credential_file, model };
}

export function OpenCodeSettings({ api, onForbidden }: { api: Api; onForbidden: () => void }) {
  const id = useId();
  const [settings, setSettings] = useState<Settings | null>(null);
  const [draft, setDraft] = useState<OpenCodeSettingsInput | null>(null);
  const [busy, setBusy] = useState<'save' | 'probe' | null>(null);
  const [error, setError] = useState<ErrorText | null>(null);
  const [saved, setSaved] = useState(false);
  const [loads, setLoads] = useState(0);

  useEffect(() => {
    const abort = new AbortController();
    setError(null);
    api.getOpenCodeSettings(abort.signal).then(result => {
      if (abort.signal.aborted) return;
      setSettings(result);
      setDraft(formValues(result));
    }, cause => {
      if (abort.signal.aborted) return;
      const problem = describeError(cause);
      setError({ ...problem, message: `${t.loadFailed} ${problem.message}`, detail: null });
      if (cause instanceof ApiError && cause.status === 403) onForbidden();
    });
    return () => abort.abort();
  }, [api, loads, onForbidden]);

  if (!settings || !draft) {
    return error ? <><ErrorNote error={error} /><button type="button" onClick={() => setLoads(n => n + 1)}>{ko.accounts.admin.reload}</button></>
      : <p role="status">{ko.accounts.admin.loading}</p>;
  }

  const values = { executable: draft.executable.trim(), config_file: draft.config_file.trim(),
    credential_file: draft.credential_file.trim(), model: draft.model.trim() };
  const dirty = fields.some(field => values[field] !== settings[field]);
  const editable = settings.editable;
  const probeStatus = settings.probe === 'verified' ? t.verified : settings.probe === 'failed' ? t.failed : t.notProbed;
  const reason = settings.reason && Object.hasOwn(t.reasons, settings.reason) ? t.reasons[settings.reason] : null;
  async function change(operation: 'save' | 'probe') {
    if (busy || !editable || (operation === 'save' ? !dirty : dirty)) return;
    const invalidPath = operation === 'save' && fields.find(field => {
      const value = values[field];
      return field !== 'model' && value !== '' && !value.startsWith('/') && !value.startsWith('~/');
    });
    if (invalidPath) {
      setError({ message: t.badPath(t.fields[invalidPath].label), detail: null, requestId: null });
      return;
    }
    setBusy(operation);
    setError(null);
    setSaved(false);
    try {
      const result = operation === 'save' ? await api.putOpenCodeSettings(values) : await api.probeOpenCodeSettings();
      setSettings(result);
      setDraft(formValues(result));
      setSaved(operation === 'save');
    } catch (cause) {
      const problem = describeError(cause);
      const message = operation === 'save' ? t.saveFailed : t.probeFailed;
      setError({ ...problem, message: `${message} ${problem.message}`, detail: null });
      if (cause instanceof ApiError && cause.status === 403) onForbidden();
    } finally {
      setBusy(null);
    }
  }

  function submit(event: FormEvent) {
    event.preventDefault();
    void change('save');
  }

  return <>
    <p>{t.intro}</p>
    <div aria-live="polite">
      <p><strong>{settings.configured ? t.configured : t.notConfigured}</strong>{` · ${probeStatus}`}</p>
      {settings.effective_model && <p className="small">{t.effectiveModel} <code>{settings.effective_model}</code></p>}
      {settings.probe === 'failed' ? <p className="small">{reason ?? t.failureHint}</p>
        : !settings.configured && editable && <p className="small">{reason ?? t.configureHint}</p>}
    </div>
    {!editable && <p className="notice">{t.readOnly}</p>}
    <form className="opencode-form" onSubmit={submit} aria-describedby={`${id}-defaults`}>
      <p id={`${id}-defaults`} className="small">{t.defaults} {t.defaultsEmpty}</p>
      {fields.map(field => <div className="field" key={field}>
        <label htmlFor={`${id}-${field}`}>{t.fields[field].label}</label>
        <input id={`${id}-${field}`} type="text" value={draft[field]} disabled={!editable || busy !== null}
          autoComplete="off" spellCheck={false} aria-describedby={`${id}-${field}-help`}
          onChange={event => { setDraft({ ...draft, [field]: event.target.value }); setError(null); setSaved(false); }} />
        <small className="small" id={`${id}-${field}-help`}>{t.fields[field].help}</small>
      </div>)}
      <div className="actions">
        <button type="submit" className="primary" disabled={!editable || !dirty || busy !== null}>{busy === 'save' ? t.saving : t.save}</button>
        <button type="button" disabled={!editable || dirty || busy !== null} aria-describedby={`${id}-probe-help`}
          onClick={() => void change('probe')}>{busy === 'probe' ? t.probing : t.probe}</button>
      </div>
      <p id={`${id}-probe-help`} className="small">{dirty ? t.unsaved : t.probeHelp}</p>
      {saved && <p role="status">{t.saved}</p>}
      {error && <ErrorNote error={error} />}
    </form>
  </>;
}
