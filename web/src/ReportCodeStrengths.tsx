import type { Sections } from './parseReportSections';
import { reportKo as t } from './locales/report-ko';
import { Citations } from './ReportParts';
import { primaryCitation } from './reportGroups';

export function ReportCodeStrengths({ review, includesCode, id }: {
  review: Sections['codeReview']; includesCode: boolean; id: string;
}) {
  if (!review.strengths.length) return null;
  type Strength = Sections['codeReview']['strengths'][number];
  type GroupRow = { strength: Strength; indices: number[] };
  const byScope = new Map<string, Map<string, GroupRow>>();
  const normalize = (value: string) => value.trim().replace(/\s+/g, ' ');
  review.strengths.forEach((strength, index) => {
    const scope = strength.scope.trim() || '.';
    const strengths = byScope.get(scope) ?? new Map<string, GroupRow>();
    const key = includesCode ? JSON.stringify([normalize(strength.title), normalize(strength.benefit)]) : String(index);
    const row = strengths.get(key);
    if (row) {
      row.strength.refs = [...new Set([...row.strength.refs, ...strength.refs])];
      row.indices.push(index);
    } else {
      strengths.set(key, { strength: { ...strength }, indices: [index] });
    }
    byScope.set(scope, strengths);
  });
  const rowId = (index: number) => `${id}-code-strength-${index + 1}`;
  return <details className="report-reading-records">
    <summary>{t.strengthsTitle}</summary>
    <div className="report-reading-groups">
    <p className="model-note" role="note">{t.modelReading}</p>
    {Array.from(byScope, ([scope, rows]) => <div className="report-reading-group report-strength-group" key={scope}>
      <h6>{scope === '.' ? t.codeReading.repository : <code>{scope}</code>}</h6>
      <ul className="report-reading-list">
        {Array.from(rows.values(), ({ strength, indices: [index, ...aliases] }) => <li className="report-reading-row" key={index}>
          {aliases.map(alias => <span id={rowId(alias)} key={alias} tabIndex={-1} />)}
          <h6 id={rowId(index)} tabIndex={-1}>{includesCode ? strength.title : t.codeReading.withheldStrength(index + 1)}</h6>
          {!review.citations.some(c => strength.refs.includes(c.id) && primaryCitation(c, 'code')) &&
            <span className="badge report-reading-status">{t.reading.secondary}</span>}
          {includesCode && <p>{strength.benefit}</p>}
          <div className="reading-refs"><Citations refs={strength.refs} catalog={review.citations} showLocation /></div>
        </li>)}
      </ul>
    </div>)}
    </div>
  </details>;
}
