import type { ReactNode } from 'react';
import type { AnalysisReport, Finding, Observation } from './analysisReport';
import type { DiagnosticTriage, Sections } from './parseReportSections';
import { Citations, FindingSourceLink, PinnedSourceLink, ReportPath, ReportPreview, label, sourceLocation } from './ReportParts';
import { ReportIcon } from './ReportLayout';
import { inlineComparisonCode } from './ProjectOverview';
import { actionPlan, COVERAGE_ATTENTION, DIAGNOSTIC_PLACES, MISMATCH_SHOWN, NOTICE_ATTENTION } from './reportActions';
import { overviewFacts, regularStaticLanguages, ruleGroups, severityOf, type RuleGroup } from './reportGroups';
import { reportKo as t, diagnosticDescription } from './locales/report-ko';
import { reportActionsKo as a } from './locales/report-actions-ko';
import { ko } from './locales/ko';

const tone = (f: Finding) => f.validation === 'supported' || f.validation === 'reproduced' ? 'ready' : 'attention';

function Fold({ id, title, children }: { id?: string; title: string; children: ReactNode }) {
  return <details className="report-more report-action-fold" id={id}>
    <summary><ReportIcon name="chevron" />{title}</summary>
    <div className="report-more-body">{children}</div>
  </details>;
}

function Row({ name, children }: { name: string; children: ReactNode }) {
  return <div><dt>{name}</dt><dd>{children}</dd></div>;
}

function ActionCard({ f, included, openFinding }: { f: Finding; included: boolean; openFinding: (id: string) => void }) {
  const text = (value: string) => included && value.trim() !== '' ? value : '';
  const code = f.evidence.find(e => e.kind === 'code' && e.path && e.lines && !f.counter.includes(e.id));
  const stored = included ? code : undefined;
  const location = code?.path && code.lines ? sourceLocation(code.path, code.lines.start, code.lines.end) : f.locations[0];
  const conditions = f.conditions.filter(c => text(c.statement));
  const impact = text(f.impact);
  const recommendation = text(f.recommendation);
  const checks = f.verification.filter(text);
  const limits = f.limitations.filter(text);
  return <article className="card report-action-card" data-finding-card={f.id} data-severity={f.severity} data-validation={f.validation}>
    <div className="card-body">
      <header className="report-action-head">
        <h6>{f.displayTitle ?? f.title}</h6>
        <p className="report-action-badges">
          <span className="report-status-badge" data-tone={tone(f)}>{label(t.validation, f.validation)}</span>
          <span className="report-action-severity">{a.severity(label(t.severity, f.severity))}</span>
        </p>
      </header>
      {(conditions.length > 0 || impact) && <dl className="report-action-rows">
        {conditions.map((c, i) => <Row key={i} name={Object.hasOwn(a.conditions, c.status) ? a.conditions[c.status] : a.conditions.other}>{c.statement}</Row>)}
        {impact && <Row name={a.impact}>{impact}</Row>}
      </dl>}
      {stored?.excerpt && stored.path && stored.lines
        ? <><ReportPreview path={stored.path} start={stored.lines.start} end={stored.lines.end} excerpt={stored.excerpt} title={f.displayTitle ?? f.title} />
          <PinnedSourceLink path={stored.path} start={stored.lines.start} end={stored.lines.end} fallback={undefined} /></>
        : code && included ? <FindingSourceLink evidence={code} />
        : location && <p className="report-action-location"><ReportPath value={location} /></p>}
      {(recommendation || checks.length > 0 || limits.length > 0) && <dl className="report-action-rows">
        {recommendation && <Row name={a.recommendation}>{recommendation}</Row>}
        {checks.map((c, i) => <Row key={i} name={a.verification}>{c}</Row>)}
        {limits.length > 0 && <Row name={a.limitation}>{limits.join(' ')}</Row>}
      </dl>}
      <button type="button" className="btn btn-sm btn-ghost-primary report-card-action" onClick={() => openFinding(f.id)}>
        {a.evidenceLink(f.evidence.length - f.counter.length, f.counter.length)}<ReportIcon name="jump" />
      </button>
    </div>
  </article>;
}

type Comparison = Sections['documents']['comparisons'][number];

