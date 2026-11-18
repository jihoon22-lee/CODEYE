import { useRef, useState, type FormEvent } from 'react';
import { ApiError, type Api } from './api';
import type { Comparison, ComparisonEntry, FindingStatus } from './comparison';
import { describeError, formatInstant, shorten, type ErrorText } from './format';
import { newKey } from './idempotency';
import { ko } from './locales/ko';
import { retryable } from './useRunPolling';

// W23 comparison header, per-finding comparison state and human review (docs/40).
// Every state, count and disposition shown is the server's; labels are text, never colour only.

const t = ko.history;
export const HIDDEN = new Set(['accepted_risk', 'false_positive', 'suppressed']);

export type ComparisonLoad = { state: 'loading' } | { state: 'failed'; error: ErrorText } | { state: 'ready'; value: Comparison };

const pick = (labels: Record<string, string>, raw: string, unknown: (raw: string) => string) =>
  Object.hasOwn(labels, raw) ? labels[raw]! : unknown(shorten(raw, 40));
export const stateText = (state: string) => pick(t.states, state, t.stateUnknown);
export const dispositionText = (d: string) => pick(t.dispositions, d, t.dispositionUnknown);
const reasonText = (reason: string) => pick(t.reasons, reason, (raw) => raw);
const short = (id: string) => shorten(id, 8);

function ErrorNote({ error }: { error: ErrorText }) {
  return (
    <p role="alert" className="error">
      {error.message}
      {error.detail ? <> {error.detail}</> : null}
      {error.requestId ? <small> {ko.errors.requestId(error.requestId)}</small> : null}
    </p>
  );
}

function differenceKey(key: string): string {
  if (Object.hasOwn(t.differenceKeys, key)) return t.differenceKeys[key]!;
  const [kind, name, field] = key.split(':');
  if (kind === 'tool' && name && field) return t.toolKey(name, pick(t.toolFields, field, (raw) => raw));
  if (kind === 'review' && name) return t.reviewKey(name);
  return key;
}

export function ComparisonSection({ load, reload }: { load: ComparisonLoad; reload: () => void }) {
  return (
    <section aria-labelledby="comparison-title" className="comparison">
      <h5 id="comparison-title">{t.title}</h5>
      {load.state === 'loading' && <p role="status">{t.loading}</p>}
      {load.state === 'failed' && (
        <>
          <ErrorNote error={{ ...load.error, message: `${t.loadFailed} ${load.error.message}` }} />
          <button type="button" onClick={reload}>
            {t.reload}
          </button>
        </>
      )}
      {load.state === 'ready' && <ComparisonHeader c={load.value} />}
    </section>
  );
}

