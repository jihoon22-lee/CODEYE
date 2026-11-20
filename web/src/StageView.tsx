import { Fragment, useId, useState, type ReactNode } from 'react';
import type { Project, StageSummary, StageView } from './api';
import { reportURL } from './analysisReport';
import { Time } from './CopyText';
import { formatClock, formatInstant, isTerminalRun, orderStages, runStatusText, shorten, stageName, stageStateText } from './format';
import { ko as t } from './locales/ko';
import { reportKo } from './locales/report-ko';

const s = t.stages;

export function countsText(counts: Record<string, number>): string {
  return Object.entries(counts)
    .filter(([, n]) => n > 0)
    .map(([k, n]) => s.count(Object.hasOwn(s.countLabels, k) ? s.countLabels[k]! : shorten(k, 40), n))
    .join(' · ');
}
// A reason code in words: the stage wording, else the report's, else the code itself.
export const reasonText = (code: string): string =>
  s.reasonLabel(Object.hasOwn(s.reasonLabels, code) ? s.reasonLabels[code]!
    : Object.hasOwn(reportKo.reasons, code) ? reportKo.reasons[code]!
    : shorten(code, 80));
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
  // Relative times read faster; the exact time stays in the title and the datetime attribute.
  const times: ReactNode[] = [
    summary?.last_event_at ? <>{s.lastEvent} <Time iso={summary.last_event_at} /></> : null,
    summary?.checked_at ? <>{s.checkedAt} <Time iso={summary.checked_at} /></> : null,
    confirmedAt ? s.lastConfirmed(formatClock(confirmedAt)) : null,
  ].filter(x => x !== null);
  return <div className="stage-summary">
    <h4>{s.title}</h4>
    {body}
    {times.length > 0 && <p className="small">{times.map((x, i) => <Fragment key={i}>{i > 0 && ' · '}{x}</Fragment>)}</p>}
  </div>;
}

function StageItem({ stage }: { stage: StageView }) {
  const [open, setOpen] = useState(false);
  const panel = useId();
  const name = stageName(stage.key);
  const counts = countsText(stage.counts);
  const known = Object.hasOwn(s.states, stage.state);
  return <li className={`stage st-${known ? stage.state : 'unknown'}`}>
    <button type="button" className="stage-toggle" aria-expanded={open} aria-controls={panel} onClick={() => setOpen(v => !v)}>
      <strong>{name}</strong> <span>{stageStateText(stage.state)}</span>
    </button>
    {(counts !== '' || stage.reasons.length > 0) && <ul className="stage-notes">
      {counts !== '' && <li>{counts}</li>}
      {stage.reasons.map(r => <li key={r}>{reasonText(r)}</li>)}
    </ul>}
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
    // Server without the stage fields: only the last run's status is known.
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
      // A queued run has no plan yet, so its task stages are always plan_pending:
      // the waiting reason is what the reader needs, and nothing has started.
      if (status === 'queued') {
        const r = a.waiting_reason;
        parts.push(r ? (Object.hasOwn(t.runs.waitingLabels, r) ? t.runs.waitingLabels[r]! : shorten(r, 80)) : s.noStarted);
      } else if (stages.some(x => x.state === 'plan_pending')) parts.push(s.planPending);
      else if (waiting.length > 0) waiting.forEach(x => parts.push(s.waitingFor(stageName(x.key))));
      else parts.push(s.noStarted);
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
  return <a className="button-link small-button" href={reportURL(r.run_id, 'html', false, r.revision)} title={s.lastReportTitle(r.run_id, r.revision)}>{s.lastReport(shorten(r.run_id, 16), r.revision)}</a>;
}
