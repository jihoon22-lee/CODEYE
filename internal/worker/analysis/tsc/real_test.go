package tsc

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts/jststest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Real tsc tests run only with CODEYE_TEST_TSC and CODEYE_TEST_NODE (CI has
// no Node tools). CODEYE_W18_CAPTURE=DIR also writes the redacted tsc.check
// (and tsc.config) streams and exit code of each case to DIR, which is how
// testdata/*.stdout|stderr|exit were captured (docs/ANALYZERS.md#실제-도구-시험과-출력-캡처):
//
//	CODEYE_W18_CAPTURE=$PWD/internal/worker/analysis/tsc/testdata \
//	  go test -count=1 -run TestRealTsc ./internal/worker/analysis/tsc/

const (
	pkgJSON = "{\"name\":\"w18-case\",\"private\":true}\n"
	// base compiler options of the case fixtures (no automatic @types: TS 6
	// defaults types to []).
	opts = "\"target\":\"ES2022\",\"module\":\"ESNext\",\"moduleResolution\":\"Bundler\",\"lib\":[\"ES2022\"],\"strict\":true"
	// incremental and composite would write tsbuildinfo even with --noEmit
	// (measured); the adapter's --incremental false --composite false keep
	// the workspace unchanged.
	fixCleanConfig = "{\"compilerOptions\":{" + opts + ",\"incremental\":true,\"composite\":true,\"tsBuildInfoFile\":\"./build/info.tsbuildinfo\"},\"include\":[\"src\"]}\n"
	fixClean       = "export function add(a: number, b: number): number {\n  return a + b;\n}\n"
	fixConfig      = "{\"compilerOptions\":{" + opts + "},\"include\":[\"src\"]}\n"
	// a missing package, a callback whose parameter type follows from it and
	// an independent type error: all withheld in a gap run (docs/ANALYZERS.md#의존성-공백).
	fixGap    = "import { z } from \"zod\";\n\nexport const schema = z.object({ name: z.string() });\n\nexport const names = schema.parse({}).items.map((x) => x.name);\n\nexport const n: number = \"x\";\n"
	fixSyntax = "export const = 1;\n"
	// solution-style config (files: [] plus references), the Vite layout.
	fixSolution    = "{\"files\":[],\"references\":[{\"path\":\"./tsconfig.app.json\"},{\"path\":\"./tsconfig.node.json\"}]}\n"
	fixSolutionApp = "{\"compilerOptions\":{" + opts + ",\"composite\":true},\"include\":[\"src\"]}\n"
	fixSolutionNod = "{\"compilerOptions\":{" + opts + ",\"composite\":true},\"include\":[\"vite.config.ts\"]}\n"
	fixTypeErr     = "export const total: number = \"3\";\n"
	fixPackageExt  = "{\"extends\":\"@tsconfig/strictest/tsconfig.json\",\"compilerOptions\":{" + opts + "},\"include\":[\"src\"]}\n"
	fixNoInputs    = "{\"compilerOptions\":{" + opts + "},\"include\":[\"nothing\"]}\n"
	fixDeprecated  = "{\"compilerOptions\":{\"target\":\"ES2022\",\"module\":\"commonjs\",\"moduleResolution\":\"node\",\"lib\":[\"ES2022\"],\"strict\":true,\"baseUrl\":\".\"},\"include\":[\"src\"]}\n"
)

func caseFiles(extra map[string]string) map[string]string {
	out := map[string]string{"package.json": pkgJSON}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// TC-111: tsc checks the TS module of the monorepo with its tsconfig chain
// (JSONC, an extends above the module, a plugin entry it never loads):
// @ts-expect-error is honored, the file outside the program is not
// claimed, the workspace gains no file and no node path is recorded.
func TestRealTscMonorepo(t *testing.T) {
	a := newReal(t)
	fx := jststest.NewFixture(t, filepath.Join(t.TempDir(), "공백 & (dir)"), jststest.MonorepoFiles(t, 4), domain.Scope{})
	before, ws := jststest.TreeHash(t, fx.SourceRoot), jststest.TreeHash(t, fx.Job.Snapshot.WorkspaceDir)
	tasks, err := a.Plan(context.Background(), fx.Job)
	if err != nil || len(tasks) != 1 || tasks[0].Spec.Key != "tsc.m01" || tasks[0].Spec.Required {
		t.Fatalf("plan %v %+v", err, tasks)
	}
	res := jststest.RunTask(t, a, fx.Job, "tsc.m01", &executor.Executor{})
	jststest.Capture(t, "monorepo", res)
	doc, raw := jststest.ReadEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != obsdoc.ExitSuccessWithFindings || doc.Outcome.PartialParse {
		t.Fatalf("%s/%s %+v errors %+v", res.Status, res.Reason, doc.Outcome, doc.ToolErrors)
	}
	names := []string{}
	for _, e := range res.Executions {
		names = append(names, e.Name)
	}
	check := res.Executions[len(res.Executions)-1]
	if !slices.Equal(names, []string{execVersion, execConfig, execCheck}) || *check.Result.ExitCode != 2 || len(check.Result.Stderr.Data) != 0 {
		t.Fatalf("executions %v %+v", names, check.Result)
	}
	if len(doc.Observations) != 1 || doc.Observations[0].RuleID != "TS2322" || *doc.Observations[0].Path != "packages/app/src/index.ts" ||
		doc.Observations[0].Location.StartLine != 28 || doc.Observations[0].Location.ColumnUnit != "byte" {
		t.Fatalf("observations %+v", doc.Observations)
	}
	if !slices.Equal(doc.Coverage.ProcessedPaths, []string{"packages/app/src/index.ts", "packages/app/src/suppressed.ts", "packages/app/src/util.ts"}) ||
		doc.Coverage.FilesInScope == nil || *doc.Coverage.FilesInScope != 4 {
		t.Fatalf("coverage %+v", doc.Coverage)
	}
	var cfgs []string
	for _, c := range doc.RulePack.ConfigFiles {
		cfgs = append(cfgs, c.Path)
	}
	if doc.RulePack.RepositoryConfig != obsdoc.RepoConfigHonored || doc.RulePack.SourceSuppressions != obsdoc.SuppressHonored ||
		!slices.Equal(cfgs, []string{"packages/app/tsconfig.json", "tsconfig.base.json"}) {
		t.Fatalf("rule pack %+v", doc.RulePack)
	}
	if doc.Module.Name != "@w18/app" || doc.Target.Properties["strict"] != "true" || doc.Target.Properties["module_resolution"] != "bundler" || !doc.Tool.VersionTested {
		t.Fatalf("module/target %+v %+v %+v", doc.Module, doc.Target, doc.Tool)
	}
	jststest.AssertNoNodePaths(t, raw, nodePaths(a, fx)...)
	if jststest.TreeHash(t, fx.SourceRoot) != before || jststest.TreeHash(t, fx.Job.Snapshot.WorkspaceDir) != ws {
		t.Fatal("source or workspace changed")
	}
}

// incremental/composite/tsBuildInfoFile in the repository config would make
// tsc write a tsbuildinfo even with --noEmit; the adapter's overrides keep
// the workspace unchanged. A clean program exits 0.
func TestRealTscCleanWritesNothing(t *testing.T) {
	a := newReal(t)
	fx := jststest.NewFixture(t, "", caseFiles(map[string]string{"tsconfig.json": fixCleanConfig, "src/a.ts": fixClean}), domain.Scope{})
	ws := jststest.TreeHash(t, fx.Job.Snapshot.WorkspaceDir)
	res := jststest.RunTask(t, a, fx.Job, "tsc", &executor.Executor{})
	jststest.Capture(t, "clean", res)
	doc, _ := jststest.ReadEnvelope(t, fx.Job, res)
	check := res.Executions[len(res.Executions)-1]
	if res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != obsdoc.ExitSuccessWithoutFindings || *check.Result.ExitCode != 0 ||
		!slices.Equal(doc.Coverage.ProcessedPaths, []string{"src/a.ts"}) {
		t.Fatalf("clean %s/%s %+v %+v", res.Status, res.Reason, doc.Outcome, doc.Coverage)
	}
	if jststest.TreeHash(t, fx.Job.Snapshot.WorkspaceDir) != ws {
		t.Fatal("tsc wrote into the workspace")
	}
}

// TC-112: an unresolved package is a coverage gap: the task fails with
// dependency_unavailable and every diagnostic (the implicit any that
// follows from the missing types and the independent type error alike) is
// withheld as a tool error, never an observation.
func TestRealTscDependencyGap(t *testing.T) {
	a := newReal(t)
	fx := jststest.NewFixture(t, "", caseFiles(map[string]string{"tsconfig.json": fixConfig, "src/main.ts": fixGap}), domain.Scope{})
	res := jststest.RunTask(t, a, fx.Job, "tsc", &executor.Executor{})
	jststest.Capture(t, "gap", res)
	doc, _ := jststest.ReadEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskFailed || res.Reason != ReasonDependency || doc.Outcome.ExitClass != obsdoc.ExitConfigurationError || len(doc.Observations) != 0 {
		t.Fatalf("gap %s/%s %+v %+v", res.Status, res.Reason, doc.Outcome, doc.Observations)
	}
	kinds := map[string]int{}
	for _, te := range doc.ToolErrors {
		kinds[te.Kind]++
	}
	if kinds["dependency"] != 1 || kinds["withheld"] < 1 || doc.Coverage.ProcessedPaths != nil || doc.Coverage.Units[FileGap] != 1 {
		t.Fatalf("tool errors %v %+v coverage %+v", kinds, doc.ToolErrors, doc.Coverage)
	}
}

// Syntax errors stop tsc before semantic checking (measured): the task
// fails, and the syntax diagnostics are kept as tool errors.
func TestRealTscSyntaxError(t *testing.T) {
	a := newReal(t)
	fx := jststest.NewFixture(t, "", caseFiles(map[string]string{"tsconfig.json": fixConfig, "src/bad.ts": fixSyntax, "src/ok.ts": fixTypeErr}), domain.Scope{})
	res := jststest.RunTask(t, a, fx.Job, "tsc", &executor.Executor{})
	jststest.Capture(t, "syntax", res)
	doc, _ := jststest.ReadEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskFailed || res.Reason != ReasonSyntax || doc.Coverage.ProcessedPaths != nil {
		t.Fatalf("syntax %s/%s %+v %+v errors %+v", res.Status, res.Reason, doc.Outcome, doc.Observations, doc.ToolErrors)
	}
	if len(doc.ToolErrors) == 0 || doc.ToolErrors[0].Kind != "syntax" {
		t.Fatalf("tool errors %+v", doc.ToolErrors)
	}
}

// A solution-style config checks nothing itself (exit 0, measured): the
// adapter checks each local referenced project and records each run.
func TestRealTscSolutionConfig(t *testing.T) {
	a := newReal(t)
	fx := jststest.NewFixture(t, "", caseFiles(map[string]string{"tsconfig.json": fixSolution, "tsconfig.app.json": fixSolutionApp, "tsconfig.node.json": fixSolutionNod,
		"src/main.ts": fixTypeErr, "vite.config.ts": fixClean}), domain.Scope{})
	res := jststest.RunTask(t, a, fx.Job, "tsc", &executor.Executor{})
	jststest.Capture(t, "solution", res)
	doc, _ := jststest.ReadEnvelope(t, fx.Job, res)
	names := []string{}
	for _, e := range res.Executions {
		names = append(names, e.Name)
	}
	want := []string{execVersion, execConfig, execConfig + ".r01", execCheck + ".r01", execConfig + ".r02", execCheck + ".r02"}
	if res.Status != domain.TaskSucceeded || !slices.Equal(names, want) || len(doc.Observations) != 1 || *doc.Observations[0].Path != "src/main.ts" {
		t.Fatalf("solution %s/%s %v %+v %+v", res.Status, res.Reason, names, doc.Observations, doc.ToolErrors)
	}
	if !slices.Equal(doc.Coverage.ProcessedPaths, []string{"src/main.ts", "vite.config.ts"}) || len(doc.RulePack.ConfigFiles) != 3 ||
		!slices.Contains(doc.Limitations, "solution_config_references_checked") {
		t.Fatalf("coverage %+v rule pack %+v limitations %v", doc.Coverage, doc.RulePack, doc.Limitations)
	}
}

// Configuration failures: a package extends that cannot resolve (TS6053)
// and an include matching nothing (TS18003). Neither is a clean result.
func TestRealTscConfigurationFailures(t *testing.T) {
	a := newReal(t)
	for _, c := range []struct {
		name, config string
		status       domain.TaskStatus
		reason       string
	}{
		{"extends", fixPackageExt, domain.TaskFailed, ReasonExtendsUnresolved},
		{"no-inputs", fixNoInputs, domain.TaskUnsupported, ReasonNoInputs},
	} {
		fx := jststest.NewFixture(t, "", caseFiles(map[string]string{"tsconfig.json": c.config, "src/a.ts": fixClean}), domain.Scope{})
		res := jststest.RunTask(t, a, fx.Job, "tsc", &executor.Executor{})
		jststest.Capture(t, c.name, res)
		doc, _ := jststest.ReadEnvelope(t, fx.Job, res)
		if res.Status != c.status || res.Reason != c.reason || len(doc.Observations) != 0 || len(doc.Coverage.ProcessedPaths) != 0 {
			t.Fatalf("%s: %s/%s %+v %+v", c.name, res.Status, res.Reason, doc.Outcome, doc.ToolErrors)
		}
	}
}

// TypeScript 6 deprecations (moduleResolution node10, baseUrl) would stop
// the check at the options stage; --ignoreDeprecations 6.0 lets it run.
func TestRealTscDeprecatedOptionsStillCheck(t *testing.T) {
	a := newReal(t)
	fx := jststest.NewFixture(t, "", caseFiles(map[string]string{"tsconfig.json": fixDeprecated, "src/a.ts": fixTypeErr}), domain.Scope{})
	res := jststest.RunTask(t, a, fx.Job, "tsc", &executor.Executor{})
	jststest.Capture(t, "deprecated", res)
	doc, _ := jststest.ReadEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskSucceeded || len(doc.Observations) != 1 || doc.Observations[0].RuleID != "TS2322" {
		t.Fatalf("deprecated %s/%s %+v %+v", res.Status, res.Reason, doc.Observations, doc.ToolErrors)
	}
}

// A tsconfig extends leaving the snapshot and a node_modules on the module's
// path are refused before tsc runs.
func TestRealTscPolicyRefusals(t *testing.T) {
	a := newReal(t)
	fx := jststest.NewFixture(t, "", caseFiles(map[string]string{"tsconfig.json": "{\"extends\":\"../outside.json\"}\n", "src/a.ts": fixClean}), domain.Scope{})
	runner := &jststest.MutatingRunner{Inner: &executor.Executor{}}
	res := jststest.RunTask(t, a, fx.Job, "tsc", runner)
	jststest.ReadEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskSkippedPolicy || res.Reason != ReasonConfigOutside || len(runner.Specs) != 0 {
		t.Fatalf("outside %s/%s specs %d", res.Status, res.Reason, len(runner.Specs))
	}
	fx = jststest.NewFixture(t, "", caseFiles(map[string]string{"tsconfig.json": fixConfig, "src/a.ts": fixClean}), domain.Scope{})
	jststest.WriteTree(t, fx.Job.Snapshot.WorkspaceDir, map[string]string{"node_modules/zod/package.json": "{\"name\":\"zod\"}\n"})
	res = jststest.RunTask(t, a, fx.Job, "tsc", runner)
	jststest.ReadEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskSkippedPolicy || res.Reason != ReasonForeignNodeModules || len(runner.Specs) != 0 {
		t.Fatalf("node_modules %s/%s specs %d", res.Status, res.Reason, len(runner.Specs))
	}
}

// Evidence for docs/ANALYZERS.md#저장소-코드와-tsconfig-검사: tsc does not load tsconfig language-service
// plugins (they belong to tsserver). tsc runs directly on a project whose
// node_modules holds a plugin that would leave a marker.
func TestRealTscDoesNotLoadTSConfigPlugins(t *testing.T) {
	a := newReal(t)
	dir := t.TempDir()
	jststest.WriteTree(t, dir, map[string]string{
		"tsconfig.json": "{\"compilerOptions\":{" + opts + ",\"plugins\":[{\"name\":\"marker-plugin\"}]},\"include\":[\"src\"]}\n",
		"src/a.ts":      fixClean,
		"node_modules/marker-plugin/package.json": "{\"name\":\"marker-plugin\",\"main\":\"index.js\"}\n",
		"node_modules/marker-plugin/index.js":     "require(\"node:fs\").writeFileSync(require(\"node:path\").join(__dirname, \"LOADED\"), \"x\");\nmodule.exports = () => ({ create: (info) => info.languageService });\n",
	})
	res := (&executor.Executor{}).Run(context.Background(), executor.Spec{Executable: a.script.Node.Executable, Args: a.script.Argv(CheckArgs("tsconfig.json")...),
		WorkspaceRoot: dir, Dir: dir, TempParent: t.TempDir(), Env: a.script.Env(), Timeout: time.Minute, StdoutLimit: 1 << 20, StderrLimit: 1 << 20})
	if res.ExitCode == nil || *res.ExitCode != 0 {
		t.Fatalf("tsc %s %v %s", res.Status, res.ExitCode, res.Stdout.Data)
	}
	if _, err := os.Stat(filepath.Join(dir, "node_modules", "marker-plugin", "LOADED")); err == nil {
		t.Fatal("tsc loaded a tsconfig plugin")
	}
}

// TC-08 on real bytes: truncated or garbled stdout and an unexpected exit
// are failures; none of them reads as zero findings.
func TestRealTscDamagedOutput(t *testing.T) {
	a := newReal(t)
	isCheck := func(s executor.Spec) bool {
		return !slices.Contains(s.Args, "--showConfig") && !slices.Contains(s.Args, "--version")
	}
	cases := []struct {
		name, reason string
		mutate       func(r *executor.Result)
	}{
		{"truncated", ReasonOutputTruncated, func(r *executor.Result) {
			r.Stdout.Data = r.Stdout.Data[:len(r.Stdout.Data)/2]
			r.Stdout.Truncated = true
		}},
		{"garbled", ReasonOutputUnparsed, func(r *executor.Result) {
			r.Stdout.Data = append(r.Stdout.Data, []byte("not a diagnostic\n")...)
			r.Stdout.TotalBytes = int64(len(r.Stdout.Data))
		}},
		{"crash", ReasonFailed, func(r *executor.Result) {
			seven := 7
			r.ExitCode = &seven
		}},
		{"exit-zero-with-records", ReasonExitMismatch, func(r *executor.Result) { zero := 0; r.ExitCode = &zero }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fx := jststest.NewFixture(t, "", caseFiles(map[string]string{"tsconfig.json": fixConfig, "src/a.ts": fixTypeErr}), domain.Scope{})
			runner := &jststest.MutatingRunner{Inner: &executor.Executor{}, Mutate: func(s executor.Spec, r *executor.Result) {
				if isCheck(s) {
					c.mutate(r)
				}
			}}
			res := jststest.RunTask(t, a, fx.Job, "tsc", runner)
			doc, _ := jststest.ReadEnvelope(t, fx.Job, res)
			if res.Status != domain.TaskFailed || res.Reason != c.reason || doc.Parse.OK || doc.Coverage.ProcessedPaths != nil {
				t.Fatalf("%s: %s/%s %+v parse %+v", c.name, res.Status, res.Reason, doc.Outcome, doc.Parse)
			}
		})
	}
}

func TestRealTscCancelAndTimeout(t *testing.T) {
	a := newReal(t)
	fx := jststest.NewFixture(t, "", caseFiles(map[string]string{"tsconfig.json": fixConfig, "src/a.ts": fixClean}), domain.Scope{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := jststest.RunTaskCtx(t, ctx, a, fx.Job, "tsc", &executor.Executor{})
	doc, _ := jststest.ReadEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskCancelled || doc.Outcome.ExitClass != obsdoc.ExitCancelled {
		t.Fatalf("cancelled %s %+v", res.Status, doc.Outcome)
	}
	short, err := New(Config{Script: a.cfg.Script, Node: a.cfg.Node, Timeout: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	fx2 := jststest.NewFixture(t, "", caseFiles(map[string]string{"tsconfig.json": fixConfig, "src/a.ts": fixClean}), domain.Scope{})
	res = jststest.RunTask(t, short, fx2.Job, "tsc", &executor.Executor{})
	doc, _ = jststest.ReadEnvelope(t, fx2.Job, res)
	if res.Status != domain.TaskTimedOut || doc.Outcome.ExitClass != obsdoc.ExitTimeout {
		t.Fatalf("timeout %s %+v", res.Status, doc.Outcome)
	}
	for _, e := range res.Executions {
		if strings.Contains(string(e.Result.Cleanup), "uncertain") {
			t.Fatalf("cleanup %+v", e.Result)
		}
	}
}
