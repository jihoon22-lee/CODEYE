import glossary from '../../internal/report/reportui/overview_glossary.json';
import { reportKo as t } from './locales/report-ko';
import { ReportIcon } from './ReportLayout';

const entries = glossary.map(({ name, definition, pattern, source }) => ({
  name, definition, source, match: pattern ? new RegExp(pattern, 'i') : undefined,
}));

export function ReportGlossary({ text, hasSources }: { text: string[]; hasSources: boolean }) {
  const terms = entries.filter(entry => entry.source && hasSources || text.some(part => entry.match?.test(part)));
  if (!terms.length) return null;
  return <details className="report-glossary">
    <summary><ReportIcon name="chevron" />{t.reading.glossary}</summary>
    <dl>{terms.map(term => <div key={term.name}><dt>{term.name}</dt><dd>{term.definition}</dd></div>)}</dl>
  </details>;
}
