import { ReportPanel } from './ReportPanel';
import { useState } from 'react';
import type { Api, Project, RunAck, RunView } from './api';
import { canCancelRun, describeError, isKnownRunStatus, isTerminalRun, runStatusLabel, shorten, type ErrorText } from './format';
import type { Idempotency } from './idempotency';
import { ko as t } from './locales/ko';
import { useRunPolling } from './useRunPolling';

export function ErrorNote({ error }: { error: ErrorText }) {
  return <p role="alert" className="error">{error.message}{error.detail ? <> {error.detail}</> : null}{error.requestId ? <small> {t.errors.requestId(error.requestId)}</small> : null}</p>;
}

type Props = { api: Api; userId: string; project: Project; idempotency: Idempotency; openRunId?: string | null };

export function RunPanel({ api, userId, project, idempotency, openRunId = null }: Props) {
  const [runId, setRunId] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<ErrorText | null>(null);
  const [accepted, setAccepted] = useState<RunAck | null>(null);
  const activeId = runId ?? openRunId;
  const retrying = error !== null && idempotency.pending(userId, project.id);

  async function start() {
    if (pending) return;
    setPending(true);
    setError(null);
    // The key is created once per (user, project, body) and reused until the server accepts it,
    // so retrying after a lost response returns the same run instead of creating another.
    const key = idempotency.keyFor(userId, project.id);
    try {
      const ack = await api.createRun(project.id, key);
      idempotency.complete(userId, project.id);
      setAccepted(ack);
      setRunId(ack.id);
    } catch (e) {
      setError(describeError(e));
    } finally {
      setPending(false);
    }
  }

  return <section className="panel" aria-labelledby="run-title">
    <h2 id="run-title">{t.runs.title}</h2>
    <ul className="plain small"><li>{t.runs.profile}</li><li>{t.runs.policy}</li><li>{t.runs.scope}</li></ul>
    <p className="small">{t.runs.startNote}</p>
    <button type="button" className="primary" onClick={start} disabled={pending} aria-busy={pending}>{pending ? t.runs.starting : t.runs.start}</button>
    {error && <><ErrorNote error={error} />{retrying && <p className="small">{t.runs.retryNote}</p>}</>}
    {accepted && <p role="status">{t.runs.accepted(shorten(accepted.id, 12))}</p>}
    {activeId && <RunStatus key={activeId} api={api} runId={activeId} placeholder={accepted?.id === activeId ? accepted : null} />}
  </section>;
}

function RunStatus({ api, runId, placeholder }: { api: Api; runId: string; placeholder: RunAck | null }) {
  const { run, error, stopped, hidden, applyAck, refresh } = useRunPolling(api, runId);
  const status = run?.status ?? placeholder?.status ?? null;
  const version = run?.version ?? placeholder?.version ?? null;
  const terminal = status !== null && isTerminalRun(status);
  return <div className="run" aria-labelledby="run-current">
    <h3 id="run-current">{t.runs.current}</h3>
    <div role="status" aria-live="polite" aria-atomic="true">
      {status === null
        ? <p>{t.projects.loading}</p>
        : <p className={`badge ${isKnownRunStatus(status) ? `s-${status}` : 's-unknown'}`}><strong>{runStatusLabel(status)}</strong></p>}
    </div>
    {status !== null && !isKnownRunStatus(status) && <p className="notice">{t.runs.statusUnknownNote} <code>{shorten(status, 40)}</code></p>}
    {run && <RunDetails run={run} />}
    {terminal && run?.reportRevision && <ReportPanel key={runId} api={api} runID={runId}/> }
    {!run && <dl><dt>{t.runs.id}</dt><dd><code>{runId}</code></dd>{version !== null && <><dt>{t.runs.version}</dt><dd>{version}</dd></>}</dl>}
    {error !== null && <p role="alert" className="error">{stopped ? t.runs.pollStopped : t.runs.pollError} {describeError(error).message}</p>}
    <p className="small">
      {terminal ? t.runs.terminalNote : hidden ? t.runs.pollingHidden : stopped ? t.runs.pollStopped : t.runs.polling}
    </p>
    <div className="actions">
      {(!terminal || (run?.snapshot_id && !run.reportRevision)) && <button type="button" onClick={refresh}>{t.runs.refresh}</button>}
      {status !== null && !terminal && <CancelControl api={api} runId={runId} status={status} onAck={applyAck} />}
    </div>
  </div>;
}

function RunDetails({ run }: { run: RunView }) {
  const p = run.progress;
  const reason = p.waiting_reason;
  return <dl>
    <dt>{t.runs.id}</dt><dd><code>{run.id}</code></dd>
    <dt>{t.runs.version}</dt><dd>{run.version}</dd>
    <dt>{t.runs.snapshot}</dt><dd>{run.snapshot_id ? <code>{run.snapshot_id}</code> : t.runs.snapshotNone}</dd>
    <dt>{t.runs.stage}</dt><dd>{p.stage === '' ? '—' : shorten(p.stage)}</dd>
    <dt>{t.runs.tasks}</dt><dd>{t.runs.tasksValue(p.completed_tasks, p.total_tasks)}</dd>
    <dt>{t.runs.percent}</dt>
    <dd>{p.estimated_percent === null
      ? t.runs.percentUnknown
      : <><progress max={100} value={p.estimated_percent} aria-label={t.runs.percent} /> {p.estimated_percent}%</>}</dd>
    {reason !== null && <><dt>{t.runs.waiting}</dt><dd>{Object.hasOwn(t.runs.waitingLabels, reason) ? t.runs.waitingLabels[reason] : shorten(reason, 120)}</dd></>}
    {run.quality_verdict !== null && <><dt>{t.runs.verdict}</dt><dd>{Object.hasOwn(t.runs.verdictLabels, run.quality_verdict) ? t.runs.verdictLabels[run.quality_verdict] : t.runs.statusUnknown}</dd></>}
  </dl>;
}

function CancelControl({ api, runId, status, onAck }: { api: Api; runId: string; status: string; onAck: (ack: RunAck) => void }) {
  const [step, setStep] = useState<'idle' | 'confirm' | 'sending' | 'done'>('idle');
  const [error, setError] = useState<ErrorText | null>(null);
  if (!canCancelRun(status)) {
    return <p className="small">{status === 'cancel_requested' || status === 'cancelling' ? t.runs.cancelDone : t.runs.cancelUnavailable}</p>;
  }
  async function send() {
    setStep('sending');
    setError(null);
    try {
      onAck(await api.cancelRun(runId));
      setStep('done');
    } catch (e) {
      setError(describeError(e));
      setStep('confirm');
    }
  }
  if (step === 'done') return <p role="status">{t.runs.cancelDone}</p>;
  if (step === 'idle') return <button type="button" onClick={() => setStep('confirm')}>{t.runs.cancel}</button>;
  return <div role="group" aria-label={t.runs.cancel}>
    <p>{t.runs.cancelConfirm}</p>
    <button type="button" className="danger" onClick={send} disabled={step === 'sending'}>{step === 'sending' ? t.runs.cancelSending : t.runs.cancelYes}</button>
    <button type="button" onClick={() => setStep('idle')} disabled={step === 'sending'}>{t.runs.cancelNo}</button>
    {error && <ErrorNote error={error} />}
  </div>;
}

export type { Project };
