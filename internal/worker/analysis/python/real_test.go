package python

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Real-tool tests (docs/ANALYZERS.md#importstub-공백). Each is skipped unless its tool is named
// by CODEYE_TEST_RUFF, CODEYE_TEST_MYPY, CODEYE_TEST_BANDIT or
// CODEYE_TEST_PYRIGHT (+ CODEYE_TEST_NODE). The fixture projects are in
// fixtures/python/. With CODEYE_PYTHON_CAPTURE=<dir> the tool streams are
// written there for the replay tests (see replay_test.go).

// loadFixture reads fixtures/python/<name> as a file map.
func loadFixture(t *testing.T, name string) map[string]string {
	t.Helper()
	root := filepath.Join("..", "..", "..", "..", "fixtures", "python", name)
	files := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		files[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil || len(files) == 0 {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return files
}

type realRun struct {
	res analysis.TaskResult
	doc obsdoc.Document
	raw []byte
	fx  fixture
	rr  *recordingRunner
}

// runReal runs one task of a on a fixture through the real executor and
// checks what every real run must hold: the source and snapshot are
// unchanged, no tool cache or bytecode is left in the workspace, and the
// envelope names no node path.
func runReal(t *testing.T, a planner, fixtureName, key string, extra ...string) realRun {
	t.Helper()
	r := runWith(t, a, fixtureName, key, &executor.Executor{}, extra...)
	for i, res := range r.rr.results {
		// docs/ANALYZERS.md#실행-규칙: no tool leaves a background child (daemon, worker
		// process, telemetry) behind; cleanup is confirmed for every run.
		if res.Cleanup != executor.CleanupConfirmed || res.LeftoverKilled {
			t.Fatalf("execution %d cleanup %s leftover %v", i, res.Cleanup, res.LeftoverKilled)
		}
	}
	capture(t, fixtureName, key, r)
	return r
}

// runWith is runReal through runner (the real executor or a replay).
func runWith(t *testing.T, a planner, fixtureName, key string, runner executor.Runner, extra ...string) realRun {
	t.Helper()
	files := loadFixture(t, fixtureName)
	fx := newFixture(t, files, domain.Scope{})
	before, snapBefore := packtest.TreeHash(t, fx.SourceRoot), packtest.TreeHash(t, fx.Job.Snapshot.SnapshotDir)
	rr := &recordingRunner{inner: runner}
	res := packtest.RunTask(t, a, fx.Job, key, rr)
	doc, raw := readEnvelope(t, fx.Job, res)
	if packtest.TreeHash(t, fx.SourceRoot) != before || packtest.TreeHash(t, fx.Job.Snapshot.SnapshotDir) != snapBefore {
		t.Fatal("source or snapshot changed")
	}
	_ = filepath.WalkDir(fx.Job.Snapshot.WorkspaceDir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && slices.Contains([]string{".ruff_cache", ".mypy_cache", "__pycache__", "PLUGIN_EXECUTED"}, d.Name()) {
			t.Errorf("workspace has %s", p)
		}
		return nil
	})
	for _, s := range rr.specs {
		if s.Dir != filepath.Join(fx.Job.Snapshot.WorkspaceDir, filepath.FromSlash(doc.Module.Dir)) || s.Timeout <= 0 || s.Env["PATH"] == "" || len(s.Env) > 6 {
			t.Fatalf("spec %+v", s)
		}
		for _, arg := range s.Args {
			if strings.Contains(arg, fx.SourceRoot) {
				t.Fatalf("argv names the source root: %v", s.Args)
			}
		}
	}
	paths := append([]string{fx.SourceRoot, fx.Job.Snapshot.WorkspaceDir, fx.Job.Scratch, fx.Job.Snapshot.Root, os.Getenv("HOME")}, extra...)
	packtest.AssertNoNodePaths(t, raw, paths...)
	return realRun{res: res, doc: doc, raw: raw, fx: fx, rr: rr}
}

func wantStatus(t *testing.T, r realRun, status domain.TaskStatus, class string) {
	t.Helper()
	if r.res.Status != status || r.doc.Outcome.ExitClass != class {
		t.Fatalf("outcome %s/%s %+v tool errors %+v\n%s", r.res.Status, r.res.Reason, r.doc.Outcome, r.doc.ToolErrors, r.raw)
	}
}

func wantProcessed(t *testing.T, d obsdoc.Document, want ...string) {
	t.Helper()
	if !slices.Equal(d.Coverage.ProcessedPaths, want) {
		t.Fatalf("processed %v, want %v (coverage %+v)", d.Coverage.ProcessedPaths, want, d.Coverage)
	}
}

func wantTarget(t *testing.T, d obsdoc.Document, version, source string) {
	t.Helper()
	if d.Target.Properties["python_version"] != version || d.Target.Properties["python_version_source"] != source {
		t.Fatalf("target %+v", d.Target.Properties)
	}
}

// findObs returns the active observations of rule.
func findObs(d obsdoc.Document, rule string) []obsdoc.Observation {
	var out []obsdoc.Observation
	for _, o := range d.Observations {
		if o.RuleID == rule && !o.Suppressed {
			out = append(out, o)
		}
	}
	return out
}

// snapshotLine is line n (1-based) of a fixture file.
func snapshotLine(t *testing.T, fx fixture, rel string, n int) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fx.Job.Snapshot.SnapshotDir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(string(b), "\n")[n-1]
}

// ---- ruff ----

// TC-108: ruff on the findings project. Exit 1 with findings is
// success_with_findings; the CODEYE rule pack is applied in isolation (no S
// rules: bandit owns security), the target version comes from
// requires-python, rune columns become byte columns, every planned file is
// processed (the .pyi stub included) and nothing is cached in the workspace.
func TestRealRuffFindings(t *testing.T) {
	checkRuffFindings(t, runReal(t, newRuffT(t), "findings", "ruff"))
}

func checkRuffFindings(t *testing.T, r realRun) {
	t.Helper()
	wantStatus(t, r, domain.TaskSucceeded, obsdoc.ExitSuccessWithFindings)
	wantProcessed(t, r.doc, "app/__init__.py", "app/core.py", "app/stubs.pyi")
	wantTarget(t, r.doc, "3.11", SourceRequiresPython)
	got := rulesOf(r.doc, false)
	for _, rule := range []string{"F401", "E711", "E722", "B006", "F821"} {
		if got[rule] != 1 {
			t.Fatalf("rule %s: %v", rule, got)
		}
	}
	for rule := range got {
		if strings.HasPrefix(rule, "S") {
			t.Fatalf("ruff S rule ran: %v", got)
		}
	}
	o := findObs(r.doc, "F821")[0]
	line := snapshotLine(t, r.fx, "app/core.py", o.Location.StartLine)
	if o.Location.ColumnUnit != "byte" || !strings.HasPrefix(line[o.Location.StartCol-1:], "undefined_suffix") || line[o.Location.StartCol-1:o.Location.EndCol-1] != "undefined_suffix" {
		t.Fatalf("F821 location %+v on %q", o.Location, line)
	}
	rp := r.doc.RulePack
	if rp.RepositoryConfig != obsdoc.RepoConfigOverridden || rp.SourceSuppressions != obsdoc.SuppressReported || len(rp.UnreportedSuppressionRules) != 0 {
		t.Fatalf("rule pack %+v", rp)
	}
	var names []string
	for _, e := range r.doc.Executions {
		names = append(names, e.Name)
	}
	if !slices.Equal(names, []string{"ruff.version", "ruff.files", "ruff.check", "ruff.check-ignore-noqa"}) {
		t.Fatalf("executions %v", names)
	}
	check := r.rr.specs[2]
	if !slices.Contains(check.Args, "--isolated") || !slices.Contains(check.Args, "--no-cache") || !slices.Contains(check.Args, "--target-version") ||
		check.Env["RUFF_CACHE_DIR"] == "" || !strings.HasPrefix(check.Env["RUFF_CACHE_DIR"], r.fx.Job.Scratch) {
		t.Fatalf("check spec %v %v", check.Args, check.Env)
	}
	if r.doc.Tool.Version == nil || !r.doc.Tool.VersionTested {
		t.Fatalf("tool %+v", r.doc.Tool)
	}
}

func TestRealRuffClean(t *testing.T) { checkRuffClean(t, runReal(t, newRuffT(t), "clean", "ruff")) }

func checkRuffClean(t *testing.T, r realRun) {
	t.Helper()
	wantStatus(t, r, domain.TaskSucceeded, obsdoc.ExitSuccessWithoutFindings)
	wantProcessed(t, r.doc, "pkg/__init__.py", "pkg/util.py")
	wantTarget(t, r.doc, "3.12", SourceVersionFile)
	if len(r.doc.Observations) != 0 || r.doc.Coverage.Units[UnitAnalyzedClean] != 2 {
		t.Fatalf("clean %+v %+v", r.doc.Observations, r.doc.Coverage)
	}
}

// TC-109: every ruff suppression form of the fixture (# noqa: X, bare
// # noqa, # ruff: noqa: X) is reported through the --ignore-noqa run.
func TestRealRuffSuppressionsReported(t *testing.T) {
	checkRuffSuppressionsReported(t, runReal(t, newRuffT(t), "suppressions", "ruff"))
}

func checkRuffSuppressionsReported(t *testing.T, r realRun) {
	t.Helper()
	wantStatus(t, r, domain.TaskSucceeded, obsdoc.ExitSuccessWithFindings)
	if active, hidden := rulesOf(r.doc, false), rulesOf(r.doc, true); active["F401"] != 1 || hidden["F401"] != 3 || len(active) != 1 || len(hidden) != 1 {
		t.Fatalf("active %v suppressed %v", active, hidden)
	}
	if !hasLimit(r.doc, LimSuppressed) || r.doc.RulePack.SourceSuppressions != obsdoc.SuppressReported {
		t.Fatalf("limitations %v rule pack %+v", r.doc.Limitations, r.doc.RulePack)
	}
	for _, o := range r.doc.Observations {
		if o.Suppressed && !slices.Contains([]string{"sup/ruff_line.py", "sup/ruff_file.py"}, *o.Path) {
			t.Fatalf("suppressed %+v", o)
		}
	}
}

// Repository ruff configuration is overridden: ruff.toml selects only E501,
// yet the CODEYE selection reports F401.
func TestRealRuffRepositoryConfigOverridden(t *testing.T) {
	r := runReal(t, newRuffT(t), "config", "ruff")
	wantStatus(t, r, domain.TaskSucceeded, obsdoc.ExitSuccessWithFindings)
	if got := rulesOf(r.doc, false); got["F401"] != 1 || got["E501"] != 0 {
		t.Fatalf("rules %v", got)
	}
}

// A syntax error is a tool error of that file, never an observation, and
// the task fails: the file was not analyzed.
func TestRealRuffSyntaxError(t *testing.T) {
	checkRuffSyntaxError(t, runReal(t, newRuffT(t), "syntax", "ruff"))
}

// One file that does not parse leaves the rest analyzed: the task succeeds
// with the file as a syntax_error unit and its syntax error as observation.
func checkRuffSyntaxError(t *testing.T, r realRun) {
	t.Helper()
	wantStatus(t, r, domain.TaskSucceeded, obsdoc.ExitSuccessWithFindings)
	checkUnparsable(t, r, "invalid-syntax", []string{"ok.py"}, "broken.py")
}

// TC-191: two unparsable files (a syntax error and Python 2 print) next to a
// healthy file with a real finding: the finding stands, each broken file is
// one syntax observation and a syntax_error unit.
func TestRealRuffUnparsableFiles(t *testing.T) {
	checkRuffUnparsable(t, runReal(t, newRuffT(t), "syntaxmix", "ruff"))
}

func checkRuffUnparsable(t *testing.T, r realRun) {
	t.Helper()
	checkUnparsable(t, r, "invalid-syntax", []string{"app.py"}, "broken.py", "legacy.py")
	if got := findObs(r.doc, "E722"); len(got) != 1 || *got[0].Path != "app.py" {
		t.Fatalf("E722 on app.py: %+v", got)
	}
}

// Nested modules are separate tasks with their own target version; skipped
// directories (venv, hidden) are never analyzed.
func TestRealRuffNestedModules(t *testing.T) {
	a := newRuffT(t)
	r := runReal(t, a, "nested", "ruff")
	wantStatus(t, r, domain.TaskSucceeded, obsdoc.ExitSuccessWithFindings)
	wantProcessed(t, r.doc, "root_mod.py")
	wantTarget(t, r.doc, "3.10", SourceRequiresPython)
	sub := runReal(t, a, "nested", "ruff.m01")
	wantStatus(t, sub, domain.TaskSucceeded, obsdoc.ExitSuccessWithFindings)
	wantProcessed(t, sub.doc, "sub/pkg/__init__.py", "sub/pkg/m.py")
	wantTarget(t, sub.doc, "3.13", SourceRequiresPython)
	if sub.doc.Module.Dir != "sub" || findObs(sub.doc, "F401")[0].Unit != "sub/pkg/m.py" {
		t.Fatalf("sub module %+v %+v", sub.doc.Module, sub.doc.Observations)
	}
}

// docs/ANALYZERS.md#python-엔진-중복: the overlap fixture's constructs are ruff findings; the
// type checkers' same checks are disabled (see the mypy and pyright tests).
func TestRealRuffOverlap(t *testing.T) {
	checkRuffOverlap(t, runReal(t, newRuffT(t), "overlap", "ruff"))
}

func checkRuffOverlap(t *testing.T, r realRun) {
	t.Helper()
	got := rulesOf(r.doc, false)
	for _, rule := range []string{"F821", "F811", "F524", "F631", "B015", "PLE0604"} {
		if got[rule] == 0 {
			t.Fatalf("rule %s: %v", rule, got)
		}
	}
}

// ---- mypy ----

func TestRealMypyFindings(t *testing.T) {
	checkMypyFindings(t, runReal(t, newMypyT(t), "findings", "mypy"))
}

func checkMypyFindings(t *testing.T, r realRun) {
	t.Helper()
	wantStatus(t, r, domain.TaskSucceeded, obsdoc.ExitSuccessWithFindings)
	wantProcessed(t, r.doc, "app/__init__.py", "app/core.py", "app/stubs.pyi")
	wantTarget(t, r.doc, "3.11", SourceRequiresPython)
	got := rulesOf(r.doc, false)
	if got["return-value"] != 1 || got["name-defined"] != 0 {
		t.Fatalf("rules %v", got)
	}
	o := findObs(r.doc, "return-value")[0]
	if line := snapshotLine(t, r.fx, "app/core.py", o.Location.StartLine); !strings.HasPrefix(line[o.Location.StartCol-1:], "x") {
		t.Fatalf("location %+v on %q", o.Location, line)
	}
	rp := r.doc.RulePack
	if rp.RepositoryConfig != obsdoc.RepoConfigOverridden || rp.SourceSuppressions != obsdoc.SuppressHonored || !slices.Contains(rp.Selection, "--config-file=") {
		t.Fatalf("rule pack %+v", rp)
	}
	if r.res.Status == domain.TaskSucceeded && !hasLimit(r.doc, LimNoThirdParty) {
		t.Fatalf("limitations %v", r.doc.Limitations)
	}
}

// TC-110: unresolved imports and missing stubs are coverage units and
// limitations, never observations; mypy reports no follow-on error for them.
func TestRealMypyImportsAreCoverageGaps(t *testing.T) {
	checkMypyImportsAreCoverageGaps(t, runReal(t, newMypyT(t), "imports", "mypy"))
}

func checkMypyImportsAreCoverageGaps(t *testing.T, r realRun) {
	t.Helper()
	wantStatus(t, r, domain.TaskSucceeded, obsdoc.ExitSuccessWithoutFindings)
	u := r.doc.Coverage.Units
	if u[UnitImportUnresolved] != 1 || u[UnitStubMissing] != 2 || len(r.doc.Observations) != 0 || !hasLimit(r.doc, LimImportsUnresolved) || !hasLimit(r.doc, LimStubsMissing) {
		t.Fatalf("units %v observations %+v limitations %v", u, r.doc.Observations, r.doc.Limitations)
	}
	wantTarget(t, r.doc, "3.12", SourceRequiresPython)
}

// The repository's mypy configuration (mypy.ini, [tool.mypy]) declares a
// path plugin and disables return-value. mypy runs with the CODEYE
// configuration only: the plugin never runs and the error is reported.
func TestRealMypyRepositoryConfigAndPluginNotExecuted(t *testing.T) {
	r := runReal(t, newMypyT(t), "config", "mypy")
	if got := rulesOf(r.doc, false); got["return-value"] != 1 {
		t.Fatalf("rules %v (%s)", got, r.raw)
	}
	for _, root := range []string{r.fx.SourceRoot, r.fx.Job.Snapshot.WorkspaceDir, r.fx.Job.Scratch} {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.Name() == "PLUGIN_EXECUTED" {
				t.Fatalf("plugin executed: %s", p)
			}
			return nil
		})
	}
	for _, s := range r.rr.specs {
		if len(s.Args) > 1 && !slices.Contains(s.Args, "--config-file=") {
			t.Fatalf("mypy without the CODEYE configuration: %v", s.Args)
		}
	}
}

