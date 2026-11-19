import { useState, type ReactNode } from 'react';
import type { AnalysisReport, Finding, Observation } from './analysisReport';
import type { Dependency, Ledger, LedgerModule, ModelState, SectionStage, Sections } from './reportSections';
import { ComparisonSection } from './HistoryView';
import { ko } from './locales/ko';
import { reportKo as t } from './locales/report-ko';
import { FindingList, ReportCalls, ReportCounts, ReportExports, ReportScope, ReportTasks, TextList, explainReason, label, type ReportHistory } from './ReportParts';
import {
  findingSecondary,
  gapGroups,
  locationPath,
  moduleDir,
  moduleSecondary,
  orderFindings,
  overviewFacts,
  places,
  ruleGroups,
  secondaryDir,
  secondaryPath,
  severityOf,
  slotRuns,
  toolRows,
  SHOWN,
  type Places,
} from './reportGroups';

const s = t.sections;
const sm = s.summary;
// Sections in order of value (docs/REPORT.md#내보내기-상세도): what the
// review and the tools found first, the context after.
const ORDER = ['code_review', 'static_analysis', 'metrics', 'secrets', 'environment', 'documents', 'gaps'];

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
function or(value: string | null | undefined, fallback = '—') {
  return value == null || value === '' ? fallback : value;
}
function anchor(key: string) {
  return 'report-section-' + key;
}

