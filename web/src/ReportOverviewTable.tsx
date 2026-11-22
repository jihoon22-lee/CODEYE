import { Fragment } from 'react';
import type { ReadingSummaryRow } from './reportGroups';
import { reportKo as t } from './locales/report-ko';
import { ReportIcon } from './ReportLayout';

// Keep fractions and grouped numbers intact even when the result sentence wraps.
function ResultText({ value }: { value: string }) {
  return <>{value.split(/([0-9]+(?:[.,/][0-9]+)*)/).map((part, index) =>
    index % 2 === 1 ? <span className="report-result-number" key={index}>{part}</span> : <Fragment key={index}>{part}</Fragment>)}</>;
}

export function ReportOverviewTable({ rows, open }: { rows: ReadingSummaryRow[]; open: (section: string) => void }) {
  if (!rows.length) return null;
  return <section className="report-overview-results" id="report-coverage" tabIndex={-1} aria-label={t.reading.outcome}>
    <h6>{t.reading.outcome}</h6>
    <table>
      <thead><tr><th scope="col">{t.reading.area}</th><th scope="col">{t.sections.col.status}</th><th scope="col">{t.reading.result}</th></tr></thead>
      <tbody>{rows.map(({ section, label, value, status, tone }) => <tr key={label}>
        <th scope="row"><button type="button" className="report-overview-link" aria-label={t.reading.viewArea(label)} onClick={() => open(section)}>
          {label}<ReportIcon name="jump" />
        </button></th>
        <td className="report-result-status">{status && <span className="report-status-badge" data-tone={tone ?? 'neutral'}>{status}</span>}</td>
        <td className="report-result-value"><ResultText value={value} /></td>
      </tr>)}</tbody>
    </table>
  </section>;
}