// mypy suppressions are honored: # type: ignore hides its error without a
// record, the pyright-only comment does not.
func TestRealMypySuppressionsHonored(t *testing.T) {
	checkMypySuppressionsHonored(t, runReal(t, newMypyT(t), "suppressions", "mypy"))
}

func checkMypySuppressionsHonored(t *testing.T, r realRun) {
	t.Helper()
	if got := rulesOf(r.doc, false); got["return-value"] != 2 || len(rulesOf(r.doc, true)) != 0 {
		t.Fatalf("rules %v", got)
	}
	if r.doc.RulePack.SourceSuppressions != obsdoc.SuppressHonored {
		t.Fatalf("rule pack %+v", r.doc.RulePack)
	}
}

func TestRealMypySyntaxError(t *testing.T) {
	checkMypySyntaxError(t, runReal(t, newMypyT(t), "syntax", "mypy"))
}

// mypy stops at the first file it cannot parse; the pack runs it again
// without that file, so the others are type-checked.
func checkMypySyntaxError(t *testing.T, r realRun) {
	t.Helper()
	checkUnparsable(t, r, "syntax", []string{"ok.py"}, "broken.py")
	wantExecutions(t, r.doc, "mypy.version", "mypy.check", "mypy.check.r01")
}

// TC-191 for mypy: one rerun per unparsable file, then the type check of the
// rest with its real finding.
func TestRealMypyUnparsableFiles(t *testing.T) {
	checkMypyUnparsable(t, runReal(t, newMypyT(t), "syntaxmix", "mypy"))
}

