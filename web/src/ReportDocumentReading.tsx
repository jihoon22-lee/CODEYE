import { useContext, useMemo, type MouseEvent } from 'react';
import { sourceEvidenceURL } from './analysisReport';
import type { Sections } from './parseReportSections';
import { reportKo as t } from './locales/report-ko';
import { Citations, CodeWindow, PinnedSourceLink, ReportSourceContext, WorkflowReading, label, sourceLocation } from './ReportParts';
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
    const locations = new Map<string, { id: string; anchor?: string; href?: string }>();
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
        locations.set(location, {
          id: ref, anchor: source && sourceId(source.index),
          href: sourceEvidenceURL(includesCode ? sourceBase : undefined, citation.path, citation.line, citation.end),
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
        </li>)}
      </ul>}
      {otherRefs.length > 0 && <Citations refs={otherRefs} catalog={catalog} />}
    </>;
  };
  if (!groups.length) return null;
  return <div className="report-reading-groups">
    <p className="model-note" role="note">{documents.model.performed ? s.model(documents.model.task) : t.modelReading}</p>
    {groups.length > 1 && <nav aria-label={r.files}><ul className="report-reading-index">
      {groups.map(({ path }, i) => <li key={path}><a href={'#' + groupId(i)} onClick={e => jumpToReading(e, groupId(i))}><code>{groupTitle(path)}</code></a></li>)}
    </ul></nav>}
    {groups.map(({ path, rows, sources }, i) => {
      const sections = [
        { title: t.comparisonsTitle, rows: rows.filter(({ reading }) => reading.kind === 'comparison') },
        { title: t.qualityTitle, rows: rows.filter(({ reading }) => reading.kind === 'quality') },
        { title: r.otherNotes, rows: rows.filter(({ reading }) => reading.kind !== 'comparison' && reading.kind !== 'quality') },
      ];
      return <section className="report-reading-group" key={path} aria-labelledby={groupId(i)}>
        <h6 id={groupId(i)} tabIndex={-1}>{path ? <code>{path}</code> : r.unlocated}</h6>
        {sections.filter(section => section.rows.length > 0).map(section => <div className="report-reading-category" key={section.title}>
          <h6>{section.title}</h6>
          <ul className="report-reading-list">
            {section.rows.map(({ reading, primary }) => {
              const anchor = readingId(reading);
              const heading = readingHeading(reading, includesCode);
              const value = reading.value;
              if (!primary) return <li key={anchor}>
                <a href={'#' + anchor} onClick={e => jumpToReading(e, anchor)}>{heading}</a>
                {' '}<small>{r.sharedWith(reading.paths[0])}</small>
              </li>;
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
              return <li key={anchor} className="report-reading-row">
                <h6 id={anchor} tabIndex={-1}>{heading}</h6>
                {status && <span className="badge report-reading-status">{status}</span>}
                {type && includesCode && <span className="badge report-reading-status">{type}</span>}
                {secondary && <span className="badge report-reading-status">{t.reading.secondary}</span>}
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
              </li>;
            })}
          </ul>
        </div>)}
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
