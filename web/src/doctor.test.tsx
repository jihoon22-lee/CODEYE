import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { App } from './App';
import { parseReport } from './doctor';
import example from '../../examples/doctor.json';

describe('diagnostics import', () => {
  it('accepts the shared contract and preserves unverified capabilities', () => {
    const report = parseReport(JSON.stringify(example));
    expect(report.checks.find(c => c.id === 'model')?.status).toBe('unverified');
  });
  it.each([
    '{', JSON.stringify({ ...example, schema_version: '99' }),
    JSON.stringify({ ...example, checks: example.checks.map(c => ({ ...c, id: 'host' })) }),
    JSON.stringify({ ...example, checks: example.checks.map(c => ({ ...c, status: 'success' })) }),
    JSON.stringify({ ...example, secret: 'not-allowed' }), ' '.repeat(1048577),
  ])('rejects malformed, unknown, duplicate, or oversized input', text => {
    expect(() => parseReport(text)).toThrow();
  });
  it('shows the implementation boundary without fabricated results', () => {
    const html = renderToStaticMarkup(<App />);
    expect(html).toContain('아직 구현되지 않았습니다');
    expect(html).toContain('아직 불러온 진단 결과가 없습니다');
    expect(html).not.toContain('<table');
  });
});
