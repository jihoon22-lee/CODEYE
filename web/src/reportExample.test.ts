import { describe, expect, it } from 'vitest';
import cases from '../../internal/report/testdata/document_examples.json';
import { citedDocumentExample } from './reportExample';
import type { Citation } from './parseReportSections';

describe('saved document examples', () => {
  it.each(cases)('$name', ({ statement, text, example }) => {
    const end = text.split('\n').length;
    const citation: Citation = { id: 'doc', kind: 'document', path: 'README.md', line: 1, end, excerpt: { start: 1, end, text } };
    expect(citedDocumentExample(statement, citation) ?? null).toEqual(example);
    expect(citedDocumentExample(statement, { ...citation, end: end - 1 })).toBeUndefined();
    expect(citedDocumentExample(statement, { ...citation, excerpt: null })).toBeUndefined();
  });
});
