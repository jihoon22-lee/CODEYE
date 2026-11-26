import { Fragment, createContext, useContext, useId, useState, useSyncExternalStore, type ReactNode } from 'react';
import type { Api } from './api';
import type { AnalysisReport, Evidence, Finding } from './analysisReport';
import { formatInstant, label, runStatusLabel } from './format';
import type { Citation, Excerpt, RelatedWorkflow } from './parseReportSections';
import { reportURL, sourceEvidenceURL } from './analysisReport';
import { FindingHistory, HIDDEN, findingBadge, type ComparisonLoad } from './HistoryView';
import { ko } from './locales/ko';
import { codeText } from './locales/codeLabels';
import { reportKo as t } from './locales/report-ko';
import { ReportIcon } from './ReportLayout';
import tablerLicense from '../../internal/report/reportui/LICENSE.tabler-core?url&no-inline';
import iconsLicense from '../../internal/report/reportui/LICENSE.tabler-icons?url&no-inline';
import bootstrapLicense from '../../internal/report/reportui/LICENSE.bootstrap?url&no-inline';
import fontLicense from '../../internal/report/reportui/LICENSE.noto-sans-kr?url&no-inline';
import { secondaryReviewFinding } from './reportGroups';
import type { DocumentExample } from './reportExample';
import { ReportFindingWorkbench } from './ReportFindingWorkbench';

