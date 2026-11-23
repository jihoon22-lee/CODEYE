package report

import (
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/cpp"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// GapCompileDBNotGenerated: the C/C++ compile DB generation task
// (docs/ANALYZERS.md#compile-db-생성) did not succeed; the C/C++ analyzers
// then read no generated database. Detail is the task reason.
const GapCompileDBNotGenerated = "compile_db_not_generated"

// analyzerTask reports an analyzer task key: the compile DB generation task
// runs before the analyzers and writes no envelope, so it is no tool row.
func analyzerTask(key string) bool {
	return record.AnalyzerTaskKey(key) && key != cpp.GenerateTaskKey
}

// buildContextLimitations are the limitations of the compile DB generation
// task the report lifts from the result manifest. The task writes no
// envelope, so this is the only place its own limitations reach the report.
func buildContextLimitations(mt *analysis.ManifestTask) []string {
	if mt == nil || mt.Key != cpp.GenerateTaskKey {
		return nil
	}
	var out []string
	for _, l := range mt.Limitations {
		switch {
		case l == cpp.LimGeneratorCMake:
			// Implied by compile_db_generated and the task title.
		case strings.HasPrefix(l, "cmake_"), strings.HasPrefix(l, "compile_db_"), l == "repository_code_executed":
			out = append(out, l)
		}
	}
	return out
}

// buildContextGap adds the gap of a compile DB generation task that did not
// succeed.
func (c sectionContext) buildContextGap(add func(section, code string, subject, detail *string)) {
	st, ok := c.states[cpp.GenerateTaskKey]
	if !ok || st.facts.Status == string(domain.TaskSucceeded) {
		return
	}
	add(SectionStaticAnalysis, GapCompileDBNotGenerated, strPtr(cpp.GenerateTaskKey), st.facts.Reason)
}
