import { useId, useState, type FormEvent } from 'react';
import type { AnalysisChoices, Api, Project, Provider, Setup } from './api';
import { describeError, isTerminalRun, type ErrorText } from './format';
import type { Idempotency } from './idempotency';
import { ko as t } from './locales/ko';
import { choicesFor, parseTarget, quickBody } from './quickStart';
import { ErrorNote } from './RunPanel';

type Props = { api: Api; userId: string; idempotency: Idempotency; setup: Setup | null; providers: Provider[]; onStarted: (project: Project, runId: string) => void };

// Personal mode: one field (a folder path or a Git address) and the analyze button. The
// project is registered (or the existing one of the same folder or repository reused), a run
// is requested unless one is still going, and the caller opens that run.
export function QuickStart({ api, userId, idempotency, setup, providers, onStarted }: Props) {
  const [input, setInput] = useState('');
  const [name, setName] = useState('');
  const [branch, setBranch] = useState<string | null>(null); // null: follow the address
  const [touched, setTouched] = useState<Partial<AnalysisChoices>>({});
  const [error, setError] = useState<string | null>(null);
  const [serverError, setServerError] = useState<ErrorText | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const uid = useId();
  const q = t.quick;
  const parsed = parseTarget(input).target;
  const kind = parsed?.kind ?? 'local';
  const choices = choicesFor(kind, setup, touched);
  const shownBranch = branch ?? (parsed?.kind === 'git' ? parsed.branch ?? '' : '');

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (submitting) return;
    setServerError(null);
    const { target, error: invalid } = parseTarget(input);
    if (!target) { setError(invalid); return; }
    const { body, error: refused } = quickBody(target, name, shownBranch, choices, providers, setup);
    if (!body) { setError(refused); return; }
    setError(null);
    setSubmitting(true);
    try {
      const project = await api.createProject(body);
      const active = project.activity?.run;
      let runId: string;
      if (active && !isTerminalRun(active.status)) {
        runId = active.id;
      } else {
        const ack = await api.createRun(project.id, idempotency.keyFor(userId, project.id));
        idempotency.complete(userId, project.id);
        runId = ack.id;
      }
      setInput('');
      setName('');
      setBranch(null);
      setTouched({});
      onStarted(project, runId);
    } catch (e) {
      setServerError(describeError(e));
    } finally {
      setSubmitting(false);
    }
  }

  const box = (key: keyof AnalysisChoices, label: string, help: string) => <div className="field">
    <label className="inline"><input type="checkbox" checked={choices[key]} aria-describedby={`${uid}-${key}-help`} onChange={e => { const checked = e.target.checked; setTouched(x => ({ ...x, [key]: checked })); }} /> {label}</label>
    <small id={`${uid}-${key}-help`} className="small">{help}</small>
  </div>;

  return <form onSubmit={submit} noValidate aria-labelledby={`${uid}-title`}>
    <h3 id={`${uid}-title`}>{q.title}</h3>
    <div className="field">
      <label htmlFor={`${uid}-target`}>{q.field}</label>
      <input id={`${uid}-target`} type="text" autoComplete="off" spellCheck={false} placeholder={q.placeholder} value={input}
        aria-invalid={error !== null} aria-describedby={`${uid}-help${error ? ` ${uid}-error` : ''}`}
        onChange={e => { setInput(e.target.value); setError(null); }} />
      <small id={`${uid}-help`} className="small">{q.help}</small>
      {error && <small id={`${uid}-error`} role="alert" className="error">{error}</small>}
    </div>
    <details>
      <summary>{q.advanced}</summary>
      <div className="field">
        <label htmlFor={`${uid}-name`}>{q.name}</label>
        <input id={`${uid}-name`} type="text" maxLength={200} autoComplete="off" value={name} onChange={e => setName(e.target.value)} />
      </div>
      {kind === 'git' && <div className="field">
        <label htmlFor={`${uid}-branch`}>{q.branch}</label>
        <input id={`${uid}-branch`} type="text" autoComplete="off" spellCheck={false} value={shownBranch} onChange={e => setBranch(e.target.value)} />
      </div>}
      {box('send_source_to_model', q.sendSource, q.sendSourceHelp)}
      {box('run_build_scripts', q.runBuildScripts, q.runBuildScriptsHelp)}
    </details>
    <button type="submit" className="primary" disabled={submitting} aria-busy={submitting}>{submitting ? q.submitting : q.submit}</button>
    {serverError && <ErrorNote error={serverError} />}
  </form>;
}
