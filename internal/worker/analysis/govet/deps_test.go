package govet

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps/depdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// writeDepsRecord stores the record a deps.go task of this attempt would
// have written for the root module.
func writeDepsRecord(t *testing.T, job analysis.Job, state, status string, reason *string, runID string) {
	t.Helper()
	key := "go/mod"
	r := depdoc.Record{SchemaVersion: depdoc.SchemaVersion, Kind: depdoc.Kind, Snapshot: job.Snapshot.Digest, RunID: runID, AttemptID: job.AttemptID,
		TaskKey: "deps.go", Module: depdoc.Module{ID: "go:.", Ecosystem: "go", Path: "."}, Manager: depdoc.Manager{Name: "go"}, Pinned: true,
		State: state, Reason: reason, TaskStatus: status, Cache: depdoc.Cache{Kind: depdoc.CacheShared, Key: &key},
		DroppedInputs: []depdoc.Dropped{}, FailedItems: []depdoc.Failed{}, Limitations: []string{}}
	b, err := depdoc.Marshal(r, depdoc.Expect{Snapshot: job.Snapshot.Digest, RunID: runID, AttemptID: job.AttemptID, TaskKey: "deps.go"})
	if err != nil {
		t.Fatal(err)
	}
	p := depdoc.Path(job.Scratch, "deps.go")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// go vet reads the dependency cache only through a usable record of this
// attempt; without one it keeps the offline path (empty scratch cache).
func TestVetReadsPreparedDependencyCache(t *testing.T) {
	files := map[string]string{"go.mod": fixGoMod, "clean/c.go": fixClean}
	missing := "module_not_on_proxy"
	cases := map[string]struct {
		write      func(t *testing.T, job analysis.Job)
		prepared   bool
		limitation string
	}{
		"no record": {func(*testing.T, analysis.Job) {}, false, "no_offline_module_cache"},
		"prepared": {func(t *testing.T, job analysis.Job) {
			writeDepsRecord(t, job, depdoc.StatePrepared, string(domain.TaskSucceeded), nil, job.RunID)
		}, true, LimDependenciesPrepared},
		"partial": {func(t *testing.T, job analysis.Job) {
			writeDepsRecord(t, job, depdoc.StatePartial, string(domain.TaskFailed), &missing, job.RunID)
		}, true, LimDependenciesPartiallyPrepared},
		"other run": {func(t *testing.T, job analysis.Job) {
			writeDepsRecord(t, job, depdoc.StatePrepared, string(domain.TaskSucceeded), nil, "00000000-0000-4000-8000-0000000000ff")
		}, false, "no_offline_module_cache"},
		"failed": {func(t *testing.T, job analysis.Job) {
			reason := "registry_unreachable"
			writeDepsRecord(t, job, depdoc.StateFailed, string(domain.TaskFailed), &reason, job.RunID)
		}, false, "no_offline_module_cache"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			depRoot := t.TempDir()
			h := newHandler(t, Config{DependencyCacheRoot: depRoot})
			fx := newFixture(t, "", files, domain.Scope{})
			c.write(t, fx.Job)
			runner := &mutatingRunner{inner: &executor.Executor{}}
			res := runAll(t, h, fx.Job, runner)["go-vet"]
			doc, _ := readDoc(t, res)
			if res.Status != domain.TaskSucceeded {
				t.Fatalf("status %s/%s", res.Status, res.Reason)
			}
			cache := filepath.Join(depRoot, "go", "mod")
			for _, s := range runner.specs {
				if (s.Env["GOMODCACHE"] == cache) != c.prepared || s.Env["GOPROXY"] != "off" {
					t.Fatalf("GOMODCACHE %q GOPROXY %q", s.Env["GOMODCACHE"], s.Env["GOPROXY"])
				}
				protected := false
				for _, r := range s.ProtectedRoots {
					protected = protected || r == cache
				}
				if protected != c.prepared {
					t.Fatalf("dependency cache protected = %v", protected)
				}
			}
			if !contains(doc.Limitations, c.limitation) {
				t.Fatalf("limitations %v, want %s", doc.Limitations, c.limitation)
			}
			if c.prepared && contains(doc.Limitations, "no_offline_module_cache") {
				t.Fatalf("offline limitation kept: %v", doc.Limitations)
			}
		})
	}
}
