import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import type { Api } from './api';
import type { AnalysisReport } from './analysisReport';
import { describeError } from './format';
import { ComparisonSection, type ComparisonLoad } from './HistoryView';
import { reportKo as t } from './locales/report-ko';
import { ReportIdentity, FindingList, ReportCalls, ReportCounts, ReportExports, ReportReviewStatus, ReportScope, ReportSourceContext, ReportTasks, TextList, explainReason, type ReportHistory } from './ReportParts';
import { ReportGroup, ReportIcon, ReportLayout } from './ReportLayout';
import { SectionedReport } from './ReportSections';
import { ReportSearch, focusReportFinding } from './ReportSearch';

export type { ReportHistory } from './ReportParts';

export function ReportDocument({ report, history }: { report: AnalysisReport; history?: ReportHistory }) {
  return <ReportSourceContext value={report.sourceBase}>
    {report.sections ? <SectionedReport report={report} sections={report.sections} history={history} /> : <FlatReport report={report} history={history} />}
  </ReportSourceContext>;
}

// Revisions published before the analysis sections keep this flat layout.
function FlatReport({ report, history }: { report: AnalysisReport; history?: ReportHistory }) {
  const findingList = useRef<HTMLDivElement>(null);
  const [finding, setFinding] = useState<{ id: string }>();
  useLayoutEffect(() => {
    if (finding) focusReportFinding(findingList.current, finding.id);
  }, [finding]);
  const rail = <nav aria-label={t.title} className="nav report-tabs">
    <button type="button" className="nav-link" aria-controls="report-findings" onClick={() => document.getElementById('report-findings')?.focus()}>
      <ReportIcon name="findings" /><span>{t.findings}</span>
    </button>
    <button type="button" className="nav-link" aria-controls="report-inputs" onClick={() => {
      const details = document.getElementById('report-inputs') as HTMLDetailsElement | null;
      if (details) {
        details.open = true;
        details.querySelector('summary')?.focus();
      }
    }}>
      <ReportIcon name="file" /><span>{t.reading.provenance}</span>
    </button>
    <button type="button" className="nav-link" aria-controls="report-exports" onClick={() => document.getElementById('report-exports')?.focus()}>
      <ReportIcon name="jump" /><span>{t.exports}</span>
    </button>
  </nav>;
  return (
    <ReportLayout report={report} rail={<>
      {rail}
      {report.findings.length > 0 && <ReportSearch key={report.runID + ':' + report.revision} report={report}
        navigate={target => { if (target.kind === 'finding') setFinding({ id: target.id }); }} />}
    </>}>
      <ReportGroup title={t.findings} tone="neutral" icon="findings" id="report-findings">
        <div ref={findingList}><FindingList findings={report.findings} history={history} empty={t.empty} reveal={finding} /></div>
      </ReportGroup>
      <ReportGroup title={t.reading.operations} tone="neutral" icon="chart"><ReportReviewStatus report={report} /><ReportCounts report={report} /></ReportGroup>
      {report.limitations.length > 0 && (
        <ReportGroup title={t.scope} tone="pending" icon="alert">
          <div className="card report-card"><div className="card-body"><TextList values={report.limitations.map(explainReason)} /></div></div>
        </ReportGroup>
      )}
      {history && <ComparisonSection load={history.load} reload={history.reload} />}
      <details className="report-section" id="report-inputs">
        <summary>{t.reading.provenance}</summary>
        <ReportIdentity report={report} />
        <p>{t.snapshot} <code>{report.snapshot}</code></p>
        <ReportScope report={report} />
        <ReportCalls report={report} />
        <ReportTasks report={report} />
      </details>
      {report.observations.length > 0 && (
        <details className="report-section">
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
    </ReportLayout>
  );
}

// The report of a finished run opens by itself; extra renders above it (the improvement hints).
export function ReportPanel({ api, runID, extra, claimReportLanding }: {
  api: Api; runID: string; extra?: (report: AnalysisReport) => ReactNode; claimReportLanding?: (runId: string) => boolean;
}) {
  const [requested, setRequested] = useState(1);
  const [report, setReport] = useState<AnalysisReport | null>(null);
  const [error, setError] = useState('');
  const [load, setLoad] = useState<ComparisonLoad>({ state: 'loading' });
  const [loads, setLoads] = useState(0);
  const reload = useCallback(() => setLoads((n) => n + 1), []);
  const revision = report?.revision;
  const attachReport = useCallback((container: HTMLDivElement | null) => {
    if (!claimReportLanding || !container) return;
    const reveal = () => {
      if (document.visibilityState === 'hidden') return;
      document.removeEventListener('visibilitychange', reveal);
      const heading = container.querySelector<HTMLElement>('.report-name');
      if (!heading || !claimReportLanding(runID)) return;
      heading.tabIndex = -1;
      heading.focus({ preventScroll: true });
      heading.closest('article')?.scrollIntoView?.({ block: 'start', behavior: 'instant' });
    };
    if (document.visibilityState === 'hidden') {
      document.addEventListener('visibilitychange', reveal);
      return () => document.removeEventListener('visibilitychange', reveal);
    }
    reveal();
  }, [claimReportLanding, runID]);
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
  if (report) return <>
    <div ref={attachReport}><ReportDocument report={report} history={{ load, api, reload }} /></div>
    {extra?.(report)}
  </>;
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
