import { ReportPanel } from './ReportPanel';
import { useEffect, useState } from 'react';
import { ApiError, type AnalysisChoices, type Api, type Project, type RetryAck, type RunAck, type RunProgress, type RunTrigger, type RunView, type Setup } from './api';
import { improvementHints, type Hint } from './improve';
import type { AnalysisReport } from './analysisReport';
import { canCancelRun, describeError, roleLabel, formatClock, formatInstant, isKnownRunStatus, isTerminalRun, runStatusLabel, shorten, stageName, type ErrorText } from './format';
import type { Idempotency } from './idempotency';
import { ko as t } from './locales/ko';
import type { StreamMode } from './runStream';
import { StageList } from './StageView';
import { useRunPolling } from './useRunPolling';

export function ErrorNote({ error }: { error: ErrorText }) {
  return <p role="alert" className="error">{error.message}{error.detail ? <> {error.detail}</> : null}{error.requestId ? <small> {t.errors.requestId(error.requestId)}</small> : null}</p>;
}

// onRunChange lets the project list re-read its aggregate after this run was accepted or changed state.
type Props = { api: Api; userId: string; project: Project; idempotency: Idempotency; openRunId?: string | null; onRunChange?: () => void; setup?: Setup | null };

export function RunPanel({ api, userId, project, idempotency, openRunId = null, onRunChange, setup = null }: Props) {
  const [runId, setRunId] = useState<string | null>(null);
  // A retry of the same run restarts its status view; a new run replaces it.
  const [generation, setGeneration] = useState(0);
  const [retried, setRetried] = useState<RetryAck | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<ErrorText | null>(null);
  const [accepted, setAccepted] = useState<RunAck | null>(null);
  const activeId = runId ?? openRunId;
  const retrying = error !== null && idempotency.pending(userId, project.id);
  // The server decides; this only keeps a viewer from offering an action it will refuse.
  const canRun = project.can_run;

  async function start() {
    if (pending || !canRun) return;
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
      onRunChange?.();
    } catch (e) {
      setError(describeError(e));
      // A 403 means the caller's role changed: re-read the project so the controls follow it.
      if (e instanceof ApiError && e.status === 403) onRunChange?.();
    } finally {
      setPending(false);
    }
  }

  return <section className="panel" aria-labelledby="run-title">
    <h2 id="run-title">{t.runs.title}</h2>
    <ul className="plain small"><li>{t.runs.profile}</li><li>{t.runs.policy}</li><li>{t.runs.scope}</li></ul>
    <p className="small">{t.runs.startNote}</p>
    <button type="button" className="primary" onClick={start} disabled={pending || !canRun} aria-busy={pending} aria-describedby={canRun ? undefined : 'run-not-allowed'}>{pending ? t.runs.starting : t.runs.start}</button>
    {!canRun && <p id="run-not-allowed" className="small">{t.accounts.runNotAllowed(roleLabel(project.role))}</p>}
    {error && <><ErrorNote error={error} />{retrying && <p className="small">{t.runs.retryNote}</p>}</>}
    {accepted && <p role="status">{t.runs.accepted(shorten(accepted.id, 12))}</p>}
    {retried && <p role="status">{t.runs.retried(retried.retry)}</p>}
    {activeId && <RunStatus key={`${activeId}:${generation}`} api={api} runId={activeId} project={project} setup={setup} canRun={canRun}
      placeholder={accepted?.id === activeId ? accepted : retried?.id === activeId ? retried : null} onRunChange={onRunChange}
      onRetried={ack => { setRetried(ack); setAccepted(null); setRunId(ack.id); setGeneration(n => n + 1); onRunChange?.(); }} />}
  </section>;
}

type StatusProps = { api: Api; runId: string; project: Project; setup: Setup | null; canRun: boolean; placeholder: RunAck | null; onRunChange?: () => void; onRetried: (ack: RetryAck) => void };

// Finished runs that a retry can improve: a failure, a partial result, a cancellation.
const RETRYABLE = new Set(['failed', 'partial', 'cancelled']);

