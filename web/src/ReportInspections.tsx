import { Fragment, useState, type ReactNode } from 'react';
import type { Finding, Observation, ObservationPlace } from './analysisReport';
import type { Excerpt, Sections, StaticTool } from './parseReportSections';
import { explainReason, label, PinnedSourceLink, ReportPreview, sourceLocation } from './ReportParts';
import { cutLine } from './format';
import { ruleGroups, secondaryPath } from './reportGroups';
import { reportKo } from './locales/report-ko';
import { inspectionsKo as i } from './locales/report-inspections-ko';

const s = reportKo.sections;
const knownTypes = new Set(['javascript.tsc', 'python.mypy', 'python.pyright', 'rust.cargocheck']);
const analyzerForTask: Record<string, string> = {
  'go-vet': 'go.vet', staticcheck: 'go.staticcheck', gofmt: 'go.gofmt', tsc: 'javascript.tsc',
  mypy: 'python.mypy', pyright: 'python.pyright', bandit: 'python.bandit', 'cargo-check': 'rust.cargocheck',
  lizard: 'any.lizard', jscpd: 'any.jscpd',
};

function identity(tool: StaticTool) {
  const task = tool.task.split('.')[0];
  return tool.analyzer ?? (Object.hasOwn(analyzerForTask, task) ? analyzerForTask[task] : task);
}

function category(tool: StaticTool) {
  const id = identity(tool);
  if (id === 'go.gofmt' || id === 'gofmt') return 'format';
  if (knownTypes.has(id)) return 'type';
  if (id === 'python.bandit') return 'other';
  return 'lint';
}

function shown(tool: StaticTool) {
  return tool.status === 'succeeded' && tool.result === 'accepted';
}

function scope(tool: StaticTool) {
  if (tool.processed === null) return i.unknownFiles;
  return identity(tool) === 'any.jscpd'
    ? i.filesSubmitted(tool.processed, tool.inScope)
    : i.checkedFiles(tool.processed, tool.inScope);
}

function status(tool: StaticTool) {
  if (tool.reason) return `${label(s.taskStatus, tool.status)} · ${explainReason(tool.reason)}`;
  return shown(tool) ? label(s.taskStatus, tool.status) : `${label(s.taskStatus, tool.status)} · ${label(s.result, tool.result)}`;
}

function inspectionResults(tools: StaticTool[]) {
  const results = new Map<string, StaticTool[]>();
  for (const tool of tools) {
    const key = JSON.stringify([identity(tool), tool.version, tool.status, tool.result, tool.reason, tool.rulePack]);
    const group = results.get(key);
    if (group) group.push(tool);
    else results.set(key, [tool]);
  }
  return [...results.values()];
}

function TaskScope({ tool, kind, byID, grouped }: { tool: StaticTool; kind: string; byID: Map<string, Observation>; grouped: boolean }) {
  const total = tool.observations.filter(id => !byID.get(id)?.suppressed).length;
  const limit = kind === 'format' ? tool.units.format_input_limit : 0;
  return <>
    {grouped && <code>{tool.task}</code>}
    {shown(tool) && <> · {i.issues(total, kind === 'format')} · {scope(tool)}</>}
    {!shown(tool) && total > 0 && <> · {i.recordedDiagnostics(total)}</>}
    {limit > 0 && <small className="inspection-detail">{i.formatLimit(limit)}</small>}
    {!!tool.paths?.length && <details className="inspection-paths">
      <summary>{i.processedPaths(tool.paths.length)}</summary>
      <ul className="plain">{tool.paths.map(path => <li key={path}><code>{path}</code></li>)}</ul>
    </details>}
  </>;
}

