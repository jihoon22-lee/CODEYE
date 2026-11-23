import { describe, expect, it } from 'vitest';
import { explainReason } from './ReportParts';
import { reportKo } from './locales/report-ko';

// The reasons and limitations of the C/C++ compile DB generation, the Rust
// build scripts and dependency-cruiser read as sentences like their
// neighbours (docs/ANALYZERS.md#compile-db-생성), never as raw codes.
const codes = [
  'compile_db_missing', 'build_tool_not_configured', 'compile_db_generation_failed', 'compile_db_not_written',
  'compile_db_generated', 'compile_db_generation_partial', 'compile_db_generation_not_allowed', 'compile_db_generation_unavailable',
  'compile_db_build_description_unsupported', 'cmake_not_found', 'cmake_generator_not_found', 'cmake_inputs_not_enumerated',
  'generated_include_dirs_used', 'repository_code_executed', 'build_scripts_used_toolchain_path', 'doc_output_collision',
  'dependency_cruiser_no_parsed_sources', 'dependency_violations_not_represented',
];

describe('build context texts', () => {
  it.each(codes)('%s has a sentence', (code) => {
    expect(explainReason(code)).not.toBe(code);
  });
  it('labels the compile DB gap', () => {
    expect(reportKo.sections.gapLabels.compile_db_not_generated).toBeTruthy();
  });
  it('no longer says compile DB generation is not done', () => {
    expect(explainReason('compile_db_missing')).not.toContain('생성은 저장소 코드를 실행하므로');
  });
});