export { label } from './format';
// A reason code ("code" or "code:detail") in words from the shared catalog, else
// the code itself.
export function explainReason(value: string) {
  return codeText(value) ?? value;
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

export function ReportReviewStatus({ report }: { report: AnalysisReport }) {
  return <>
    <p><strong>{t.reading.verdict}</strong> {label(ko.runs.verdictLabels, report.verdict)}</p>
    <p><strong>{t.coverage}</strong> {label(t.ai, report.aiReview)}</p>
  </>;
}

export function CodeWindow({ excerpt, location, focusLine }: { excerpt: Excerpt; location: string; focusLine?: number }) {
  return (
    <pre className="source-window" tabIndex={0} aria-label={t.excerptAt(location)}><code>
      {excerpt.text.split('\n').map((line, i) => (
        <span className="source-line" key={i} data-focus={excerpt.start + i === focusLine ? 'true' : undefined}>
          <span className="source-line-number" aria-hidden="true">{excerpt.start + i}</span><span className="source-line-code">{line}</span>
        </span>
      ))}
    </code></pre>
  );
}

function sourceFocus(title: string | undefined, lines: string[], excerpt: Excerpt, start: number, end: number) {
  if (start === end && start >= excerpt.start && start <= excerpt.end) return start;
  const exact = title?.trim();
  if (exact) {
    const matches = lines.map((line, index) => line.trim() === exact ? index : -1).filter(index => index >= 0);
    if (matches.length === 1) return excerpt.start + matches[0];
  }
  for (const [, symbol] of title?.matchAll(/`([a-zA-Z_][a-zA-Z_0-9]*)`/g) ?? []) {
    const token = new RegExp(`\\b${symbol}\\b`);
    const matches = lines.map((line, index) => !/^\s*(?:\/\/|\/\*|\* |# )/.test(line) && token.test(line) ? index : -1).filter(index => index >= 0);
    if (matches.length === 1) return excerpt.start + matches[0];
  }
  if (start >= excerpt.start && end <= excerpt.end && end - start < 5) return start;
}

const sourceWrapQuery = '(max-width: 36rem)';

function watchSourceWidth(change: () => void) {
  const query = typeof window.matchMedia === 'function' ? window.matchMedia(sourceWrapQuery) : null;
  query?.addEventListener('change', change);
  return () => query?.removeEventListener('change', change);
}

function sourceWrapDefault() {
  return typeof window.matchMedia === 'function' && window.matchMedia(sourceWrapQuery).matches;
}

export function ReportPath({ value, className, children }: { value: string; className?: string; children?: ReactNode }) {
  return <code className={className ? `report-path-text ${className}` : 'report-path-text'}>{value.split(/(?<=\/)/).map((part, index) =>
    <Fragment key={index}>{index > 0 && <wbr />}{part}</Fragment>)}{children}</code>;
}

export function ReportPreview({ path, start, end, excerpt, title }: { path: string; start: number; end: number; excerpt: Excerpt; title?: string }) {
  const [full, setFull] = useState(false);
  const wrapDefault = useSyncExternalStore(watchSourceWidth, sourceWrapDefault, () => false);
  const [wrapPreference, setWrap] = useState<boolean>();
  const wrap = wrapPreference ?? wrapDefault;
  const lines = excerpt.text.split('\n');
  const focus = sourceFocus(title, lines, excerpt, start, end);
  const first = focus === undefined ? excerpt.start : Math.max(excerpt.start, Math.min(focus - 1, excerpt.end - 4));
  const last = focus === undefined ? excerpt.end : Math.min(excerpt.end, first + 4);
  const previewRange = first === last ? String(first) : `${first}–${last}`;
  const citedRange = start === end ? String(start) : `${start}–${end}`;
  const canExpand = first !== excerpt.start || last !== excerpt.end;
  const shown = full && canExpand ? excerpt : { start: first, end: last, text: lines.slice(first - excerpt.start, last - excerpt.start + 1).join('\n') };
  const windowTitle = sourceLocation(path, shown.start, shown.end);
  const savedIsCitation = excerpt.start === start && excerpt.end === end;
  return <div className="report-selected-source" data-wrap={wrap}>
    <p className="report-excerpt-label"><ReportPath value={path} /></p>
    <div className="report-source-toolbar">
      {canExpand ? <>
        <button type="button" aria-pressed={!full} onClick={() => setFull(false)}>{t.savedExcerpt} {previewRange}</button>
        <button type="button" aria-pressed={full} onClick={() => setFull(true)}>{savedIsCitation ? t.citedRange : t.fullSavedExcerpt} {excerpt.start}–{excerpt.end}</button>
      </> : <span>{t.savedExcerpt} {previewRange}</span>}
      {!savedIsCitation && citedRange !== previewRange && <span>{t.citedRange} {citedRange}</span>}
      <button type="button" aria-pressed={wrap} onClick={() => setWrap(!wrap)}>{t.wrapSource}</button>
    </div>
    <p className="report-print-ranges">{t.savedExcerpt} {previewRange} · {t.citedRange} {citedRange}</p>
    <CodeWindow location={windowTitle} excerpt={shown} focusLine={focus} />
  </div>;
}

export const ReportSourceContext = createContext<string | undefined>(undefined);

function SourceAnchor({ href, location, action = t.openSource }: { href: string; location: string; action?: string }) {
  return <a className="btn btn-sm btn-ghost-primary report-source-action" href={href} target="_blank" rel="noopener noreferrer" aria-label={t.sourceActionAt(action, location)}>
    {action}<ReportIcon name="external" />
  </a>;
}

function EvidenceLocation({ location, href, excerpt, path, kind, actionLabel, example, showLocation }: {
  location: string; href?: string; excerpt?: Excerpt | null; path?: string; kind?: string; actionLabel?: string;
  example?: DocumentExample; showLocation?: boolean;
}) {
  const isDocument = kind === 'document';
  const action = actionLabel ?? (isDocument ? t.viewDocument : kind === 'test' ? t.viewTestCode : kind === 'manifest' ? t.viewManifest : t.viewCode);
  const file = <span className="report-file"><ReportIcon name="file" /><ReportPath value={location} /></span>;
  return <div className="report-evidence">
    {example && <p className="report-document-example"><strong>{t.reading.documentExample}</strong>{' '}
      <code>{example.call}</code> → <code>{example.output}</code></p>}
    {excerpt ? <details className="report-excerpt">
      <summary className="btn btn-sm btn-ghost-primary" aria-label={t.sourceActionAt(action, location)}>
        <ReportIcon name={isDocument ? 'file' : 'code'} />{showLocation ? <ReportPath value={location} /> : action}<ReportIcon name="chevron" />
      </summary>
      <div className="report-evidence-row">
        {file}{href && <SourceAnchor href={href} location={location} />}
      </div>
      <p className="report-excerpt-label">{t.excerptAt(path ? sourceLocation(path, excerpt.start, excerpt.end) : location)}</p>
      <CodeWindow excerpt={excerpt} location={path ? sourceLocation(path, excerpt.start, excerpt.end) : location} />
    </details> : href ? <SourceAnchor href={href} location={location} action={showLocation ? location : action} /> : file}
  </div>;
}

export function PinnedSourceLink({ path, start, end, fallback }: { path: string; start: number; end: number; fallback?: string }) {
  const base = useContext(ReportSourceContext);
  const href = sourceEvidenceURL(base, path, start, end);
  return href || fallback ? <EvidenceLocation location={fallback ?? sourceLocation(path, start, end)} href={href} /> : null;
}

export function FindingSourceLink({ evidence, actionLabel }: { evidence: Evidence; actionLabel?: string }) {
  const base = useContext(ReportSourceContext);
  if (!evidence.path || !evidence.lines) return null;
  const { start, end } = evidence.lines;
  const href = ['code', 'document', 'test'].includes(evidence.kind) ? sourceEvidenceURL(base, evidence.path, start, end) : undefined;
  return <EvidenceLocation location={sourceLocation(evidence.path, start, end)} href={href} excerpt={evidence.excerpt} path={evidence.path} kind={evidence.kind} actionLabel={actionLabel} />;
}

export function Citations({ refs, catalog, actionLabel, example, showLocation }: {
  refs: string[]; catalog: Citation[]; actionLabel?: string; example?: DocumentExample & { citationId: string }; showLocation?: boolean;
}) {
  const base = useContext(ReportSourceContext);
  const evidence = new Map<string, Citation>();
  for (const e of catalog) {
    if (!refs.includes(e.id)) continue;
    const key = JSON.stringify([e.path, e.line, e.end, e.kind]);
    if (!evidence.has(key) || e.id === example?.citationId || e.excerpt && evidence.get(key)?.id !== example?.citationId) evidence.set(key, e);
  }
  return <>{Array.from(evidence.values(), e => {
    const location = sourceLocation(e.path, e.line, e.end);
    const href = sourceEvidenceURL(base, e.path, e.line, e.end);
    return <EvidenceLocation key={e.id} location={location} href={href} excerpt={e.excerpt} path={e.path} kind={e.kind}
      actionLabel={actionLabel} showLocation={showLocation} example={example?.citationId === e.id ? example : undefined} />;
  })}</>;
}

export function sourceLocation(path: string, start: number, end: number) {
  return `${path}:${start === end ? start : `${start}–${end}`}`;
}

export function WorkflowReading({ workflow, catalog }: { workflow: RelatedWorkflow; catalog: Citation[] }) {
  const labels = t.workflow;
  return <details className="report-workflow-reading">
    <summary>{labels.heading}</summary>
    <p>{labels.context}</p>
    <ul>{(['read', 'change', 'test'] as const).map(role => <li key={role}>
      <strong>{labels[role]}</strong>
      <Citations refs={[workflow[role]]} catalog={catalog} />
    </li>)}</ul>
  </details>;
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
export function FindingCard({ f, badge, history, showCategory, detailGroup }: { f: Finding; badge?: string | null; history?: ReactNode; showCategory?: boolean; detailGroup?: string }) {
  const known = (labels: Record<string, string>, key: string) => Object.hasOwn(labels, key) ? key : 'unknown';
  const location = f.locations[0];
  return (
    <div className="card report-card report-finding">
      <details data-finding-id={f.id} name={detailGroup} className="finding-card">
        <summary>
          <strong>{f.displayTitle ?? f.title}</strong>
          {detailGroup ? <><span className="report-index-status">{label(t.validation, f.validation)} · {label(t.severity, f.severity)}
            {secondaryReviewFinding(f) && <small className="report-item-category">{t.reading.secondary}</small>}</span>
            <span className="report-index-location">{location ? <code>{location}</code> : '—'}</span></> : <span>
            <span className={'badge pill sev sev-' + known(t.severity, f.severity)}>{t.severityBadge(label(t.severity, f.severity))}</span>
            <span className={'badge pill val val-' + known(t.validation, f.validation)}>{label(t.validation, f.validation)}</span>
            {showCategory && <span className="badge pill tone-plain">{label(t.categories, f.category)}</span>}
            {badge && <span className="badge pill tone-plain">{badge}</span>}
            {secondaryReviewFinding(f) && <span className="badge pill tone-plain">{t.reading.secondary}</span>}
            {location && <span className="where">{location}{f.locations.length > 1 && t.morePlaces(f.locations.length - 1)}</span>}
          </span>}
        </summary>
        <FindingDetails f={f} history={history} />
      </details>
    </div>
  );
}

function FindingDetails({ f, history }: { f: Finding; history?: ReactNode }) {
  const ref = useId();
  const source = f.evidence.find(e => e.kind === 'code' && e.excerpt && e.path && e.lines && !f.counter.includes(e.id));
  return (
    <div className="finding-body">
      {source?.excerpt && source.path && source.lines && <ReportPreview path={source.path} start={source.lines.start} end={source.lines.end} excerpt={source.excerpt} title={f.displayTitle ?? f.title} />}
      <dl className="key">
        <dt>{t.impact}</dt>
        <dd>{f.impact}</dd>
        <dt>{t.recommendation}</dt>
        <dd>{f.recommendation}</dd>
      </dl>
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
      <details className="finding-evidence">
        <summary>{t.evidenceToggle(f.evidence.length)}</summary>
        {f.claims.map((claim, i) => (
          <p key={i}>
            <strong>{claim.kind === 'observed' ? t.observed : t.inferred} · </strong>
            {claim.assertion}
            {claim.evidence.map((id, index) => <button key={id} type="button" className="btn btn-sm btn-ghost-primary report-ref"
              aria-controls={`${ref}-${id}`} onClick={() => document.getElementById(`${ref}-${id}`)?.focus()}>{t.ref(index + 1)}</button>)}
          </p>
        ))}
        <h6>{t.evidence}</h6>
        <ul>
          {f.evidence.map((e) => (
            <li key={e.id} id={`${ref}-${e.id}`} tabIndex={-1}>
              {f.counter.includes(e.id) && <strong>{t.counter} · </strong>}
              {e.description}
              <FindingSourceLink evidence={e} />
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
export function FindingList({ findings, history, empty, reveal, groupByCategory = true, inspect = false }: { findings: Finding[]; history?: ReportHistory; empty: string; reveal?: { id: string }; groupByCategory?: boolean; inspect?: boolean }) {
  const detailGroup = useId();
  const [filter, setFilter] = useState('');
  const [showHidden, setShowHidden] = useState(false);
  const [dismissed, setDismissed] = useState<{ id: string }>();
  const revealedID = reveal !== dismissed ? reveal?.id : undefined;
  const comparison = history?.load.state === 'ready' ? history.load.value : null;
  const statuses = new Map(comparison?.findings.map((s) => [s.findingID, s]));
  const entries = new Map(comparison?.entries.map((e) => [e.fingerprint, e]));
  const hidden = findings.filter((f) => HIDDEN.has(statuses.get(f.id)?.current ?? 'open'));
  const hiddenCount = hidden.filter(f => f.id !== revealedID).length;
  const grouped = new Map<string, Finding[]>();
  for (const item of findings) {
    if (!showHidden && hidden.includes(item) && item.id !== revealedID) continue;
    if (
      filter && item.id !== revealedID &&
      !(item.title + ' ' + item.impact + ' ' + label(t.categories, item.category))
        .toLocaleLowerCase()
        .includes(filter.trim().toLocaleLowerCase())
    )
      continue;
    const category = groupByCategory ? item.category : '';
    const group = grouped.get(category);
    if (group) group.push(item);
    else grouped.set(category, [item]);
  }
  return (
    <>
      {findings.length > 1 && (
        <label className="field">
          {t.filter}
          <input type="search" value={filter} onChange={e => { setFilter(e.target.value); setDismissed(reveal); }} />
        </label>
      )}
      {hidden.length > 0 && (
        <>
          <label>
            <input type="checkbox" checked={showHidden} onChange={e => { setShowHidden(e.target.checked); setDismissed(reveal); }} /> {ko.history.hiddenFilter(hidden.length)}
          </label>
          {!showHidden && hiddenCount > 0 && <p className="small">{ko.history.hiddenNote(hiddenCount)}</p>}
        </>
      )}
      {grouped.size === 0 && (filter ? <p>{t.noMatch}</p> : hidden.length === 0 && <p>{empty}</p>)}
      {Array.from(grouped, ([category, items]) => {
        const historyFor = (f: Finding) => {
          const status = statuses.get(f.id);
          return comparison && status && history && <FindingHistory api={history.api} comparison={comparison} status={status}
            entry={status.fingerprint ? entries.get(status.fingerprint) : undefined} onChanged={history.reload} />;
        };
        const card = (f: Finding) => <FindingCard key={f.id} f={f} badge={findingBadge(statuses.get(f.id))} detailGroup={groupByCategory ? undefined : detailGroup}
          history={historyFor(f)} />;
        if (inspect) return <ReportFindingWorkbench key={category} findings={items} reveal={revealedID ? reveal : undefined}
          detail={f => <FindingDetails key={f.id} f={f} history={historyFor(f)} />} />;
        if (!groupByCategory) return <div className="table report-detail-index report-finding-index" key={category}><table>
          <thead><tr><th scope="col">{t.reading.detailItem}</th><th scope="col">{t.sections.col.status}</th><th scope="col">{t.sections.col.location}</th></tr></thead>
          <tbody>{items.map(f => <tr key={f.id}><td colSpan={3}>{card(f)}</td></tr>)}</tbody>
        </table></div>;
        return (
        <section key={category}>
          {category && <h5>
            {label(t.categories, category)} ({items.length})
          </h5>}
          {items.map(card)}
        </section>
      );})}
    </>
  );
}

// ReportCounts presents the counts stored at publication.
export function ReportCounts({ report }: { report: AnalysisReport }) {
  const labels = report.sections ? { ...t.sectionedCounts, ...t.counts, raw: t.sectionedCounts.raw } : t.counts;
  return (
    <dl className="report-counts">
      {Object.entries(labels).map(([key, name]) => (
        <div key={key}>
          <dt>{name}</dt>
          <dd>{report.counts[key as keyof AnalysisReport['counts']]}</dd>
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
            {label(t.roles, c.role)}{c.purpose ? ` (${label(t.callPurposes, c.purpose)})` : ''} · {c.model || t.unknown} · OpenCode {c.version || t.unknown} · {label(t.outcomes, c.outcome)} · {t.pack} <code>{c.pack.slice(0, 12)}</code>
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
    <section className="card report-card exports">
      <div className="card-body">
        <h5 id="report-exports" tabIndex={-1}>{t.exports}</h5>
        <label className="inline">
          <input type="checkbox" checked={includeCode} onChange={(e) => setIncludeCode(e.target.checked)} /> {t.include}
        </label>
        <div className="actions">
          {['json', 'markdown', 'html'].map((format) => (
            <a key={format} className="btn btn-sm btn-ghost-primary report-card-action" download href={reportURL(report.runID, format, includeCode, report.revision)}>
              {format.toUpperCase()}
            </a>
          ))}
          {['markdown', 'html'].map((format) => (
            <a key={format + '-full'} className="btn btn-sm btn-ghost-primary report-card-action" download href={reportURL(report.runID, format, includeCode, report.revision, 'full')}>
              {t.exportFull(format.toUpperCase())}
            </a>
          ))}
        </div>
        <p className="small">{t.exportNote}</p>
        <details className="report-notices">
          <summary>{t.reading.notices}</summary>
          <ul>
            <li><a className="btn btn-sm btn-ghost-primary" href={tablerLicense} download="LICENSE.tabler-core.txt">Tabler Core (MIT)</a></li>
            <li><a className="btn btn-sm btn-ghost-primary" href={iconsLicense} download="LICENSE.tabler-icons.txt">Tabler Icons (MIT)</a></li>
            <li><a className="btn btn-sm btn-ghost-primary" href={bootstrapLicense} download="LICENSE.bootstrap.txt">Bootstrap (MIT)</a></li>
            <li><a className="btn btn-sm btn-ghost-primary" href={fontLicense} download="LICENSE.noto-sans-kr.txt">Noto Sans KR (OFL)</a></li>
          </ul>
        </details>
      </div>
    </section>
  );
}

