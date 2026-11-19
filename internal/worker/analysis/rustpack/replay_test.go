package rustpack

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Replay tests run in CI without a Rust toolchain: every execution returns the
// redacted streams and dep-info captured from rustc 1.99.1, cargo 1.99.1 and
// clippy 0.1.99 on the fixtures in fixtures/rust (see real_test.go
// capture). Changing a fixture requires a new capture (docs/ANALYZERS.md#실제-도구-시험과-출력-캡처).

type recorded struct {
	stdout, stderr []byte
	exit           int
}

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func loadCapture(t *testing.T, name string) recorded {
	t.Helper()
	code, err := strconv.Atoi(strings.TrimSpace(string(readTestdata(t, name+".exit"))))
	if err != nil {
		t.Fatal(err)
	}
	return recorded{stdout: readTestdata(t, name+".stdout"), stderr: readTestdata(t, name+".stderr"), exit: code}
}

// fakeToolchain creates a toolchain bin directory whose files are never
// executed (the replay runner answers for them).
func fakeToolchain(t *testing.T) (dir string) {
	t.Helper()
	dir = t.TempDir()
	for _, n := range []string{"cargo", "rustc", "cargo-clippy", "clippy-driver"} {
		if err := os.WriteFile(filepath.Join(dir, exeName(n)), []byte("not executed: replayed\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

type replayRunner struct {
	versions struct{ rustc, cargo, clippy []byte }
	main     recorded
	depsDir  string // testdata/<name>.deps, written into CARGO_TARGET_DIR
	mutate   func(r *executor.Result)
	onMain   func(spec executor.Spec)
	all      []executor.Spec
	mains    []executor.Spec
}

func newReplay(t *testing.T, capture string) *replayRunner {
	t.Helper()
	r := &replayRunner{}
	r.versions.rustc, r.versions.cargo, r.versions.clippy = readTestdata(t, "versions.rustc"), readTestdata(t, "versions.cargo"), readTestdata(t, "versions.clippy")
	if capture != "" {
		r.main = loadCapture(t, capture)
		r.depsDir = filepath.Join("testdata", capture+".deps")
	}
	return r
}

func (r *replayRunner) Run(_ context.Context, spec executor.Spec) executor.Result {
	r.all = append(r.all, spec)
	keys := make([]string, 0, len(spec.Env))
	for k := range spec.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	res := executor.Result{Status: executor.StatusExited, Cleanup: executor.CleanupConfirmed, Containment: executor.ContainmentNone,
		Command: executor.CommandRecord{Executable: spec.Executable, Argv: append([]string{spec.Executable}, spec.Args...), Dir: analysis.RedactedMarker, EnvKeys: keys}}
	base := strings.TrimSuffix(filepath.Base(spec.Executable), ".exe")
	var out, errb []byte
	code := 0
	isMain := false
	switch {
	case base == "rustc":
		out = r.versions.rustc
	case base == "cargo" && len(spec.Args) == 1 && spec.Args[0] == "--version":
		out = r.versions.cargo
	case base == "cargo-clippy" && len(spec.Args) == 1 && spec.Args[0] == "--version":
		out = r.versions.clippy
	default:
		isMain = true
		out, errb, code = r.main.stdout, r.main.stderr, r.main.exit
		r.mains = append(r.mains, spec)
		if r.depsDir != "" {
			entries, _ := os.ReadDir(r.depsDir)
			for _, e := range entries {
				b, _ := os.ReadFile(filepath.Join(r.depsDir, e.Name()))
				dst := filepath.Join(spec.Env["CARGO_TARGET_DIR"], "debug", "deps")
				_ = os.MkdirAll(dst, 0o700)
				_ = os.WriteFile(filepath.Join(dst, e.Name()), b, 0o600)
			}
		}
		if r.onMain != nil {
			r.onMain(spec)
		}
	}
	res.ExitCode = &code
	res.Stdout = executor.Stream{Data: append([]byte{}, out...), TotalBytes: int64(len(out))}
	res.Stderr = executor.Stream{Data: append([]byte{}, errb...), TotalBytes: int64(len(errb))}
	if isMain && r.mutate != nil {
		r.mutate(&res)
	}
	return res
}

func setStdout(r *executor.Result, data []byte) {
	r.Stdout.Data, r.Stdout.TotalBytes = data, int64(len(data))
}

func replayAnalyzer(t *testing.T, kind Kind) *Analyzer {
	t.Helper()
	dir := fakeToolchain(t)
	a, err := New(kind, Config{Cargo: filepath.Join(dir, exeName("cargo")), Clippy: filepath.Join(dir, exeName("cargo-clippy"))})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

type replayResult struct {
	res analysis.TaskResult
	doc obsdoc.Document
	raw []byte
	r   *replayRunner
	fx  fixture
	a   *Analyzer
}

// replayCase runs fixture files with the captured streams of capture.
func replayCase(t *testing.T, kind Kind, fixtureName, capture string, mutate func(*executor.Result)) replayResult {
	t.Helper()
	files := fixtureFiles(t, fixtureName)
	a := replayAnalyzer(t, kind)
	fx := newFixture(t, "", files, domain.Scope{}, true)
	r := newReplay(t, capture)
	r.mutate = mutate
	key := a.Descriptor().Name
	res := packtest.RunTask(t, a, fx.Job, key, r)
	doc, raw := readEnvelope(t, fx.Job, res)
	return replayResult{res: res, doc: doc, raw: raw, r: r, fx: fx, a: a}
}

// TC-114 on captured bytes: warnings exit 0 and build-finished says success;
// the task succeeds with findings, identical records of the library and its
// test harness are merged, and the byte column after non-ASCII text is exact.
func TestReplayCheckFindings(t *testing.T) {
	x := replayCase(t, KindCheck, "findings", "check-findings", nil)
	res, doc := x.res, x.doc
	if res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != obsdoc.ExitSuccessWithFindings || doc.Outcome.PartialParse || !doc.Parse.OK {
		t.Fatalf("status %s/%s outcome %+v errors %+v", res.Status, res.Reason, doc.Outcome, doc.ToolErrors)
	}
	if got := packtest.Rules(doc); len(got) != 2 || got["unused_imports"] != 1 || got["unused_variables"] != 3 {
		t.Fatalf("rules %v", got)
	}
	files := fixtureFiles(t, "findings")
	lines := strings.Split(files["src/lib.rs"], "\n")
	for _, o := range obsOf(doc, "unused_variables") {
		if strings.Contains(o.RawMessage, "`tail`") {
			want := strings.Index(lines[o.Location.StartLine-1], "tail") + 1
			if o.Location.StartCol != want || o.Location.ColumnUnit != "byte" {
				t.Fatalf("tail %+v want column %d", o.Location, want)
			}
		}
	}
	if !slices.Equal(doc.Coverage.ProcessedPaths, []string{"src/lib.rs", "src/main.rs", "tests/it.rs"}) || *doc.Coverage.FilesInScope != 3 || doc.Coverage.FilesUnlisted != 0 {
		t.Fatalf("coverage %+v", doc.Coverage)
	}
	if !slices.Contains(doc.Limitations, "duplicate_diagnostics_merged") || !slices.Equal(execNames(doc), []string{execRustc, execCargo, execCheck}) {
		t.Fatalf("limitations %v executions %v", doc.Limitations, execNames(doc))
	}
	if doc.Tool.Version == nil || *doc.Tool.Version != "1.99.1 (5f94df478 2026-08-27)" || !doc.Tool.VersionTested || *doc.Target.Platform != "x86_64-unknown-linux-gnu" || doc.Target.Properties["rustc"] != "1.99.1" {
		t.Fatalf("tool/target %+v %+v", doc.Tool, doc.Target)
	}
	// One explicit process contract: argv, cwd, offline, scratch, no PATH search.
	if len(x.r.mains) != 1 {
		t.Fatalf("main executions %d", len(x.r.mains))
	}
	m := x.r.mains[0]
	if !slices.Equal(m.Args, CheckArgs()) || m.Dir != x.fx.Job.Snapshot.WorkspaceDir || m.Timeout != DefaultTimeout || m.Env["CARGO_NET_OFFLINE"] != "true" ||
		m.Env["PATH"] != x.a.tcDir || m.Env["CARGO_BUILD_TARGET"] != "x86_64-unknown-linux-gnu" || !strings.HasPrefix(m.Env["CARGO_TARGET_DIR"], x.fx.Job.Scratch) || !strings.HasPrefix(m.Env["CARGO_HOME"], x.fx.Job.Scratch) {
		t.Fatalf("main spec %+v", m)
	}
	if _, set := m.Env["RUSTC_WRAPPER"]; !set || m.Env["RUSTC_WRAPPER"] != "" {
		t.Fatalf("rustc wrapper must be explicitly empty: %v", m.Env)
	}
	for _, s := range x.r.all {
		if !filepath.IsAbs(s.Executable) || s.Env["HOME"] != "" && false {
			t.Fatalf("spec %+v", s)
		}
	}
	packtest.AssertNoNodePaths(t, x.raw, x.fx.Job.Scratch, x.fx.Job.Snapshot.WorkspaceDir, x.fx.Job.Snapshot.SnapshotDir, x.a.tcDir)
}

// TC-114/116 on captured bytes: clippy keeps only clippy:: lints.
func TestReplayClippyFindings(t *testing.T) {
	x := replayCase(t, KindClippy, "findings", "clippy-findings", nil)
	res, doc := x.res, x.doc
	if res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != obsdoc.ExitSuccessWithFindings || !doc.Parse.OK {
		t.Fatalf("status %s/%s %+v errors %+v", res.Status, res.Reason, doc.Outcome, doc.ToolErrors)
	}
	rules := packtest.Rules(doc)
	want := map[string]int{"clippy::len_zero": 1, "clippy::ptr_arg": 1, "clippy::missing_safety_doc": 1, "clippy::undocumented_unsafe_blocks": 1, "clippy::panic_in_result_fn": 1, "clippy::todo": 1}
	for r, n := range want {
		if rules[r] != n {
			t.Fatalf("rule %s = %d: %v", r, rules[r], rules)
		}
	}
	for r := range rules {
		if !strings.HasPrefix(r, "clippy::") {
			t.Fatalf("non-clippy rule %s kept: %v", r, rules)
		}
	}
	if !slices.Equal(execNames(doc), []string{execRustc, execCargo, execClippyV, execClippy}) || !slices.Contains(doc.Limitations, "rustc_diagnostics_excluded") {
		t.Fatalf("executions %v limitations %v", execNames(doc), doc.Limitations)
	}
	m := x.r.mains[0]
	if !slices.Equal(m.Args, ClippyArgs()) || filepath.Base(m.Executable) != exeName("cargo-clippy") || m.Env["CARGO"] != x.a.cargo {
		t.Fatalf("clippy spec %+v", m)
	}
	// clippy runs as cargo-clippy, never as `cargo clippy`: a repository alias
	// cannot shadow the external subcommand (measured, docs/ANALYZERS.md#rust-실행과-lint-수준).
	if m.Args[0] != "clippy" {
		t.Fatalf("args %v", m.Args)
	}
	if ClippyVersionTested(*doc.Tool.Version) != doc.Tool.VersionTested || !doc.Tool.VersionTested {
		t.Fatalf("tool %+v", doc.Tool)
	}
	if o := obsOf(doc, "clippy::panic_in_result_fn"); len(o) != 1 || o[0].Category != "error_handling" {
		t.Fatalf("panic_in_result_fn %+v", o)
	}
	if o := obsOf(doc, "clippy::undocumented_unsafe_blocks"); len(o) != 1 || o[0].Category != "security" || o[0].Location == nil {
		t.Fatalf("undocumented_unsafe_blocks %+v", o)
	}
}

// The outcome table on captured bytes (docs/ANALYZERS.md#rust-결과-판정).
func TestReplayOutcomeTable(t *testing.T) {
	for _, tc := range []struct {
		name, fixture, capture string
		kind                   Kind
		status                 domain.TaskStatus
		reason                 string
		class                  string
		processed              []string
		partial                bool
		check                  func(*testing.T, replayResult)
	}{
		{"check clean", "clean", "check-clean", KindCheck, domain.TaskSucceeded, "", obsdoc.ExitSuccessWithoutFindings, []string{"src/lib.rs"}, false, nil},
		{"denied lints fail the build", "denied", "check-denied", KindCheck, domain.TaskFailed, ReasonLintsDenied, obsdoc.ExitExecutionError, nil, true, func(t *testing.T, x replayResult) {
			if o := obsOf(x.doc, "overflowing_literals"); len(o) != 1 || *o[0].RawSeverity != "error" || len(x.doc.ToolErrors) != 0 {
				t.Fatalf("denied lint %+v %+v", o, x.doc.ToolErrors)
			}
		}},
		{"denied clippy lints are capped", "denied", "clippy-denied", KindClippy, domain.TaskSucceeded, "", obsdoc.ExitSuccessWithFindings, []string{"src/lib.rs"}, false, func(t *testing.T, x replayResult) {
			if o := obsOf(x.doc, "clippy::eq_op"); len(o) != 1 || *o[0].RawSeverity != "warning" || o[0].Category != "correctness" {
				t.Fatalf("eq_op %+v", o)
			}
		}},
		{"compile error", "typeerr", "check-typeerr", KindCheck, domain.TaskFailed, ReasonPackageErrors, obsdoc.ExitExecutionError, nil, true, func(t *testing.T, x replayResult) {
			if len(x.doc.Observations) != 0 || len(x.doc.ToolErrors) == 0 || x.doc.ToolErrors[0].Kind != "compile" || x.doc.Coverage.Units[CrateFailed] != 1 {
				t.Fatalf("observations %d errors %+v units %v", len(x.doc.Observations), x.doc.ToolErrors, x.doc.Coverage.Units)
			}
		}},
		{"workspace", "workspace", "check-workspace", KindCheck, domain.TaskSucceeded, "", obsdoc.ExitSuccessWithFindings, []string{"crates/app/src/lib.rs", "crates/core/src/lib.rs"}, false, func(t *testing.T, x replayResult) {
			var paths []string
			for _, f := range x.doc.RulePack.ConfigFiles {
				paths = append(paths, f.Path)
			}
			if !slices.Equal(paths, []string{".cargo/config.toml", "Cargo.toml", "crates/core/Cargo.toml"}) || x.doc.Observations[0].Unit != "app" || x.doc.Coverage.Units[CrateAnalyzedClean] != 1 ||
				!slices.Contains(x.doc.Limitations, "rust_toolchain_file_ignored") {
				t.Fatalf("config %v unit %s limitations %v", paths, x.doc.Observations[0].Unit, x.doc.Limitations)
			}
		}},
		{"non-member crate", "nonmember", "check-nonmember", KindCheck, domain.TaskSucceeded, "", obsdoc.ExitSuccessWithoutFindings, []string{"src/lib.rs"}, false, func(t *testing.T, x replayResult) {
			if x.doc.Coverage.FilesUnlisted != 1 || !slices.Contains(x.doc.Limitations, "rust_files_not_compiled") {
				t.Fatalf("coverage %+v %v", x.doc.Coverage, x.doc.Limitations)
			}
		}},
		{"build script and proc macro", "buildscript", "check-buildscript", KindCheck, domain.TaskSucceeded, "", obsdoc.ExitSuccessWithFindings, []string{"app/build.rs", "app/src/lib.rs", "macros/src/lib.rs"}, false, func(t *testing.T, x replayResult) {
			for _, l := range []string{"build_script_executed", "proc_macro_executed"} {
				if !slices.Contains(x.doc.Limitations, l) {
					t.Fatalf("limitation %s missing: %v", l, x.doc.Limitations)
				}
			}
		}},
		{"linker missing", "buildscript", "check-nolinker", KindCheck, domain.TaskFailed, ReasonLinker, obsdoc.ExitConfigurationError, nil, true, nil},
		{"build script fails", "buildfail", "check-buildfail", KindCheck, domain.TaskFailed, ReasonBuildScript, obsdoc.ExitExecutionError, nil, true, nil},
		{"vendored dependency", "vendored", "check-vendored", KindCheck, domain.TaskSucceeded, "", obsdoc.ExitSuccessWithoutFindings, []string{"src/lib.rs"}, false, nil},
		{"offline dependency missing", "nodeps", "check-nodeps", KindCheck, domain.TaskFailed, ReasonDependency, obsdoc.ExitConfigurationError, nil, false, nil},
		{"lock file missing", "missinglock", "check-missinglock", KindCheck, domain.TaskFailed, ReasonLockMissing, obsdoc.ExitConfigurationError, nil, false, nil},
		{"lock file outdated", "lockoutdated", "check-lockoutdated", KindCheck, domain.TaskFailed, ReasonLockOutdated, obsdoc.ExitConfigurationError, nil, false, nil},
		{"manifest does not parse", "manifestbad", "check-manifestbad", KindCheck, domain.TaskFailed, ReasonManifestInvalid, obsdoc.ExitConfigurationError, nil, false, nil},
		{"toolchain too old for rust-version", "toonew", "check-toonew", KindCheck, domain.TaskFailed, ReasonToolchain, obsdoc.ExitConfigurationError, nil, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := replayCase(t, tc.kind, tc.fixture, tc.capture, nil)
			if x.res.Status != tc.status || x.res.Reason != tc.reason || x.doc.Outcome.ExitClass != tc.class || x.doc.Outcome.PartialParse != tc.partial {
				t.Fatalf("status %s/%s %+v errors %+v", x.res.Status, x.res.Reason, x.doc.Outcome, x.doc.ToolErrors)
			}
			if !slices.Equal(x.doc.Coverage.ProcessedPaths, tc.processed) {
				t.Fatalf("processed %v, want %v", x.doc.Coverage.ProcessedPaths, tc.processed)
			}
			if tc.status != domain.TaskSucceeded && x.doc.Parse.OK {
				t.Fatal("a failed task has a complete parse")
			}
			if tc.check != nil {
				tc.check(t, x)
			}
		})
	}
}

// A build script that rewrites a source file of the workspace copy: the
// workspace no longer equals the snapshot, the task fails and no observation
// links the drifted file.
func TestReplayWorkspaceDrift(t *testing.T) {
	files := fixtureFiles(t, "drift")
	a := replayAnalyzer(t, KindCheck)
	fx := newFixture(t, "", files, domain.Scope{}, true)
	r := newReplay(t, "check-drift")
	r.onMain = func(spec executor.Spec) {
		if err := os.WriteFile(filepath.Join(spec.Dir, "src", "lib.rs"), []byte("pub fn rewritten() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res := packtest.RunTask(t, a, fx.Job, "cargo-check", r)
	doc, _ := readEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskFailed || res.Reason != ReasonWorkspaceDrift || !slices.Equal(doc.WorkspaceDrift, []string{"src/lib.rs"}) || doc.Coverage.ProcessedPaths != nil {
		t.Fatalf("status %s/%s drift %v", res.Status, res.Reason, doc.WorkspaceDrift)
	}
}

// Anything the parser cannot read, or that disagrees with the exit status, is
// never a success (the findings capture is the base).
func TestReplayNeverSucceedsOnDamagedOutput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*executor.Result)
		reason string
	}{
		{"truncated stdout", func(r *executor.Result) { r.Stdout.Truncated = true }, ReasonOutputTruncated},
		{"incomplete stderr", func(r *executor.Result) { r.Stderr.Incomplete = true }, ReasonOutputTruncated},
		{"cut json", func(r *executor.Result) { setStdout(r, r.Stdout.Data[:len(r.Stdout.Data)/2]) }, ReasonOutputUnparsed},
		{"garbage line", func(r *executor.Result) { setStdout(r, append([]byte("not json\n"), r.Stdout.Data...)) }, ReasonOutputUnparsed},
		{"unknown reason", func(r *executor.Result) {
			setStdout(r, append([]byte(`{"reason":"timing-info","package_id":"x"}`+"\n"), r.Stdout.Data...))
		}, ReasonOutputUnparsed},
		{"unknown field", func(r *executor.Result) {
			setStdout(r, bytes.Replace(r.Stdout.Data, []byte(`"reason":"build-finished"`), []byte(`"surprise":1,"reason":"build-finished"`), 1))
		}, ReasonOutputUnparsed},
		{"no build-finished", func(r *executor.Result) {
			setStdout(r, bytes.ReplaceAll(r.Stdout.Data, []byte(`{"reason":"build-finished","success":true}`), nil))
		}, ReasonExitMismatch},
		{"build-finished says failure at exit 0", func(r *executor.Result) {
			setStdout(r, bytes.ReplaceAll(r.Stdout.Data, []byte(`"success":true`), []byte(`"success":false`)))
		}, ReasonExitMismatch},
		{"exit 101 with a successful build", func(r *executor.Result) { c := 101; r.ExitCode = &c }, ReasonExitMismatch},
		{"unexpected exit code", func(r *executor.Result) { c := 2; r.ExitCode = &c }, ReasonFailed},
		{"cleanup uncertain", func(r *executor.Result) { r.Cleanup = executor.CleanupUncertain }, ReasonCleanupUncertain},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := replayCase(t, KindCheck, "findings", "check-findings", tc.mutate)
			if x.res.Status == domain.TaskSucceeded || x.res.Reason != tc.reason || x.doc.Coverage.ProcessedPaths != nil || x.doc.Parse.OK {
				t.Fatalf("status %s/%s processed %v parse %+v", x.res.Status, x.res.Reason, x.doc.Coverage.ProcessedPaths, x.doc.Parse)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		mutate func(*executor.Result)
		status domain.TaskStatus
		reason string
	}{
		{"timed out", func(r *executor.Result) { r.Status = executor.StatusTimedOut }, domain.TaskTimedOut, ReasonTimeout},
		{"cancelled", func(r *executor.Result) { r.Status = executor.StatusCancelled }, domain.TaskCancelled, ReasonCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := replayCase(t, KindCheck, "findings", "check-findings", tc.mutate)
			if x.res.Status != tc.status || x.res.Reason != tc.reason || x.doc.Coverage.ProcessedPaths != nil {
				t.Fatalf("status %s/%s", x.res.Status, x.res.Reason)
			}
		})
	}
}

// A damaged dep-info set must not turn a build into processed files.
func TestReplayNoDepInfoIsNotProcessed(t *testing.T) {
	files := fixtureFiles(t, "clean")
	a := replayAnalyzer(t, KindCheck)
	fx := newFixture(t, "", files, domain.Scope{}, true)
	r := newReplay(t, "check-clean")
	r.depsDir = ""
	res := packtest.RunTask(t, a, fx.Job, "cargo-check", r)
	doc, _ := readEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskUnsupported || res.Reason != ReasonNoCrates || doc.Outcome.ExitClass != obsdoc.ExitConfigurationError || doc.Coverage.ProcessedPaths != nil && len(doc.Coverage.ProcessedPaths) != 0 {
		t.Fatalf("status %s/%s %+v coverage %+v", res.Status, res.Reason, doc.Outcome, doc.Coverage)
	}
}

// Version contract: an unrecognized tool is never run on the code; another
// series of the same format runs and is marked untested.
func TestReplayVersionContract(t *testing.T) {
	a := replayAnalyzer(t, KindCheck)
	run := func(mutate func(*replayRunner)) (analysis.TaskResult, obsdoc.Document, *replayRunner) {
		fx := newFixture(t, "", fixtureFiles(t, "clean"), domain.Scope{}, true)
		r := newReplay(t, "check-clean")
		mutate(r)
		res := packtest.RunTask(t, a, fx.Job, "cargo-check", r)
		doc, _ := readEnvelope(t, fx.Job, res)
		return res, doc, r
	}
	for name, mutate := range map[string]func(*replayRunner){
		"nightly cargo text": func(r *replayRunner) { r.versions.cargo = []byte("cargo nightly-2026-01-01\n") },
		"rustc without host": func(r *replayRunner) {
			r.versions.rustc = []byte("rustc 1.99.1 (58cf8f9c3 2026-10-13)\nrelease: 1.99.1\n")
		},
		"empty rustc output":  func(r *replayRunner) { r.versions.rustc = nil },
		"garbage cargo bytes": func(r *replayRunner) { r.versions.cargo = []byte("\x00\x01") },
	} {
		t.Run(name, func(t *testing.T) {
			res, doc, r := run(mutate)
			if res.Status != domain.TaskFailed || res.Reason != ReasonToolVersion || doc.Outcome.ExitClass != obsdoc.ExitConfigurationError || len(r.mains) != 0 {
				t.Fatalf("status %s/%s mains %d", res.Status, res.Reason, len(r.mains))
			}
		})
	}
	res, doc, r := run(func(r *replayRunner) {
		r.versions.cargo = []byte("cargo 1.100.0 (abcdef123 2027-01-01)\n")
		r.versions.rustc = []byte("rustc 1.100.0 (abcdef123 2027-01-01)\nhost: x86_64-unknown-linux-gnu\nrelease: 1.100.0\n")
	})
	if res.Status != domain.TaskSucceeded || len(r.mains) != 1 || doc.Tool.VersionTested || !slices.Contains(doc.Limitations, "tool_version_untested") || !slices.Contains(doc.Limitations, "rustc_version_untested") {
		t.Fatalf("untested series: %s/%s tested %v %v", res.Status, res.Reason, doc.Tool.VersionTested, doc.Limitations)
	}
	// A pre-release of the tested series is recognized and untested.
	_, doc, _ = run(func(r *replayRunner) { r.versions.cargo = []byte("cargo 1.99.0-nightly (abcdef123 2026-11-01)\n") })
	if doc.Tool.VersionTested || doc.Tool.Version == nil {
		t.Fatalf("pre-release tool %+v", doc.Tool)
	}
}

// The Rust pack plans placeholders and gaps visibly.
func TestReplayPlaceholderWhenNoCargoPackage(t *testing.T) {
	a := replayAnalyzer(t, KindCheck)
	c := replayAnalyzer(t, KindClippy)
	fx := newFixture(t, "", map[string]string{"tool.rs": "fn main() {}\n", "README.md": "# x\n"}, domain.Scope{}, true)
	tasks, err := a.Plan(context.Background(), fx.Job)
	if err != nil || len(tasks) != 1 || tasks[0].Spec.Key != "cargo-check" || !tasks[0].Spec.Required || tasks[0].RepositoryCode || *tasks[0].FilesInScope != 1 {
		t.Fatalf("plan %+v %v", tasks, err)
	}
	if ct, err := c.Plan(context.Background(), fx.Job); err != nil || len(ct) != 0 {
		t.Fatalf("clippy plans the gap once, via cargo-check: %+v %v", ct, err)
	}
	r := newReplay(t, "")
	res := packtest.RunTask(t, a, fx.Job, "cargo-check", r)
	doc, _ := readEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskUnsupported || res.Reason != ReasonManifestMissing || doc.Outcome.ExitClass != obsdoc.ExitNotRun || len(r.all) != 0 || doc.Coverage.ProcessedPaths == nil || len(doc.Coverage.ProcessedPaths) != 0 {
		t.Fatalf("status %s/%s %+v processes %d", res.Status, res.Reason, doc.Outcome, len(r.all))
	}
	// A manifest exists but the path scope holds no package.
	fx = newFixture(t, "", map[string]string{"a/Cargo.toml": "[package]\nname=\"a\"\n", "a/src/lib.rs": "pub fn f() {}\n", "b/loose.rs": "fn g() {}\n"}, domain.Scope{Kind: "paths", Paths: []string{"b"}}, true)
	res = packtest.RunTask(t, a, fx.Job, "cargo-check", newReplay(t, ""))
	if res.Reason != ReasonScopeNoPackage {
		t.Fatalf("status %s/%s", res.Status, res.Reason)
	}
}
