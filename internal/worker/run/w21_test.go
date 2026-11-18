package run

import (
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
)

// w21Tools are the W21 any-ecosystem analyzer tools (docs/45): planned for
// every snapshot with language source, not_probed without their flags.
var w21Tools = []string{"gitleaks", "jscpd", "lizard"}

// withoutW21Tools drops the W21 rows of env.discover's analyzer tools after
// checking they are not_probed (these tests configure no W21 tool).
func withoutW21Tools(t *testing.T, in []envdoc.AnalyzerTool) []envdoc.AnalyzerTool {
	t.Helper()
	var out []envdoc.AnalyzerTool
	for _, a := range in {
		if slices.Contains(w21Tools, a.Tool) {
			if a.Status != envdoc.ObsNotProbed {
				t.Fatalf("W21 tool row %+v", a)
			}
			continue
		}
		out = append(out, a)
	}
	return out
}

// w21StaticTasks are the W21 task keys in catalog order.
func w21StaticTasks() []string { return []string{"lizard", "jscpd", "gitleaks"} }

// w21NotRunRows are the report tool rows of the not-configured W21 tasks.
func w21NotRunRows() []string {
	return []string{"any.gitleaks=not_run", "any.jscpd=not_run", "any.lizard=not_run"}
}
