import { Fragment, useState } from 'react';
import type { GoImportEdge, GoPackageGraph } from './parseReportSections';
import { label, PinnedSourceLink, sourceLocation } from './ReportParts';

import { graphKo as t } from './locales/report-graph-ko';

function add(map: Map<string, GoImportEdge[]>, key: string, edge: GoImportEdge) {
  const list = map.get(key);
  if (list) list.push(edge);
  else map.set(key, [edge]);
}

function shortName(graph: GoPackageGraph, path: string) {
  return path === graph.module ? path : path.slice(graph.module.length + 1);
}

function edgeKey(edge: GoImportEdge) {
  return edge.from + ':' + edge.to + ':' + edge.kind;
}

function Witness({ graph, edge, focus }: { graph: GoPackageGraph; edge: GoImportEdge; focus?: string }) {
  const title = focus === edge.to ? shortName(graph, edge.from) : focus === edge.from ? shortName(graph, edge.to) :
    shortName(graph, edge.from) + ' → ' + shortName(graph, edge.to);
  return <li><code>{title}</code>
    {edge.kind !== 'production' && <small> {label(t.kind, edge.kind)}</small>}
    <PinnedSourceLink path={edge.path} start={edge.line} end={edge.line} fallback={sourceLocation(edge.path, edge.line, edge.line)} />
  </li>;
}

function cycleFor(graph: GoPackageGraph, outgoing: Map<string, GoImportEdge[]>) {
  const states = new Map<string, number>();
  const route: GoImportEdge[] = [];
  function follow(node: string): GoImportEdge[] | null {
    states.set(node, 1);
    for (const edge of outgoing.get(node) ?? []) {
      if (states.get(edge.to) === 1) {
        const start = route.findIndex(row => row.from === edge.to);
        return [...(start < 0 ? [] : route.slice(start)), edge];
      }
      if (!states.has(edge.to)) {
        route.push(edge);
        const cycle = follow(edge.to);
        if (cycle) return cycle;
        route.pop();
      }
    }
    states.set(node, 2);
    return null;
  }
  for (const name of graph.packages) if (!states.has(name)) {
    const cycle = follow(name);
    if (cycle) return cycle;
  }
  return null;
}

function Graph({ graph }: { graph: GoPackageGraph }) {
  const [query, setQuery] = useState('');
  const [open, setOpen] = useState<string | null>(null);
  const [limit, setLimit] = useState(12);
  const incoming = new Map<string, GoImportEdge[]>(), outgoing = new Map<string, GoImportEdge[]>(), tests = new Map<string, GoImportEdge[]>();
  for (const edge of graph.edges) {
    if (edge.kind === 'production') {
      add(incoming, edge.to, edge);
      add(outgoing, edge.from, edge);
    } else add(tests, edge.to, edge);
  }
  const cycle = cycleFor(graph, outgoing);
  const matches = graph.packages.filter(name => name.toLowerCase().includes(query.toLowerCase()));
  const witnessList = (title: string, edges: GoImportEdge[], focus?: string) => {
    if (edges.length === 0) return null;
    const limit = focus ? 4 : edges.length;
    const list = (items: GoImportEdge[]) => <ul>{items.map(edge => <Witness key={edgeKey(edge)} graph={graph} edge={edge} focus={focus} />)}</ul>;
    return <>
      <strong>{title}</strong>
      {list(edges.slice(0, limit))}
      {edges.length > limit && <details><summary>{t.moreImports(edges.length - limit)}</summary>{list(edges.slice(limit))}</details>}
    </>;
  };
  return <section className="report-inspections report-go-graph">
    <h6>{t.title}</h6>
    <p><code>{graph.module}</code> · {graph.directory && graph.directory !== '.' && <><code>{graph.directory}</code> · </>}<code>{graph.platform}</code> · {label(t.scope, graph.scope)}</p>
    <p>{label(t.status, graph.status)} {t.coverage(graph.packages.length, graph.parsed, graph.ignored)}
      {graph.unlisted !== undefined && <> · {t.unlistedFiles(graph.unlisted)}</>}</p>
    {(graph.incomplete > 0 || graph.unresolved > 0 || graph.unparsed > 0) && <p>{t.gaps(graph.incomplete, graph.unparsed, graph.unresolved)}</p>}
    {graph.limitations.length > 0 && <p>{graph.limitations.map(value => label(t.limit, value)).join(' · ')}</p>}
    {cycle ? witnessList(t.cycle, cycle) : graph.status === 'complete' && <p>{t.noCycle}</p>}
    {graph.packages.length > 0 && <>
      <label className="field">{t.selection}<input value={query} onChange={event => { setQuery(event.target.value); setOpen(null); setLimit(12); }} /></label>
      {matches.length > 0 ? <div className="table report-data-table inspection-results">
        <table role="table">
          <thead><tr role="row">{[t.package, t.incoming, t.outgoing, t.tests, t.results].map(title => <th key={title} scope="col" role="columnheader">{title}</th>)}</tr></thead>
          <tbody>{matches.slice(0, limit).map(name => {
            const source = incoming.get(name) ?? [], target = outgoing.get(name) ?? [], test = tests.get(name) ?? [];
            const direct = new Set([name, ...source.map(edge => edge.from)]);
            let pair: GoImportEdge[] = [];
            for (const last of source) {
              const first = incoming.get(last.from)?.find(edge => !direct.has(edge.from));
              if (first) { pair = [first, last]; break; }
            }
            return <Fragment key={name}>
              <tr role="row">
                <td role="cell" data-label={t.package}><code>{shortName(graph, name)}</code></td>
                <td role="cell" data-label={t.incoming}>{source.length}</td>
                <td role="cell" data-label={t.outgoing}>{target.length}</td>
                <td role="cell" data-label={t.tests}>{new Set(test.map(edge => edge.from)).size}</td>
                <td role="cell" data-label={t.results}><button type="button" className="btn btn-sm btn-ghost-primary" aria-expanded={open === name}
                  onClick={() => setOpen(open === name ? null : name)}>{open === name ? t.close : t.open}</button></td>
              </tr>
              {open === name && <tr className="inspection-selected" role="row"><td role="cell" colSpan={5}>
                <p>{t.candidateScope}</p>
                {witnessList(t.incoming, source, name)}
                {witnessList(t.outgoing, target, name)}
                {witnessList(t.tests, test, name)}
                {witnessList(t.indirect, pair)}
              </td></tr>}
            </Fragment>;
          })}</tbody>
        </table>
      </div> : <p>{t.noMatches}</p>}
      {matches.length > limit && <button type="button" className="btn btn-sm btn-ghost-primary" onClick={() => setLimit(limit + 12)}>{t.more(Math.min(matches.length - limit, 12))}</button>}
    </>}
  </section>;
}

export function ReportGoDependencies({ graphs }: { graphs: GoPackageGraph[] }) {
  return <>{graphs.map(graph => <Graph key={graph.task} graph={graph} />)}</>;
}
