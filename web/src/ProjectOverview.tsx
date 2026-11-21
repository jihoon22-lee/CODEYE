import { useContext, type ReactNode } from 'react';
import { sourceEvidenceURL, type AnalysisReport } from './analysisReport';
import type { Citation, Sections } from './parseReportSections';
import { Citations, FindingSourceLink, PinnedSourceLink, ReportSourceContext, WorkflowReading, label, sourceLocation } from './ReportParts';
import { ReportAction } from './ReportLayout';
import { ReportGlossary } from './ReportGlossary';
import { citedDocumentExample } from './reportExample';
import { matchingDocumentCondition, primaryCitation, projectReadings, relatedStrength, shareTitleSubject } from './reportGroups';
import { maxProjectBehaviorRows, projectBehaviors } from './reportBehaviors';
import { cutLine } from './format';
import { ko } from './locales/ko';
import { reportKo as t } from './locales/report-ko';

const s = t.sections;

type OverviewReading = {
  comparisonIndex?: number;
  comparison?: boolean;
  comparisonExample?: string;
  title: string;
  text?: string;
  documentClaim?: string;
  reasonLabel?: string;
  comparisonRows?: { label: string; text: string; sources?: ReactNode }[];
  condition?: string;
  limitation?: string;
  scope?: string;
  followedContract?: boolean;
  action?: { state: string; title?: string; limitation?: string; source: ReactNode };
  unresolved?: string;
  status?: string;
  model?: boolean;
  next?: { label: string; text: string };
  sources?: ReactNode;
};

function strengthTextKey(text: string) {
  return text.trim().split(/\s+/).join(' ').toLowerCase().replace(/[.。!?！？]+$/, '').trimEnd();
}

function distinctStrengthDetails(titles: string[], ...benefits: string[]) {
  const shown = new Set(['', ...titles.map(strengthTextKey)]);
  return benefits.filter(benefit => {
    const key = strengthTextKey(benefit);
    if (shown.has(key)) return false;
    shown.add(key);
    return true;
  }).join(' ');
}

