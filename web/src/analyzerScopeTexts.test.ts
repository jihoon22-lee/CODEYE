import { describe, expect, it } from 'vitest';
import { explainReason } from './ReportParts';
import { reasonText } from './StageView';

// The reasons, limitations and coverage units an analyzer records when it
// narrows what it analyzed (GCC complexity limit, Rust targets and colliding
// documentation, Java module descriptors and out-of-scope reports, Go vendor
// directories; docs/ANALYZERS.md) read as sentences, never as raw codes.
const codes = [
  'gcc_analyzer_too_complex', 'partial_too_complex',
  'failing_target_kinds_excluded', 'rust_files_not_compiled', 'doc_output_collision', 'doc_colliding_packages_excluded',
  'java_production_sources_unselected', 'java_module_descriptor_not_compiled', 'java_module_descriptors_not_compiled',
  'java_findings_outside_scope', 'java_findings_location_unmapped', 'observations_outside_scope_omitted',
  'vendor_directory_used',
];

describe('analyzer scope texts', () => {
  it.each(codes)('%s has a sentence in the report and on the run page', (code) => {
    expect(explainReason(code)).not.toBe(code);
    expect(reasonText(code)).not.toContain(code);
  });
});