func checkMypyUnparsable(t *testing.T, r realRun) {
	t.Helper()
	checkUnparsable(t, r, "syntax", []string{"app.py"}, "broken.py", "legacy.py")
	wantExecutions(t, r.doc, "mypy.version", "mypy.check", "mypy.check.r01", "mypy.check.r02")
	if got := findObs(r.doc, "return-value"); len(got) != 1 || *got[0].Path != "app.py" {
		t.Fatalf("return-value on app.py: %+v", got)
	}
}

func wantExecutions(t *testing.T, d obsdoc.Document, names ...string) {
	t.Helper()
	var got []string
	for _, e := range d.Executions {
		got = append(got, e.Name)
	}
	if !slices.Equal(got, names) {
		t.Fatalf("executions %v, want %v", got, names)
	}
}

func TestRealMypyOverlapDisabled(t *testing.T) {
	checkMypyOverlapDisabled(t, runReal(t, newMypyT(t), "overlap", "mypy"))
}

func checkMypyOverlapDisabled(t *testing.T, r realRun) {
	t.Helper()
	got := rulesOf(r.doc, false)
	for _, code := range []string{"name-defined", "no-redef", "str-format", "await-not-async"} {
		if got[code] != 0 {
			t.Fatalf("overlap code %s reported: %v", code, got)
		}
	}
}

