package eslint

import (
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts/jststest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
)

// newReal configures the operator eslint (CODEYE_TEST_ESLINT) on the node
// toolchain (CODEYE_TEST_NODE); it skips without them.
func newReal(t *testing.T, typed bool) *Analyzer {
	t.Helper()
	script := jststest.ESLint(t)
	node := jststest.Node(t)
	a, err := newAnalyzer(Config{Script: script, Node: node}, typed)
	if err != nil {
		t.Fatalf("eslint: %v", err)
	}
	return a
}

// nodePaths are the host paths no structured output may hold.
func nodePaths(a *Analyzer, fx packtest.Fixture) []string {
	return []string{fx.Base, fx.Job.Scratch, fx.Job.Snapshot.WorkspaceDir, fx.Job.Snapshot.SnapshotDir, a.plugins.dir, a.script.Node.Dir, a.script.Resolved}
}
