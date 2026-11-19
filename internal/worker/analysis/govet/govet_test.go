package govet

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

func probeFiles() map[string]string {
	return map[string]string{
		"go.mod":          fixGoMod,
		"bad/bad.go":      fixBad,
		"bad/bad_test.go": fixBadT,
		"bad/x_test.go":   fixBadX,
		"clean/c.go":      fixClean,
		"clean/ign.go":    fixIgn,
		"tagged/t.go":     fixTag,
		"cg/c.go":         fixCgo,
		"README.md":       "# probe\n",
	}
}

func stateOf(doc Document, dir string) string {
	for _, p := range doc.Inventory {
		if p.Dir == dir {
			return p.State
		}
	}
	return ""
}

// TC-66: real go vet with findings exits 0; findings come from stdout JSON,
// locations are snapshot-relative and verified, unlisted packages are not clean.
func TestRealVetFindingsExitZero(t *testing.T) {
	// Spaces and non-ASCII in every node path (data root, workspace, scratch).
	base := filepath.Join(t.TempDir(), "공백 & (dir)")
	fx := newFixture(t, base, probeFiles(), domain.Scope{})
	before := packtest.TreeHash(t, fx.SourceRoot)
	snapBefore := packtest.TreeHash(t, fx.Job.Snapshot.SnapshotDir)
	h := newHandler(t, Config{})
	runner := &mutatingRunner{inner: &executor.Executor{}}
	results := runAll(t, h, fx.Job, runner)
	res, ok := results["go-vet"]
	if !ok || len(results) != 1 {
		t.Fatalf("tasks = %v", results)
	}
	doc, raw := readDoc(t, res)
	if res.Status != domain.TaskSucceeded || res.Reason != "" {
		t.Fatalf("status = %s/%s outcome=%+v failures=%+v", res.Status, res.Reason, doc.Outcome, doc.StderrFailures)
	}
	if doc.Outcome.ExitClass != ExitSuccessWithFindings || doc.Outcome.PartialParse {
		t.Fatalf("outcome = %+v", doc.Outcome)
	}
	if len(res.Executions) != 4 {
		t.Fatalf("executions = %d", len(res.Executions))
	}
	vet := res.Executions[3].Result
	if res.Executions[3].Name != execVet || vet.ExitCode == nil || *vet.ExitCode != 0 {
		t.Fatalf("go vet -json must exit 0 with findings: %+v", vet)
	}
	if len(doc.Observations) != 5 || doc.Parse.UnresolvedObservations != 0 {
		t.Fatalf("observations = %d unresolved=%d", len(doc.Observations), doc.Parse.UnresolvedObservations)
	}
	rules := map[string]int{}
	for _, o := range doc.Observations {
		rules[o.RuleID]++
		if o.Location == nil || o.LocationState != LocationResolved || o.RawSeverity != nil {
			t.Fatalf("observation not resolved: %+v", o)
		}
		if err := domain.ValidateEvidencePath(o.Location.Path); err != nil || !strings.HasPrefix(o.Location.Path, "bad/") {
			t.Fatalf("location path %q", o.Location.Path)
		}
		entry := fx.Job.Snapshot.Manifest.Entries
		found := false
		for _, e := range entry {
			if e.Path == o.Location.Path && e.SHA256 == o.Location.BlobSHA256 {
				found = true
			}
		}
		if !found || o.RawRef.StreamSHA256 != doc.Executions[3].Stdout.SHA256 || o.RawRef.ByteEnd <= o.RawRef.ByteStart {
			t.Fatalf("raw/code reference broken: %+v", o)
		}
		// The raw value at the reference contains the diagnostic message.
		stdout := vet.Stdout.Data[o.RawRef.ByteStart:o.RawRef.ByteEnd]
		if !strings.Contains(string(stdout), `"`+o.RuleID+`"`) {
			t.Fatalf("raw ref does not point at the analyzer value")
		}
	}
	if rules["copylocks"] != 2 || rules["printf"] != 3 {
		t.Fatalf("rules = %v", rules)
	}
	if got := stateOf(doc, "bad"); got != PackageAnalyzedWithFindings {
		t.Fatalf("bad state %q", got)
	}
	if got := stateOf(doc, "clean"); got != PackageAnalyzedClean {
		t.Fatalf("clean state %q", got)
	}
	for _, dir := range []string{"tagged", "cg"} {
		if got := stateOf(doc, dir); got != PackageNotAnalyzed {
			t.Fatalf("%s state %q (build-constraint/cgo excluded packages must not be clean)", dir, got)
		}
	}
	if doc.Coverage.UnlistedGoFiles != 2 || doc.Coverage.IgnoredGoFiles != 3 || doc.Coverage.ProcessedGoFiles == nil || *doc.Coverage.ProcessedGoFiles != 4 {
		t.Fatalf("coverage = %+v", doc.Coverage)
	}
	for _, want := range []string{"default_build_context_only", "cgo_disabled", "unlisted_go_files", "build_constraint_ignored_files", "no_offline_module_cache"} {
		if !contains(doc.Limitations, want) {
			t.Fatalf("limitation %s missing: %v", want, doc.Limitations)
		}
	}
	if doc.ToolVersion == nil || !strings.HasPrefix(*doc.ToolVersion, "go1.") || doc.Module.Path != "example.test/probe" {
		t.Fatalf("version/module = %v %+v", doc.ToolVersion, doc.Module)
	}
	// Execution spec boundaries.
	for _, s := range runner.specs {
		if s.WorkspaceRoot != fx.Job.Snapshot.WorkspaceDir || s.Env["GOPROXY"] != "off" || s.Env["GOTOOLCHAIN"] != "local" || s.Env["CGO_ENABLED"] != "0" || s.Env["GOFLAGS"] != "" {
			t.Fatalf("spec = %+v", s)
		}
		if !contains(s.ProtectedRoots, fx.Job.Snapshot.SnapshotDir) {
			t.Fatalf("snapshot not protected: %v", s.ProtectedRoots)
		}
	}
	for _, e := range res.Executions {
		if !strings.HasPrefix(e.StdoutPath, fx.Job.Scratch) || !strings.HasPrefix(e.StderrPath, fx.Job.Scratch) {
			t.Fatalf("stream files outside scratch: %+v", e)
		}
		for _, form := range redactForms([]string{fx.Job.Snapshot.WorkspaceDir}) {
			if strings.Contains(string(e.Result.Stdout.Data), form) || strings.Contains(string(e.Result.Stderr.Data), form) {
				t.Fatalf("raw stream carries the workspace path form %q", form)
			}
		}
		if strings.HasPrefix(e.Result.Command.Dir, "/") {
			t.Fatalf("raw stream/command carries the workspace path")
		}
		cmd := analysis.CommandFor(e.Result.Command)
		if cmd.Argv[0] != "go" && cmd.Argv[0] != "go.exe" || cmd.Dir != "." {
			t.Fatalf("manifest command = %+v", cmd)
		}
	}
	assertNoNodePaths(t, raw, base, fx.Job.Scratch, fx.Job.Snapshot.WorkspaceDir, fx.Job.Snapshot.SnapshotDir, h.goroot)
	if packtest.TreeHash(t, fx.SourceRoot) != before || packtest.TreeHash(t, fx.Job.Snapshot.SnapshotDir) != snapBefore {
		t.Fatal("original source or snapshot changed")
	}
	if len(doc.WorkspaceDrift) != 0 {
		t.Fatalf("drift = %v", doc.WorkspaceDrift)
	}

	// Determinism: a second attempt on a new clone yields the same observation IDs.
	fx2 := fx
	fx2.Job.Scratch = filepath.Join(base, "scratch2")
	ws2 := filepath.Join(fx.DataRoot, "attempt-workspace-2")
	if err := os.MkdirAll(fx2.Job.Scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	cloneInto(t, fx2.Job.Snapshot, ws2)
	fx2.Job.Snapshot.WorkspaceDir = ws2
	res2 := runAll(t, h, fx2.Job, &executor.Executor{})["go-vet"]
	doc2, _ := readDoc(t, res2)
	if len(doc2.Observations) != len(doc.Observations) {
		t.Fatalf("second run observation count differs")
	}
	for i := range doc.Observations {
		if doc.Observations[i].ID != doc2.Observations[i].ID {
			t.Fatalf("observation IDs are not stable across attempts")
		}
	}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// TC-67: a type error makes go vet exit 1 with stderr diagnostics; other
// packages' observations are kept, the task fails with partial_parse.
func TestRealVetTypeErrorPartial(t *testing.T) {
	files := probeFiles()
	files["typeerr/t.go"] = fixType
	fx := newFixture(t, "", files, domain.Scope{})
	h := newHandler(t, Config{})
	res := runAll(t, h, fx.Job, &executor.Executor{})["go-vet"]
	doc, _ := readDoc(t, res)
	if res.Status != domain.TaskFailed || res.Reason != ReasonPackageErrors {
		t.Fatalf("status = %s/%s", res.Status, res.Reason)
	}
	if doc.Outcome.ExitClass != ExitExecutionError || !doc.Outcome.PartialParse {
		t.Fatalf("outcome = %+v", doc.Outcome)
	}
	if len(doc.Observations) != 5 {
		t.Fatalf("observations from other packages must be kept, got %d", len(doc.Observations))
	}
	if got := stateOf(doc, "typeerr"); got != PackageFailedTypecheck {
		t.Fatalf("typeerr state %q", got)
	}
	if got := stateOf(doc, "clean"); got != PackageUnknown {
		t.Fatalf("clean state %q: a failed run cannot prove other packages clean", got)
	}
	if doc.Coverage.ProcessedGoFiles != nil {
		t.Fatalf("processed files must be unknown (null)")
	}
	found := false
	for _, f := range doc.StderrFailures {
		if f.Kind == "typecheck" && f.Path != nil && *f.Path == "typeerr/t.go" && f.Line == 3 {
			found = true
		}
	}
	if !found {
		t.Fatalf("stderr failure not mapped: %+v", doc.StderrFailures)
	}
}

// TC-68: offline dependency and toolchain failures are configuration errors,
// never "no findings"; no network fallback happens.
func TestRealVetOfflineConfigurationErrors(t *testing.T) {
	h := newHandler(t, Config{})
	cases := map[string]struct {
		files  map[string]string
		reason string
	}{
		"dependency": {map[string]string{"go.mod": "module example.test/d\n\ngo 1.27\n\nrequire example.invalid/x v1.0.0\n", "d.go": "package d\n\nimport _ \"example.invalid/x\"\n"}, ReasonDependency},
		"toolchain":  {map[string]string{"go.mod": "module example.test/t\n\ngo 1.99\n", "t.go": "package t\n"}, ReasonToolchain},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newFixture(t, "", c.files, domain.Scope{})
			res := runAll(t, h, fx.Job, &executor.Executor{})["go-vet"]
			doc, _ := readDoc(t, res)
			if res.Status != domain.TaskFailed || res.Reason != c.reason || doc.Outcome.ExitClass != ExitConfigurationError {
				t.Fatalf("status = %s/%s outcome=%+v failures=%+v", res.Status, res.Reason, doc.Outcome, doc.StderrFailures)
			}
			if doc.Outcome.ExitClass == ExitSuccessWithoutFindings || doc.Parse.OK || doc.Outcome.PartialParse || len(doc.Observations) != 0 {
				t.Fatal("configuration error reported as clean")
			}
			// Nothing was fetched into the empty per-task module cache.
			mc := filepath.Join(fx.Job.Scratch, "go-vet", "go-vet", "gopath", "pkg", "mod")
			_ = filepath.WalkDir(mc, func(p string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					t.Fatalf("module cache received a file: %s", p)
				}
				return nil
			})
		})
	}
}

