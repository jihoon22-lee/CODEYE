import { useId, useState, type ReactNode } from 'react';
import type { AnalysisReport, Finding, Observation } from './analysisReport';
import type { Citation, Dependency, Ledger, LedgerModule, ModelState, SectionStage, Sections } from './reportSections';
import { ComparisonSection } from './HistoryView';
import { runStatusLabel } from './format';
import { ko } from './locales/ko';
import { reportKo as t } from './locales/report-ko';
import { Citations, FindingCard, FindingList, PinnedSourceLink, ReportCalls, ReportCounts, ReportExports, ReportIdentity, ReportScope, ReportTasks, TextList, Verdict, explainReason, label, sourceLocation, type ReportHistory } from './ReportParts';
import {
  gapGroups,
  locationPath,
  moduleDir,
  moduleSecondary,
  orderFindings,
  overviewFacts,
  places,
  primaryCitation,
  projectReadings,
  ruleGroups,
  secondaryDir,
  secondaryPath,
  secondaryReviewFinding,
  secondaryReviewScope,
  severityOf,
  slotRuns,
  toolRows,
  SHOWN,
  type Places,
} from './reportGroups';

const s = t.sections;
const sm = s.summary;
const ORDER = ['environment', 'documents', 'static_analysis', 'metrics', 'secrets', 'code_review', 'gaps'];
const TABS = ['overview', ...ORDER];

function placesText(p: Places) {
  let out = p.shown.join(', ');
  if (p.more > 0) out += ' ' + sm.morePlaces(p.more);
  if (p.moreSecondary > 0) out += ' ' + sm.ofSecondary(p.moreSecondary);
  return out;
}
function shortList(list: string[]) {
  return list.length <= SHOWN ? list.join(', ') : list.slice(0, SHOWN).join(', ') + ' ' + sm.more(list.length - SHOWN);
}
// cutLine is s on one line, cut to n characters.
function cutLine(text: string | null, n = 100) {
  const one = (text ?? '').split(/\s+/).filter(Boolean).join(' ');
  const chars = Array.from(one);
  return chars.length <= n ? one : chars.slice(0, n).join('') + '…';
}

// More is a disclosure whose rows render only when it is opened, so a long
// list costs nothing until asked for (keyboard reachable through summary).
function More({ title, children }: { title: string; children: () => ReactNode }) {
  const [open, setOpen] = useState(false);
  return (
    <details className="report-more" onToggle={(e) => setOpen((e.currentTarget as HTMLDetailsElement).open)}>
      <summary>{title}</summary>
      {open && children()}
    </details>
  );
}

function state(value: string | null) {
  return value === null ? '—' : Object.hasOwn(s.states, value) ? s.states[value] : ko.stages.unknownState(value);
}
// The colour class of a state; an unknown state is never coloured as success.
function stateClass(value: string | null) {
  return 'state-pill st-' + (value === null ? 'none' : Object.hasOwn(s.states, value) ? value : 'unknown');
}
function or(value: string | null | undefined, fallback = '—') {
  return value == null || value === '' ? fallback : value;
}
function anchor(key: string) {
  return 'report-section-' + key;
}

