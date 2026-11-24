import { useContext, useId, useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { sourceEvidenceURL, type AnalysisReport, type Finding, type GoTestFailure, type Observation } from './analysisReport';
import type { Dependency, Ledger, LedgerModule, ModelState, SectionStage, Sections } from './parseReportSections';
import { ComparisonSection, HIDDEN } from './HistoryView';
import { ko } from './locales/ko';
import { reportKo as t, diagnosticDescription } from './locales/report-ko';
import { inspectionsKo } from './locales/report-inspections-ko';
import { Citations, FindingList, PinnedSourceLink, ReportCalls, ReportCounts, ReportExports, ReportIdentity, ReportPath, ReportReviewStatus, ReportScope, ReportSourceContext, ReportTasks, TextList, explainReason, label, sourceLocation, type ReportHistory } from './ReportParts';
import { ReportAction, ReportIcon, ReportLayout } from './ReportLayout';
import { ProjectOverview } from './ProjectOverview';
import { ReportDocumentReading } from './ReportDocumentReading';
import { ReportOverviewTable } from './ReportOverviewTable';
import { ReportActions, ReportCoverageNote } from './ReportActions';
import { InspectionReading, MeasurementReading, StaticDiagnosticReading } from './ReportInspections';
import { ReportGoDependencies } from './ReportGoDependencies';
import { ReportCodeStrengths } from './ReportCodeStrengths';
import { ReportSearch, focusReportFinding, focusReportSearchLocation, type ReportSearchTarget } from './ReportSearch';
import { projectBehaviors } from './reportBehaviors';
import {
  acceptedStaticResult,
  gapGroups,
  duplicateReadingFindings,
  locationPath,
  moduleDir,
  moduleSecondary,
  overviewFacts,
  readingSummary,
  regularStaticLanguages,
  secondaryDir,
  secondaryPath,
  secondaryReviewFinding,
  sevRank,
  severityOf,
  slotRuns,
  testResultSummary,
  SHOWN,
} from './reportGroups';

const s = t.sections;
const sm = s.summary;
const ORDER = ['environment', 'documents', 'static_analysis', 'metrics', 'secrets', 'code_review', 'gaps'] as const;
const TABS = ['overview', ...ORDER] as const;
const TAB_ICONS = { overview: 'overview', environment: 'server', documents: 'file', static_analysis: 'terminal', metrics: 'chart', secrets: 'secret', code_review: 'code', gaps: 'alert' } as const;

function shortList(list: string[]) {
  return list.length <= SHOWN ? list.join(', ') : list.slice(0, SHOWN).join(', ') + ' ' + sm.more(list.length - SHOWN);
}
// More is a disclosure whose rows render only when it is opened, so a long
// list costs nothing until asked for (keyboard reachable through summary).
function More({ title, children, reveal }: { title: string; children: () => ReactNode; reveal?: { id: string } }) {
  const [open, setOpen] = useState(false);
  const [closedRequest, setClosedRequest] = useState<{ id: string }>();
  const shown = open || reveal !== undefined && reveal !== closedRequest;
  return (
    <details className="report-more" open={shown} onToggle={e => {
      const isOpen = e.currentTarget.open;
      setOpen(isOpen);
      if (!isOpen) setClosedRequest(reveal);
    }}>
      <summary><ReportIcon name="chevron" />{title}</summary>
      {shown && children()}
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
    <div className={'table report-data-table' + (compact ? ' compact' : '')}>
      <table role="table">
        <thead>
          <tr role="row">
            {head.map((h, j) => (
              <th key={h} scope="col" role="columnheader" className={numeric[j] ? 'num' : undefined}>
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, i) => (
            <tr key={i} role="row">
              {row.map((cell, j) => (
                <td key={j} role="cell" data-label={head[j]} className={numeric[j] ? 'num' : undefined}>
                  <div className="report-cell-value">{cell}</div>
                </td>
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

function TestDeclaration({ path, line }: GoTestFailure) {
  const base = useContext(ReportSourceContext);
  const location = sourceLocation(path, line, line);
  const href = sourceEvidenceURL(base, path, line, line);
  const label = <ReportPath value={path}><span className="report-test-line">:{line}</span></ReportPath>;
  return href ? <a href={href} target="_blank" rel="noopener noreferrer" aria-label={t.sourceActionAt(t.viewTestCode, location)}>{label}</a> : label;
}

function TestRunOverview({ results, id }: { results: NonNullable<AnalysisReport['testResults']>; id: string }) {
  const reasons = new Set(results.tasks.map(task => task.reason));
  const reason = results.status === 'not_run' && reasons.size === 1 ? [...reasons][0] : null;
  const rows = results.tasks.flatMap(task => {
    const suite = task.cases?.runner ?? label(t.tests.suites, task.key.split('.')[1]);
    const directory = task.directory && task.directory !== '.' ? task.directory : null;
    const taskStatus = task.result !== 'unverified' ? label(t.tests.statuses, task.result) :
      task.status === 'succeeded' ? t.tests.unverified : label(s.taskStatus, task.status);
    const exit = task.exit === null ? null : t.tests.commandExit(task.exit);
    const explanation = task.reason && task.reason !== 'test_command_failed' ? explainReason(task.reason) : null;
    const status = (text: string, showCommand = true) => <span key="status">{text}
      {showCommand && task.cases && <small className="report-test-detail">{t.tests.caseCounts(task.cases.passed, task.cases.failed, task.cases.skipped)}</small>}
      {showCommand && exit && <small className="report-test-detail">{exit}</small>}
      {showCommand && explanation && <small className="report-test-detail">{explanation}</small>}
    </span>;
    if (task.failures.length && !task.cases?.cases.some(c => c.status === 'failed' && c.path)) return task.failures.map((failure, index): ReactNode[] => [
      <span key="test">{failure.name ? <code>{failure.name}</code> : t.tests.nameWithheld}
        {index === 0 && <small className="report-test-detail">{suite}{directory && <> · <ReportPath value={directory} /></>}</small>}
      </span>,
      <TestDeclaration key="position" {...failure} />,
      status(t.tests.testFailed, index === 0),
    ]);
    if (task.cases) {
      const rows: ReactNode[][] = [[<span key="suite">{suite}{directory && <small className="report-test-detail"><ReportPath value={directory} /></small>}</span>, '—', status(taskStatus)]];
      for (const c of task.cases.cases.filter(c => c.status === 'failed')) {
        rows.push([<code key="test">{c.name}</code>,
          c.path && c.line ? <TestDeclaration key="position" path={c.path} line={c.line} /> : '—', status(t.tests.testFailed, false)]);
      }
      return rows;
    }
    return [[
      <span key="suite">{suite}{directory && <small className="report-test-detail"><ReportPath value={directory} /></small>}
        {task.result === 'failed' && <small className="report-test-detail">{t.tests.unidentified}</small>}
      </span>,
      '—',
      status(taskStatus),
    ]];
  });
  return <details id={id} className="report-reading-records report-test-results" data-test-status={results.status} aria-label={t.tests.heading}>
    <summary>{testResultSummary(results)}
      {reason && <small className="report-test-reason">{explainReason(reason)}</small>}
    </summary>
    <Table head={[t.tests.test, t.tests.declaration, s.col.status]} rows={rows} />
  </details>;
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
            {o ? diagnosticDescription(o.tool ?? '', o.rule, o.message) ?? o.message ?? t.unknown : t.unknown}
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
  const withLines = ledger.files !== null;
  const head = [s.col.module, ...(classes ? [l.col.cls] : []), l.col.discovered, l.col.eligible, l.col.sent,
    ...(withLines ? [l.col.accepted] : []), l.col.bytesSent, l.col.truncated, l.col.withheld, l.col.unreadable,
    l.col.budgetExhausted, l.col.notPlanned, ...(classes ? [l.col.secondaryNotReached] : []), l.col.notSent,
    withLines ? l.col.candidateFollowup : l.col.completed];
  const row = (m: LedgerModule, name: ReactNode, discovered: string) => [
    name,
    ...(classes ? [label(l.classes, m.cls ?? '')] : []),
    discovered,
    m.eligible,
    m.sent,
    ...(withLines ? [n(m.accepted)] : []),
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
  if (withLines) total.accepted = secondary.reduce((n, m) => n + (m.accepted ?? 0), 0);
  const slices = new Map<string, { status: string; reason: string | null; modules: string[] }>();
  for (const m of ledger.modules) {
    for (const x of m.slices) {
      const r = slices.get(x.task) ?? { status: x.status, reason: x.reason, modules: [] };
      r.modules.push(m.id);
      slices.set(x.task, r);
    }
  }
  const eligible = ledger.modules.reduce((a, m) => a + m.eligible, 0);
  const sent = ledger.modules.reduce((a, m) => a + m.sent, 0);
  const accepted = ledger.modules.reduce((a, m) => a + (m.accepted ?? 0), 0);
  const acceptedModules = ledger.modules.filter((m) => (m.accepted ?? 0) > 0).length;
  const lineReason = (reason: string) => l.lineReasons[reason] ?? label(l.lineStates, reason);
  const files = ledger.files;
  return (
    <>
      <h6>{l.heading}</h6>
      <p>
        <strong>{withLines ? l.lineCoverage(ledger.modules.length, acceptedModules, eligible, sent, accepted) : l.coverage(ledger.modules.length, eligible, sent)}</strong>
      </p>
      <p className="small">{withLines ? l.lineLead : l.lead}</p>
      <ul>
        <li>{label(withLines ? l.lineStatus : l.status, ledger.status)}</li>
        <li>
          {l.scope} <code>{ledger.scope.key}</code> {label(s.taskStatus, ledger.scope.status ?? '')}
          {ledger.scope.reason ? ' — ' + explainReason(ledger.scope.reason) : ''}
        </li>
        <li>{l.calls(n(ledger.callsUsed), n(ledger.maxCalls))}</li>
        {ledger.extended && <li>{l.extendedCoverage}</li>}
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
      {files && files.length > 0 && (
        <More title={l.allFiles(files.length)}>
          {() => <Table head={[l.lines.file, s.col.status, l.lines.total, l.lines.requested, l.lines.accepted, l.lines.remaining]}
            rows={files.map((f) => [
              <code key={f.path}>{f.path}</code>, label(l.lineStates, f.investigation), f.total > 0 ? f.total : '—',
              f.sent, f.accepted, f.reason ? lineReason(f.reason) : f.remaining.map((r) =>
                `${r.start === r.end ? r.start : `${r.start}–${r.end}`} ${lineReason(r.reason)}`).join(' · ') || '—',
            ])}
          />}
        </More>
      )}
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

// Project reports use tabs. Archived reports without project metadata use
// the same reading order in native disclosures.
export function SectionedReport({ report, sections, history }: { report: AnalysisReport; sections: Sections; history?: ReportHistory }) {
  // Sections the overview points at start open; the rest stay collapsed.
  const [open, setOpen] = useState<Set<string>>(() => new Set(sections.attention.map((a) => a.section)));
  const tabbed = Boolean(report.project);
  const id = useId();
  const testsId = id + '-test-results';
  const documentTarget = useRef<string | null>(null);
  const [active,setActive] = useState('overview');
  const [readingSection, setReadingSection] = useState<string>();
  const [searchTarget, setSearchTarget] = useState<ReportSearchTarget>();
  const findingTarget = searchTarget?.kind === 'finding' ? searchTarget : undefined;
  const tabId = (key:string) => id + '-tab-' + key;
  const comparisonId = (index: number) => id + '-document-comparison-' + (index + 1);
  useLayoutEffect(() => {
    if (!tabbed) return;
    const tab = document.getElementById(`${id}-tab-${active}`);
    const strip = tab?.parentElement;
    if (!tab || !strip || strip.scrollWidth <= strip.clientWidth) return;
    const item = tab.getBoundingClientRect();
    const viewport = strip.getBoundingClientRect();
    if (item.left < viewport.left) strip.scrollLeft += item.left - viewport.left;
    if (item.right > viewport.right) strip.scrollLeft += item.right - viewport.right;
  }, [tabbed, id, active]);
  useLayoutEffect(() => {
    if (tabbed) return;
    const blocks = ORDER.map(key => document.getElementById(anchor(key)));
    let frame: number | undefined;
    const schedule = () => {
      if (frame !== undefined) return;
      frame = window.requestAnimationFrame(() => {
        frame = undefined;
        const readingLine = Math.min(96, window.innerHeight / 5);
        let section: string | undefined;
        for (const [index, block] of blocks.entries()) {
          if (block && block.getBoundingClientRect().top <= readingLine) section = ORDER[index];
        }
        setReadingSection(section);
      });
    };
    window.addEventListener('scroll', schedule, { passive: true });
    window.addEventListener('resize', schedule);
    document.addEventListener('toggle', schedule, true);
    return () => {
      if (frame !== undefined) window.cancelAnimationFrame(frame);
      window.removeEventListener('scroll', schedule);
      window.removeEventListener('resize', schedule);
      document.removeEventListener('toggle', schedule, true);
    };
  }, [tabbed, report.runID, report.revision]);
  useLayoutEffect(() => {
    if (documentTarget.current) {
      focusReportSearchLocation(document.getElementById(documentTarget.current));
      documentTarget.current = null;
    }
  }, [active, open]);
  useLayoutEffect(() => {
    if (searchTarget?.kind === 'comparison') {
      focusReportSearchLocation(document.getElementById(id + '-document-comparison-' + (searchTarget.index + 1)));
    } else if (searchTarget?.kind === 'finding') {
      focusReportFinding(document.getElementById(anchor(searchTarget.section)), searchTarget.id);
    }
  }, [id, searchTarget]);
  const navigateSearch = (target: ReportSearchTarget) => {
    const section = target.kind === 'comparison' ? 'documents' : target.section;
    setSearchTarget({ ...target });
    if (tabbed) setActive(section);
    else setOpen(prev => new Set(prev).add(section));
  };
  const select = (key:string) => {
    setSearchTarget(undefined);
    setActive(key);
    document.getElementById(tabId(key))?.focus();
  };
  const byID = new Map(report.observations.map((o) => [o.id, o]));
  const go = (key: string) => {
    if (tabbed) {select(key); return;}
    setSearchTarget(undefined);
    setOpen((prev) => new Set(prev).add(key));
    setReadingSection(key);
    document.getElementById(anchor(key))?.querySelector('summary')?.focus();
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
    const st = key === 'static_analysis' ? null : overview.get(key)?.state ?? null;
    if (tabbed) return <section className="card report-card report-tab-content" id={anchor(key)} role="tabpanel" aria-labelledby={tabId(key)} hidden={active!==key} tabIndex={0} key={key}>
      {active===key && <div className="card-body"><h5>{label(s.titles,key)}</h5>{body}</div>}
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
  const behaviors = projectBehaviors(d, report.includesCode !== false);
  const staticLanguages = regularStaticLanguages(sections);
  const staticTools = staticLanguages.flatMap(language => language.tools);
  const staticAccepted = staticTools.filter(acceptedStaticResult).length;
  const m = sections.metrics;
  const sc = sections.secrets;
  // Not screened because a secrets task ran but its result is unusable: name
  // that task with its state and reason (review still ran on the read filters).
  const unusableScreen = sc.modelInput === 'not_screened'
    ? sc.tasks.map((k) => report.tasks.find((x) => x.key === k)).find((r) => r && r.status !== 'succeeded' && r.status !== 'unsupported')
    : undefined;
  const cr = sections.codeReview;
  const investigationRequests = report.calls.filter(call => call.requested && !call.purpose && call.role === 'investigate').length;
  const verificationRequests = report.calls.filter(call => call.requested && !call.purpose && call.role === 'verify').length;
  const staticFindings = sections.staticAnalysis.languages.flatMap(language => language.tools.flatMap(tool => tool.findings));
  const sevOf = severityOf(report.findings, staticFindings);
  const facts = overviewFacts(report, sections);
  const readingSignals = readingSummary(report);
  const modSecondary = (id: string) => secondaryDir(moduleDir(id));
  // Review findings: the supported and reproduced ones on primary paths
  // open the section; the others and those on secondary paths come after.
  const allReviewFindings = pick(report.findings, cr.findings).sort((a, b) => sevRank(b.severity) - sevRank(a.severity));
  const duplicates = duplicateReadingFindings(allReviewFindings, report.observations);
  const groupedRecords = allReviewFindings.filter(f => duplicates.has(f.id));
  const reviewFindings = allReviewFindings.filter(f => !duplicates.has(f.id));
  const supported = (f: Finding) => f.validation === 'supported' || f.validation === 'reproduced';
  const currentDisposition = new Map(history?.load.state === 'ready' ? history.load.value.findings.map(f => [f.findingID, f.current]) : []);
  const isDisposed = (f: Finding) => {
    const disposition = currentDisposition.get(f.id) ?? f.disposition;
    return HIDDEN.has(disposition) || disposition === 'fixed_by_user';
  };
  const refuted = reviewFindings.filter(f => f.validation === 'refuted');
  const disposed = reviewFindings.filter(f => f.validation !== 'refuted' && isDisposed(f));
  const reviewable = reviewFindings.filter(f => f.validation !== 'refuted' && !isDisposed(f));
  const reviewPrimary = reviewable.filter(f => !secondaryReviewFinding(f));
  const reviewFirst = reviewPrimary.filter(supported);
  const reviewRest = reviewPrimary.filter(f => !supported(f));
  const reviewSecondary = reviewable.filter(secondaryReviewFinding);
  const hasCodeReview = cr.findings.length > 0 || cr.strengths.length > 0 || cr.roles.some(role =>
    (role.result === 'accepted' && role.key === 'review.investigate') ||
    ((role.result === 'accepted' || role.result === 'accepted_incomplete') && role.key === 'review.verify')
  ) || cr.ledger?.modules.some(module => module.completed > 0);
  const allDiagnostics = staticTools.flatMap(tool => tool.observations);
  const suppressed = allDiagnostics.filter((id) => byID.get(id)?.suppressed && !byID.get(id)?.metric).length;

  const detectionsPrimary = sc.detections.filter((x) => !secondaryPath(locationPath(x.place)));
  const detectionsSecondary = sc.detections.filter((x) => secondaryPath(locationPath(x.place)));
  const detectionRow = (x: (typeof sc.detections)[number]) => [<code key="r">{x.rule}</code>, <code key="p">{x.place}</code>];
  // Environment and document rows of modules below a secondary path move behind
  // a disclosure; a mismatch stays in view because the overview points at it.
  const envModules = e.modules.filter((x) => !modSecondary(x.id));
  const envRequirements = e.requirements.filter((x) => !modSecondary(x.module));
  const envVerdicts = e.verdicts.filter((x) => !modSecondary(x.module) || x.status === 'mismatch');
  const envSecondary = e.modules.length - envModules.length + e.requirements.length - envRequirements.length + e.verdicts.length - envVerdicts.length;
  const docItems = d.items.filter((x) => !secondaryPath(x.path));
  const docModules = d.modules.filter((x) => !modSecondary(x.module));
  const docSecondary = d.items.length - docItems.length + d.modules.length - docModules.length;
  const moduleRow = (x: (typeof e.modules)[number]) => [<code key="m">{x.id}</code>, x.ecosystem, <span key="p"><code>{x.path}</code>{x.buildSystem && <small className="report-test-detail">{x.buildSystem}{x.buildRoot && x.buildRoot !== x.path && <> · <code>{x.buildRoot}</code></>}</small>}</span>, label(s.inScope, String(x.inScope))];
  const requirementRow = (x: (typeof e.requirements)[number]) => [<code key="m">{x.module}</code>, x.tool, <code key="c">{x.constraint ?? s.withheld}</code>, <code key="s">{x.source}</code>];
  const verdictRow = (x: (typeof e.verdicts)[number]) => [<code key="m">{x.module}</code>, x.tool, label(s.verdictStatus, x.status), or(x.reason)];
  const itemRow = (x: (typeof d.items)[number]) => [<code key="p">{x.path}</code>, x.cls, <code key="a">{x.appliesTo}</code>, label(s.docStatus, x.status), x.reason ? explainReason(x.reason) : '—', x.size ?? '—'];
  const docModuleRow = (x: (typeof d.modules)[number]) => [<code key="m">{x.module}</code>, label(s.docState, x.state), or(x.own.join(', ')), or(x.inherited.join(', '))];
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
        <ReportDocumentReading results={report.testResults} documents={d} includesCode={report.includesCode !== false} id={id} comparisonId={comparisonId} />
        {!!d.documentationGaps?.length && <section className="report-reading-group">
          <h6>{t.documentationGaps.heading}</h6>
          <p>{t.documentationGaps.note}</p>
          <Table head={[t.documentationGaps.interface, t.documentationGaps.defaultValue, s.col.source, t.documentationGaps.searched]}
            rows={d.documentationGaps.map(gap => [
              <code key="symbol">{gap.symbol ?? t.documentationGaps[gap.kind]}</code>,
              gap.defaultValue && <code key="default">{gap.defaultValue}</code>,
              <code key="source">{sourceLocation(gap.source.path, gap.source.line, gap.source.end)}</code>,
              <ul key="searched" className="report-reading-references">{gap.searched.map(file =>
                <li key={file.path}><code>{file.lines ? sourceLocation(file.path, 1, file.lines) : file.path}</code></li>)}</ul>,
            ])} />
        </section>}
        {!d.model.performed && (d.model.reason === 'documents_not_selected' && d.items.length > 0 &&
          d.items.every(item => item.reason === 'credential_like_content')
          ? <p>{s.noModel(s.modelReasons.documentsWithheld)}</p>
          : <ModelNote model={d.model} />)}
        <details className="report-reading-records" open={d.comparisons.length === 0 && d.quality.length === 0 && !d.model.performed ? true : undefined}>
          <summary>{t.documentReading.discovery}</summary>
        <StageLine stage={d.stage} />
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
        </details>
      </>
    ),
    static_analysis: (
      <>
        <p>{staticTools.length ? t.reading.staticResults(staticAccepted, staticTools.length) : inspectionsKo.noStaticTasks}</p>
        <StaticDiagnosticReading languages={staticLanguages} findings={report.findings} byID={byID} severityOf={sevOf} />
        {staticLanguages.map(lang => (
          <InspectionReading key={lang.ecosystem ?? 'unknown'} language={lang} byID={byID} />
        ))}
        <details><summary>{inspectionsKo.allAnalyzerCounts}</summary><SectionCounts sections={sections} keyName="static_analysis" /></details>
        <details><summary>{t.glossary}</summary><Table head={[s.col.tool,s.col.message]} rows={Object.entries(t.tools)} /></details>
        <p className="small">{sm.rulesNote}</p>
        {suppressed > 0 && <p className="small">{sm.suppressedRows(suppressed)}</p>}
        <More title={sm.allDiagnostics(allDiagnostics.length)} reveal={findingTarget?.section === 'static_analysis' ? findingTarget : undefined}>
          {() => (
            <>
              {staticLanguages.map((lang) => (
                <section key={lang.ecosystem ?? 'unknown'}>
                  <h6>{s.language(lang.ecosystem)}</h6>
                  <Table
                    head={[s.col.task, s.col.tool, s.col.required, s.col.status, s.col.result, s.col.version, s.col.files, s.col.diagnostics, s.col.rules]}
                    rows={lang.tools.map((x) => {
                      const diagnostics = acceptedStaticResult(x) ? String(x.observations.length) : s.notRun;
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
              <h6>{inspectionsKo.allAnalyzerFindings}</h6>
              <FindingList findings={pick(report.findings, staticFindings)} history={history} empty={s.noFindings} reveal={findingTarget} />
            </>
          )}
        </More>
      </>
    ),
    metrics: (
      <>
        <StageLine stage={m.stage} />
        <p>{s.metricsLead}</p>
        <ReportGoDependencies graphs={m.goDependencies ?? []} />
        <MeasurementReading sections={sections} byID={byID} />
        <details><summary>{inspectionsKo.counts}</summary><SectionCounts sections={sections} keyName="metrics" /></details>
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
        <Table head={[s.col.rule, s.col.location]} rows={detectionsPrimary.map(detectionRow)} empty={detectionsSecondary.length > 0 ? undefined : s.noSecrets} />
        {detectionsSecondary.length > 0 && (
          <More title={sm.secondaryDetections(detectionsSecondary.length)}>{() => <Table head={[s.col.rule, s.col.location]} rows={detectionsSecondary.map(detectionRow)} />}</More>
        )}
      </>
    ),
    code_review: (
      <>
        <p className="small">{t.codeReading.requestCounts(investigationRequests, verificationRequests)}</p>
        <h6>{t.codeReading.results}</h6>
        <FindingList findings={[...reviewFirst, ...reviewRest, ...reviewSecondary]} history={history} empty={hasCodeReview ? t.codeReading.none : t.codeReading.noEvidence} reveal={findingTarget} groupByCategory={false} inspect={tabbed} />
        <ReportCodeStrengths review={cr} includesCode={report.includesCode !== false} id={id} />
        {refuted.length > 0 && (
          <More title={t.codeReading.refuted(refuted.length)} reveal={refuted.some(f => f.id === findingTarget?.id) ? findingTarget : undefined}>
            {() => <FindingList findings={refuted} history={history} empty={s.noFindings} reveal={findingTarget} groupByCategory={false} />}
          </More>
        )}
        {disposed.length > 0 && (
          <More title={t.codeReading.disposition(disposed.length)} reveal={disposed.some(f => f.id === findingTarget?.id) ? findingTarget : undefined}>
            {() => <FindingList findings={disposed} history={history} empty={s.noFindings} reveal={findingTarget} groupByCategory={false} />}
          </More>
        )}
        {groupedRecords.length > 0 && (
          <More title={t.codeReading.relatedRecords(groupedRecords.length)} reveal={groupedRecords.some(f => f.id === findingTarget?.id) ? findingTarget : undefined}>
            {() => <FindingList findings={groupedRecords} history={history} empty={s.noFindings} reveal={findingTarget} groupByCategory={false} />}
          </More>
        )}
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

        <details className="report-reading-records">
          <summary>{t.codeReading.execution}</summary>
        <StageLine stage={cr.stage} />
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
            <Table
              head={[s.col.role, s.col.status, s.col.result]}
              rows={cr.roles.map((x) => [
                <code key="k">{x.key}</code>,
                <>
                  {label(s.taskStatus, x.status ?? '')}
                  {x.reason === 'no_review_scope' && <small> · {explainReason(x.reason)}</small>}
                </>,
                label(s.result, x.result),
              ])}
            />
          </>
        )}
        {cr.ledger && <LedgerBlock ledger={cr.ledger} />}
        </details>
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
  const railCounts = new Map<string, string>();
  for (const row of readingSignals) {
    if (row.count && !railCounts.has(row.section)) railCounts.set(row.section, row.count);
  }
  if (reviewable.length) railCounts.set('code_review', String(reviewable.length));
  const rail = tabbed ? <div role="tablist" aria-label={t.title} className="nav report-tabs">
    {TABS.map((key, i) => <button key={key} id={tabId(key)} type="button" role="tab" className="nav-link" aria-label={key === 'overview' ? s.overview : label(s.titles, key)} aria-selected={active === key}
      aria-controls={anchor(key)} tabIndex={active === key ? 0 : -1} onClick={() => select(key)}
      onKeyDown={e => {
        const next = (i + 1) % TABS.length;
        const previous = (i + TABS.length - 1) % TABS.length;
        const to = { ArrowRight: next, ArrowDown: next, ArrowLeft: previous, ArrowUp: previous, Home: 0, End: TABS.length - 1 }[e.key];
        if (to !== undefined) { e.preventDefault(); select(TABS[to]); }
      }}><ReportIcon name={TAB_ICONS[key]} /><span>{key === 'overview' ? s.overview : label(s.titles, key)}</span>{railCounts.has(key) && <small className="report-rail-count" aria-hidden="true">{railCounts.get(key)}</small>}</button>)}
  </div> : <nav aria-label={t.title} className="nav report-tabs">
    {ORDER.map(key => <button key={key} type="button" className="nav-link" aria-controls={anchor(key)}
      aria-current={readingSection === key ? 'location' : undefined}
      onClick={() => go(key)}><ReportIcon name={TAB_ICONS[key]} /><span>{label(s.titles, key)}</span></button>)}
  </nav>;
  return (
    <ReportLayout report={report} rail={<>
      {rail}
      {(d.comparisons.length > 0 || report.findings.length > 0) &&
        <ReportSearch key={report.runID + ':' + report.revision} report={report} navigate={navigateSearch} />}
    </>} className="sectioned-report" purpose={behaviors.description}>
      <section id={anchor('overview')} role={tabbed ? 'tabpanel' : undefined} aria-labelledby={tabbed ? tabId('overview') : 'report-overview'} hidden={tabbed && active!=='overview'} tabIndex={tabbed ? 0 : undefined}>
        <h5 id="report-overview" className="visually-hidden">{s.overview}</h5>
        {(!tabbed || active === 'overview') && <>
          <ReportCoverageNote report={report} sections={sections} openCoverage={() => {
            const table = document.getElementById('report-coverage');
            table?.scrollIntoView?.({ block: 'start' });
            table?.focus({ preventScroll: true });
          }} />
          <ReportActions report={report} sections={sections} isDisposed={isDisposed} openSection={go}
            openFinding={findingID => navigateSearch({ kind: 'finding', section: 'code_review', id: findingID })}
            openComparison={index => {
              documentTarget.current = comparisonId(index);
              go('documents');
            }} />
          <ReportOverviewTable rows={readingSignals} open={section => {
            if (section !== 'tests') { go(section); return; }
            const tests = document.getElementById(testsId) as HTMLDetailsElement | null;
            if (tests) tests.open = true;
            focusReportSearchLocation(tests?.querySelector('summary'));
          }} />
          {report.testResults && <TestRunOverview results={report.testResults} id={testsId} />}
          <details className="report-reading-records report-overview-context">
            <summary>{t.reading.contextDetails}</summary>
            <ProjectOverview report={report} sections={sections} behaviors={behaviors} openDocuments={index => {
              documentTarget.current = index === undefined ? null : comparisonId(index);
              go('documents');
            }} />
          </details>
          <More title={t.reading.operations}>{() => <div className="report-more-body">
            <ReportReviewStatus report={report} />
            <ReportCounts report={report} />
            <ul className="report-summary">
              {summaryLines(facts, report.aiReview).map(line => <li key={line}>{line}</li>)}
            </ul>
            {sections.attention.length > 0 && <>
              <h6>{s.attention}</h6>
              <ul className="attention">
                {sections.attention.map(a => <li key={a.section + ':' + a.code}>
                  <span>{(s.attentionText[a.code] ?? (() => a.code))(a.count)}</span>
                  <ReportAction onClick={() => go(a.section)}>{s.go(label(s.titles, a.section))}</ReportAction>
                </li>)}
              </ul>
            </>}
            <h6>{s.stateTable}</h6>
            <Table
              head={[s.section, s.col.status, s.counts]}
              rows={ORDER.map(key => {
                const row = overview.get(key);
                return [
                  <ReportAction key="b" onClick={() => go(key)}>{key === 'static_analysis' ? inspectionsKo.allAnalyzerCounts : label(s.titles, key)}</ReportAction>,
                  <span key="s" className={stateClass(row?.state ?? null)}>{state(row?.state ?? null)}</span>,
                  row && (s.countOrder[key] ?? [])
                    .filter(k => Object.hasOwn(row.counts, k))
                    .map(k => `${label(s.countLabels, key + '.' + k)} ${row.counts[k]}`).join(' · '),
                ];
              })}
            />
          </div>}</More>
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
    </ReportLayout>
  );
}
