import { useId, useState } from 'react';
import type { Project, StageSummary, StageView } from './api';
import { reportURL } from './analysisReport';
import { formatClock, formatInstant, isTerminalRun, orderStages, runStatusText, shorten, stageName, stageStateText } from './format';
import { ko as t } from './locales/ko';

const s = t.stages;

export function countsText(counts: Record<string, number>): string {
  return Object.entries(counts)
    .filter(([, n]) => n > 0)
    .map(([k, n]) => s.count(Object.hasOwn(s.countLabels, k) ? s.countLabels[k]! : shorten(k, 40), n))
    .join(' · ');
}
export const reasonText = (code: string): string =>
  s.reasonLabel(Object.hasOwn(s.reasonLabels, code) ? s.reasonLabels[code]! : shorten(code, 80));
export const progressText = (st: StageView): string =>
  st.planned === null ? s.finishedOnly(st.finished) : s.finishedOfPlanned(st.finished, st.planned);

// no_tasks stages carry no information for a reader and are hidden.
const visible = (stages: StageView[]) => orderStages(stages.filter(x => x.state !== 'no_tasks'));

// Run detail: ordered stages, each expandable by a real button (keyboard accessible).
export function StageList({ summary, runStatus, confirmedAt }: { summary: StageSummary | undefined; runStatus: string; confirmedAt?: Date | null }) {
  const body = (() => {
    if (!summary || summary.detail === 'not_recorded') return <p>{s.unrecordedWithStatus(runStatusText(runStatus))}</p>;
    if (summary.detail !== 'recorded') return <p>{s.unknownDetail(shorten(summary.detail, 40))}</p>;
    const stages = visible(summary.stages);
    if (stages.length === 0) return <p>{s.noStages}</p>;
    return <ol className="plain stages">{stages.map(st => <StageItem key={st.key} stage={st} />)}</ol>;
  })();
  const times = [
    summary?.last_event_at ? s.lastEvent(formatInstant(summary.last_event_at)) : null,
    summary?.checked_at ? s.checkedAt(formatInstant(summary.checked_at)) : null,
    confirmedAt ? s.lastConfirmed(formatClock(confirmedAt)) : null,
  ].filter(x => x !== null);
  return <div className="stage-summary">
    <h4>{s.title}</h4>
    {body}
    {times.length > 0 && <p className="small">{times.join(' · ')}</p>}
  </div>;
}

function StageItem({ stage }: { stage: StageView }) {
  const [open, setOpen] = useState(false);
  const panel = useId();
  const name = stageName(stage.key);
  const counts = countsText(stage.counts);
  return <li className="stage">
    <button type="button" className="stage-toggle" aria-expanded={open} aria-controls={panel} onClick={() => setOpen(v => !v)}>
      <strong>{name}</strong> <span>{stageStateText(stage.state)}</span>
    </button>
    {counts !== '' && <span className="small"> {counts}</span>}
    {stage.reasons.map(r => <span key={r} className="small"> {reasonText(r)}</span>)}
    <div id={panel} hidden={!open}>
      <ul className="plain small">
        <li>{progressText(stage)}</li>
        <li>{s.succeededCount(stage.succeeded)}</li>
        <li>{stage.started_at ? s.startedAt(formatInstant(stage.started_at)) : s.notStarted}</li>
        {stage.finished_at && <li>{s.finishedAt(formatInstant(stage.finished_at))}</li>}
      </ul>
    </div>
  </li>;
}

// Project list: representative run status, all concurrently active stages, other active runs.
export function ProjectActivity({ project }: { project: Project }) {
  const a = project.activity;
  if (!a) {
    // Server without W15 fields: only the last run's status is known.
    if (!project.last_run) return <p className="small activity">{s.noRun}</p>;
    return <p className="small activity">{runStatusText(project.last_run.status)} · {s.unrecorded}</p>;
  }
  if (!a.run) return <p className="small activity">{s.noRun}</p>;
  const status = a.run.status;
  const parts: string[] = [];
  if (a.detail === null || a.detail === 'not_recorded') parts.push(s.unrecorded);
  else if (a.detail !== 'recorded') parts.push(s.unknownDetail(shorten(a.detail, 40)));
  else {
    const stages = visible(a.stages);
    const running = stages.filter(x => x.state === 'running');
    const attention = stages.filter(x => x.state !== 'running' && (['failed', 'partial', 'cancelled'].includes(x.state) || !Object.hasOwn(s.states, x.state)));
    if (running.length > 0) running.forEach(x => parts.push(s.activeNow(stageName(x.key))));
    if (attention.length > 0) attention.forEach(x => parts.push(s.attention(stageName(x.key), stageStateText(x.state))));
    else if (running.length === 0 && !isTerminalRun(status)) {
      const waiting = stages.filter(x => x.state === 'waiting');
      if (stages.some(x => x.state === 'plan_pending')) parts.push(s.planPending);
      else if (status !== 'queued' && waiting.length > 0) waiting.forEach(x => parts.push(s.waitingFor(stageName(x.key))));
      else if (status === 'queued' && a.waiting_reason) {
        const r = a.waiting_reason;
        parts.push(Object.hasOwn(t.runs.waitingLabels, r) ? t.runs.waitingLabels[r]! : shorten(r, 80));
      } else parts.push(s.noStarted);
    }
  }
  return <div className="small activity">
    <p>{[runStatusText(status), ...parts].join(' · ')}</p>
    {a.other_active_runs > 0 && <p>{s.otherActive(a.other_active_runs)}</p>}
  </div>;
}

// Pinned to the report's own run and revision; never to "the latest".
export function LastReportLink({ project }: { project: Project }) {
  const r = project.activity?.last_report;
  if (!r) return null;
  return <a className="small" href={reportURL(r.run_id, 'html', false, r.revision)}>{s.lastReport(shorten(r.run_id, 16), r.revision)}</a>;
}
