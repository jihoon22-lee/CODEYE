import { useState, type ReactNode } from 'react';
import type { AnalysisReport, Finding, Observation } from './analysisReport';
import type { Dependency, Ledger, ModelState, SectionStage, Sections } from './reportSections';
import { ComparisonSection } from './HistoryView';
import { ko } from './locales/ko';
import { reportKo as t } from './locales/report-ko';
import { FindingList, ReportCalls, ReportCounts, ReportExports, ReportScope, ReportTasks, TextList, explainReason, label, type ReportHistory } from './ReportParts';

const s = t.sections;
const ORDER = ['environment', 'documents', 'static_analysis', 'metrics', 'secrets', 'code_review', 'gaps'];

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
function LedgerBlock({ ledger }: { ledger: Ledger }) {
  const l = s.ledger;
  const n = (v: number | null) => (v === null ? l.unknown : String(v));
  const slices = new Map<string, { status: string; reason: string | null; modules: string[] }>();
  for (const m of ledger.modules) {
    for (const x of m.slices) {
      const row = slices.get(x.task) ?? { status: x.status, reason: x.reason, modules: [] };
      row.modules.push(m.id);
      slices.set(x.task, row);
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
      </ul>
      <Table
        head={[s.col.module, l.col.discovered, l.col.eligible, l.col.sent, l.col.bytesSent, l.col.truncated, l.col.withheld, l.col.unreadable, l.col.budgetExhausted, l.col.notPlanned, l.col.notSent, l.col.completed]}
        rows={ledger.modules.map((m) => [
          <code key="m">{m.id}</code>,
          m.discovered ? l.discovered : l.notDiscovered,
          m.eligible,
          m.sent,
          m.bytesSent,
          m.truncated,
          m.withheld,
          m.unreadable,
          m.budgetExhausted,
          m.notPlanned,
          m.notSent,
          m.completed,
        ])}
        empty={l.none}
      />
      <Table
        head={[l.col.task, s.col.status, l.col.reason, l.col.modules]}
        rows={[...slices.entries()]
          .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
          .map(([k, x]) => [<code key="k">{k}</code>, label(s.taskStatus, x.status), x.reason ? explainReason(x.reason) : '—', <code key="m">{x.modules.join(', ')}</code>])}
      />
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
  const cr = sections.codeReview;
  const staticFindings = sa.languages.flatMap((l) => l.tools.flatMap((x) => x.findings));
  const bodies: Record<string, ReactNode> = {
    environment: (
      <>
        <StageLine stage={e.stage} />
        <p>{label(s.discovery, e.discovery)}</p>
        <SectionCounts sections={sections} keyName="environment" />
        {e.discovery === 'accepted' && (
          <>
            <h6>{s.modules}</h6>
            <Table head={[s.col.module, s.col.ecosystem, s.col.path, s.col.scope]} rows={e.modules.map((x) => [<code key="m">{x.id}</code>, x.ecosystem, <code key="p">{x.path}</code>, label(s.inScope, String(x.inScope))])} />
            <h6>{s.requirements}</h6>
            <Table
              head={[s.col.module, s.col.tool, s.col.requirement, s.col.source]}
              rows={e.requirements.map((x) => [<code key="m">{x.module}</code>, x.tool, <code key="c">{x.constraint ?? s.withheld}</code>, <code key="s">{x.source}</code>])}
              empty={ko.stages.states.no_tasks}
            />
            <h6>{s.observed}</h6>
            <Table head={[s.col.tool, s.col.status, s.col.version, s.col.detail]} rows={e.observed.map((x) => [x.tool, label(s.obsStatus, x.status), or(x.version), or(x.detail)])} />
            <h6>{s.verdicts}</h6>
            <Table
              head={[s.col.module, s.col.tool, s.col.status, s.col.reason]}
              rows={e.verdicts.map((x) => [<code key="m">{x.module}</code>, x.tool, label(s.verdictStatus, x.status), or(x.reason)])}
            />
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
            <Table
              head={[s.col.path, s.col.cls, s.col.appliesTo, s.col.status, s.col.reason, s.col.size]}
              rows={d.items.map((x) => [<code key="p">{x.path}</code>, x.cls, <code key="a">{x.appliesTo}</code>, label(s.docStatus, x.status), x.reason ? explainReason(x.reason) : '—', x.size ?? '—'])}
              empty={t.unknown}
            />
            <h6>{s.docModules}</h6>
            <Table
              head={[s.col.module, s.col.status, s.col.own, s.col.inherited]}
              rows={d.modules.map((x) => [<code key="m">{x.module}</code>, label(s.docState, x.state), or(x.own.join(', ')), or(x.inherited.join(', '))])}
            />
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
        {sa.languages.map((lang) => (
          <section key={lang.ecosystem ?? 'unknown'}>
            <h6>{s.language(lang.ecosystem)}</h6>
            {lang.ecosystem === 'any' && <p className="small">{s.anyNote}</p>}
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
        <ObservationTable ids={sa.languages.flatMap((l) => l.tools.flatMap((x) => x.observations))} byID={byID} />
        <h6>{s.staticFindings}</h6>
        <FindingList findings={pick(report.findings, staticFindings)} history={history} empty={s.noFindings} />
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
          head={[s.col.metric, s.col.unit, s.col.threshold, s.col.max, s.col.count]}
          rows={m.groups.map((g) => [<code key="n">{g.name}</code>, g.unit, g.thresholds.join(', '), g.max, g.observations.length])}
          empty={s.noMetrics}
        />
        {m.groups.length > 0 && <ObservationTable ids={m.groups.flatMap((g) => g.observations)} byID={byID} />}
      </>
    ),
    secrets: (
      <>
        <StageLine stage={sc.stage} />
        <p>{s.secretsLead}</p>
        <p>{label(s.modelInput, sc.modelInput)}</p>
        <SectionCounts sections={sections} keyName="secrets" />
        <Table
          head={[s.col.task, s.col.status, s.col.reason]}
          rows={sc.tasks.map((k) => {
            const row = report.tasks.find((x) => x.key === k);
            return [<code key="k">{k}</code>, label(s.taskStatus, row?.status ?? ''), row?.reason ? explainReason(row.reason) : '—'];
          })}
          empty={s.gapLabels.not_planned}
        />
        <Table
          head={[s.col.rule, s.col.location, s.col.finding]}
          rows={sc.detections.map((x) => [<code key="r">{x.rule}</code>, <code key="p">{x.place}</code>, <code key="f">{or(x.finding)}</code>])}
          empty={s.noSecrets}
        />
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
        <h6>{s.reviewFindings}</h6>
        <FindingList findings={pick(report.findings, cr.findings)} history={history} empty={s.noFindings} />
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
        <Table
          head={[s.col.section, s.col.gap, s.col.subject, s.col.detail]}
          rows={sections.gaps.map((g) => [label(s.titles, g.section), label(s.gapLabels, g.code), <code key="s">{or(g.subject)}</code>, g.detail ? explainReason(g.detail) : '—'])}
          empty={s.noGaps}
        />
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
