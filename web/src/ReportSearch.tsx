import { useId, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { AnalysisReport } from './analysisReport';
import { reportKo as t } from './locales/report-ko';
import { reportSearchKo as s } from './locales/report-search-ko';
import type { Citation } from './parseReportSections';
import { documentComparisonHeading } from './reportDocumentGroups';
import './ReportSearch.css';

export type ReportSearchTarget =
  | { kind: 'comparison'; index: number }
  | { kind: 'finding'; id: string; section: 'static_analysis' | 'code_review' | 'findings' };

type SearchEntry = {
  target: ReportSearchTarget;
  title: string;
  section: string;
  places: string[];
  heading: string;
  locations: string;
  text: string;
};

const pageSize = 8;

function normalize(value: string) {
  return value.normalize('NFKC').toLowerCase();
}

function entry(target: ReportSearchTarget, title: string, section: string, locations: string[], details: string[]): SearchEntry {
  return {
    target, title, section, places: locations,
    heading: normalize(title), locations: normalize(locations.join('\n')), text: normalize(details.join('\n')),
  };
}

function searchEntries(report: AnalysisReport): SearchEntry[] {
  const { sections, includesCode } = report;
  const citations = new Map(sections?.documents.citations.map(c => [c.id, c]));
  const codeReview = new Set(sections?.codeReview.findings);
  const rules = new Map(report.observations.map(o => [o.id, o.rule]));
  const entries = sections?.documents.comparisons.map((row, index) => {
    const workflowRefs = row.workflow ? [row.workflow.read, row.workflow.change, row.workflow.test] : [];
    const refs = [...new Set([...row.documentRefs, ...row.codeRefs, ...(row.additional?.codeRefs ?? []), ...workflowRefs])]
      .map(id => citations.get(id)).filter((c): c is Citation => !!c);
    return entry({kind: 'comparison', index}, documentComparisonHeading(row, index, includesCode !== false),
      t.sections.titles.documents, refs.flatMap(c => [c.path, ...(c.directivePath ? [c.directivePath] : [])]),
      [t.reading.comparisonStatus[row.status] ?? s.comparison, ...(includesCode ? [row.reason, row.additional?.reason ?? '', ...refs.map(c => c.excerpt?.text ?? '')] : [])]);
  }) ?? [];
  for (const f of report.findings) {
    const section = !sections ? 'findings' : codeReview.has(f.id) ? 'code_review' : 'static_analysis';
    const ruleNames = f.observations.flatMap(id => rules.get(id) ?? []);
    const locations = [...new Set([...f.locations, ...f.evidence.flatMap(e => e.path ?? [])])];
    const placeAndRules = [...locations, ...ruleNames];
    entries.push(entry({ kind: 'finding', id: f.id, section }, f.title, section === 'findings' ? t.findings : t.sections.titles[section],
      placeAndRules, includesCode ? [f.impact, f.recommendation, ...f.conditions.map(c => c.statement),
        ...f.claims.map(c => c.assertion), ...f.evidence.map(e => e.excerpt?.text ?? '')] : []));
  }
  return entries;
}

function matchingEntries(entries: SearchEntry[], terms: string[]) {
  return entries.flatMap(e => {
    const ranks = terms.map(word => e.heading.includes(word) ? 3 : e.locations.includes(word) ? 2 : e.text.includes(word) ? 1 : 0);
    return ranks.includes(0) ? [] : [{ entry: e, rank: ranks.reduce<number>((a, b) => a + b, 0) }];
  }).sort((a, b) => b.rank - a.rank).map(r => r.entry);
}

export function focusReportFinding(container: HTMLElement | null | undefined, id: string) {
  const finding = [...container?.querySelectorAll<HTMLDetailsElement>('details[data-finding-id]') ?? []]
    .find(node => node.dataset.findingId === id);
  if (!finding) return;
  finding.open = true;
  focusReportSearchLocation(finding.querySelector('summary'));
}

export function focusReportSearchLocation(target: HTMLElement | null | undefined) {
  for (let parent = target?.parentElement; parent; parent = parent.parentElement) {
    if (parent instanceof HTMLDetailsElement) parent.open = true;
  }
  target?.focus({ preventScroll: true });
  target?.scrollIntoView?.({ block: 'start' });
}

export function ReportSearch({ report, navigate }: { report: AnalysisReport; navigate: (target: ReportSearchTarget) => void }) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [visible, setVisible] = useState(pageSize);
  const trigger = useRef<HTMLButtonElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const id = useId();
  const entries = useMemo(() => open ? searchEntries(report) : [], [open, report]);
  const terms = normalize(query).trim().split(/\s+/).filter(Boolean);
  const hits = open && terms.length > 0 ? matchingEntries(entries, terms) : [];

  useLayoutEffect(() => {
    if (open) input.current?.focus();
  }, [open]);

  const jump = (target: ReportSearchTarget) => {
    setOpen(false);
    navigate(target);
  };
  return (
    <div className="report-search" onKeyDown={e => {
      if (open && e.key === 'Escape' && !e.nativeEvent.isComposing) {
        e.preventDefault();
        setOpen(false);
        trigger.current?.focus();
      }
    }}>
      <button ref={trigger} type="button" className="btn btn-sm btn-ghost-primary report-search-toggle"
        aria-expanded={open} aria-controls={id} aria-label={s.title} onClick={() => setOpen(!open)}>
        <span className="report-search-label">{s.title}</span><span className="report-search-icon" aria-hidden="true" />
      </button>
      <div id={id} hidden={!open}>
        {open && <>
          <form role="search" aria-label={s.title} onSubmit={e => {
            e.preventDefault();
            if (hits.length > 0) jump(hits[0].target);
          }}>
            <label htmlFor={id + '-input'} className="visually-hidden">{s.query}</label>
            <input ref={input} id={id + '-input'} type="search" autoComplete="off" maxLength={256} value={query}
              placeholder={s.query} onChange={e => { setQuery(e.target.value); setVisible(pageSize); }} />
          </form>
          <p className="report-search-count" role="status">{terms.length > 0 ? hits.length ? s.count(hits.length) : s.empty : ''}</p>
          {hits.length > 0 && <ul className="report-search-results">
            {hits.slice(0, visible).map(({target, title, section, places}) => {
              const place = places.find(name => terms.some(word => normalize(name).includes(word))) ?? places[0];
              return <li key={target.kind === 'finding' ? 'finding-' + target.id : 'comparison-' + target.index}>
                <button type="button" className="btn btn-ghost-primary report-search-result" onClick={() => jump(target)}>
                  <strong>{title}</strong>
                  <small>{section}{place && <> · <code>{place}</code></>}</small>
                </button>
              </li>;
            })}
          </ul>}
          {visible < hits.length && <button type="button" className="btn btn-sm btn-ghost-primary" onClick={() => setVisible(visible + pageSize)}>{s.more}</button>}
        </>}
      </div>
    </div>
  );
}
