package rustpack

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Real-tool tests run only with CODEYE_TEST_CARGO / CODEYE_TEST_CLIPPY (CI has
// no Rust toolchain). CODEYE_W20_CAPTURE=DIR also writes the redacted streams
// and dep-info of each case to DIR, which is how testdata/ was captured
// (docs/44 §9). The replay tests run the same fixtures in CI on those bytes.

// realRun runs one task of a fixture with the real toolchain under the grant
// and returns everything a test checks.
func realRun(t *testing.T, kind Kind, name, key, capName string, scope domain.Scope, files map[string]string) (analysis.TaskResult, obsdoc.Document, []byte, fixture, *Analyzer) {
	t.Helper()
	if files == nil {
		files = fixtureFiles(t, name)
	}
	if kind == KindClippy {
		testClippy(t)
	} else {
		testToolchain(t)
	}
	fx := newFixture(t, "", files, scope, true)
	a := newReal(t, kind)
	res := runTask(t, a, fx.Job, key, &executor.Executor{})
	if capName != "" {
		capture(t, capName, fx.Job, key, res, a)
	}
	doc, raw := readEnvelope(t, fx.Job, res)
	assertNoNodePaths(t, raw, fx.Job.Scratch, fx.Job.Snapshot.WorkspaceDir, fx.Job.Snapshot.SnapshotDir, fx.Job.Snapshot.Root, fx.SourceRoot, a.tcDir, a.cargo, a.linker, os.TempDir())
	return res, doc, raw, fx, a
}

func execNames(doc obsdoc.Document) []string {
	var out []string
	for _, e := range doc.Executions {
		out = append(out, e.Name)
	}
	return out
}

func obsOf(doc obsdoc.Document, rule string) []obsdoc.Observation {
	var out []obsdoc.Observation
	for _, o := range doc.Observations {
		if o.RuleID == rule {
			out = append(out, o)
		}
	}
	return out
}

// TC-114: cargo check on a package with rustc warnings. Warnings exit 0, the
// build-finished record says success, the task succeeds with findings.
// --all-targets reports each library warning once per unit (library and its
// test harness); the adapter merges the identical records.
func TestRealCargoCheckFindings(t *testing.T) {
	testToolchain(t)
	files := fixtureFiles(t, "findings")
	fx := newFixture(t, filepath.Join(t.TempDir(), "공백 & (dir)"), files, domain.Scope{}, true)
	before, snapBefore := treeHash(t, fx.SourceRoot), treeHash(t, fx.Job.Snapshot.SnapshotDir)
	a := newReal(t, KindCheck)
	mr := &mutatingRunner{inner: &executor.Executor{}}
	res := runTask(t, a, fx.Job, "cargo-check", mr)
	capture(t, "check-findings", fx.Job, "cargo-check", res, a)
	doc, raw := readEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != obsdoc.ExitSuccessWithFindings || doc.Outcome.PartialParse || !doc.Parse.OK {
		t.Fatalf("status %s/%s outcome %+v errors %+v", res.Status, res.Reason, doc.Outcome, doc.ToolErrors)
	}
	if !slices.Equal(execNames(doc), []string{execRustc, execCargo, execCheck}) {
		t.Fatalf("executions %v", execNames(doc))
	}
	main := res.Executions[len(res.Executions)-1]
	if main.Result.ExitCode == nil || *main.Result.ExitCode != 0 {
		t.Fatalf("warnings must exit 0: %+v", main.Result)
	}
	if got, want := ruleCount(doc), map[string]int{"unused_imports": 1, "unused_variables": 3}; len(got) != len(want) || got["unused_imports"] != 1 || got["unused_variables"] != 3 {
		t.Fatalf("rules %v, want %v (the #[allow]ed variable must not appear)", got, want)
	}
	for _, o := range doc.Observations {
		if o.Location == nil || o.LocationState != obsdoc.LocationResolved || o.Category != "maintainability" || o.RawSeverity == nil || *o.RawSeverity != "warning" || o.Suppressed || o.Unit != "findings" {
			t.Fatalf("observation %+v", o)
		}
	}
	// The column after non-ASCII text is a byte column (rustc reports characters).
	src := files["src/lib.rs"]
	lines := strings.Split(src, "\n")
	tail := obsOf(doc, "unused_variables")
	found := false
	for _, o := range tail {
		if strings.Contains(o.RawMessage, "`tail`") {
			want := strings.Index(lines[o.Location.StartLine-1], "tail") + 1
			if o.Location.StartCol != want || o.Location.ColumnUnit != "byte" || o.Location.EndCol != want+len("tail") {
				t.Fatalf("tail location %+v, want byte column %d", o.Location, want)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("tail diagnostic missing: %v", ruleCount(doc))
	}
	if !slices.Equal(doc.Coverage.ProcessedPaths, []string{"src/lib.rs", "src/main.rs", "tests/it.rs"}) || *doc.Coverage.FilesInScope != 3 || doc.Coverage.FilesUnlisted != 0 ||
		doc.Coverage.Units[CrateAnalyzedWithFindings] != 1 || doc.Coverage.Unit != "crate" {
		t.Fatalf("coverage %+v", doc.Coverage)
	}
	if doc.Tool.Version == nil || !doc.Tool.VersionTested || doc.Tool.Name != "cargo" || doc.Target.Platform == nil || doc.Target.Properties["rustc"] == "" || doc.Target.Properties["host_only"] != "true" {
		t.Fatalf("tool/target %+v %+v", doc.Tool, doc.Target)
	}
	rp := doc.RulePack
	if rp.ID != CheckRulePackID || rp.RepositoryConfig != obsdoc.RepoConfigHonored || rp.SourceSuppressions != obsdoc.SuppressHonored || rp.ConfigFiles == nil || len(rp.ConfigFiles) != 0 || len(rp.UnreportedSuppressionRules) != 0 {
		t.Fatalf("rule pack %+v", rp)
	}
	for _, l := range []string{"duplicate_diagnostics_merged", "default_features_only", "host_target_only", "offline_scratch_cargo_home", "rustc_wrapper_overridden"} {
		if !slices.Contains(doc.Limitations, l) {
			t.Fatalf("limitation %s missing: %v", l, doc.Limitations)
		}
	}
	// Process contract: argv, cwd, an explicit environment, offline, scratch.
	var check executor.Spec
	for _, s := range mr.specs {
		if len(s.Args) > 0 && s.Args[0] == "check" {
			check = s
		}
	}
	if !slices.Equal(check.Args, CheckArgs()) || check.Dir != filepath.Join(fx.Job.Snapshot.WorkspaceDir) || check.Timeout <= 0 {
		t.Fatalf("check spec %+v", check)
	}
	env := check.Env
	for k, want := range map[string]string{"PATH": a.tcDir, "RUSTC": a.rustc, "CARGO_NET_OFFLINE": "true", "RUSTC_WRAPPER": "", "RUSTC_WORKSPACE_WRAPPER": "", "LANG": "C.UTF-8"} {
		if v, ok := env[k]; !ok || v != want {
			t.Fatalf("env %s = %q (set %v), want %q", k, v, ok, want)
		}
	}
	taskDir := filepath.Join(fx.Job.Scratch, "analyzers", "cargo-check")
	if env["CARGO_HOME"] != filepath.Join(taskDir, "cargo-home") || env["CARGO_TARGET_DIR"] != filepath.Join(taskDir, "target") || env["CARGO_BUILD_TARGET"] != *doc.Target.Platform {
		t.Fatalf("scratch env %v", env)
	}
	if _, err := os.Stat(filepath.Join(fx.Job.Snapshot.WorkspaceDir, "target")); err == nil {
		t.Fatal("target directory was created in the workspace copy")
	}
	assertNoNodePaths(t, raw, filepath.Dir(fx.SourceRoot), fx.Job.Scratch, fx.Job.Snapshot.WorkspaceDir, fx.Job.Snapshot.SnapshotDir, a.tcDir, a.cargo, a.linker)
	if treeHash(t, fx.SourceRoot) != before || treeHash(t, fx.Job.Snapshot.SnapshotDir) != snapBefore {
		t.Fatal("source or snapshot changed")
	}
	// Same input, fresh attempt: IDs are content derived.
	fx2 := newFixture(t, "", files, domain.Scope{}, true)
	doc2, _ := readEnvelope(t, fx2.Job, runTask(t, a, fx2.Job, "cargo-check", &executor.Executor{}))
	if len(doc2.Observations) != len(doc.Observations) {
		t.Fatalf("rerun observations %d vs %d", len(doc2.Observations), len(doc.Observations))
	}
	for i := range doc.Observations {
		if doc.Observations[i].ID != doc2.Observations[i].ID {
			t.Fatalf("observation id changed across runs: %s vs %s", doc.Observations[i].ID, doc2.Observations[i].ID)
		}
	}
}

// TC-114: a clean package is success_without_findings with processed files.
func TestRealCargoCheckClean(t *testing.T) {
	res, doc, _, _, _ := realRun(t, KindCheck, "clean", "cargo-check", "check-clean", domain.Scope{}, nil)
	if res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != obsdoc.ExitSuccessWithoutFindings || len(doc.Observations) != 0 ||
		!slices.Equal(doc.Coverage.ProcessedPaths, []string{"src/lib.rs"}) || doc.Coverage.Units[CrateAnalyzedClean] != 1 {
		t.Fatalf("status %s/%s doc %+v", res.Status, res.Reason, doc.Outcome)
	}
}

// TC-114/116: clippy keeps only clippy:: diagnostics, runs the review lints
// (unsafe and panic) and honors #[allow(clippy::...)]. The rustc diagnostics
// it also prints are the check task's, not duplicated here.
func TestRealClippyFindings(t *testing.T) {
	testClippy(t)
	files := fixtureFiles(t, "findings")
	fx := newFixture(t, "", files, domain.Scope{}, true)
	a := newReal(t, KindClippy)
	res := runTask(t, a, fx.Job, "clippy", &executor.Executor{})
	capture(t, "clippy-findings", fx.Job, "clippy", res, a)
	doc, _ := readEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != obsdoc.ExitSuccessWithFindings || !doc.Parse.OK {
		t.Fatalf("status %s/%s outcome %+v errors %+v", res.Status, res.Reason, doc.Outcome, doc.ToolErrors)
	}
	if !slices.Equal(execNames(doc), []string{execRustc, execCargo, execClippyV, execClippy}) {
		t.Fatalf("executions %v", execNames(doc))
	}
	rules := ruleCount(doc)
	want := map[string]int{"clippy::len_zero": 1, "clippy::ptr_arg": 1, "clippy::missing_safety_doc": 1, "clippy::undocumented_unsafe_blocks": 1, "clippy::panic_in_result_fn": 1, "clippy::todo": 1}
	for r, n := range want {
		if rules[r] != n {
			t.Fatalf("rule %s = %d, want %d: %v", r, rules[r], n, rules)
		}
	}
	for r := range rules {
		if !strings.HasPrefix(r, "clippy::") {
			t.Fatalf("rustc diagnostic %s kept by the clippy task: %v", r, rules)
		}
	}
	for r, c := range map[string]string{"clippy::undocumented_unsafe_blocks": "security", "clippy::panic_in_result_fn": "error_handling", "clippy::todo": "error_handling", "clippy::len_zero": "maintainability"} {
		if o := obsOf(doc, r); len(o) == 0 || o[0].Category != c {
			t.Fatalf("category of %s: %+v", r, o)
		}
	}
	if !slices.Contains(doc.Limitations, "rustc_diagnostics_excluded") || !slices.Contains(doc.Limitations, "cap_lints_warn") || doc.Tool.Name != "clippy" || doc.Tool.Version == nil || !doc.Tool.VersionTested {
		t.Fatalf("limitations %v tool %+v", doc.Limitations, doc.Tool)
	}
	if doc.Analyzer.Required || doc.RulePack.ID != ClippyRulePackID || !slices.Contains(doc.RulePack.Selection, "clippy::undocumented_unsafe_blocks") {
		t.Fatalf("analyzer/rule pack %+v %+v", doc.Analyzer, doc.RulePack)
	}
}

// docs/06 §1: clippy also prints rustc's own diagnostics. They are exactly the
// check task's observations, so keeping only clippy:: lints in the clippy task
// removes duplicates and nothing else.
func TestRealClippyRustcOverlapIsTheCheckTask(t *testing.T) {
	testClippy(t)
	files := fixtureFiles(t, "findings")
	fxA := newFixture(t, "", files, domain.Scope{}, true)
	check := newReal(t, KindCheck)
	docCheck, _ := readEnvelope(t, fxA.Job, runTask(t, check, fxA.Job, "cargo-check", &executor.Executor{}))
	fxB := newFixture(t, "", files, domain.Scope{}, true)
	clippy := newReal(t, KindClippy)
	res := runTask(t, clippy, fxB.Job, "clippy", &executor.Executor{})
	var raw []byte
	for _, e := range res.Executions {
		if e.Name == execClippy {
			raw = e.Result.Stdout.Data
		}
	}
	st := ParseStream(raw)
	type key struct {
		code string
		line int
	}
	fromClippy, fromCheck := map[key]bool{}, map[key]bool{}
	for _, m := range st.Messages {
		d := m.Diag
		if (d.Level == "warning" || d.Level == "error") && d.Code != "" && !strings.HasPrefix(d.Code, "clippy::") {
			if p, ok := primarySpan(d); ok {
				fromClippy[key{d.Code, p.LineStart}] = true
			}
		}
	}
	for _, o := range docCheck.Observations {
		fromCheck[key{o.RuleID, o.Location.StartLine}] = true
	}
	if len(fromClippy) == 0 || len(fromClippy) != len(fromCheck) {
		t.Fatalf("rustc diagnostics in the clippy run %v, check observations %v", fromClippy, fromCheck)
	}
	for k := range fromCheck {
		if !fromClippy[k] {
			t.Fatalf("check observation %v is not in the clippy output", k)
		}
	}
}

// docs/39 §5.1: source suppressions are honored. #[allow] hides the diagnostic
// and no flag reports it as suppressed: --force-warn prints it as an ordinary
// warning with no marker, so a vanished finding cannot be told fixed from
// allowed (measured with the pinned rustc).
func TestRealSuppressionsCannotBeReported(t *testing.T) {
	testToolchain(t)
	files := fixtureFiles(t, "findings")
	fx := newFixture(t, "", files, domain.Scope{}, true)
	a := newReal(t, KindCheck)
	run := func(extra map[string]string) []Diag {
		dir := t.TempDir()
		for _, d := range []string{"home", "target", "tmp"} {
			if err := os.Mkdir(filepath.Join(dir, d), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		env := a.env(filepath.Join(dir, "home"), filepath.Join(dir, "target"), "")
		for k, v := range extra {
			env[k] = v
		}
		r := (&executor.Executor{}).Run(context.Background(), executor.Spec{Executable: a.cargo, Args: CheckArgs(), WorkspaceRoot: fx.Job.Snapshot.WorkspaceDir, Dir: fx.Job.Snapshot.WorkspaceDir,
			ProtectedRoots: []string{fx.Job.Snapshot.SnapshotDir}, TempParent: filepath.Join(dir, "tmp"), Env: env, Timeout: time.Minute, StdoutLimit: 8 << 20, StderrLimit: 1 << 20})
		if r.Status != executor.StatusExited || r.ExitCode == nil || *r.ExitCode != 0 {
			t.Fatalf("cargo: %+v", r)
		}
		var out []Diag
		for _, m := range ParseStream(r.Stdout.Data).Messages {
			if m.Diag.Code == "unused_variables" {
				out = append(out, m.Diag)
			}
		}
		return out
	}
	hasHidden := func(ds []Diag) bool {
		for _, d := range ds {
			if strings.Contains(d.Message, "`hidden`") {
				return true
			}
		}
		return false
	}
	if plain := run(nil); hasHidden(plain) {
		t.Fatal("an #[allow]ed lint was reported")
	}
	forced := run(map[string]string{"RUSTFLAGS": "--force-warn=unused_variables"})
	var hidden *Diag
	for i := range forced {
		if strings.Contains(forced[i].Message, "`hidden`") {
			hidden = &forced[i]
		}
	}
	if hidden == nil {
		t.Fatal("--force-warn did not reveal the allowed lint")
	}
	if hidden.Level != "warning" {
		t.Fatalf("a forced diagnostic is %s", hidden.Level)
	}
	for _, c := range hidden.Children {
		if strings.Contains(c.Message, "allow") || strings.Contains(c.Message, "suppress") {
			t.Fatalf("the forced diagnostic marks its suppression: %+v", c)
		}
	}
}

// TC-114/116: deny-by-default lints fail the build under cargo check (the
// code does not compile as the project configured it): the task fails
// lints_denied, nothing is claimed processed, and the lint stays an
// observation with severity error. Under clippy the lint levels are capped at
// warn, so a denied clippy lint is a finding and the task succeeds.
func TestRealDeniedLints(t *testing.T) {
	testClippy(t)
	res, doc, _, _, _ := realRun(t, KindCheck, "denied", "cargo-check", "check-denied", domain.Scope{}, nil)
	if res.Status != domain.TaskFailed || res.Reason != ReasonLintsDenied || doc.Outcome.ExitClass != obsdoc.ExitExecutionError || doc.Coverage.ProcessedPaths != nil || !doc.Outcome.PartialParse {
		t.Fatalf("check: %s/%s %+v", res.Status, res.Reason, doc.Outcome)
	}
	o := obsOf(doc, "overflowing_literals")
	if len(o) != 1 || o[0].RawSeverity == nil || *o[0].RawSeverity != "error" || len(doc.ToolErrors) != 0 {
		t.Fatalf("denied lint %+v errors %+v", o, doc.ToolErrors)
	}
	res, doc, _, _, _ = realRun(t, KindClippy, "denied", "clippy", "clippy-denied", domain.Scope{}, nil)
	if res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != obsdoc.ExitSuccessWithFindings {
		t.Fatalf("clippy: %s/%s %+v", res.Status, res.Reason, doc.Outcome)
	}
	if o := obsOf(doc, "clippy::eq_op"); len(o) != 1 || *o[0].RawSeverity != "warning" || o[0].Category != "correctness" {
		t.Fatalf("eq_op %+v", o)
	}
}

// TC-114: a compile error fails the task (exit 101): the error is a tool
// error, not an observation, and the library is not claimed processed.
func TestRealCompileError(t *testing.T) {
	res, doc, _, _, _ := realRun(t, KindCheck, "typeerr", "cargo-check", "check-typeerr", domain.Scope{}, nil)
	if res.Status != domain.TaskFailed || res.Reason != ReasonPackageErrors || doc.Outcome.ExitClass != obsdoc.ExitExecutionError || doc.Coverage.ProcessedPaths != nil || !doc.Outcome.PartialParse {
		t.Fatalf("status %s/%s %+v", res.Status, res.Reason, doc.Outcome)
	}
	if len(doc.Observations) != 0 || len(doc.ToolErrors) == 0 || doc.ToolErrors[0].Kind != "compile" || doc.ToolErrors[0].Path == nil || *doc.ToolErrors[0].Path != "src/lib.rs" {
		t.Fatalf("observations %d tool errors %+v", len(doc.Observations), doc.ToolErrors)
	}
	main := res.Executions[len(res.Executions)-1]
	if main.Result.ExitCode == nil || *main.Result.ExitCode != 101 {
		t.Fatalf("compile errors exit 101: %+v", main.Result)
	}
}

// TC-115: a virtual workspace is one task run with --workspace. The workspace
// lint table silences the member that inherits it, the other member warns,
// the repository's Cargo configuration and lint tables are recorded as the
// rule context, and rust-toolchain.toml (channel 1.0.0) has no effect because
// the operator's own cargo and rustc run, never a rustup proxy.
func TestRealWorkspace(t *testing.T) {
	res, doc, _, fx, _ := realRun(t, KindCheck, "workspace", "cargo-check", "check-workspace", domain.Scope{}, nil)
	if res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != obsdoc.ExitSuccessWithFindings {
		t.Fatalf("status %s/%s errors %+v", res.Status, res.Reason, doc.ToolErrors)
	}
	if got := ruleCount(doc); len(got) != 1 || got["unused_variables"] != 1 || *doc.Observations[0].Path != "crates/app/src/lib.rs" || doc.Observations[0].Unit != "app" {
		t.Fatalf("rules %v obs %+v", got, doc.Observations)
	}
	if !slices.Equal(doc.Coverage.ProcessedPaths, []string{"crates/app/src/lib.rs", "crates/core/src/lib.rs"}) || doc.Coverage.Units[CrateAnalyzedWithFindings] != 1 || doc.Coverage.Units[CrateAnalyzedClean] != 1 {
		t.Fatalf("coverage %+v", doc.Coverage)
	}
	var paths []string
	for _, f := range doc.RulePack.ConfigFiles {
		paths = append(paths, f.Path)
	}
	if !slices.Equal(paths, []string{".cargo/config.toml", "Cargo.toml", "crates/core/Cargo.toml"}) {
		t.Fatalf("config files %v", paths)
	}
	if doc.Target.Properties["rustc"] == "1.0.0" || !slices.Contains(doc.Limitations, "rust_toolchain_file_ignored") || !RustcVersionTested(doc.Target.Properties["rustc"]) {
		t.Fatalf("toolchain %v limitations %v", doc.Target.Properties, doc.Limitations)
	}
	plan, err := CheckPlan(context.Background(), fx.Job)
	if err != nil || len(plan) != 1 || plan[0].Module != "." || !plan[0].RepositoryCode {
		t.Fatalf("plan %+v %v", plan, err)
	}
}

// TC-115: a package that is not a member of the root manifest is not part of
// the build; its source is reported as not compiled, never as clean.
func TestRealNonMemberCrateIsNotClaimed(t *testing.T) {
	res, doc, _, _, _ := realRun(t, KindCheck, "nonmember", "cargo-check", "check-nonmember", domain.Scope{}, nil)
	if res.Status != domain.TaskSucceeded || *doc.Coverage.FilesInScope != 2 || doc.Coverage.FilesUnlisted != 1 || !slices.Contains(doc.Limitations, "rust_files_not_compiled") ||
		!slices.Equal(doc.Coverage.ProcessedPaths, []string{"src/lib.rs"}) {
		t.Fatalf("status %s/%s coverage %+v limitations %v", res.Status, res.Reason, doc.Coverage, doc.Limitations)
	}
}

// A path scope keeps only the in-scope observations and processed files; the
// out-of-scope one is counted in a limitation.
func TestRealPathScope(t *testing.T) {
	res, doc, _, _, _ := realRun(t, KindCheck, "findings", "cargo-check", "", domain.Scope{Kind: "paths", Paths: []string{"src/lib.rs"}}, nil)
	if res.Status != domain.TaskSucceeded || *doc.Coverage.FilesInScope != 1 || !slices.Equal(doc.Coverage.ProcessedPaths, []string{"src/lib.rs"}) || !slices.Contains(doc.Limitations, "observations_outside_scope_omitted") {
		t.Fatalf("status %s/%s coverage %+v limitations %v", res.Status, res.Reason, doc.Coverage, doc.Limitations)
	}
	for _, o := range doc.Observations {
		if *o.Path != "src/lib.rs" {
			t.Fatalf("out of scope observation %+v", o)
		}
	}
	if !slices.Equal(doc.Patterns, []string{"src/lib.rs"}) {
		t.Fatalf("patterns %v", doc.Patterns)
	}
}

// TC-115: build scripts and proc macros run during check. The build-script
// record is the evidence, and the task still succeeds with its findings.
func TestRealBuildScriptAndProcMacro(t *testing.T) {
	requireLinker(t)
	res, doc, _, fx, _ := realRun(t, KindCheck, "buildscript", "cargo-check", "check-buildscript", domain.Scope{}, nil)
	if res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != obsdoc.ExitSuccessWithFindings {
		t.Fatalf("status %s/%s errors %+v", res.Status, res.Reason, doc.ToolErrors)
	}
	for _, l := range []string{"build_script_executed", "proc_macro_executed", "linker_from_toolchain_path"} {
		if !slices.Contains(doc.Limitations, l) {
			t.Fatalf("limitation %s missing: %v", l, doc.Limitations)
		}
	}
	if o := obsOf(doc, "unused_variables"); len(o) != 1 || *o[0].Path != "app/src/lib.rs" {
		t.Fatalf("observations %+v", doc.Observations)
	}
	// The build script wrote its marker into the scratch target, not the copy.
	var marker string
	_ = filepath.WalkDir(filepath.Join(fx.Job.Scratch, "analyzers", "cargo-check", "target"), func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Name() == "marker.txt" {
			marker = p
		}
		return nil
	})
	if marker == "" {
		t.Fatal("the build script did not run in the scratch target")
	}
}

// Without a linker in the operator's tool path, a crate with a build script
// cannot be linked: an explicit configuration reason, never a clean result.
func TestRealMissingLinker(t *testing.T) {
	cargo, _ := testToolchain(t)
	a, err := New(KindCheck, Config{Cargo: cargo})
	if err != nil {
		t.Fatal(err)
	}
	fx := newFixture(t, "", fixtureFiles(t, "buildscript"), domain.Scope{}, true)
	res := runTask(t, a, fx.Job, "cargo-check", &executor.Executor{})
	capture(t, "check-nolinker", fx.Job, "cargo-check", res, a)
	doc, _ := readEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskFailed || res.Reason != ReasonLinker || doc.Outcome.ExitClass != obsdoc.ExitConfigurationError || doc.Coverage.ProcessedPaths != nil {
		t.Fatalf("status %s/%s %+v", res.Status, res.Reason, doc.Outcome)
	}
}

func TestRealBuildScriptFailure(t *testing.T) {
	requireLinker(t)
	res, doc, _, _, _ := realRun(t, KindCheck, "buildfail", "cargo-check", "check-buildfail", domain.Scope{}, nil)
	if res.Status != domain.TaskFailed || res.Reason != ReasonBuildScript || doc.Outcome.ExitClass != obsdoc.ExitExecutionError || doc.Coverage.ProcessedPaths != nil {
		t.Fatalf("status %s/%s %+v", res.Status, res.Reason, doc.Outcome)
	}
}

// A build script may write into its working directory; the workspace copy
// must still equal the snapshot or the task fails (the observations would
// point into code the snapshot does not hold).
func TestRealWorkspaceDrift(t *testing.T) {
	requireLinker(t)
	res, doc, _, fx, _ := realRun(t, KindCheck, "drift", "cargo-check", "check-drift", domain.Scope{}, nil)
	if res.Status != domain.TaskFailed || res.Reason != ReasonWorkspaceDrift || !slices.Equal(doc.WorkspaceDrift, []string{"src/lib.rs"}) {
		t.Fatalf("status %s/%s drift %v", res.Status, res.Reason, doc.WorkspaceDrift)
	}
	if b, _ := os.ReadFile(filepath.Join(fx.SourceRoot, "src", "lib.rs")); !bytes.Contains(b, []byte("original")) {
		t.Fatal("the original source was modified")
	}
}

// TC-68-style configuration errors: a registry dependency with no offline
// source and a missing Cargo.lock fail with explicit reasons; cargo is offline
// and --locked, so nothing is downloaded or generated.
func TestRealConfigurationErrors(t *testing.T) {
	for _, tc := range []struct {
		fixture, capName, reason string
	}{{"nodeps", "check-nodeps", ReasonDependency}, {"missinglock", "check-missinglock", ReasonLockMissing}, {"lockoutdated", "check-lockoutdated", ReasonLockOutdated},
		{"manifestbad", "check-manifestbad", ReasonManifestInvalid}, {"toonew", "check-toonew", ReasonToolchain}} {
		t.Run(tc.fixture, func(t *testing.T) {
			res, doc, _, fx, a := realRun(t, KindCheck, tc.fixture, "cargo-check", tc.capName, domain.Scope{}, nil)
			// the capture is named after the case; the mutation of the lock file is checked below
			if res.Status != domain.TaskFailed || res.Reason != tc.reason || doc.Outcome.ExitClass != obsdoc.ExitConfigurationError || doc.Coverage.ProcessedPaths != nil {
				t.Fatalf("status %s/%s %+v", res.Status, res.Reason, doc.Outcome)
			}
			// Nothing was downloaded into the scratch Cargo home.
			var files []string
			_ = filepath.WalkDir(filepath.Join(fx.Job.Scratch, "analyzers", "cargo-check", "cargo-home"), func(p string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					files = append(files, p)
				}
				return nil
			})
			for _, f := range files {
				if strings.Contains(f, "registry") && strings.HasSuffix(f, ".crate") {
					t.Fatalf("a crate was downloaded: %s", f)
				}
			}
			if _, err := os.Stat(filepath.Join(fx.Job.Snapshot.WorkspaceDir, "Cargo.lock")); tc.fixture == "missinglock" && err == nil {
				t.Fatal("Cargo.lock was generated")
			}
			if b, err := os.ReadFile(filepath.Join(fx.Job.Snapshot.WorkspaceDir, "Cargo.lock")); tc.fixture == "lockoutdated" && (err != nil || !bytes.Contains(b, []byte(`version = "0.1.0"`))) {
				t.Fatal("Cargo.lock was updated")
			}
			_ = a
		})
	}
}

// Timeout and cancellation end the task with their own status and clean up.
func TestRealTimeoutAndCancel(t *testing.T) {
	cargo, _ := testToolchain(t)
	fx := newFixture(t, "", fixtureFiles(t, "clean"), domain.Scope{}, true)
	a, err := New(KindCheck, Config{Cargo: cargo, Timeout: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	res := runTask(t, a, fx.Job, "cargo-check", &executor.Executor{})
	doc, _ := readEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskTimedOut || res.Reason != ReasonTimeout || doc.Outcome.ExitClass != obsdoc.ExitTimeout || doc.Coverage.ProcessedPaths != nil {
		t.Fatalf("timeout: %s/%s %+v", res.Status, res.Reason, doc.Outcome)
	}
	fx2 := newFixture(t, "", fixtureFiles(t, "clean"), domain.Scope{}, true)
	a2 := newReal(t, KindCheck)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tasks, _ := a2.Plan(context.Background(), fx2.Job)
	res = a2.Run(ctx, fx2.Job, tasks[0].Spec, &executor.Executor{})
	doc, _ = readEnvelope(t, fx2.Job, res)
	if res.Status != domain.TaskCancelled || doc.Outcome.ExitClass != obsdoc.ExitCancelled {
		t.Fatalf("cancel: %s/%s %+v", res.Status, res.Reason, doc.Outcome)
	}
}

// A rustup proxy is refused: it would let the repository's rust-toolchain
// file choose the toolchain and may download one. The check is on the
// operator's real ~/.cargo/bin/cargo when it exists.
func TestRealRustupProxyIsRefused(t *testing.T) {
	testToolchain(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}
	proxy := filepath.Join(home, ".cargo", "bin", "cargo")
	if _, err := os.Stat(proxy); err != nil {
		t.Skip("no rustup proxy on this machine")
	}
	if _, err := New(KindCheck, Config{Cargo: proxy}); err == nil {
		t.Fatal("the rustup proxy was accepted")
	}
}
