import type { ReadingSummaryRow } from './reportGroups';
import { reportKo as t } from './locales/report-ko';
import { ReportIcon } from './ReportLayout';

export function ReportOverviewTable({ rows, open }: { rows: ReadingSummaryRow[]; open: (section: string) => void }) {
  if (!rows.length) return null;
  return <section className="report-overview-results" aria-label={t.reading.outcome}>
    <h6>{t.reading.outcome}</h6>
    <table>
      <thead><tr><th scope="col">{t.reading.area}</th><th scope="col">{t.reading.result}</th><th scope="col">{t.reading.details}</th></tr></thead>
      <tbody>{rows.map(({ section, label, value, status }) => <tr key={label}>
        <th scope="row">{label}{status && <small>{status}</small>}</th><td>{value}</td>
        <td><button className="btn btn-sm btn-ghost-primary overview-result-action" type="button" aria-label={t.reading.viewArea(label)} onClick={() => open(section)}><span>{t.reading.view}</span><ReportIcon name="jump" /></button></td>
      </tr>)}</tbody>
    </table>
  </section>;
}
