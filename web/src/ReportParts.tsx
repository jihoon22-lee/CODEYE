import { useState, type ReactNode } from 'react';
import type { Api } from './api';
import type { AnalysisReport, Finding } from './analysisReport';
import { reportURL } from './analysisReport';
import { FindingHistory, HIDDEN, findingBadge, type ComparisonLoad } from './HistoryView';
import { ko } from './locales/ko';
import { reportKo as t } from './locales/report-ko';

export function label(labels: Record<string, string>, key: string) {
  return Object.hasOwn(labels, key) ? labels[key] : key;
}
// A reason code ("code" or "code:detail") in words: the report's wording, else the stage
// wording the run page uses, else the code itself.
export function explainReason(value: string) {
  const [code, ...detail] = value.split(':');
  if (!code) return value;
  const text = Object.hasOwn(t.reasons, code) ? t.reasons[code] : Object.hasOwn(ko.stages.reasonLabels, code) ? ko.stages.reasonLabels[code] : null;
  if (text == null) return value;
  return [text, ...detail].join(' · ');
}
export function TextList({ values }: { values: string[] }) {
  return (
    <ul>
      {values.map((value, i) => (
        <li key={i}>{value}</li>
      ))}
    </ul>
  );
}
// A finding a reader can scan: the title, severity and validation as words, and where; once
// opened, the impact and the fix first, then the conditions and how to check, with the claims
// and evidence behind their own disclosure.
function FindingCard({ f, badge, history }: { f: Finding; badge?: string | null; history?: ReactNode }) {
  const known = (labels: Record<string, string>, key: string) => (Object.hasOwn(labels, key) ? key : 'unknown');
  return (
    <details className={'finding-card sev-' + known(t.severity, f.severity)}>
      <summary>
        <strong>{f.title}</strong>
        <span>
          <span className={'pill sev sev-' + known(t.severity, f.severity)}>{t.severityBadge(label(t.severity, f.severity))}</span>
          <span className={'pill val val-' + known(t.validation, f.validation)}>{label(t.validation, f.validation)}</span>
          {badge && <span className="pill tone-plain">{badge}</span>}
          {f.locations.length > 0 && (
            <span className="where">
              {f.locations[0]}
              {f.locations.length > 1 && t.morePlaces(f.locations.length - 1)}
            </span>
          )}
        </span>
      </summary>
      {history}
      <dl className="key">
        <dt>{t.impact}</dt>
        <dd>{f.impact}</dd>
        <dt>{t.recommendation}</dt>
        <dd>{f.recommendation}</dd>
      </dl>
      <dl>
        <dt>{t.conditions}</dt>
        <dd>
          <TextList values={f.conditions} />
        </dd>
        <dt>{t.severityReason}</dt>
        <dd>{f.severityReason}</dd>
        <dt>{t.verification}</dt>
        <dd>
          <TextList values={f.verification} />
        </dd>
      </dl>
      {f.limitations.length > 0 && (
        <>
          <h6>{t.limitations}</h6>
          <TextList values={f.limitations} />
        </>
      )}
      <details className="finding-evidence">
      <summary>{t.evidenceToggle(f.evidence.length)}</summary>
      {f.claims.map((claim, i) => (
        <p key={i}>
          <strong>
            {claim.kind === 'observed' ? t.observed : t.inferred} ·{' '}
          </strong>
          {claim.assertion}
          {claim.evidence.map((id, index) => (
            <a key={id} className="report-ref" href={'#ev-' + f.id + '-' + id}>
              {t.ref(index + 1)}
            </a>
          ))}
        </p>
      ))}
      <h6>{t.evidence}</h6>
      <ul>
        {f.evidence.map((e) => (
          <li key={e.id} id={'ev-' + f.id + '-' + e.id}>
            {f.counter.includes(e.id) && <strong>{t.counter} · </strong>}
            {e.description}
            {e.path && (
              <div>
                <code>
                  {e.path}:{e.range}
                </code>
              </div>
            )}
            {e.artifact && (
              <div>
                {t.artifact} <code>{e.artifact}</code>
              </div>
            )}
          </li>
        ))}
      </ul>
      </details>
    </details>
  );
}

// The comparison of this revision and the reviews of its findings, when loaded.
export type ReportHistory = { load: ComparisonLoad; api: Api; reload: () => void };