function Table({ head, rows, empty, compact }: { head: string[]; rows: ReactNode[][]; empty?: string; compact?: boolean }) {
  if (rows.length === 0) return empty ? <p>{empty}</p> : null;
  // Columns of numbers align right in tabular figures.
  const numeric = head.map((_, j) => rows.every((row) => typeof row[j] === 'number'));
  return (
    <div className={'table' + (compact ? ' compact' : '')}>
      <table>
        <thead>
          <tr>
            {head.map((h, j) => (
              <th key={h} scope="col" className={numeric[j] ? 'num' : undefined}>
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, i) => (
            <tr key={i}>
              {row.map((cell, j) => (
                <td key={j} className={numeric[j] ? 'num' : undefined}>{cell}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function SectionCounts({ sections, keyName }: { sections: Sections; keyName: string }) {
  const counts = sections.overview.find((x) => x.key === keyName)?.counts ?? {};
  return (
    <dl className="report-counts section-counts" data-section-counts={keyName}>
      {(s.countOrder[keyName] ?? [])
        .filter((k) => Object.hasOwn(counts, k))
        .map((k) => (
          <div key={k} data-count={k}>
            <dt>{label(s.countLabels, keyName + '.' + k)}</dt>
            <dd>{counts[k]}</dd>
          </div>
        ))}
    </dl>
  );
}

function StageLine({ stage }: { stage: SectionStage }) {
  return <p>{s.stageLine(state(stage.state), stage.tasks, stage.succeeded)}</p>;
}

function ModelNote({ model }: { model: ModelState }) {
  if (model.performed)
    return (
      <p className="model-note" role="note">
        {s.model(model.task)}
      </p>
    );
  return <p>{s.noModel(label(s.modelReasons, model.reason ?? '') || explainReason(model.reason ?? ''))}</p>;
}

function ModelReading({ title, children }: { title: string; children: ReactNode }) {
  const heading = useId();
  return (
    <section className="model-reading" aria-labelledby={heading}>
      <header><h6 id={heading}>{title}</h6><span>{t.modelReading}</span></header>
      {children}
    </section>
  );
}

type Reading = {
  title: string;
  statement: string;
  scope?: string;
  status?: string;
  secondary?: boolean;
  sources: { label?: string; refs: string[] }[];
};

function ReadingList({ rows, catalog }: { rows: Reading[]; catalog: Citation[] }) {
  return (
    <ul className="plain model-reading-list">
      {rows.map(({ title, statement, scope, status, secondary, sources }, i) => (
        <li className="model-reading-item" key={i}>
          <div className="reading-title">
            {status && <span className="pill tone-plain">{status}</span>}
            {secondary && <span className="pill tone-plain">{t.reading.secondary}</span>}
            <strong>{title}</strong>
          </div>
          <p>{statement}</p>
          {scope && <p className="small">{t.scopeTitle} <code>{scope}</code></p>}
          {sources.map(({ label, refs }, j) => refs.length > 0 && <div className="reading-refs" key={j}>
            {label && <b>{label}</b>}
            <Citations refs={refs} catalog={catalog} />
          </div>)}
        </li>
      ))}
    </ul>
  );
}

function ObservationTable({ ids, byID }: { ids: string[]; byID: Map<string, Observation> }) {
  return (
    <Table
      head={[s.col.rule, s.col.location, s.col.message]}
      rows={ids.map((id) => {
        const o = byID.get(id);
        return [
          <code key="r">{o?.rule}</code>,
          <code key="l">{o?.path ?? o?.state}</code>,
          <>
            {o?.message ?? t.unknown}
            {o?.metric && <small> ({t.metricMarker(o.metric.value, o.metric.threshold)})</small>}
          </>,
        ];
      })}
    />
  );
}

// LedgerBlock is the per-module review ledger: what of each module's code
// and test files was sent to the model (not verified scope) and why the rest
// was not, then the slice tasks with the modules they hold.
// The summary shows one row per primary module and one sum row for the
// modules below a secondary path, and the slots folded into runs of one state;
// every module and slice task is behind a disclosure.
function LedgerBlock({ ledger }: { ledger: Ledger }) {
  const l = s.ledger;
  const n = (v: number | null) => (v === null ? l.unknown : String(v));
  const classes = ledger.modules.some((m) => m.cls !== null);
  const head = [s.col.module, ...(classes ? [l.col.cls] : []), l.col.discovered, l.col.eligible, l.col.sent, l.col.bytesSent, l.col.truncated, l.col.withheld, l.col.unreadable, l.col.budgetExhausted, l.col.notPlanned, ...(classes ? [l.col.secondaryNotReached] : []), l.col.notSent, l.col.completed];
  const row = (m: LedgerModule, name: ReactNode, discovered: string) => [
    name,
    ...(classes ? [label(l.classes, m.cls ?? '')] : []),
    discovered,
    m.eligible,
    m.sent,
    m.bytesSent,
    m.truncated,
    m.withheld,
    m.unreadable,
    m.budgetExhausted,
    m.notPlanned,
    ...(classes ? [m.secondaryNotReached] : []),
    m.notSent,
    m.completed,
  ];
  const primary = ledger.modules.filter((m) => !moduleSecondary(m));
  const secondary = ledger.modules.filter(moduleSecondary);
  const sum = (k: keyof LedgerModule) => secondary.reduce((a, m) => a + (m[k] as number), 0);
  const total = { ...secondary[0], cls: 'secondary' } as LedgerModule;
  for (const k of ['eligible', 'sent', 'bytesSent', 'truncated', 'withheld', 'unreadable', 'budgetExhausted', 'notPlanned', 'secondaryNotReached', 'notSent', 'completed'] as const) total[k] = sum(k);
  const slices = new Map<string, { status: string; reason: string | null; modules: string[] }>();
  for (const m of ledger.modules) {
    for (const x of m.slices) {
      const r = slices.get(x.task) ?? { status: x.status, reason: x.reason, modules: [] };
      r.modules.push(m.id);
      slices.set(x.task, r);
    }
  }
  return (
    <>
      <h6>{l.heading}</h6>
      <p>
        <strong>{l.coverage(ledger.modules.length, ledger.modules.reduce((a, m) => a + m.eligible, 0), ledger.modules.reduce((a, m) => a + m.sent, 0))}</strong>
      </p>
      <p className="small">{l.lead}</p>
      <ul>
        <li>{label(l.status, ledger.status)}</li>
        <li>
          {l.scope} <code>{ledger.scope.key}</code> {label(s.taskStatus, ledger.scope.status ?? '')}
          {ledger.scope.reason ? ' — ' + explainReason(ledger.scope.reason) : ''}
        </li>
        <li>{l.calls(n(ledger.callsUsed), n(ledger.maxCalls))}</li>
        <li>{l.bytes(n(ledger.requestBytesSent), n(ledger.maxRequestBytes))}</li>
        {ledger.primarySlotsRefused > 0 && <li>{l.refusedSlots(ledger.primarySlotsRefused)}</li>}
      </ul>
      <Table
        compact
        head={head}
        rows={[
          ...primary.map((m) => row(m, <code key="m">{m.id}</code>, m.discovered ? l.discovered : l.notDiscovered)),
          ...(secondary.length > 0 ? [row(total, l.secondaryModules(secondary.length), '—')] : []),
        ]}
        empty={l.none}
      />
      <p className="small">{l.slotsLead}</p>
      <Table
        head={[l.col.slots, s.col.status, l.col.reason, l.col.modules]}
        rows={slotRuns(ledger).map((x) => [l.slotSpan(x.first, x.last, x.n), label(s.taskStatus, x.status), x.reason ? explainReason(x.reason) : '—', <code key="m">{shortList(x.modules)}</code>])}
      />
      <More title={l.allModules(ledger.modules.length)}>
        {() => (
          <>
            <Table compact head={head} rows={ledger.modules.map((m) => row(m, <code key="m">{m.id}</code>, m.discovered ? l.discovered : l.notDiscovered))} empty={l.none} />
            <Table
              head={[l.col.task, s.col.status, l.col.reason, l.col.modules]}
              rows={[...slices.entries()]
                .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
                .map(([k, x]) => [<code key="k">{k}</code>, label(s.taskStatus, x.status), x.reason ? explainReason(x.reason) : '—', <code key="m">{x.modules.join(', ')}</code>])}
            />
          </>
        )}
      </More>
    </>
  );
}

// DependencyBlock is what each dependency task prepared before the analyzers ran,
// or why it did not: the record's facts, or only the task's when the record is missing.
function DependencyBlock({ dependencies }: { dependencies: Dependency[] }) {
  const l = s.dependencies;
  const name = (x: Dependency) => x.module ?? x.task;
  const prepared = (x: Dependency) => {
    if (x.prepared === null) return '—';
    const n = x.declared === null ? String(x.prepared) : `${x.prepared}/${x.declared}`;
    return x.failed !== null && x.failed > 0 ? `${n} ${l.failed(x.failed)}` : n;
  };
  const reason = (x: Dependency) => {
    const code = x.reason ?? x.taskReason;
    if (code !== null) return explainReason(code);
    return x.result === 'accepted' ? '—' : label(l.record, x.result);
  };
  const failures = dependencies.flatMap((x) => x.failedItems.map((f) => ({ module: name(x), ...f })));
  return (
    <>
      <h6>{l.heading}</h6>
      <p>{l.lead}</p>
      <Table
        head={[s.col.module, l.col.manager, l.col.state, l.col.prepared, s.col.reason, l.col.limitations]}
        rows={dependencies.map((x) => [
          <code key="m">{name(x)}</code>,
          // go reports "go1.27.4": show "go 1.27.4", not the name twice.
          x.manager === null ? '—' : <code key="g">{x.manager + (x.managerVersion ? ' ' + (x.managerVersion.startsWith(x.manager) ? x.managerVersion.slice(x.manager.length) : x.managerVersion) : '')}</code>,
          x.state === null ? label(s.taskStatus, x.status) : label(l.state, x.state),
          prepared(x),
          reason(x),
          or(x.limitations.map(explainReason).join(' ')),
        ])}
      />
      {failures.length > 0 && (
        <>
          <h6>{l.failedItems}</h6>
          <ul>
            {failures.map((f, i) => (
              <li key={i}>
                <code>{f.module}</code> <code>{f.name}</code>
                {f.reason ? ' — ' + explainReason(f.reason) : ''}
              </li>
            ))}
          </ul>
        </>
      )}
    </>
  );
}

// summaryLines are the overview's sentences: each kind of result with the
// counts that matter, from the stored report.
function summaryLines(f: ReturnType<typeof overviewFacts>, aiReview: string): string[] {
  const out: string[] = [];
  if (!f.model && aiReview === 'performed') out.push(sm.modelNone);
  if (f.model) {
    const rest = (['reproduced', 'inconclusive', 'refuted', 'unreviewed'] as const).filter((k) => f.model![k] > 0).map((k) => sm.validation[k](f.model![k]));
    out.push(sm.modelFindings(f.model.total, f.model.supported + f.model.reproduced) + (rest.length > 0 ? ' (' + rest.join(', ') + ')' : '') + '.');
  }
  if (f.scope) out.push(sm.reviewScope(f.scope.primary, f.scope.primarySent) + (f.scope.secondary > 0 ? ' ' + sm.reviewScopeSecondary(f.scope.secondary, f.scope.secondarySent) : ''));
  const d = f.diagnostics;
  let line = sm.static(d.total, d.rules, d.tools);
  if (d.suppressed > 0) line += ' ' + sm.staticSuppressed(d.suppressed);
  if (d.secondary > 0) line += ' ' + sm.staticSecondary(d.secondary);
  if (d.requiredFailed > 0) line += ' ' + sm.requiredNotSucceeded(d.requiredFailed);
  out.push(line);
  if (f.metrics.kinds > 0) out.push(sm.metrics(f.metrics.observations, f.metrics.kinds));
  if (f.secrets.detections > 0) out.push(sm.secrets(f.secrets.detections, f.secrets.files));
  if (f.gaps.total > 0) out.push(sm.gaps(f.gaps.total, f.gaps.kinds));
  return out;
}

function pick(findings: Finding[], ids: string[]) {
  return findings.filter((f) => ids.includes(f.id));
}

function comparisonReading(row: Sections['documents']['comparisons'][number], catalog: Citation[]): Reading {
  const hasPrimary = catalog.some(c => row.codeRefs.includes(c.id) && primaryCitation(c, 'code'));
  return {
    title: row.statement,
    scope: row.scope,
    status: label(t.assessment, row.status),
    statement: row.reason,
    secondary: row.codeRefs.length > 0 ? !hasPrimary : secondaryReviewScope(row.scope),
    sources: [
      { label: t.reading.documentEvidence, refs: row.documentRefs },
      { label: t.reading.codeEvidence, refs: row.codeRefs },
    ],
  };
}

function strengthReading(row: Sections['codeReview']['strengths'][number], catalog: Citation[]): Reading {
  return {
    title: row.title, scope: row.scope, statement: row.benefit,
    secondary: !catalog.some(c => row.refs.includes(c.id) && primaryCitation(c, 'code')),
    sources: [{ label: t.reading.codeEvidence, refs: row.refs }],
  };
}

function ProjectOverview({ report, sections, go }: { report: AnalysisReport; sections: Sections; go: (key: string) => void }) {
  const { actions, comparisons, uncertainComparison, strengths, followups, diagnostic } = projectReadings(report, sections);
  const { documents, codeReview } = sections;
  const next = (title: string, decisions: typeof actions) => decisions.length > 0 && (
    <section className="project-decisions">
      <h6>{title}</h6>
      <ul className="plain report-priorities">
        {decisions.map(decision => <li key={decision.finding.id}><FindingCard f={decision.finding} preview={decision} /></li>)}
      </ul>
    </section>
  );
  const gaps = sections.gaps.length;
  const unknown = documents.comparisons.filter(row => row.status === 'unknown').length;
  const showUnknownCount = unknown > (uncertainComparison ? 1 : 0);
  const secrets = sections.attention.find(a => a.code === 'secrets_detected');
  const comparisonRows = uncertainComparison ? [uncertainComparison] : comparisons;
  const hasReadings = actions.length + comparisonRows.length + strengths.length + followups.length > 0;
  const documentReading = <section className="project-reading">
    {comparisonRows.length > 0 ? (
      <ModelReading title={uncertainComparison ? t.reading.uncertainComparison : t.comparisonsTitle}>
        <ReadingList rows={comparisonRows.map(row => comparisonReading(row, documents.citations))} catalog={documents.citations} />
      </ModelReading>
    ) : <><h6>{t.comparisonsTitle}</h6><p>{t.reading.noComparison}</p></>}
    <button type="button" className="link" onClick={() => go('documents')}>{s.go(s.titles.documents)}</button>
  </section>;
  return (
    <>
      {next(t.reading.actions, actions)}
      <div className={strengths.length ? 'project-readings has-strengths' : 'project-readings'}>
        {comparisons.length > 0 && documentReading}
        {strengths.length > 0 && <section className="project-reading">
          <ModelReading title={t.strengthsTitle}>
            <ReadingList rows={strengths.map(row => strengthReading(row, codeReview.citations))} catalog={codeReview.citations} />
          </ModelReading>
          <button type="button" className="link" onClick={() => go('code_review')}>{s.go(s.titles.code_review)}</button>
        </section>}
        {comparisons.length === 0 && documentReading}
      </div>
      {next(t.reading.followups, followups)}
      {diagnostic?.location && <section className="project-decisions">
        <h6>{t.reading.staticTitle}</h6>
        <div className="report-diagnostic">
          <span className="pill tone-plain">{t.reading.staticStatus}</span>
          <code>{[diagnostic.tool, diagnostic.rule].filter(Boolean).join(' · ')}</code>
          {diagnostic.message && <p>{diagnostic.message}</p>}
          <PinnedSourceLink {...diagnostic.location} fallback={sourceLocation(diagnostic.location.path, diagnostic.location.start, diagnostic.location.end)} />
        </div>
        <button type="button" className="link" onClick={() => go('static_analysis')}>{s.go(s.titles.static_analysis)}</button>
      </section>}
      {!hasReadings && !diagnostic && <p>{t.reading.noFindings}</p>}
      {(gaps > 0 || showUnknownCount || secrets || report.aiReview !== 'performed') && (
        <section className="project-coverage">
          <h6>{t.reading.coverage}</h6>
          {report.aiReview !== 'performed' && <p>{t.reading.modelState(label(t.ai, report.aiReview))}</p>}
          {showUnknownCount && <p>{t.reading.unknownComparisons(unknown)}</p>}
          {gaps > 0 && <p>
            {s.attentionText.analysis_gaps(gaps)}{' '}
            <button type="button" className="link" onClick={() => go('gaps')}>{s.go(s.titles.gaps)}</button>
          </p>}
          {secrets && <p>
            {s.attentionText.secrets_detected(secrets.count)}{' '}
            <button type="button" className="link" onClick={() => go('secrets')}>{s.go(s.titles.secrets)}</button>
          </p>}
        </section>
      )}
    </>
  );
}

// Project reports use tabs. Archived reports without project metadata use
// the same reading order in native disclosures.
export function SectionedReport({ report, sections, history }: { report: AnalysisReport; sections: Sections; history?: ReportHistory }) {
  // Sections the overview points at start open; the rest stay collapsed.
  const [open, setOpen] = useState<Set<string>>(() => new Set(sections.attention.map((a) => a.section)));
  const tabbed = Boolean(report.project);
  const id = useId();
  const [active,setActive] = useState('overview');
  const tabId = (key:string) => id + '-tab-' + key;
  const select = (key:string) => {
    setActive(key);
    document.getElementById(tabId(key))?.focus();
  };
  const byID = new Map(report.observations.map((o) => [o.id, o]));
  const go = (key: string) => {
    if (tabbed) {select(key); return;}
    setOpen((prev) => new Set(prev).add(key));
    // After the section opened, move focus to its summary.
    setTimeout(() => document.getElementById(anchor(key))?.querySelector('summary')?.focus(), 0);
  };
  const toggle = (key: string, isOpen: boolean) =>
    setOpen((prev) => {
      const next = new Set(prev);
      if (isOpen) next.add(key);
      else next.delete(key);
      return next;
    });
  const overview = new Map(sections.overview.map((x) => [x.key, x]));
  const block = (key: string, body: ReactNode) => {
    const st = overview.get(key)?.state ?? null;
    if (tabbed) return <section id={anchor(key)} role="tabpanel" aria-labelledby={tabId(key)} hidden={active!==key} tabIndex={0} key={key}>
      {active===key && <><h5>{label(s.titles,key)}</h5>{body}</>}
    </section>;
    return (
      <details key={key} id={anchor(key)} className="report-section" open={open.has(key)} onToggle={(e) => toggle(key, (e.currentTarget as HTMLDetailsElement).open)}>
        <summary>
          <strong>{label(s.titles, key)}</strong>
          {st !== null && <span className={stateClass(st)}>{state(st)}</span>}
        </summary>
        {body}
      </details>
    );
  };
  const e = sections.environment;
  const d = sections.documents;
  const sa = sections.staticAnalysis;
  const m = sections.metrics;
  const sc = sections.secrets;
  // Not screened because a secrets task ran but its result is unusable: name
  // that task with its state and reason (review still ran on the read filters).
  const unusableScreen = sc.modelInput === 'not_screened'
    ? sc.tasks.map((k) => report.tasks.find((x) => x.key === k)).find((r) => r && r.status !== 'succeeded' && r.status !== 'unsupported')
    : undefined;
  const cr = sections.codeReview;
  const staticFindings = sa.languages.flatMap((l) => l.tools.flatMap((x) => x.findings));
  const sevOf = severityOf(report.findings, staticFindings);
  const facts = overviewFacts(report, sections);
  const modSecondary = (id: string) => secondaryDir(moduleDir(id));
  // Review findings: the supported and reproduced ones on primary paths
  // open the section; the others and those on secondary paths come after.
  const reviewFindings = orderFindings(pick(report.findings, cr.findings));
  const supported = (f: Finding) => (f.validation === 'supported' || f.validation === 'reproduced') && !secondaryReviewFinding(f);
  const reviewFirst = reviewFindings.filter(supported);
  const reviewRest = reviewFindings.filter((f) => !supported(f));
  const allDiagnostics = sa.languages.flatMap((l) => l.tools.flatMap((x) => x.observations));
  const suppressed = allDiagnostics.filter((id) => byID.get(id)?.suppressed && !byID.get(id)?.metric).length;
  const largest = (ids: string[]) => {
    const list = ids.map((id) => {
      const o = byID.get(id);
      const sec = !!o?.path && secondaryPath(locationPath(o.path));
      return { text: (o?.path ?? '—') + ' (' + (o?.metric?.value ?? '—') + ')', value: o?.metric?.value ?? 0, sec };
    });
    list.sort((a, b) => Number(a.sec) - Number(b.sec) || b.value - a.value);
    return placesText(places(list.filter((x) => !x.sec).map((x) => x.text), list.filter((x) => x.sec).map((x) => x.text)));
  };
  const detectionsPrimary = sc.detections.filter((x) => !secondaryPath(locationPath(x.place)));
  const detectionsSecondary = sc.detections.filter((x) => secondaryPath(locationPath(x.place)));
  const detectionRow = (x: (typeof sc.detections)[number]) => [<code key="r">{x.rule}</code>, <code key="p">{x.place}</code>, <code key="f">{or(x.finding)}</code>];
  // Environment and document rows of modules below a secondary path move behind
  // a disclosure; a mismatch stays in view because the overview points at it.
  const envModules = e.modules.filter((x) => !modSecondary(x.id));
  const envRequirements = e.requirements.filter((x) => !modSecondary(x.module));
  const envVerdicts = e.verdicts.filter((x) => !modSecondary(x.module) || x.status === 'mismatch');
  const envSecondary = e.modules.length - envModules.length + e.requirements.length - envRequirements.length + e.verdicts.length - envVerdicts.length;
  const docItems = d.items.filter((x) => !secondaryPath(x.path));
  const docModules = d.modules.filter((x) => !modSecondary(x.module));
  const docSecondary = d.items.length - docItems.length + d.modules.length - docModules.length;
  const moduleRow = (x: (typeof e.modules)[number]) => [<code key="m">{x.id}</code>, x.ecosystem, <code key="p">{x.path}</code>, label(s.inScope, String(x.inScope))];
  const requirementRow = (x: (typeof e.requirements)[number]) => [<code key="m">{x.module}</code>, x.tool, <code key="c">{x.constraint ?? s.withheld}</code>, <code key="s">{x.source}</code>];
  const verdictRow = (x: (typeof e.verdicts)[number]) => [<code key="m">{x.module}</code>, x.tool, label(s.verdictStatus, x.status), or(x.reason)];
  const itemRow = (x: (typeof d.items)[number]) => [<code key="p">{x.path}</code>, x.cls, <code key="a">{x.appliesTo}</code>, label(s.docStatus, x.status), x.reason ? explainReason(x.reason) : '—', x.size ?? '—'];
  const docModuleRow = (x: (typeof d.modules)[number]) => [<code key="m">{x.module}</code>, label(s.docState, x.state), or(x.own.join(', ')), or(x.inherited.join(', '))];
  const docComparisons = d.comparisons.map(c => comparisonReading(c, d.citations));
  const strengths = cr.strengths.map(x => strengthReading(x, cr.citations));
  const bodies: Record<string, ReactNode> = {
    environment: (
      <>
        <StageLine stage={e.stage} />
        <p>{label(s.discovery, e.discovery)}</p>
        <SectionCounts sections={sections} keyName="environment" />
        {e.discovery === 'accepted' && (
          <>
            <h6>{s.modules}</h6>
            <Table head={[s.col.module, s.col.ecosystem, s.col.path, s.col.scope]} rows={envModules.map(moduleRow)} />
            <h6>{s.requirements}</h6>
            <Table head={[s.col.module, s.col.tool, s.col.requirement, s.col.source]} rows={envRequirements.map(requirementRow)} empty={ko.stages.states.no_tasks} />
            <h6>{s.observed}</h6>
            <Table head={[s.col.tool, s.col.status, s.col.version, s.col.detail]} rows={e.observed.map((x) => [x.tool, label(s.obsStatus, x.status), or(x.version), or(x.detail)])} />
            <h6>{s.verdicts}</h6>
            <Table head={[s.col.module, s.col.tool, s.col.status, s.col.reason]} rows={envVerdicts.map(verdictRow)} />
            {envSecondary > 0 && (
              <More title={sm.secondaryRows(envSecondary)}>
                {() => (
                  <>
                    <Table head={[s.col.module, s.col.ecosystem, s.col.path, s.col.scope]} rows={e.modules.filter((x) => modSecondary(x.id)).map(moduleRow)} />
                    <Table head={[s.col.module, s.col.tool, s.col.requirement, s.col.source]} rows={e.requirements.filter((x) => modSecondary(x.module)).map(requirementRow)} />
                    <Table head={[s.col.module, s.col.tool, s.col.status, s.col.reason]} rows={e.verdicts.filter((x) => modSecondary(x.module) && x.status !== 'mismatch').map(verdictRow)} />
                  </>
                )}
              </More>
            )}
            <h6>{s.analyzerTools}</h6>
            <Table
              head={[s.col.tool, s.col.analyzers, s.col.status, s.col.version]}
              rows={e.analyzerTools.map((x) => [x.tool, <code key="a">{x.analyzers.join(', ')}</code>, label(s.obsStatus, x.status), or(x.version)])}
            />
          </>
        )}
        {e.dependencies.length > 0 && <DependencyBlock dependencies={e.dependencies} />}
        <h6>{s.notes}</h6>
        <ModelNote model={e.model} />
        {e.model.performed && (
          <ul>
            {e.notes.map((n, i) => (
              <li key={i}>
                {s.note(n)}
                <Citations refs={n.evidence} catalog={e.citations} />
              </li>
            ))}
          </ul>
        )}
      </>
    ),
    documents: (
      <>
        <StageLine stage={d.stage} />
        {d.comparisons.length > 0 && (
          <ModelReading title={t.comparisonsTitle}>
            <ReadingList rows={docComparisons} catalog={d.citations} />
          </ModelReading>
        )}
        {d.quality.length > 0 && (
          <ModelReading title={t.qualityTitle}>
            <ReadingList rows={d.quality.map(q => ({ title: label(t.assessment, q.aspect), status: label(t.assessment, q.assessment), statement: q.statement, sources: [{ refs: q.refs }] }))} catalog={d.citations} />
          </ModelReading>
        )}
        <p>{label(s.discovery, d.discovery)}</p>
        {d.budget && <p>{s.budget(d.budget)}</p>}
        <SectionCounts sections={sections} keyName="documents" />
        {d.discovery === 'accepted' && (
          <>
            <h6>{s.items}</h6>
            <Table head={[s.col.path, s.col.cls, s.col.appliesTo, s.col.status, s.col.reason, s.col.size]} rows={docItems.map(itemRow)} empty={t.unknown} />
            <h6>{s.docModules}</h6>
            <Table head={[s.col.module, s.col.status, s.col.own, s.col.inherited]} rows={docModules.map(docModuleRow)} />
            {docSecondary > 0 && (
              <More title={sm.secondaryDocuments(docSecondary)}>
                {() => (
                  <>
                    <Table head={[s.col.path, s.col.cls, s.col.appliesTo, s.col.status, s.col.reason, s.col.size]} rows={d.items.filter((x) => secondaryPath(x.path)).map(itemRow)} />
                    <Table head={[s.col.module, s.col.status, s.col.own, s.col.inherited]} rows={d.modules.filter((x) => modSecondary(x.module)).map(docModuleRow)} />
                  </>
                )}
              </More>
            )}
            {d.overlaps.length > 0 && (
              <>
                <h6>{s.overlaps}</h6>
                <Table head={[s.col.path, s.col.documents]} rows={d.overlaps.map((x) => [<code key="p">{x.path}</code>, x.documents.join(', ')])} />
              </>
            )}
          </>
        )}
        <ModelNote model={d.model} />
        {d.model.performed && (
          <>
            <h6>{s.contracts}</h6>
            <ul>
              {d.contracts.map((x, i) => (
                <li key={i}>
                  [{x.appliesTo}] {x.statement}
                  <Citations refs={x.evidence} catalog={d.citations} />
                </li>
              ))}
            </ul>
            <h6>{s.conflicts}</h6>
            <ul>
              {d.conflicts.map((x, i) => (
                <li key={i}>
                  [{x.scope}] {x.summary} / {x.impact}
                  <Citations refs={x.evidence} catalog={d.citations} />
                </li>
              ))}
            </ul>
            <h6>{s.questions}</h6>
            <ul>
              {d.questions.map((x, i) => (
                <li key={i}>
                  [{x.appliesTo}] {x.question}
                  <Citations refs={x.evidence} catalog={d.citations} />
                </li>
              ))}
            </ul>
            <h6>{s.instructionNotes}</h6>
            <ul>
              {d.notes.map((x, i) => (
                <li key={i}>
                  {x.note}
                  <Citations refs={x.evidence} catalog={d.citations} />
                </li>
              ))}
            </ul>
          </>
        )}
      </>
    ),
    static_analysis: (
      <>
        <StageLine stage={sa.stage} />
        <p>{s.staticLead}</p>
        <details><summary>{t.glossary}</summary><Table head={[s.col.tool,s.col.message]} rows={Object.entries(t.tools)} /></details>
        <SectionCounts sections={sections} keyName="static_analysis" />
        {sa.languages.map((lang) => {
          const groups = ruleGroups(lang.tools, byID, sevOf);
          return (
            <section key={lang.ecosystem ?? 'unknown'} data-language={lang.ecosystem ?? 'unknown'}>
              <h6>{s.language(lang.ecosystem)}</h6>
              {lang.ecosystem === 'any' && <p className="small">{s.anyNote}</p>}
              <Table
                head={[s.col.tool, s.col.tasks, s.col.required, s.col.succeeded, s.col.diagnostics, s.col.notSucceeded]}
                rows={toolRows(lang.tools, byID).map((x) => {
                  const ran = x.succeeded > 0;
                  // An analyzer none of whose tasks ran never reads as zero diagnostics.
                  const diagnostics = !ran ? s.notRun : x.purpose === 'metrics' ? s.metricCount(x.diagnostics) : x.purpose === 'secrets' ? s.secretCount(x.diagnostics) : String(x.diagnostics);
                  const failed = x.failed.map((f) => f.task + ' ' + label(s.taskStatus, f.status) + (f.reason ? ' · ' + explainReason(f.reason) : f.result !== 'accepted' ? ' · ' + label(s.result, f.result) : ''));
                  return [<code key="t">{x.name}</code>, x.tasks, x.required, x.succeeded, diagnostics, or(shortList(failed))];
                })}
              />
              {groups.length > 0 && (
                // What (the rule and its first message), how many, how severe, where first.
                <Table
                  head={[s.col.rule, s.col.message, s.col.count, s.col.severity, s.col.location]}
                  rows={groups.map((g) => [
                    <span key="r">
                      <code>{g.rule}</code>
                      <br />
                      <small>{g.analyzer}</small>
                    </span>,
                    cutLine(g.message),
                    g.count,
                    g.severity === null ? '—' : <span key="v" className={'pill sev sev-' + (Object.hasOwn(t.severity, g.severity) ? g.severity : 'unknown')}>{label(t.severity, g.severity)}</span>,
                    placesText(places(g.primary, g.secondary)),
                  ])}
                />
              )}
            </section>
          );
        })}
        <p className="small">{sm.rulesNote}</p>
        {suppressed > 0 && <p className="small">{sm.suppressedRows(suppressed)}</p>}
        <More title={sm.allDiagnostics(allDiagnostics.length)}>
          {() => (
            <>
              {sa.languages.map((lang) => (
                <section key={lang.ecosystem ?? 'unknown'}>
                  <h6>{s.language(lang.ecosystem)}</h6>
                  <Table
                    head={[s.col.task, s.col.tool, s.col.required, s.col.status, s.col.result, s.col.version, s.col.files, s.col.diagnostics, s.col.rules]}
                    rows={lang.tools.map((x) => {
                      const ran = x.status === 'succeeded' && x.result === 'accepted';
                      // A task that did not run never reads as zero diagnostics.
                      const diagnostics = !ran ? s.notRun : x.purpose === 'metrics' ? s.metricCount(x.metrics) : x.purpose === 'secrets' ? s.secretCount(x.secrets) : String(x.observations.length);
                      return [
                        <code key="k">{x.task}</code>,
                        <code key="t">{or(x.analyzer)}</code>,
                        label(s.requiredLabel, String(x.required)),
                        <>
                          {label(s.taskStatus, x.status)}
                          {x.reason && <small> · {explainReason(x.reason)}</small>}
                        </>,
                        label(s.result, x.result),
                        or(x.version),
                        (x.inScope ?? t.unknown) + ' / ' + (x.processed ?? t.unknown),
                        diagnostics,
                        or(x.rules.map((r) => r.rule + ' ' + r.count).join(', ')),
                      ];
                    })}
                  />
                </section>
              ))}
              <h6>{t.observations}</h6>
              <ObservationTable ids={allDiagnostics} byID={byID} />
              <h6>{s.staticFindings}</h6>
              <FindingList findings={pick(report.findings, staticFindings)} history={history} empty={s.noFindings} />
            </>
          )}
        </More>
      </>
    ),
    metrics: (
      <>
        <StageLine stage={m.stage} />
        <p>{s.metricsLead}</p>
        <SectionCounts sections={sections} keyName="metrics" />
        <Table
          head={[s.col.task, s.col.status, s.col.reason]}
          rows={m.tasks.map((k) => {
            const row = report.tasks.find((x) => x.key === k);
            return [<code key="k">{k}</code>, label(s.taskStatus, row?.status ?? ''), row?.reason ? explainReason(row.reason) : '—'];
          })}
          empty={s.gapLabels.not_planned}
        />
        <Table
          head={[s.col.metric, s.col.unit, s.col.threshold, s.col.max, s.col.count, sm.largest]}
          rows={m.groups.map((g) => [<code key="n">{g.name}</code>, g.unit, g.thresholds.join(', '), g.max, g.observations.length, largest(g.observations)])}
          empty={s.noMetrics}
        />
        {m.groups.length > 0 && (
          <More title={sm.allMetrics(facts.metrics.observations)}>{() => <ObservationTable ids={m.groups.flatMap((g) => g.observations)} byID={byID} />}</More>
        )}
      </>
    ),
    secrets: (
      <>
        <StageLine stage={sc.stage} />
        <p>{s.secretsLead}</p>
        <p>
          {unusableScreen
            ? s.modelInputUnusable(unusableScreen.key, label(s.taskStatus, unusableScreen.status), unusableScreen.reason ? explainReason(unusableScreen.reason) : '—')
            : label(s.modelInput, sc.modelInput)}
        </p>
        <SectionCounts sections={sections} keyName="secrets" />
        <Table
          head={[s.col.task, s.col.status, s.col.reason]}
          rows={sc.tasks.map((k) => {
            const row = report.tasks.find((x) => x.key === k);
            return [<code key="k">{k}</code>, label(s.taskStatus, row?.status ?? ''), row?.reason ? explainReason(row.reason) : '—'];
          })}
          empty={s.gapLabels.not_planned}
        />
        <Table head={[s.col.rule, s.col.location, s.col.finding]} rows={detectionsPrimary.map(detectionRow)} empty={detectionsSecondary.length > 0 ? undefined : s.noSecrets} />
        {detectionsSecondary.length > 0 && (
          <More title={sm.secondaryDetections(detectionsSecondary.length)}>{() => <Table head={[s.col.rule, s.col.location, s.col.finding]} rows={detectionsSecondary.map(detectionRow)} />}</More>
        )}
      </>
    ),
    code_review: (
      <>
        <StageLine stage={cr.stage} />
        <h6>{sm.supported}</h6>
        <FindingList findings={reviewFirst} history={history} empty={cr.findings.length === 0 ? s.noFindings : sm.supportedNone} />
        {reviewRest.length > 0 && (
          <More title={sm.otherFindings(reviewRest.length)}>{() => <FindingList findings={reviewRest} history={history} empty={s.noFindings} />}</More>
        )}
        {cr.strengths.length > 0 && (
          <ModelReading title={t.strengthsTitle}>
            <ReadingList rows={strengths} catalog={cr.citations} />
          </ModelReading>
        )}
        {!cr.planned ? (
          <p>{s.noPack}</p>
        ) : (
          <ul>
            <li>{s.pack(or(cr.packID))}</li>
            <li>{s.callsLine(cr.calls, cr.acceptedCalls)}</li>
            <li>{s.requestBytes(cr.requestBytes)}</li>
            <li>{s.exposedLine(cr.exposed, cr.inventory)}</li>
          </ul>
        )}
        <SectionCounts sections={sections} keyName="code_review" />
        {cr.roles.length > 0 && (
          <>
            <h6>{s.roles}</h6>
            <Table head={[s.col.role, s.col.status, s.col.result]} rows={cr.roles.map((x) => [<code key="k">{x.key}</code>, label(s.taskStatus, x.status ?? ''), label(s.result, x.result)])} />
          </>
        )}
        {cr.ledger && <LedgerBlock ledger={cr.ledger} />}
        {cr.limits.length > 0 && (
          <>
            <h6>{s.limits}</h6>
            <p className="model-note" role="note">
              {s.model('review.interpret')}
            </p>
            <ul>
              {cr.limits.map((x, i) => (
                <li key={i}>
                  {x.statement} <small>{x.observations.join(', ')}</small>
                </li>
              ))}
            </ul>
          </>
        )}
      </>
    ),
    gaps: (
      <>
        <p>{s.gapsLead}</p>
        {sections.gaps.length > 0 && <p className="small">{sm.gapGroups}</p>}
        <Table
          head={[s.col.section, s.col.gap, s.col.detail, s.col.count, s.col.subject]}
          rows={gapGroups(sections.gaps).map((g) => [label(s.titles, g.section), label(s.gapLabels, g.code), g.detail ? explainReason(g.detail) : '—', g.subjects.length, <code key="s">{shortList(g.subjects)}</code>])}
          empty={s.noGaps}
        />
        {sections.gaps.length > 0 && (
          <More title={sm.allGaps(sections.gaps.length)}>
            {() => (
              <Table
                head={[s.col.section, s.col.gap, s.col.subject, s.col.detail]}
                rows={sections.gaps.map((g) => [label(s.titles, g.section), label(s.gapLabels, g.code), <code key="s">{or(g.subject)}</code>, g.detail ? explainReason(g.detail) : '—'])}
              />
            )}
          </More>
        )}
      </>
    ),
  };
  return (
    <article className="report-document sectioned-report" aria-label={t.title}>
      <header className="report-masthead">
        <div className="report-masthead-heading">
          <div>
            {report.project?.name && <p>{t.title}</p>}
            <h4>{report.project?.name || t.title}</h4>
          </div>
          <span className="pill tone-plain">{runStatusLabel(report.status)}</span>
        </div>
      </header>
      {tabbed && <div role="tablist" aria-label={t.title} className="report-tabs">
        {TABS.map((key,i) => <button key={key} id={tabId(key)} type="button" role="tab" aria-selected={active===key}
          aria-controls={anchor(key)} tabIndex={active===key ? 0 : -1} onClick={() => select(key)}
          onKeyDown={e => {
            const to = {ArrowRight:(i+1)%TABS.length, ArrowLeft:(i+TABS.length-1)%TABS.length, Home:0, End:TABS.length-1}[e.key];
            if (to!==undefined) {e.preventDefault(); select(TABS[to]);}
          }}>{key==='overview' ? s.overview : label(s.titles,key)}</button>)}
      </div>}
      <section id={anchor('overview')} role={tabbed ? 'tabpanel' : undefined} aria-labelledby={tabbed ? tabId('overview') : 'report-overview'} hidden={tabbed && active!=='overview'} tabIndex={tabbed ? 0 : undefined}>
        <h5 id="report-overview">{s.overview}</h5>
        {(!tabbed || active === 'overview') && <>
          <ProjectOverview report={report} sections={sections} go={go} />
          <More title={t.reading.operations}>{() => <>
            <Verdict report={report} />
            <ReportCounts report={report} />
            <ul className="report-summary">
              {summaryLines(facts, report.aiReview).map(line => <li key={line}>{line}</li>)}
            </ul>
            {sections.attention.length > 0 && <>
              <h6>{s.attention}</h6>
              <ul className="attention">
                {sections.attention.map(a => <li key={a.section + ':' + a.code}>
                  <span>{(s.attentionText[a.code] ?? (() => a.code))(a.count)}</span>
                  <button type="button" className="link" onClick={() => go(a.section)}>{s.go(label(s.titles, a.section))}</button>
                </li>)}
              </ul>
            </>}
            <h6>{s.stateTable}</h6>
            <Table
              head={[s.section, s.col.status, s.counts]}
              rows={ORDER.map(key => {
                const row = overview.get(key);
                return [
                  <button key="b" type="button" className="link" onClick={() => go(key)}>{label(s.titles, key)}</button>,
                  <span key="s" className={stateClass(row?.state ?? null)}>{state(row?.state ?? null)}</span>,
                  row && (s.countOrder[key] ?? [])
                    .filter(k => Object.hasOwn(row.counts, k))
                    .map(k => `${label(s.countLabels, key + '.' + k)} ${row.counts[k]}`).join(' · '),
                ];
              })}
            />
          </>}</More>
          {history && <More title={ko.history.title}>{() => <ComparisonSection load={history.load} reload={history.reload} />}</More>}
        </>}
      </section>
      {ORDER.map((key) => block(key, bodies[key]))}
      <details className="report-section">
        <summary>
          <strong>{t.reading.provenance}</strong>
        </summary>
        <p>{t.revision(report.revision)}</p>
        <ReportIdentity report={report} showStatus={false} />
        <ReportScope report={report} />
        <ReportCalls report={report} />
        <p>
          {t.snapshot} <code>{report.snapshot}</code>
        </p>
        {report.limitations.length > 0 && (
          <section>
            <h6>{t.scope}</h6>
            <TextList values={report.limitations.map(explainReason)} />
          </section>
        )}
        <ReportTasks report={report} />
      </details>
      <ReportExports report={report} />
    </article>
  );
}