function Table({ head, rows, empty }: { head: string[]; rows: ReactNode[][]; empty?: string }) {
  if (rows.length === 0) return empty ? <p>{empty}</p> : null;
  return (
    <div className="table">
      <table>
        <thead>
          <tr>
            {head.map((h) => (
              <th key={h} scope="col">
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, i) => (
            <tr key={i}>
              {row.map((cell, j) => (
                <td key={j}>{cell}</td>
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

function Codes({ ids }: { ids: string[] }) {
  if (ids.length === 0) return null;
  return <small> {s.evidence(ids)}</small>;
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
      <p>{l.lead}</p>
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
            <Table head={head} rows={ledger.modules.map((m) => row(m, <code key="m">{m.id}</code>, m.discovered ? l.discovered : l.notDiscovered))} empty={l.none} />
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
function summaryLines(f: ReturnType<typeof overviewFacts>, pack: string): string[] {
  const out: string[] = [];
  if (!f.model && pack !== 'none') out.push(sm.modelNone);
  if (f.model) {
    const rest = (['reproduced', 'inconclusive', 'refuted', 'unreviewed'] as const).filter((k) => f.model![k] > 0).map((k) => sm.validation[k](f.model![k]));
    out.push(sm.modelFindings(f.model.total, f.model.supported + f.model.reproduced) + (rest.length > 0 ? ' (' + rest.join(', ') + ')' : '') + '. ' + sm.modelNote);
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

// SectionedReport is the report of builder 5 and later: the overview first,
// then one collapsible section per analysis kind (keyboard reachable through
// the native summary element), the gaps and the raw records. The comparison
// with the previous run is a screen section, not part of the stored document.
export function SectionedReport({ report, sections, history }: { report: AnalysisReport; sections: Sections; history?: ReportHistory }) {
  // Sections the overview points at start open; the rest stay collapsed.
  const [open, setOpen] = useState<Set<string>>(() => new Set(sections.attention.map((a) => a.section)));
  const byID = new Map(report.observations.map((o) => [o.id, o]));
  const go = (key: string) => {
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
    return (
      <details key={key} id={anchor(key)} className="report-section" open={open.has(key)} onToggle={(e) => toggle(key, (e.currentTarget as HTMLDetailsElement).open)}>
        <summary>
          <strong>{label(s.titles, key)}</strong>
          {st !== null && <span> — {state(st)}</span>}
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
  const supported = (f: Finding) => (f.validation === 'supported' || f.validation === 'reproduced') && !findingSecondary(f);
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
                <Codes ids={n.evidence} />
              </li>
            ))}
          </ul>
        )}
      </>
    ),
    documents: (
      <>
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
        <ModelNote model={d.model} />
        {d.model.performed && (
          <>
            <h6>{s.contracts}</h6>
            <ul>
              {d.contracts.map((x, i) => (
                <li key={i}>
                  [{x.appliesTo}] {x.statement}
                  <Codes ids={x.evidence} />
                </li>
              ))}
            </ul>
            <h6>{s.conflicts}</h6>
            <ul>
              {d.conflicts.map((x, i) => (
                <li key={i}>
                  [{x.scope}] {x.summary} / {x.impact}
                  <Codes ids={x.evidence} />
                </li>
              ))}
            </ul>
            <h6>{s.questions}</h6>
            <ul>
              {d.questions.map((x, i) => (
                <li key={i}>
                  [{x.appliesTo}] {x.question}
                  <Codes ids={x.evidence} />
                </li>
              ))}
            </ul>
            <h6>{s.instructionNotes}</h6>
            <ul>
              {d.notes.map((x, i) => (
                <li key={i}>
                  {x.note}
                  <Codes ids={x.evidence} />
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
                <Table
                  head={[s.col.tool, s.col.rule, s.col.count, s.col.severity, s.col.message, s.col.location]}
                  rows={groups.map((g) => [<code key="a">{g.analyzer}</code>, <code key="r">{g.rule}</code>, g.count, or(g.severity), cutLine(g.message), placesText(places(g.primary, g.secondary))])}
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
        {cr.pack === 'none' ? (
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
            <Table head={[s.col.role, s.col.status, s.col.result]} rows={cr.roles.map((x) => [<code key="k">{x.key}</code>, label(s.taskStatus, x.status ?? ''), x.result])} />
          </>
        )}
        {cr.ledger && <LedgerBlock ledger={cr.ledger} />}
        <h6>{sm.supported}</h6>
        <FindingList findings={reviewFirst} history={history} empty={cr.findings.length === 0 ? s.noFindings : sm.supportedNone} />
        {reviewRest.length > 0 && (
          <More title={sm.otherFindings(reviewRest.length)}>{() => <FindingList findings={reviewRest} history={history} empty={s.noFindings} />}</More>
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
    <article className="report-document" aria-label={t.title}>
      <header>
        <h4>{t.title}</h4>
        <span>{t.revision(report.revision)}</span>
      </header>
      <section aria-labelledby="report-overview">
        <h5 id="report-overview">{s.overview}</h5>
        <p>
          <strong>{label(ko.runs.verdictLabels, report.verdict)}</strong> · {t.coverage} {label(t.ai, report.aiReview)}
        </p>
        <ul className="report-summary">
          {summaryLines(facts, cr.pack).map((line, i) => (
            <li key={i}>{line}</li>
          ))}
        </ul>
        <p className="small" role="note">
          {sm.note}
        </p>
        <ReportCounts report={report} />
        <h6>{s.stateTable}</h6>
        <Table
          head={[s.section, s.col.status, s.counts]}
          rows={ORDER.map((key) => {
            const row = overview.get(key);
            return [
              <button key="b" type="button" className="link" onClick={() => go(key)}>
                {label(s.titles, key)}
              </button>,
              state(row?.state ?? null),
              (s.countOrder[key] ?? [])
                .filter((k) => row && Object.hasOwn(row.counts, k))
                .map((k) => label(s.countLabels, key + '.' + k) + ' ' + row!.counts[k])
                .join(' · '),
            ];
          })}
        />
        <h6>{s.attention}</h6>
        {sections.attention.length === 0 ? (
          <p>{s.noAttention}</p>
        ) : (
          <ul>
            {sections.attention.map((a) => (
              <li key={a.section + ':' + a.code}>
                {(s.attentionText[a.code] ?? (() => a.code))(a.count)}{' '}
                <button type="button" className="link" onClick={() => go(a.section)}>
                  {s.go(label(s.titles, a.section))}
                </button>
              </li>
            ))}
          </ul>
        )}
        {report.priorities.length > 0 && (
          <>
            <h6>{s.priorities}</h6>
            <ol className="report-priorities">
              {report.priorities.map((id) => {
                const f = report.findings.find((x) => x.id === id)!;
                // A priority finding is listed in the code review or the static analysis section.
                const section = sections.codeReview.findings.includes(id) ? 'code_review' : 'static_analysis';
                return (
                  <li key={id}>
                    {f.title} — {label(t.validation, f.validation)} · {label(t.severity, f.severity)}{' '}
                    <button type="button" className="link" onClick={() => go(section)}>
                      {s.go(label(s.titles, section))}
                    </button>
                  </li>
                );
              })}
            </ol>
          </>
        )}
      </section>
      {history && <ComparisonSection load={history.load} reload={history.reload} />}
      {ORDER.map((key) => block(key, bodies[key]))}
      <details className="report-section">
        <summary>
          <strong>{s.appendix}</strong>
        </summary>
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
