package run

import (
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
)

// anyEcosystemTools are the any-ecosystem analyzer tools (docs/ANALYZERS.md):
// planned for every snapshot with language source, not_probed without their flags.
var anyEcosystemTools = []string{"gitleaks", "jscpd", "lizard"}

// withoutAnyEcosystemTools drops the any-ecosystem rows of env.discover's
// analyzer tools after checking they are not_probed (these tests configure
// none of those tools).
func withoutAnyEcosystemTools(t *testing.T, in []envdoc.AnalyzerTool) []envdoc.AnalyzerTool {
	t.Helper()
	var out []envdoc.AnalyzerTool
	for _, a := range in {
		if slices.Contains(anyEcosystemTools, a.Tool) {
			if a.Status != envdoc.ObsNotProbed {
				t.Fatalf("any-ecosystem tool row %+v", a)
			}
			continue
		}
		out = append(out, a)
	}
	return out
}

// anyEcosystemTasks are the any-ecosystem task keys in catalog order.
func anyEcosystemTasks() []string { return []string{"lizard", "jscpd", "gitleaks"} }

// anyEcosystemNotRunRows are the report tool rows of the not-configured
// any-ecosystem tasks.
func anyEcosystemNotRunRows() []string {
	return []string{"any.gitleaks=not_run", "any.jscpd=not_run", "any.lizard=not_run"}
}
