package tsc

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts/jststest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps/depdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// writeJSRecord writes the deps.javascript record of the root install
// root listing node_modules, as the dependency task would.
func writeJSRecord(t *testing.T, fx packtest.Fixture, state string) {
	t.Helper()
	job := fx.Job
	key := depdoc.TaskKey("javascript", "")
	want := jsts.Expect(job)
	want.TaskKey = key
	r := depdoc.Record{SchemaVersion: depdoc.SchemaVersion, Kind: depdoc.Kind, Snapshot: job.Snapshot.Digest, RunID: job.RunID, AttemptID: job.AttemptID, TaskKey: key,
		Module: depdoc.Module{ID: "javascript:.", Ecosystem: "javascript", Path: "."}, Manager: depdoc.Manager{Name: "npm"}, State: state, TaskStatus: "succeeded",
		Cache: depdoc.Cache{Kind: depdoc.CacheAttempt}, InstalledDirs: []string{"node_modules"}, DroppedInputs: []depdoc.Dropped{}, FailedItems: []depdoc.Failed{}, Limitations: []string{}}
	if state != depdoc.StatePrepared {
		reason := depdoc.ReasonRegistryUnreachable
		r.TaskStatus, r.Reason = "failed", &reason
		if state == depdoc.StateFailed {
			r.InstalledDirs = nil
		}
	}
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

// tsc accepts exactly the node_modules the dependency task recorded; a
// failed record (or none) leaves the tree foreign and the task is refused
// before tsc runs.
func TestTscAcceptsTheRecordedNodeModules(t *testing.T) {
	for _, c := range []struct {
		name, state string
		status      domain.TaskStatus
		limitation  string
	}{
		{"prepared", depdoc.StatePrepared, domain.TaskSucceeded, jsts.LimDependenciesPrepared},
		{"partial", depdoc.StatePartial, domain.TaskSucceeded, jsts.LimDependenciesPartiallyPrepared},
		{"failed", depdoc.StateFailed, domain.TaskSkippedPolicy, ""},
		{"no record", "", domain.TaskSkippedPolicy, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := fakeAnalyzer(t)
			fx := packtest.NewFixture(t, "", replayFiles(t, "clean"), domain.Scope{})
			if c.state != "" {
				writeJSRecord(t, fx, c.state)
			}
			packtest.WriteTree(t, fx.Job.Snapshot.WorkspaceDir, map[string]string{"node_modules/zod/package.json": "{\"name\":\"zod\"}\n"})
			r := &jststest.Replay{T: t, Steps: jststest.LoadCapture(t, "clean"), Match: func(step jststest.Recorded, s executor.Spec) bool {
				switch {
				case step.Name == execVersion:
					return slices.Equal(s.Args[1:], []string{"--version"})
				case strings.HasPrefix(step.Name, execConfig):
					return slices.Contains(s.Args, "--showConfig")
				}
				return !slices.Contains(s.Args, "--showConfig") && slices.Contains(s.Args, "--noEmit")
			}}
			res := packtest.RunTask(t, a, fx.Job, "tsc", r)
			doc, _ := jststest.ReadEnvelope(t, fx.Job, res)
			if res.Status != c.status || (c.status == domain.TaskSkippedPolicy) != (len(r.Specs) == 0) {
				t.Fatalf("%s/%s specs %d", res.Status, res.Reason, len(r.Specs))
			}
			if c.status == domain.TaskSkippedPolicy && res.Reason != ReasonForeignNodeModules {
				t.Fatalf("reason %s", res.Reason)
			}
			if c.limitation != "" && (!slices.Contains(doc.Limitations, c.limitation) || slices.Contains(doc.Limitations, "type_check_without_dependencies")) {
				t.Fatalf("limitations %v", doc.Limitations)
			}
		})
	}
}
