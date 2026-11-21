import { Fragment, type ReactNode } from 'react';
import { sourceEvidenceURL, type AnalysisReport } from './analysisReport';
import type { Citation } from './parseReportSections';
import { reportKo as t } from './locales/report-ko';
import { formatReportInstant, runStatusLabel } from './format';
import '../../internal/report/reportui/font.css';
import '../../internal/report/reportui/tabler.css';
import '../../internal/report/reportui/report.css';
import './report.css';
import '../../internal/report/reportui/workbench.css';

type IconName = 'alert' | 'chart' | 'check' | 'chevron' | 'clock' | 'code' | 'external' | 'file' | 'findings' | 'jump' | 'mark' | 'overview' | 'secret' | 'server' | 'terminal';

export function ReportIcon({ name }: { name: IconName }) {
  return <span className={`icon report-icon report-icon-${name}`} aria-hidden="true" />;
}

export function ReportLayout({ report, rail, children, className = '', purpose }: {
  report: AnalysisReport; rail?: ReactNode; children: ReactNode; className?: string; purpose?: Citation;
}) {
  const name = report.project?.name;
  const start = report.project?.start;
  const finish = report.project?.finish;
  const startedAt = Date.parse(start ?? '');
  const finishedAt = Date.parse(finish ?? '');
  const end = finish && Number.isFinite(finishedAt) ? {label: t.finish, time: finish} :
    report.published && Number.isFinite(Date.parse(report.published)) ? {label: t.published, time: report.published} : null;
  const duration = Number.isFinite(startedAt) && Number.isFinite(finishedAt) && finishedAt >= startedAt
    ? Math.round((finishedAt - startedAt) / 1000) : null;
  const purposeLink = purpose && sourceEvidenceURL(report.sourceBase, purpose.path, purpose.line, purpose.end);
  const purposeLocation = purpose && <code>{purpose.path}:{purpose.line}</code>;
  return (
    <article className={`report-document review-theme web-report ${className}`} aria-label={t.title}>
      <div className="report-layout">
        {rail && <aside className="report-rail">
          <div className="report-brand"><span className="report-mark"><ReportIcon name="mark" /></span><strong>{t.title}</strong></div>
          {rail}
        </aside>}
        <div className="report-main">
          <header className="report-hero">
            <p className="report-kicker">{name ? t.title : t.revision(report.revision)}</p>
            <h4 className="report-name">{name || t.title}</h4>
            {purpose?.excerpt && <details className="report-purpose"><summary>{t.projectIntro}</summary><p>
              {purpose.excerpt.text.trim()}{' '}
              {purposeLink ? <a href={purposeLink} target="_blank" rel="noopener noreferrer">{purposeLocation}</a> : purposeLocation}
            </p></details>}
            {report.project?.source && <div className="report-hero-source"><span>{t.target}</span><code>
              {report.project.source.split(/(?<=\/)/).map((part, i) => <Fragment key={i}>{i > 0 && <wbr />}{part}</Fragment>)}
            </code></div>}
            <div className="report-verdict" data-status={report.status}>
              <span className="badge"><ReportIcon name={report.status === 'completed' ? 'check' : 'clock'} />
                {Object.hasOwn(t.executionStatuses, report.status) ? t.executionStatuses[report.status] : t.executionStatus(runStatusLabel(report.status))}</span>
              {start && Number.isFinite(startedAt) && <span className="report-hero-time"><b>{t.start}</b> <time dateTime={start}>{formatReportInstant(start)}</time></span>}
              {end && <span className="report-hero-time"><b>{end.label}</b> <time dateTime={end.time}>{formatReportInstant(end.time)}</time></span>}
              {duration !== null && <span className="report-hero-time"><b>{t.duration}</b> {t.durationValue(duration)}</span>}
              {report.commit && <span className="report-hero-time"><b>{t.commit}</b> <code title={report.commit}>{report.commit.slice(0, 12)}</code></span>}
            </div>
          </header>
          {children}
        </div>
      </div>
    </article>
  );
}

export function ReportGroup({ title, tone, icon, children, id }: {
  title: string; tone: 'pending' | 'neutral'; icon: IconName; children: ReactNode; id?: string;
}) {
  return <section className="report-group" data-tone={tone}>
    <header className="report-group-title">
      <span className="report-group-mark"><ReportIcon name={icon} /></span>
      <h6 id={id} tabIndex={id ? -1 : undefined}>{title}</h6>
    </header>
    <div className="report-cards">{children}</div>
  </section>;
}

export function ReportAction({ onClick, children }: { onClick: () => void; children: ReactNode }) {
  return <button type="button" className="btn btn-sm btn-ghost-primary report-card-action" onClick={onClick}>
    {children}<ReportIcon name="jump" />
  </button>;
}
