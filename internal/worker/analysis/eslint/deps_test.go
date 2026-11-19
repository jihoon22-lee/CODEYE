package eslint

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts/jststest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps/depdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// writeJSRecord writes a prepared deps.javascript record of the root
// install root listing node_modules, as the dependency task would.
func writeJSRecord(t *testing.T, fx packtest.Fixture) {
	t.Helper()
	job := fx.Job
	key := depdoc.TaskKey("javascript", "")
	want := jsts.Expect(job)
	want.TaskKey = key
	r := depdoc.Record{SchemaVersion: depdoc.SchemaVersion, Kind: depdoc.Kind, Snapshot: job.Snapshot.Digest, RunID: job.RunID, AttemptID: job.AttemptID, TaskKey: key,
		Module: depdoc.Module{ID: "javascript:.", Ecosystem: "javascript", Path: "."}, Manager: depdoc.Manager{Name: "npm"}, State: depdoc.StatePrepared, TaskStatus: "succeeded",
		Cache: depdoc.Cache{Kind: depdoc.CacheAttempt}, InstalledDirs: []string{"node_modules"}, DroppedInputs: []depdoc.Dropped{}, FailedItems: []depdoc.Failed{}, Limitations: []string{}}
	b, err := depdoc.Marshal(r, want)
	if err != nil {
		t.Fatal(err)
	}
	p := depdoc.Path(job.Scratch, key)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// Type-aware ESLint accepts the node_modules the dependency task recorded
// and says so; without the record the same tree is refused before ESLint
// runs. The untyped analyzer does not read the record.
func TestTypedESLintAcceptsTheRecordedNodeModules(t *testing.T) {
	for _, recorded := range []bool{true, false} {
		a := fakeAnalyzer(t, true)
		fx := packtest.NewFixture(t, "", replayFiles(t, "typed-deprecated"), domain.Scope{})
		if recorded {
			writeJSRecord(t, fx)
		}
		packtest.WriteTree(t, fx.Job.Snapshot.WorkspaceDir, map[string]string{"node_modules/zod/package.json": "{\"name\":\"zod\"}\n"})
		r := &jststest.Replay{T: t, Steps: jststest.LoadCapture(t, "typed-deprecated"), Match: func(step jststest.Recorded, s executor.Spec) bool {
			return (step.Name == execVersion) == slices.Equal(s.Args[1:], []string{"--version"})
		}}
		res := packtest.RunTask(t, a, fx.Job, "eslint-typed", r)
		doc, _ := jststest.ReadEnvelope(t, fx.Job, res)
		if !recorded {
			if res.Status != domain.TaskSkippedPolicy || res.Reason != ReasonForeignNodeModules || len(r.Specs) != 0 {
				t.Fatalf("without a record %s/%s specs %d", res.Status, res.Reason, len(r.Specs))
			}
			continue
		}
		if res.Status != domain.TaskSucceeded || !slices.Contains(doc.Limitations, jsts.LimDependenciesPrepared) {
			t.Fatalf("with the record %s/%s limitations %v", res.Status, res.Reason, doc.Limitations)
		}
	}
}