// ---- pyright ----

func TestRealPyrightFindings(t *testing.T) {
	a := newPyrightT(t)
	checkPyrightFindings(t, a, runReal(t, a, "findings", "pyright", a.cfg.Node.Dir, filepath.Dir(a.resolved)))
}

func checkPyrightFindings(t *testing.T, a *Pyright, r realRun) {
	t.Helper()
	wantStatus(t, r, domain.TaskSucceeded, obsdoc.ExitSuccessWithFindings)
	wantProcessed(t, r.doc, "app/__init__.py", "app/core.py", "app/stubs.pyi")
	wantTarget(t, r.doc, "3.11", SourceRequiresPython)
	got := rulesOf(r.doc, false)
	if got["reportReturnType"] != 1 || got["reportUndefinedVariable"] != 0 {
		t.Fatalf("rules %v", got)
	}
	for _, s := range r.rr.specs {
		if s.Executable != a.cfg.Node.Executable || s.Args[0] != a.resolved || s.Env["PATH"] != a.cfg.Node.Dir {
			t.Fatalf("pyright spec %s %v %v", s.Executable, s.Args, s.Env)
		}
	}
	rp := r.doc.RulePack
	if rp.RepositoryConfig != obsdoc.RepoConfigOverridden || rp.SourceSuppressions != obsdoc.SuppressHonored {
		t.Fatalf("rule pack %+v", rp)
	}
}

