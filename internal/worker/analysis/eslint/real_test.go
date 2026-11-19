package eslint

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts/jststest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Real ESLint tests run only with CODEYE_TEST_ESLINT and CODEYE_TEST_NODE
// (CI has no Node tools). CODEYE_W18_CAPTURE=DIR also writes the redacted
// eslint.check streams and exit code of each case to DIR, which is how
// testdata/*.stdout|stderr|exit were captured (docs/ANALYZERS.md#실제-도구-시험과-출력-캡처):
//
//	CODEYE_W18_CAPTURE=$PWD/internal/worker/analysis/eslint/testdata \
//	  go test -count=1 -run TestRealESLint ./internal/worker/analysis/eslint/

// Fixture sources stay strings (or under fixtures/w18-js-ts) so the
// repository's own lint never sees the intentionally defective code.
const (
	pkgJSON   = "{\"name\":\"w18-case\",\"private\":true}\n"
	fixClean  = "export function add(a, b) {\n  return a + b;\n}\n"
	fixBroken = "export function broken( {\n  return 1;\n}\n"
	// suppressions (docs/ANALYZERS.md#규칙-문맥과-소스-안-억제): an inline rule-off comment, a disable
	// line, a disable block and a bulk suppressions file in the module. The
	// managed run turns all of them off, so every problem is reported.
	fixSup     = "/* eslint no-unused-vars: off */\nexport function run(f) {\n  // eslint-disable-next-line no-empty\n  try { f(); } catch {}\n  const hidden = 1;\n  /* eslint-disable */\n  debugger;\n  /* eslint-enable */\n}\n"
	fixSupFile = "{\"index.js\":{\"no-debugger\":{\"count\":1}}}\n"
	// a TS module whose import cannot resolve (no node_modules in a snapshot).
	fixGapTSConfig = "{\"compilerOptions\":{\"target\":\"ES2022\",\"module\":\"ESNext\",\"moduleResolution\":\"Bundler\",\"lib\":[\"ES2022\"],\"strict\":true},\"include\":[\"src\"]}\n"
	fixGapMain     = "import { z } from \"zod\";\n\nexport const schema = z.object({ name: z.string() });\n\nexport function lengths(items: string[]): number[] {\n  return items.map((x) => x.length);\n}\n\nexport async function later(): Promise<number> {\n  return 1;\n}\n"
)