// TC-66: clean module exits 0, zero observations is "tool reported 0", with coverage.
func TestRealVetCleanModule(t *testing.T) {
	fx := newFixture(t, "", map[string]string{"go.mod": fixGoMod, "clean/c.go": fixClean, "파일.go": "package probe\n\nconst X = 1\n"}, domain.Scope{})
	h := newHandler(t, Config{})
	res := runAll(t, h, fx.Job, &executor.Executor{})["go-vet"]
	doc, _ := readDoc(t, res)
	if res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != ExitSuccessWithoutFindings || len(doc.Observations) != 0 {
		t.Fatalf("status = %s outcome=%+v", res.Status, doc.Outcome)
	}
	if doc.Coverage.ProcessedGoFiles == nil || *doc.Coverage.ProcessedGoFiles != 2 || doc.Parse.EmptyValues != 2 {
		t.Fatalf("coverage = %+v parse=%+v", doc.Coverage, doc.Parse)
	}
}

// TC-67: truncated, garbled, cleanup-uncertain and start-failed executions
// on real tool bytes never become succeeded or "0 findings".
func TestRealVetFailureModes(t *testing.T) {
	h := newHandler(t, Config{})
	cases := []struct {
		name   string
		mutate func(sub string, r *executor.Result)
		status domain.TaskStatus
		reason string
	}{
		{"truncated", func(sub string, r *executor.Result) {
			if sub == "vet" {
				r.Stdout.Data = r.Stdout.Data[:len(r.Stdout.Data)/2]
				r.Stdout.Truncated = true
			}
		}, domain.TaskFailed, ReasonOutputTruncated},
		{"garbled", func(sub string, r *executor.Result) {
			if sub == "vet" {
				r.Stdout.Data = append(append([]byte{}, r.Stdout.Data...), []byte("{\"x\": [garbage")...)
			}
		}, domain.TaskFailed, ReasonOutputUnparsed},
		{"cleanup_uncertain", func(sub string, r *executor.Result) {
			if sub == "list" {
				r.Cleanup = executor.CleanupUncertain
			}
		}, domain.TaskFailed, ReasonCleanupUncertain},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fx := newFixture(t, "", probeFiles(), domain.Scope{})
			res := runAll(t, h, fx.Job, &mutatingRunner{inner: &executor.Executor{}, mutate: c.mutate})["go-vet"]
			doc, _ := readDoc(t, res)
			if res.Status != c.status || res.Reason != c.reason {
				t.Fatalf("status = %s/%s", res.Status, res.Reason)
			}
			if doc.Parse.OK || doc.Outcome.ExitClass == ExitSuccessWithFindings || doc.Outcome.ExitClass == ExitSuccessWithoutFindings {
				t.Fatalf("outcome = %+v", doc.Outcome)
			}
		})
	}
	t.Run("garbled_keeps_prefix", func(t *testing.T) {
		fx := newFixture(t, "", probeFiles(), domain.Scope{})
		res := runAll(t, h, fx.Job, &mutatingRunner{inner: &executor.Executor{}, mutate: cases[1].mutate})["go-vet"]
		doc, _ := readDoc(t, res)
		if len(doc.Observations) != 5 || doc.Parse.UnparsedBytes == 0 || !doc.Outcome.PartialParse {
			t.Fatalf("observations=%d parse=%+v", len(doc.Observations), doc.Parse)
		}
	})
	t.Run("missing_executable", func(t *testing.T) {
		fx := newFixture(t, "", probeFiles(), domain.Scope{})
		bad := *h
		bad.cfg.GoExecutable = filepath.Join(t.TempDir(), "no-go")
		res := runAll(t, &bad, fx.Job, &executor.Executor{})["go-vet"]
		if res.Status == domain.TaskSucceeded || len(res.Executions) != 1 || res.Executions[0].Result.Status != executor.StatusRejected {
			t.Fatalf("status=%s executions=%d", res.Status, len(res.Executions))
		}
	})
}

