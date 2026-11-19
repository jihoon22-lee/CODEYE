package rustpack

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps/depdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// writeRustRecord writes the deps.rust record of the root module, as the
// dependency task would.
func writeRustRecord(t *testing.T, fx fixture, state string) {
	t.Helper()
	job := fx.Job
	key := depdoc.TaskKey("rust", "")
	cache := "rust/cargo"
	r := depdoc.Record{SchemaVersion: depdoc.SchemaVersion, Kind: depdoc.Kind, Snapshot: job.Snapshot.Digest, RunID: job.RunID, AttemptID: job.AttemptID, TaskKey: key,
		Module: depdoc.Module{ID: "rust:.", Ecosystem: "rust", Path: "."}, Manager: depdoc.Manager{Name: "cargo"}, Pinned: true, State: state,
		TaskStatus: "succeeded", Cache: depdoc.Cache{Kind: depdoc.CacheShared, Key: &cache}, DroppedInputs: []depdoc.Dropped{}, FailedItems: []depdoc.Failed{}, Limitations: []string{}}
	if state != depdoc.StatePrepared {
		reason := depdoc.ReasonCancelled
		r.TaskStatus, r.Reason = "cancelled", &reason
	}
	b, err := depdoc.Marshal(r, depdoc.Expect{Snapshot: job.Snapshot.Digest, RunID: job.RunID, AttemptID: job.AttemptID, TaskKey: key})
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

func depsAnalyzer(t *testing.T, cacheRoot string) *Analyzer {
	t.Helper()
	dir := fakeToolchain(t)
	a, err := New(KindCheck, Config{Cargo: filepath.Join(dir, exeName("cargo")), DependencyCacheRoot: cacheRoot})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// cargo check reads the crates deps.rust fetched: CARGO_HOME is the shared
// cache and the limitation says so; without a record it keeps the scratch
// CARGO_HOME.
func TestCheckReadsTheFetchedCrates(t *testing.T) {
	cacheRoot := t.TempDir()
	for _, tc := range []struct {
		name, state, limitation string
		record                  bool
	}{
		{"prepared", depdoc.StatePrepared, LimDependenciesPrepared, true},
		{"partial", depdoc.StatePartial, LimDependenciesPartiallyPrepared, true},
		{"no record", "", "offline_scratch_cargo_home", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixture(t, "", fixtureFiles(t, "findings"), domain.Scope{}, true)
			if tc.record {
				writeRustRecord(t, fx, tc.state)
			}
			r := newReplay(t, "check-findings")
			res := packtest.RunTask(t, depsAnalyzer(t, cacheRoot), fx.Job, CheckName, r)
			doc, _ := readEnvelope(t, fx.Job, res)
			home := r.mains[0].Env["CARGO_HOME"]
			if tc.record != (home == filepath.Join(cacheRoot, "rust", "cargo")) {
				t.Fatalf("CARGO_HOME %s", home)
			}
			if r.mains[0].Env["CARGO_NET_OFFLINE"] != "true" {
				t.Fatalf("cargo check is not offline: %v", r.mains[0].Env)
			}
			if !slices.Contains(doc.Limitations, tc.limitation) || doc.Outcome.ExitClass != obsdoc.ExitSuccessWithFindings {
				t.Fatalf("limitations %v outcome %+v", doc.Limitations, doc.Outcome)
			}
		})
	}
}

// A module without Cargo.lock reads the lockfile deps.rust generated: it is
// in the workspace copy while cargo runs and removed afterwards. Without the
// generated file the record is not used.
func TestCheckPlacesTheGeneratedLockfile(t *testing.T) {
	files := fixtureFiles(t, "findings")
	lock := files["Cargo.lock"]
	delete(files, "Cargo.lock")
	cacheRoot := t.TempDir()

	fx := newFixture(t, "", files, domain.Scope{}, true)
	writeRustRecord(t, fx, depdoc.StatePrepared)
	r := newReplay(t, "check-findings")
	packtest.RunTask(t, depsAnalyzer(t, cacheRoot), fx.Job, CheckName, r)
	if r.mains[0].Env["CARGO_HOME"] == filepath.Join(cacheRoot, "rust", "cargo") {
		t.Fatal("the record is used without the generated lockfile")
	}

	fx = newFixture(t, "", files, domain.Scope{}, true)
	writeRustRecord(t, fx, depdoc.StatePrepared)
	gen := filepath.Join(filepath.Dir(depdoc.Path(fx.Job.Scratch, "deps.rust")), GeneratedLockfile)
	if err := os.WriteFile(gen, []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}
	placed := filepath.Join(fx.Job.Snapshot.WorkspaceDir, "Cargo.lock")
	r = newReplay(t, "check-findings")
	seen := ""
	r.onMain = func(executor.Spec) {
		b, _ := os.ReadFile(placed)
		seen = string(b)
	}
	res := packtest.RunTask(t, depsAnalyzer(t, cacheRoot), fx.Job, CheckName, r)
	doc, _ := readEnvelope(t, fx.Job, res)
	if seen != lock || r.mains[0].Env["CARGO_HOME"] != filepath.Join(cacheRoot, "rust", "cargo") || !slices.Contains(doc.Limitations, LimDependenciesPrepared) {
		t.Fatalf("lockfile placed %v, env %v, limitations %v", seen == lock, r.mains[0].Env["CARGO_HOME"], doc.Limitations)
	}
	if _, err := os.Lstat(placed); !os.IsNotExist(err) {
		t.Fatalf("generated lockfile left in the workspace copy: %v", err)
	}
}