export function InspectionReading({ language, byID }: { byID: Map<string, Observation>; language: Sections['staticAnalysis']['languages'][number] }) {
  const tools = language.tools.filter(t => t.purpose !== 'metrics' && t.purpose !== 'secrets');
  if (!tools.length) return null;
  const groups = ['lint', 'type', 'format', 'other'];
  const details = new Set<string>();
  return <section data-language={language.ecosystem ?? 'unknown'} className="report-inspections">
    <h6>{language.ecosystem === null ? i.earlier : i.languages[language.ecosystem] ?? s.language(language.ecosystem)}</h6>
    <dl className="inspection-groups">
      {groups.map(kind => {
        const rows = tools.filter(t => category(t) === kind);
        if (!rows.length && (kind === 'other' || language.ecosystem === null)) return null;
        return <div key={kind}>
          <dt>{i.kind[kind]}</dt>
          <dd>{rows.length === 0 ? i.checkMissing : <ul className="plain">
            {inspectionResults(rows).map(group => {
              const tool = group[0];
              const id = identity(tool);
              const [name, basis] = i.tool[id] ?? [tool.tool ?? id];
              const detail = tool.rulePack?.startsWith('codeye-js-eslint-rejection-types@') ? i.scopedJavaScript : basis;
              const showDetail = detail && !details.has(detail);
              if (detail) details.add(detail);
              const completed = shown(tool);
              return <li key={tool.task}>
                <strong>{name}</strong> {tool.version && <code>{tool.version}</code>}
                {(!completed || group.length > 1) && <> · {status(tool)}</>}
                {group.length === 1 && tool.task.includes('.') && <small> <code>{tool.task}</code></small>}
                {group.length === 1 && <TaskScope tool={tool} kind={kind} byID={byID} grouped={false} />}
                {showDetail && <small className="inspection-detail">{detail}</small>}
                {group.length > 1 && <details className="inspection-paths">
                  <summary>{i.moduleChecks(group.length)}</summary>
                  <ul className="plain">{group.map(row => <li key={row.task}><TaskScope tool={row} kind={kind} byID={byID} grouped /></li>)}</ul>
                </details>}
              </li>;
            })}
          </ul>}</dd>
        </div>;
      })}
    </dl>
  </section>;
}

function Place({ value }: { value?: ObservationPlace | null }) {
  if (!value) return <span>{i.noLocation}</span>;
  return <PinnedSourceLink path={value.path} start={value.start} end={value.end} fallback={sourceLocation(value.path, value.start, value.end)} />;
}

type InspectionRow = { id: string; cells: ReactNode[]; detail: () => ReactNode };
type RowSelection = { value: string | null; set: (value: string | null) => void };

function InspectionDetailTable({ head, rows, selection, diagnostics = false }: {
  head: string[]; rows: InspectionRow[]; selection: RowSelection; diagnostics?: boolean;
}) {
  if (rows.length === 0) return null;
  return <div className={'table report-data-table inspection-results' + (diagnostics ? ' inspection-diagnostics' : '')}>
    <table role="table">
      <thead><tr role="row">{[...head, i.details].map(key => <th key={key} scope="col" role="columnheader">{key}</th>)}</tr></thead>
      <tbody>{rows.map(row => {
        const opened = selection.value === row.id;
        const detailID = 'inspection-' + row.id;
        return <Fragment key={row.id}>
          <tr role="row">{row.cells.map((cell, n) => <td key={n} role="cell" data-label={head[n]}><div className="report-cell-value">{cell}</div></td>)}
            <td role="cell" data-label={i.details}><button type="button" className="btn btn-sm btn-ghost-primary" aria-expanded={opened}
              aria-controls={opened ? detailID : undefined} onClick={() => selection.set(opened ? null : row.id)}>
              {opened ? i.hideDetail : i.showDetail}
            </button></td>
          </tr>
          {opened && <tr id={detailID} className="inspection-selected" role="row"><td colSpan={head.length + 1} role="cell">{row.detail()}</td></tr>}
        </Fragment>;
      })}</tbody>
    </table>
  </div>;
}

function InspectionExcerpt({ place, excerpt }: { place?: ObservationPlace | null; excerpt?: Excerpt | null }) {
  if (!place) return <p>{i.noLocation}</p>;
  return <div className="inspection-source">
    {excerpt ? <>
      <ReportPreview path={place.path} start={place.start} end={place.start} excerpt={excerpt} />
      <PinnedSourceLink {...place} />
    </> : <><Place value={place} /><small>{i.noExcerpt}</small></>}
  </div>;
}

