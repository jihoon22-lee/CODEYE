import { useEffect, useState } from 'react';
import type { Api } from './api';
import type { AnalysisReport, Finding } from './analysisReport';
import { reportURL } from './analysisReport';
import { describeError } from './format';
import { ko } from './locales/ko';
import { reportKo as t } from './locales/report-ko';

function label(labels: Record<string, string>, key: string) {
  return Object.hasOwn(labels, key) ? labels[key] : key;
}
function explainReason(value: string) {
  const [code, ...detail] = value.split(':');
  if (!code || !Object.hasOwn(t.reasons, code)) return value;
  return [t.reasons[code], ...detail].join(' · ');
}
function TextList({ values }: { values: string[] }) {
  return (
    <ul>
      {values.map((value, i) => (
        <li key={i}>{value}</li>
      ))}
    </ul>
  );
}
function FindingCard({ f }: { f: Finding }) {
  return (
    <details className="finding-card">
      <summary>
        <strong>{f.title}</strong>
        <span>
          {label(t.validation, f.validation)} · {label(t.severity, f.severity)}
        </span>
      </summary>
      <dl>
        <dt>{t.conditions}</dt>
        <dd>
          <TextList values={f.conditions} />
        </dd>
        <dt>{t.severityReason}</dt>
        <dd>{f.severityReason}</dd>
        <dt>{t.impact}</dt>
        <dd>{f.impact}</dd>
        <dt>{t.recommendation}</dt>
        <dd>{f.recommendation}</dd>
        <dt>{t.verification}</dt>
        <dd>
          <TextList values={f.verification} />
        </dd>
      </dl>
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
      {f.limitations.length > 0 && (
        <>
          <h6>{t.limitations}</h6>
          <TextList values={f.limitations} />
        </>
      )}
    </details>
  );
}

export function ReportDocument({ report }: { report: AnalysisReport }) {
  const [includeCode, setIncludeCode] = useState(false);
  const [filter, setFilter] = useState('');
  const grouped = new Map<string, Finding[]>();
  for (const item of report.findings) {
    if (
      filter &&
      !(
        item.title +
        ' ' +
        item.impact +
        ' ' +
        label(t.categories, item.category)
      )
        .toLocaleLowerCase()
        .includes(filter.trim().toLocaleLowerCase())
    )
      continue;
    const group = grouped.get(item.category);
    if (group) group.push(item);
    else grouped.set(item.category, [item]);
  }
  return (
    <article className="report-document" aria-label={t.title}>
      <header>
        <h4>{t.title}</h4>
        <span>{t.revision(report.revision)}</span>
      </header>
      <p>
        <strong>{label(ko.runs.verdictLabels, report.verdict)}</strong> ·{' '}
        {t.coverage} {label(t.ai, report.aiReview)}
      </p>
      <dl className="report-counts">
        {(Object.keys(t.counts) as Array<keyof typeof t.counts>).map((key) => (
          <div key={key}>
            <dt>{t.counts[key]}</dt>
            <dd>{report.counts[key]}</dd>
          </div>
        ))}
      </dl>
      {report.limitations.length > 0 && (
        <section>
          <h5>{t.scope}</h5>
          <TextList values={report.limitations.map(explainReason)} />
        </section>
      )}
      <dl className="report-counts">
        {(Object.keys(t.metrics) as Array<keyof typeof t.metrics>).map(
          (key) => (
            <div key={key}>
              <dt>{t.metrics[key]}</dt>
              <dd>{report.scope[key] ?? t.unknown}</dd>
            </div>
          ),
        )}
      </dl>
      {report.calls.length > 0 && (
        <details>
          <summary>{t.calls}</summary>
          <ul>
            {report.calls.map((c, i) => (
              <li key={i}>
                {label(t.roles, c.role)} · {c.model || t.unknown} · OpenCode{' '}
                {c.version || t.unknown} · {label(t.outcomes, c.outcome)} ·{' '}
                {t.pack} <code>{c.pack.slice(0, 12)}</code>
              </li>
            ))}
          </ul>
        </details>
      )}
      <p>
        {t.snapshot} <code>{report.snapshot}</code>
      </p>
      <h5>{t.findings}</h5>
      {report.findings.length > 1 && (
        <label className="field">
          {t.filter}
          <input
            type="search"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
        </label>
      )}
      {grouped.size === 0 && <p>{filter ? t.noMatch : t.empty}</p>}
      {Array.from(grouped, ([category, findings]) => (
        <section key={category}>
          <h5>
            {label(t.categories, category)} ({findings.length})
          </h5>
          {findings.map((f) => (
            <FindingCard key={f.id} f={f} />
          ))}
        </section>
      ))}
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
      {report.observations.length > 0 && (
        <details>
          <summary>
            {t.observations} ({report.observations.length})
          </summary>
          <ul>
            {report.observations.map((o) => (
              <li key={o.id}>
                <code>{o.rule}</code> {o.message}{' '}
                <small>{o.path ?? o.state}</small>
              </li>
            ))}
          </ul>
        </details>
      )}
      <section>
        <h5>{t.exports}</h5>
        <label>
          <input
            type="checkbox"
            checked={includeCode}
            onChange={(e) => setIncludeCode(e.target.checked)}
          />{' '}
          {t.include}
        </label>
        <div className="actions">
          {['json', 'markdown', 'html'].map((format) => (
            <a
              key={format}
              download
              href={reportURL(
                report.runID,
                format,
                includeCode,
                report.revision,
              )}
            >
              {format.toUpperCase()}
            </a>
          ))}
        </div>
      </section>
    </article>
  );
}

export function ReportPanel({ api, runID }: { api: Api; runID: string }) {
  const [requested, setRequested] = useState(0);
  const [report, setReport] = useState<AnalysisReport | null>(null);
  const [error, setError] = useState('');
  useEffect(() => {
    if (!requested) return;
    const controller = new AbortController();
    api
      .getReport(runID, controller.signal)
      .then((value) => {
        if (!controller.signal.aborted) setReport(value);
      })
      .catch((err) => {
        if (!controller.signal.aborted) setError(describeError(err).message);
      });
    return () => controller.abort();
  }, [api, runID, requested]);
  if (report) return <ReportDocument report={report} />;
  return (
    <section>
      {error && <p role="alert">{error}</p>}
      {!requested || error ? (
        <button
          type="button"
          onClick={() => {
            setError('');
            setRequested(requested + 1);
          }}
        >
          {error ? t.retry : t.open}
        </button>
      ) : (
        <p role="status">{t.loading}</p>
      )}
    </section>
  );
}
