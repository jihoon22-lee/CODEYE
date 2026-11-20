import { createContext, useContext, useId, useState, type ReactNode } from 'react';
import type { Api } from './api';
import type { AnalysisReport, Evidence, Finding } from './analysisReport';
import { formatInstant, runStatusLabel } from './format';
import type { Citation, Excerpt } from './reportSections';
import { reportURL, sourceEvidenceURL } from './analysisReport';
import { FindingHistory, HIDDEN, findingBadge, type ComparisonLoad } from './HistoryView';
import { ko } from './locales/ko';
import { reportKo as t } from './locales/report-ko';
import { secondaryReviewFinding, type Decision } from './reportGroups';

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
export function ReportIdentity({ report, showStatus = true }: { report: AnalysisReport; showStatus?: boolean }) {
  const p = report.project;
  const facts = [
    { label: t.sourcePath, value: p?.source, wide: true },
    { label: t.status, value: showStatus ? runStatusLabel(report.status) : null },
    { label: t.start, value: p?.start && formatInstant(p.start) },
    { label: t.finish, value: p?.finish && formatInstant(p.finish) },
    { label: t.published, value: !p?.finish && report.published ? formatInstant(report.published) : null },
    { label: t.commit, value: report.commit, wide: true },
  ];
  return <dl className="report-identity">
    {facts.map(({ label, value, wide }) => value && (
      <div key={label} className={wide ? 'identity-wide' : undefined}>
        <dt>{label}</dt><dd>{value}</dd>
      </div>
    ))}
  </dl>;
}

function CodeWindow({ excerpt, location }: { excerpt: Excerpt; location: string }) {
  return (
    <pre className="source-window" tabIndex={0} aria-label={t.excerptAt(location)}><code>
      {excerpt.text.split('\n').map((line, i) => (
        <span className="source-line" key={i}><span aria-hidden="true">{excerpt.start + i}</span>{line}{'\n'}</span>
      ))}
    </code></pre>
  );
}

export const ReportSourceContext = createContext<string | undefined>(undefined);

function SourceAnchor({ href, location }: { href: string; location: string }) {
  return <a className="pinned-source" href={href} target="_blank" rel="noopener noreferrer" aria-label={t.pinnedRangeAt(location)}>{location}</a>;
}

export function PinnedSourceLink({ path, start, end, fallback }: { path: string; start: number; end: number; fallback?: string }) {
  const base = useContext(ReportSourceContext);
  const href = sourceEvidenceURL(base, path, start, end);
  return href ? <SourceAnchor href={href} location={sourceLocation(path, start, end)} /> : fallback ? <code>{fallback}</code> : null;
}

function FindingSourceLink({ evidence, fallback }: { evidence: Evidence; fallback: string }) {
  return evidence.path && evidence.lines && ['code', 'document', 'test'].includes(evidence.kind)
    ? <PinnedSourceLink path={evidence.path} {...evidence.lines} fallback={fallback} />
    : <code>{fallback}</code>;
}

export function Citations({refs,catalog}: {refs: string[]; catalog: Citation[]}) {
  const base = useContext(ReportSourceContext);
  const evidence = refs.map(id => catalog.find(x => x.id === id)).filter(x => x !== undefined);
  return <>{evidence.map(e => {
    const location = sourceLocation(e.path, e.line, e.end);
    const href = sourceEvidenceURL(base, e.path, e.line, e.end);
    return <div className="source-citation" key={e.id}>
      {href && <SourceAnchor href={href} location={location} />}
      {e.excerpt ? <details>
        <summary aria-label={href ? t.excerptAt(location) : undefined}>{href ? t.showExcerpt : location}</summary>
        <CodeWindow excerpt={e.excerpt} location={sourceLocation(e.path, e.excerpt.start, e.excerpt.end)} />
      </details> : !href && <code>{location}</code>}
    </div>;
  })}</>;
}