// TC-69: timeout and cancellation end the task with the real executor
// status and never run later steps after cancellation.
func TestRealVetTimeoutAndCancel(t *testing.T) {
	fx := newFixture(t, "", probeFiles(), domain.Scope{})
	h := newHandler(t, Config{Timeout: time.Millisecond})
	res := runAll(t, h, fx.Job, &executor.Executor{})["go-vet"]
	if res.Status != domain.TaskTimedOut || res.Reason != ReasonTimeout {
		t.Fatalf("status = %s/%s", res.Status, res.Reason)
	}
	for _, e := range res.Executions {
		if e.Result.Cleanup == executor.CleanupUncertain {
			t.Fatalf("cleanup uncertain after timeout: %+v", e.Result)
		}
	}

	fx2 := newFixture(t, "", probeFiles(), domain.Scope{})
	h2 := newHandler(t, Config{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tasks, _ := h2.Plan(context.Background(), fx2.Job)
	res2 := h2.Run(ctx, fx2.Job, tasks[0], &executor.Executor{})
	if res2.Status != domain.TaskCancelled || len(res2.Executions) != 0 {
		t.Fatalf("cancelled: status=%s executions=%d", res2.Status, len(res2.Executions))
	}
}

// TC-69: non-Go snapshot is planned but unsupported, zero executions, observations
// document still records why.
func TestNonGoSnapshotUnsupported(t *testing.T) {
	fx := newFixture(t, "", map[string]string{"README.md": "hello\n", "app.py": "print(1)\n"}, domain.Scope{})
	h := newHandler(t, Config{})
	runner := &mutatingRunner{inner: &executor.Executor{}}
	res := runAll(t, h, fx.Job, runner)["go-vet"]
	doc, _ := readDoc(t, res)
	if res.Status != domain.TaskUnsupported || res.Reason != analysis.ReasonAnalysisUnsupported || len(res.Executions) != 0 || len(runner.specs) != 0 {
		t.Fatalf("status=%s reason=%s executions=%d", res.Status, res.Reason, len(res.Executions))
	}
	if !contains(doc.Limitations, "no_go_files") || doc.Outcome.ExitClass != ExitNotRun {
		t.Fatalf("doc = %+v", doc)
	}
	// Pack member (docs/ANALYZERS.md#범위에-없는-생태계와-팩-공백): as a pack member go vet plans nothing when no Go
	// source is in scope; the composite shows the ecosystems' own gaps. The
	// placeholder stays when Go source is in scope but cannot be analyzed.
	ctx := context.Background()
	if tasks, err := Plan(ctx, fx.Job); err != nil || len(tasks) != 0 {
		t.Fatalf("member plan without Go: %+v %v", tasks, err)
	}
	scoped := newFixture(t, "", probeFiles(), domain.Scope{Kind: "paths", Paths: []string{"README.md"}})
	if tasks, err := Plan(ctx, scoped.Job); err != nil || len(tasks) != 0 {
		t.Fatalf("member plan with Go out of scope: %+v %v", tasks, err)
	}
	noModule := newFixture(t, "", map[string]string{"main.go": "package main\n"}, domain.Scope{})
	tasks, err := Plan(ctx, noModule.Job)
	if err != nil || len(tasks) != 1 || tasks[0].Spec.Key != "go-vet" || !tasks[0].Spec.Required {
		t.Fatalf("member plan with Go but no module: %+v %v", tasks, err)
	}
	res = runAll(t, h, noModule.Job, runner)["go-vet"]
	if doc, _ := readDoc(t, res); res.Status != domain.TaskUnsupported || !contains(doc.Limitations, "go_module_missing") {
		t.Fatalf("no module: %s %+v", res.Status, doc.Limitations)
	}
}

// TC-70: a workspace file changed after cloning is reported and
// its observations are not linked to code.
func TestWorkspaceDriftIsolatesLocations(t *testing.T) {
	fx := newFixture(t, "", probeFiles(), domain.Scope{})
	p := filepath.Join(fx.Job.Snapshot.WorkspaceDir, "bad", "bad.go")
	if err := os.WriteFile(p, []byte(fixBad+"\n// changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newHandler(t, Config{})
	res := runAll(t, h, fx.Job, &executor.Executor{})["go-vet"]
	doc, _ := readDoc(t, res)
	if res.Status != domain.TaskFailed || res.Reason != ReasonWorkspaceDrift {
		t.Fatalf("drift status = %s/%s", res.Status, res.Reason)
	}
	if !contains(doc.WorkspaceDrift, "bad/bad.go") || !contains(doc.Limitations, "workspace_drift") || len(doc.Observations) == 0 {
		t.Fatalf("drift = %v", doc.WorkspaceDrift)
	}
	for _, o := range doc.Observations {
		if o.Path != nil && *o.Path == "bad/bad.go" && (o.Location != nil || o.LocationState != LocationSourceDrift) {
			t.Fatalf("drifted file linked: %+v", o)
		}
	}
}

// TC-70: nested modules and path scope; each module is its own task.
func TestRealVetNestedModuleScope(t *testing.T) {
	files := probeFiles()
	files["tools/go.mod"] = "module example.test/tools\n\ngo 1.27\n"
	files["tools/x/x.go"] = fixClean
	fx := newFixture(t, "", files, domain.Scope{Kind: "paths", Paths: []string{"tools", "clean/c.go"}})
	h := newHandler(t, Config{})
	tasks, err := h.Plan(context.Background(), fx.Job)
	if err != nil || len(tasks) != 2 || tasks[0].Key != "go-vet" || tasks[1].Key != "go-vet.m01" {
		t.Fatalf("tasks = %+v err=%v", tasks, err)
	}
	results := runAll(t, h, fx.Job, &executor.Executor{})
	root, _ := readDoc(t, results["go-vet"])
	tools, _ := readDoc(t, results["go-vet.m01"])
	if strings.Join(root.Patterns, ",") != "./clean" || len(root.Observations) != 0 || root.Coverage.GoFilesInScope != 2 || root.Coverage.IgnoredGoFiles != 1 {
		t.Fatalf("root doc = %+v", root.Patterns)
	}
	if tools.Module.Dir != "tools" || stateOf(tools, "tools/x") != PackageAnalyzedClean {
		t.Fatalf("tools doc = %+v %+v", tools.Module, tools.Inventory)
	}
}

func cloneInto(t *testing.T, snap source.Snapshot, dst string) {
	t.Helper()
	if err := source.CloneWorkspace(context.Background(), snap, dst); err != nil {
		t.Fatalf("clone: %v", err)
	}
}

// TC-68: a filesystem replace that leaves the snapshot is refused before any
// process starts (the native go command would read that host path); a
// replace inside the snapshot is analyzed normally.
func TestReplaceOutsideSnapshotRefused(t *testing.T) {
	h := newHandler(t, Config{})
	// Quoted "a//b" is a module path, not a comment: only the go command's own
	// parser (go mod edit -json) sees the replace that follows it.
	outside := map[string]string{"go.mod": "module example.test/m\n\ngo 1.27\n\nrequire example.test/x v0.0.0\n\nreplace \"a//b\" => ../outside\n", "m.go": "package m\n"}
	fx := newFixture(t, "", outside, domain.Scope{})
	runner := &mutatingRunner{inner: &executor.Executor{}}
	res := runAll(t, h, fx.Job, runner)["go-vet"]
	doc, _ := readDoc(t, res)
	if res.Status != domain.TaskSkippedPolicy || res.Reason != ReasonReplaceOutside || len(runner.specs) != 2 || len(res.Executions) != 2 {
		t.Fatalf("status=%s/%s specs=%d", res.Status, res.Reason, len(runner.specs))
	}
	for _, sp := range runner.specs {
		if sp.Args[0] == "list" || sp.Args[0] == "vet" {
			t.Fatalf("%s ran despite an escaping replace", sp.Args[0])
		}
	}
	if !contains(doc.Limitations, "local_replace_outside_snapshot") {
		t.Fatalf("limitations = %v", doc.Limitations)
	}
	inside := map[string]string{
		"go.mod":               "module example.test/m\n\ngo 1.27\n\nrequire example.test/x v0.0.0\n\nreplace example.test/x => ./third_party/x\n",
		"m.go":                 "package m\n\nimport _ \"example.test/x\"\n",
		"third_party/x/go.mod": "module example.test/x\n\ngo 1.27\n",
		"third_party/x/x.go":   "package x\n",
	}
	fx2 := newFixture(t, "", inside, domain.Scope{})
	results := runAll(t, h, fx2.Job, &executor.Executor{})
	if r := results["go-vet"]; r.Status != domain.TaskSucceeded {
		doc, _ := readDoc(t, r)
		t.Fatalf("inside replace: %s/%s %+v", r.Status, r.Reason, doc.StderrFailures)
	}
}

// TC-70: vet exiting 0 after vetting nothing (every package excluded by
// build constraints or cgo) is not a clean result.
func TestRealVetNothingAnalyzable(t *testing.T) {
	fx := newFixture(t, "", map[string]string{"go.mod": fixGoMod, "cg/c.go": fixCgo, "tagged/t.go": fixTag}, domain.Scope{})
	h := newHandler(t, Config{})
	res := runAll(t, h, fx.Job, &executor.Executor{})["go-vet"]
	doc, _ := readDoc(t, res)
	if res.Status != domain.TaskUnsupported || res.Reason != ReasonNoPackages {
		t.Fatalf("status = %s/%s outcome=%+v failures=%+v", res.Status, res.Reason, doc.Outcome, doc.StderrFailures)
	}
	if doc.Coverage.GoFilesInScope != 2 || doc.Coverage.UnlistedGoFiles != 2 {
		t.Fatalf("coverage = %+v", doc.Coverage)
	}
}
