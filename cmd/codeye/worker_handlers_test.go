package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
)

// The production deep-review handler is discovery → the analyzer composite
// (Go analysis, structure metrics, and secrets screening) → review roles.
func TestWorkerHandlersComposeDiscovery(t *testing.T) {
	exe := "go"
	if runtime.GOOS == "windows" {
		exe = "go.exe"
	}
	goExe := filepath.Join(runtime.GOROOT(), "bin", exe)
	if _, err := os.Stat(goExe); err != nil {
		t.Skip("go toolchain not found at GOROOT/bin")
	}
	hs, missing, err := workerHandlers(context.Background(), &workerproto.Hello{GoExecutable: goExe, ScratchRoot: t.TempDir()}, &executor.Executor{})
	if err != nil || len(hs) != 2 || strings.Join(missing, ",") != "staticcheck:not_configured,errcheck:not_configured,cppcheck:not_configured,clang-tidy:not_configured,gcc-analyzer:not_configured,ruff:not_configured,mypy:not_configured,pyright:not_configured,bandit:not_configured,import-linter:not_configured,eslint:not_configured,tsc:not_configured,eslint-typed:not_configured,dependency-cruiser:not_configured,cargo-check:not_configured,clippy:not_configured,cargo-doc:not_configured,pmd:not_configured,spotbugs:not_configured,"+structureNotConfigured {
		t.Fatalf("handlers %d missing %v err %v", len(hs), missing, err)
	}
	if info := hs[0].Info(); info.Name != "analyzers" || info.Capability != "analysis:deep-review" {
		t.Fatalf("identity %+v", info)
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module x\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "x_test.go"), []byte("package x\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snap, err := source.AcquireLocal(context.Background(), src, ".", t.TempDir(), source.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := hs[0].Plan(context.Background(), analysis.Job{Scope: domain.Scope{Kind: "full"}, Snapshot: snap})
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for _, p := range plan {
		if strings.HasPrefix(p.Key, "review.investigate.s") || strings.HasPrefix(p.Key, "review.verify.s") {
			continue
		}
		keys = append(keys, p.Key+"="+string(p.Stage))
		if (p.Key == "staticcheck" || p.Key == "gofmt" || p.Key == "errcheck") && p.Required {
			t.Fatalf("%s must be optional", p.Key)
		}
	}
	want := []string{"env.discover=environment", "docs.discover=documents", "go-vet=static_analysis", "staticcheck=static_analysis", "gofmt=static_analysis", "errcheck=static_analysis", "lizard=static_analysis", "jscpd=static_analysis", "gitleaks=static_analysis", "review.environment=environment", "review.documents=documents", "review.investigate=code_review", "review.interpret=code_review", "review.verify=code_review", "review.triage.t001=code_review", "review.verify.t001=code_review", "review.triage.t002=code_review", "review.verify.t002=code_review", "review.scope=code_review"}
	if len(keys) != len(want) {
		t.Fatalf("plan %v", keys)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("plan %v, want %v", keys, want)
		}
	}
	if hs[1].Info().Capability != "analysis:with-tests" {
		t.Fatalf("with-tests capability %+v", hs[1].Info())
	}
	withTests, err := hs[1].Plan(context.Background(), analysis.Job{Profile: "with-tests", Scope: domain.Scope{Kind: "full"}, Snapshot: snap})
	if err != nil || len(withTests) != len(plan)+1 || withTests[len(plan)].Key != "test.go" || withTests[len(plan)].Stage != domain.StageTest || !withTests[len(plan)].Required {
		t.Fatalf("with-tests plan %v, error %v", withTests, err)
	}
	reviewed, err := hs[1].Plan(context.Background(), analysis.Job{Profile: "with-tests", AllowModelReview: true, Scope: domain.Scope{Kind: "full"}, Snapshot: snap})
	var order []string
	for _, stage := range reviewed {
		if stage.Key == "test.go" || stage.Key == "review.verify" || stage.Key == "review.interpret" {
			order = append(order, stage.Key)
		}
	}
	if err != nil || !slices.Equal(order, []string{"review.interpret", "test.go", "review.verify"}) {
		t.Fatalf("review with tests order %v, error %v", order, err)
	}

}