func TestRealPyrightImportsAreCoverageGaps(t *testing.T) {
	a := newPyrightT(t)
	checkPyrightImportsAreCoverageGaps(t, a, runReal(t, a, "imports", "pyright", a.cfg.Node.Dir))
}

func checkPyrightImportsAreCoverageGaps(t *testing.T, a *Pyright, r realRun) {
	t.Helper()
	wantStatus(t, r, domain.TaskSucceeded, obsdoc.ExitSuccessWithoutFindings)
	u := r.doc.Coverage.Units
	if u[UnitImportUnresolved] != 1 || u[UnitImportSourceMissing] != 2 || len(r.doc.Observations) != 0 {
		t.Fatalf("units %v observations %+v", u, r.doc.Observations)
	}
}

// pyrightconfig.json and [tool.pyright] turn checking off; the CODEYE
// configuration in scratch replaces both.
func TestRealPyrightRepositoryConfigOverridden(t *testing.T) {
	a := newPyrightT(t)
	r := runReal(t, a, "config", "pyright", a.cfg.Node.Dir)
	if got := rulesOf(r.doc, false); got["reportReturnType"] != 1 {
		t.Fatalf("rules %v", got)
	}
}

// Without a configured environment pyright runs python/python3 from PATH to
// find the search paths. The node directory of this test holds logging fake
// interpreters next to node; the CODEYE configuration (an empty scratch
// venv) must keep pyright from running any of them.
func TestRealPyrightExecutesNoInterpreter(t *testing.T) {
	exe := testTool(t, "CODEYE_TEST_PYRIGHT")
	node := testNode(t)
	dir := t.TempDir()
	logFile := filepath.Join(t.TempDir(), "python-called")
	if err := os.Symlink(node.Executable, filepath.Join(dir, "node")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"python", "python3"} {
		script := "#!/bin/sh\necho \"$0 $*\" >> " + logFile + "\nexit 1\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	a, err := NewPyright(Config{Executable: exe, Node: &pack.Toolchain{Name: "node", Executable: filepath.Join(dir, "node"), Dir: dir}})
	if err != nil {
		t.Fatal(err)
	}
	r := runReal(t, a, "findings", "pyright", dir)
	wantStatus(t, r, domain.TaskSucceeded, obsdoc.ExitSuccessWithFindings)
	if b, err := os.ReadFile(logFile); err == nil {
		t.Fatalf("pyright ran an interpreter: %s", b)
	}
}

func TestRealPyrightSuppressionsHonored(t *testing.T) {
	a := newPyrightT(t)
	checkPyrightSuppressionsHonored(t, a, runReal(t, a, "suppressions", "pyright", a.cfg.Node.Dir))
}

func checkPyrightSuppressionsHonored(t *testing.T, a *Pyright, r realRun) {
	t.Helper()
	if got := rulesOf(r.doc, false); got["reportReturnType"] != 1 || len(rulesOf(r.doc, true)) != 0 {
		t.Fatalf("rules %v", got)
	}
}

func TestRealPyrightSyntaxError(t *testing.T) {
	a := newPyrightT(t)
	checkPyrightSyntaxError(t, a, runReal(t, a, "syntax", "pyright", a.cfg.Node.Dir))
}

func checkPyrightSyntaxError(t *testing.T, a *Pyright, r realRun) {
	t.Helper()
	checkUnparsable(t, r, PyrightSyntaxRule, []string{"ok.py"}, "broken.py")
}

// TC-191 for pyright: it checks every file in one run; the parser errors of
// each broken file become one observation, the rest stands.
func TestRealPyrightUnparsableFiles(t *testing.T) {
	a := newPyrightT(t)
	checkPyrightUnparsable(t, a, runReal(t, a, "syntaxmix", "pyright", a.cfg.Node.Dir))
}

func checkPyrightUnparsable(t *testing.T, a *Pyright, r realRun) {
	t.Helper()
	checkUnparsable(t, r, PyrightSyntaxRule, []string{"app.py"}, "broken.py", "legacy.py")
	if got := findObs(r.doc, "reportReturnType"); len(got) != 1 || *got[0].Path != "app.py" {
		t.Fatalf("reportReturnType on app.py: %+v", got)
	}
}

func TestRealPyrightOverlapDisabled(t *testing.T) {
	a := newPyrightT(t)
	checkPyrightOverlapDisabled(t, a, runReal(t, a, "overlap", "pyright", a.cfg.Node.Dir))
}

func checkPyrightOverlapDisabled(t *testing.T, a *Pyright, r realRun) {
	t.Helper()
	got := rulesOf(r.doc, false)
	for _, rule := range []string{"reportUndefinedVariable", "reportRedeclaration", "reportAssertAlwaysTrue", "reportUnusedExpression", "reportUnsupportedDunderAll"} {
		if got[rule] != 0 {
			t.Fatalf("overlap rule %s reported: %v", rule, got)
		}
	}
}

// ---- bandit ----

func TestRealBanditFindings(t *testing.T) {
	checkBanditFindings(t, runReal(t, newBanditT(t), "findings", "bandit"))
}

func checkBanditFindings(t *testing.T, r realRun) {
	t.Helper()
	wantStatus(t, r, domain.TaskSucceeded, obsdoc.ExitSuccessWithFindings)
	// bandit reads .py only: the stub is not in its scope.
	wantProcessed(t, r.doc, "app/__init__.py", "app/core.py")
	if *r.doc.Coverage.FilesInScope != 2 {
		t.Fatalf("files in scope %d", *r.doc.Coverage.FilesInScope)
	}
	got := rulesOf(r.doc, false)
	if got["B602"] != 1 || got["B404"] != 1 {
		t.Fatalf("rules %v", got)
	}
	for _, skipped := range []string{"B101", "B105", "B106", "B107", "B613"} {
		if got[skipped] != 0 {
			t.Fatalf("skipped test %s ran", skipped)
		}
	}
	for _, o := range r.doc.Observations {
		if o.Category != "security" || o.RawSeverity == nil {
			t.Fatalf("observation %+v", o)
		}
	}
	o := findObs(r.doc, "B602")[0]
	if line := snapshotLine(t, r.fx, "app/core.py", o.Location.StartLine); !strings.HasPrefix(line[o.Location.StartCol-1:], "subprocess.call") {
		t.Fatalf("location %+v on %q", o.Location, line)
	}
	if r.doc.Target.Properties["python_version_applied"] != "false" || r.doc.Target.Properties["parser_python_version"] == "" {
		t.Fatalf("target %+v", r.doc.Target.Properties)
	}
}

// TC-109: # nosec with a test id is reported through --ignore-nosec.
func TestRealBanditSuppressionsReported(t *testing.T) {
	checkBanditSuppressionsReported(t, runReal(t, newBanditT(t), "suppressions", "bandit"))
}

func checkBanditSuppressionsReported(t *testing.T, r realRun) {
	t.Helper()
	active, hidden := rulesOf(r.doc, false), rulesOf(r.doc, true)
	if active["B602"] != 1 || hidden["B602"] != 1 || hidden["B404"] != 1 || active["B404"] != 0 {
		t.Fatalf("active %v suppressed %v", active, hidden)
	}
	if r.doc.RulePack.SourceSuppressions != obsdoc.SuppressReported {
		t.Fatalf("rule pack %+v", r.doc.RulePack)
	}
}

// .bandit in the target directory skips B602 and B404 when bandit reads
// it; the CODEYE --ini file replaces it.
func TestRealBanditRepositoryConfigOverridden(t *testing.T) {
	r := runReal(t, newBanditT(t), "config", "bandit")
	if got := rulesOf(r.doc, false); got["B602"] != 1 || got["B404"] != 1 {
		t.Fatalf("rules %v", got)
	}
}

func TestRealBanditSyntaxError(t *testing.T) {
	checkBanditSyntaxError(t, runReal(t, newBanditT(t), "syntax", "bandit"))
}

// bandit skips a file it cannot parse and scans the rest; the error has no
// position, so the file is counted, not observed.
func checkBanditSyntaxError(t *testing.T, r realRun) {
	t.Helper()
	checkUnparsable(t, r, "", []string{"ok.py"}, "broken.py")
}

// TC-191 for bandit.
func TestRealBanditUnparsableFiles(t *testing.T) {
	checkBanditUnparsable(t, runReal(t, newBanditT(t), "syntaxmix", "bandit"))
}

func checkBanditUnparsable(t *testing.T, r realRun) {
	t.Helper()
	checkUnparsable(t, r, "", []string{"app.py"}, "broken.py", "legacy.py")
	if got := findObs(r.doc, "B602"); len(got) != 1 || *got[0].Path != "app.py" {
		t.Fatalf("B602 on app.py: %+v", got)
	}
}