function MismatchCard({ index, comparison: c, sections, included, openComparison }: {
  index: number; comparison: Comparison; sections: Sections; included: boolean; openComparison: (index: number) => void;
}) {
  const citations = sections.documents.citations;
  return <article className="card report-action-card report-mismatch-card" data-comparison={c.status}>
    <div className="card-body">
      <header className="report-action-head">
        <h6>{included && c.statement.trim() ? c.statement : a.withheldBehavior(index + 1)}</h6>
        <p className="report-action-badges">
          <span className="report-status-badge" data-tone={c.status === 'mismatch' ? 'attention' : 'partial'}>{label(t.reading.comparisonStatus, c.status)}</span>
          <small className="report-group-note" role="note">{a.interpretation}</small>
        </p>
      </header>
      <dl className="report-action-rows">
        {included && c.reason.trim() && <Row name={a.observed}>{inlineComparisonCode(c.reason)}</Row>}
        {c.scope !== '.' && <Row name={a.appliesTo}><code>{c.scope}</code></Row>}
        {c.documentRefs.length > 0 && <Row name={a.documentLocation}><Citations catalog={citations} refs={c.documentRefs} showLocation /></Row>}
        {c.codeRefs.length > 0 && <Row name={a.codeLocation}><Citations catalog={citations} refs={c.codeRefs} showLocation /></Row>}
      </dl>
      <button type="button" className="btn btn-sm btn-ghost-primary report-card-action" onClick={() => openComparison(index)}>
        {a.openComparison}<ReportIcon name="jump" />
      </button>
    </div>
  </article>;
}

function CandidateList({ title, note, findings, included, openFinding }: {
  title: string; note: string; findings: Finding[]; included: boolean; openFinding: (id: string) => void;
}) {
  if (findings.length === 0) return null;
  return <Fold title={title}>
    <p>{note}</p>
    <ul className="report-action-list">{findings.map(f => {
      const next = included ? f.verification.find(v => v.trim()) : undefined;
      return <li key={f.id}>
        <div className="report-action-list-head">
          <strong>{f.displayTitle ?? f.title}</strong>
          <span className="report-status-badge" data-tone={tone(f)}>{label(t.validation, f.validation)}</span>
          <span className="report-action-severity">{a.severity(label(t.severity, f.severity))}</span>
        </div>
        {f.locations[0] && <ReportPath value={f.locations[0]} />}
        {next && <p><strong>{a.verification}</strong> {next}</p>}
        <button type="button" className="btn btn-sm btn-ghost-primary report-action-link" onClick={() => openFinding(f.id)}>{a.openFinding}</button>
      </li>;
    })}</ul>
  </Fold>;
}

// TRIAGE_BUCKETS is the order of the triage results in the diagnostics fold;
// '' holds the diagnostics no triage group classified.
const TRIAGE_BUCKETS = ['attention', 'refuted', 'harmless', ''] as const;

function Diagnostics({ report, sections, openFinding }: { report: AnalysisReport; sections: Sections; openFinding: (id: string) => void }) {
  const byID = new Map<string, Observation>(report.observations.map(o => [o.id, o]));
  const staticIDs = sections.staticAnalysis.languages.flatMap(language => language.tools.flatMap(tool => tool.findings));
  const severity = severityOf(report.findings, staticIDs);
  // merged is the rule groups across languages of the diagnostics keep accepts.
  const merged = (keep: (id: string) => boolean = () => true) => {
    const groups = new Map<string, RuleGroup>();
    for (const language of regularStaticLanguages(sections)) {
      const tools = language.tools.map(tool => ({ ...tool, observations: tool.observations.filter(keep) }));
      for (const g of ruleGroups(tools, byID, severity)) {
        const key = g.analyzer + '\u0000' + g.rule;
        const known = groups.get(key);
        if (!known) { groups.set(key, { ...g, primary: [...g.primary], secondary: [...g.secondary], observations: [...g.observations] }); continue; }
        known.count += g.count;
        known.primary.push(...g.primary);
        known.secondary.push(...g.secondary);
        known.observations.push(...g.observations);
      }
    }
    return [...groups.values()];
  };
  const rows = merged();
  if (rows.length === 0) return null;
  const total = rows.reduce((sum, g) => sum + g.count, 0);
  const tools = new Set(rows.map(g => g.analyzer));
  const unplaced = rows.some(g => [...g.primary, ...g.secondary].includes('—'));
  const triage = sections.codeReview.triage;
  if (!triage) {
    return <Fold id="report-tool-diagnostics" title={a.diagnostics(total, rows.length, tools.size)}>
      <p>{a.diagnosticsNote}</p>
      {unplaced && <p>{a.diagnosticsUnplaced}</p>}
      <DiagnosticRows rows={rows} triage={null} report={report} openFinding={openFinding} />
    </Fold>;
  }
  const bucket = new Map<string, string>(triage.groups.flatMap(g => g.observations.map(id => [id, g.bucket] as [string, string])));
  // What was decided (problem or harmless) apart from what the model left unclear.
  const answered = (unclear: boolean) => triage.groups.filter(g => (g.classification === 'unclear') === unclear).reduce((n, g) => n + g.observations.length, 0);
  return <Fold id="report-tool-diagnostics" title={a.diagnosticsTriaged(total, answered(false), answered(true), rows.length, tools.size)}>
    <p>{a.diagnosticsTriageNote}</p>
    {unplaced && <p>{a.diagnosticsUnplaced}</p>}
    {TRIAGE_BUCKETS.map(b => {
      const groups = merged(id => (bucket.get(id) ?? '') === b);
      if (groups.length === 0) return null;
      const name = b || 'not_triaged';
      const n = groups.reduce((sum, g) => sum + g.count, 0);
      return <section key={name} className="report-diag-bucket" data-bucket={name}>
        <h6>{a.diagnosticBucket[name](n)}</h6>
        <p><small>{a.diagnosticBucketNote[name]}{b === '' && triage.interpreted > 0 ? ' ' + a.diagnosticInterpreted(triage.interpreted) : ''}</small></p>
        <DiagnosticRows rows={groups} triage={triage} report={report} openFinding={openFinding} />
      </section>;
    })}
  </Fold>;
}

