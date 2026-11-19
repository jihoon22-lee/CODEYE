import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react';
import { RUN_BODY, type Api, type CreateScheduleBody, type Schedule, type ScheduleList } from './api';
import { describeError, formatInstant, shorten, type ErrorText } from './format';
import { newKey } from './idempotency';
import { ko as t } from './locales/ko';
import { ErrorNote } from './RunPanel';
import { retryable } from './useRunPolling';

// Project schedules: members see them, managers (can_manage) create and delete.
// Every value shown comes from the schedule API; the next time is the server's.

const label = (labels: Record<string, string>, raw: string) => (Object.hasOwn(labels, raw) ? labels[raw]! : t.schedules.unknown(shorten(raw, 40)));

function browserZone(): string {
  try { return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'; } catch { return 'UTC'; }
}

export function SchedulePanel({ api, projectId }: { api: Api; projectId: string }) {
  const [list, setList] = useState<ScheduleList | null>(null);
  const [error, setError] = useState<ErrorText | null>(null);
  const [loads, setLoads] = useState(0);
  const reload = useCallback(() => setLoads(n => n + 1), []);
  const s = t.schedules;

  useEffect(() => {
    const controller = new AbortController();
    api.listSchedules(projectId, controller.signal).then(
      value => { setList(value); setError(null); },
      e => { if (!controller.signal.aborted) setError(describeError(e)); },
    );
    return () => controller.abort();
  }, [api, projectId, loads]);

  return <section className="panel" aria-labelledby="schedules-title">
    <h2 id="schedules-title">{s.title}</h2>
    <p className="small">{s.intro}</p>
    {error && <><ErrorNote error={{ ...error, message: `${s.loadFailed} ${error.message}` }} /><button type="button" onClick={reload}>{s.reload}</button></>}
    {list === null && !error && <p>{s.loading}</p>}
    {list && (list.items.length === 0
      ? <p>{s.empty}</p>
      : <ul className="plain schedules">{list.items.map(item => <ScheduleItem key={item.id} api={api} projectId={projectId} item={item} canManage={list.can_manage} onChanged={reload} />)}</ul>)}
    {list && !list.can_manage && <p className="small">{s.viewerNote}</p>}
    {list?.can_manage && <ScheduleForm api={api} projectId={projectId} onCreated={reload} />}
  </section>;
}

function ScheduleItem({ api, projectId, item, canManage, onChanged }: { api: Api; projectId: string; item: Schedule; canManage: boolean; onChanged: () => void }) {
  const [step, setStep] = useState<'idle' | 'confirm' | 'sending'>('idle');
  const [error, setError] = useState<ErrorText | null>(null);
  const s = t.schedules;
  const last = item.last_occurrence;
  async function remove() {
    setStep('sending');
    setError(null);
    try {
      await api.deleteSchedule(projectId, item.id, item.version);
      onChanged();
    } catch (e) {
      // A changed version (409) or a schedule deleted elsewhere (404): show it and re-read the list.
      setError(describeError(e));
      setStep('idle');
      onChanged();
    }
  }
  return <li>
    <p><code>{item.cron}</code> · {item.timezone} · {item.enabled ? s.enabled : s.disabled}</p>
    <p className="small">{label(s.overlapLabels, item.overlap)} · {label(s.misfireLabels, item.misfire)}</p>
    <p className="small">{item.next_run_at ? s.next(formatInstant(item.next_run_at)) : s.nextNone}</p>
    {last && <p className="small">
      {s.last(formatInstant(last.scheduled_for), label(s.states, last.state))}
      {last.reason && <> ({label(s.reasons, last.reason)})</>}
      {last.missed_from && <> {s.missedFrom(formatInstant(last.missed_from))}</>}
    </p>}
    {item.pending_occurrence && <p className="small">{s.pending(formatInstant(item.pending_occurrence.scheduled_for))}</p>}
    {canManage && (step === 'idle'
      ? <button type="button" onClick={() => setStep('confirm')}>{s.delete}</button>
      : <div role="group" aria-label={s.delete}>
        <p>{s.deleteConfirm(item.cron)}</p>
        <button type="button" className="danger" onClick={remove} disabled={step === 'sending'}>{step === 'sending' ? s.deleting : s.deleteYes}</button>
        <button type="button" onClick={() => setStep('idle')} disabled={step === 'sending'}>{s.deleteNo}</button>
      </div>)}
    {error && <ErrorNote error={error} />}
  </li>;
}

function ScheduleForm({ api, projectId, onCreated }: { api: Api; projectId: string; onCreated: () => void }) {
  const [cron, setCron] = useState('');
  const [timezone, setTimezone] = useState(browserZone);
  const [overlap, setOverlap] = useState('coalesce_latest');
  const [misfire, setMisfire] = useState('latest_once');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<ErrorText | null>(null);
  const [done, setDone] = useState(false);
  // The last attempt may have been stored (network loss, 5xx): a retry reuses its key.
  const [unknownOutcome, setUnknownOutcome] = useState(false);
  // One key per exact body until the server answers it, so a retry after a lost response
  // returns the same schedule instead of creating a second one.
  const key = useRef<{ body: string; key: string } | null>(null);
  const s = t.schedules;

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (pending) return;
    const body: CreateScheduleBody = { cron: cron.trim(), timezone: timezone.trim(), overlap, misfire, run: RUN_BODY };
    const text = JSON.stringify(body);
    if (key.current?.body !== text) key.current = { body: text, key: newKey() };
    setPending(true);
    setError(null);
    setDone(false);
    try {
      await api.createSchedule(projectId, body, key.current.key);
      key.current = null;
      setCron('');
      setDone(true);
      onCreated();
    } catch (err) {
      setError(describeError(err));
      setUnknownOutcome(retryable(err));
    } finally {
      setPending(false);
    }
  }

  return <form onSubmit={submit} aria-label={s.create}>
    <label>{s.cron}<input value={cron} onChange={e => setCron(e.target.value)} required maxLength={200} spellCheck={false} /></label>
    <p className="small">{s.cronHint}</p>
    <label>{s.timezone}<input value={timezone} onChange={e => setTimezone(e.target.value)} required maxLength={64} spellCheck={false} /></label>
    <p className="small">{s.timezoneHint}</p>
    <label>{s.overlap}<select value={overlap} onChange={e => setOverlap(e.target.value)}>
      {Object.entries(s.overlapLabels).map(([v, l]) => <option key={v} value={v}>{l}</option>)}
    </select></label>
    <label>{s.misfire}<select value={misfire} onChange={e => setMisfire(e.target.value)}>
      {Object.entries(s.misfireLabels).map(([v, l]) => <option key={v} value={v}>{l}</option>)}
    </select></label>
    <button type="submit" className="primary" disabled={pending} aria-busy={pending}>{pending ? s.creating : s.create}</button>
    {error && <><ErrorNote error={error} />{unknownOutcome && <p className="small">{s.retryNote}</p>}</>}
    {done && <p role="status">{s.created}</p>}
  </form>;
}