export function StaticDiagnosticReading({ languages, findings, byID, severityOf }: {
  languages: Sections['staticAnalysis']['languages']; findings: Pick<Finding, 'evidence' | 'observations'>[]; byID: Map<string, Observation>; severityOf: Map<string, string>;
}) {
  const [value, set] = useState<string | null>(null);
  const evidenceByObservation = new Map<string, Excerpt>();
  for (const finding of findings) for (const id of finding.observations) {
    const o = byID.get(id);
    if (o?.evidenceState !== 'verified_against_source_manifest' || !o.location) continue;
    const place = o.location;
    const evidence = finding.evidence.find(e => e.excerpt && e.path === place.path && e.lines?.start === place.start);
    if (evidence?.excerpt) evidenceByObservation.set(id, evidence.excerpt);
  }
  return <>{languages.map(lang => {
    const groups = ruleGroups(lang.tools, byID, severityOf);
    const rows = groups.map(g => {
      const rank = (o: Observation) => !o.location ? 3 : secondaryPath(o.location.path) ? 2 : evidenceByObservation.has(o.id) ? 0 : 1;
      const candidates = g.observations.map(id => byID.get(id)!).sort((a, b) => rank(a) - rank(b));
      const o = candidates[0];
      const message = reportKo.ruleDescriptions[g.analyzer + ':' + g.rule] ?? cutLine(o.message);
      return {
        id: 'static-' + o.id,
        cells: [<><code>{g.rule}</code><br /><small>{g.analyzer}</small></>,
          message, g.count,
          g.severity ? <span className={'pill sev sev-' + (Object.hasOwn(reportKo.severity, g.severity) ? g.severity : 'unknown')}>{label(reportKo.severity, g.severity)}</span> : '—',
          <Place value={o.location} />],
        detail: () => <>
          <InspectionExcerpt place={o.location} excerpt={evidenceByObservation.get(o.id)} />
          {o.message && o.message.trim() !== message && <p>{o.message}</p>}
          {candidates.length > 1 && <details className="inspection-paths"><summary>{i.otherLocations(candidates.length - 1)}</summary>
            <ul className="plain">{candidates.slice(1).map(other => <li key={other.id}><Place value={other.location} /></li>)}</ul>
          </details>}
        </>,
      };
    });
    return <InspectionDetailTable key={lang.ecosystem ?? 'unknown'} head={[s.col.rule, s.col.message, s.col.count, s.col.severity, s.col.location]} rows={rows} selection={{ value, set }} diagnostics />;
  })}</>;
}

function thirdParty(value?: ObservationPlace | null) {
  return !!value && value.path.split('/').slice(0, -1).some(segment => ['third-party', 'third_party', 'thirdparty'].includes(segment.toLowerCase()));
}

function placeKey(place: ObservationPlace) {
  return JSON.stringify([place.path, place.start, place.end]);
}

function clonePlaces(o: Observation) {
  return [o.location, o.metric?.counterpart].filter((p): p is ObservationPlace => !!p);
}

function cloneGroups(rows: Observation[]) {
  const parents = rows.map((_, n) => n);
  const root = (n: number): number => {
    while (parents[n] !== n) {
      parents[n] = parents[parents[n]];
      n = parents[n];
    }
    return n;
  };
  const firstAt = new Map<string, number>();
  rows.forEach((o, n) => clonePlaces(o).forEach(place => {
    const key = placeKey(place);
    const first = firstAt.get(key);
    if (first === undefined) firstAt.set(key, n);
    else parents[root(n)] = root(first);
  }));
  const groups = new Map<number, Observation[]>();
  rows.forEach((row, n) => {
    const key = root(n);
    const group = groups.get(key);
    if (group) group.push(row);
    else groups.set(key, [row]);
  });
  return [...groups.values()];
}

function MetricRows({ rows, name, selection }: { rows: Observation[]; name: keyof typeof i.metric; selection: RowSelection }) {
  const m = i.metric[name];
  const duplicate = name === 'duplicated_lines';
  return <InspectionDetailTable head={[i.measure, s.col.location, ...(duplicate ? [i.counterpart] : [])]} selection={selection} rows={rows.map(o => {
    const metric = o.metric!;
    const symbolMarker = o.location ? ` at ${o.location.path}:${o.location.start}: ` : undefined;
    const fn = !duplicate && symbolMarker && o.message?.includes(symbolMarker) && o.message.split(symbolMarker)[0];
    const counterpartStatus = o.evidenceState === 'verified_against_source_manifest' ? i.legacyCounterpart : i.noCounterpart;
    return {
      id: 'metric-' + o.id,
      cells: [<><strong>{m.item(metric.value)}</strong>{fn && <><br /><code>{fn}</code></>}</>,
        <Place value={o.location} />, ...(duplicate ? [metric.counterpart ? <Place value={metric.counterpart} /> : '—'] : [])],
      detail: () => <>
        <InspectionExcerpt place={o.location} excerpt={metric.excerpt} />
        {duplicate && (metric.counterpart
          ? <InspectionExcerpt place={metric.counterpart} excerpt={metric.counterpartExcerpt} />
          : <><p>{counterpartStatus}</p>{o.message && <details><summary>{i.rawToolOutput}</summary><code>{o.message}</code></details>}</>)}
      </>,
    };
  })} />;
}