function selectedReading(report: AnalysisReport, sections: Sections, readings: ReturnType<typeof projectReadings>): OverviewReading {
  const { actions, comparisons, unresolvedComparison, exampleComparison, strengths, followups, diagnostic } = readings;
  const { documents, codeReview } = sections;
  const included = report.includesCode !== false;
  const source = (catalog: Citation[], refs: string[], kind: 'code' | 'document') =>
    catalog.find(c => refs.includes(c.id) && primaryCitation(c, kind))?.id;
  const action = actions[0];
  const actionStatus = action?.finding.validation === 'supported' ? t.reading.supported : t.reading.reproduced;
  const readComparison = (comparison: (typeof comparisons)[number]): OverviewReading => {
    const unknown = comparison.status === 'unknown';
    const newCode = comparison.additional && source(documents.citations, comparison.additional.codeRefs, 'code');
    const documentRef = source(documents.citations, comparison.documentRefs, 'document');
    const codeRef = newCode ?? source(documents.citations, comparison.codeRefs, 'code');
    const newObservation = newCode && comparison.additional?.reason.trim() ? comparison.additional : undefined;
    const comparisonText = newObservation?.reason ?? comparison.reason;
    const codeRefs = newObservation?.codeRefs ?? comparison.codeRefs;
    const codeCatalog = codeRefs.flatMap(id => documents.citations.find(e => e.id === id) ?? []);
    const paired = included && unknown && comparison.statement.trim() !== '' && comparisonText.trim() !== '';
    const condition = included && comparison.status === 'match'
      ? matchingDocumentCondition(comparison, documents.comparisons, documents.citations) : undefined;
    const conditionRef = condition && source(documents.citations, condition.documentRefs, 'document');
    const findExample = (statement: string, citationId: string | undefined) => {
      if (!included || !citationId) return;
      const example = citedDocumentExample(statement, documents.citations.find(c => c.id === citationId));
      if (example) return { ...example, citationId };
    };
    const example = findExample(comparison.statement, documentRef) ??
      (condition && !newCode ? findExample(condition.statement, conditionRef) : undefined);
    return {
      comparisonIndex: documents.comparisons.indexOf(comparison),
      comparison: true,
      title: !included ? t.reading.omitted : paired ? t.reading.documentComparison : unknown ? t.reading.documentClaim : comparison.statement,
      model: included ? !unknown || Boolean(newObservation) : true,
      documentClaim: included && unknown && !paired ? comparison.statement : undefined,
      text: included && !paired ? comparisonText : undefined,
      comparisonRows: paired ? [
        { label: t.reading.comparisonDocument, text: comparison.statement,
          sources: documentRef && <Citations catalog={documents.citations} refs={[documentRef]} example={example} /> },
        { label: t.reading.comparisonCode, text: comparisonText,
          sources: codeCatalog.length > 0 ? <Citations catalog={codeCatalog} refs={codeRefs} /> : undefined },
      ] : undefined,
      condition: condition?.statement,
      scope: comparison.scope !== '.' ? comparison.scope : undefined,
      followedContract: comparison.followup,
      reasonLabel: newCode ? t.reading.additionalObservation : unknown ? t.reading.unconfirmedReason : undefined,
      status: paired && !newObservation ? t.reading.codeEvidenceMissing : label(t.reading.comparisonStatus, comparison.status),
      sources: !paired && <Citations catalog={documents.citations} refs={[
        documentRef,
        ...(newCode ? codeRefs : [codeRef]),
        conditionRef,
      ].filter((id): id is string => id !== undefined)} example={example} />,
      action: action && ['critical', 'high'].includes(action.finding.severity) ? {
        state: actionStatus,
        title: included ? action.finding.title : undefined,
        limitation: action.limitation,
        source: <FindingSourceLink evidence={action.source} />,
      } : undefined,
    };
  };
  if (comparisons[0]) return readComparison(comparisons[0]);
  const example = exampleComparison && {
    comparisonIndex: documents.comparisons.indexOf(exampleComparison),
    comparisonExample: included ? exampleComparison.statement : undefined,
  };
  if (action) return {
    ...example,
    title: included ? action.finding.title : t.reading.omitted,
    model: true,
    text: included ? action.finding.impact : undefined,
    limitation: action.limitation,
    status: actionStatus,
    next: included ? { label: action.limitation ? t.reading.conditionalAction : t.reading.nextAction, text: action.finding.recommendation } : undefined,
    sources: <FindingSourceLink evidence={action.source} />,
  };
  const followup = followups[0];
  if (!unresolvedComparison && followup?.next) {
    const confirmed = included ? relatedStrength(followup, strengths, codeReview.citations) : undefined;
    const strength = confirmed?.strength;
    const text = strength ? distinctStrengthDetails([strength.title], strength.benefit)
      : followup.condition && !followup.limitation ? `${t.conditions} · ${followup.condition}` : undefined;
    return {
      title: included ? strength?.title ?? followup.finding.title : t.reading.omitted,
      model: true,
      text: included ? text : undefined,
      limitation: followup.limitation,
      scope: strength?.scope !== '.' ? strength?.scope : undefined,
      unresolved: confirmed ? followup.finding.title : undefined,
      status: confirmed ? t.reading.recordedStrength : label(t.validation, followup.finding.validation),
      next: included ? { label: t.reading.nextCheck, text: followup.next } : undefined,
      sources: <>
        {confirmed && <Citations catalog={[confirmed.citation]} refs={[confirmed.citation.id]} actionLabel={t.reading.confirmedCode} />}
        <FindingSourceLink evidence={followup.source} actionLabel={confirmed ? t.reading.followupCode : undefined} />
      </>,
    };
  }
  const strength = strengths[0];
  if (strength) {
    const first = codeReview.citations.find(c => strength.refs.includes(c.id) && primaryCitation(c, 'code'))!;
    const related = strengths.slice(1).find(row => strengthTextKey(row.title) !== strengthTextKey(strength.title) &&
      strengthTextKey(row.benefit) !== strengthTextKey(strength.benefit) && shareTitleSubject(strength.title, row.title) &&
      codeReview.citations.some(c => row.refs.includes(c.id) && primaryCitation(c, 'code') && c.path === first.path &&
        ((first.line <= c.line && c.end <= first.end) || (c.line <= first.line && first.end <= c.end))));
    const proof = related && codeReview.citations.find(c => related.refs.includes(c.id) && primaryCitation(c, 'code') &&
      c.path === first.path && c.line <= first.line && first.end <= c.end);
    const [title, text] = related
      ? [t.reading.relatedStrengths(strength.title, related.title), distinctStrengthDetails([strength.title, related.title], strength.benefit, related.benefit)]
      : [strength.title, distinctStrengthDetails([strength.title], strength.benefit)];
    return {
      ...example,
      title: included ? title : t.reading.omitted,
      model: true,
      text: included ? text : undefined,
      scope: strength.scope !== '.' && (!related || related.scope === strength.scope) ? strength.scope : undefined,
      status: t.reading.recordedStrength,
      sources: <Citations catalog={codeReview.citations} refs={[(proof || first).id]} />,
    };
  }
  if (unresolvedComparison) return readComparison(unresolvedComparison);
  if (diagnostic?.location) return {
    title: diagnostic.message || diagnostic.rule,
    status: t.reading.staticStatus,
    sources: <PinnedSourceLink {...diagnostic.location}
      fallback={sourceLocation(diagnostic.location.path, diagnostic.location.start, diagnostic.location.end)} />,
  };
  const reason = report.aiReviewReasons.map(code => [t.reasons, s.modelReasons, ko.stages.reasonLabels]
    .find(labels => Object.hasOwn(labels, code))?.[code]).find(Boolean);
  return { title: t.reading.noFindings, text: report.aiReview === 'not_requested' ? t.reading.reviewNotRequested : reason };
}