export function sourceLocation(path: string, start: number, end: number) {
  return `${path}:${start === end ? start : `${start}–${end}`}`;
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
export function FindingCard({ f, badge, history, preview }: { f: Finding; badge?: string | null; history?: ReactNode; preview?: Decision }) {
  const [open, setOpen] = useState(false);
  const base = useContext(ReportSourceContext);
  const known = (labels: Record<string, string>, key: string) => (Object.hasOwn(labels, key) ? key : 'unknown');
  const location = preview ? sourceLocation(preview.source.path, preview.source.lines.start, preview.source.lines.end) : f.locations[0];
  const href = preview && sourceEvidenceURL(base, preview.source.path, preview.source.lines.start, preview.source.lines.end);
  const status = preview && f.validation === 'supported' ? t.reading.supported : label(t.validation, f.validation);
  const source = location && href ? <SourceAnchor href={href} location={location} /> : null;
  const details = (
    <FindingDetails f={f} history={history} preview={Boolean(preview)} linkedEvidence={href ? preview?.source.id : undefined} />
  );
  return (
    <>
    <details data-finding-id={f.id} className={'finding-card sev-' + known(t.severity, f.severity)} onToggle={preview ? (e) => setOpen(e.currentTarget.open) : undefined}>
      <summary>
        <strong>{f.title}</strong>
        <span>
          <span className={'pill sev sev-' + known(t.severity, f.severity)}>{t.severityBadge(label(t.severity, f.severity))}</span>
          <span className={'pill val val-' + known(t.validation, f.validation)}>{status}</span>
          {badge && <span className="pill tone-plain">{badge}</span>}
          {!preview && secondaryReviewFinding(f) && <span className="pill tone-plain">{t.reading.secondary}</span>}
          {location && !href && (
            <span className="where">
              {location}
              {!preview && f.locations.length > 1 && t.morePlaces(f.locations.length - 1)}
            </span>
          )}
        </span>
        {preview && <span className="finding-preview">
          {preview.condition && <span><b>{t.reading.basis}</b> {preview.condition}</span>}
          {preview.next
            ? <span><b>{t.reading.nextCheck}</b> {preview.next}</span>
            : <span><b>{t.reading.nextAction}</b> {f.recommendation}</span>}
        </span>}
      </summary>
      {open && source}
      {(!preview || open) && details}
    </details>
    {!open && source}
    </>
  );
}

function FindingDetails({ f, history, preview, linkedEvidence }: { f: Finding; history?: ReactNode; preview: boolean; linkedEvidence?: string }) {
  const ref = useId();
  return (
    <div className="finding-body">
      <dl className="key">
        <dt>{t.impact}</dt>
        <dd>{f.impact}</dd>
        <dt>{t.recommendation}</dt>
        <dd>{f.recommendation}</dd>
      </dl>
      {f.evidence.map(e => {
        if (!e.excerpt) return null;
        const { start, end } = e.excerpt;
        const location = sourceLocation(e.path ?? '', start, end);
        return (
          <figure key={e.id} className="finding-excerpt">
            <figcaption>{e.id === linkedEvidence ? <code>{location}</code> : <FindingSourceLink evidence={e} fallback={location} />}</figcaption>
            <CodeWindow excerpt={e.excerpt} location={location} />
            <p className="small">{e.description}</p>
          </figure>
        );
      })}
      <dl>
        <dt>{t.conditions}</dt>
        <dd>
          <TextList values={f.conditions.map(c => c.statement)} />
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
      <details className="finding-evidence" open={preview && !f.evidence.some(e => e.excerpt)}>
        <summary>{t.evidenceToggle(f.evidence.length)}</summary>
        {f.claims.map((claim, i) => (
          <p key={i}>
            <strong>{claim.kind === 'observed' ? t.observed : t.inferred} · </strong>
            {claim.assertion}
            {claim.evidence.map((id, index) => (
              <a key={id} className="report-ref" href={`#${ref}-${id}`}>{t.ref(index + 1)}</a>
            ))}
          </p>
        ))}
        <h6>{t.evidence}</h6>
        <ul>
          {f.evidence.map((e) => (
            <li key={e.id} id={`${ref}-${e.id}`}>
              {f.counter.includes(e.id) && <strong>{t.counter} · </strong>}
              {e.description}
              {e.path && e.lines && <div>
                {e.excerpt || e.id === linkedEvidence
                  ? <code>{e.path}:{e.lines.start}–{e.lines.end}</code>
                  : <FindingSourceLink evidence={e} fallback={`${e.path}:${e.lines.start}–${e.lines.end}`} />}
              </div>}
              {e.artifact && <div>{t.artifact} <code>{e.artifact}</code></div>}
            </li>
          ))}
        </ul>
      </details>
      {history}
    </div>
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

// ReportCounts presents the counts stored at publication.
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