// DiagnosticRows are the rule rows of the fold, each with the triage results
// of its diagnostics when the run triaged them.
function DiagnosticRows({ rows, triage, report, openFinding }: {
  rows: RuleGroup[]; triage: DiagnosticTriage | null; report: AnalysisReport; openFinding: (id: string) => void;
}) {
  const findings = new Set(report.findings.map(f => f.id));
  return <div className="report-diag-groups">{rows.map(g => {
    const places = [...g.primary, ...g.secondary].map(place => place === '—' ? a.noPlace : place);
    const description = diagnosticDescription(g.analyzer, g.rule, g.message);
    const notes = triage ? triage.groups.filter(x => x.observations.some(id => g.observations.includes(id))) : [];
    return <details key={g.analyzer + g.rule} className="report-diag-group">
      <summary>
        <span className="report-diag-name"><code>{g.analyzer}</code> <code>{g.rule}</code></span>
        <span className="report-diag-count">{a.diagnosticCount(g.count)}</span>
        {g.severity && <span className="report-diag-severity">{triage ? a.defaultSeverity(label(t.severity, g.severity)) : label(t.severity, g.severity)}</span>}
        {description && <span className="report-diag-description">{description}</span>}
      </summary>
      {notes.length > 0 && <ul className="report-diag-notes">{notes.map(x => <li key={x.task + x.id} data-classification={x.classification} data-bucket={x.bucket}>
        <strong>{x.bucket === 'refuted' ? a.triageRefuted : a.triageClassification[x.classification]}</strong>{x.reason && ' ' + x.reason}
        {x.locations.map((l, i) => <span key={i}> <code className="report-path-text">{sourceLocation(l.path, l.start, l.end)}</code></span>)}
        {x.finding && findings.has(x.finding) && <> <button type="button" className="report-plain-action" onClick={() => openFinding(x.finding!)}>{a.openFinding}</button></>}
      </li>)}</ul>}
      <ul>{places.slice(0, DIAGNOSTIC_PLACES).map((place, i) => <li key={i}><code className="report-path-text">{place}</code></li>)}</ul>
      {places.length > DIAGNOSTIC_PLACES && <p><small>{a.diagnosticMore(places.length - DIAGNOSTIC_PLACES)}</small></p>}
    </details>;
  })}</div>;
}

// The line under the verdict that says what was not analysed.
export function ReportCoverageNote({ report, sections, openCoverage }: { report: AnalysisReport; sections: Sections; openCoverage: () => void }) {
  const s = t.sections;
  const sentences = sections.attention.filter(x => COVERAGE_ATTENTION.has(x.code))
    .map(x => (s.attentionText[x.code] ?? (() => x.code))(x.count));
  const noModel = report.aiReview !== 'performed' && !sections.attention.some(x => x.code === 'model_review_not_performed');
  if (noModel) sentences.push(report.aiReview === 'partial' ? a.modelPartial : report.aiReview === 'not_requested' ? t.reading.reviewNotRequested : a.modelNotPerformed);
  if (sentences.length === 0) return null;
  return <p className="report-coverage-note" role="note">
    <strong>{a.coverageLead}</strong> {sentences.join(' ')}{' '}
    <button type="button" className="report-plain-action" onClick={openCoverage}>{a.coverageLink}</button>
  </p>;
}