function RunStatus({ api, runId, project, setup, canRun, placeholder, onRunChange, onRetried }: StatusProps) {
  const { run, error, stopped, hidden, confirmedAt, mode, applyAck, refresh } = useRunPolling(api, runId);
  const runVersion = run?.version;
  useEffect(() => { if (runVersion !== undefined) onRunChange?.(); }, [runVersion]); // eslint-disable-line react-hooks/exhaustive-deps
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
    {run && <RunDetails run={run} confirmedAt={error !== null ? confirmedAt : null} />}
    {terminal && run?.reportRevision && <ReportPanel key={runId} api={api} runID={runId}
      extra={report => <ImproveHints api={api} project={project} hints={improvementHints(report.sections?.gaps ?? [], setup, project, report.sections?.codeReview.ledger ?? null)} runId={runId} canRun={canRun} onRetried={onRetried} />} />}
    {!run && <dl><dt>{t.runs.id}</dt><dd><code>{runId}</code></dd>{version !== null && <><dt>{t.runs.version}</dt><dd>{version}</dd></>}</dl>}
    {error !== null && <p role="alert" className="error">{stopped ? t.runs.pollStopped : t.runs.pollError} {describeError(error).message}</p>}
    <p className="small">
      {terminal ? t.runs.terminalNote : hidden && mode !== 'paused' ? t.runs.pollingHidden : stopped ? t.runs.pollStopped : updateText(mode)}
      {!terminal && !stopped && mode === 'live' && confirmedAt && <> {t.stages.lastConfirmed(formatClock(confirmedAt))}</>}
    </p>
    <div className="actions">
      {(!terminal || (run?.snapshot_id && !run.reportRevision)) && <button type="button" onClick={refresh}>{t.runs.refresh}</button>}
      {terminal && status !== null && RETRYABLE.has(status) && canRun && <RetryControl api={api} runId={runId} onRetried={onRetried} />}
      {status !== null && !terminal && (canRun
        ? <CancelControl api={api} runId={runId} status={status} onAck={applyAck} onForbidden={onRunChange} />
        : <p className="small">{t.accounts.cancelNotAllowed}</p>)}
    </div>
  </div>;
}

// How the panel is kept up to date. polling keeps the wording used without a stream.
function updateText(mode: StreamMode): string {
  switch (mode) {
    case 'connecting': return t.runs.streamConnecting;
    case 'live': return t.runs.streamLive;
    case 'fallback': return t.runs.streamFallback;
    case 'paused': return t.runs.streamPaused;
    default: return t.runs.polling;
  }
}

function triggerText(trigger: RunTrigger): string {
  if (trigger.kind === 'manual') return t.runs.triggerManual;
  if (trigger.kind === 'schedule' && trigger.scheduled_for) return t.runs.triggerSchedule(formatInstant(trigger.scheduled_for));
  return t.runs.triggerUnknown(shorten(trigger.kind, 40));
}

// The weighted plan behind the percentage and why its denominator changed. Servers without a stream
// send none of it: nothing is shown rather than a guess.
function PlanDetails({ p }: { p: RunProgress }) {
  if (p.planned_weight === undefined && p.plan_changes === undefined) return null;
  const basis = p.weight_basis ?? null;
  const changes = p.plan_changes ?? [];
  return <>
    <dt>{t.runs.plan}</dt>
    <dd>
      {p.plan_revision === null || p.planned_weight == null
        ? t.runs.planPending
        : <>{t.runs.planValue(p.plan_revision, p.finished_weight ?? 0, p.planned_weight)}
          {basis !== null && <span className="small"> {Object.hasOwn(t.runs.weightBasis, basis) ? t.runs.weightBasis[basis] : t.runs.weightBasisUnknown(shorten(basis, 40))}</span>}</>}
      {changes.length > 0 && <details>
        <summary>{t.runs.planChanges}</summary>
        <ul className="plain small">{changes.map(c => <li key={c.revision}>
          {c.reason === 'task_added'
            ? t.runs.planChange(c.revision, c.stage === null ? t.runs.planChangeNoStage : stageName(c.stage), c.weight, c.planned_weight)
            : t.runs.planChangeUnknown(c.revision, shorten(c.reason, 40))}
          {c.recorded_at && <> ({formatInstant(c.recorded_at)})</>}
        </li>)}</ul>
      </details>}
    </dd>
  </>;
}