func caseFiles(extra map[string]string) map[string]string {
	out := map[string]string{"package.json": pkgJSON}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// TC-111: the baseline lints every package.json module of the monorepo and
// the root sources outside them, the type-aware task lints the TS module's
// tsconfig program; no repository configuration program runs, the source
// and snapshot are unchanged and no node path is recorded.
func TestRealESLintMonorepo(t *testing.T) {
	base, typed := newReal(t, false), newReal(t, true)
	fx := jststest.NewFixture(t, filepath.Join(t.TempDir(), "공백 & (dir)"), jststest.MonorepoFiles(t, 4), domain.Scope{})
	before, snapBefore := jststest.TreeHash(t, fx.SourceRoot), jststest.TreeHash(t, fx.Job.Snapshot.SnapshotDir)
	tasks, err := base.Plan(context.Background(), fx.Job)
	if err != nil || len(tasks) != 3 {
		t.Fatalf("baseline plan %v %+v", err, tasks)
	}
	cases := []struct {
		a       *Analyzer
		key     string
		capture string
		rules   []string
		absent  []string
		paths   []string
	}{
		{base, "eslint", "monorepo-root", []string{"no-unused-vars", "no-empty"}, nil, []string{"scripts/release.mjs"}},
		{base, "eslint.m01", "monorepo-app", []string{"@typescript-eslint/no-explicit-any", "@typescript-eslint/no-unused-vars", "prefer-const"},
			[]string{"no-undef", "no-unused-vars"}, []string{"packages/app/src/index.ts", "packages/app/src/suppressed.ts", "packages/app/src/util.ts", "packages/app/tools/gen.ts"}},
		{base, "eslint.m02", "monorepo-lib", []string{"no-unused-vars", "no-cond-assign", "no-dupe-keys"}, nil, []string{"packages/lib/eslint.config.js", "packages/lib/index.js"}},
		{typed, "eslint-typed.m01", "monorepo-typed", []string{"@typescript-eslint/no-floating-promises", "@typescript-eslint/no-misused-promises",
			"@typescript-eslint/no-unsafe-member-access", "@typescript-eslint/no-unsafe-return", "@typescript-eslint/require-await"},
			[]string{"@typescript-eslint/no-explicit-any", "@typescript-eslint/no-unused-vars", "prefer-const"}, []string{"packages/app/src/index.ts", "packages/app/src/suppressed.ts", "packages/app/src/util.ts"}},
	}
	for _, c := range cases {
		res := jststest.RunTask(t, c.a, fx.Job, c.key, &executor.Executor{})
		jststest.Capture(t, c.capture, res)
		doc, raw := jststest.ReadEnvelope(t, fx.Job, res)
		if res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != obsdoc.ExitSuccessWithFindings || doc.Outcome.PartialParse {
			t.Fatalf("%s: %s/%s %+v errors %+v", c.key, res.Status, res.Reason, doc.Outcome, doc.ToolErrors)
		}
		check := res.Executions[len(res.Executions)-1]
		if check.Name != execCheck || check.Result.ExitCode == nil || *check.Result.ExitCode != 1 || len(check.Result.Stderr.Data) != 0 {
			t.Fatalf("%s: eslint must exit 1 with findings and a clean stderr: %+v", c.key, check.Result)
		}
		rules := jststest.Rules(doc)
		for _, r := range c.rules {
			if rules[r] == 0 {
				t.Fatalf("%s: rule %s missing: %v", c.key, r, rules)
			}
		}
		for _, r := range c.absent {
			if rules[r] != 0 {
				t.Fatalf("%s: rule %s must not run here: %v", c.key, r, rules)
			}
		}
		if !slices.Equal(doc.Coverage.ProcessedPaths, c.paths) {
			t.Fatalf("%s: processed %v", c.key, doc.Coverage.ProcessedPaths)
		}
		for _, o := range doc.Observations {
			if o.LocationState != obsdoc.LocationResolved || o.Location.ColumnUnit != "byte" || o.Suppressed || o.RawRef.Stream != "stdout" || o.RawRef.JSONPointer == "" {
				t.Fatalf("%s: observation %+v", c.key, o)
			}
		}
		if doc.RulePack.SourceSuppressions != obsdoc.SuppressDisabled || len(doc.RulePack.UnreportedSuppressionRules) != 0 || doc.Tool.Version == nil || !doc.Tool.VersionTested {
			t.Fatalf("%s: rule pack %+v tool %+v", c.key, doc.RulePack, doc.Tool)
		}
		if c.a.typed {
			if doc.RulePack.RepositoryConfig != obsdoc.RepoConfigHonored || len(doc.RulePack.ConfigFiles) != 2 || doc.Coverage.Units[FileOutsideTSConfig] != 1 ||
				!slices.Contains(doc.Limitations, "files_outside_tsconfig_program") {
				t.Fatalf("%s: typed rule pack %+v coverage %+v", c.key, doc.RulePack, doc.Coverage)
			}
		} else if doc.RulePack.RepositoryConfig != obsdoc.RepoConfigOverridden || len(doc.RulePack.ConfigFiles) != 0 {
			t.Fatalf("%s: rule pack %+v", c.key, doc.RulePack)
		}
		if c.key == "eslint" && !slices.Contains(doc.Limitations, "no_package_json_module") && doc.Module.Name != "w18-fixture" {
			t.Fatalf("root module %+v %v", doc.Module, doc.Limitations)
		}
		jststest.AssertNoNodePaths(t, raw, nodePaths(c.a, fx)...)
	}
	if _, err := os.Stat(filepath.Join(fx.Job.Snapshot.WorkspaceDir, "packages", "lib", "CONFIG_LOADED")); err == nil {
		t.Fatal("the repository eslint.config.js was executed")
	}
	if jststest.TreeHash(t, fx.SourceRoot) != before || jststest.TreeHash(t, fx.Job.Snapshot.SnapshotDir) != snapBefore {
		t.Fatal("source or snapshot changed")
	}
	// Same input, fresh attempt: IDs are content derived.
	fx2 := jststest.NewFixture(t, "", jststest.MonorepoFiles(t, 4), domain.Scope{})
	d1, _ := jststest.ReadEnvelope(t, fx.Job, analysis.TaskResult{Artifacts: []analysis.ArtifactFile{{Role: analysis.RoleObservations, MediaType: analysis.MediaObservations,
		Path: obsdoc.ArtifactPath(fx.Job.Scratch, "eslint.m02")}}})
	d2, _ := jststest.ReadEnvelope(t, fx2.Job, jststest.RunTask(t, base, fx2.Job, "eslint.m02", &executor.Executor{}))
	if len(d1.Observations) != len(d2.Observations) {
		t.Fatal("observation count differs between runs")
	}
	for i := range d1.Observations {
		if d1.Observations[i].ID != d2.Observations[i].ID {
			t.Fatalf("observation ids differ between runs: %s %s", d1.Observations[i].ID, d2.Observations[i].ID)
		}
	}
}

func TestRealESLintClean(t *testing.T) {
	a := newReal(t, false)
	fx := jststest.NewFixture(t, "", caseFiles(map[string]string{"index.js": fixClean}), domain.Scope{})
	res := jststest.RunTask(t, a, fx.Job, "eslint", &executor.Executor{})
	jststest.Capture(t, "clean", res)
	doc, _ := jststest.ReadEnvelope(t, fx.Job, res)
	check := res.Executions[len(res.Executions)-1]
	if res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != obsdoc.ExitSuccessWithoutFindings || len(doc.Observations) != 0 ||
		!slices.Equal(doc.Coverage.ProcessedPaths, []string{"index.js"}) || *check.Result.ExitCode != 0 {
		t.Fatalf("clean %s %+v %+v", res.Status, doc.Outcome, doc.Coverage)
	}
}

// A file ESLint cannot parse is a fatal message (exit 1): the task fails
// with parse_errors, the other file's observations are kept (partial parse)
// and no file is claimed as processed.
func TestRealESLintParseError(t *testing.T) {
	a := newReal(t, false)
	fx := jststest.NewFixture(t, "", caseFiles(map[string]string{"index.js": fixBroken, "ok.js": "const unused = 1;\n"}), domain.Scope{})
	res := jststest.RunTask(t, a, fx.Job, "eslint", &executor.Executor{})
	jststest.Capture(t, "parse-error", res)
	doc, _ := jststest.ReadEnvelope(t, fx.Job, res)
	check := res.Executions[len(res.Executions)-1]
	if res.Status != domain.TaskFailed || res.Reason != ReasonParseErrors || !doc.Outcome.PartialParse || *check.Result.ExitCode != 1 {
		t.Fatalf("parse error %s/%s %+v", res.Status, res.Reason, doc.Outcome)
	}
	if len(doc.ToolErrors) != 1 || doc.ToolErrors[0].Kind != "parse" || *doc.ToolErrors[0].Path != "index.js" || jststest.Rules(doc)["no-unused-vars"] != 1 ||
		doc.Coverage.ProcessedPaths != nil || doc.Coverage.Units[FileParseError] != 1 {
		t.Fatalf("tool errors %+v coverage %+v", doc.ToolErrors, doc.Coverage)
	}
}

// TC-113 (docs/ANALYZERS.md#규칙-문맥과-소스-안-억제, per family on the real tool): eslint-disable
// comments, inline rule configuration and a repository
// eslint-suppressions.json are all turned off by --no-inline-config and the
// empty managed suppressions file. Every problem is an active observation,
// suppressedMessages stays empty and the rule pack records disabled.
func TestRealESLintSuppressionsDisabled(t *testing.T) {
	a := newReal(t, false)
	fx := jststest.NewFixture(t, "", caseFiles(map[string]string{"index.js": fixSup, "eslint-suppressions.json": fixSupFile}), domain.Scope{})
	res := jststest.RunTask(t, a, fx.Job, "eslint", &executor.Executor{})
	jststest.Capture(t, "suppressions", res)
	doc, _ := jststest.ReadEnvelope(t, fx.Job, res)
	rules := jststest.Rules(doc)
	if res.Status != domain.TaskSucceeded || rules["no-empty"] != 1 || rules["no-unused-vars"] != 1 || rules["no-debugger"] != 1 {
		t.Fatalf("suppressions %s/%s rules %v errors %+v", res.Status, res.Reason, rules, doc.ToolErrors)
	}
	if doc.RulePack.SourceSuppressions != obsdoc.SuppressDisabled || !slices.Contains(doc.RulePack.Selection, "--no-inline-config") {
		t.Fatalf("rule pack %+v", doc.RulePack)
	}
	check := res.Executions[len(res.Executions)-1]
	if strings.Contains(string(check.Result.Stdout.Data), "\"suppressedMessages\":[{") || len(check.Result.Stderr.Data) != 0 {
		t.Fatalf("eslint suppressed or warned: %s %s", check.Result.Stdout.Data, check.Result.Stderr.Data)
	}
}

// TC-112: a TS module whose import is not installed (a snapshot never holds
// node_modules). The baseline needs no types and succeeds; the type-aware
// task fails with dependency_unavailable, records the gap as a dependency
// tool error and withholds every type-aware diagnostic.
func TestRealESLintTypedDependencyGap(t *testing.T) {
	base, typed := newReal(t, false), newReal(t, true)
	fx := jststest.NewFixture(t, "", caseFiles(map[string]string{"tsconfig.json": fixGapTSConfig, "src/main.ts": fixGapMain}), domain.Scope{})
	res := jststest.RunTask(t, base, fx.Job, "eslint", &executor.Executor{})
	doc, _ := jststest.ReadEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != obsdoc.ExitSuccessWithoutFindings {
		t.Fatalf("baseline %s/%s %+v %+v", res.Status, res.Reason, doc.Outcome, doc.Observations)
	}
	res = jststest.RunTask(t, typed, fx.Job, "eslint-typed", &executor.Executor{})
	jststest.Capture(t, "typed-gap", res)
	doc, _ = jststest.ReadEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskFailed || res.Reason != ReasonDependency || doc.Outcome.ExitClass != obsdoc.ExitConfigurationError || len(doc.Observations) != 0 {
		t.Fatalf("typed gap %s/%s %+v %+v", res.Status, res.Reason, doc.Outcome, doc.Observations)
	}
	kinds := map[string]int{}
	for _, te := range doc.ToolErrors {
		kinds[te.Kind]++
		if te.Kind == "dependency" && !strings.HasPrefix(te.Message, "TS2307: ") {
			t.Fatalf("dependency error %+v", te)
		}
	}
	if kinds["dependency"] != 1 || kinds["withheld"] == 0 || !slices.Contains(doc.Limitations, "diagnostics_withheld_incomplete_types") || doc.Coverage.ProcessedPaths != nil {
		t.Fatalf("tool errors %v %+v limitations %v", kinds, doc.ToolErrors, doc.Limitations)
	}
}

// tsconfig plugins and repository node_modules: a module (or an ancestor)
// holding node_modules is refused before ESLint runs, because module
// resolution would feed repository dependency code into the program.
func TestRealESLintTypedRefusesNodeModules(t *testing.T) {
	typed := newReal(t, true)
	fx := jststest.NewFixture(t, "", caseFiles(map[string]string{"tsconfig.json": fixGapTSConfig, "src/main.ts": "export const one = 1;\n"}), domain.Scope{})
	jststest.WriteTree(t, fx.Job.Snapshot.WorkspaceDir, map[string]string{"node_modules/zod/package.json": "{\"name\":\"zod\"}\n"})
	runner := &jststest.MutatingRunner{Inner: &executor.Executor{}}
	res := jststest.RunTask(t, typed, fx.Job, "eslint-typed", runner)
	if res.Status != domain.TaskSkippedPolicy || res.Reason != ReasonForeignNodeModules || len(runner.Specs) != 0 {
		t.Fatalf("node_modules %s/%s specs %d", res.Status, res.Reason, len(runner.Specs))
	}
	jststest.ReadEnvelope(t, fx.Job, res)
}

// Evidence for docs/ANALYZERS.md#저장소-코드와-tsconfig-검사: typescript-eslint's parserOptions.project
// program does not load tsconfig language-service plugins. The module holds
// a plugin that would leave a marker; ESLint runs directly (outside the
// adapter, whose node_modules refusal would stop it) with the managed
// type-aware configuration.
func TestRealESLintTypedDoesNotLoadTSConfigPlugins(t *testing.T) {
	typed := newReal(t, true)
	dir := t.TempDir()
	jststest.WriteTree(t, dir, map[string]string{
		"package.json":  pkgJSON,
		"tsconfig.json": "{\"compilerOptions\":{\"strict\":true,\"plugins\":[{\"name\":\"marker-plugin\"}]},\"include\":[\"src\"]}\n",
		"src/main.ts":   "export const one: number = 1;\n",
		"node_modules/marker-plugin/package.json": "{\"name\":\"marker-plugin\",\"main\":\"index.js\"}\n",
		"node_modules/marker-plugin/index.js":     "require(\"node:fs\").writeFileSync(require(\"node:path\").join(__dirname, \"LOADED\"), \"x\");\nmodule.exports = () => ({ create: (info) => info.languageService });\n",
	})
	cfgDir := t.TempDir()
	cfg := filepath.Join(cfgDir, "eslint.config.cjs")
	sup := filepath.Join(cfgDir, "eslint-suppressions.json")
	mod := jsts.Module{Dir: "."}
	if err := os.WriteFile(cfg, []byte(typedConfig(typed.plugins, mod, dir)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sup, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := (&executor.Executor{}).Run(context.Background(), executor.Spec{Executable: typed.script.Node.Executable, Args: typed.script.Argv(CheckArgs(cfg, sup, []string{"."})...),
		WorkspaceRoot: dir, Dir: dir, TempParent: cfgDir, Env: typed.script.Env(), Timeout: time.Minute, StdoutLimit: 1 << 20, StderrLimit: 1 << 20})
	if res.ExitCode == nil || *res.ExitCode != 0 {
		t.Fatalf("eslint %s %v %s %s", res.Status, res.ExitCode, res.Stdout.Data, res.Stderr.Data)
	}
	if _, err := os.Stat(filepath.Join(dir, "node_modules", "marker-plugin", "LOADED")); err == nil {
		t.Fatal("typescript-eslint loaded a tsconfig plugin")
	}
}

// TC-08 on real bytes: truncated or garbled stdout and a crash exit are
// failures that keep what was parsed; none of them reads as zero findings.
func TestRealESLintDamagedOutput(t *testing.T) {
	a := newReal(t, false)
	isCheck := func(s executor.Spec) bool { return slices.Contains(s.Args, "--format") }
	cases := []struct {
		name, reason string
		mutate       func(r *executor.Result)
		partial      bool
	}{
		{"truncated", ReasonOutputTruncated, func(r *executor.Result) {
			r.Stdout.Data = r.Stdout.Data[:len(r.Stdout.Data)/2]
			r.Stdout.Truncated = true
		}, true},
		{"garbled", ReasonOutputUnparsed, func(r *executor.Result) {
			r.Stdout.Data = append(r.Stdout.Data, []byte("{\"filePath\":")...)
			r.Stdout.TotalBytes = int64(len(r.Stdout.Data))
		}, true},
		{"crash", ReasonFailed, func(r *executor.Result) {
			two := 2
			r.ExitCode = &two
			r.Stdout.Data, r.Stdout.TotalBytes = nil, 0
			r.Stderr.Data = []byte("\nOops! Something went wrong! :(\n\nESLint: 10.15.0\n\nboom\n")
			r.Stderr.TotalBytes = int64(len(r.Stderr.Data))
		}, false},
		{"exit-zero-with-errors", ReasonExitMismatch, func(r *executor.Result) { zero := 0; r.ExitCode = &zero }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fx := jststest.NewFixture(t, "", caseFiles(map[string]string{"index.js": "const unused = 1;\n"}), domain.Scope{})
			runner := &jststest.MutatingRunner{Inner: &executor.Executor{}, Mutate: func(s executor.Spec, r *executor.Result) {
				if isCheck(s) {
					c.mutate(r)
				}
			}}
			res := jststest.RunTask(t, a, fx.Job, "eslint", runner)
			doc, _ := jststest.ReadEnvelope(t, fx.Job, res)
			if res.Status != domain.TaskFailed || res.Reason != c.reason || doc.Outcome.PartialParse != c.partial || doc.Parse.OK || doc.Coverage.ProcessedPaths != nil {
				t.Fatalf("%s: %s/%s %+v parse %+v", c.name, res.Status, res.Reason, doc.Outcome, doc.Parse)
			}
		})
	}
}

func TestRealESLintCancelAndTimeout(t *testing.T) {
	a := newReal(t, false)
	fx := jststest.NewFixture(t, "", caseFiles(map[string]string{"index.js": fixClean}), domain.Scope{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := jststest.RunTaskCtx(t, ctx, a, fx.Job, "eslint", &executor.Executor{})
	doc, _ := jststest.ReadEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskCancelled || doc.Outcome.ExitClass != obsdoc.ExitCancelled {
		t.Fatalf("cancelled %s %+v", res.Status, doc.Outcome)
	}
	short, err := New(Config{Script: a.cfg.Script, Node: a.cfg.Node, Timeout: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	fx2 := jststest.NewFixture(t, "", caseFiles(map[string]string{"index.js": fixClean}), domain.Scope{})
	res = jststest.RunTask(t, short, fx2.Job, "eslint", &executor.Executor{})
	doc, _ = jststest.ReadEnvelope(t, fx2.Job, res)
	if res.Status != domain.TaskTimedOut || doc.Outcome.ExitClass != obsdoc.ExitTimeout {
		t.Fatalf("timeout %s %+v", res.Status, doc.Outcome)
	}
	for _, e := range res.Executions {
		if e.Result.Cleanup != executor.CleanupConfirmed && e.Result.Cleanup != executor.CleanupNotStarted {
			t.Fatalf("cleanup %+v", e.Result)
		}
	}
}

// Type-aware program diagnostics on real tools: TypeScript 6 deprecations
// are not errors of the program (limitation only); an unresolved package
// extends is a configuration error that withholds every diagnostic.
func TestRealESLintTypedProgramDiagnostics(t *testing.T) {
	typed := newReal(t, true)
	main := "export async function later(): Promise<number> {\n  return 1;\n}\n"
	for _, c := range []struct {
		name, config string
		status       domain.TaskStatus
		reason, lim  string
	}{
		{"deprecated", "{\"compilerOptions\":{\"target\":\"ES2022\",\"module\":\"commonjs\",\"moduleResolution\":\"node\",\"baseUrl\":\".\",\"lib\":[\"ES2022\"],\"strict\":true},\"include\":[\"src\"]}\n",
			domain.TaskSucceeded, "", "typescript_deprecations_ignored"},
		{"package-extends", "{\"extends\":\"@tsconfig/strictest/tsconfig.json\",\"compilerOptions\":{\"lib\":[\"ES2022\"]},\"include\":[\"src\"]}\n",
			domain.TaskFailed, ReasonConfig, "diagnostics_withheld_incomplete_types"},
	} {
		fx := jststest.NewFixture(t, "", caseFiles(map[string]string{"tsconfig.json": c.config, "src/main.ts": main}), domain.Scope{})
		res := jststest.RunTask(t, typed, fx.Job, "eslint-typed", &executor.Executor{})
		jststest.Capture(t, "typed-"+c.name, res)
		doc, _ := jststest.ReadEnvelope(t, fx.Job, res)
		if res.Status != c.status || res.Reason != c.reason || !slices.Contains(doc.Limitations, c.lim) {
			t.Fatalf("%s: %s/%s %v %+v %+v", c.name, res.Status, res.Reason, doc.Limitations, doc.ToolErrors, doc.Observations)
		}
	}
}