function ComparisonHeader({ c }: { c: Comparison }) {
  const gone = c.entries.filter((e) => e.current.length === 0);
  return (
    <>
      {c.baseline && (
        <p>
          {t.baseline(short(c.baseline.runID), c.baseline.revision, formatInstant(c.baseline.createdAt))}
          <br />
          <small>{pick(t.selection, c.baseline.selection, (raw) => raw)}</small>
        </p>
      )}
      {!c.comparable && <p role="note">{c.notComparableReason ? pick(t.notComparable, c.notComparableReason, t.notComparableUnknown) : null}</p>}
      {c.baseline && (
        <>
          <h6>{t.differences}</h6>
          {c.differences.length === 0 ? (
            <p>{t.noDifferences}</p>
          ) : (
            <ul>
              {c.differences.map((d) => (
                <li key={d.key}>
                  {differenceKey(d.key)}: {t.change(d.baseline ?? t.none, d.current ?? t.none)}
                </li>
              ))}
            </ul>
          )}
        </>
      )}
      {c.comparable && (
        <>
          <h6>{t.counts}</h6>
          <dl className="report-counts">
            {Object.keys(t.states).map((state) => (
              <div key={state}>
                <dt>{stateText(state)}</dt>
                <dd>{c.counts[state] ?? 0}</dd>
              </div>
            ))}
          </dl>
          {(c.unidentified.current > 0 || c.unidentified.baseline > 0) && <p>{t.unidentified(c.unidentified.current, c.unidentified.baseline)}</p>}
          <details>
            <summary>
              {t.gone} ({gone.length})
            </summary>
            {gone.length === 0 ? (
              <p>{t.goneNone}</p>
            ) : (
              <ul>
                {gone.map((e) => (
                  <li key={e.fingerprint}>
                    <strong>{stateText(e.state)}</strong> · {pick(t.producers, e.producer, (raw) => raw)} <code>{e.analyzer}</code> <code>{e.rule}</code>
                    {e.baseline.map((o) => (
                      <div key={o.findingID}>
                        <code>
                          {o.path}:{o.startLine}
                        </code>
                      </div>
                    ))}
                    {e.reason && <div>{reasonText(e.reason)}</div>}
                    {e.moveCandidates.length > 0 && <div>{t.moveCandidates(e.moveCandidates.length)}</div>}
                  </li>
                ))}
              </ul>
            )}
          </details>
        </>
      )}
      <h6>{t.summary}</h6>
      <div className="table">
        <table>
          <thead>
            <tr>
              <th></th>
              {Object.values(t.summaryCounts).map((label) => (
                <th key={label}>{label}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {(
              [
                [t.summaryAt, c.summary.atPublication],
                [t.summaryNow, c.summary.current],
              ] as const
            ).map(([label, counts]) => (
              <tr key={label}>
                <th scope="row">{label}</th>
                {Object.keys(t.summaryCounts).map((key) => (
                  <td key={key}>{counts[key as 'total' | 'actionable' | 'hidden']}</td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}

// The comparison state and current disposition shown next to a finding's title.
export function findingBadge(status: FindingStatus | undefined): string | null {
  if (!status) return null;
  const parts: string[] = [];
  if (status.state) parts.push(stateText(status.state));
  if (status.current !== 'open') parts.push(dispositionText(status.current));
  return parts.length ? parts.join(' · ') : null;
}

type ReviewProps = { api: Api; comparison: Comparison; status: FindingStatus; entry: ComparisonEntry | undefined; onChanged: () => void };

export function FindingHistory({ api, comparison, status, entry, onChanged }: ReviewProps) {
  const r = status.review;
  return (
    <section className="finding-history">
      {entry && (
        <p>
          <strong>{stateText(entry.state)}</strong>
          {entry.state === 'new' && entry.reason && <> · {t.newBecause(reasonText(entry.reason))}</>}
          {entry.changes.length > 0 && <> · {t.changed(entry.changes.map((c) => pick(t.changeNames, c, (raw) => raw)).join(', '))}</>}
          {entry.ambiguous && <> · {t.ambiguous}</>}
          {entry.moveCandidates.length > 0 && <> · {t.moveCandidates(entry.moveCandidates.length)}</>}
        </p>
      )}
      <h6>{t.review}</h6>
      <dl>
        <dt>{t.atPublication}</dt>
        <dd>{dispositionText(status.publication)}</dd>
        <dt>{t.current}</dt>
        <dd>
          {dispositionText(status.current)} — {pick(t.matches, status.match, t.matchUnknown)}
        </dd>
        {r && (
          <>
            <dt>{t.reviewReason}</dt>
            <dd>
              {dispositionText(r.disposition)}: {r.reason}
              <div>
                <small>
                  {t.reviewBy(r.version, short(r.actorID), formatInstant(r.createdAt))}
                  {r.expiresAt && <> · {t.expiresAt(formatInstant(r.expiresAt))}</>}
                  {r.pathPrefix && <> · {t.pathScope(r.pathPrefix)}</>}
                  {(r.runID !== comparison.runID || r.reportRevision !== comparison.revision) && <> · {t.reviewedOn(short(r.runID), r.reportRevision)}</>}
                </small>
              </div>
            </dd>
          </>
        )}
      </dl>
      {!status.fingerprint ? (
        <p className="small">{t.noFingerprint}</p>
      ) : comparison.canReview ? (
        <ReviewForm api={api} comparison={comparison} status={status} onChanged={onChanged} />
      ) : (
        <p className="small">{t.viewerNote}</p>
      )}
    </section>
  );
}

// End of the chosen local day, so "until 2026-12-01" still applies on that day.
function expiryInstant(day: string): Date | null {
  if (!day) return null;
  const d = new Date(day + 'T23:59:59');
  return Number.isNaN(d.getTime()) ? null : d;
}

function ReviewForm({ api, comparison, status, onChanged }: Omit<ReviewProps, 'entry'>) {
  const [disposition, setDisposition] = useState(status.current === 'open' ? 'acknowledged' : 'open');
  const [reason, setReason] = useState('');
  const [expires, setExpires] = useState('');
  const [pathPrefix, setPathPrefix] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<ErrorText | null>(null);
  const [unknownOutcome, setUnknownOutcome] = useState(false);
  const [done, setDone] = useState(false);
  // One key per exact request (body and precondition) until the server answers it, so a retry
  // after a lost response returns the recorded version instead of appending another.
  const key = useRef<{ request: string; key: string } | null>(null);
  const id = 'review-' + status.findingID;
  const scoped = disposition !== 'open';

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (pending || !status.fingerprint) return;
    setDone(false);
    if (reason.trim() === '') {
      setError({ message: t.reasonRequired, detail: null, requestId: null });
      return;
    }
    const expiry = scoped ? expiryInstant(expires) : null;
    if (expiry && expiry.getTime() <= Date.now()) {
      setError({ message: t.expiryPast, detail: null, requestId: null });
      return;
    }
    const body = {
      fingerprint: status.fingerprint,
      disposition,
      reason: reason.trim(),
      expires_at: expiry ? expiry.toISOString() : null,
      path_prefix: scoped && pathPrefix.trim() ? pathPrefix.trim() : null,
      run_id: comparison.runID,
      revision: comparison.revision,
      finding_id: status.findingID,
    };
    const version = status.review?.version ?? null;
    const request = JSON.stringify([body, version]);
    if (key.current?.request !== request) key.current = { request, key: newKey() };
    setPending(true);
    setError(null);
    try {
      await api.createFindingReview(comparison.projectID, body, version, key.current.key);
      key.current = null;
      setReason('');
      setDone(true);
      setUnknownOutcome(false);
      onChanged();
    } catch (err) {
      const conflict = err instanceof ApiError && err.status === 409 && err.code === 'CONFLICT';
      setError(conflict ? { message: t.conflict, detail: null, requestId: err.requestId } : describeError(err));
      setUnknownOutcome(!conflict && retryable(err));
      // A version changed elsewhere: re-read so the next attempt uses the new precondition.
      if (conflict) onChanged();
    } finally {
      setPending(false);
    }
  }

  return (
    <form onSubmit={submit} aria-label={t.form} noValidate>
      <label>
        {t.disposition}
        <select value={disposition} onChange={(e) => setDisposition(e.target.value)}>
          {Object.entries(t.dispositions).map(([v, l]) => (
            <option key={v} value={v}>
              {l}
            </option>
          ))}
        </select>
      </label>
      <label>
        {t.reason}
        <textarea value={reason} onChange={(e) => setReason(e.target.value)} required maxLength={2000} aria-describedby={id + '-reason'} />
      </label>
      <p className="small" id={id + '-reason'}>
        {t.reasonHint}
      </p>
      {scoped ? (
        <>
          <label>
            {t.expires}
            <input type="date" value={expires} onChange={(e) => setExpires(e.target.value)} aria-describedby={id + '-expires'} />
          </label>
          <p className="small" id={id + '-expires'}>
            {t.expiresHint}
          </p>
          <label>
            {t.pathPrefix}
            <input value={pathPrefix} onChange={(e) => setPathPrefix(e.target.value)} maxLength={1024} spellCheck={false} aria-describedby={id + '-path'} />
          </label>
          <p className="small" id={id + '-path'}>
            {t.pathHint}
          </p>
        </>
      ) : (
        <p className="small">{t.openNote}</p>
      )}
      <button type="submit" className="primary" disabled={pending} aria-busy={pending}>
        {pending ? t.submitting : t.submit}
      </button>
      {error && (
        <>
          <ErrorNote error={error} />
          {unknownOutcome && <p className="small">{t.retryNote}</p>}
        </>
      )}
      {done && <p role="status">{t.saved}</p>}
    </form>
  );
}
