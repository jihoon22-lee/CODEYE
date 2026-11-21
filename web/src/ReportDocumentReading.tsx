import { useContext, useMemo, type MouseEvent } from 'react';
import { sourceEvidenceURL } from './analysisReport';
import type { Sections } from './parseReportSections';
import { reportKo as t } from './locales/report-ko';
import { Citations, CodeWindow, PinnedSourceLink, ReportPreview, ReportSourceContext, WorkflowReading, label, sourceLocation } from './ReportParts';
import { ReportIcon } from './ReportLayout';
import { focusReportSearchLocation } from './ReportSearch';
import { documentComparisonHeading, documentReadingGroups, type DocumentReading } from './reportDocumentGroups';
import { primaryCitation, secondaryReviewScope } from './reportGroups';

const s = t.sections;
const r = t.documentReading;

export function jumpToReading(event: MouseEvent<HTMLAnchorElement>, id: string) {
  const target = document.getElementById(id);
  if (!target || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
  event.preventDefault();
  for (let parent = target.parentElement; parent; parent = parent.parentElement) {
    if (parent instanceof HTMLDetailsElement) parent.open = true;
  }
  focusReportSearchLocation(target);
}

function readingHeading(row: DocumentReading, includesCode: boolean) {
  switch (row.kind) {
    case 'comparison': return documentComparisonHeading(row.value, row.index, includesCode);
    case 'quality': return label(t.assessment, row.value.aspect);
    case 'contract': return includesCode ? row.value.statement : r.withheldContract(row.index + 1);
    case 'conflict': return includesCode ? row.value.summary : s.conflicts;
    case 'question': return includesCode ? row.value.question : s.questions;
    case 'note': return includesCode ? row.value.note : s.instructionNotes;
  }
}

function readingType(row: DocumentReading) {
  switch (row.kind) {
    case 'contract': return s.contracts;
    case 'conflict': return s.conflicts;
    case 'question': return s.questions;
    case 'note': return s.instructionNotes;
    default: return undefined;
  }
}

export function ReportDocumentReading({ documents, includesCode, id, comparisonId }: {
  documents: Sections['documents']; includesCode: boolean; id: string; comparisonId: (index: number) => string;
}) {
  const { groups, citations, sourceByRef } = useMemo(() => documentReadingGroups(documents, includesCode), [documents, includesCode]);
  const catalog = includesCode ? documents.citations : documents.citations.map(c => ({ ...c, excerpt: null }));
  const sourceBase = useContext(ReportSourceContext);
  const readingId = (row: DocumentReading) => row.kind === 'comparison' ? comparisonId(row.index) : `${id}-document-${row.kind}-${row.index + 1}`;
  const sourceId = (index: number) => `${id}-document-source-${index}`;
  const groupId = (index: number) => `${id}-document-path-${index + 1}`;
  const groupTitle = (path: string) => path || r.unlocated;
  const citationsByRef = (refs: string[]) => {
    const locations = new Map<string, { id: string; anchor?: string; href?: string; coverage?: string }>();
    const otherRefs: string[] = [];
    for (const ref of refs) {
      const citation = citations.get(ref);
      if (citation?.kind !== 'document') {
        otherRefs.push(ref);
        continue;
      }
      const location = sourceLocation(citation.path, citation.line, citation.end);
      const source = sourceByRef.get(ref);
      if (!locations.has(location) || source && !locations.get(location)?.anchor) {
        const excerpt = includesCode ? citation.excerpt : undefined;
        locations.set(location, {
          id: ref, anchor: source && sourceId(source.index),
          href: sourceEvidenceURL(includesCode ? sourceBase : undefined, citation.path, citation.line, citation.end),
          coverage: excerpt && (excerpt.start > citation.line || excerpt.end < citation.end)
            ? r.sourceCoverage(r.lineRange(citation.line, citation.end), r.lineRange(excerpt.start, excerpt.end))
            : undefined,
        });
      }
    }
    return <>
      {locations.size > 0 && <ul className="report-reading-references">
        {Array.from(locations, ([location, link]) => <li key={link.id}>
          {link.anchor ? <a href={'#' + link.anchor} onClick={e => jumpToReading(e, link.anchor!)}>
            <code>{location}</code>
          </a> : link.href ? <a href={link.href} target="_blank" rel="noopener noreferrer"><code>{location}</code></a> :
            <code>{location}</code>}
          {link.anchor && link.href && <a className="report-reading-source-link" href={link.href} target="_blank" rel="noopener noreferrer"
            aria-label={t.sourceActionAt(t.openSource, location)} title={t.openSource}><ReportIcon name="external" /></a>}
          {link.coverage && <small className="report-source-coverage">{link.coverage}</small>}
        </li>)}
      </ul>}
      {otherRefs.length > 0 && <Citations refs={otherRefs} catalog={catalog} />}
    </>;
  };
  if (!groups.length) return null;
  return <div className="report-reading-groups">
    {groups.length > 1 && <nav aria-label={r.files}><ul className="report-reading-index">
      {groups.map(({ path }, i) => <li key={path}><a className="btn btn-sm btn-ghost-primary report-file-chip" href={'#' + groupId(i)} onClick={e => jumpToReading(e, groupId(i))}><code>{groupTitle(path)}</code></a></li>)}
    </ul></nav>}
    {groups.map(({ path, rows, sources }, i) => {
      return <section className="report-reading-group" key={path} aria-labelledby={groupId(i)}>
        <h6 id={groupId(i)} tabIndex={-1}>{path ? <code>{path}</code> : r.unlocated}</h6>
          <div className="table report-detail-index"><table>
            <thead><tr><th scope="col">{t.reading.detailItem}</th><th scope="col">{s.col.status}</th><th scope="col">{s.col.location}</th></tr></thead>
            <tbody>
            {rows.map(({ reading, primary }) => {
              const anchor = readingId(reading);
              const heading = readingHeading(reading, includesCode);
              const value = reading.value;
              if (!primary) return <tr key={anchor}><td colSpan={3}>
                <a href={'#' + anchor} onClick={e => jumpToReading(e, anchor)}>{heading}</a>
                {' '}<small>{r.sharedWith(reading.paths[0])}</small>
              </td></tr>;
              const scope = 'scope' in value ? value.scope : 'appliesTo' in value ? value.appliesTo : undefined;
              const status = reading.kind === 'comparison' ? label(t.reading.comparisonStatus, reading.value.status) :
                reading.kind === 'quality' ? label(t.assessment, reading.value.assessment) : undefined;
              const type = readingType(reading);
              const secondary = reading.kind === 'comparison' && (reading.value.codeRefs.length > 0
                ? reading.value.codeRefs.every(ref => {
                  const citation = citations.get(ref);
                  return !citation || !primaryCitation(citation, 'code');
                })
                : secondaryReviewScope(reading.value.scope));
              const codePreview = includesCode && reading.kind === 'comparison' && reading.value.status !== 'unknown' ?
                reading.value.codeRefs.map(ref => citations.get(ref)).find(c => c?.kind === 'code' && c.excerpt) : undefined;
              const location = reading.refs.map(ref => citations.get(ref)).find(c => c?.path === path);
              const preview = codePreview ?? (includesCode ? location : undefined);
              const statement = reading.kind === 'comparison' || reading.kind === 'quality' ? reading.value.statement : undefined;
              const category = reading.kind === 'comparison' ? t.comparisonsTitle : reading.kind === 'quality' ? t.qualityTitle : type;
              return <tr key={anchor} className="report-reading-row"><td colSpan={3}>
                <details name={`${id}-document-reading`} className="report-result-details">
                  <summary><h6 id={anchor} tabIndex={-1}>{heading}</h6>
                    <span className="report-index-status">{(status || type && includesCode) && <span className="badge report-reading-status">{status || type}</span>}
                      {category && category !== type && <small className="report-item-category">{category}</small>}
                      {secondary && <span className="badge report-reading-status">{t.reading.secondary}</span>}</span>
                    <span className="report-index-location">{location ? t.documentReading.lineRange(location.line, location.end) : '—'}</span>
                  </summary>
                {preview?.excerpt && <ReportPreview path={preview.path} start={preview.line} end={preview.end} excerpt={preview.excerpt} title={statement} />}
                {(scope && scope !== '.') && <p className="report-card-meta">{t.scopeTitle} <code>{scope}</code></p>}
                {reading.kind === 'comparison' && <>
                  {reading.value.followup && <small>{t.reading.followedContract}</small>}
                  {includesCode && <p>{reading.value.reason}</p>}
                  {citationsByRef(reading.refs)}
                  <div className="reading-refs">
                    <Citations refs={reading.value.codeRefs.filter(ref => !reading.value.additional?.codeRefs.includes(ref))} catalog={catalog} showLocation />
                  </div>
                  {reading.value.additional && <>
                    <p><strong>{t.reading.additionalObservation}</strong>{includesCode && <> {reading.value.additional.reason}</>}</p>
                    <Citations refs={reading.value.additional.codeRefs} catalog={catalog} showLocation />
                  </>}
                  {reading.value.workflow && <WorkflowReading workflow={reading.value.workflow} catalog={catalog} />}
                </>}
                {reading.kind !== 'comparison' && <>
                  {includesCode && reading.kind === 'quality' && <p>{reading.value.statement}</p>}
                  {includesCode && reading.kind === 'conflict' && <p>{reading.value.impact}</p>}
                  {citationsByRef(reading.refs)}
                </>}
                </details></td></tr>;
            })}
            </tbody></table></div>
        {sources.length > 0 && <details className="report-reading-sources">
          <summary>{r.sources}</summary>
          {sources.map(({ citation, index }) => {
            const { excerpt } = citation;
            if (!excerpt) return null;
            const location = sourceLocation(citation.path, excerpt.start, excerpt.end);
            return <div id={sourceId(index)} key={index} tabIndex={-1}>
              <p className="report-excerpt-label">{t.excerptAt(location)}</p>
              <PinnedSourceLink path={citation.path} start={excerpt.start} end={excerpt.end} />
              <CodeWindow excerpt={excerpt} location={location} />
            </div>;
          })}
        </details>}
      </section>;
    })}
  </div>;
}
