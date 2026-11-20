import { useId, useState, type FormEvent } from 'react';
import { SETUP_AVAILABLE_KEYS, type AnalysisChoices, type Api, type Project, type Provider, type Setup } from './api';
import { describeError, isTerminalRun, type ErrorText } from './format';
import type { Idempotency } from './idempotency';
import { ko as t } from './locales/ko';
import { CHOICE_AVAILABILITY, choiceLocked, choicesFor, parseTarget, quickBody, unavailableReason } from './quickStart';
import { ErrorNote } from './RunPanel';

type Props = { api: Api; userId: string; idempotency: Idempotency; setup: Setup | null; providers: Provider[]; onStarted: (project: Project, runId: string) => void };

// The first screen for every user: one field (a Git address, or a folder on the server the user
// may read) and the analyze button. The project is registered (or the user's existing one of the
// same folder or repository opened), a run is requested unless one is still going, and the
// caller opens that run.
export function QuickStart({ api, userId, idempotency, setup, providers, onStarted }: Props) {
  const [input, setInput] = useState('');
  const [name, setName] = useState('');
  const [branch, setBranch] = useState<string | null>(null); // null: follow the address
  const [touched, setTouched] = useState<Partial<AnalysisChoices>>({});
  const [comment, setComment] = useState<boolean | null>(null); // null: the project's remembered choice
  const [error, setError] = useState<string | null>(null);
  const [serverError, setServerError] = useState<ErrorText | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const uid = useId();
  const q = t.quick;
  const parsed = parseTarget(input).target;
  const kind = parsed?.kind ?? 'local';
  const choices = choicesFor(kind, setup, touched);
  const shownBranch = branch ?? (parsed?.kind === 'git' ? parsed.branch ?? '' : '');
  const pullRequest = parsed?.kind === 'git' ? parsed.pullRequest ?? null : null;
  // What this service does not offer the user, said plainly under the field.
  const limits = SETUP_AVAILABLE_KEYS.flatMap(key => { const r = unavailableReason(setup, key); return r ? [[key, r] as const] : []; });

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
      if (target.kind === 'git' && target.pullRequest) {
        // A pull request always gets a new run on its current head; an explicit comment
        // choice is remembered for the project.
        if (comment !== null && project.can_manage && project.analysis && (project.analysis.comment_on_pull_request ?? false) !== comment) {
          await api.setProjectAnalysis(project.id, { ...project.analysis, comment_on_pull_request: comment });
        }
        const ack = await api.createPullRequestRun(project.id, idempotency.keyFor(userId, project.id), target.pullRequest, comment);
        idempotency.complete(userId, project.id);
        runId = ack.id;
      } else if (active && !isTerminalRun(active.status)) {
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
      setComment(null);
      onStarted(project, runId);
    } catch (e) {
      setServerError(describeError(e));
    } finally {
      setSubmitting(false);
    }
  }

  const box = (key: keyof AnalysisChoices, label: string, help: string) => {
    const locked = choiceLocked(setup, key);
    const reason = unavailableReason(setup, CHOICE_AVAILABILITY[key]);
    return <div className="field">
      <label className="inline"><input type="checkbox" name={key} checked={choices[key]} disabled={locked} aria-describedby={`${uid}-${key}-help${reason ? ` ${uid}-${key}-reason` : ''}`}
        onChange={e => { const checked = e.target.checked; setTouched(x => ({ ...x, [key]: checked })); }} /> {label}</label>
      <small id={`${uid}-${key}-help`} className="small">{help}</small>
      {reason && <small id={`${uid}-${key}-reason`} className="small unavailable" data-unavailable={key}>{q.unavailable(reason)}</small>}
    </div>;
  };

  return <form className="quick-form" onSubmit={submit} noValidate aria-labelledby={`${uid}-title`}>
    <h3 id={`${uid}-title`}>{q.title}</h3>
    <div className="field">
      <label htmlFor="start-target">{q.field}</label>
      <input id="start-target" type="text" autoComplete="off" spellCheck={false} placeholder={q.placeholder} value={input}
        aria-invalid={error !== null} aria-describedby={`${uid}-help${error ? ` ${uid}-error` : ''}`}
        onChange={e => { setInput(e.target.value); setError(null); }} />
      <small id={`${uid}-help`} className="small">{q.help}</small>
      {error && <small id={`${uid}-error`} role="alert" className="error">{error}</small>}

    </div>
    {limits.length > 0 && <div id="start-unavailable" className="notice small">
      <p>{q.limitsTitle}</p>
      <ul className="plain">{limits.map(([key, reason]) => <li key={key} data-unavailable={key}>{q.limitLabels[key] ?? key}: {reason}</li>)}</ul>
    </div>}
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
      {box('fetch_dependencies', q.fetchDependencies, q.fetchDependenciesHelp)}
      {pullRequest !== null && <div className="field">
        <label className="inline"><input type="checkbox" checked={comment ?? false} aria-describedby={`${uid}-comment-help`} onChange={e => setComment(e.target.checked)} /> {t.pullRequest.commentBox}</label>
        <small id={`${uid}-comment-help`} className="small">{t.pullRequest.commentHelp}</small>
      </div>}
    </details>
    <button id="start-submit" type="submit" className="primary" disabled={submitting} aria-busy={submitting}>{submitting ? q.submitting : q.submit}</button>
    {serverError && <ErrorNote error={serverError} />}
  </form>;
}
