import { useCallback, useEffect, useState, type ReactNode } from 'react';
import type { Api } from './api';
import type { AnalysisReport } from './analysisReport';
import { describeError } from './format';
import { ComparisonSection, type ComparisonLoad } from './HistoryView';
import { reportKo as t } from './locales/report-ko';
import { ReportIdentity, FindingList, ReportCalls, ReportCounts, ReportExports, ReportScope, ReportSourceContext, ReportTasks, TextList, Verdict, explainReason, type ReportHistory } from './ReportParts';
import { SectionedReport } from './ReportSections';

export type { ReportHistory } from './ReportParts';

export function ReportDocument({ report, history }: { report: AnalysisReport; history?: ReportHistory }) {
  return <ReportSourceContext value={report.sourceBase}>
    {report.sections ? <SectionedReport report={report} sections={report.sections} history={history} /> : <FlatReport report={report} history={history} />}
  </ReportSourceContext>;
}

// Revisions published before the analysis sections keep this flat layout.
function FlatReport({ report, history }: { report: AnalysisReport; history?: ReportHistory }) {
  return (
    <article className="report-document" aria-label={t.title}>
      <header>
        <h4>{t.title}</h4>
        {report.project?.name && <p className="report-project">{report.project.name}</p>}
        <span>{t.revision(report.revision)}</span>
      </header>
      <p>{t.reading.reportsOnly}</p>
      <Verdict report={report} />
      <h5>{t.findings}</h5>
      <FindingList findings={report.findings} history={history} empty={t.empty} />
      <ReportCounts report={report} />
      {report.limitations.length > 0 && (
        <section>
          <h5>{t.scope}</h5>
          <TextList values={report.limitations.map(explainReason)} />
        </section>
      )}
      {history && <ComparisonSection load={history.load} reload={history.reload} />}
      <details>
        <summary>{t.reading.provenance}</summary>
        <ReportIdentity report={report} />
        <p>{t.snapshot} <code>{report.snapshot}</code></p>
        <ReportScope report={report} />
        <ReportCalls report={report} />
        <ReportTasks report={report} />
      </details>
      {report.observations.length > 0 && (
        <details>
          <summary>
            {t.observations} ({report.observations.length})
          </summary>
          <ul>
            {report.observations.map((o) => (
              <li key={o.id}>
                <code>{o.rule}</code> {o.message} {o.metric && <small>({t.metricMarker(o.metric.value, o.metric.threshold)})</small>} <small>{o.path ?? o.state}</small>
              </li>
            ))}
          </ul>
        </details>
      )}
      <ReportExports report={report} />
    </article>
  );
}

// The report of a finished run opens by itself; extra renders above it (the improvement hints).
export function ReportPanel({ api, runID, extra }: { api: Api; runID: string; extra?: (report: AnalysisReport) => ReactNode }) {
  const [requested, setRequested] = useState(1);
  const [report, setReport] = useState<AnalysisReport | null>(null);
  const [error, setError] = useState('');
  const [load, setLoad] = useState<ComparisonLoad>({ state: 'loading' });
  const [loads, setLoads] = useState(0);
  const reload = useCallback(() => setLoads((n) => n + 1), []);
  const revision = report?.revision;
  useEffect(() => {
    if (revision === undefined) return;
    // The comparison is of the revision shown, never of a newer one published meanwhile.
    const controller = new AbortController();
    api.getComparison(runID, revision, controller.signal).then(
      (value) => setLoad({ state: 'ready', value }),
      (err) => {
        if (!controller.signal.aborted) setLoad({ state: 'failed', error: describeError(err) });
      },
    );
    return () => controller.abort();
  }, [api, runID, revision, loads]);
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
  if (report) return <><ReportDocument report={report} history={{ load, api, reload }} />{extra?.(report)}</>;
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