function CloneRows({ groups, selection }: { groups: Observation[][]; selection: RowSelection }) {
  if (groups.every(rows => rows.length === 1)) {
    return <MetricRows rows={groups.flat()} name="duplicated_lines" selection={selection} />;
  }
  return <>{groups.map(rows => {
    if (rows.length === 1) return <MetricRows key={rows[0].id} rows={rows} name="duplicated_lines" selection={selection} />;
    const places = new Map(rows.flatMap(clonePlaces).map(place => [placeKey(place), place] as const));
    return <div key={rows[0].id} className="inspection-clone-group">
      <strong>{i.cloneGroup(rows.length, places.size)}</strong>
      <MetricRows rows={rows.slice(0, 1)} name="duplicated_lines" selection={selection} />
      <details className="inspection-paths">
        <summary>{i.cloneDetails(rows.length - 1)}</summary>
        <MetricRows rows={rows.slice(1)} name="duplicated_lines" selection={selection} />
      </details>
    </div>;
  })}</>;
}

export function MeasurementReading({ sections, byID }: { sections: Pick<Sections, 'staticAnalysis' | 'metrics'>; byID: Map<string, Observation> }) {
  const [value, set] = useState<string | null>(null);
  const selection = { value, set };
  const tools = sections.staticAnalysis.languages.flatMap(lang => lang.tools).filter(tool => sections.metrics.tasks.includes(tool.task));
  if (!tools.length) return <p>{i.noMeasuredTasks}</p>;
  return <>{tools.map(tool => {
    const id = identity(tool);
    const name = id === 'any.lizard' ? 'cyclomatic_complexity' : id === 'any.jscpd' ? 'duplicated_lines' : undefined;
    const m = name ? i.metric[name] : undefined;
    const group = name ? sections.metrics.groups.find(g => g.name === name) : undefined;
    const rows = (group?.observations ?? []).map(id => byID.get(id)).filter((o): o is Observation => !!o)
      .sort((a, b) => Number(thirdParty(a.location) || thirdParty(a.metric?.counterpart)) - Number(thirdParty(b.location) || thirdParty(b.metric?.counterpart)) ||
        (b.metric?.value ?? 0) - (a.metric?.value ?? 0));
    const legacy = rows.filter(o => thirdParty(o.location) || thirdParty(o.metric?.counterpart)).length;
    const clones = name === 'duplicated_lines' ? cloneGroups(rows) : null;
    const first = rows.slice(0, 5);
    return <section key={tool.task} className="report-inspections">
      <h6>{m?.label ?? tool.task}</h6>
      <p><strong>{m?.engine ?? id}</strong> {tool.version && <code>{tool.version}</code>}
        {' · '}{shown(tool) ? scope(tool) : status(tool)}
        {shown(tool) && tool.units.functions_measured !== undefined && <> · {i.totalFunctions(tool.units.functions_measured)}</>}
        {m && (shown(tool) || rows.length > 0) && <> · {i.threshold(m.floor)}</>}</p>
      {m && <p className="small">{m.meaning}</p>}
      {!!tool.paths?.length && <details className="inspection-paths">
        <summary>{id === 'any.jscpd' ? i.submittedPaths(tool.paths.length) : i.processedPaths(tool.paths.length)}</summary>
        <ul className="plain">{tool.paths.map(path => <li key={path}><code>{path}</code></li>)}</ul>
      </details>}
      {(tool.units.excluded_third_party_directory > 0 || tool.units.excluded_data_directory > 0 || legacy > 0) &&
        <p className="small">{[
          tool.units.excluded_third_party_directory > 0 && i.excluded(tool.units.excluded_third_party_directory),
          tool.units.excluded_data_directory > 0 && i.excludedData(tool.units.excluded_data_directory),
          legacy > 0 && i.legacyThirdParty(legacy),
        ].filter(Boolean).join(' · ')}</p>}
      {shown(tool) && <p>{rows.length === 0 ? tool.limitations.includes('no_file_reached_clone_thresholds') ? i.noEligibleCloneFiles : i.noMetricHit : m && <><strong>{m.value(rows.length)}</strong>{' · '}{m.next}</>}</p>}
      {clones && clones.length > 0 && <>
        <CloneRows groups={clones.slice(0, 5)} selection={selection} />
        {clones.length > 5 && <details className="report-more">
          <summary>{i.otherCloneGroups(clones.length - 5)}</summary>
          <CloneRows groups={clones.slice(5)} selection={selection} />
        </details>}
      </>}
      {name === 'cyclomatic_complexity' && rows.length > 0 && <>
        <MetricRows rows={first} name={name} selection={selection} />
        {rows.length > first.length && <details className="report-more">
          <summary>{i.otherLocations(rows.length - first.length)}</summary>
          <MetricRows rows={rows.slice(first.length)} name={name} selection={selection} />
        </details>}
      </>}
    </section>;
  })}</>;
}