// ReportActions is the opening of the overview: what to fix and why, then the
// documents that disagree with the code, then what still needs checking.
export function ReportActions({ report, sections, isDisposed, openFinding, openComparison, openSection }: {
  report: AnalysisReport; sections: Sections; isDisposed: (f: Finding) => boolean;
  openFinding: (id: string) => void; openComparison: (index: number) => void; openSection: (key: string) => void;
}) {
  const included = report.includesCode !== false;
  const { actions, candidates, secondary, mismatches, assessed } = actionPlan(report, sections, isDisposed);
  const facts = overviewFacts(report, sections);
  const paragraphs: string[] = [];
  let lead: string;
  if (actions.length > 0) {
    lead = a.lead(actions.length);
    if (report.aiReview === 'partial') paragraphs.push(a.leadPartial);
  } else if (!assessed) {
    lead = a.notAssessed;
    paragraphs.push(report.aiReview === 'not_requested' ? t.reading.reviewNotRequested : report.aiReview !== 'performed' ? a.modelNotPerformed : '');
    const reason = report.aiReviewReasons.map(code => [t.reasons, t.sections.modelReasons, ko.stages.reasonLabels]
      .find(labels => Object.hasOwn(labels, code))?.[code]).find(Boolean);
    if (reason && report.aiReview !== 'not_requested') paragraphs.push(reason);
  } else if (sections.codeReview.findings.length > 0 || sections.codeReview.strengths.length > 0 || sections.codeReview.roles.some(role =>
    (role.result === 'accepted' && role.key === 'review.investigate') ||
    ((role.result === 'accepted' || role.result === 'accepted_incomplete') && role.key === 'review.verify')) ||
    sections.codeReview.ledger?.modules.some(module => module.completed > 0)) {
    lead = t.codeReading.none;
    if (facts.scope) paragraphs.push(t.sections.summary.reviewScope(facts.scope.primary, facts.scope.primarySent));
    if (report.aiReview === 'partial') paragraphs.push(a.nonePartial);
    paragraphs.push(a.noneNotAProof);
  } else {
    lead = t.codeReading.noEvidence;
    paragraphs.push(a.notAssessed);
  }
  if (actions.length === 0) {
    if (candidates.length > 0) paragraphs.push(a.othersCandidates(candidates.length));
    if (secondary.length > 0) paragraphs.push(a.othersSecondary(secondary.length));
    if (mismatches.length > 0) paragraphs.push(a.othersMismatch(mismatches.length));
  }
  if (!included) paragraphs.push(a.leadCodeFree);
  const notices = sections.attention.filter(x => NOTICE_ATTENTION.has(x.code));
  return <section className="report-actions" aria-labelledby="report-actions-heading">
    <div className="report-group" data-tone="action">
      <header className="report-group-title">
        <span className="report-group-mark"><ReportIcon name="alert" /></span>
        <h6 id="report-actions-heading">{a.heading}</h6>
      </header>
      <p className="report-actions-lead">{lead}</p>
      {paragraphs.filter(Boolean).map((text, i) => <p key={i} className="report-actions-note">{text}</p>)}
      {actions.map(f => <ActionCard key={f.id} f={f} included={included} openFinding={openFinding} />)}
    </div>
    {mismatches.length > 0 && <div className="report-group" data-tone="pending">
      <header className="report-group-title">
        <span className="report-group-mark"><ReportIcon name="file" /></span>
        <h6>{a.mismatchHeading}</h6>
      </header>
      <p className="report-actions-lead">{a.mismatchLead(mismatches.length)}</p>
      {mismatches.slice(0, MISMATCH_SHOWN).map(row => <MismatchCard key={row.index} {...row} sections={sections} included={included} openComparison={openComparison} />)}
      {mismatches.length > MISMATCH_SHOWN && <Fold title={a.mismatchMore(mismatches.length - MISMATCH_SHOWN)}>
        {mismatches.slice(MISMATCH_SHOWN).map(row => <MismatchCard key={row.index} {...row} sections={sections} included={included} openComparison={openComparison} />)}
      </Fold>}
    </div>}
    {notices.length > 0 && <div className="report-group" data-tone="neutral">
      <header className="report-group-title">
        <span className="report-group-mark"><ReportIcon name="check" /></span>
        <h6>{a.noticesHeading}</h6>
      </header>
      <ul className="report-actions-notices">{notices.map(x => <li key={x.code}>
        <button type="button" className="report-plain-action" onClick={() => openSection(x.section)}>{(t.sections.attentionText[x.code] ?? (() => x.code))(x.count)}</button>
      </li>)}</ul>
    </div>}
    <CandidateList title={a.candidates(candidates.length)} note={a.candidatesNote} findings={candidates} included={included} openFinding={openFinding} />
    <CandidateList title={a.secondary(secondary.length)} note={a.secondaryNote} findings={secondary} included={included} openFinding={openFinding} />
    <Diagnostics report={report} sections={sections} openFinding={openFinding} />
  </section>;
}
