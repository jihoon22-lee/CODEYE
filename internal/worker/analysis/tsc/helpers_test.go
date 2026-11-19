package tsc

import (
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts/jststest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
)

// newReal configures the operator tsc (CODEYE_TEST_TSC) on the node
// toolchain (CODEYE_TEST_NODE); it skips without them.
func newReal(t *testing.T) *Analyzer {
	t.Helper()
	script := jststest.Tsc(t)
	node := jststest.Node(t)
	a, err := New(Config{Script: script, Node: node})
	if err != nil {
		t.Fatalf("tsc: %v", err)
	}
	return a
}

// nodePaths are the host paths no structured output may hold.
func nodePaths(a *Analyzer, fx packtest.Fixture) []string {
	return []string{fx.Base, fx.Job.Scratch, fx.Job.Snapshot.WorkspaceDir, fx.Job.Snapshot.SnapshotDir, a.script.Package, a.script.Node.Dir, a.script.Resolved}
}