// The worker composes discovery → go vet → the five review roles; a call
// ceiling out of range is refused, never replaced by the default.
func TestWorkerHandlersComposeReview(t *testing.T) {
	goExe := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goExe += ".exe"
	}
	if _, err := os.Stat(goExe); err != nil {
		t.Skip("go toolchain not found at GOROOT/bin")
	}
	for _, n := range []int{14, 2014, -1} {
		if _, _, err := workerHandlers(context.Background(), &workerproto.Hello{GoExecutable: goExe, ScratchRoot: t.TempDir(), ReviewMaxCalls: n}, &executor.Executor{}); err == nil {
			t.Fatalf("call ceiling %d accepted", n)
		}
	}
	hs, _, err := workerHandlers(context.Background(), &workerproto.Hello{GoExecutable: goExe, ScratchRoot: t.TempDir(), ReviewMaxCalls: 600}, &executor.Executor{})
	if err != nil || len(hs) != 2 {
		t.Fatal(err)
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module x\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snap, err := source.AcquireLocal(context.Background(), src, ".", t.TempDir(), source.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := hs[0].Plan(context.Background(), analysis.Job{Scope: domain.Scope{Kind: "full"}, Snapshot: snap})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, p := range plan {
		keys = append(keys, p.Key+"="+string(p.Stage))
	}
	want := "env.discover=environment docs.discover=documents go-vet=static_analysis staticcheck=static_analysis errcheck=static_analysis gitleaks=static_analysis review.environment=environment review.documents=documents review.investigate=code_review review.interpret=code_review review.verify=code_review"
	if strings.Join(keys, " ") != want {
		t.Fatalf("plan %v", keys)
	}
}

// The shared node toolchain (--node) travels in the hello's analyzer_tools
// under its reserved name: it is probed at startup, never an unknown tool.
// With CODEYE_TEST_NODE the real node passes and adds no missing code.
func TestWorkerHandlersKnowTheNodeToolchain(t *testing.T) {
	exe := "go"
	if runtime.GOOS == "windows" {
		exe = "go.exe"
	}
	goExe := filepath.Join(runtime.GOROOT(), "bin", exe)
	if _, err := os.Stat(goExe); err != nil {
		t.Skip("go toolchain not found at GOROOT/bin")
	}
	node, want := filepath.Join(t.TempDir(), "absent-node"), "node:invalid_config,staticcheck:not_configured,errcheck:not_configured,cppcheck:not_configured,clang-tidy:not_configured,gcc-analyzer:not_configured,ruff:not_configured,mypy:not_configured,pyright:not_configured,bandit:not_configured,import-linter:not_configured,eslint:not_configured,tsc:not_configured,eslint-typed:not_configured,dependency-cruiser:not_configured,cargo-check:not_configured,clippy:not_configured,cargo-doc:not_configured,pmd:not_configured,spotbugs:not_configured,"+structureNotConfigured
	if p := os.Getenv("CODEYE_TEST_NODE"); p != "" {
		node, want = p, "staticcheck:not_configured,errcheck:not_configured,cppcheck:not_configured,clang-tidy:not_configured,gcc-analyzer:not_configured,ruff:not_configured,mypy:not_configured,pyright:not_configured,bandit:not_configured,import-linter:not_configured,eslint:not_configured,tsc:not_configured,eslint-typed:not_configured,dependency-cruiser:not_configured,cargo-check:not_configured,clippy:not_configured,cargo-doc:not_configured,pmd:not_configured,spotbugs:not_configured,"+structureNotConfigured
	}
	hs, missing, err := workerHandlers(context.Background(), &workerproto.Hello{GoExecutable: goExe, ScratchRoot: t.TempDir(), AnalyzerTools: map[string]string{"node": node}}, &executor.Executor{})
	if err != nil || len(hs) != 2 || strings.Join(missing, ",") != want {
		t.Fatalf("handlers %d missing %v err %v", len(hs), missing, err)
	}
}

// The Java baseline registers the normal handler on a Java-only node. It
// uses the same probes, snapshot and runner that a web request uses.
func TestWorkerHandlersRunJavaProject(t *testing.T) {
	java, pmd, spotbugs := os.Getenv("CODEYE_TEST_JAVA"), os.Getenv("CODEYE_TEST_PMD"), os.Getenv("CODEYE_TEST_SPOTBUGS")
	if java == "" || pmd == "" || spotbugs == "" {
		t.Skip("configure the Java runtime and analyzer JARs")
	}
	ctx := context.Background()
	runner := &executor.Executor{}
	hs, _, err := workerHandlers(ctx, &workerproto.Hello{ScratchRoot: t.TempDir(),
		AnalyzerTools: map[string]string{"java": java, "pmd": pmd, "spotbugs": spotbugs}}, runner)
	if err != nil || len(hs) != 2 {
		t.Fatalf("Java-only worker handlers: %v, %v", len(hs), err)
	}
	fx := packtest.NewFixture(t, "", map[string]string{"src/main/java/example/Example.java": `package example;
public final class Example {
  public int run() {
    int neverUsed = 1;
    String input = null;
    return input.length();
  }
}
`, "pom.xml": "<project><properties><maven.compiler.release>17</maven.compiler.release></properties></project>"}, domain.Scope{})
	fx.Job.RunID, fx.Job.AttemptID = domain.NewID(), domain.NewID()
	plan, err := hs[0].Plan(ctx, fx.Job)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, task := range plan {
		if task.Key != "env.discover" && task.Key != "pmd" && task.Key != "spotbugs" {
			continue
		}
		seen[task.Key] = true
		result := hs[0].Run(ctx, fx.Job, task, runner)
		if result.Status != domain.TaskSucceeded || len(result.Artifacts) == 0 {
			t.Fatalf("%s: %+v", task.Key, result)
		}
		if task.Key != "env.discover" {
			doc, _ := packtest.ReadEnvelope(t, fx.Job, result, "CODEYE_JAVA_DUMP")
			if doc.Analyzer.Ecosystem != "java" || len(doc.Observations) == 0 {
				t.Fatalf("%s: %+v", task.Key, doc)
			}
		}
	}
	if len(seen) != 3 {
		t.Fatalf("missing Java analysis tasks %v", plan)
	}
}

func TestWorkerHandlersRunDependencyContracts(t *testing.T) {
	node, cruiser, importLinter := os.Getenv("CODEYE_TEST_NODE"), os.Getenv("CODEYE_TEST_DEPENDENCY_CRUISER"), os.Getenv("CODEYE_TEST_IMPORT_LINTER")
	if node == "" || cruiser == "" || importLinter == "" {
		t.Skip("configure Node, dependency-cruiser and Import Linter")
	}
	ctx := context.Background()
	runner := &executor.Executor{}
	hs, _, err := workerHandlers(ctx, &workerproto.Hello{ScratchRoot: t.TempDir(), AdvertiseWithoutBaseline: true,
		AnalyzerTools: map[string]string{"node": node, "dependency-cruiser": cruiser, "import-linter": importLinter}}, runner)
	if err != nil || len(hs) != 2 {
		t.Fatalf("architecture handlers %d, error %v", len(hs), err)
	}
	fx := packtest.NewFixture(t, "", map[string]string{
		"a.js": `import "./b.js";`, "b.js": `import "./a.js";`,
		"pkg/__init__.py": "", "pkg/a.py": "import pkg.b\n", "pkg/b.py": "",
		".importlinter": "[importlinter]\nroot_package=pkg\n[importlinter:contract:no-link]\nname=No link\ntype=forbidden\nsource_modules=pkg.a\nforbidden_modules=pkg.b\n",
	}, domain.Scope{})
	fx.Job.RunID, fx.Job.AttemptID = domain.NewID(), domain.NewID()
	plan, err := hs[0].Plan(ctx, fx.Job)
	if err != nil {
		t.Fatal(err)
	}
	remaining := map[string]string{"dependency-cruiser": "no-circular-dependencies", "import-linter": "no-link"}
	for _, task := range plan {
		rule, match := remaining[task.Key]
		if !match {
			continue
		}
		result := hs[0].Run(ctx, fx.Job, task, runner)
		if result.Status != domain.TaskSucceeded || task.Required {
			t.Fatalf("%s: %+v", task.Key, result)
		}
		doc, _ := packtest.ReadEnvelope(t, fx.Job, result, "CODEYE_ARCHITECTURE_DUMP")
		if packtest.Rules(doc)[rule] == 0 {
			t.Fatalf("%s: no source evidence for %s", task.Key, rule)
		}
		delete(remaining, task.Key)
	}
	if len(remaining) != 0 {
		t.Fatalf("missing dependency contract tasks %v", remaining)
	}
}

// The C/C++ compile DB generation sits after discovery and before the
// analyzers in both the deep-review and with-tests chains, and is not
// planned when no C/C++ analyzer can run.
func TestWorkerHandlersPlanCompileDBGeneration(t *testing.T) {
	files := map[string]string{"CMakeLists.txt": "project(x C)\nadd_library(x x.c)\n", "x.c": "int x(void) { return 0; }\n"}
	keysOf := func(t *testing.T, h analysis.Handler) []string {
		t.Helper()
		fx := packtest.NewFixture(t, "", files, domain.Scope{})
		plan, err := h.Plan(context.Background(), fx.Job)
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		for _, p := range plan {
			keys = append(keys, p.Key)
		}
		return keys
	}
	hs, _, err := workerHandlers(context.Background(), &workerproto.Hello{ScratchRoot: t.TempDir(), AdvertiseWithoutBaseline: true}, &executor.Executor{})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hs {
		if keys := keysOf(t, h); slices.Contains(keys, "compile-db") {
			t.Fatalf("%s: generation planned without a usable C/C++ analyzer: %v", h.Info().Capability, keys)
		}
	}
	cppcheck := os.Getenv("CODEYE_TEST_CPPCHECK")
	if cppcheck == "" {
		t.Skip("set CODEYE_TEST_CPPCHECK for the planned chain")
	}
	hs, _, err = workerHandlers(context.Background(), &workerproto.Hello{ScratchRoot: t.TempDir(), AnalyzerTools: map[string]string{"cppcheck": cppcheck}}, &executor.Executor{})
	if err != nil || len(hs) != 2 {
		t.Fatalf("handlers %d %v", len(hs), err)
	}
	for _, h := range hs {
		keys := keysOf(t, h)
		gen, check, env := slices.Index(keys, "compile-db"), slices.Index(keys, "cppcheck"), slices.Index(keys, "env.discover")
		if gen < 0 || check < gen || env > gen {
			t.Fatalf("%s: plan order %v", h.Info().Capability, keys)
		}
	}
}