// FindingList is a searchable list of findings grouped by category. A finding
// whose loaded current disposition hides it is left out of the list unless
// asked for; counts elsewhere keep it.
export function FindingList({ findings, history, empty }: { findings: Finding[]; history?: ReportHistory; empty: string }) {
  const [filter, setFilter] = useState('');
  const [showHidden, setShowHidden] = useState(false);
  const comparison = history?.load.state === 'ready' ? history.load.value : null;
  const statuses = new Map(comparison?.findings.map((s) => [s.findingID, s]));
  const entries = new Map(comparison?.entries.map((e) => [e.fingerprint, e]));
  const hidden = findings.filter((f) => HIDDEN.has(statuses.get(f.id)?.current ?? 'open'));
  const grouped = new Map<string, Finding[]>();
  for (const item of findings) {
    if (!showHidden && hidden.includes(item)) continue;
    if (
      filter &&
      !(item.title + ' ' + item.impact + ' ' + label(t.categories, item.category))
        .toLocaleLowerCase()
        .includes(filter.trim().toLocaleLowerCase())
    )
      continue;
    const group = grouped.get(item.category);
    if (group) group.push(item);
    else grouped.set(item.category, [item]);
  }
  return (
    <>
      {findings.length > 1 && (
        <label className="field">
          {t.filter}
          <input type="search" value={filter} onChange={(e) => setFilter(e.target.value)} />
        </label>
      )}
      {hidden.length > 0 && (
        <>
          <label>
            <input type="checkbox" checked={showHidden} onChange={(e) => setShowHidden(e.target.checked)} /> {ko.history.hiddenFilter(hidden.length)}
          </label>
          {!showHidden && <p className="small">{ko.history.hiddenNote(hidden.length)}</p>}
        </>
      )}
      {grouped.size === 0 && (filter ? <p>{t.noMatch}</p> : hidden.length === 0 && <p>{empty}</p>)}
      {Array.from(grouped, ([category, items]) => (
        <section key={category}>
          <h5>
            {label(t.categories, category)} ({items.length})
          </h5>
          {items.map((f) => {
            const status = statuses.get(f.id);
            return (
              <FindingCard
                key={f.id}
                f={f}
                badge={findingBadge(status)}
                history={
                  comparison &&
                  status &&
                  history && (
                    <FindingHistory
                      api={history.api}
                      comparison={comparison}
                      status={status}
                      entry={status.fingerprint ? entries.get(status.fingerprint) : undefined}
                      onChanged={history.reload}
                    />
                  )
                }
              />
            );
          })}
        </section>
      ))}
    </>
  );
}

// Verdict states the quality verdict in words with what it means; the model review state follows.
export function Verdict({ report }: { report: AnalysisReport }) {
  const known = Object.hasOwn(t.verdictLead, report.verdict);
  return (
    <div className={'verdict v-' + (known ? report.verdict : 'unknown')}>
      <span className="icon" aria-hidden="true">
        {known ? t.verdictIcon[report.verdict] : '?'}
      </span>
      <div>
        <strong>{label(ko.runs.verdictLabels, report.verdict)}</strong>
        <p>{known ? t.verdictLead[report.verdict] : t.verdictUnknown}</p>
        <p className="small">
          {t.coverage} {label(t.ai, report.aiReview)}
        </p>
      </div>
    </div>
  );
}

// ReportCounts are the summary numbers both layouts open with.
export function ReportCounts({ report }: { report: AnalysisReport }) {
  return (
    <dl className="report-counts">
      {(Object.keys(t.counts) as Array<keyof typeof t.counts>).map((key) => (
        <div key={key}>
          <dt>{t.counts[key]}</dt>
          <dd>{report.counts[key]}</dd>
        </div>
      ))}
      {report.metricObservations > 0 && (
        <div>
          <dt>{t.metricObservations}</dt>
          <dd>{report.metricObservations}</dd>
        </div>
      )}
    </dl>
  );
}

export function ReportScope({ report }: { report: AnalysisReport }) {
  return (
    <dl className="report-counts">
      {(Object.keys(t.metrics) as Array<keyof typeof t.metrics>).map((key) => (
        <div key={key}>
          <dt>{t.metrics[key]}</dt>
          <dd>{report.scope[key] ?? t.unknown}</dd>
        </div>
      ))}
    </dl>
  );
}

export function ReportCalls({ report }: { report: AnalysisReport }) {
  if (report.calls.length === 0) return null;
  return (
    <details>
      <summary>{t.calls}</summary>
      <ul>
        {report.calls.map((c, i) => (
          <li key={i}>
            {label(t.roles, c.role)} · {c.model || t.unknown} · OpenCode {c.version || t.unknown} · {label(t.outcomes, c.outcome)} · {t.pack} <code>{c.pack.slice(0, 12)}</code>
          </li>
        ))}
      </ul>
    </details>
  );
}

export function ReportTasks({ report }: { report: AnalysisReport }) {
  return (
    <details>
      <summary>{t.tasks}</summary>
      <div className="table">
        <table>
          <thead>
            <tr>
              <th>{t.task}</th>
              <th>{t.state}</th>
              <th>{t.reason}</th>
            </tr>
          </thead>
          <tbody>
            {report.tasks.map((row) => (
              <tr key={row.key}>
                <td>
                  <code>{row.key}</code>
                </td>
                <td>{label(t.statuses, row.status)}</td>
                <td>{row.reason ? explainReason(row.reason) : '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </details>
  );
}

export function ReportExports({ report }: { report: AnalysisReport }) {
  const [includeCode, setIncludeCode] = useState(false);
  return (
    <section className="exports">
      <h5>{t.exports}</h5>
      <label className="inline">
        <input type="checkbox" checked={includeCode} onChange={(e) => setIncludeCode(e.target.checked)} /> {t.include}
      </label>
      <div className="actions">
        {['json', 'markdown', 'html'].map((format) => (
          <a key={format} download href={reportURL(report.runID, format, includeCode, report.revision)}>
            {format.toUpperCase()}
          </a>
        ))}
        {['markdown', 'html'].map((format) => (
          <a key={format + '-full'} download href={reportURL(report.runID, format, includeCode, report.revision, 'full')}>
            {t.exportFull(format.toUpperCase())}
          </a>
        ))}
      </div>
      <p className="small">{t.exportNote}</p>
    </section>
  );
}