function RunDetails({ run, confirmedAt }: { run: RunView; confirmedAt: Date | null }) {
  const p = run.progress;
  const reason = p.waiting_reason;
  return <><dl>
    <dt>{t.runs.id}</dt><dd><code>{run.id}</code></dd>
    <dt>{t.runs.version}</dt><dd>{run.version}</dd>
    {run.trigger && <><dt>{t.runs.trigger}</dt><dd>{triggerText(run.trigger)}</dd></>}
    <dt>{t.runs.snapshot}</dt><dd>{run.snapshot_id ? <code>{run.snapshot_id}</code> : t.runs.snapshotNone}</dd>
    <dt>{t.runs.tasks}</dt><dd>{t.runs.tasksValue(p.completed_tasks, p.total_tasks)}</dd>
    <dt>{t.runs.percent}</dt>
    <dd>{p.estimated_percent === null
      ? t.runs.percentUnknown
      : <><progress max={100} value={p.estimated_percent} aria-label={t.runs.percent} /> {p.estimated_percent}%</>}
      {p.planned_weight !== undefined && <p className="small">{t.runs.percentNote}</p>}</dd>
    <PlanDetails p={p} />
    {reason !== null && <><dt>{t.runs.waiting}</dt><dd>{Object.hasOwn(t.runs.waitingLabels, reason) ? t.runs.waitingLabels[reason] : shorten(reason, 120)}</dd></>}
    {run.quality_verdict !== null && <><dt>{t.runs.verdict}</dt><dd>{Object.hasOwn(t.runs.verdictLabels, run.quality_verdict) ? t.runs.verdictLabels[run.quality_verdict] : t.runs.statusUnknown}</dd></>}
  </dl>
  <StageList summary={p.stages} runStatus={run.status} confirmedAt={confirmedAt} /></>;
}

function RetryControl({ api, runId, onRetried }: { api: Api; runId: string; onRetried: (ack: RetryAck) => void }) {
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<ErrorText | null>(null);
  async function send() {
    setSending(true);
    setError(null);
    try {
      onRetried(await api.retryRun(runId));
    } catch (e) {
      setError(describeError(e));
      setSending(false);
    }
  }
  return <>
    <button type="button" className="primary" onClick={send} disabled={sending} aria-busy={sending}>{sending ? t.runs.retrying : t.runs.retry}</button>
    {error && <ErrorNote error={error} />}
  </>;
}

// "더 나은 결과를 얻으려면": what would fill the report's gaps. A project choice is turned on
// and the run retried in one step.
function ImproveHints({ api, project, hints, runId, canRun, onRetried }: { api: Api; project: Project; hints: Hint[]; runId: string; canRun: boolean; onRetried: (ack: RetryAck) => void }) {
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<ErrorText | null>(null);
  if (hints.length === 0) return null;
  async function turnOn(key: keyof AnalysisChoices) {
    if (!project.analysis) return;
    setSending(true);
    setError(null);
    try {
      await api.setProjectAnalysis(project.id, { ...project.analysis, [key]: true });
      onRetried(await api.retryRun(runId));
    } catch (e) {
      setError(describeError(e));
      setSending(false);
    }
  }
  const i = t.improve;
  const action: Record<keyof AnalysisChoices, string> = { send_source_to_model: i.sendSourceAction, run_build_scripts: i.buildScriptsAction, fetch_dependencies: i.fetchDependenciesAction };
  return <section className="notice improve" aria-labelledby={`improve-${runId}`}>
    <h4 id={`improve-${runId}`}>{i.title}</h4>
    <ul>{hints.map(h => <li key={h.key}>{h.text}
      {h.turnOn && (project.can_manage && canRun
        ? <> <button type="button" onClick={() => void turnOn(h.turnOn!)} disabled={sending} aria-busy={sending}>{sending ? i.sending : action[h.turnOn]}</button></>
        : <small> {i.needsManager}</small>)}
    </li>)}</ul>
    {error && <ErrorNote error={error} />}
  </section>;
}

function CancelControl({ api, runId, status, onAck, onForbidden }: { api: Api; runId: string; status: string; onAck: (ack: RunAck) => void; onForbidden?: () => void }) {
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
      if (e instanceof ApiError && e.status === 403) onForbidden?.();
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
