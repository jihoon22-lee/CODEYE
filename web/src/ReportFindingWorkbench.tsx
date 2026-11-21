import { useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import type { Finding } from './analysisReport';
import { label } from './format';
import { ReportPath } from './ReportParts';
import { reportKo as t } from './locales/report-ko';
import { secondaryReviewFinding } from './reportGroups';

function FindingLocation({ finding, full = false }: { finding: Finding; full?: boolean }) {
  const first = finding.locations[0];
  const match = finding.evidence.find(e => e.path === first && e.lines);
  if (!first) return <>—</>;
  const line = match?.lines && (match.lines.start === match.lines.end ? match.lines.start : `${match.lines.start}–${match.lines.end}`);
  const suffix = line == null ? '' : ':' + line;
  const complete = first + suffix;
  if (full) return <ReportPath value={complete} className="report-index-path" />;
  const shortPath = first.includes('/') ? '…/' + (first.split('/').at(-1) ?? first) : first;
  return <code className="report-index-path report-index-compact" title={complete} aria-label={complete}>
    <span>{shortPath}</span><span>{suffix}</span>
  </code>;
}

export function ReportFindingWorkbench({ findings, reveal, detail }: {
  findings: Finding[]; reveal?: { id: string }; detail: (finding: Finding) => ReactNode;
}) {
  const [chosen, setChosen] = useState<string>();
  const [dismissed, setDismissed] = useState<typeof reveal>();
  const root = useRef<HTMLDivElement>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const target = reveal && reveal !== dismissed ? reveal.id : chosen;
  const selected = findings.find(f => f.id === target);
  const selectedID = selected?.id;
  useLayoutEffect(() => {
    if (!selectedID || !heading.current) return;
    heading.current.focus({ preventScroll: true });
    const bounds = heading.current.getBoundingClientRect();
    if (bounds.top < 84 || bounds.bottom > window.innerHeight) heading.current.scrollIntoView?.({ block: 'start' });
  }, [selectedID, reveal]);

  return <div className="report-finding-workbench" ref={root} data-selected={Boolean(selected)}>
    <div className="report-workbench-index">
      <table className="report-workbench-table">
        <thead><tr><th scope="col">{t.reading.detailItem}</th><th scope="col">{t.sections.col.location}</th><th scope="col">{t.sections.col.validation}</th><th scope="col">{t.sections.col.severity}</th></tr></thead>
        <tbody>{findings.map(finding => {
          const active = selectedID === finding.id;
          return <tr key={finding.id} data-active={active} onClick={event => {
            if ((event.target as HTMLElement).closest('button')) return;
            event.currentTarget.querySelector('button')?.click();
          }}>
            <th scope="row"><button type="button" className="report-finding-select" data-finding-id={finding.id}
              aria-pressed={active}
              onClick={() => { setChosen(finding.id); setDismissed(reveal); }}
              onKeyDown={event => {
                if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
                const current = event.currentTarget.closest('tr');
                const next = event.key === 'ArrowDown' ? current?.nextElementSibling : current?.previousElementSibling;
                if (next) { event.preventDefault(); next.querySelector('button')?.focus(); }
              }}>{finding.title}{secondaryReviewFinding(finding) && <small>{t.reading.secondary}</small>}</button></th>
            <td className="report-workbench-location"><FindingLocation finding={finding} /></td>
            <td className="report-workbench-status"><span className="report-status-badge" data-tone={finding.validation === 'supported' || finding.validation === 'reproduced' ? 'ready' : 'attention'}>{label(t.validation, finding.validation)}</span></td>
            <td className="report-workbench-severity">{label(t.severity, finding.severity)}</td>
          </tr>;
        })}</tbody>
      </table>
    </div>
    <section className="report-workbench-detail" aria-label={t.evidence}>
      {selected ? <>
        <button type="button" className="report-plain-action" onClick={() => {
          const returnTo = selected.id;
          setChosen(undefined);
          setDismissed(reveal);
          requestAnimationFrame(() => [...root.current?.querySelectorAll<HTMLButtonElement>('button[data-finding-id]') ?? []]
            .find(button => button.dataset.findingId === returnTo)?.focus());
        }}>← {t.codeReading.resultList}</button>
        <h6 className="report-finding-heading" tabIndex={-1} ref={heading}>{selected.title}</h6>
        <div className="report-workbench-meta">
          <span className="report-status-badge" data-tone={selected.validation === 'supported' || selected.validation === 'reproduced' ? 'ready' : 'attention'}>{label(t.validation, selected.validation)}</span>
          <span className="report-detail-severity">{label(t.severity, selected.severity)}</span>
          {secondaryReviewFinding(selected) && <span>{t.reading.secondary}</span>}
          {!selected.evidence.some(e => e.kind === 'code' && e.excerpt && !selected.counter.includes(e.id)) && <FindingLocation finding={selected} full />}
        </div>
        {detail(selected)}
      </> : <p className="report-workbench-empty">{t.codeReading.pickResult}</p>}
    </section>
  </div>;
}