// Only pairs of single backticks format code. Text and unmatched delimiters
// stay as React children, including any markup-like characters.
function inlineComparisonCode(text: string): ReactNode[] {
  const parts: ReactNode[] = [];
  let start = 0;
  let open = -1;
  for (const run of text.matchAll(/`+/g)) {
    if (run[0].length !== 1) continue;
    const at = run.index;
    if (open < 0) {
      open = at;
      continue;
    }
    parts.push(text.slice(start, open), <code key={open}>{text.slice(open + 1, at)}</code>);
    start = at + 1;
    open = -1;
  }
  parts.push(text.slice(start));
  return parts;
}

function ProjectBehaviors({ rows, catalog, openDocuments }: {
  rows: ReturnType<typeof projectBehaviors>['rows']; catalog: Citation[]; openDocuments: (index?: number) => void;
}) {
  const base = useContext(ReportSourceContext);
  const { heading, documented, recordedComparison, previewSource, openComparison } = t.behavior;
  return <section className="card report-card report-behaviors" aria-labelledby="report-behaviors">
    <div className="card-body">
      <h6 id="report-behaviors">{heading}</h6>
      {rows.map((row, i) => {
        const claim = inlineComparisonCode(row.statement);
        const preview = row.preview;
        const previewLink = preview && sourceEvidenceURL(base, preview.path, preview.line, preview.end);
        const place = preview && <code>{sourceLocation(preview.path, preview.line, preview.end)}</code>;
        const evidence = <>
          <p className="report-behavior-status">{documented} · {label(t.reading.comparisonStatus, row.status)}</p>
          {preview && <div className="report-behavior-preview">
            <p>{previewSource} · {previewLink ? <a href={previewLink} target="_blank" rel="noopener noreferrer">{place}</a> : place}</p>
            <pre tabIndex={0}><code>{preview.excerpt?.text}</code></pre>
          </div>}
          {row.additional && <p className="report-behavior-explanation">
            <strong>{t.reading.additionalObservation}</strong>{' '}{inlineComparisonCode(row.additional)}
          </p>}
          {row.explanation.trim() && <p className="report-behavior-explanation">
            <strong>{recordedComparison}</strong>{' '}{inlineComparisonCode(row.explanation)}
          </p>}
          <div className="report-behavior-links">
            <Citations catalog={[row.document]} refs={[row.document.id]} />
            {row.code && <Citations catalog={[row.code]} refs={[row.code.id]} actionLabel={preview ? t.behavior.fullSource : undefined} />}
            <ReportAction onClick={() => openDocuments(row.index)}>{openComparison}</ReportAction>
          </div>
          {row.workflow && <WorkflowReading workflow={row.workflow} catalog={catalog} />}
        </>;
        return i === 0
          ? <article key={row.index} className="report-behavior"><h6>{claim}</h6>{evidence}</article>
          : <details key={row.index} className="report-behavior"><summary>{claim}</summary>{evidence}</details>;
      })}
    </div>
  </section>;
}

export function ProjectOverview({ report, sections, behaviors, openDocuments }: {
  report: AnalysisReport; sections: Sections; behaviors: ReturnType<typeof projectBehaviors>; openDocuments: (index?: number) => void;
}) {
  const readings = projectReadings(report, sections);
  const { status, title, text, documentClaim, reasonLabel, comparisonRows, condition, limitation, scope, comparisonIndex,
    comparisonExample, comparison, followedContract, action, unresolved, next, sources, model } =
    selectedReading(report, sections, readings);
  const priority = Boolean(action) || status === t.reading.comparisonStatus.mismatch || status === t.reading.comparisonStatus.partial ||
    status === t.reading.supported || status === t.reading.reproduced;
  const rows = (priority && comparison ? behaviors.rows.filter(row => row.index !== comparisonIndex) : behaviors.rows)
    .slice(0, maxProjectBehaviorRows);
  const showExample = comparisonExample && !rows.some(row => row.statement === comparisonExample) ? comparisonExample : undefined;
  const heading = cutLine(title, 165);
  const detail = text !== title ? cutLine(text ?? null, 205) : '';
  const claim = cutLine(documentClaim ?? null, 205);
  const boundary = cutLine(unresolved ?? null, 165);
  const nextStep = cutLine(next?.text ?? null, 165);
  const limitationText = cutLine(limitation ?? null, 205);
  const comparisonCount = sections.documents.comparisons.length;
  const decision = (
    <section className="card report-card report-executive" aria-labelledby="report-judgment">
      <div className="card-body">
        {status && <div className="report-executive-status"><strong>{status}</strong>{model && <span>{t.modelReading}</span>}</div>}
        <h6 className="card-title" id="report-judgment">{heading}</h6>
        {comparisonRows && <dl className="report-comparison-rows">
          {comparisonRows.map(row => <div key={row.label}><dt>{row.label}</dt><dd>
            <p>{inlineComparisonCode(row.text)}</p>
            {row.sources && <div className="report-executive-sources">{row.sources}</div>}
          </dd></div>)}
        </dl>}
        {claim && <p className="report-card-copy">{claim}</p>}
        {detail && <p className="report-card-copy">{reasonLabel && <strong>{reasonLabel}{' '}</strong>}{detail}</p>}
        {condition && <p className="report-card-copy"><strong>{t.reading.option}</strong> {condition}</p>}
        {boundary && <p className="report-executive-next"><strong>{t.reading.unresolvedBehavior}</strong> {boundary}</p>}
        {limitationText && <p className="report-card-copy"><strong>{t.reading.findingLimitation}</strong> {limitationText}</p>}
        {next && nextStep && <p className="report-executive-next"><strong>{next.label}</strong> {nextStep}</p>}
        {scope && <p className="report-card-meta">{t.scopeTitle} <code>{scope}</code></p>}
        {followedContract && <p className="report-card-meta">{t.reading.followedContract}</p>}
        {sources && <div className="report-executive-sources">{sources}</div>}
        {action && <>
          <p className="report-executive-next"><strong>{action.limitation ? t.reading.priorityReview : t.reading.priorityAction}</strong> {action.state}{action.title && <> · {cutLine(action.title, 165)}</>}</p>
          {action.limitation && <p className="report-card-copy"><strong>{t.reading.findingLimitation}</strong> {cutLine(action.limitation, 205)}</p>}
          <div className="report-executive-sources">{action.source}</div>
        </>}
        <div className="report-document-assessment">
          <div className="report-document-assessment-header">
            <strong>{comparisonCount ? t.reading.documentAssessmentTotal(comparisonCount) : t.reading.documentAssessment}</strong>
            {readings.comparisonCounts.length > 0
              ? <dl>{readings.comparisonCounts.map(({status, count}) => <div key={status}>
                <dt>{label(t.reading.comparisonCount, status)}</dt><dd data-comparison-status={status}>{t.reading.comparisonItems(count)}</dd>
              </div>)}</dl>
              : <p>{t.reading.noDocumentAssessment}</p>}
          </div>
          {showExample && <p className="report-document-assessment-example">
            <strong>{t.reading.documentClaim}</strong>{' '}{inlineComparisonCode(cutLine(showExample, 165))}
          </p>}
          <ReportAction onClick={() => openDocuments(comparisonIndex)}>{t.reading.openDocumentAnalysis}</ReportAction>
        </div>
        <ReportGlossary text={[heading, claim, detail, condition ?? '', boundary, limitationText, nextStep, ...(comparisonRows?.map(row => row.text) ?? [])]}
          hasSources={Boolean(sources || action || comparisonRows?.some(row => row.sources))} />
      </div>
    </section>
  );
  if (rows.length === 0) return decision;
  return <>
    {priority && decision}
    <ProjectBehaviors rows={rows} catalog={sections.documents.citations} openDocuments={openDocuments} />
    {!priority && <details className="report-section report-behavior-assessment">
      <summary>{t.reading.documentAssessment}</summary>
      {decision}
    </details>}
  </>;
}
