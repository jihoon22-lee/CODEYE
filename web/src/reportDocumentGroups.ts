import type { Citation, Sections } from './parseReportSections';
import { reportKo as t } from './locales/report-ko';

type Documents = Sections['documents'];
type DocumentReadingRow =
  | { kind: 'comparison'; index: number; value: Documents['comparisons'][number] }
  | { kind: 'quality'; index: number; value: Documents['quality'][number] }
  | { kind: 'contract'; index: number; value: Documents['contracts'][number] }
  | { kind: 'conflict'; index: number; value: Documents['conflicts'][number] }
  | { kind: 'question'; index: number; value: Documents['questions'][number] }
  | { kind: 'note'; index: number; value: Documents['notes'][number] };

export type DocumentReading = DocumentReadingRow & { refs: string[]; paths: string[] };
export type DocumentSource = { citation: Citation; index: number };
export type DocumentReadingGroup = {
  path: string;
  rows: { reading: DocumentReading; primary: boolean }[];
  sources: DocumentSource[];
};

export function documentComparisonHeading(row: Documents['comparisons'][number], index: number, includesCode: boolean) {
  return includesCode && row.statement.trim() ? row.statement : t.documentReading.withheldBehavior(index + 1);
}

function excerptContains(container: Citation, inside: Citation) {
  const outer = container.excerpt;
  const inner = inside.excerpt;
  return !!outer && !!inner && outer.start <= inner.start && outer.end >= inner.end &&
    outer.text.split('\n').slice(inner.start - outer.start, inner.end - outer.start + 1).join('\n') === inner.text;
}

export function documentReadingGroups(documents: Documents, includesCode: boolean) {
  const groups = new Map<string, DocumentReadingGroup>();
  const citations = new Map(documents.citations.map(c => [c.id, c]));
  const usedDocuments = new Set<string>();
  const group = (path: string) => {
    let current = groups.get(path);
    if (!current) {
      current = { path, rows: [], sources: [] };
      groups.set(path, current);
    }
    return current;
  };
  const add = (row: DocumentReadingRow, refs: string[]) => {
    const paths = new Set<string>();
    for (const id of refs) {
      const citation = citations.get(id);
      if (citation?.kind !== 'document') continue;
      paths.add(citation.path);
      usedDocuments.add(id);
    }
    const reading = { ...row, refs, paths: Array.from(paths) };
    (reading.paths.length > 0 ? reading.paths : ['']).forEach((path, i) => {
      group(path).rows.push({ reading, primary: i === 0 });
    });
  };
  const docRefs = documents.comparisons;
  const statusOrder = ['mismatch', 'partial', 'match', 'unknown'];
  docRefs.map((value, index) => ({ value, index })).sort((a, b) =>
    statusOrder.indexOf(a.value.status) - statusOrder.indexOf(b.value.status)
  ).forEach(({ value, index }) => {
    add({ kind: 'comparison', index, value }, value.documentRefs);
  });
  documents.quality.forEach((value, index) => add({ kind: 'quality', index, value }, value.refs));
  if (documents.model.performed) {
    documents.contracts.forEach((value, index) => {
      const paired = docRefs.some(c => c.scope === value.appliesTo && c.statement.trim() === value.statement.trim() &&
        value.evidence.every(id => c.documentRefs.includes(id)));
      if (!paired) add({ kind: 'contract', index, value }, value.evidence);
    });
    documents.conflicts.forEach((value, index) => add({ kind: 'conflict', index, value }, value.evidence));
    documents.questions.forEach((value, index) => add({ kind: 'question', index, value }, value.evidence));
    documents.notes.forEach((value, index) => add({ kind: 'note', index, value }, value.evidence));
  }

  const sourceByRef = new Map<string, DocumentSource>();
  if (includesCode) {
    // Build the catalog from displayed references only. A longer line-identical
    // window can serve all citations it contains while preserving exact ranges.
    const windows = documents.citations.filter(c => usedDocuments.has(c.id) && c.excerpt).sort((a, b) =>
      (b.excerpt!.end - b.excerpt!.start) - (a.excerpt!.end - a.excerpt!.start));
    for (const citation of windows) {
      const document = group(citation.path);
      let source = document.sources.find(s => excerptContains(s.citation, citation));
      if (!source) {
        source = { citation, index: sourceByRef.size + 1 };
        document.sources.push(source);
      }
      sourceByRef.set(citation.id, source);
    }
    for (const document of groups.values()) {
      document.sources.sort((a, b) => a.citation.excerpt!.start - b.citation.excerpt!.start);
    }
  }
  return { groups: Array.from(groups.values()), citations, sourceByRef };
}
