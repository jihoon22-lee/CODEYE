import { describe, expect, it } from 'vitest';
import goSource from '../../internal/report/code_labels.go?raw';
import { codeLabels, codeText, unitText } from './locales/codeLabels';
import { ko } from './locales/ko';
import { reportKo } from './locales/report-ko';
import { explainReason } from './ReportParts';
import { reasonText } from './StageView';

// The stored example reports and observation documents, written by real worker runs.
const examples = import.meta.glob('../../examples/{report*,observations-*}.json', { eager: true, import: 'default' }) as Record<string, unknown>;

// One catalog serves the Go renderers and this screen: the web maps are that
// catalog and not copies, and the Go side embeds the same file.
describe('shared code catalog', () => {
  it('is the table every web surface reads', () => {
    expect(reportKo.reasons).toBe(codeLabels.reasons);
    expect(ko.stages.reasonLabels).toBe(codeLabels.reasons);
    expect(reportKo.sections.modelReasons).toBe(codeLabels.model_reasons);
  });

  it('is the file the Go renderers embed', () => {
    expect(goSource).toContain('//go:embed reportui/code_labels_ko.json');
  });

  it('words a code on the report and on the run page', () => {
    for (const code of ['mypy_failed', 'cppcheck_failed', 'tsconfig_invalid', 'node_modules_outside_snapshot', 'java_compilation_failed', 'spotbugs_failed']) {
      expect(explainReason(code)).not.toBe(code);
      expect(reasonText(code)).not.toContain(code);
    }
  });

  it('words a code with a detail and a composed upstream code, and leaves an unknown code as it is', () => {
    expect(codeText('static_analysis_gap:go')).toMatch(/ · go$/);
    expect(codeText('upstream_interpret_skipped')).toContain('interpret');
    expect(codeText('upstream_interpret_exploded')).toBeNull();
    expect(explainReason('code_nobody_defined')).toBe('code_nobody_defined');
    expect(unitText('no_such_counter')).toBe('no_such_counter');
    expect(unitText('analyzed_clean')).not.toBe('analyzed_clean');
    // A counter with only a reason text reads as that text.
    expect(unitText('imports_unresolved')).toBe(codeLabels.reasons.imports_unresolved);
  });

  it('keeps unit and reason spaces apart where one word means two things', () => {
    expect(unitText('dependency_unavailable')).not.toBe(codeText('dependency_unavailable'));
    for (const code of Object.keys(codeLabels.model_reasons)) expect(codeLabels.reasons).not.toHaveProperty(code);
  });
});

// Every reason, limitation and counter in the stored example reports and
// observation documents (written by real worker runs) reads as words.
describe('stored example codes', () => {
  const seen: { code: string; unit: boolean; file: string }[] = [];
  const walk = (value: unknown, file: string) => {
    if (Array.isArray(value)) return value.forEach(v => walk(v, file));
    if (value === null || typeof value !== 'object') return;
    for (const [key, v] of Object.entries(value)) {
      if ((key === 'packages' || key === 'units') && v && typeof v === 'object' && !Array.isArray(v)) {
        for (const unit of Object.keys(v)) seen.push({ code: unit, unit: true, file });
      } else if (key === 'limitations' && Array.isArray(v)) {
        for (const c of v) if (typeof c === 'string' && /^[a-z][a-z0-9]*(_[a-z0-9]+)+(:|$)/.test(c)) seen.push({ code: c.split(':')[0], unit: false, file });
      } else if ((key === 'reason' || key === 'task_reason') && typeof v === 'string' && /^[a-z][a-z0-9]*(_[a-z0-9]+)+(:|$)/.test(v)) {
        seen.push({ code: v.split(':')[0], unit: false, file });
      } else walk(v, file);
    }
  };
  for (const [file, document] of Object.entries(examples)) walk(document, file);
  it('has words for every code', () => {
    expect(seen.length).toBeGreaterThan(100);
    const missing = seen.filter(c => c.unit ? unitText(c.code) === c.code : codeText(c.code) === null);
    expect(missing.map(c => `${c.code} in ${c.file}`)).toEqual([]);
  });
});
